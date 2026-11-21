// managed_lease.go —— 受管租约会话（issue #265）。
//
// # 它补的是哪一段
//
// 受管执行器的四步编排（Claim → 施加 → Checkpoint → Complete）此前全程
// 没有续期：ClaimTTL 被迫覆盖「整个任务做完」，长任务（LLM 推理、工具
// 调用、远端阻塞都可能超过 TTL）必然撞上「执行到一半租约过期 ⇒ 被接管
// ⇒ 旧持有者还在写」的错位。本文件把「认领之后、收场之前」这段的持有
// 资格收成一个**会话**：执行期间续期、关键步之前确认所有权、易主即刻
// 封口在途执行。
//
// # 会话不是第二份租约状态
//
// 会话持有的唯一凭据（Token / Fence）出自任务板认领那次原子操作——
// 与 @@taskboard.Board@@ 的「令牌由存储铸造」同源，本包不铸造、不缓存
// 第二份真相。续期走 @@RenewingBoard.Renew@@（资格校验 + 推后到期，
// 存储侧原子），确认所有权走同一个原语：会话只是把「什么时候调、调完
// 怎么处置」固化下来，不复制任何判定。
//
// # 续期节拍（#266 的门）
//
// 续期按固定节拍跑在独立协程上（默认 TTL/3）：一拍失败不致命——节拍
// 远短于 TTL，下一拍仍有出路；撑满 TTL 仍失败 ⇒ 任务可被接管，接管后
// 的下一拍必然 @@ErrLeaseLost@@ ⇒ 会话封口。调度策略（按到期时刻动态
// 安排、抖动、退避）是 #266 的车道：本会话提供的是「会话存续期间持有
// 资格有人管」的机制面，节拍经 @@LeaseSessionConfig.RenewInterval@@
// 显式可换。
//
// # 持续故障的本地失格（不依赖存储回包）
//
// 「撑满 TTL 后的下一拍必然 ErrLeaseLost」有一个隐含前提：**那一拍必须
// 重新得到存储响应**。持续网络分区下这个前提不成立——续期错误被当成
// 抖动无限重试，而存储侧租约早已过期、任务已被接管，旧持有者却仍在
// 执行业务动作（双执行者窗口）。
//
// 故每拍除看回包外还看**本地资格截止**：最近一次续期成功时刻 +
// TTL（减 safety margin，默认 TTL/10——吸收本地与存储时钟的传输
// 漂移）。超过截止仍未确认持有权 ⇒ 主动失格、取消执行 ctx：本地截止
// 点只负责**提前撤销执行资格**，不能用于授予新租约（授予权永远属于
// 权威存储）。
// # 易主即封口
//
// 任一续期被 @@ErrLeaseLost@@ 拒（或 @@AssertOwned@@ 资格确认失败）：
// 会话记下失因、取消 @@Context@@——在途的施加段随之取消（施加之前封口，
// 与 worker.Loop 在途取消同一纪律）。之后的一切资格确认快速失败，
// 不再碰一条已经死了的租约。
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// LeaseSession 一次任务认领的租约会话（issue #265）。
//
// 生命周期：认领成功后由 @@NewLeaseSession@@ 开启；调用方**必须**在收场
// 后 @@Close@@（释放续期协程，不释放租约——租约归还/收口是编排面的处置，
// 见 ManagedExecutor）。Context 在会话关闭或租约易主时取消。
//
// # 契约
//
//   - Token / Fence 是认领那次原子操作铸造的凭据（只读投影，不另铸）；
//   - Context() 返回的 ctx 在租约易主或 Close 后取消——长动作应拿它执行，
//     易主时施加段被取消（封口）；
//   - AssertOwned 是**资格确认 + 续期**（不是心跳）：存储侧校验令牌仍是
//     最新代并推后到期；被 @@taskboard.ErrLeaseLost@@ 拒即失格（此后恒败）；
//   - Close 停止续期并等待协程退出；**不**归还租约（归还归编排面——
//     收口与释放的处置各不相同，会话不替调用方选）。
type LeaseSession interface {
	// Context 会话作用域的 ctx：租约易主或 Close 后取消。
	Context() context.Context
	// Token 本次认领的租约令牌（Complete / Release 的唯一凭据）。
	Token() string
	// Fence 本次认领的接管代次（下游栅栏判据的出示值）。
	Fence() uint64
	// AssertOwned 确认持有资格并推后到期（Renew）。失格后恒返回
	// ErrLeaseLost 链（快速失败，不重试已死的租约）。
	AssertOwned(ctx context.Context) error
	// Close 停止续期协程并取消会话 ctx（幂等；不归还租约）。
	Close(ctx context.Context) error
}

// LeaseSessionConfig 会话配置。
type LeaseSessionConfig struct {
	// TTL 每次续期申请的租约有效期（必填，>0）——与认领时同一口径，
	// 由存储按自己的时钟推算。
	TTL time.Duration
	// RenewInterval 续期节拍（零值 = TTL/3）。
	//
	// 必须短于 TTL：节拍是「资格容忍窗」里的巡逻频率，等于或超过 TTL
	// 意味着两次巡逻之间租约必然过期——那不是续期，是补票。显式给了
	// 非正值以外的配置时按装配期错误拒绝（ErrInvalidConfig）。
	//
	// 调度策略的正式化（按到期时刻动态安排、抖动、退避）是 #266 的车道；
	// 本字段是换装点，不是最终形态。
	RenewInterval time.Duration
	// Now 本地单调时钟注入点（持续故障失格判定的唯一时钟源）。
	// 零值 = time.Now；测试注入假时钟，否则「超过 TTL 未确认」场景
	// 恒不触发、判据退化成空断言。
	Now func() time.Time
}

// leaseSession LeaseSession 的机制面实现（每次认领一个实例）。
type leaseSession struct {
	board taskboard.RenewingBoard
	id    taskboard.TaskID
	token string
	fence taskboard.Fence
	ttl   time.Duration
	every time.Duration
	now   func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu          sync.Mutex
	lost        error     // 首个失格原因（失格后恒败——已死的租约不再重试）
	confirmedAt time.Time // 最近一次续期成功的本地时刻（资格截止的锚）
}

// NewLeaseSession 为一次认领开启租约会话：立刻起续期协程，直到 Close。
//
// 会话 ctx 派生自传入的 ctx：外层取消（停机）时在途执行随之封口——
// 与租约易主的封口同一通道（Context），两个失格源不设第二套机制。
//
// task 必须携带认领铸造的租约（Lease.Token 为空 ⇒ ErrInvalidConfig——
// 没有凭据的会话是摆设，静默开启等于假装持有）。
func NewLeaseSession(ctx context.Context, board taskboard.RenewingBoard, task taskboard.Task, cfg LeaseSessionConfig) (LeaseSession, error) {
	switch {
	case board == nil:
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 租约会话缺任务板（续期无处落地）")
	case task.Lease.Token == "":
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 租约会话缺认领凭据（令牌由存储铸造，会话不另造）")
	case cfg.TTL <= 0:
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 租约会话的 TTL 必须为正")
	}
	every := cfg.RenewInterval
	if every == 0 {
		every = cfg.TTL / 3
	}
	if every <= 0 || every >= cfg.TTL {
		return nil, errors.Wrap(ErrInvalidConfig,
			"worker: 续期节拍必须短于 TTL（节拍是资格容忍窗里的巡逻频率，不是补票）",
			"ttl", cfg.TTL.String(), "renewInterval", every.String())
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	sctx, cancel := context.WithCancel(ctx)
	s := &leaseSession{
		board:  board,
		id:     task.ID,
		token:  task.Lease.Token,
		fence:  task.Lease.Fence,
		ttl:    cfg.TTL,
		every:  every,
		now:    now,
		ctx:    sctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	// 认领是最近一次资格确认：资格截止从这一刻起算。
	s.confirmedAt = now()
	// 走 async.GoSafe 而非裸 go（本仓门禁：panic 无法被外层 recover）。
	async.GoSafe(s.renewLoop)
	return s, nil
}

// renewLoop 续期协程：按节拍 Renew，直到会话 ctx 取消。
//
//   - 成功 → 记录本地确认时刻，下一拍；
//   - ErrLeaseLost → 失格：记因 + 取消会话 ctx（在途施加段封口）+ 退出；
//   - 其它错误 → **先查本地资格截止**再决定重试：超过「最近确认 + TTL
//   - margin」仍未确认持有权 ⇒ 主动失格（存储可能已把任务判给别人，
//     不能再等业务动作继续）；未超过 ⇒ 存储抖动，下一拍重试。
func (s *leaseSession) renewLoop() {
	defer close(s.done)
	ticker := time.NewTicker(s.every)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		_, err := s.board.Renew(s.ctx, s.id, s.token, taskboard.ClaimRequest{TTL: s.ttl})
		switch {
		case err == nil:
			s.mu.Lock()
			s.confirmedAt = s.now()
			s.mu.Unlock()
			continue
		case errors.Is(err, taskboard.ErrLeaseLost):
			s.lose(errors.Wrap(err, "worker: 租约续期被拒（任务已归新持有者或已过期）", "task", string(s.id)))
			return
		default:
			if s.ctx.Err() != nil {
				return
			}
			if s.pastDeadline() {
				s.lose(errors.Wrap(taskboard.ErrLeaseLost,
					"worker: 超过本地资格截止仍未确认持有权（持续存储故障），主动撤销执行资格",
					"task", string(s.id), "ttl", s.ttl.String()))
				return
			}
			// 存储抖动：下一拍重试（未过本地资格截止前，自愈语义仍成立）。
		}
	}
}

// pastDeadline 本地资格截止判定：最近一次续期成功时刻 + TTL - margin
// （margin = TTL/10，吸收本地与存储时钟的传输漂移）。只读 confirmedAt，
// 调用方决定处置——本方法不持锁（confirmedAt 的单次读竞态无害：判定的
// 两端都是保守方向，多读一次少读一次不改变「必然失格」的结论）。
func (s *leaseSession) pastDeadline() bool {
	s.mu.Lock()
	confirmedAt := s.confirmedAt
	s.mu.Unlock()
	margin := s.ttl / 10
	return !s.now().Before(confirmedAt.Add(s.ttl - margin))
}

// lose 记失格原因并封口（幂等：首个原因为准）。
func (s *leaseSession) lose(err error) {
	s.mu.Lock()
	if s.lost == nil {
		s.lost = err
	}
	s.mu.Unlock()
	s.cancel()
}

func (s *leaseSession) Context() context.Context { return s.ctx }

func (s *leaseSession) Token() string { return s.token }

func (s *leaseSession) Fence() uint64 { return uint64(s.fence) }

func (s *leaseSession) AssertOwned(ctx context.Context) error {
	s.mu.Lock()
	lost := s.lost
	s.mu.Unlock()
	if lost != nil {
		return lost
	}
	if _, err := s.board.Renew(ctx, s.id, s.token, taskboard.ClaimRequest{TTL: s.ttl}); err != nil {
		if errors.Is(err, taskboard.ErrLeaseLost) {
			s.lose(errors.Wrap(err, "worker: 持有资格确认被拒（任务已归新持有者或已过期）", "task", string(s.id)))
			s.mu.Lock()
			lost = s.lost
			s.mu.Unlock()
			return lost
		}
		// 非失格错误（存储抖动）：如实上抛，不断言死亡——下一拍续期自愈。
		return errors.Wrap(err, "worker: 持有资格确认失败（存储侧不可达）", "task", string(s.id))
	}
	return nil
}

func (s *leaseSession) Close(context.Context) error {
	s.cancel()
	<-s.done // 等续期协程退出：Close 返回后不再有后台续期在跑
	return nil
}

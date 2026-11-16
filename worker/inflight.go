// inflight.go —— 在途认领集合、续期判定与调度时刻（issue #196/#266/#326）。
//
// 从 loop.go 拆出（结构阈值按判据的一侧切）：驱动生命周期（注册→领取→
// 执行→排水→注销）与「一条认领怎么续期、怎么与收场互斥」是两段可读的
// 判据，分开后两边都能在单文件内看完。
package worker

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// claimed 一次在途认领（领取环、执行协程与心跳协程的共享面）。
type claimed struct {
	id     taskboard.TaskID
	token  string
	cancel context.CancelFunc // 租约易主时取消在途执行

	// mu 串行化「本任务的续期」与「收场开始」：收场（Complete/Release）
	// 一旦开始，心跳不得再对本任务 Renew——否则会出现「Complete 已成功、
	// 随后的 Renew 却报 ErrLeaseLost」的假事件（续期读到的已是终态）。
	// 这是并发池新增的共享面（issue #196 的「事件串行化」）。
	mu            sync.Mutex
	finalizing    bool      // 收场已开始（含 panic 兜底路径）
	renewFails    int       // 连续的非超时续期失败（心跳协程在 mu 下读写）
	renewTimeouts int       // 连续的续期超时（与普通失败分开计数，issue #266）
	nextRenew     time.Time // 本地时钟下的下次续期时刻（零值 = 立即到期）
}

// scheduleRenew 安排下一次续期（本地时钟；TTL 是调用方与存储的共同刻度，
// 不与存储的绝对 Until 比较——两边时钟可能对不上）。
func (c *claimed) scheduleRenew(d time.Duration) {
	c.mu.Lock()
	c.nextRenew = time.Now().Add(d)
	c.mu.Unlock()
}

// nextRenewAt 读下次续期时刻（mu 下读；调度扫描用）。
func (c *claimed) nextRenewAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextRenew
}

// renewVerdict 一次续期判定的结论（锁内只判定，后续动作一律锁外执行）。
type renewVerdict int

const (
	// renewOK 续期成功（连续失败计数清零）。
	renewOK renewVerdict = iota
	// renewSkipped 本任务已进入收场，心跳跳过（不得再 Renew）。
	renewSkipped
	// renewRetry 非资格类失败，计数递增但未到阈值（下一拍重试）。
	renewRetry
	// renewTakenOver 租约已易主（ErrLeaseLost）：取消在途执行并留痕。
	renewTakenOver
	// renewUnconfirmed 连续失败（含超时）达到阈值：无法确认持有资格。
	renewUnconfirmed
)

// noRenewAfterLeaseLost 已判易主/无法确认后的调度占位：任务即将被取消并
// 移出集合，不再重复发起续期（避免每个心跳拍都刷一遍 OnLeaseLost）。
const noRenewAfterLeaseLost = time.Hour

// renew 在「本任务尚未进入收场」时执行一次续期判定。
//
// 只有**判定与计数**在 mu 内：续期调用本身与本任务的收场互斥（issue #196
// 的事件串行化），但通知/取消等宿主可见动作由调用方在 mu **外**执行。
// 此前整个闭包（含宿主 OnLeaseLost 回调）都在锁内，回调回查本包即二次
// 抢锁死锁、panic 则掀翻心跳协程（issue #326）。
//
// timeout > 0 时单条续期有独立的截止时间（issue #266）：超时按「超时」
// 单独计数（renewTimeouts），不吞进普通失败；成功并重排下次续期。
// renewAfter 是成功后的续期间隔（TTL − 安全余量），retryAfter 是失败后的
// 重试间隔（心跳节拍）。
func (c *claimed) renew(
	ctx context.Context,
	timeout time.Duration,
	renew func(context.Context) (taskboard.Task, error),
	renewAfter, retryAfter time.Duration,
	failMax int,
) renewVerdict {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finalizing {
		return renewSkipped
	}

	rctx := ctx
	cancel := func() {}
	if timeout > 0 {
		rctx, cancel = context.WithTimeout(ctx, timeout)
	}
	_, err := renew(rctx)
	timedOut := rctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded)
	cancel()

	return c.recordRenewLocked(err, timedOut, ctx.Err() != nil, renewAfter, retryAfter, failMax)
}

// recordRenewLocked 记录一次续期结果并给出结论（持 mu 调用）。
func (c *claimed) recordRenewLocked(
	err error, timedOut, outerCanceled bool,
	renewAfter, retryAfter time.Duration,
	failMax int,
) renewVerdict {
	switch {
	case err == nil:
		c.renewFails, c.renewTimeouts = 0, 0
		c.nextRenew = time.Now().Add(renewAfter)
		return renewOK
	case errors.Is(err, taskboard.ErrLeaseLost):
		c.renewFails, c.renewTimeouts = 0, 0
		c.nextRenew = time.Now().Add(noRenewAfterLeaseLost)
		return renewTakenOver
	default:
		if timedOut {
			c.renewTimeouts++
		} else {
			c.renewFails++
		}
		if outerCanceled {
			c.nextRenew = time.Time{}
			return renewRetry // 外层停机：不把抖动升级成「资格无法确认」
		}
		if c.renewFails+c.renewTimeouts >= failMax {
			c.nextRenew = time.Now().Add(noRenewAfterLeaseLost)
			return renewUnconfirmed
		}
		if retryAfter <= 0 {
			retryAfter = time.Millisecond // 直构测试的兜底，生产恒为正
		}
		c.nextRenew = time.Now().Add(retryAfter)
		return renewRetry
	}
}

// beginFinalize 标记本任务进入收场（幂等）。与 renew 互斥：
// 返回时不会再有本任务的 Renew 在途。
func (c *claimed) beginFinalize() {
	c.mu.Lock()
	c.finalizing = true
	c.mu.Unlock()
}

// inflight 一次 Run 的在途认领集合（并发池与心跳协程的共享面）。
//
// 串行路径（Concurrency=1）里集合最多一条，语义与旧的 `current *claimed`
// 单槽逐字一致；并发路径里每个任务一条，心跳按快照逐条续期。
type inflight struct {
	mu    sync.Mutex
	tasks map[taskboard.TaskID]*claimed
	// wake 在途集合新增时唤醒心跳重排续期时刻（缓冲 1：多次新增合并为一次
	// 重排；issue #266）。
	wake chan struct{}
}

func newInflight() *inflight {
	return &inflight{
		tasks: map[taskboard.TaskID]*claimed{},
		wake:  make(chan struct{}, 1),
	}
}

func (s *inflight) add(c *claimed) {
	s.mu.Lock()
	s.tasks[c.id] = c
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// remove 只删仍是这条认领的条目：任务释放后可能被再次认领，旧执行体的
// 收场延迟不得把新认领从集合里删掉。
func (s *inflight) remove(id taskboard.TaskID, c *claimed) {
	s.mu.Lock()
	if s.tasks[id] == c {
		delete(s.tasks, id)
	}
	s.mu.Unlock()
}

// snapshot 取当前全部在途（按 ID 排序：续期顺序确定，不依赖 map 迭代序）。
func (s *inflight) snapshot() []*claimed {
	s.mu.Lock()
	out := make([]*claimed, 0, len(s.tasks))
	for _, c := range s.tasks {
		out = append(out, c)
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(x, y *claimed) int {
		return strings.Compare(string(x.id), string(y.id))
	})
	return out
}

// due 取「到点该续期」的在途任务（按 nextRenew 与本地时钟判定）。
func (s *inflight) due(now time.Time) []*claimed {
	var out []*claimed
	for _, c := range s.snapshot() {
		if !c.nextRenewAt().After(now) {
			out = append(out, c)
		}
	}
	return out
}

// nextRenewDelay 距离最近一次到期续期还有多久（无在途任务时返回 idle
// 占位值；新增任务经 wake 通道重排，不靠这个占位值兜住）。
func (s *inflight) nextRenewDelay(now time.Time) time.Duration {
	const idle = time.Hour
	next := time.Time{}
	for _, c := range s.snapshot() {
		at := c.nextRenewAt()
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if next.IsZero() {
		return idle
	}
	if d := next.Sub(now); d > 0 {
		return d
	}
	return 0
}

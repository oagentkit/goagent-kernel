// managed_lease_test.go —— 受管执行器的租约会话判据（issue #265）。
//
// 核心情景：租约过期 ⇒ 新持有者接管 ⇒ 旧持有者的迟到提交（进度 / 收口）
// 必须被拒，且不得覆盖新持有者的状态。时钟用注入的测试时钟确定性推进，
// 不靠 sleep 撞时序；续期防止接管的端到端场景用真实时间（独立用例）。
package worker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// newRealtimeBoard 真实时间的任务板（不注入时钟）：续期防接管 / Close 后
// 到期的判据是**真实时间**上的过期——注入时钟冻结在初始时刻时，租约永远
// 不会过期，接管尝试的 ErrTaken 只是「永远持有」的假阳性。
func newRealtimeBoard() *coord.BoardAdapter {
	b, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		panic("worker: 测试装配 BoardAdapter 失败: " + err.Error())
	}
	return b
}

// renewalEvent 一次成功续期的事实。
type renewalEvent struct {
	at    time.Time // 续期判定时刻（由板返回的到期时刻回推，不含调用耗时）
	until time.Time // 本次续期后的租约到期时刻
}

// renewObserverBoard 测试侧的任务板包装器：包一层 *coord.BoardAdapter，
// 覆写 Renew 与 Claim，让「成功续期的时刻」与「认领时刻」对用例可见。
//
// # 为什么接缝只在测试侧
//
// 内核的续期节拍对调用方不可见（会话自管），而「续期防接管」这条判据
// **必须**证明「租约刚被续过、剩余有效期是一整个 TTL」——不观测续期就只能
// 回到「按墙钟排程去撞窗口」，那正是该用例原先的脆点（issue #430）。
// 生产代码不因这条判据加接缝。
type renewObserverBoard struct {
	*coord.BoardAdapter

	mu       sync.Mutex
	claimAt  time.Time
	renewals []renewalEvent
	changed  chan struct{}
}

// newRenewObserverBoard 构造观测板（包住一块真实时间的任务板）。
func newRenewObserverBoard(inner *coord.BoardAdapter) *renewObserverBoard {
	return &renewObserverBoard{BoardAdapter: inner, changed: make(chan struct{}, 1)}
}

// Claim 认领（覆写）：记下**首次成功认领**的时刻——接管尝试的计时基准。
func (b *renewObserverBoard) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	task, err := b.BoardAdapter.Claim(ctx, id, agent, req)
	if err == nil {
		b.mu.Lock()
		if b.claimAt.IsZero() {
			b.claimAt = time.Now()
		}
		b.mu.Unlock()
	}
	return task, err
}

// Renew 续期（覆写）：成功时记下「判定时刻」与「续期后的到期时刻」。
//
// 判定时刻由板返回的到期时刻回推（到期时刻 = 判定时刻 + TTL），不用调用
// 之后的墙钟——后者会把「判定到返回」这段耗时算进租约余量里（偏乐观）。
func (b *renewObserverBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	task, err := b.BoardAdapter.Renew(ctx, id, token, req)
	if err != nil {
		return task, err
	}
	at := time.Now()
	if req.TTL > 0 && !task.Lease.Until.IsZero() {
		at = task.Lease.Until.Add(-req.TTL)
	}
	b.mu.Lock()
	b.renewals = append(b.renewals, renewalEvent{at: at, until: task.Lease.Until})
	b.mu.Unlock()
	select {
	case b.changed <- struct{}{}:
	default:
	}
	return task, nil
}

// claimTime 首次成功认领的时刻。
func (b *renewObserverBoard) claimTime() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.claimAt
}

// nextRenewalAfter 等到「晚于 after」的一次成功续期。
//
// 等的是**条件**不是时长：条件一旦成立立刻返回（不多等）。abort 非空时，
// 执行器提前收场（租约易主 / 施加失败）立即中止等待——不让用例挂在超时上。
func (b *renewObserverBoard) nextRenewalAfter(ctx context.Context, after time.Time, timeout time.Duration, abort <-chan struct{}) (renewalEvent, bool) {
	deadline := time.Now().Add(timeout)
	for {
		b.mu.Lock()
		var found renewalEvent
		ok := false
		for _, ev := range b.renewals {
			if ev.at.After(after) {
				found, ok = ev, true
				break
			}
		}
		b.mu.Unlock()
		if ok {
			return found, true
		}
		if !time.Now().Before(deadline) {
			return renewalEvent{}, false
		}
		select {
		case <-b.changed:
		case <-abort:
			return renewalEvent{}, false
		case <-ctx.Done():
			return renewalEvent{}, false
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// diag 观测面摘要（失败时的诊断串）。
func (b *renewObserverBoard) diag() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	const layout = "15:04:05.000"
	if len(b.renewals) == 0 {
		return fmt.Sprintf("认领于 %v，成功续期 0 次", b.claimAt.Format(layout))
	}
	last := b.renewals[len(b.renewals)-1]
	return fmt.Sprintf("认领于 %v，成功续期 %d 次，最后一次判定于 %v（距认领 %v）",
		b.claimAt.Format(layout), len(b.renewals), last.at.Format(layout), last.at.Sub(b.claimAt))
}

// gatedEffects 控制施加时机的副作用编排面替身：Apply 在门上等测试放行，
// 让「旧持有者卡在施加段」与「时钟推进 + 新持有者接管」可以确定性交错。
type gatedEffects struct {
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	applied int
	lastAct sideeffect.Action
}

func (f *gatedEffects) ExecuteWith(_ context.Context, act sideeffect.Action, eff sideeffect.Effect) (sideeffect.Outcome, sideeffect.Result, error) {
	if eff.Apply == nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, nil
	}
	f.mu.Lock()
	f.lastAct = act
	f.mu.Unlock()
	f.entered <- struct{}{}
	res, err := eff.Apply(context.Background(), nil, sideeffect.Lease{Resource: act.Resource, Fence: 1})
	if err != nil {
		return sideeffect.OutcomeUncertain, sideeffect.Result{}, err
	}
	f.mu.Lock()
	f.applied++
	f.mu.Unlock()
	return sideeffect.OutcomeApplied, res, nil
}

func (f *gatedEffects) Reconcile(_ context.Context, _ sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	return sideeffect.OutcomeReplayed, res.Result, nil
}

// recordingProgress 记录全部进度提交（A / B 两个执行器共用一份，判
// 「谁提交的快照落了库」）。
type recordingProgress struct {
	mu      sync.Mutex
	commits []checkpoint.Snapshot
}

func (p *recordingProgress) Commit(_ context.Context, s checkpoint.Snapshot) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commits = append(p.commits, s.Clone())
	return nil
}

func (p *recordingProgress) by(tag string) []checkpoint.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []checkpoint.Snapshot
	for _, s := range p.commits {
		if s.Fields["by"] == tag {
			out = append(out, s)
		}
	}
	return out
}

// TestManagedExecutorLateCheckpointRejectedAfterTakeover
//
// 验收（issue #265）：执行超过 TTL 后，旧持有者的 Checkpoint 提交被拒；
// 旧持有者的 Complete 被拒（ErrLeaseLost）；且不覆盖新持有者的状态。
//
// 编排：A 认领后卡在施加段 ⇒ 时钟推过 TTL ⇒ B 接管并完整收场（含进度）
// ⇒ 放行 A ⇒ A 的迟到提交必须全部被拒。
//
// 反向验证锚点：修复前 A 的进度快照会**落库**（Progress.Commit 只管单调、
// 不绑定租约资格）——本用例在修复前必红（迟到快照存在）。
func TestLeaseSessionContextCancelsOnLeaseLost(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	task, err := board.Claim(context.Background(), "t1", "w-a", taskboard.ClaimRequest{TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewLeaseSession(context.Background(), board, task, LeaseSessionConfig{TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close(context.Background()) }()

	clock.Advance(2 * time.Second) // 租约过期（资格在到期时刻终止）
	if err := session.AssertOwned(context.Background()); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("过期租约的资格确认必须被拒（ErrLeaseLost）：%v", err)
	}
	select {
	case <-session.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("租约易主后会话 ctx 必须取消（在途执行施加之前封口）")
	}
	if err := session.AssertOwned(context.Background()); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("失去资格的会话必须快速失败（不重试已死的租约）：%v", err)
	}
	if session.Token() != task.Lease.Token {
		t.Fatalf("会话必须持有认领铸造的令牌：%s", session.Token())
	}
	if session.Fence() != uint64(task.Lease.Fence) {
		t.Fatalf("会话必须持有接管代次：%d", session.Fence())
	}
}

// TestLeaseSessionRenewsUntilClosed 会话存续期间租约持续有效（真实时间）；
// Close 后不再续期，租约到期后任务可被接管。
func TestLeaseSessionRenewsUntilClosed(t *testing.T) {
	ctx := context.Background()
	board := newRealtimeBoard()
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	task, err := board.Claim(ctx, "t1", "w-a", taskboard.ClaimRequest{TTL: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewLeaseSession(context.Background(), board, task, LeaseSessionConfig{TTL: 300 * time.Millisecond, RenewInterval: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := session.AssertOwned(ctx); err != nil {
			t.Fatalf("会话存续期间资格确认不得失败：%v", err)
		}
		time.Sleep(80 * time.Millisecond)
	}
	_ = session.Close(ctx)

	// Close 后不再续期：租约最迟在最后一次续期 + TTL 后到期。
	time.Sleep(700 * time.Millisecond)
	if _, err := board.Claim(ctx, "t1", "w-b", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatalf("Close 后租约应随 TTL 到期、任务可被接管：%v", err)
	}
}

// TestNewLeaseSessionValidatesConfig 装配期校验：缺板 / 缺凭据 / 非法 TTL / 非法节拍。
func TestNewLeaseSessionValidatesConfig(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	task, err := board.Claim(context.Background(), "t1", "w-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	bareTask := taskboard.Task{ID: "t1"} // 无租约（没有凭据的会话是摆设）

	cases := []struct {
		name  string
		board taskboard.RenewingBoard
		task  taskboard.Task
		cfg   LeaseSessionConfig
	}{
		{"缺任务板", nil, task, LeaseSessionConfig{TTL: time.Minute}},
		{"缺认领凭据", board, bareTask, LeaseSessionConfig{TTL: time.Minute}},
		{"零 TTL", board, task, LeaseSessionConfig{}},
		{"节拍不短于 TTL", board, task, LeaseSessionConfig{TTL: time.Minute, RenewInterval: time.Minute}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLeaseSession(context.Background(), tc.board, tc.task, tc.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("装配期应报 ErrInvalidConfig，得到 %v", err)
			}
		})
	}
}

// leaseStubBoard 确定性「认领 ⇒ 易主」世界：flipped 翻转前 A 的凭据有效，
// 翻转后 B 接管（A 的一切动作被 ErrLeaseLost 拒）。让「资格确认通过之后、
// 写入落地之前租约易主」的 check-then-act 窗口可以**确定性**重现——
// 真实板上这个窗口只有亚 TTL 的竞态，撞不出稳定用例。
type leaseStubBoard struct {
	aToken    string
	bToken    string
	mu        sync.Mutex
	flipped   bool
	completes []string // 成功收场的令牌（判「旧持有者不得收口」）
}

func (b *leaseStubBoard) List(context.Context) ([]taskboard.Task, error) { return nil, nil }

func (b *leaseStubBoard) Claim(context.Context, taskboard.TaskID, taskboard.AgentID, taskboard.ClaimRequest) (taskboard.Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.flipped {
		return taskboard.Task{ID: "t1", State: taskboard.StateClaimed, Owner: "w-a",
			Lease: taskboard.Lease{Token: b.aToken, Fence: 1, Until: time.Now().Add(time.Minute)}}, nil
	}
	return taskboard.Task{ID: "t1", State: taskboard.StateClaimed, Owner: "w-b",
		Lease: taskboard.Lease{Token: b.bToken, Fence: 2, Until: time.Now().Add(time.Minute)}}, nil
}

func (b *leaseStubBoard) Release(context.Context, taskboard.TaskID, string) error { return nil }

func (b *leaseStubBoard) Complete(_ context.Context, _ taskboard.TaskID, token string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur := b.currentLocked(); token != cur {
		return errors.Wrap(taskboard.ErrLeaseLost, "worker: 测试替身：任务已归新持有者")
	}
	b.completes = append(b.completes, token)
	return nil
}

func (b *leaseStubBoard) Renew(_ context.Context, _ taskboard.TaskID, token string, _ taskboard.ClaimRequest) (taskboard.Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if token != b.currentLocked() {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, "worker: 测试替身：租约已易主")
	}
	fence := taskboard.Fence(1)
	if b.flipped {
		fence = 2
	}
	return taskboard.Task{ID: "t1", State: taskboard.StateClaimed,
		Lease: taskboard.Lease{Token: token, Fence: fence, Until: time.Now().Add(time.Minute)}}, nil
}

func (b *leaseStubBoard) currentLocked() string {
	if b.flipped {
		return b.bToken
	}
	return b.aToken
}

// blockingLeasedProgress 真实 Committer 的阻塞包装：CommitFenced 在门上
// 等测试放行——把「确认通过之后」与「写入落地」之间撑出可编排的窗口。

// managed_lease_test.go —— 受管执行器的租约会话判据（issue #265）。
//
// 核心情景：租约过期 ⇒ 新持有者接管 ⇒ 旧持有者的迟到提交（进度 / 收口）
// 必须被拒，且不得覆盖新持有者的状态。时钟用注入的测试时钟确定性推进，
// 不靠 sleep 撞时序；续期防止接管的端到端场景用真实时间（独立用例）。
package worker

import (
	"context"
	"slices"
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
func TestManagedExecutorLateCheckpointRejectedAfterTakeover(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	board := newTestBoard(clock)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	effectsA := &gatedEffects{entered: make(chan struct{}, 1), release: make(chan struct{})}
	effectsB := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	progress := &recordingProgress{}
	execA, err := NewManagedExecutor(board, effectsA, ManagedConfig{Agent: "w-a", ClaimTTL: time.Second, Progress: progress})
	if err != nil {
		t.Fatal(err)
	}
	execB, err := NewManagedExecutor(board, effectsB, ManagedConfig{Agent: "w-b", ClaimTTL: time.Second, Progress: progress})
	if err != nil {
		t.Fatal(err)
	}

	workA := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
				<-effectsA.release // 卡在施加段：等测试完成「过期 + 接管」再放行
				return sideeffect.Result{Payload: []byte("a")}, nil
			},
		},
		Snapshot: func(task taskboard.Task, _ sideeffect.Result) (checkpoint.Snapshot, bool, error) {
			return checkpoint.Snapshot{RunID: "run-1", Seq: 3, Status: "running", Fields: map[string]string{"by": "a", "task": string(task.ID)}}, true, nil
		},
	}
	workB := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
				return sideeffect.Result{Payload: []byte("b")}, nil
			},
		},
		Snapshot: func(task taskboard.Task, _ sideeffect.Result) (checkpoint.Snapshot, bool, error) {
			return checkpoint.Snapshot{RunID: "run-1", Seq: 2, Status: "running", Fields: map[string]string{"by": "b", "task": string(task.ID)}}, true, nil
		},
	}

	type execResult struct {
		outcome sideeffect.Outcome
		err     error
	}
	resA := make(chan execResult, 1)
	go func() {
		o, e := execA.Execute(ctx, "t1", workA)
		resA <- execResult{o, e}
	}()
	<-effectsA.entered // A 已进入施加段

	clock.Advance(2 * time.Second) // A 的租约（TTL=1s）过期
	if _, err := execB.Execute(ctx, "t1", workB); err != nil {
		t.Fatalf("新持有者应能接管并完整收场：%v", err)
	}
	close(effectsA.release)

	ra := <-resA
	if !errors.Is(ra.err, taskboard.ErrLeaseLost) {
		t.Fatalf("旧持有者的收场必须被拒（ErrLeaseLost）：%v", ra.err)
	}

	if got := progress.by("a"); len(got) != 0 {
		t.Fatalf("旧持有者（租约已过期）的进度提交必须被拒，实得 %d 条落库快照: %+v", len(got), got)
	}
	if got := progress.by("b"); len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("新持有者的进度必须原样保留（不被旧持有者覆盖）：%+v", progress.by("b"))
	}
	tasks, _ := board.List(ctx)
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
		t.Fatalf("任务终态必须保持新持有者的收场结果：%+v", tasks)
	}
	if effectsA.applied != 1 {
		t.Fatalf("旧持有者的副作用已施加一次（记账在案），不得重跑：%d", effectsA.applied)
	}
}

// TestManagedExecutorRenewalPreventsTakeover
//
// 验收（issue #265）：续期期间任务不被他人接管（长任务场景端到端）。
// 真实时间推进：TTL 短、续期节拍更短，施加段横跨原 TTL 数倍——
// 期间他人认领必须被拒（ErrTaken），本持有者最终正常收场。
func TestManagedExecutorRenewalPreventsTakeover(t *testing.T) {
	ctx := context.Background()
	board := newRealtimeBoard()
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	exec, err := NewManagedExecutor(board, effects, ManagedConfig{
		Agent:         "w-a",
		ClaimTTL:      400 * time.Millisecond,
		RenewInterval: 80 * time.Millisecond,
		Progress:      nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	applied := make(chan struct{}, 1)
	work := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
				applied <- struct{}{}
				time.Sleep(1100 * time.Millisecond) // 横跨原 TTL（400ms）数倍
				return sideeffect.Result{Payload: []byte("done")}, nil
			},
		},
	}

	type execResult struct {
		outcome sideeffect.Outcome
		err     error
	}
	res := make(chan execResult, 1)
	go func() {
		o, e := exec.Execute(ctx, "t1", work)
		res <- execResult{o, e}
	}()
	<-applied // 已进入施加段（租约会话已在续期）

	// 接管尝试：横跨原 TTL 的时刻点前后各试一次，必须全部 ErrTaken。
	for i := range 3 {
		time.Sleep(350 * time.Millisecond) // 第 i 次尝试落在 ~(350i, 350i+400)ms
		_, claimErr := board.Claim(ctx, "t1", "w-b", taskboard.ClaimRequest{TTL: time.Minute})
		if !errors.Is(claimErr, taskboard.ErrTaken) {
			t.Fatalf("续期期间第 %d 次接管尝试必须被拒（ErrTaken），实得 %v", i+1, claimErr)
		}
	}

	ra := <-res
	if ra.err != nil {
		t.Fatalf("长任务在续期保护下应完整收场：%v", ra.err)
	}
	if ra.outcome != sideeffect.OutcomeApplied {
		t.Fatalf("应得到 applied：%s", ra.outcome)
	}
	tasks, _ := board.List(ctx)
	if tasks[0].State != taskboard.StateDone {
		t.Fatalf("任务应完成：%+v", tasks[0])
	}
}

// TestLeaseSessionContextCancelsOnLeaseLost 租约易主后，会话 ctx 必须取消
// （在途执行施加之前封口——与 worker.Loop 的在途取消同一纪律）。
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
	defer session.Close(context.Background())

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
	session.Close(ctx)

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
type blockingLeasedProgress struct {
	inner     *checkpoint.Committer
	entered   chan struct{}
	release   chan struct{}
	mu        sync.Mutex
	lastFence uint64
}

func (p *blockingLeasedProgress) Commit(ctx context.Context, s checkpoint.Snapshot) error {
	return p.inner.Commit(ctx, s)
}

func (p *blockingLeasedProgress) CommitFenced(ctx context.Context, s checkpoint.Snapshot, fence uint64) error {
	p.mu.Lock()
	p.lastFence = fence
	p.mu.Unlock()
	p.entered <- struct{}{}
	<-p.release
	// 用独立 ctx 委托：会话 ctx 可能在此期间因续期失败被取消（易主封口），
	// 而这里要观察的是**存储侧**的代次判定，不是 ctx 的死活。
	return p.inner.CommitFenced(context.Background(), s, fence)
}

// memFencedWorker 进度存储替身：双路径（带/不带代次）的内存实现，
// 语义与 checkpoint 包的 memFenced 一致（原子性来自单锁）。
type memFencedWorker struct {
	mu    sync.Mutex
	data  map[string]checkpoint.Snapshot
	fence map[string]uint64
}

func newMemFencedWorker() *memFencedWorker {
	return &memFencedWorker{data: map[string]checkpoint.Snapshot{}, fence: map[string]uint64{}}
}

func (m *memFencedWorker) SaveIfNewer(_ context.Context, s checkpoint.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.Seq == 0 {
		return checkpoint.ErrStaleSeq
	}
	if cur, ok := m.data[s.RunID]; ok && s.Seq <= cur.Seq {
		return checkpoint.ErrStaleSeq
	}
	m.data[s.RunID] = s.Clone()
	return nil
}

func (m *memFencedWorker) SaveIfNewerFenced(_ context.Context, s checkpoint.Snapshot, fence uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.Seq == 0 {
		return checkpoint.ErrStaleSeq
	}
	if cur, ok := m.fence[s.RunID]; ok && cur > fence {
		return checkpoint.ErrStaleFence
	}
	if cur, ok := m.data[s.RunID]; ok && s.Seq <= cur.Seq {
		return checkpoint.ErrStaleSeq
	}
	m.fence[s.RunID] = fence
	m.data[s.RunID] = s.Clone()
	return nil
}

func (m *memFencedWorker) Load(_ context.Context, runID string) (checkpoint.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.data[runID]
	if !ok {
		return checkpoint.Snapshot{}, checkpoint.ErrNotFound
	}
	return s.Clone(), nil
}

// TestManagedExecutorFencedCommitRejectsLateHolder
//
// 验收（issue #265）：旧持有者的 Checkpoint 提交被**存储端条件提交**拒绝。
//
// 编排：A 资格确认通过 ⇒ 进入带代次的提交（门上暂停）⇒ 世界翻转（B 接管、
// B 的快照以代次 2 落库、B 收口）⇒ 放行 A ⇒ A 的提交（代次 1）被存储侧
// 判定拒绝 ⇒ 执行器按失格处置返回，不收口。
//
// 反向验证锚点：本用例的绿依赖「执行器把会话代次随提交出示」——若进度
// 提交改回不带代次的普通 Commit，同样的迟到写会被普通 SaveIfNewer 放行
// （对照断言见 checkpoint 包 TestStaleCommitLandsWithoutFence）。
func TestManagedExecutorFencedCommitRejectsLateHolder(t *testing.T) {
	ctx := context.Background()
	board := &leaseStubBoard{aToken: "tok-a", bToken: "tok-b"}
	store := newMemFencedWorker()
	committer := checkpoint.NewCommitter(nil, store)
	progress := &blockingLeasedProgress{
		inner:   committer,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}

	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	exec, err := NewManagedExecutor(board, effects, ManagedConfig{
		Agent: "w-a", ClaimTTL: time.Minute, RenewInterval: 10 * time.Second, Progress: progress,
	})
	if err != nil {
		t.Fatal(err)
	}

	work := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
				return sideeffect.Result{Payload: []byte("a")}, nil
			},
		},
		Snapshot: func(task taskboard.Task, _ sideeffect.Result) (checkpoint.Snapshot, bool, error) {
			return checkpoint.Snapshot{RunID: "run-1", Seq: 3, Status: "running",
				Fields: map[string]string{"by": "a", "task": string(task.ID)}}, true, nil
		},
	}

	type execResult struct {
		outcome sideeffect.Outcome
		err     error
	}
	res := make(chan execResult, 1)
	go func() {
		o, e := exec.Execute(ctx, "t1", work)
		res <- execResult{o, e}
	}()
	<-progress.entered // A 已进入带代次的提交（资格确认已通过）
	if got := progress.lastFence; got != 1 {
		t.Fatalf("执行器必须出示本次认领的接管代次（1），实得 %d", got)
	}

	// 世界翻转：B 接管、以代次 2 提交快照、收口。
	board.mu.Lock()
	board.flipped = true
	board.mu.Unlock()
	bTask, err := board.Claim(ctx, "t1", "w-b", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitFenced(ctx, checkpoint.Snapshot{RunID: "run-1", Seq: 2, Status: "running",
		Fields: map[string]string{"by": "b"}}, uint64(bTask.Lease.Fence)); err != nil {
		t.Fatalf("新持有者的快照应以代次 2 落库：%v", err)
	}
	if err := board.Complete(ctx, "t1", bTask.Lease.Token); err != nil {
		t.Fatalf("新持有者应能收口：%v", err)
	}

	close(progress.release) // 放行 A 的迟到提交

	ra := <-res
	if !errors.Is(ra.err, checkpoint.ErrStaleFence) {
		t.Fatalf("旧持有者的迟到提交必须被存储端条件提交拒绝（ErrStaleFence）：%v", ra.err)
	}
	if got, _ := store.Load(ctx, "run-1"); got.Seq != 2 || got.Fields["by"] != "b" {
		t.Fatalf("存储必须保持新持有者的快照（旧持有者的 seq=3 不得落库）：%+v", got)
	}
	board.mu.Lock()
	completes := append([]string(nil), board.completes...)
	board.mu.Unlock()
	if !slices.Equal(completes, []string{bTask.Lease.Token}) {
		t.Fatalf("旧持有者不得收口（只有新持有者收场成功）：%v", completes)
	}
}

// 编译期锚：替身满足执行器/存储依赖面。
var (
	_ EffectRunner              = (*gatedEffects)(nil)
	_ Progress                  = (*recordingProgress)(nil)
	_ LeasedProgress            = (*blockingLeasedProgress)(nil)
	_ checkpoint.FencedStoreCAS = (*memFencedWorker)(nil)
	_ taskboard.RenewingBoard   = (*leaseStubBoard)(nil)
)

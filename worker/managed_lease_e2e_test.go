// managed_lease_e2e_test.go —— 受管执行器 × 租约会话的执行器级集成用例（issue #265）。
//
// 会话自身的单元行为（续期节拍、ctx 封口、配置校验）在 managed_lease_test.go；
// 这里只放「整条编排链上」的验证：易主后迟到快照被拒、续期防接管、
// 栅栏提交拒迟到持有者（含反向验证锚点）。
package worker

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

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

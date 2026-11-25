// managed_test.go —— 受管执行器的编排判据：正常链路、结果未知、收口被拒。
//
// 夹具全部合成数据（w-1 / t1 / run-1），不携带任何领域词面。
package worker

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/checkpoint"
	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// eventLog 记录编排四步的顺序（判据：执行 → 记账 → 收口，不能重排）。
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// managedBoard 观察 Complete 并可按需模拟「任务已归新持有者」。
type managedBoard struct {
	*coord.BoardAdapter
	log          *eventLog
	failComplete bool
}

func (b *managedBoard) Complete(ctx context.Context, id taskboard.TaskID, token string) error {
	b.log.add("complete")
	if b.failComplete {
		return errors.Wrap(taskboard.ErrLeaseLost, "worker: 测试替身：任务已归新持有者", "task", string(id))
	}
	return b.BoardAdapter.Complete(ctx, id, token)
}

// fakeEffects 可控结论的副作用编排面（四种结论各一条用例）。
type fakeEffects struct {
	mu sync.Mutex

	calls      int
	actions    []sideeffect.Action
	outcome    sideeffect.Outcome
	result     sideeffect.Result
	err        error
	applyCount int

	reconciles int
	resolution sideeffect.Resolution
}

func (f *fakeEffects) ExecuteWith(ctx context.Context, act sideeffect.Action, eff sideeffect.Effect) (sideeffect.Outcome, sideeffect.Result, error) {
	f.mu.Lock()
	f.calls++
	f.actions = append(f.actions, act)
	outcome, res, err := f.outcome, f.result, f.err
	f.mu.Unlock()

	if outcome == sideeffect.OutcomeApplied && err == nil && eff.Apply != nil {
		// 模拟 Runner：只有真正授予执行权时才施加。
		f.mu.Lock()
		f.applyCount++
		f.mu.Unlock()
		got, aerr := eff.Apply(ctx, nil, sideeffect.Lease{Resource: act.Resource, Fence: 1})
		if aerr != nil {
			return sideeffect.OutcomeUncertain, sideeffect.Result{}, aerr
		}
		return outcome, got, nil
	}
	return outcome, res, err
}

func (f *fakeEffects) Reconcile(_ context.Context, _ sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconciles++
	f.resolution = res
	return sideeffect.OutcomeReplayed, res.Result, nil
}

func (f *fakeEffects) counts() (calls, applies int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.applyCount
}

// fakeProgress 记录进度提交并按需注入失败。
type fakeProgress struct {
	mu      sync.Mutex
	log     *eventLog
	commits []checkpoint.Snapshot
	err     error
}

func (p *fakeProgress) Commit(_ context.Context, s checkpoint.Snapshot) error {
	p.log.add("checkpoint")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commits = append(p.commits, s)
	return p.err
}

func (p *fakeProgress) commitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.commits)
}

func newManagedBoard(clock *testClock, log *eventLog) *managedBoard {
	return &managedBoard{BoardAdapter: newTestBoard(clock), log: log}
}

func newTestExecutor(t *testing.T, board taskboard.RenewingBoard, effects EffectRunner, progress Progress) *ManagedExecutor {
	t.Helper()
	e, err := NewManagedExecutor(board, effects, ManagedConfig{
		Agent: "w-1", ClaimTTL: time.Minute, Progress: progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// workFor 构造一个受管任务：Apply 记事件并返回固定结果；可带快照构造。
func workFor(log *eventLog, withSnapshot bool) ManagedWork {
	w := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
				log.add("apply")
				return sideeffect.Result{Payload: []byte("done")}, nil
			},
		},
	}
	if withSnapshot {
		w.Snapshot = func(task taskboard.Task, res sideeffect.Result) (checkpoint.Snapshot, bool, error) {
			return checkpoint.Snapshot{
				RunID:  "run-1",
				Seq:    1,
				Status: "running",
				Done:   []string{string(task.ID)},
			}, true, nil
		}
	}
	return w
}

func TestManagedExecutorHappyPath(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	board := newManagedBoard(newTestClock(), log)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	progress := &fakeProgress{log: log}
	exec := newTestExecutor(t, board, effects, progress)

	outcome, err := exec.Execute(ctx, "t1", workFor(log, true))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("正常链路应 OutcomeApplied，得到 %s", outcome)
	}
	if got := log.snapshot(); !slices.Equal(got, []string{"apply", "checkpoint", "complete"}) {
		t.Fatalf("编排顺序必须是 执行 → 记账 → 收口：%v", got)
	}
	tasks, _ := board.List(ctx)
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
		t.Fatalf("任务应已完成：%+v", tasks)
	}
	if calls, applies := effects.counts(); calls != 1 || applies != 1 {
		t.Fatalf("副作用应恰好执行一次：calls=%d applies=%d", calls, applies)
	}
	if progress.commitCount() != 1 {
		t.Fatalf("应提交一次进度快照：%d", progress.commitCount())
	}
	effects.mu.Lock()
	act := effects.actions[0]
	effects.mu.Unlock()
	if act.Key != "task:t1" || act.Resource != "t1" {
		t.Fatalf("缺省幂等键/资源应由任务 id 派生：%+v", act)
	}
}

func TestManagedExecutorUncertainThenResolve(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	board := newManagedBoard(newTestClock(), log)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	effects := &fakeEffects{
		outcome: sideeffect.OutcomeUncertain,
		err:     errors.Wrap(sideeffect.ErrOutcomeUnknown, "worker: 测试替身：结果未知"),
	}
	progress := &fakeProgress{log: log}
	exec := newTestExecutor(t, board, effects, progress)

	outcome, err := exec.Execute(ctx, "t1", workFor(log, true))
	if outcome != sideeffect.OutcomeUncertain || !errors.Is(err, sideeffect.ErrOutcomeUnknown) {
		t.Fatalf("应得到结果未知：outcome=%s err=%v", outcome, err)
	}
	if got := log.snapshot(); !slices.Equal(got, []string{}) {
		t.Fatalf("结果未知不得执行/记账/收口，事件应为空：%v", got)
	}
	tasks, _ := board.List(ctx)
	if len(tasks) != 1 || tasks[0].State != taskboard.StateOpen {
		t.Fatalf("结果未知应释放任务板租约（不占锁，也不 Complete）：%+v", tasks)
	}
	if progress.commitCount() != 0 {
		t.Fatalf("结果未知不得提交进度：%d", progress.commitCount())
	}

	// 对账裁决（转发 Runner.Reconcile）：已生效 + 结果。
	if _, _, rerr := exec.Resolve(ctx, sideeffect.Action{Key: "task:t1", Resource: "t1"},
		sideeffect.Resolution{Applied: true, Result: sideeffect.Result{Payload: []byte("reconciled")}, Reason: "下游已确认"}); rerr != nil {
		t.Fatal(rerr)
	}
	effects.mu.Lock()
	if effects.reconciles != 1 || !effects.resolution.Applied {
		t.Fatalf("裁决应转交副作用编排面：%+v", effects)
	}
	effects.mu.Unlock()

	// 裁决后重试：Runner 返回 Replayed，跳过执行直接收口。
	effects.mu.Lock()
	effects.outcome = sideeffect.OutcomeReplayed
	effects.err = nil
	effects.result = sideeffect.Result{Payload: []byte("reconciled")}
	effects.mu.Unlock()
	outcome, err = exec.Execute(ctx, "t1", workFor(log, true))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeReplayed {
		t.Fatalf("裁决后重试应重放而非重做：%s", outcome)
	}
	if calls, applies := effects.counts(); calls != 2 || applies != 0 {
		t.Fatalf("执行体不得被重跑：calls=%d applies=%d", calls, applies)
	}
	tasks, _ = board.List(ctx)
	if tasks[0].State != taskboard.StateDone {
		t.Fatalf("重放后应收口：%+v", tasks[0])
	}
}

func TestManagedExecutorCompleteLeaseLost(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	board := newManagedBoard(newTestClock(), log)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	board.failComplete = true
	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	exec := newTestExecutor(t, board, effects, nil)

	outcome, err := exec.Execute(ctx, "t1", workFor(log, false))
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("副作用已施加，Outcome 应为 applied：%s", outcome)
	}
	if !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("收口被拒应如实返回 ErrLeaseLost：%v", err)
	}
	if calls, applies := effects.counts(); calls != 1 || applies != 1 {
		t.Fatalf("收口被拒不得重试副作用：calls=%d applies=%d", calls, applies)
	}
	tasks, _ := board.List(ctx)
	if tasks[0].State != taskboard.StateClaimed {
		t.Fatalf("替身未放闸，任务应仍在认领态（证明没有被二次收口）：%+v", tasks[0])
	}
	if got := log.snapshot(); !slices.Equal(got, []string{"apply", "complete"}) {
		t.Fatalf("事件顺序应为执行 → 收口（失败）：%v", got)
	}
}

func TestManagedExecutorContendedReleases(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	board := newManagedBoard(newTestClock(), log)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	effects := &fakeEffects{outcome: sideeffect.OutcomeContended}
	progress := &fakeProgress{log: log}
	exec := newTestExecutor(t, board, effects, progress)

	outcome, err := exec.Execute(ctx, "t1", workFor(log, true))
	if outcome != sideeffect.OutcomeContended || err != nil {
		t.Fatalf("contended 是正常判据（不带 error）：outcome=%s err=%v", outcome, err)
	}
	tasks, _ := board.List(ctx)
	if tasks[0].State != taskboard.StateOpen {
		t.Fatalf("没轮上我应把任务放回可认领态：%+v", tasks[0])
	}
	if progress.commitCount() != 0 {
		t.Fatal("contended 不得提交进度")
	}

	// 准备/施加失败的可安全重试出口（contended + error）必须上抛。
	effects.mu.Lock()
	effects.err = errors.New("worker: 测试替身：准备失败")
	effects.mu.Unlock()
	if _, err := exec.Execute(ctx, "t1", workFor(log, true)); err == nil {
		t.Fatal("contended 带的失败原因不得被吞掉")
	}
}

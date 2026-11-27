// fault_event_checkpoint_test.go —— 插点 G5/F4（事件 → checkpoint）的正向
// 注入（issue #523）：事件已发布、快照未保存时租约过期，旧持有者恢复执行
// ⇒ 快照序号不得回退、事件不得重放。
//
// 与 G3/F4（TestManagedExecutorLateCheckpointRejectedAfterTakeover，卡在施加
// 段内）的分工：本用例把闸卡在**施加完成之后、快照落库之前**——正是
// 「事件已发布、快照未保存」那道间隙；租约过期走注入时钟，不靠墙钟窗口。
package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// replayOnceEffects 按动作只施加一次的副作用编排面替身：首次授予并施加，
// 之后同键一律判 Replayed（不重放）。语义对齐 sideeffect.Store 的「已完成
// → 重放」判定；这里只做执行器接缝（EffectRunner）面的最小投影——判据是
// 「跨两个执行器施加恰好一次」，不是替存储再实现一份状态机。
type replayOnceEffects struct {
	mu      sync.Mutex
	applies int
	result  sideeffect.Result
}

func (g *replayOnceEffects) ExecuteWith(ctx context.Context, act sideeffect.Action, eff sideeffect.Effect) (sideeffect.Outcome, sideeffect.Result, error) {
	g.mu.Lock()
	if g.applies > 0 {
		res := g.result
		g.mu.Unlock()
		return sideeffect.OutcomeReplayed, res, nil
	}
	g.mu.Unlock()
	res, err := eff.Apply(ctx, nil, sideeffect.Lease{Resource: act.Resource, Fence: 1})
	if err != nil {
		return sideeffect.OutcomeUncertain, sideeffect.Result{}, err
	}
	g.mu.Lock()
	g.applies++
	g.result = res
	g.mu.Unlock()
	return sideeffect.OutcomeApplied, res, nil
}

func (g *replayOnceEffects) Reconcile(_ context.Context, _ sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	return sideeffect.OutcomeReplayed, res.Result, nil
}

// applyCount 已施加次数（「事件只发布一次」的计数面）。
func (g *replayOnceEffects) applyCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applies
}

// TestFaultLeaseExpiredOldHolderResumeBetweenEventAndCheckpoint
//
// 编排：A 施加（事件已发布）→ 资格确认通过 → 构造快照 → 卡在 CommitFenced
// 入口（快照未保存）⇒ 注入时钟推过 TTL（租约过期）⇒ B 接管并完整收场
// （同一动作判 Replayed、快照以代次 2 落库）⇒ 放行 A（旧持有者恢复执行）
// ⇒ A 的迟到快照必须被存储侧代次判定拒绝。
//
// 期望失败形态（坏实现必红的对照）：
//   - 旧持有者的迟到写落地 ⇒ 存储 seq 退回 1 / 内容换成旧持有者 ⇒ 序号断言红；
//   - 接管方重跑施加 ⇒ 施加计数 2、事件日志两条 apply ⇒ 不重放断言红。
func TestFaultLeaseExpiredOldHolderResumeBetweenEventAndCheckpoint(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	board := newTestBoard(clock)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	store := newMemFencedWorker()
	effects := &replayOnceEffects{}
	log := &eventLog{}

	// A（旧持有者）的进度面：CommitFenced 入口即闸——事件已发布、快照未保存。
	progressA := &blockingLeasedProgress{
		inner:   checkpoint.NewCommitter(checkpoint.NewSeqGate(), store),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	execA, err := NewManagedExecutor(board, effects, ManagedConfig{
		Agent: "w-a", ClaimTTL: time.Minute, Progress: progressA,
	})
	if err != nil {
		t.Fatal(err)
	}
	workA := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(actx context.Context, _ any, _ sideeffect.Lease) (sideeffect.Result, error) {
				log.add("apply")
				return sideeffect.Result{Payload: []byte("a")}, nil
			},
		},
		Snapshot: func(task taskboard.Task, _ sideeffect.Result) (checkpoint.Snapshot, bool, error) {
			return checkpoint.Snapshot{RunID: "run-1", Seq: 1, Status: "running",
				Fields: map[string]string{"by": "a", "task": string(task.ID)}}, true, nil
		},
	}

	type execResult struct {
		outcome sideeffect.Outcome
		err     error
	}
	doneA := make(chan struct{})
	var ra execResult
	go func() {
		ra.outcome, ra.err = execA.Execute(ctx, "t1", workA)
		close(doneA)
	}()
	// 收尾 join 写成 defer 形态：失败路径上也等 A 退出（注册先于下面的
	// 放闸 defer ⇒ LIFO 下先放闸、再 join，不会有人被闸挂住）。
	defer func() { <-doneA }()

	<-progressA.entered // A 已发布事件、已过资格确认，卡在快照落库之前

	// 放闸的收尾保证：用例中途失败也必须放行 A，否则 join 会挂在闸上。
	var releaseOnce sync.Once
	releaseA := func() { releaseOnce.Do(func() { close(progressA.release) }) }
	defer releaseA()

	// 间隙前置：事件已发布（施加 1 次）、快照未保存（存储里还没有这个 run）。
	if got := effects.applyCount(); got != 1 {
		t.Fatalf("进入快照闸前事件应已发布（施加 1 次），实得 %d", got)
	}
	if _, err := store.Load(ctx, "run-1"); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("事件已发布、快照未保存：此阶段存储不应有快照，实得 %v", err)
	}

	// 租约过期：注入时钟推过 TTL（A 卡在闸上，无人替它续期）。
	clock.Advance(2 * time.Minute)

	// B 接管并完整收场：同一动作必须判 Replayed（事件不重放），快照以
	// 代次 2 落库，任务收口。
	commB := checkpoint.NewCommitter(checkpoint.NewSeqGate(), store)
	execB, err := NewManagedExecutor(board, effects, ManagedConfig{
		Agent: "w-b", ClaimTTL: time.Minute, Progress: commB,
	})
	if err != nil {
		t.Fatal(err)
	}
	workB := ManagedWork{
		Effect: sideeffect.Effect{
			Apply: func(actx context.Context, _ any, _ sideeffect.Lease) (sideeffect.Result, error) {
				log.add("apply") // 不应被执行到：接管方拿回的是重放结论
				return sideeffect.Result{Payload: []byte("b")}, nil
			},
		},
		Snapshot: func(task taskboard.Task, _ sideeffect.Result) (checkpoint.Snapshot, bool, error) {
			return checkpoint.Snapshot{RunID: "run-1", Seq: 2, Status: "running",
				Fields: map[string]string{"by": "b", "task": string(task.ID)}}, true, nil
		},
	}
	outB, err := execB.Execute(ctx, "t1", workB)
	if err != nil {
		t.Fatalf("新持有者接管后应完整收场: %v", err)
	}
	if outB != sideeffect.OutcomeReplayed {
		t.Fatalf("同一动作必须判重放（事件不得重放），实得 %s", outB)
	}
	snap, err := store.Load(ctx, "run-1")
	if err != nil || snap.Seq != 2 || snap.Fields["by"] != "b" {
		t.Fatalf("新持有者的快照应以 seq 2 落库: snap=%+v err=%v", snap, err)
	}

	// 放行旧持有者恢复执行：它的迟到快照（代次 1）必须被存储侧拒绝。
	releaseA()
	<-doneA

	if !errors.Is(ra.err, checkpoint.ErrStaleFence) {
		t.Fatalf("旧持有者的迟到快照必须被代次判定拒绝（ErrStaleFence）: %v", ra.err)
	}
	// 快照序号不得回退：存储保持新持有者的 seq 2，内容不被旧持有者换掉。
	snap, err = store.Load(ctx, "run-1")
	if err != nil || snap.Seq != 2 || snap.Fields["by"] != "b" {
		t.Fatalf("旧持有者的迟到写不得让快照回退: snap=%+v err=%v", snap, err)
	}
	// 事件不得重放：跨两个执行器施加恰好一次，事件日志里 apply 恰好一条。
	if got := effects.applyCount(); got != 1 {
		t.Fatalf("事件不得重放：施加次数应为 1，实得 %d", got)
	}
	applies := 0
	for _, e := range log.snapshot() {
		if e == "apply" {
			applies++
		}
	}
	if applies != 1 {
		t.Fatalf("事件不得重放：事件日志里的 apply 应恰好 1 条，实得 %d（%v）", applies, log.snapshot())
	}
	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
		t.Fatalf("任务应由新持有者收场、板上恰好一行: %+v", tasks)
	}
}

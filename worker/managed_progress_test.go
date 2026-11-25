// managed_progress_test.go —— 进度快照与装配校验的判据（从 managed_test.go
// 拆出：结构阈值按判据的一侧切——编排主链与进度/装配分开，各自可读）。
package worker

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/checkpoint"
	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

func TestManagedExecutorCheckpointStaleStillCompletes(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	board := newManagedBoard(newTestClock(), log)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	progress := &fakeProgress{log: log, err: errors.Wrap(checkpoint.ErrStaleSeq, "已有更高序号")}
	exec := newTestExecutor(t, board, effects, progress)

	outcome, err := exec.Execute(ctx, "t1", workFor(log, true))
	if err != nil {
		t.Fatalf("进度陈旧（已有更高版本）不阻塞收口：%v", err)
	}
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("应为 applied：%s", outcome)
	}
	tasks, _ := board.List(ctx)
	if tasks[0].State != taskboard.StateDone {
		t.Fatalf("任务应完成：%+v", tasks[0])
	}
	if progress.commitCount() != 1 {
		t.Fatal("应尝试提交一次进度（由 Committer 拒掉旧版本）")
	}
}

func TestManagedExecutorCheckpointFailureReleases(t *testing.T) {
	ctx := context.Background()
	log := &eventLog{}
	board := newManagedBoard(newTestClock(), log)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	progress := &fakeProgress{log: log, err: errors.New("worker: 测试替身：进度落库失败")}
	exec := newTestExecutor(t, board, effects, progress)

	outcome, err := exec.Execute(ctx, "t1", workFor(log, true))
	if outcome != sideeffect.OutcomeApplied || err == nil {
		t.Fatalf("进度失败必须上抛（副作用已施加）：outcome=%s err=%v", outcome, err)
	}
	tasks, _ := board.List(ctx)
	if tasks[0].State != taskboard.StateOpen {
		t.Fatalf("进度失败不得收口，应释放租约等重试：%+v", tasks[0])
	}

	// 重试：副作用闸返回 Replayed（不重做），进度补上后收口。
	progress.mu.Lock()
	progress.err = nil
	progress.mu.Unlock()
	effects.mu.Lock()
	effects.outcome = sideeffect.OutcomeReplayed
	effects.mu.Unlock()
	if _, err := exec.Execute(ctx, "t1", workFor(log, true)); err != nil {
		t.Fatal(err)
	}
	if calls, applies := effects.counts(); calls != 2 || applies != 1 {
		t.Fatalf("重试不得重做副作用：calls=%d applies=%d", calls, applies)
	}
	tasks, _ = board.List(ctx)
	if tasks[0].State != taskboard.StateDone {
		t.Fatalf("重试应收口：%+v", tasks[0])
	}
}

func TestNewManagedExecutorValidatesConfig(t *testing.T) {
	board := newTestBoard(newTestClock())
	effects := &fakeEffects{outcome: sideeffect.OutcomeApplied}
	cases := []struct {
		name    string
		board   taskboard.RenewingBoard
		effects EffectRunner
		cfg     ManagedConfig
	}{
		{"缺任务板", nil, effects, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute}},
		{"缺副作用编排面", board, nil, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute}},
		{"缺 Agent", board, effects, ManagedConfig{ClaimTTL: time.Minute}},
		{"零 TTL", board, effects, ManagedConfig{Agent: "w-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewManagedExecutor(tc.board, tc.effects, tc.cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("装配期应报 ErrInvalidConfig，得到 %v", err)
			}
		})
	}
}

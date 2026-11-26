// concurrency_test.go —— 并发池（issue #196）的判据：默认串行不变、
// 显式并发时多任务真正并行、每条在途租约独立续期/独立取消、停机排空、
// panic 只隔离在单个任务内。
//
// 判据全部用可控时钟与闸门同步，不靠 sleep 撞时序（等待超时只做兜底）。
package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestLoopConcurrencyTwoLongTasksRunInParallel 两个慢任务在 Concurrency=2
// 时必须同时进入执行体：串行实现里第二个任务要等第一个返回后才被领取，
// 本用例在「两个都进入」处必红。
func TestLoopConcurrencyTwoLongTasksRunInParallel(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	reg := newMemRegistry(clock.Now)
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	entered := make(chan taskboard.TaskID, 2)
	release := make(chan struct{})
	var applied atomic.Int64
	handler := HandlerFunc(func(ctx context.Context, task taskboard.Task) error {
		entered <- task.ID
		select {
		case <-release:
			applied.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 50 * time.Millisecond, Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	seen := map[taskboard.TaskID]bool{}
	for len(seen) < 2 {
		select {
		case id := <-entered:
			seen[id] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("并发度 2 时两个任务必须同时进入执行体（已进入 %v）", seen)
		}
	}
	if got := applied.Load(); got != 0 {
		t.Fatalf("闸门未放开前不得有任务收场（实得 %d）", got)
	}
	close(release)

	deadline := time.After(5 * time.Second)
	for {
		tasks, err := board.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		doneCount := 0
		for _, task := range tasks {
			if task.State == taskboard.StateDone {
				doneCount++
			}
		}
		if doneCount == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("两个任务必须各自完成（实得 %v）", tasks)
		case <-time.After(time.Millisecond):
		}
	}
	if applied.Load() != 2 {
		t.Fatalf("两个执行体都应施加成功（实得 %d）", applied.Load())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("优雅退出必须返回 nil（实得 %v）", err)
	}
}

// TestLoopConcurrencyRenewsEveryInflightLease 并发在途时心跳按批续期：
// 两个任务的租约终点都必须跟随存储时钟 + TTL。只续第一个（把 current 单槽
// 原样搬进并发实现）在第二个任务上必红。
func TestLoopConcurrencyRenewsEveryInflightLease(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock() // 注册表时钟不动：本用例只看任务租约
	board := newTestBoard(boardClock)
	reg := newMemRegistry(regClock.Now)
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond, Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	waitClaimed(t, board, 2)
	boardClock.Advance(10 * time.Second)
	target := boardClock.Now().Add(time.Minute)

	deadline := time.After(5 * time.Second)
	for {
		tasks, err := board.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		renewed := 0
		for _, task := range tasks {
			if task.State == taskboard.StateClaimed && task.Lease.Until.Equal(target) {
				renewed++
			}
		}
		if renewed == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("两条在途租约都必须续到 %v（实得 %v）", target, tasks)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("优雅退出必须返回 nil（实得 %v）", err)
	}
}

// renewLostBoard 只在 Renew 上注入 ErrLeaseLost（不改变存储状态）：
// 判「租约易主只取消它自己」时，隔离条件是错误来源，不是存储状态。
type renewLostBoard struct {
	*coord.BoardAdapter
	lost atomic.Value // string：要注入易主的任务 id（空串 = 不注入）
}

func (b *renewLostBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	if s, _ := b.lost.Load().(string); s != "" && s == string(id) {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, "fault: 注入租约易主")
	}
	return b.BoardAdapter.Renew(ctx, id, token, req)
}

// TestLoopConcurrencyLeaseLostCancelsOnlyItself 并发在途时一条租约报
// ErrLeaseLost ⇒ 只取消它自己的执行体，另一条继续被续期、继续运行。
func TestLoopConcurrencyLeaseLostCancelsOnlyItself(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock()
	board := &renewLostBoard{BoardAdapter: newTestBoard(boardClock)}
	reg := newMemRegistry(regClock.Now)
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	entered := make(chan taskboard.TaskID, 2)
	canceled := make(chan taskboard.TaskID, 8)
	handler := HandlerFunc(func(ctx context.Context, task taskboard.Task) error {
		select {
		case entered <- task.ID:
		default:
		}
		<-ctx.Done()
		select {
		case canceled <- task.ID:
		default:
		}
		return errors.New("worker: 在途取消后按失败收场（替身）")
	})
	// 注入易主的 t1 释放后不得被重新领取（否则每个心跳节拍都会再取消它
	// 一次，判据观测到的是自旋而不是「另一条没被波及」）。t2 优先领取。
	var t1Picked atomic.Bool
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond, Concurrency: 2,
		Pick: func(candidates []taskboard.Task) (taskboard.TaskID, bool) {
			for _, c := range candidates {
				if c.ID == "t2" {
					return c.ID, true
				}
			}
			for _, c := range candidates {
				if c.ID == "t1" && !t1Picked.Swap(true) {
					return c.ID, true
				}
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	acquired := map[taskboard.TaskID]bool{}
	for len(acquired) < 2 {
		select {
		case id := <-entered:
			acquired[id] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("等待两个任务进入执行体超时（实得 %v）", acquired)
		}
	}

	board.lost.Store("t1")
	select {
	case id := <-canceled:
		if id != "t1" {
			t.Fatalf("被取消的必须是注入易主的 t1（实得 %s）", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("t1 租约易主后必须取消它的在途执行")
	}
	select {
	case id := <-canceled:
		t.Fatalf("租约易主不得波及另一条在途（误取消 %s）", id)
	case <-time.After(50 * time.Millisecond):
	}

	// t2 不仅没被取消，还必须继续按自己的租约被续期。
	boardClock.Advance(10 * time.Second)
	target := boardClock.Now().Add(time.Minute)
	deadline := time.After(5 * time.Second)
	for {
		tasks, err := board.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if task.ID == "t2" && task.State == taskboard.StateClaimed && task.Lease.Until.Equal(target) {
				goto renewed
			}
		}
		select {
		case <-deadline:
			t.Fatalf("t2 的租约必须继续续期到 %v（实得 %v）", target, tasks)
		case <-time.After(time.Millisecond):
		}
	}
renewed:
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("单任务资格失败不构成驱动故障，应优雅退出（实得 %v）", err)
	}
}

// waitClaimed 等任务板上恰好 want 条在途认领（超时即红）。
func waitClaimed(t *testing.T, board taskboard.Board, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		tasks, err := board.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		claimed := 0
		for _, task := range tasks {
			if task.State == taskboard.StateClaimed {
				claimed++
			}
		}
		if claimed == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("等待 %d 条在途认领超时（实得 %d，%v）", want, claimed, tasks)
		case <-time.After(time.Millisecond):
		}
	}
}

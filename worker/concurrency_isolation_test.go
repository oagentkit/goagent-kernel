// concurrency_isolation_test.go —— 并发池的隔离与排水判据（issue #196）：
// panic 只影响单个任务、停机排空不丢在途。与并发/续期判据分文件（结构阈值
// 按判据的一侧切），共用同包的夹具。
package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestLoopConcurrencyDrainKeepsInFlight 停机排空：外层取消后驱动必须等待
// 所有在途执行体收场（收场走 WithoutCancel），不得提前返回把任务悬在 claimed。
func TestLoopConcurrencyDrainKeepsInFlight(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	reg := newMemRegistry(clock.Now)
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	entered := make(chan taskboard.TaskID, 2)
	finish := make(chan struct{})
	var finalized atomic.Int64
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		entered <- taskboard.TaskID("in")
		<-ctx.Done()
		<-finish // 收场由测试显式放行：驱动必须等在这里
		finalized.Add(1)
		return nil // 成功收场 ⇒ Complete 走 WithoutCancel
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond, Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("等待两个任务进入执行体超时")
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("在途未收场时 Run 不得提前返回（实得 %v）", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("排空完成后应优雅退出（实得 %v）", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待排空超时")
	}
	if finalized.Load() != 2 {
		t.Fatalf("两个在途执行体都必须收场（实得 %d）", finalized.Load())
	}
	tasks, err := board.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.State != taskboard.StateDone {
			t.Fatalf("停机不得丢在途：任务 %s 必须已落终态（实得 %s）", task.ID, task.State)
		}
	}
}

// TestLoopConcurrencyPanicIsolated handler panic 只隔离在该任务内：
// 进程不退出、同池其它任务照常完成、panic 任务按失败收场（Release 回
// 可重做）。实现走 async.GoSafe + 执行体 recover，缺一层都会红。
func TestLoopConcurrencyPanicIsolated(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	reg := newMemRegistry(clock.Now)
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)

	prev := async.GlobalPanicHandler()
	var panicked atomic.Bool
	async.SetPanicHandler(func(context.Context, any, []byte) { panicked.Store(true) })
	defer async.SetPanicHandler(prev)

	var t1Runs atomic.Int32
	t2Done := make(chan struct{}, 1)
	handler := HandlerFunc(func(ctx context.Context, task taskboard.Task) error {
		if task.ID == "t1" {
			if t1Runs.Add(1) == 1 {
				panic("fault: 执行体 panic（隔离判据）")
			}
			<-ctx.Done() // 重领后挂住：不让 panic 形成自旋
			return ctx.Err()
		}
		t2Done <- struct{}{}
		return nil
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond, Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-t2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("panic 任务不得拖垮同池的另一个任务")
	}
	deadline := time.After(5 * time.Second)
	for !panicked.Load() {
		select {
		case <-deadline:
			t.Fatal("执行体 panic 必须经 async 的观测面收编，不得静默")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("panic 隔离后应优雅退出（实得 %v）", err)
	}
	tasks, err := board.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		switch {
		case task.ID == "t2" && task.State != taskboard.StateDone:
			t.Fatalf("同池的 t2 必须完成（实得 %s）", task.State)
		case task.ID == "t1" && task.State != taskboard.StateOpen:
			t.Fatalf("panic 任务必须被释放回可重做（实得 %s）", task.State)
		}
	}
}

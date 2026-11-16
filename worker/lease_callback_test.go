// lease_callback_test.go —— OnLeaseLost 钩子的并发面（issue #326）。
//
// 两条判据：
//   - 回调在 claimed.mu **之外**：回调内同 goroutine 回查本包（这里用
//     beginFinalize 抢同一把锁）必须在超时内返回，不得挂死；
//   - 回调 panic 不得掀翻心跳协程：续期次数必须在观测窗口内持续增长
//     （旧实现里 panic 逃出 heartbeat，全部在途租约停止续期）。
package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestRenewOneNotifiesOutsideClaimLock 回调重入同一把锁必须自行返回。
func TestRenewOneNotifiesOutsideClaimLock(t *testing.T) {
	clock := newTestClock()
	board := newTestBoard(clock)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	claimedTask, err := board.Claim(context.Background(), "t1", "w-1",
		taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("预置认领: %v", err)
	}
	clock.Advance(2 * time.Minute) // 租约过期：Renew 必报 ErrLeaseLost

	cur := &claimed{id: "t1", token: claimedTask.Lease.Token, cancel: func() {}}
	var callbackRan atomic.Bool
	loop := &Loop{
		board: board,
		cfg: Config{
			RenewFailMax: 3,
			OnLeaseLost: func(taskboard.TaskID, LeaseLostCause) {
				callbackRan.Store(true)
				cur.beginFinalize() // 同 goroutine 二次抢 claimed.mu
			},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var unconfirmed bool
		loop.renewOne(context.Background(), func() {}, cur, &unconfirmed)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnLeaseLost 回调回查本包时死锁（回调仍在 claimed.mu 内）")
	}
	if !callbackRan.Load() {
		t.Fatal("ErrLeaseLost 必须触发 OnLeaseLost")
	}
}

// countingLostBoard 每次 Renew 都报 ErrLeaseLost 并计数：判「回调 panic
// 后心跳是否还在跑」只需要续期次数这一个观测面。
type countingLostBoard struct {
	*coord.BoardAdapter
	renews atomic.Int64
}

func (b *countingLostBoard) Renew(context.Context, taskboard.TaskID, string,
	taskboard.ClaimRequest) (taskboard.Task, error) {
	b.renews.Add(1)
	return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, "fault: 注入租约易主")
}

// TestOnLeaseLostPanicKeepsHeartbeatAlive 回调 panic 不得停止心跳。
func TestOnLeaseLostPanicKeepsHeartbeatAlive(t *testing.T) {
	// panic 上报走 async 的统一收口：测试里换成计数器，既不喷栈也顺带
	// 断言「panic 确实被隔离并上报」。
	prev := async.GlobalPanicHandler()
	var reported atomic.Int64
	async.SetPanicHandler(func(context.Context, any, []byte) { reported.Add(1) })
	defer async.SetPanicHandler(prev)

	boardClock := newTestClock()
	regClock := newTestClock()
	board := &countingLostBoard{BoardAdapter: newTestBoard(boardClock)}
	reg := newMemRegistry(regClock.Now)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond,
		OnLeaseLost: func(taskboard.TaskID, LeaseLostCause) {
			panic("probe: 宿主留痕回调 panic")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for board.renews.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("等待首次续期超时")
		case <-time.After(time.Millisecond):
		}
	}
	first := board.renews.Load()
	time.Sleep(80 * time.Millisecond) // 至少十几个心跳拍
	if got := board.renews.Load(); got < first+3 {
		t.Fatalf("回调 panic 后心跳必须继续续期（%d → %d）——panic 逃出 heartbeat 了", first, got)
	}
	if reported.Load() == 0 {
		t.Fatal("被隔离的回调 panic 必须上报到 async 观测面")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("回调 panic 不构成驱动失败，应优雅退出（实得 %v）", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待驱动退出超时")
	}
}

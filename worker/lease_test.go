package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// 本文件钉 issue #179 的两类资格失效：租约续期失败（易主/无法确认）与
// 收场被拒都必须「在施加之前封口」且留下可观测信号——旧实现把心跳与续期
// 焊在一起、把收场被拒当成功路径吞掉，三种形态都静默。

// TestLoopHeartbeatFailureDoesNotStarveLeaseRenewal 用例 A（issue #179）：
// 注册表心跳持续报错（存储抖动，非身份丢失）、任务板续期正常 ⇒ 租约必须
// 仍按心跳节拍续期。旧实现把心跳与续期焊在同一个分支里（心跳失败即
// continue），本用例在旧实现上必红（Renew 一次都不发生）。
func TestLoopHeartbeatFailureDoesNotStarveLeaseRenewal(t *testing.T) {
	clock := newTestClock()
	board := &renewCountingBoard{BoardAdapter: newTestBoard(clock)}
	reg := &heartbeatFailingRegistry{memRegistry: newMemRegistry(clock.Now), err: errRegistryDown}
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done() // 长任务：在途直到停机
		return ctx.Err()
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: 5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	// 心跳节拍 5ms：等到至少 3 次续期才算「按节拍在续」，排除碰巧一次。
	deadline := time.After(5 * time.Second)
	for board.renews.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("注册表心跳持续失败时租约必须继续续期（实际续期 %d 次）——"+
				"身份可用性不得成为任务正确性的前置条件", board.renews.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("心跳存储抖动不应让驱动收刀（身份未判丢失，实得 %v）", err)
	}
}

// TestLoopRenewalFailureCancelsInFlightWithinTTL 用例 B（issue #179）：
// 注册表心跳与任务租约续期都持续失败 ⇒ 持有资格无法确认；handler 的 ctx
// 必须在 TTL 量级内被取消（旧实现里续期根本不被调用，本用例必红）。
func TestLoopRenewalFailureCancelsInFlightWithinTTL(t *testing.T) {
	clock := newTestClock()
	board := &renewFailingBoard{BoardAdapter: newTestBoard(clock), err: errBoardDown}
	reg := &heartbeatFailingRegistry{memRegistry: newMemRegistry(clock.Now), err: errRegistryDown}
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{})
	var once sync.Once
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		once.Do(func() { close(cancelled) })
		return ctx.Err()
	})
	const ttl = 40 * time.Millisecond
	events := &eventLog{}
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: ttl, PollInterval: 5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		OnEvent:           events.record,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待在途执行超时")
	}
	// 门槛是「一个租约周期」：取消必须在 TTL 量级内发生（20×TTL 留足
	// CI 负载余量，同时把「阈值被写成远大于 TTL」的实现判红）。
	select {
	case <-cancelled:
	case <-time.After(20 * ttl):
		t.Fatal("续期持续失败跨过 TTL 后必须取消在途 ctx——无法确认持有资格" +
			"就不得继续施加副作用")
	}
	// 取消不是静默动作：必须有一次「资格无法确认」的留痕。
	if ev := events.wait(t, EventLeaseUnconfirmed); ev.Task != "t1" {
		t.Fatalf("EventLeaseUnconfirmed 必须带任务标识（实得 %q）", ev.Task)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("停机不应因本次任务取消而报错（实得 %v）", err)
	}
}

// TestLoopLeaseLostSignalsAreObservable 用例 C（issue #179）：租约易主时，
// Renew 报 ErrLeaseLost（取消在途）与随后 Complete 被拒（本次收场未生效）
// 都必须有可观测信号——旧实现把后者当成功路径静默吞掉。
func TestLoopLeaseLostSignalsAreObservable(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock() // 注册表时钟不动：身份保持有效，易主只由任务租约触发
	board := newTestBoard(boardClock)
	reg := newMemRegistry(regClock.Now)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	entered := make(chan struct{}, 1)
	finish := make(chan struct{}) // 测试确认易主信号后才放行收场
	var sawCancel atomic.Bool
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done() // 易主时驱动取消在途
		sawCancel.Store(true)
		<-finish
		return nil // 触发 Complete——会被 ErrLeaseLost 拒
	})

	events := &eventLog{}
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: 5 * time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		OnEvent:           events.record,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待在途执行超时")
	}
	boardClock.Advance(2 * time.Minute) // 租约过期
	// 新持有者接管：旧令牌从此被拒（Renew/Complete 都报 ErrLeaseLost）。
	if _, err := board.Claim(context.Background(), "t1", "w-2", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatalf("过期租约应可被接管（实得 %v）", err)
	}
	// 易主信号：EventLeaseLost 留痕 + 在途 ctx 被取消。
	if lost := events.wait(t, EventLeaseLost); lost.Task != "t1" {
		t.Fatalf("EventLeaseLost 必须带任务标识（实得 %q）", lost.Task)
	}
	deadline := time.After(5 * time.Second)
	for !sawCancel.Load() {
		select {
		case <-deadline:
			t.Fatal("租约易主后必须取消在途执行——危险窗口要在施加之前封口")
		case <-time.After(2 * time.Millisecond):
		}
	}
	close(finish)
	// 收场被拒信号：Complete 被 ErrLeaseLost 拒不能安安静静当成功。
	rejected := events.wait(t, EventFinalizeRejected)
	if rejected.Task != "t1" || !errors.Is(rejected.Err, taskboard.ErrLeaseLost) {
		t.Fatalf("EventFinalizeRejected 必须带任务与 ErrLeaseLost（实得 task=%q err=%v）",
			rejected.Task, rejected.Err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("易主后应按既有语义优雅退出（实得 %v）", err)
	}
	tasks, _ := board.List(context.Background())
	if tasks[0].State != taskboard.StateClaimed || tasks[0].Owner != "w-2" {
		t.Fatalf("收场被拒后任务必须归新持有者（实得 state=%s owner=%s）",
			tasks[0].State, tasks[0].Owner)
	}
}

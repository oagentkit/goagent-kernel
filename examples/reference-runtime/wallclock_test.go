package main

// wallclock_test.go —— 会话级墙钟接线的判据（issue #355）。
//
// 两条判据都对着「总时限不因续跑轮重置」这条契约：
//
//   - 锚点已过期 ⇒ 本轮**不进编排**，立即 timed_out 收敛（进去也只是白跑一段，
//     而超时的产出不可信 = deny-by-default）；
//   - 续跑轮沿用同一锚点，且**绝不刷新**它（刷新等于把总时限重新计时）。

import (
	"context"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/deadline"
)

// TestWallClockExpiredAnchorConvergesTimedOut 锚点已过期 ⇒ 本轮不进编排，
// 立即按 timed_out 收敛。
func TestWallClockExpiredAnchorConvergesTimedOut(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	calls := newCallCounter()
	steps := testSteps(calls)
	anchors := newWallClockStore()
	// 锚点已过期（余量耗完）：本轮拿不到任何剩余时长。
	if err := anchors.SaveAnchor(ctx, deadline.Anchor{
		FirstClaimAt: time.Now().Add(-time.Hour),
		DeadlineAt:   time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	rt := newRuntime("run-1", store, steps, time.Minute).
		withWallClock(deadline.Config{Timeout: time.Hour, Shared: true}, anchors)
	rep, err := rt.run(ctx, 0)
	if err != nil {
		t.Fatalf("墙钟到点不是错误（是收敛）：%v", err)
	}
	if rep.Status != contract.StatusTimedOut {
		t.Fatalf("墙钟到点必须收敛为 timed_out，实得 %q", rep.Status)
	}
	if len(rep.Executed) != 0 {
		t.Fatalf("墙钟到点的轮不得进编排（进去也只是白跑一段），实得执行 %v", rep.Executed)
	}
	if n := calls.snapshot()["prepare"]; n != 0 {
		t.Fatalf("墙钟到点的轮不得施加任何动作（超时的产出不可信），实得调用 %d 次", n)
	}
}

// TestWallClockAnchorSharedAcrossRounds 锚点首轮写一次、续跑轮沿用且不刷新。
func TestWallClockAnchorSharedAcrossRounds(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	calls := newCallCounter()
	steps := testSteps(calls)
	anchors := newWallClockStore()
	wall := deadline.Config{Timeout: time.Hour, Shared: true}

	rt := newRuntime("run-1", store, steps, time.Minute).withWallClock(wall, anchors)
	if _, err := rt.run(ctx, 0); err != nil {
		t.Fatalf("首轮执行失败: %v", err)
	}
	first, err := anchors.LoadAnchor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("首轮必须写锚点（否则续跑轮各自新开一段时限，总时限被静默重置）")
	}

	// 续跑轮：重建运行时（等价于进程重启），共用同一份锚点存储。
	rt2 := newRuntime("run-1", store, steps, time.Minute).withWallClock(wall, anchors)
	if _, err := rt2.run(ctx, 0); err != nil {
		t.Fatalf("续跑轮执行失败: %v", err)
	}
	second, err := anchors.LoadAnchor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second == nil {
		t.Fatal("续跑轮不得抹掉锚点（抹掉等于把总时限重新计时）")
	}
	if !second.DeadlineAt.Equal(first.DeadlineAt) {
		t.Fatalf("续跑轮不得刷新锚点：first=%v second=%v", first.DeadlineAt, second.DeadlineAt)
	}
}

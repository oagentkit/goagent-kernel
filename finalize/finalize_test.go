package finalize_test

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/finalize"
	"github.com/yi-nology/goagent-kernel/steer"
)

// withCause 造一个已按 cause 取消的编排 ctx（宿主 WithCancelCause 的形态）。
func withCause(t *testing.T, cause error) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	cancel(cause)
	return ctx
}

// plainCanceled 造一个无 Cause 的裸取消 ctx（进程停机的形态）。
func plainCanceled(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancel()
	return ctx
}

// TestRouteFourWaySplit 四分路由各一组 + 其余臂：Cause→终态的唯一收口。
func TestRouteFourWaySplit(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want finalize.Action
	}{
		{"接管不落终态回 created", withCause(t, steer.ErrSteered), finalize.ActionTakeover},
		{"用户取消落 cancelled", withCause(t, finalize.ErrUserCancelled), finalize.ActionCancelled},
		{"会话超时落 timed_out", withCause(t, finalize.ErrSessionDeadline), finalize.ActionTimedOut},
		{"进程停机不落终态", plainCanceled(t), finalize.ActionShutdown},
		{"其余落 failed", context.Background(), finalize.ActionFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := finalize.Route(tc.ctx); got != tc.want {
				t.Fatalf("Route = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRouteTimedOutBeatsShutdown 会话超时必须**先于**停机分支：超时 ctx 同样
// ctx.Err() != nil，次序反了该落 timed_out 的会话会被判成停机、不落终态
// 留给恢复循环——路由表的次序是语义本体，不是实现细节。
//
// 让墙钟**真的到期**（预设 Cause 只在到期时生效，手动 cancel() 是普通取消
// ——与宿主 WithTimeoutCause 的接线一致：超时由墙钟触发，不靠人按）。
func TestRouteTimedOutBeatsShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeoutCause(context.Background(), time.Millisecond, finalize.ErrSessionDeadline)
	t.Cleanup(cancel)
	<-ctx.Done()
	if ctx.Err() == nil {
		t.Fatal("前置条件失败：超时 ctx 应有 ctx.Err()（正是它会落进停机分支）")
	}
	if got := finalize.Route(ctx); got != finalize.ActionTimedOut {
		t.Fatalf("Route = %v, want timed_out——超时分支必须先于停机分支", got)
	}
}

// TestRoutePlainDeadlineIsShutdown 裸 deadline（未带 ErrSessionDeadline Cause）
// 归停机臂：会话超时必须 WithTimeoutCause 携带哨兵，否则超时会被误判成
// 停机、会话滞留 running。本用例钉住这条接线要求。
func TestRoutePlainDeadlineIsShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	t.Cleanup(cancel)
	cancel()
	if got := finalize.Route(ctx); got != finalize.ActionShutdown {
		t.Fatalf("Route = %v, want shutdown（裸 deadline 不携带超时哨兵）", got)
	}
}

// TestRouteCauseChainTolerant Cause 判定走 errors.Is：宿主包一层再发也判得中。
func TestRouteCauseChainTolerant(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	cancel(errors.Wrap(steer.ErrSteered, "注入链"))
	if got := finalize.Route(ctx); got != finalize.ActionTakeover {
		t.Fatalf("Route = %v, want takeover（errors.Is 应判穿包装链）", got)
	}
}

// TestActionStatus 该臂的收敛状态词映射 contract 词表；停机臂无目标态。
func TestActionStatus(t *testing.T) {
	cases := map[finalize.Action]string{
		finalize.ActionTakeover:  contract.StatusCreated,
		finalize.ActionCancelled: contract.StatusCancelled,
		finalize.ActionTimedOut:  contract.StatusTimedOut,
		finalize.ActionFailed:    contract.StatusFailed,
		finalize.ActionShutdown:  "",
	}
	for a, want := range cases {
		if got := a.Status(); got != want {
			t.Fatalf("%v.Status() = %q, want %q", a, got, want)
		}
	}
	if finalize.ActionShutdown.String() == "" {
		t.Fatal("Action.String 不得为空串（留痕与测试失败信息要用）")
	}
}

// TestDetachedCutsCancellationKeepsValues 脱离 ctx：切断取消传播、保留值。
func TestDetachedCutsCancellationKeepsValues(t *testing.T) {
	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "keep")
	ctx, cancel := context.WithCancel(parent)
	cancel() // 编排 ctx 已被取消
	d := finalize.Detached(ctx)
	if d.Err() != nil {
		t.Fatal("脱离 ctx 不得继承取消（否则终态写随编排 ctx 一起死）")
	}
	if got := d.Value(ctxKey{}); got != "keep" {
		t.Fatalf("脱离 ctx 丢失父值: %v", got)
	}
}

// TestRouteRecognizesContractDeadlineSentinel issue #333：内核 deadline 包装的
// ctx 与收尾侧判定必须指向同一个哨兵（contract.ErrSessionDeadline）。此前
// 两侧各写一份 errors.New("会话超过会话级时限")，同一 ctx 上 deadline 判
// timed_out、finalize 判 shutdown（Status() 为空 ⇒ 会话永久滞留 running）。
func TestRouteRecognizesContractDeadlineSentinel(t *testing.T) {
	if !errors.Is(finalize.ErrSessionDeadline, contract.ErrSessionDeadline) {
		t.Fatal("finalize.ErrSessionDeadline 必须是 contract.ErrSessionDeadline（同一 error 值）")
	}
	ctx := withCause(t, contract.ErrSessionDeadline)
	got := finalize.Route(ctx)
	if got != finalize.ActionTimedOut {
		t.Fatalf("契约哨兵的墙钟超时必须判 timed_out, got %s", got)
	}
	if status := got.Status(); status != contract.StatusTimedOut {
		t.Fatalf("必须落非空终态 contract.StatusTimedOut, got %q", status)
	}
}

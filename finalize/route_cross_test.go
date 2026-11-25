package finalize_test

// deadline.Route 与 finalize.Route 的**交叉断言**（issue #355）。
//
// 两包是同一收尾事实的两份投影：deadline.Outcome 是内核侧判据，finalize.Action
// 是收尾漏斗的动作。此前两包各钉各的用例、零交叉断言，接管臂因此无声分叉
// （deadline 判 shutdown 不落终态，finalize 判 takeover 落 created）——同一 ctx
// 两个相反结论，取决于调用哪个包。
//
// 本用例把同一组 ctx 同时喂给两包，断言两侧结论逐条一致。反向验证：
// 把 finalize 的接管映射删掉（或让 deadline 多认一个 cause），本用例必红。

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/deadline"
	"github.com/oagentkit/goagent-kernel/finalize"
	"github.com/oagentkit/goagent-kernel/steer"
)

// outcomeOf 收尾动作 → deadline 结论的**唯一映射**（两包同属一根收尾轴）。
//
// ActionFailed 是 finalize 对「没有特殊 cause」的缺省收尾（落 failed）；
// deadline 在 ctx 未取消时不做任何收敛（OutcomeNone）——两者是同一格的
// 两个投影：没有取消类 cause。其余四臂逐条同名对应。
func outcomeOf(a finalize.Action) deadline.Outcome {
	switch a {
	case finalize.ActionTakeover:
		return deadline.OutcomeTakeover
	case finalize.ActionCancelled:
		return deadline.OutcomeCancelled
	case finalize.ActionTimedOut:
		return deadline.OutcomeTimedOut
	case finalize.ActionShutdown:
		return deadline.OutcomeShutdown
	default:
		return deadline.OutcomeNone
	}
}

// crossWithCause 造一个已按 cause 取消的 ctx（宿主 WithCancelCause 的形态）。
func crossWithCause(t *testing.T, cause error) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	cancel(cause)
	return ctx
}

// TestDeadlineAndFinalizeRouteAgree 同一组 ctx（接管/超时/用户取消/裸停机/
// 正常）在两包得到一致结论（issue #355 验收 1）。
func TestDeadlineAndFinalizeRouteAgree(t *testing.T) {
	tctx, tcancel := context.WithTimeoutCause(context.Background(),
		5*time.Millisecond, contract.ErrSessionDeadline)
	t.Cleanup(tcancel)
	<-tctx.Done()

	cases := []struct {
		name string
		ctx  context.Context
	}{
		{"接管", crossWithCause(t, steer.ErrSteered)},
		{"会话超时", tctx},
		{"用户取消", crossWithCause(t, contract.ErrUserCancelled)},
		{"裸停机", plainCanceled(t)},
		{"正常", context.Background()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := finalize.Route(tc.ctx)
			want := outcomeOf(action)
			if got := deadline.Route(tc.ctx); got != want {
				t.Fatalf("同一 ctx 两侧结论分叉：finalize=%v（→ deadline.Outcome=%v），deadline=%v",
					action, want, got)
			}
		})
	}
}

// TestUserCancelledSentinelShared issue #355：用户取消哨兵也必须是全仓唯一
// 身份（contract.ErrUserCancelled），否则两包对同一 ctx 的 errors.Is 判定
// 互不认领。
func TestUserCancelledSentinelShared(t *testing.T) {
	if !errors.Is(finalize.ErrUserCancelled, contract.ErrUserCancelled) ||
		!errors.Is(contract.ErrUserCancelled, finalize.ErrUserCancelled) {
		t.Fatal("finalize.ErrUserCancelled 必须是 contract.ErrUserCancelled（同一 error 值）")
	}
	// 宿主经 WithCancelCause 携带契约哨兵：两包都必须判用户取消。
	ctx := crossWithCause(t, contract.ErrUserCancelled)
	if got := finalize.Route(ctx); got != finalize.ActionCancelled {
		t.Fatalf("契约哨兵的用户取消应判 cancelled, got %v", got)
	}
	if got := deadline.Route(ctx); got != deadline.OutcomeCancelled {
		t.Fatalf("契约哨兵的用户取消应判 cancelled, got %v", got)
	}

	// 宿主自有哨兵经 userCancel 透传口注入：两包必须同时认。
	hostSentinel := errors.New("api cancel")
	hctx := crossWithCause(t, hostSentinel)
	if got := finalize.Route(hctx, hostSentinel); got != finalize.ActionCancelled {
		t.Fatalf("宿主取消哨兵应判 cancelled, got %v", got)
	}
	if got := deadline.Route(hctx, hostSentinel); got != deadline.OutcomeCancelled {
		t.Fatalf("宿主取消哨兵应判 cancelled, got %v", got)
	}
}

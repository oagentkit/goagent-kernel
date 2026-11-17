package deadline

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/finalize"
)

// 收尾三分路由的契约判据（issue #170）：超时 / 用户取消 / 进程停机可区分，
// 且 timed_out 臂必须先于停机臂（次序纪律）。

// TestRouteThreeWaySplit 三分可区分：三种 cause 各归各的结论与终态词表。
func TestRouteThreeWaySplit(t *testing.T) {
	// ① 会话墙钟到点 → timed_out（deny-by-default：超时的产出不可信）。
	tctx, tcancel := WithCause(context.Background(), 3*time.Millisecond)
	defer tcancel()
	<-tctx.Done()
	if got := Route(tctx); got != OutcomeTimedOut {
		t.Fatalf("墙钟到点应判 timed_out, got %s", got)
	}
	if got := Route(tctx).Status(); got != contract.StatusTimedOut {
		t.Fatalf("timed_out 应落 contract.StatusTimedOut, got %q", got)
	}

	// ② 用户取消（宿主专属哨兵经 WithCancelCause 表达）→ cancelled。
	userSentinel := errors.New("caller asked to stop")
	uctx, ucancel := context.WithCancelCause(context.Background())
	ucancel(userSentinel)
	if got := Route(uctx, userSentinel); got != OutcomeCancelled {
		t.Fatalf("用户取消应判 cancelled, got %s", got)
	}
	if got := Route(uctx, userSentinel).Status(); got != contract.StatusCancelled {
		t.Fatalf("cancelled 应落 contract.StatusCancelled, got %q", got)
	}

	// ③ 进程停机（父 ctx 取消传播）→ 不落终态（留给恢复循环）。
	pctx, pcancel := context.WithCancel(context.Background())
	child, childCancel := context.WithCancel(pctx) // 轮 ctx 挂在关停链上
	pcancel()
	<-child.Done()
	childCancel()
	if got := Route(child); got != OutcomeShutdown {
		t.Fatalf("停机应判 shutdown（不落终态）, got %s", got)
	}
	if s := Route(child).Status(); s != "" {
		t.Fatalf("停机不得给出终态词, got %q", s)
	}

	// 未取消 → 无收敛动作。
	if got := Route(context.Background()); got != OutcomeNone || got.Status() != "" {
		t.Fatalf("未取消应判 none, got %s (%q)", got, got.Status())
	}

	// 不给 userCancel 哨兵时，只有内核缺省的用户取消哨兵
	//（finalize.ErrUserCancelled）判 cancelled；其余 cause 一律判停机
	//（裸 cancel 与停机在 Cause 上不可分——各档的前提是各有 cause）。
	if got := Route(uctx); got != OutcomeShutdown {
		t.Fatalf("未声明用户取消哨兵时应保守判停机, got %s", got)
	}
}

// 次序纪律（timeout 臂必须先于停机臂）的断言**只在 finalize 包里**
//（issue #355）：路由表只有那一份，本包的 Route 是它的投影——两份各写一遍的
// 判据曾让接管的 ctx 在两包得到相反结论，而零交叉断言时无声发生。
// 交叉断言（同一组 ctx 同时喂两包、断言结论一致）见 finalize/cross_test.go。

// TestRouteRecognizesCanonicalUserCancel 合并后的口径（issue #355）：内核缺省的
// 用户取消哨兵（finalize.ErrUserCancelled）判 cancelled——从前只有宿主显式注入
// 的哨兵才判，同一个专属 cause 在两包得到不同结论，正是本次要消掉的那类分叉。
func TestRouteRecognizesCanonicalUserCancel(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	cancel(finalize.ErrUserCancelled)
	if got := Route(ctx); got != OutcomeCancelled {
		t.Fatalf("内核缺省的用户取消哨兵应判 cancelled, got %s", got)
	}
}

// TestRouteMultipleUserCancelSentinels 用户取消哨兵可给多个（宿主常有
// 「API 取消」与「前端断开」两个入口），任一命中即判取消。
func TestRouteMultipleUserCancelSentinels(t *testing.T) {
	first := errors.New("api cancel")
	second := errors.New("client disconnect")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(second)
	if got := Route(ctx, first, second); got != OutcomeCancelled {
		t.Fatalf("第二个哨兵命中应判 cancelled, got %s", got)
	}
}

// TestSessionDeadlineSentinelSharedWithContract issue #333：墙钟超时只有
// 一个身份。deadline 的哨兵必须是 contract.ErrSessionDeadline 的别名，
// 否则收尾侧（用同一个值判定）认不出来——同一 ctx 上两侧结论相反。
func TestSessionDeadlineSentinelSharedWithContract(t *testing.T) {
	if !errors.Is(ErrSessionDeadline, contract.ErrSessionDeadline) ||
		!errors.Is(contract.ErrSessionDeadline, ErrSessionDeadline) {
		t.Fatal("deadline.ErrSessionDeadline 必须是 contract.ErrSessionDeadline（同一 error 值）")
	}

	ctx, cancel := WithCause(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()

	if got := Route(ctx); got != OutcomeTimedOut {
		t.Fatalf("墙钟到点必须判 timed_out, got %s", got)
	} else if got.Status() != contract.StatusTimedOut {
		t.Fatalf("timed_out 必须落 contract.StatusTimedOut, got %q", got.Status())
	}
}

package deadline

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/steer"
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

	// ②' 契约哨兵（contract.ErrUserCancelled）缺省即认：宿主不必再传参。
	cctx, ccancel := context.WithCancelCause(context.Background())
	ccancel(contract.ErrUserCancelled)
	if got := Route(cctx); got != OutcomeCancelled {
		t.Fatalf("契约用户取消哨兵应判 cancelled, got %s", got)
	}

	// ②'' 接管（steer.ErrSteered）→ takeover：不落终态回 created。
	// 这是 issue #355 的实测分叉点（此前 deadline 判 shutdown、finalize 判 takeover）。
	sctx, scancel := context.WithCancelCause(context.Background())
	scancel(steer.ErrSteered)
	if got := Route(sctx); got != OutcomeTakeover {
		t.Fatalf("接管应判 takeover, got %s", got)
	}
	if got := Route(sctx).Status(); got != contract.StatusCreated {
		t.Fatalf("takeover 应回迁 contract.StatusCreated, got %q", got)
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

	// 宿主自有哨兵不传进 userCancel 时，它与停机在 Cause 上不可分——
	// 仍判停机。要区分必须显式传哨兵，或改用契约哨兵 contract.ErrUserCancelled。
	if got := Route(uctx); got != OutcomeShutdown {
		t.Fatalf("未声明的宿主自有哨兵应保守判停机, got %s", got)
	}
}

// TestRouteDeadlineArmBeatsShutdownArm **次序纪律**：墙钟到点的 ctx 同样满足
// 停机臂的条件（ctx.Err() != nil），timed_out 臂必须先被判走——次序写反，
// 每次会话超时都会被判成停机、不落终态，会话永久滞留 running。
//
// 场景即任务判据：**停机形态的 ctx（已取消）+ 已过期锚点 → timed_out 而非停机臂**。
func TestRouteDeadlineArmBeatsShutdownArm(t *testing.T) {
	cfg := Config{Timeout: 30 * time.Minute, Shared: true}
	// 锚点还有几毫秒余量 → Begin 按剩余时长装饰出携带专属 cause 的轮 ctx。
	anchor := &Anchor{
		FirstClaimAt: time.Now().Add(-time.Minute),
		DeadlineAt:   time.Now().Add(3 * time.Millisecond),
	}
	rctx, release, constraint := cfg.Begin(context.Background(), anchor, time.Now())
	defer release()
	if constraint.Expired() {
		t.Fatal("锚点此刻仍有效，不该判过期")
	}

	<-rctx.Done()
	// 断言点打在「两个臂同时成立」的那一刻：ctx 已取消（停机臂条件），
	// 且锚点此刻已过期。
	if rctx.Err() == nil {
		t.Fatal("前提失效：ctx 应已取消（否则测不到臂序）")
	}
	if got := anchor.State(time.Now()); got != StateExpired {
		t.Fatalf("前提失效：锚点应已过期, got %s", got)
	}
	if got := Route(rctx); got != OutcomeTimedOut {
		t.Fatalf("次序纪律被破坏：已取消 + 锚点过期应走 timed_out 臂而非停机臂, got %s", got)
	}

	// 对照组：同样已取消，但 cause 来自停机（没有本包哨兵）→ 停机臂。
	// 两条同时成立才说明「区分靠 cause 与臂序」，不是「取消了一律 timed_out」。
	pctx, pcancel := context.WithCancel(context.Background())
	pctx2, c2 := context.WithCancel(pctx)
	pcancel()
	<-pctx2.Done()
	c2()
	if got := Route(pctx2); got != OutcomeShutdown {
		t.Fatalf("对照：停机 ctx 应走停机臂, got %s", got)
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

package deadline

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
)

// 会话墙钟的契约判据（issue #170）：shared 锚点跨续跑轮不刷新、过期锚点认领后
// 立即收敛不进编排、0=关闭直通、锚点持久化接缝 fail-closed。

// memStore 锚点持久化接缝的内存替身（计写入次数，判「只写一次」）。
type memStore struct {
	a       *Anchor
	saves   int
	loadErr error
	saveErr error
}

func (s *memStore) LoadAnchor(context.Context) (*Anchor, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.a, nil
}

func (s *memStore) SaveAnchor(_ context.Context, a Anchor) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	cp := a
	s.a = &cp
	return nil
}

// runRound 宿主一轮的最小形状（按包文档的次序）：
// Restore → Begin → 认领迁移 →（expired：立即 timed_out 收敛、不进编排）
// → Persist → 编排段。
//
// 它不是实现，是**文档承诺的接线方式**——契约测试钉的就是这条接线。
func runRound(ctx context.Context, store *memStore, cfg Config, claimed bool) (status string, entered bool) {
	anchor, err := Restore(ctx, store)
	if err != nil {
		return "error", false
	}
	rctx, release, expired := cfg.Begin(ctx, anchor, time.Now())
	defer release()
	if !claimed {
		return "", false // 认领零行：不产出任何东西、不写锚点
	}
	if expired {
		return OutcomeTimedOut.Status(), false // 认领迁移后立即收敛，不进编排
	}
	if _, err := Persist(ctx, store, cfg, anchor, time.Now()); err != nil {
		return "error", false
	}
	_ = rctx // 编排段跑在这里；收尾按 Route(rctx) 三分
	return "", true
}

// TestZeroTimeoutPassesThrough 0=关闭直通：不装饰 ctx、不写锚点、不判锚点。
func TestZeroTimeoutPassesThrough(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	cfg := Config{} // Timeout=0

	rctx, release, expired := cfg.Begin(ctx, &Anchor{DeadlineAt: time.Now().Add(-time.Hour)}, time.Now())
	defer release()
	if expired {
		t.Fatal("0=关闭时不得判过期（压根不看锚点）")
	}
	if _, ok := rctx.Deadline(); ok {
		t.Fatal("0=关闭的轮 ctx 不得带 deadline（直通）")
	}
	if rctx != ctx {
		t.Fatal("0=关闭应原样返回父 ctx")
	}
	if a, err := Persist(ctx, store, cfg, nil, time.Now()); err != nil || a != nil {
		t.Fatalf("0=关闭不得写锚点: (%v, %v)", a, err)
	}
	if store.saves != 0 {
		t.Fatalf("0=关闭不得触发持久化, saves=%d", store.saves)
	}
}

// TestSharedAnchorNotRefreshedAcrossRounds shared 锚点跨续跑轮**不刷新**：
// 总时限不因续跑轮重置——首轮写一次，续跑轮只读、只按剩余时长装饰。
func TestSharedAnchorNotRefreshedAcrossRounds(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	cfg := Config{Timeout: 30 * time.Minute, Shared: true}
	t0 := time.Now()

	// 首轮认领成功 → 写锚点（一次）。
	a1, err := Persist(ctx, store, cfg, nil, t0)
	if err != nil || a1 == nil {
		t.Fatalf("首轮应写锚点: (%v, %v)", a1, err)
	}
	if !a1.DeadlineAt.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("锚点应为 FirstClaimAt+Timeout, got %v", a1.DeadlineAt)
	}
	if store.saves != 1 {
		t.Fatalf("首轮只写一次, saves=%d", store.saves)
	}

	// 续跑轮 2、3：不写（沿用），且**不产生第二次持久化**。
	for _, now := range []time.Time{t0.Add(10 * time.Minute), t0.Add(20 * time.Minute)} {
		a, err := Persist(ctx, store, cfg, a1, now)
		if err != nil || a != nil {
			t.Fatalf("续跑轮不得重写锚点: (%v, %v)", a, err)
		}
	}
	if store.saves != 1 {
		t.Fatalf("锚点只写一次（刷新=总时限被重置）, saves=%d", store.saves)
	}
	if !store.a.DeadlineAt.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("库内锚点不得被刷新, got %v", store.a.DeadlineAt)
	}

	// 续跑轮 Begin：沿用锚点的**剩余**时长，不是新开整段 30 分钟。
	aged := &Anchor{FirstClaimAt: t0.Add(-20 * time.Minute), DeadlineAt: t0.Add(10 * time.Minute)}
	rctx, release, expired := cfg.Begin(ctx, aged, time.Now())
	defer release()
	if expired {
		t.Fatal("还有 10 分钟余量，不该判过期")
	}
	dl, ok := rctx.Deadline()
	if !ok {
		t.Fatal("shared 有效锚点必须装饰轮 ctx")
	}
	if rem := time.Until(dl); rem <= 0 || rem > 15*time.Minute {
		t.Fatalf("续跑轮应拿锚点剩余（≈10 分钟），不得新开整段 30 分钟: 剩余 %v", rem)
	}

	// 对照：非 shared 每轮各自新开整段（锚点不参与）。
	perRound := Config{Timeout: 30 * time.Minute}
	rctx2, release2, expired2 := perRound.Begin(ctx, aged, time.Now())
	defer release2()
	if expired2 {
		t.Fatal("非 shared 不判锚点过期")
	}
	if dl2, ok := rctx2.Deadline(); !ok || time.Until(dl2) <= 15*time.Minute {
		t.Fatalf("非 shared 应新开整段 30 分钟, deadline ok=%v", ok)
	}
}

// TestExpiredAnchorConvergesWithoutEnteringOrchestration 过期锚点的续跑轮：
// 认领迁移成功后**立即 timed_out 收敛、不进编排**（认领排队窗口已耗完余量）。
func TestExpiredAnchorConvergesWithoutEnteringOrchestration(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Timeout: 30 * time.Minute, Shared: true}
	store := &memStore{a: &Anchor{
		FirstClaimAt: time.Now().Add(-time.Hour),
		DeadlineAt:   time.Now().Add(-time.Minute), // 已过期
	}}

	status, entered := runRound(ctx, store, cfg, true)
	if entered {
		t.Fatal("过期锚点的续跑轮不得进编排")
	}
	if status != contract.StatusTimedOut {
		t.Fatalf("应立即收敛为 timed_out, got %q", status)
	}
	if store.saves != 0 {
		t.Fatalf("过期收敛轮不得写锚点, saves=%d", store.saves)
	}

	// 认领零行（他路已接手）：什么都不产出，也不该顺手写锚点。
	store2 := &memStore{a: store.a}
	if status, entered := runRound(ctx, store2, cfg, false); status != "" || entered {
		t.Fatalf("认领失败不产出任何东西, got (%q, %v)", status, entered)
	}
	if store2.saves != 0 {
		t.Fatalf("认领失败不得写锚点, saves=%d", store2.saves)
	}
}

// TestFirstRoundWritesAnchorAndContinuationsReuseIt 端到端接线：首轮写、
// 续跑轮读且不重写，编排段照常进入。
func TestFirstRoundWritesAnchorAndContinuationsReuseIt(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Timeout: 30 * time.Minute, Shared: true}
	store := &memStore{}

	if status, entered := runRound(ctx, store, cfg, true); status != "" || !entered {
		t.Fatalf("首轮应进编排且无终态, got (%q, %v)", status, entered)
	}
	if store.saves != 1 || store.a == nil {
		t.Fatalf("首轮应写一次锚点, saves=%d anchor=%v", store.saves, store.a)
	}

	// 续跑轮（锚点仍有效）：进编排、不重写。
	if status, entered := runRound(ctx, store, cfg, true); status != "" || !entered {
		t.Fatalf("续跑轮应进编排且无终态, got (%q, %v)", status, entered)
	}
	if store.saves != 1 {
		t.Fatalf("续跑轮不得刷新锚点, saves=%d", store.saves)
	}
}

// TestAnchorStateThreeStates 锚点三态判定（纯函数）。
func TestAnchorStateThreeStates(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		anchor *Anchor
		want   State
	}{
		{name: "nil = 未写", anchor: nil, want: StateUnwritten},
		{name: "零值锚点 = 未写（存层没写全，不是 1970 就过期）", anchor: &Anchor{}, want: StateUnwritten},
		{name: "未到点 = 有效", anchor: &Anchor{DeadlineAt: now.Add(time.Second)}, want: StateLive},
		{name: "已过点 = 过期", anchor: &Anchor{DeadlineAt: now.Add(-time.Second)}, want: StateExpired},
		{name: "恰好到点 = 过期（余量为 0 不进编排）", anchor: &Anchor{DeadlineAt: now}, want: StateExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.anchor.State(now); got != tc.want {
				t.Fatalf("State() = %s, want %s", got, tc.want)
			}
		})
	}
	var nilAnchor *Anchor
	if got := nilAnchor.Remaining(now); got != 0 {
		t.Fatalf("nil 锚点剩余应为 0, got %v", got)
	}
	// 零值锚点 Begin：按未写处理，新开一段 Timeout（不是立刻过期）。
	cfg := Config{Timeout: time.Minute, Shared: true}
	rctx, release, expired := cfg.Begin(context.Background(), &Anchor{}, now)
	defer release()
	if expired {
		t.Fatal("零值锚点不得判过期")
	}
	if _, ok := rctx.Deadline(); !ok {
		t.Fatal("零值锚点应新开一段 Timeout")
	}
}

// TestStoreSeamFailsClosed 持久化接缝未注入时 fail-closed：shared 模式下
// 漏写/漏读锚点 = 总时限静默重置，必须响亮报错而不是静默跳过。
func TestStoreSeamFailsClosed(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Timeout: time.Minute, Shared: true}

	if _, err := Restore(ctx, nil); !errors.Is(err, ErrNoStore) {
		t.Fatalf("Restore 未注入接缝应报 ErrNoStore, got %v", err)
	}
	if _, err := Persist(ctx, nil, cfg, nil, time.Now()); !errors.Is(err, ErrNoStore) {
		t.Fatalf("Persist 需要写入时未注入接缝应报 ErrNoStore, got %v", err)
	}
	// 不需要写入时不要求接缝（非 shared / 0=关闭 / 已有锚点）。
	if a, err := Persist(ctx, nil, Config{}, nil, time.Now()); err != nil || a != nil {
		t.Fatalf("0=关闭不该要求接缝: (%v, %v)", a, err)
	}
	if a, err := Persist(ctx, nil, Config{Timeout: time.Minute}, nil, time.Now()); err != nil || a != nil {
		t.Fatalf("非 shared 不该要求接缝: (%v, %v)", a, err)
	}
	existing := &Anchor{DeadlineAt: time.Now().Add(time.Minute)}
	if a, err := Persist(ctx, nil, cfg, existing, time.Now()); err != nil || a != nil {
		t.Fatalf("已有锚点不写、不该要求接缝: (%v, %v)", a, err)
	}

	// 存层错误原样上抛（不静默当「未写」）。
	boom := errors.New("store unavailable")
	if _, err := Restore(ctx, &memStore{loadErr: boom}); !errors.Is(err, boom) {
		t.Fatalf("读失败应原样上抛, got %v", err)
	}
	if _, err := Persist(ctx, &memStore{saveErr: boom}, cfg, nil, time.Now()); !errors.Is(err, boom) {
		t.Fatalf("写失败应原样上抛, got %v", err)
	}
	// 未写 → (nil, nil)（续跑轮首轮语义，不是零值锚点）。
	if a, err := Restore(ctx, &memStore{}); err != nil || a != nil {
		t.Fatalf("未写应返回 (nil, nil), got (%v, %v)", a, err)
	}
}

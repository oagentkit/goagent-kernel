package deadline

// Begin 返回值的三态可辨（issue #318）。
//
// 修前 Begin 用单个 bool 表达「本轮时限状态」，把至少三态塌缩成二值：
//   - Timeout<=0 关闭直通（本轮无限跑）；
//   - 本轮受约束（ctx 带 Deadline）；
//   - 本轮已过期。
// 前两者返回值完全相同（false），调用方无法从返回值判断本轮有没有墙钟保护，
// 只能去猜 ctx.Deadline()——而父 ctx 自带的 deadline 会污染该信号。
//
// 修法：返回包内一等公民 `Constraint`（off/active/expired），与 Anchor.State
// 的三态判定同源，不新增概念。

import (
	"context"
	"testing"
	"time"
)

func TestBeginDistinguishesThreeConstraints(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// 1) 关闭直通：本轮不受约束，原样 ctx。
	off, releaseOff, cOff := Config{Timeout: 0}.Begin(ctx, nil, now)
	defer releaseOff()
	if cOff != ConstraintOff {
		t.Fatalf("Timeout=0 应为 ConstraintOff，实得 %s", cOff)
	}
	if _, ok := off.Deadline(); ok {
		t.Fatal("关闭直通不得装饰 deadline")
	}

	// 2) 非 shared：本轮新开一段时限。
	active, releaseActive, cActive := Config{Timeout: time.Minute}.Begin(ctx, nil, now)
	defer releaseActive()
	if cActive != ConstraintActive {
		t.Fatalf("非 shared 应为 ConstraintActive，实得 %s", cActive)
	}
	if _, ok := active.Deadline(); !ok {
		t.Fatal("受约束轮必须带 deadline")
	}

	// 3) shared + 有效锚点：沿用剩余时长，仍是受约束。
	live, releaseLive, cLive := Config{Timeout: time.Minute, Shared: true}.Begin(
		ctx, &Anchor{DeadlineAt: now.Add(30 * time.Second)}, now)
	defer releaseLive()
	if cLive != ConstraintActive {
		t.Fatalf("shared+live 应为 ConstraintActive，实得 %s", cLive)
	}
	if _, ok := live.Deadline(); !ok {
		t.Fatal("shared+live 必须带 deadline")
	}

	// 4) shared + 过期锚点：已过期，不装饰。
	exp, releaseExp, cExp := Config{Timeout: time.Minute, Shared: true}.Begin(
		ctx, &Anchor{DeadlineAt: now.Add(-time.Second)}, now)
	defer releaseExp()
	if cExp != ConstraintExpired {
		t.Fatalf("shared+expired 应为 ConstraintExpired，实得 %s", cExp)
	}
	if _, ok := exp.Deadline(); ok {
		t.Fatal("已过期轮不得装饰 deadline")
	}

	// 核心判据：关闭直通与受约束必须可区分（修前两者都是 false）。
	if cOff == cActive || cOff == cLive {
		t.Fatalf("本轮不受约束与受约束返回了同一态：off=%s active=%s live=%s", cOff, cActive, cLive)
	}
	if cOff.Constrained() || !cActive.Constrained() || !cLive.Constrained() {
		t.Fatalf("Constrained() 口径错：off=%v active=%v live=%v",
			cOff.Constrained(), cActive.Constrained(), cLive.Constrained())
	}
	if !cExp.Expired() || cOff.Expired() || cActive.Expired() {
		t.Fatalf("Expired() 口径错：off=%v active=%v exp=%v",
			cOff.Expired(), cActive.Expired(), cExp.Expired())
	}
}

// TestBeginConstraintStrings 机读名稳定（进日志与测试断言）。
func TestBeginConstraintStrings(t *testing.T) {
	cases := map[Constraint]string{
		ConstraintOff:     "off",
		ConstraintActive:  "active",
		ConstraintExpired: "expired",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("Constraint(%d).String() = %q, want %q", c, got, want)
		}
	}
}

package sideeffect

import (
	"context"
	"testing"
	"time"
)

// TestResolveNotAppliedOpensGateImmediately 交叉断言（#218）：裁决
// 「未生效」的返回值与随后 Acquire 的判据必须同一口径——都表达「门已打开」。
//
// 契约（Store.ResolveUncertain 第 2 条）把这一侧钉为 OutcomeApplied：
// 裁决成功且门已打开，不是 OutcomeContended（那会把「可执行了」误报成
// 「没轮上」）。两套参考实现（本包 fakeStore 与 sidetest 的 memStore）
// 必须给出同一个答案，否则调用方按其中一份写的 `if outcome == OutcomeApplied`
// 在另一份上会走错分支。
func TestResolveNotAppliedOpensGateImmediately(t *testing.T) {
	st := newFakeStore()
	ctx := context.Background()
	a := act("k1", "res-a")

	_, lease, _, err := st.Acquire(ctx, a, time.Minute)
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if err := st.MarkUncertain(ctx, a, lease, "响应丢了"); err != nil {
		t.Fatalf("MarkUncertain 失败: %v", err)
	}
	if err := st.Release(ctx, a, lease); err != nil {
		t.Fatalf("Release 失败: %v", err)
	}

	outcome, _, err := st.ResolveUncertain(ctx, a, Resolution{Applied: false, Reason: "查下游：未生效"})
	if err != nil {
		t.Fatalf("裁决「未生效」失败: %v", err)
	}
	if outcome != OutcomeApplied {
		t.Fatalf("裁决「未生效」应返回 OutcomeApplied（门已打开），实得 %v", outcome)
	}

	// 交叉断言：紧接着 Acquire 必须判为 applied（与裁决返回值同一口径），
	// 而不是 contended（那意味着门还关着，调用方会误退避）。
	got, _, _, err := st.Acquire(ctx, a, time.Minute)
	if err != nil {
		t.Fatalf("裁决后的 Acquire 失败: %v", err)
	}
	if got != OutcomeApplied {
		t.Fatalf("裁决「未生效」后立刻 Acquire 应得 applied，实得 %v", got)
	}
}

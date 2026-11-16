package tooldisclose

// 门禁恢复路径（issue #286）。
//
// 旧实现把「已加载」的来源设成「被拒的调用尝试」：第一次 Check 顺手 Load，
// 并给出「以相同参数直接重新调用」的指引 ⇒ 模型不取 schema、原样重试就放行，
// 渐进披露的整道闸被自身恢复路径绕开。已加载必须只由**schema 交付事件**改写。

import (
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// TestGateRepeatedCheckNeverSelfActivates 未交付 schema 前，重复调用始终被拒。
func TestGateRepeatedCheckNeverSelfActivates(t *testing.T) {
	c := NewCatalog(tools())
	g := NewGate(c)

	for i := 0; i < 3; i++ {
		err := g.Check("alpha_lookup")
		if err == nil {
			t.Fatalf("第 %d 次调用不得放行（schema 从未交付）", i+1)
		}
		if !errors.Is(err, ErrNotLoaded) {
			t.Fatalf("第 %d 次应命中 ErrNotLoaded，实得 %v", i+1, err)
		}
		if c.IsLoaded("alpha_lookup") {
			t.Fatalf("第 %d 次被拒后门禁不得自行激活（已加载必须是 schema 交付事件）", i+1)
		}
		if ge := gateErr(t, err); ge.Activated {
			t.Fatalf("第 %d 次拒绝不应声称有激活副作用: %+v", i+1, ge)
		}
	}

	// schema 真正交付（检索/取回侧调 Load）之后，同名调用才放行。
	if _, ok := c.Load("alpha_lookup"); !ok {
		t.Fatal("目录内工具应可交付 schema")
	}
	if err := g.Check("alpha_lookup"); err != nil {
		t.Fatalf("schema 交付后应放行，实得 %v", err)
	}
}

// TestGateGuidanceDoesNotTeachBypass 拒绝指引不得教模型跳过 schema 取回、原样重试。
func TestGateGuidanceDoesNotTeachBypass(t *testing.T) {
	g := NewGate(NewCatalog(tools()))
	got := Guidance(g.Check("alpha_lookup"))
	for _, banned := range []string{"相同参数", "直接重新调用", "重新调用同一工具"} {
		if strings.Contains(got, banned) {
			t.Fatalf("指引不得含跳过取回的措辞 %q: %q", banned, got)
		}
	}
	for _, want := range []string{"取回", "schema", "alpha_lookup"} {
		if !strings.Contains(got, want) {
			t.Fatalf("指引应说明正确恢复路径（含 %q），实得 %q", want, got)
		}
	}
}

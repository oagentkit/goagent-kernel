package tooldisclose

// 门禁指引（模型侧）的用例。夹具用合成工具名，不带领域味。
//
// 与 gate_test.go 分文件：那里钉的是机器侧契约（哨兵二分 / 重试性 / 放行判据与交付同源），
// 这里钉的是模型侧契约（拒绝时模型拿到的话能不能指导它做对下一步）。
// 两组用例都必须存在——机器侧全对而模型侧缺位，是这个包最容易出现的一种半成品。

import (
	"context"
	"strings"
	"testing"

	"github.com/oagentkit/guardx/invoke"
	"github.com/oekit/pkg/errors"
)

// TestGuidanceDirectsRetrievalNotBlindRetry 门禁的两类结局要求模型做的动作恰好相反，
// 指引必须把这件事说死；且「未加载」的指引必须教**先取回 schema**，不得教跳过取回（#286）。
//
// 旧措辞「本次调用已把它激活，请以相同参数直接重新调用同一工具」与占位面的指令方向相反：
// 占位面教「先用检索工具取回 schema」，拒绝面却教「原样重试」——模型照拒绝面走，
// 同一组编造参数第二次就放行，「先取回再调用」这道闸被自己的恢复路径绕开。
func TestGuidanceDirectsRetrievalNotBlindRetry(t *testing.T) {
	c := catalogOf("alpha_lookup")
	g := NewGate(c)

	err := g.Check("alpha_lookup")
	if err == nil {
		t.Fatal("未加载的调用必须被拒")
	}
	loaded := Guidance(err)
	// 指引必须指回「先取回 schema」：与占位面的取回指令同一个方向。
	if !strings.Contains(loaded, "取回") {
		t.Fatalf("未加载的指引必须教先取回 schema（与占位面同向），实得 %q", loaded)
	}
	// 反向验证：不得出现「以相同参数直接重新调用」这类跳过取回的措辞——
	// 那正是 #286 的缺陷本身。把文案改回旧措辞，这条必红。
	if strings.Contains(loaded, "相同参数") || strings.Contains(loaded, "直接重新调用") {
		t.Fatalf("指引不得教模型跳过 schema 取回（#286），实得 %q", loaded)
	}
	if strings.Contains(loaded, "不要重试") {
		t.Fatalf("可恢复的结局不得出现「不要重试」，实得 %q", loaded)
	}

	err = g.Check("ghost_tool")
	if err == nil {
		t.Fatal("不在目录的调用必须被拒")
	}
	ghost := Guidance(err)
	if !strings.Contains(ghost, "不要重试") {
		t.Fatalf("不在目录不可恢复，指引必须明说不要重试，实得 %q", ghost)
	}
	if strings.Contains(ghost, "取回") {
		t.Fatalf("不在目录谈不上取回 schema，指引不得张冠李戴，实得 %q", ghost)
	}

	// 反证：两句话给出的指令必须真的相反，而不是同一句话换个说法——
	// 若将来有人把指引写成模板套话，这条会立刻变红。
	if loaded == ghost {
		t.Fatal("两类结局的指引必须不同，否则指引没有承载二分")
	}
}

// TestGuidanceCarriesToolNameForAudit 指引要同时服务模型与留痕两头的需要，
// 所以工具名不能因为「写给模型看」而丢掉。
func TestGuidanceCarriesToolNameForAudit(t *testing.T) {
	c := catalogOf("alpha_lookup", "beta_export")
	g := NewGate(c)

	for _, name := range []string{"alpha_lookup", "beta_export"} {
		err := g.Check(name)
		if err == nil {
			t.Fatalf("%s 未加载时应被拒", name)
		}
		if !strings.Contains(Guidance(err), name) {
			t.Fatalf("指引应带出工具名 %q（审计要回答「哪次调用被拒」），实得 %q", name, Guidance(err))
		}
	}
}

// TestGuidanceDoesNotFabricate 非门禁错误返回空串，本包不替别人的失败编措辞。
func TestGuidanceDoesNotFabricate(t *testing.T) {
	if got := Guidance(errors.New("srv-beta 连接被重置")); got != "" {
		t.Fatalf("非门禁错误应返回空串，实得 %q", got)
	}
	if got := Guidance(nil); got != "" {
		t.Fatalf("nil 应返回空串，实得 %q", got)
	}
	// 认不出的门禁结局也不能交白卷：空指引会让模型看到一条没有内容的拒绝。
	ge := newGateError("alpha_lookup", errors.New("某种没登记过的结局"), false, false)
	if got := ge.Guidance(); strings.TrimSpace(got) == "" {
		t.Fatal("未登记的结局也应给出中性指引，不能是空串")
	}
	var nilGE *GateError
	if got := nilGE.Guidance(); got != "" {
		t.Fatalf("nil 接收者应返回空串，实得 %q", got)
	}
}

// TestStageReasonCarriesGuidance 指引必须有**确定消费方**。
//
// 链上 Verdict.Reason 是宿主把这环结论交出去的唯一出口，绝大多数宿主会把它
// 原样送进模型可见的失败消息。若这里回的是裸哨兵文案，Guidance 就退化成
// 「建了没人读」的死代码——正是我们批评过的失败形态。
func TestStageReasonCarriesGuidance(t *testing.T) {
	cat := catalogOf("alpha_lookup")
	st := Stage(cat)

	v, err := st.Check(context.Background(), invoke.Call{Name: "alpha_lookup"})
	if err != nil {
		t.Fatalf("门禁环不应返回 error（约定是 (Verdict, nil)），实得 %v", err)
	}
	if v.Allow {
		t.Fatal("未加载的调用不得放行")
	}
	if !v.Retryable {
		t.Fatal("未加载结局应标为可重试")
	}
	if !strings.Contains(v.Reason, "取回") {
		t.Fatalf("链上 Reason 应教模型先取回 schema（#286），实得 %q", v.Reason)
	}
	if strings.Contains(v.Reason, "相同参数") || strings.Contains(v.Reason, "直接重新调用") {
		t.Fatalf("链上 Reason 不得教模型跳过取回（#286），实得 %q", v.Reason)
	}
	if !strings.Contains(v.Reason, "alpha_lookup") {
		t.Fatalf("链上 Reason 应保留工具名供留痕，实得 %q", v.Reason)
	}

	// 第二类结局：不可重试，且指引与第一类相反。
	v2, err := st.Check(context.Background(), invoke.Call{Name: "ghost_tool"})
	if err != nil {
		t.Fatalf("门禁环不应返回 error，实得 %v", err)
	}
	if v2.Retryable {
		t.Fatal("不在目录的结局不得标为可重试")
	}
	if !strings.Contains(v2.Reason, "不要重试") {
		t.Fatalf("不可恢复结局的指引必须明说不要重试，实得 %q", v2.Reason)
	}

	// 未交付 schema 前，同一工具的第二次调用仍不得放行（#286 的核心回归）：
	// 拒绝不激活，放行判据只认交付事件。
	v3, err := st.Check(context.Background(), invoke.Call{Name: "alpha_lookup"})
	if err != nil {
		t.Fatalf("门禁环不应返回 error，实得 %v", err)
	}
	if v3.Allow {
		t.Fatal("未交付 schema 前第二次调用不得放行（拒绝不得激活，#286）")
	}
	// 交付事件（宿主 Load）发生后，同名调用才放行。
	if _, ok := cat.Load("alpha_lookup"); !ok {
		t.Fatal("前置失败：交付应成功")
	}
	v4, err := st.Check(context.Background(), invoke.Call{Name: "alpha_lookup"})
	if err != nil || !v4.Allow {
		t.Fatalf("交付后重试应放行；实得 allow=%v err=%v", v4.Allow, err)
	}
}

// TestStageNilCatalogStillRejectsWithGuidance 未装配目录时 fail-closed，
// 且拒绝也要给出指引——否则宿主漏装配目录时，模型拿到的是一句没有出处的拒绝。
func TestStageNilCatalogStillRejectsWithGuidance(t *testing.T) {
	st := Stage(nil)
	v, err := st.Check(context.Background(), invoke.Call{Name: "alpha_lookup"})
	if err != nil {
		t.Fatalf("门禁环不应返回 error，实得 %v", err)
	}
	if v.Allow {
		t.Fatal("未装配目录时不得放行（fail-closed）")
	}
	if v.Retryable {
		t.Fatal("未装配目录属不可恢复，不应标为可重试")
	}
	if strings.TrimSpace(v.Reason) == "" {
		t.Fatal("fail-closed 路径同样要有可读的 Reason")
	}
}

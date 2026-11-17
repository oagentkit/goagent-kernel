// reconcile_test.go —— 恢复对账判据的行为钉住（v0.33.0，issue #90）。
//
// # 这组用例钉的是什么
//
// 「快照说已完成」与「闸说结果未知」此前可以同时成立——恢复按快照跳过，
// 那个副作用既没对账也没重做，失效是静默的。本组用例逐格钉住对账判据：
// **坏组合必红**（阻塞、点名、可读原因），**好组合必绿**（不误伤）。
// 只测「好实现能过」的用例等于没钉：判据失灵成「永远放行」时它们照样全绿。
package checkpoint

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

func recGraph() []StepDef {
	return graph("step-alpha", "step-beta", "step-gamma")
}

func recPlan() Plan {
	s := snap("run", 2, contract.StatusRunning, "step-alpha", "step-beta")
	return BuildPlan(recGraph(), &s) // Skip=[alpha,beta] Run=[gamma]
}

// TestReconcileConflictBlocksOnUncertain 负例（issue #90 的核心形态）：
// 快照说已完成、闸说结果未知 ⇒ 必须阻塞，且给出可读原因，不得静默跳过。
func TestReconcileConflictBlocksOnUncertain(t *testing.T) {
	p := Reconcile(recPlan(), []GateView{
		{Step: "step-beta", State: GateUncertain, Detail: "Key run:step-beta 处于结果未知态"},
	})
	if !p.Blocked() {
		t.Fatal("「快照说完成、闸说未知」的坏组合必须红（阻塞），实得放行")
	}
	if len(p.Skip) != 0 || len(p.Run) != 0 {
		t.Fatalf("冲突时不得给出半截计划: Skip=%v Run=%v", p.Skip, p.Run)
	}
	if !p.HasAnomaly(AnomalySideEffectConflict) {
		t.Fatalf("应报冲突异常: %+v", p.Anomalies)
	}
	a := p.Anomalies[len(p.Anomalies)-1]
	if a.Step != "step-beta" {
		t.Fatalf("应点名冲突步骤，实得 %q", a.Step)
	}
	for _, want := range []string{"step-beta", "uncertain", "对账"} {
		if !strings.Contains(a.Detail, want) {
			t.Errorf("冲突原因应含 %q（要能照着做），实得 %q", want, a.Detail)
		}
	}
}

// TestReconcileConflictVariants 其余坏组合逐格：未裁决尝试 / 闸说没做过 /
// 判不了 / 未登记状态值——全部阻塞（判不了不是放行的理由）。
func TestReconcileConflictVariants(t *testing.T) {
	cases := []struct {
		state GateState
		why   string
	}{
		{GateInFlight, "有一次尚未裁决的尝试"},
		{GateUnrecorded, "闸侧没有任何「已做过」的记录"},
		{GateUnknown, "无法判定"},
		{GateState("bogus"), "未登记的状态值按冲突处理"},
	}
	for _, tc := range cases {
		p := Reconcile(recPlan(), []GateView{{Step: "step-alpha", State: tc.state}})
		if !p.Blocked() {
			t.Errorf("状态 %q 必须阻塞（%s），实得放行", tc.state, tc.why)
		}
		if !p.HasAnomaly(AnomalySideEffectConflict) {
			t.Errorf("状态 %q 应报冲突异常", tc.state)
		}
	}
}

// TestReconcileNoConflictKeepsPlan 好组合不误伤：已结算 / 无副作用动作的
// 跳过项照常跳过，Run 照常可执行。
func TestReconcileNoConflictKeepsPlan(t *testing.T) {
	p := Reconcile(recPlan(), []GateView{
		{Step: "step-alpha", State: GateSettled, Detail: "闸记了已完成并有结果"},
		{Step: "step-beta", State: GateAbsent, Detail: "该步骤是纯计算，无副作用动作"},
	})
	if p.Blocked() {
		t.Fatalf("已结算/无动作不构成冲突: %+v", p.Anomalies)
	}
	if !reflect.DeepEqual(p.Skip, []string{"step-alpha", "step-beta"}) {
		t.Fatalf("跳过集应保持，实得 %v", p.Skip)
	}
	if !reflect.DeepEqual(p.Run, []string{"step-gamma"}) {
		t.Fatalf("待执行集应保持，实得 %v", p.Run)
	}
	if len(p.Anomalies) != 0 {
		t.Fatalf("不应有异常: %+v", p.Anomalies)
	}
}

// TestReconcileIgnoresRunSide Run 侧的闸态不构成冲突：执行时闸自己会拒绝
// （OutcomeUncertain / Contended）——那是「至多一次」的正常运转，不是恢复
// 判定要拦的。两种判据各管各的执行点。
func TestReconcileIgnoresRunSide(t *testing.T) {
	p := Reconcile(recPlan(), []GateView{
		{Step: "step-gamma", State: GateUncertain, Detail: "待执行步骤带着未知态"},
	})
	if p.Blocked() {
		t.Fatalf("Run 侧的闸态不是恢复冲突: %+v", p.Anomalies)
	}
	if len(p.Run) != 1 || p.Run[0] != "step-gamma" {
		t.Fatalf("计划应保持: %+v", p)
	}
}

// TestReconcileIgnoresUnplannedSteps 视图里的步骤不在跳过集（不在图内 /
// 本来就要跑）时不误伤——对账只判「按快照跳过」这一侧。
func TestReconcileIgnoresUnplannedSteps(t *testing.T) {
	p := Reconcile(recPlan(), []GateView{
		{Step: "step-removed", State: GateUncertain}, // 图收缩掉的步骤
		{Step: "step-gamma", State: GateInFlight},    // 待执行侧
	})
	if p.Blocked() {
		t.Fatalf("非跳过侧的视图不构成冲突: %+v", p.Anomalies)
	}
}

// TestReconcileMultipleConflictsNamed 逐冲突点名，不只报第一条。
func TestReconcileMultipleConflictsNamed(t *testing.T) {
	p := Reconcile(recPlan(), []GateView{
		{Step: "step-alpha", State: GateUncertain},
		{Step: "step-beta", State: GateInFlight},
	})
	if !p.Blocked() {
		t.Fatal("多冲突必须阻塞")
	}
	named := map[string]bool{}
	for _, a := range p.Anomalies {
		if a.Kind == AnomalySideEffectConflict {
			named[a.Step] = true
		}
	}
	if !named["step-alpha"] || !named["step-beta"] {
		t.Fatalf("两个冲突步骤都应被点名: %+v", p.Anomalies)
	}
}

// TestReconcileDeterministic 同一输入必得同一结论（对账可复跑的基础）。
func TestReconcileDeterministic(t *testing.T) {
	views := []GateView{{Step: "step-beta", State: GateUncertain}}
	first := Reconcile(recPlan(), views)
	for i := 0; i < 20; i++ {
		if got := Reconcile(recPlan(), views); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次结果不一致:\n%+v\n%+v", i, first, got)
		}
	}
}

// TestReconcileDoesNotAliasInputSlices 返回的 Plan 不得与入参共享 Skip / Run /
// Anomalies 的底层数组（issue #380：「纯函数，无副作用」的字面承诺）。
func TestReconcileDoesNotAliasInputSlices(t *testing.T) {
	// Skip / Run：无冲突路径原样带回，但必须是独立切片。
	p1 := Plan{Skip: []string{"s1"}, Run: []string{"s2"}}
	p2 := Reconcile(p1, []GateView{{Step: "s1", State: GateSettled}})
	if len(p2.Skip) != 1 || len(p2.Run) != 1 {
		t.Fatalf("好组合应保持计划: %+v", p2)
	}
	p2.Skip[0] = "MUTATED_SKIP"
	p2.Run[0] = "MUTATED_RUN"
	if p1.Skip[0] != "s1" || p1.Run[0] != "s2" {
		t.Fatalf("返回值与入参共享 Skip/Run：%v / %v", p1.Skip, p1.Run)
	}

	// Anomalies：冲突路径会 addAnomaly。入参留一份余量（len=1 cap=2），
	// 用窗口观察 append 是否写进入参底层数组。
	p3 := Plan{
		Skip:      []string{"s1"},
		Run:       []string{"s2"},
		Anomalies: make([]Anomaly, 1, 2),
	}
	p3.Anomalies[0] = Anomaly{Kind: "prior", Step: "s0", Detail: "原有异常"}
	sibling := p3.Anomalies[:cap(p3.Anomalies)]

	p4 := Reconcile(p3, []GateView{{Step: "s1", State: GateUncertain}})
	if !p4.Blocked() {
		t.Fatal("冲突应阻塞")
	}
	if sibling[1] != (Anomaly{}) {
		t.Fatalf("addAnomaly 的 append 写穿了入参底层数组: %+v", sibling[1])
	}
	p4.Anomalies[0].Detail = "MUTATED_ANOMALY"
	if p3.Anomalies[0].Detail != "原有异常" {
		t.Fatalf("返回值与入参共享 Anomalies: %+v", p3.Anomalies[0])
	}
}

// TestReconcileDoesNotMutateInput 输入计划不被就地改写（调用方拿原计划
// 做对照时不能被偷改）。
func TestReconcileDoesNotMutateInput(t *testing.T) {
	p := recPlan()
	skipBefore := append([]string(nil), p.Skip...)
	runBefore := append([]string(nil), p.Run...)
	_ = Reconcile(p, []GateView{{Step: "step-alpha", State: GateUncertain}})
	if !reflect.DeepEqual(p.Skip, skipBefore) || !reflect.DeepEqual(p.Run, runBefore) {
		t.Fatalf("Reconcile 改写了输入计划: Skip=%v Run=%v", p.Skip, p.Run)
	}
}

// reconcile_alias_test.go —— Reconcile 的「纯函数，无副作用」按字面兑现
// （issue #380）。
//
// 此前返回值与入参共享 Skip / Run / Anomalies 三个切片的底层数组，
// addAnomaly 的 append 还会写穿入参的底层数组——声明与实现不符。
package checkpoint

import (
	"slices"
	"testing"
)

// dirtyPlan 一条入参计划：三条切片都带内容，且 Anomalies 留有余量
// （append 在有余量时才会写穿底层数组——没有余量就看不出写穿）。
func dirtyPlan() Plan {
	anomalies := make([]Anomaly, 1, 4)
	anomalies[0] = Anomaly{Kind: AnomalyDoneNotInGraph, Step: "step-x", Detail: "合成异常"}
	return Plan{
		Skip:      []string{"step-a"},
		Run:       []string{"step-b"},
		Anomalies: anomalies,
	}
}

// TestReconcileDoesNotShareSlicesWithInput 返回值不得与入参共享底层数组。
//
// 反向验证：去掉出口的 slices.Clone ⇒ 本条转红。
func TestReconcileDoesNotShareSlicesWithInput(t *testing.T) {
	p1 := dirtyPlan()
	p2 := Reconcile(p1, nil) // 无冲突：原样返回

	p2.Skip[0] = "mutated"
	p2.Run[0] = "mutated"
	p2.Anomalies[0] = Anomaly{Kind: AnomalyEmptyStepID}

	if p1.Skip[0] != "step-a" {
		t.Fatalf("返回值与入参共享了 Skip 底层数组：%v", p1.Skip)
	}
	if p1.Run[0] != "step-b" {
		t.Fatalf("返回值与入参共享了 Run 底层数组：%v", p1.Run)
	}
	if p1.Anomalies[0].Kind != AnomalyDoneNotInGraph {
		t.Fatalf("返回值与入参共享了 Anomalies 底层数组：%+v", p1.Anomalies[0])
	}
}

// TestReconcileAnomalyAppendDoesNotWriteThroughInput addAnomaly 的 append
// 不得写穿入参的底层数组：入参的异常条数与余量位置都必须原样。
//
// 反向验证：去掉 addAnomaly 内部的 slices.Clone ⇒ 本条转红。
func TestReconcileAnomalyAppendDoesNotWriteThroughInput(t *testing.T) {
	p1 := dirtyPlan()
	// 闸侧视图与快照冲突（结算未落账）⇒ Reconcile 要追加一条异常。
	views := []GateView{{Step: "step-a", State: GateUncertain, Detail: "合成视图"}}
	p2 := Reconcile(p1, views)

	if !p2.HasAnomaly(AnomalySideEffectConflict) {
		t.Fatalf("冲突必须报出 side_effect_conflict，实得 %+v", p2.Anomalies)
	}
	if len(p1.Anomalies) != 1 {
		t.Fatalf("入参的异常条数不得变化：%d", len(p1.Anomalies))
	}
	// 余量位置必须仍是零值：append 写穿会把它填上。
	spare := p1.Anomalies[:cap(p1.Anomalies)]
	if spare[1] != (Anomaly{}) {
		t.Fatalf("addAnomaly 的 append 写穿了入参底层数组：%+v", spare[1])
	}
	// 入参的异常内容也不得被改写。
	if p1.Anomalies[0].Kind != AnomalyDoneNotInGraph {
		t.Fatalf("入参异常被改写：%+v", p1.Anomalies[0])
	}
}

// TestReconcileKeepsPlanContent 对账不得改内容（只加异常 / 整体作废）。
func TestReconcileKeepsPlanContent(t *testing.T) {
	p1 := dirtyPlan()
	p2 := Reconcile(p1, nil)
	if !slices.Equal(p2.Skip, p1.Skip) || !slices.Equal(p2.Run, p1.Run) {
		t.Fatalf("无冲突时对账不得改内容：Skip=%v Run=%v", p2.Skip, p2.Run)
	}
	if len(p2.Anomalies) != len(p1.Anomalies) {
		t.Fatalf("无冲突时异常条数不得变化：%d → %d", len(p1.Anomalies), len(p2.Anomalies))
	}
}

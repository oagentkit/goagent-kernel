// closure_test.go —— closure 判项对「点观察行」的豁免边界（issue #222）。
//
// 点事件没有括号段（设计事实）：checkpoint 提交与副作用点观察
// （replayed / contended）落成孤儿行不是「起点缺失」。旧实现只豁免
// KindCheckpoint，于是一次正常的「动作已完成、本次取回结果」就能把
// 健康轨迹判成不通过（假红）。
package eval

import (
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/trajectory"
)

// TestAssessClosureAcceptsSideEffectPointRows 健康轨迹含副作用点观察行 ⇒ 通过。
func TestAssessClosureAcceptsSideEffectPointRows(t *testing.T) {
	t0 := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	stream := []eventbus.Event{
		seEvent(eventbus.EventSideEffectGranted, t0, "act-a", "res-a", nil),
		seEvent(eventbus.EventSideEffectCompleted, t0.Add(50*time.Millisecond), "act-a", "res-a", nil),
		seEvent(eventbus.EventSideEffectReplayed, t0.Add(100*time.Millisecond), "act-a", "res-a", nil),
		seEvent(eventbus.EventSideEffectContended, t0.Add(200*time.Millisecond), "act-c", "res-c", nil),
	}
	rows := Replay(stream, trajectory.Options{})
	// 前提：replayed / contended 确实落成孤儿行（豁免判据不是空转）。
	points := 0
	for _, r := range rows {
		if r.Kind == trajectory.KindSideEffect && r.Status == trajectory.StatusOrphan &&
			(r.SideEffectPhase == trajectory.SideEffectReplayed || r.SideEffectPhase == trajectory.SideEffectContended) {
			points++
		}
	}
	if points != 2 {
		t.Fatalf("夹具前提不成立：应有 2 条点观察孤儿行，实得 %d（%+v）", points, rows)
	}

	v, err := Assess(Spec{Checks: []Check{CheckClosure}}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Pass {
		t.Fatalf("健康轨迹（含点观察行）被判违规：%+v", v.Findings)
	}
}

// TestAssessClosureStillFlagsRealOrphanSideEffect 真缺起点（非点观察）仍报 closure。
func TestAssessClosureStillFlagsRealOrphanSideEffect(t *testing.T) {
	t0 := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	cases := []struct {
		name string
		typ  string
	}{
		{"完成事件没有对应授予", eventbus.EventSideEffectCompleted},
		{"裁决事件没有对应对账段", eventbus.EventSideEffectResolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any(nil)
			if tc.typ == eventbus.EventSideEffectResolved {
				extra = map[string]any{"applied": true}
			}
			stream := []eventbus.Event{
				seEvent(tc.typ, t0, "act-a", "res-a", extra),
			}
			rows := Replay(stream, trajectory.Options{})
			v, err := Assess(Spec{Checks: []Check{CheckClosure}}, rows)
			if err != nil {
				t.Fatal(err)
			}
			if v.Pass {
				t.Fatalf("真缺起点必须报 closure：%+v", rows)
			}
			found := false
			for _, f := range v.Findings {
				if f.Check == CheckClosure && f.RowID == "sideeffect#act-a" {
					found = true
				}
			}
			if !found {
				t.Fatalf("finding 应落在 sideeffect#act-a/closure：%+v", v.Findings)
			}
		})
	}
}

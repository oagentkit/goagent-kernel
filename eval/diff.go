// diff.go —— 差异比对：录制投影 vs 重放投影，逐行逐字段判不同。
//
// 比对的两端必须是同一个投影函数的产物（两侧都用 Replay/同一 Options），
// 否则比出的是「两个投影器的差异」而不是「事件的差异」。差异按 (ID, Field)
// 排序——确定性输出让「同一对输入永远同一份差异」，回归比对才有基线。
package eval

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/yi-nology/goagent-kernel/trajectory"
)

// Divergence 一处轨迹差异。
type Divergence struct {
	// ID 行标识（缺失行以 Field="row" 表达）。
	ID string
	// Field 字段名（trajectory.Row 的字段词表）。
	Field string
	// Want 期望值（录制侧）。
	Want string
	// Got 实际值（重放侧）。
	Got string
}

// Diff 比对两份轨迹行，返回确定性排序的差异清单。
//
// 空切片 = 完全复现（issue #111 的验收线：回放能复现一条已录制轨迹，
// 差异为零）。比对字段覆盖轨迹行的全部投影事实（ID 是键、Seq 是顺序锚、
// 两侧时刻都来自事件本身——时刻不同只说明事件被改过，照样报）。
//
// 行 ID **不唯一**是 trajectory 的设计事实：同一个副作用 Key 的授予段、
// 对账段、点观察共用一个标识。故按键分组后**按出现序逐条配对**（第 k 条对
// 第 k 条），一侧多出的段逐条报 row=present→absent / absent→present。
// 按 ID 建 map 会让后写覆盖先写——同 ID 的前段永远不参与比对，含副作用
// 事件的轨迹回归比对因此给出「差异假零」（issue #221）。
func Diff(want, got []trajectory.Row) []Divergence {
	wm, gm := groupRows(want), groupRows(got)

	var out []Divergence
	ids := make([]string, 0, len(wm)+len(gm))
	for id := range wm {
		ids = append(ids, id)
	}
	for id := range gm {
		if _, ok := wm[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	for _, id := range ids {
		w, g := wm[id], gm[id]
		for i := 0; i < len(w) || i < len(g); i++ {
			switch {
			case i >= len(w):
				out = append(out, Divergence{ID: id, Field: "row", Want: "absent", Got: "present"})
			case i >= len(g):
				out = append(out, Divergence{ID: id, Field: "row", Want: "present", Got: "absent"})
			default:
				out = append(out, rowDiff(id, w[i], g[i])...)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Divergence) int {
		return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.Field, b.Field))
	})
	return out
}

// groupRows 按 ID 分组，组内保持出现序（同 ID 多行的配对顺序）。
func groupRows(rows []trajectory.Row) map[string][]trajectory.Row {
	m := make(map[string][]trajectory.Row, len(rows))
	for _, r := range rows {
		m[r.ID] = append(m[r.ID], r)
	}
	return m
}

// rowDiff 比对同一行的全部投影字段（字段表驱动，漏一个字段就是假绿）。
func rowDiff(id string, w, g trajectory.Row) []Divergence {
	type pair struct{ field, want, got string }
	pairs := []pair{
		{"seq", fmt.Sprint(w.Seq), fmt.Sprint(g.Seq)},
		{"kind", string(w.Kind), string(g.Kind)},
		{"turn", fmt.Sprint(w.Turn), fmt.Sprint(g.Turn)},
		{"has_step", fmt.Sprint(w.HasStep), fmt.Sprint(g.HasStep)},
		{"step_id", fmt.Sprint(w.StepID), fmt.Sprint(g.StepID)},
		{"call_id", w.CallID, g.CallID},
		{"tool", w.Tool, g.Tool},
		{"status", string(w.Status), string(g.Status)},
		{"pairing", string(w.Pairing), string(g.Pairing)},
		{"dup_results", fmt.Sprint(w.DupResults), fmt.Sprint(g.DupResults)},
		{"start_at", stamp(w.StartAt), stamp(g.StartAt)},
		{"end_at", stamp(w.EndAt), stamp(g.EndAt)},
		{"sideeffect_key", w.SideEffectKey, g.SideEffectKey},
		{"sideeffect_phase", string(w.SideEffectPhase), string(g.SideEffectPhase)},
		{"sideeffect_resource", w.SideEffectResource, g.SideEffectResource},
		{"sideeffect_fence", fmt.Sprint(w.SideEffectFence), fmt.Sprint(g.SideEffectFence)},
		{"sideeffect_safety", w.SideEffectSafety, g.SideEffectSafety},
		{"checkpoint_run", w.CheckpointRunID, g.CheckpointRunID},
		{"checkpoint_seq", fmt.Sprint(w.CheckpointSeq), fmt.Sprint(g.CheckpointSeq)},
		{"checkpoint_status", w.CheckpointStatus, g.CheckpointStatus},
		{"summary", w.Summary, g.Summary},
	}
	var out []Divergence
	for _, p := range pairs {
		if p.want != p.got {
			out = append(out, Divergence{ID: id, Field: p.field, Want: p.want, Got: p.got})
		}
	}
	return out
}

// stamp 时刻的稳定文本形态（零值显式为 "zero"，不与任何真实时刻混淆）。
func stamp(t time.Time) string {
	if t.IsZero() {
		return "zero"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// diff.go —— 差异比对：录制投影 vs 重放投影，逐行逐字段判不同。
//
// 比对的两端必须是同一个投影函数的产物（两侧都用 Replay/同一 Options），
// 否则比出的是「两个投影器的差异」而不是「事件的差异」。差异按
// (ID, Index, Field) 排序——确定性输出让「同一对输入永远同一份差异」，
// 回归比对才有基线。
package eval

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/oagentkit/goagent-kernel/trajectory"
)

// Divergence 一处轨迹差异。
type Divergence struct {
	// ID 行标识（缺失行以 Field="row" 表达）。
	ID string
	// Index 同 ID 多行时的出现序（0 起）。行标识在**轮内**唯一，而 Diff 是
	// 跨轮/跨批次的全局比对——同名环节号（step#7）在两轮里各出现一次是合法的，
	// 故同一 ID 仍可能有多行；配对按出现序逐条进行，单行 ID 恒为 0。
	Index int
	// Field 字段名（trajectory.Row 的字段词表；"row" = 该出现序上整行缺失）。
	Field string
	// Want 期望值（录制侧）。
	Want string
	// Got 实际值（重放侧）。
	Got string
}

// Diff 比对两份轨迹行，返回确定性排序的差异清单。
//
// 空切片 = 完全复现（issue #111 的验收线：回放能复现一条已录制轨迹，
// 差异为零）。
//
// # 配对按「ID 分组 + 出现序」（issue #221）
//
// ID 在**轮内**唯一（issue #301 起含副作用多段与重复快照：自然键重复时带
// "#-<段序>" 后缀），但 Diff 是跨轮/跨批次的全局比对：同名环节号（step#7）
// 在两轮里各出现一次是合法的，这两行的 ID 相同。按 ID 建一对一的 map 会让
// 后写覆盖先写，每个 ID 只剩数组里最后一条，**先出现的那条被整条丢弃**——
// 回归比对由此给出「差异为零 = 完全复现」的假绿，缺一段也照样判
// present↔present。故先按 ID 分组（map[string][]Row），再按出现序逐条配对：
// 第 i 条对第 i 条；一侧多出/缺少整段时，多出的出现序上报 row 缺行
// （Index 记缺的是第几条），段数差异永不静默。
//
// 副作用行的多段不再共用标识（跨段聚合用 sideeffect.key 属性，不是 row_id）；
// 同一 Key 的段差异由各自的标识逐条比对，不会互相覆盖。
//
// 比对字段覆盖轨迹行的全部投影事实（ID 是键、Seq 是顺序锚、两侧时刻都来自
// 事件本身——时刻不同只说明事件被改过，照样报）。
func Diff(want, got []trajectory.Row) []Divergence {
	group := func(rows []trajectory.Row) map[string][]trajectory.Row {
		m := make(map[string][]trajectory.Row, len(rows))
		for _, r := range rows {
			m[r.ID] = append(m[r.ID], r)
		}
		return m
	}
	wm, gm := group(want), group(got)

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
		ws, gs := wm[id], gm[id]
		n := max(len(ws), len(gs))
		for i := range n {
			switch {
			case i >= len(ws):
				out = append(out, Divergence{ID: id, Index: i, Field: "row", Want: "absent", Got: "present"})
			case i >= len(gs):
				out = append(out, Divergence{ID: id, Index: i, Field: "row", Want: "present", Got: "absent"})
			default:
				out = append(out, rowDiff(id, i, ws[i], gs[i])...)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Divergence) int {
		return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.Index, b.Index), cmp.Compare(a.Field, b.Field))
	})
	return out
}

// rowDiff 比对同一次出现的行的全部投影字段（字段表驱动，漏一个字段就是假绿）。
func rowDiff(id string, idx int, w, g trajectory.Row) []Divergence {
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
			out = append(out, Divergence{ID: id, Index: idx, Field: p.field, Want: p.want, Got: p.got})
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

package trajectory

import (
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// epoch 合成时刻原点（夹具一律合成数据，不带任何领域词）。
var epoch = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// at 相对原点的时刻。
func at(ms int) time.Time { return epoch.Add(time.Duration(ms) * time.Millisecond) }

// ev 构造一条合成事件。
func ev(typ string, ms int, payload map[string]any) eventbus.Event {
	return eventbus.Event{SessionID: "sess-a", Type: typ, OccurredAt: at(ms), Payload: payload}
}

// stepEv 构造一条带环节号的事件。
func stepEv(typ string, ms int, step uint64) eventbus.Event {
	e := ev(typ, ms, nil)
	e.StepID = &step
	return e
}

// toolEv 构造一条工具事件（call_id 是配对键）。
func toolEv(typ string, ms int, step uint64, callID, tool string) eventbus.Event {
	e := ev(typ, ms, map[string]any{"call_id": callID, "tool": tool})
	e.StepID = &step
	return e
}

// findTool 按 call_id 取工具行。
func findTool(t *testing.T, rows []Row, callID string) Row {
	t.Helper()
	for _, r := range rows {
		if r.CallID == callID {
			return r
		}
	}
	t.Fatalf("没有 call_id=%s 的工具行", callID)
	return Row{}
}

// hasIssue 判断缺口清单里是否有某类目（按 call_id 匹配）。
func hasIssue(issues []Issue, kind Pairing, callID string) bool {
	for _, is := range issues {
		if is.Pairing == kind && is.CallID == callID {
			return true
		}
	}
	return false
}

// TestProjectFullTurn 完整一轮的投影：轮次/步骤/工具三类行，起止与时长都对。
func TestProjectFullTurn(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		stepEv(eventbus.EventStepStarted, 100, 1),
		toolEv(eventbus.EventToolCall, 150, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolResult, 400, 1, "c1", "alpha_fn"),
		stepEv(eventbus.EventStepFinished, 500, 1),
		ev(eventbus.EventTurnEnded, 600, nil),
	})

	rows := p.Rows()
	if len(rows) != 3 {
		t.Fatalf("完整一轮应投影出 3 行（轮次/步骤/工具），得到 %d 行: %+v", len(rows), rows)
	}
	if len(p.Issues()) != 0 {
		t.Fatalf("完整一轮不该有配对缺口: %+v", p.Issues())
	}

	turn := rows[0]
	if turn.Kind != KindTurn || turn.ID != "turn#1" || turn.Turn != 1 {
		t.Fatalf("轮次行的类目/标识/轮次号不对: %+v", turn)
	}
	if !turn.StartAt.Equal(at(0)) || !turn.EndAt.Equal(at(600)) || turn.Status != StatusClosed {
		t.Fatalf("轮次行的起止/状态不对: %+v", turn)
	}
	if d, ok := turn.Duration(); !ok || d != 600*time.Millisecond {
		t.Fatalf("轮次行的实测时长应为 600ms，得到 %v (ok=%v)", d, ok)
	}

	step := rows[1]
	if step.Kind != KindStep || !step.HasStep || step.StepID != 1 {
		t.Fatalf("步骤行的类目/环节号不对: %+v", step)
	}
	if d, ok := step.Duration(); !ok || d != 400*time.Millisecond {
		t.Fatalf("步骤行的实测时长应为 400ms，得到 %v (ok=%v)", d, ok)
	}
	if step.Turn != 1 {
		t.Fatalf("步骤行应归属第 1 轮，得到 %d", step.Turn)
	}

	tool := rows[2]
	if tool.Kind != KindToolCall || tool.CallID != "c1" || tool.Tool != "alpha_fn" {
		t.Fatalf("工具行的类目/调用号/工具名不对: %+v", tool)
	}
	if tool.Pairing != PairingPaired {
		t.Fatalf("配对成功的工具行应是 paired，得到 %q", tool.Pairing)
	}
	if d, ok := tool.Duration(); !ok || d != 250*time.Millisecond {
		t.Fatalf("工具行的实测时长应为 250ms，得到 %v (ok=%v)", d, ok)
	}
	if !tool.HasStep || tool.StepID != 1 {
		t.Fatalf("工具行应带上环节号，得到 %+v", tool)
	}
}

// TestIncompleteRowIsNotZeroDurationRow 只有开始没有结束：显式未完成，不是 0 时长完成。
func TestIncompleteRowIsNotZeroDurationRow(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(stepEv(eventbus.EventStepStarted, 100, 1))

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("应投影出 1 行，得到 %d 行", len(rows))
	}
	row := rows[0]
	if row.Status != StatusOpen {
		t.Fatalf("没有结束事件的行应是 open，得到 %q", row.Status)
	}
	if row.Complete() {
		t.Fatal("没有结束事件的行不该算已收口")
	}
	if d, ok := row.Duration(); ok || d != 0 {
		t.Fatalf("未完成的行没有实测时长，不该报出 %v (ok=%v)", d, ok)
	}
	if open := p.Finalize(); len(open) != 1 {
		t.Fatalf("收口应报出 1 条未完成行，得到 %d 条", len(open))
	}
}

// TestOrphanStepEndIsVisible 只有结束没有开始：落成孤儿行，不静默丢弃。
func TestOrphanStepEndIsVisible(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(stepEv(eventbus.EventStepFinished, 100, 1))

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("孤儿结束事件应落成 1 行，得到 %d 行", len(rows))
	}
	if rows[0].Status != StatusOrphan {
		t.Fatalf("孤儿结束行应是 orphan，得到 %q", rows[0].Status)
	}
	if _, ok := rows[0].Duration(); ok {
		t.Fatal("孤儿行没有起点，不该报得出时长")
	}
}

// TestIncrementalProjectionUpdatesRows 增量投影：后半段补的是既有行，不是新行。
func TestIncrementalProjectionUpdatesRows(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		stepEv(eventbus.EventStepStarted, 100, 1),
		toolEv(eventbus.EventToolCall, 150, 1, "c1", "alpha_fn"),
	})

	before := p.Rows()
	if len(before) != 3 {
		t.Fatalf("前半段应投影出 3 行，得到 %d 行", len(before))
	}
	toolBefore := findTool(t, before, "c1")
	if !toolBefore.EndAt.IsZero() {
		t.Fatal("前半段不该有结束时刻")
	}
	if _, ok := toolBefore.Duration(); ok {
		t.Fatal("前半段的工具行还没有实测时长")
	}

	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolResult, 400, 1, "c1", "alpha_fn"),
		stepEv(eventbus.EventStepFinished, 500, 1),
		ev(eventbus.EventTurnEnded, 600, nil),
	})

	after := p.Rows()
	if len(after) != len(before) {
		t.Fatalf("增量投影不该新增行：%d → %d", len(before), len(after))
	}
	toolAfter := findTool(t, after, "c1")
	if toolAfter.Seq != toolBefore.Seq {
		t.Fatalf("行应被就地更新（序号不变）：%d → %d", toolBefore.Seq, toolAfter.Seq)
	}
	if !toolAfter.EndAt.Equal(at(400)) {
		t.Fatalf("工具行应被补上结束时刻，得到 %v", toolAfter.EndAt)
	}
	if d, ok := toolAfter.Duration(); !ok || d != 250*time.Millisecond {
		t.Fatalf("补上结束时刻后实测时长应为 250ms，得到 %v (ok=%v)", d, ok)
	}
	if after[1].Status != StatusClosed || after[0].Status != StatusClosed {
		t.Fatalf("增量投影后轮次行与步骤行都应收口: %+v", after)
	}
}

// TestUnrelatedEventsProduceNoRows 不构成过程段的事件不投影成行（也不是缺口）。
func TestUnrelatedEventsProduceNoRows(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventReasoning, 10, map[string]any{"text": "x"}),
		ev(eventbus.EventLLMUsage, 20, map[string]any{"tokens": 12}),
		ev(eventbus.EventTodoUpdated, 30, nil),
	})
	if p.Len() != 0 {
		t.Fatalf("旁证事件不该投影成行，得到 %d 行", p.Len())
	}
	if len(p.Issues()) != 0 {
		t.Fatalf("旁证事件不是缺口: %+v", p.Issues())
	}
}

// TestStepPairingScopedToTurn 步骤行的配对作用域与「行标识唯一性范围」（轮）
// 一致：上一轮遗留的步骤行不得被下一轮的 step_finished 收口（issue #300）。
//
// 旧实现按创建顺序跨轮配对：下一轮的第一个无环节号 step_finished 会把上一轮
// 那行收掉，导出的子 span 于是比父 span 活得久。
func TestStepPairingScopedToTurn(t *testing.T) {
	t.Run("无环节号", func(t *testing.T) {
		p := NewProjector(Options{})
		p.ApplyAll([]eventbus.Event{
			ev(eventbus.EventTurnStarted, 0, nil),
			ev(eventbus.EventStepStarted, 100, nil),
			ev(eventbus.EventTurnEnded, 200, nil),
			ev(eventbus.EventTurnStarted, 300, nil),
			ev(eventbus.EventStepFinished, 400, nil),
			ev(eventbus.EventTurnEnded, 500, nil),
		})
		rows := p.Rows()
		if len(rows) != 4 {
			t.Fatalf("应投影出 4 行（turn1/step1/turn2/orphan），得到 %d：%+v", len(rows), rows)
		}
		left := rows[1]
		if left.Turn != 1 || left.Status != StatusOpen || !left.EndAt.IsZero() {
			t.Fatalf("第 1 轮遗留的步骤行必须保持 open（不得被下一轮收口）：%+v", left)
		}
		late := rows[3]
		if late.Kind != KindStep || late.Turn != 2 || late.Status != StatusOrphan {
			t.Fatalf("第 2 轮无对应开始的结束事件应落成显式孤儿行：%+v", late)
		}
		if late.ID == left.ID {
			t.Fatalf("跨轮行不得搬用上一轮的标识：%q", late.ID)
		}
		open := p.Finalize()
		if len(open) != 1 || open[0].Seq != left.Seq {
			t.Fatalf("遗留行应以未收口形态在 Finalize 显形，实得 %+v", open)
		}
		assertNoChildOutlivesParent(t, rows)
	})

	t.Run("带环节号", func(t *testing.T) {
		p := NewProjector(Options{})
		p.ApplyAll([]eventbus.Event{
			ev(eventbus.EventTurnStarted, 0, nil),
			stepEv(eventbus.EventStepStarted, 100, 7),
			ev(eventbus.EventTurnEnded, 200, nil),
			ev(eventbus.EventTurnStarted, 300, nil),
			stepEv(eventbus.EventStepFinished, 400, 7),
			ev(eventbus.EventTurnEnded, 500, nil),
		})
		rows := p.Rows()
		if len(rows) != 4 {
			t.Fatalf("应投影出 4 行，得到 %d：%+v", len(rows), rows)
		}
		if left := rows[1]; left.Status != StatusOpen || !left.EndAt.IsZero() || left.Turn != 1 {
			t.Fatalf("同号环节跨轮也不得配对：第 1 轮的行必须保持 open，实得 %+v", left)
		}
		if late := rows[3]; late.Status != StatusOrphan || late.Turn != 2 || !late.HasStep || late.StepID != 7 {
			t.Fatalf("第 2 轮的同号结束事件应落成孤儿行并保留环节号：%+v", late)
		}
		assertNoChildOutlivesParent(t, rows)
	})
}

// assertNoChildOutlivesParent 导出的父子 span 不得出现「子比父活得久」。
func assertNoChildOutlivesParent(t *testing.T, rows []Row) {
	t.Helper()
	byID := map[string]Span{}
	for _, s := range SpansOf(rows) {
		if id, ok := s.Attributes[AttrRowID].(string); ok {
			byID[id] = s
		}
	}
	for _, s := range SpansOf(rows) {
		if s.ParentID == "" {
			continue
		}
		parent, ok := byID[s.ParentID]
		if !ok {
			continue
		}
		if !parent.EndAt.IsZero() && !s.EndAt.IsZero() && s.EndAt.After(parent.EndAt) {
			t.Fatalf("子 span 结束晚于父 span：child %v end=%v parent %v end=%v",
				s.Attributes[AttrRowID], s.EndAt, s.ParentID, parent.EndAt)
		}
	}
}

// TestRowIDsAreUnique 行标识在同一轮内唯一（跨轮同名环节号合法，见
// TestRowIDsAreUniqueWithinTurnOnly；副作用多段/重复快照的轮内唯一性见
// TestRowIDsUniqueWithinTurnIncludingMultiSegment）。
func TestRowIDsAreUnique(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		stepEv(eventbus.EventStepStarted, 100, 1),
		stepEv(eventbus.EventStepStarted, 110, 2),
		toolEv(eventbus.EventToolCall, 150, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolResult, 200, 1, "c2", "beta_fn"), // 无对应调用
		stepEv(eventbus.EventStepFinished, 300, 1),
		ev(eventbus.EventTurnEnded, 400, nil),
	})
	seen := map[string]bool{}
	for _, r := range p.Rows() {
		if r.ID == "" {
			t.Fatalf("行标识不该为空: %+v", r)
		}
		if seen[r.ID] {
			t.Fatalf("行标识重复: %q", r.ID)
		}
		seen[r.ID] = true
	}
}

// TestSummarizeHookOverridesDefault 宿主的摘要注入点生效（展示文案归宿主）。
func TestSummarizeHookOverridesDefault(t *testing.T) {
	p := NewProjector(Options{Summarize: func(r Row) string {
		if r.Kind == KindToolCall {
			return "call:" + r.Tool
		}
		return ""
	}})
	p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))

	rows := p.Rows()
	if rows[0].Summary != "call:alpha_fn" {
		t.Fatalf("宿主注入的摘要应生效，得到 %q", rows[0].Summary)
	}
}

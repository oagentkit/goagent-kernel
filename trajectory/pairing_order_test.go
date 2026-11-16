package trajectory

// 乱序配对的归属归并判据（issue #299）：结果先到、调用后到的就地配对必须是
// 一次**完整归并**——归属字段（轮次 / 环节 / 工具名）以调用事件为准，摘要与
// 类目同源重算。只补时刻不补归属的旧写法会让这三条全红。

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestResultBeforeCallMergesAttributionFromCall 结果先到（载荷无 tool 键）、
// 调用后到（落在第 2 轮且带环节号）：归属字段必须与**调用事件**一致。
//
// 实证（issue #299）：旧实现只写 Kind/StartAt/Status/Pairing 四项 ⇒
// 第 2 轮真实发生的工具调用，导出成第 1 轮的子 span，工具名整段消失。
func TestResultBeforeCallMergesAttributionFromCall(t *testing.T) {
	p := NewProjector(Options{})
	// 第 1 轮：结果先到，载荷不带 tool 键。
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		{Type: eventbus.EventToolResult, OccurredAt: at(50), Payload: map[string]any{"call_id": "c1"}},
		ev(eventbus.EventTurnEnded, 100, nil),
	})
	// 第 2 轮：调用后到，带工具名与环节号。
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 300, nil),
		toolEv(eventbus.EventToolCall, 350, 7, "c1", "alpha_fn"),
		ev(eventbus.EventTurnEnded, 400, nil),
	})

	row := findTool(t, p.Rows(), "c1")
	if row.Kind != KindToolCall {
		t.Fatalf("补上调用后行类目应是 tool_call，得到 %q", row.Kind)
	}
	if row.Tool != "alpha_fn" {
		t.Fatalf("工具名应以调用事件为准（alpha_fn），得到 %q", row.Tool)
	}
	if !row.HasStep || row.StepID != 7 {
		t.Fatalf("环节归属应以调用事件为准（step 7），得到 hasStep=%v stepID=%d", row.HasStep, row.StepID)
	}
	if row.Turn != 2 {
		t.Fatalf("轮次归属应以调用事件为准（第 2 轮），得到 %d", row.Turn)
	}
	if row.Summary != "tool_call:alpha_fn" {
		t.Fatalf("摘要应与类目同源（tool_call:alpha_fn），得到 %q", row.Summary)
	}
	if hasIssue(p.Issues(), PairingUnpairedResult, "c1") {
		t.Fatalf("调用到位后 unpaired_result 缺口应被清掉: %+v", p.Issues())
	}
}

// TestResultBeforeCallParentLandsInCallTurn SpansOf 的 ParentID 必须落在
// **调用所在轮**的环节上——挂到结果到达的那一轮就是父子挂错。
func TestResultBeforeCallParentLandsInCallTurn(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		{Type: eventbus.EventToolResult, OccurredAt: at(50), Payload: map[string]any{"call_id": "c1"}},
		ev(eventbus.EventTurnEnded, 100, nil),
		ev(eventbus.EventTurnStarted, 300, nil),
		stepEv(eventbus.EventStepStarted, 320, 7),
		toolEv(eventbus.EventToolCall, 350, 7, "c1", "alpha_fn"),
		stepEv(eventbus.EventStepFinished, 380, 7),
	})
	spans := SpansOf(p.Rows())
	var toolSpan Span
	for _, s := range spans {
		if s.Attributes[AttrRowID] == "tool#c1" {
			toolSpan = s
		}
	}
	if toolSpan.ParentID != "step#7" {
		t.Fatalf("工具行应挂在调用所在轮的环节下（step#7），实得父 %q", toolSpan.ParentID)
	}
}

// TestSummarizeSeesFinalKindAfterMerge Summarize 钩子**最后**见到的类目必须与
// 行的最终 Kind 一致——摘要与类目不同源，导出面就会出现 kind=tool_call 而
// summary 写着 tool_result 的自相矛盾行。
func TestSummarizeSeesFinalKindAfterMerge(t *testing.T) {
	var seen []Kind
	p := NewProjector(Options{Summarize: func(r Row) string {
		seen = append(seen, r.Kind)
		return string(r.Kind) + ":" + r.Tool
	}})
	p.ApplyAll([]eventbus.Event{
		{Type: eventbus.EventToolResult, OccurredAt: at(50), Payload: map[string]any{"call_id": "c1"}},
		toolEv(eventbus.EventToolCall, 100, 3, "c1", "alpha_fn"),
	})

	row := findTool(t, p.Rows(), "c1")
	if len(seen) == 0 {
		t.Fatal("Summarize 钩子从未被调用")
	}
	if last := seen[len(seen)-1]; last != row.Kind {
		t.Fatalf("钩子最后见到的类目 %q 与行最终 Kind %q 不一致", last, row.Kind)
	}
	if row.Summary != "tool_call:alpha_fn" {
		t.Fatalf("摘要应由最后一次钩子调用产出（tool_call:alpha_fn），得到 %q", row.Summary)
	}
}

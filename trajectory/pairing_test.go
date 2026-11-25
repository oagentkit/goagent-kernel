package trajectory

import (
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/eventbus"
)

// 配对面的用例单独成文件：本文件只关心「调用与结果按 call_id 配对」这一族，
// 缺口必须显形（不静默丢弃），且配对状态变化不得改写实测时刻。

// TestToolPairingByCallID 配对按 call_id 走，不是按到达顺序。
func TestToolPairingByCallID(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolCall, 150, 1, "c2", "beta_fn"),
		toolEv(eventbus.EventToolResult, 300, 1, "c2", "beta_fn"),
		toolEv(eventbus.EventToolResult, 400, 1, "c1", "alpha_fn"),
	})

	rows := p.Rows()
	if len(rows) != 2 {
		t.Fatalf("两个调用应投影出 2 行，得到 %d 行", len(rows))
	}
	c1 := findTool(t, rows, "c1")
	c2 := findTool(t, rows, "c2")
	if !c1.EndAt.Equal(at(400)) || !c2.EndAt.Equal(at(300)) {
		t.Fatalf("结束时刻应按 call_id 配对: c1=%v c2=%v", c1.EndAt, c2.EndAt)
	}
	if d, ok := c1.Duration(); !ok || d != 300*time.Millisecond {
		t.Fatalf("c1 的实测时长应为 300ms，得到 %v (ok=%v)", d, ok)
	}
	if d, ok := c2.Duration(); !ok || d != 150*time.Millisecond {
		t.Fatalf("c2 的实测时长应为 150ms，得到 %v (ok=%v)", d, ok)
	}
}

// TestPairingMissingResultIsExplicit 只有调用没有结果：显式状态 + 显式缺口。
func TestPairingMissingResultIsExplicit(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("应投影出 1 行，得到 %d 行", len(rows))
	}
	row := rows[0]
	if row.Pairing != PairingMissingResult {
		t.Fatalf("没有结果的调用行应是 missing_result，得到 %q", row.Pairing)
	}
	if _, ok := row.Duration(); ok {
		t.Fatal("没有结果就没有实测时长，不该报得出")
	}

	// 收口之后才确认为「等不到」：缺口进清单，未完成行原样报出。
	open := p.Finalize()
	if len(open) != 1 || open[0].CallID != "c1" {
		t.Fatalf("收口应报出 1 条未完成行，得到 %+v", open)
	}
	if !hasIssue(p.Issues(), PairingMissingResult, "c1") {
		t.Fatalf("收口后应有 missing_result 缺口: %+v", p.Issues())
	}
}

// TestDuplicateResultIsExplicit 结果重复：显式状态，且不重写实测时长。
func TestDuplicateResultIsExplicit(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolResult, 300, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolResult, 500, 1, "c1", "alpha_fn"),
	})

	row := findTool(t, p.Rows(), "c1")
	if row.DupResults != 1 {
		t.Fatalf("重复结果计数应为 1，得到 %d", row.DupResults)
	}
	if row.Pairing != PairingDuplicateResult {
		t.Fatalf("重复结果的行应是 duplicate_result，得到 %q", row.Pairing)
	}
	if !row.EndAt.Equal(at(300)) {
		t.Fatalf("重复结果不该重写结束时刻（第一次观测是真相），得到 %v", row.EndAt)
	}
	if d, ok := row.Duration(); !ok || d != 200*time.Millisecond {
		t.Fatalf("实测时长应仍取第一次配对的结果（200ms），得到 %v (ok=%v)", d, ok)
	}
	if !hasIssue(p.Issues(), PairingDuplicateResult, "c1") {
		t.Fatalf("应有 duplicate_result 缺口: %+v", p.Issues())
	}
}

// TestUnpairedResultIsExplicit 结果没有对应调用：不静默丢弃。
func TestUnpairedResultIsExplicit(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(toolEv(eventbus.EventToolResult, 300, 1, "c9", "gamma_fn"))

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("无对应的结果应落成 1 行（不得丢弃），得到 %d 行", len(rows))
	}
	row := rows[0]
	if row.Kind != KindToolResult {
		t.Fatalf("无对应的结果行类目应是 tool_result，得到 %q", row.Kind)
	}
	if row.Pairing != PairingUnpairedResult {
		t.Fatalf("无对应的结果行应是 unpaired_result，得到 %q", row.Pairing)
	}
	if row.Status != StatusOrphan {
		t.Fatalf("无对应的结果行起点缺失，应是 orphan，得到 %q", row.Status)
	}
	if _, ok := row.Duration(); ok {
		t.Fatal("无对应的结果没有实测起点，不该报得出时长")
	}
	if !hasIssue(p.Issues(), PairingUnpairedResult, "c9") {
		t.Fatalf("应有 unpaired_result 缺口: %+v", p.Issues())
	}
}

// TestDuplicateCallKeepsFirstObservation 同号重复声明：第一次观测是真相。
func TestDuplicateCallKeepsFirstObservation(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolCall, 150, 1, "c1", "alpha_fn"),
	})

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("同一 call_id 只应有一行，得到 %d 行", len(rows))
	}
	if !rows[0].StartAt.Equal(at(100)) {
		t.Fatalf("起点应取第一次观测（at(100)），得到 %v", rows[0].StartAt)
	}
	if !hasIssue(p.Issues(), PairingDuplicateCall, "c1") {
		t.Fatalf("应有 duplicate_call 缺口: %+v", p.Issues())
	}
}

// TestDuplicateUnpairedResultsKeepUnpairedState 两个结果都没有对应调用：
// 「没配对」这层事实不能被「结果重复」盖掉（两件事都进缺口清单）。
func TestDuplicateUnpairedResultsKeepUnpairedState(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolResult, 300, 1, "c9", "gamma_fn"),
		toolEv(eventbus.EventToolResult, 400, 1, "c9", "gamma_fn"),
	})

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("同一 call_id 的孤儿结果应落成 1 行，得到 %d 行", len(rows))
	}
	if rows[0].Pairing != PairingUnpairedResult {
		t.Fatalf("孤儿结果行的配对状态应仍是 unpaired_result，得到 %q", rows[0].Pairing)
	}
	if rows[0].DupResults != 1 {
		t.Fatalf("重复结果计数应为 1，得到 %d", rows[0].DupResults)
	}
	if !hasIssue(p.Issues(), PairingUnpairedResult, "c9") {
		t.Fatalf("应有 unpaired_result 缺口: %+v", p.Issues())
	}
	if !hasIssue(p.Issues(), PairingDuplicateResult, "c9") {
		t.Fatalf("应有 duplicate_result 缺口: %+v", p.Issues())
	}
}

// TestResultBeforeCallMergesAttribution 结果先到、调用后到：就地配对必须是
// **完整归并**，而不是只补时刻——归属字段（轮次 / 环节号 / 工具名）以调用事件
// 为准，摘要与最终类目同源重算。
//
// issue #299：旧实现只写 Kind/StartAt/Status/Pairing 四项，于是第 2 轮真实发生
// 的调用被导出成第 1 轮的子 span，工具名整段消失，摘要还写着 tool_result。
func TestResultBeforeCallMergesAttribution(t *testing.T) {
	var seenKinds []Kind
	p := NewProjector(Options{Summarize: func(r Row) string {
		seenKinds = append(seenKinds, r.Kind)
		if r.Kind == KindToolCall {
			return "call:" + r.Tool
		}
		return ""
	}})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		// 结果先到：落在第 1 轮，载荷不带 tool、不带环节号。
		ev(eventbus.EventToolResult, 100, map[string]any{"call_id": "c1"}),
		ev(eventbus.EventTurnEnded, 200, nil),
		ev(eventbus.EventTurnStarted, 300, nil),
		stepEv(eventbus.EventStepStarted, 310, 7),
		// 调用后到：第 2 轮、带环节号、载荷带工具名。
		toolEv(eventbus.EventToolCall, 400, 7, "c1", "alpha_fn"),
	})

	rows := p.Rows()
	if len(rows) != 4 {
		t.Fatalf("应投影出 4 行（turn1/turn2/step7/tool），得到 %d：%+v", len(rows), rows)
	}
	row := findTool(t, rows, "c1")
	if row.Kind != KindToolCall || row.Pairing != PairingPaired {
		t.Fatalf("归并后应是已配对的 tool_call：%+v", row)
	}
	if row.Turn != 2 {
		t.Fatalf("归属轮次必须取自调用事件（第 2 轮），实得 %d", row.Turn)
	}
	if !row.HasStep || row.StepID != 7 {
		t.Fatalf("归属环节号必须取自调用事件（7），实得 has=%v id=%d", row.HasStep, row.StepID)
	}
	if row.Tool != "alpha_fn" {
		t.Fatalf("工具名必须取自调用事件载荷，实得 %q", row.Tool)
	}
	if row.Summary != "call:alpha_fn" {
		t.Fatalf("摘要必须按归并后的类目/工具名重算，实得 %q", row.Summary)
	}
	if len(seenKinds) == 0 || seenKinds[len(seenKinds)-1] != KindToolCall {
		t.Fatalf("Summarize 钩子最后一次看到的类目必须与最终 Row.Kind 一致，实得 %v", seenKinds)
	}
	// 导出的 span 必须挂在调用所在轮的环节下。
	spans := SpansOf(rows)
	var toolSpan Span
	for _, s := range spans {
		if s.Attributes[AttrRowID] == "tool#c1" {
			toolSpan = s
		}
	}
	if toolSpan.ParentID != "step#7" {
		t.Fatalf("工具 span 的父必须是调用所在轮的环节 step#7，实得 %q", toolSpan.ParentID)
	}
	if got := toolSpan.Attributes[AttrTurnSeq]; got != int64(2) {
		t.Fatalf("turn.seq 必须是调用所在轮（2），实得 %v", got)
	}
}

// TestAnonymousCallResultNoGap 不带 call_id 的「调用 + 结果」是一次正常调用：
// 按无 id 路径归并成一行，不产生 unpaired_result 缺口（issue #306）。
func TestAnonymousCallResultNoGap(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventToolCall, 100, map[string]any{"tool": "alpha_fn"}),
		ev(eventbus.EventToolResult, 300, map[string]any{"tool": "alpha_fn"}),
	})
	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("无 id 的正常调用应归并成 1 行（不拆成 tool_call + tool_result），得到 %d：%+v", len(rows), rows)
	}
	row := rows[0]
	if row.Kind != KindToolCall || row.Pairing != PairingPaired || row.Status != StatusClosed {
		t.Fatalf("无 id 的调用/结果应正常配对：%+v", row)
	}
	if row.CallID != "" || row.Tool != "alpha_fn" {
		t.Fatalf("无 id 行不得凭空补标识：%+v", row)
	}
	if d, ok := row.Duration(); !ok || d != 200*time.Millisecond {
		t.Fatalf("配对应给出实测时长 200ms，实得 %v ok=%v", d, ok)
	}
	if len(p.Issues()) != 0 {
		t.Fatalf("无 id 的正常调用不得记缺口（旧实现记一条空 call_id 的 unpaired_result）：%+v", p.Issues())
	}
	if open := p.Finalize(); len(open) != 0 {
		t.Fatalf("已配对的调用不应报未收口：%+v", open)
	}
}

// TestAnonymousResultWithoutCallIsNotGap 独立到达的无 id 结果：落成显式
// no_call_id 行，但**不是**「结果无对应调用」——不记 unpaired_result。
func TestAnonymousResultWithoutCallIsNotGap(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(ev(eventbus.EventToolResult, 300, map[string]any{"tool": "gamma_fn"}))
	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("无 id 的结果应落成 1 行（不得丢弃）：%+v", rows)
	}
	row := rows[0]
	if row.Kind != KindToolResult || row.Status != StatusOrphan {
		t.Fatalf("无 id 的孤立结果应是 tool_result 孤儿行：%+v", row)
	}
	if row.Pairing != PairingNoCallID {
		t.Fatalf("无 id 的配对状态应显式表达「调用方未提供标识」，实得 %q", row.Pairing)
	}
	if len(p.Issues()) != 0 {
		t.Fatalf("「没有 call_id」不是缺口（与「找不到对应调用」两件事）：%+v", p.Issues())
	}
}

// TestNamedResultWithoutCallStillGap 真正的「结果无对应调用」仍记缺口（不回退）。
func TestNamedResultWithoutCallStillGap(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(toolEv(eventbus.EventToolResult, 300, 1, "c9", "gamma_fn"))
	if !hasIssue(p.Issues(), PairingUnpairedResult, "c9") {
		t.Fatalf("带 call_id 的无对应结果仍必须记 unpaired_result：%+v", p.Issues())
	}
}

// TestAnonymousResultBeforeCallMerges 无 id 的结果先到、调用后到：按先进先出
// 就地归并（归属字段以调用事件为准），不产生缺口。
func TestAnonymousResultBeforeCallMerges(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		ev(eventbus.EventToolResult, 100, map[string]any{}),
		ev(eventbus.EventTurnEnded, 150, nil),
		ev(eventbus.EventTurnStarted, 200, nil),
		stepEv(eventbus.EventStepStarted, 210, 7),
		ev(eventbus.EventToolCall, 300, map[string]any{"tool": "alpha_fn"}),
	})
	rows := p.Rows()
	var tool *Row
	for i := range rows {
		if rows[i].Kind == KindToolCall || rows[i].Kind == KindToolResult {
			if tool != nil {
				t.Fatalf("无 id 的结果先到 + 调用后到应只剩 1 条工具行：%+v", rows)
			}
			tool = &rows[i]
		}
	}
	if tool == nil {
		t.Fatalf("缺少工具行：%+v", rows)
	}
	if tool.Kind != KindToolCall || tool.Pairing != PairingPaired || tool.Tool != "alpha_fn" {
		t.Fatalf("归并后应取调用事件的归属：%+v", *tool)
	}
	if tool.Turn != 2 {
		t.Fatalf("归属轮次应取自调用事件（2），实得 %d", tool.Turn)
	}
	if len(p.Issues()) != 0 {
		t.Fatalf("无 id 路径不记配对缺口：%+v", p.Issues())
	}
}

// TestPairingKeyIsGlobalCallID 配对身份是**全局 call_id**，不含 step（issue #336）：
// 结果在另一个环节到达仍是同一次调用，不得判成 unpaired_result。
func TestPairingKeyIsGlobalCallID(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolCall, 100, 7, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolResult, 300, 9, "c1", "alpha_fn"), // 不同环节
	})
	row := findTool(t, p.Rows(), "c1")
	if row.Pairing != PairingPaired {
		t.Fatalf("跨环节到达的结果应按同一 call_id 配对，实得 %q", row.Pairing)
	}
	if len(p.Issues()) != 0 {
		t.Fatalf("跨环节不是缺口：%+v", p.Issues())
	}
}

// TestDuplicateCallAcrossSteps 跨 step 重复声明（重试/改派）：一行 + duplicate_call
// 缺口（第一次观测是真相）。watchdog 侧对同一事件流的结论见 watchdog 包用例
// （TestWatchdogCrossStepDuplicateDeclaration）——两侧同词同判据（issue #336）。
func TestDuplicateCallAcrossSteps(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolCall, 100, 7, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolCall, 150, 8, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolResult, 300, 8, "c1", "alpha_fn"),
	})
	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("同一 call_id 只应有一行，得到 %d：%+v", len(rows), rows)
	}
	if rows[0].Pairing != PairingPaired {
		t.Fatalf("重复声明不改变配对结论，实得 %q", rows[0].Pairing)
	}
	if !hasIssue(p.Issues(), PairingDuplicateCall, "c1") {
		t.Fatalf("应有 duplicate_call 缺口：%+v", p.Issues())
	}
}

// TestResultBeforeCallPairsInPlace 结果先到、调用后到：就地配对，不另起一行。
func TestResultBeforeCallPairsInPlace(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		toolEv(eventbus.EventToolResult, 300, 1, "c1", "alpha_fn"),
		toolEv(eventbus.EventToolCall, 400, 1, "c1", "alpha_fn"),
	})

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("结果先到再补调用应仍是一行，得到 %d 行", len(rows))
	}
	row := rows[0]
	if row.Kind != KindToolCall {
		t.Fatalf("补上调用后行类目应是 tool_call，得到 %q", row.Kind)
	}
	if row.Pairing != PairingPaired {
		t.Fatalf("补上调用后应是 paired，得到 %q", row.Pairing)
	}
	if !row.StartAt.Equal(at(400)) || !row.EndAt.Equal(at(300)) {
		t.Fatalf("起止时刻应取自各自事件: %v → %v", row.StartAt, row.EndAt)
	}
	// 事件顺序反了 ⇒ 两时刻倒挂 ⇒ 没有可信的实测时长（不拿绝对值糊过去）。
	if _, ok := row.Duration(); ok {
		t.Fatal("起止倒挂时不该报出实测时长")
	}
}

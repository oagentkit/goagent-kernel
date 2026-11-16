package trajectory

import (
	"github.com/yi-nology/goagent-kernel/eventbus"
)

// apply 投一条事件到投影器（调用方已持锁）。
//
// 事件类型取自本仓 eventbus 的词表——投影器吃本仓自己的事件，不另立事件模型：
// 另立一套事件词表，等于让「什么算一次工具调用」在两处各答一次，
// 答案迟早漂。
func (p *Projector) apply(ev eventbus.Event) {
	switch ev.Type {
	case eventbus.EventTurnStarted:
		p.openTurn(ev)
	case eventbus.EventTurnEnded:
		p.closeTurn(ev)
	case eventbus.EventStepStarted:
		p.openStep(ev)
	case eventbus.EventStepFinished:
		p.closeStep(ev)
	case eventbus.EventToolCall:
		p.openTool(ev)
	case eventbus.EventToolResult:
		p.closeTool(ev)
	case eventbus.EventSideEffectGranted:
		openSideEffect(p, ev)
	case eventbus.EventSideEffectCompleted:
		settleSideEffect(p, ev, SideEffectCompleted)
	case eventbus.EventSideEffectAbandoned:
		settleSideEffect(p, ev, SideEffectAbandoned)
	case eventbus.EventSideEffectUncertain:
		uncertainSideEffect(p, ev)
	case eventbus.EventSideEffectResolved:
		resolveSideEffect(p, ev)
	case eventbus.EventSideEffectReplayed:
		pointSideEffect(p, ev, SideEffectReplayed)
	case eventbus.EventSideEffectContended:
		pointSideEffect(p, ev, SideEffectContended)
	case eventbus.EventCheckpointCommitted:
		checkpointCommitted(p, ev)
	default:
		// 其余事件类型不构成轨迹行（它们是过程的旁证，不是过程段本身）——
		// 不是缺口，不记。
	}
}

// openTurn 开一轮：新建轮次行。
//
// 上一轮若仍打开（没有 turn_ended），**不替它补一个结束时刻**——补了就是
// 伪造实测时长。它保持 open，Finalize 时作为未完成行报出。
func (p *Projector) openTurn(ev eventbus.Event) {
	p.turnNo++
	// 上一轮遗留的步骤行退出配对面（opens 按轮分段）：它们不再是谁的结束
	// 事件的候选，但行本身保持 open——Finalize 仍会报出，不替它们补结束时刻
	// （补了就是伪造实测时长，issue #300）。
	p.opens = p.opens[:0]
	idx := p.append(Row{
		Kind:    KindTurn,
		ID:      turnRowID(p.turnNo),
		Turn:    p.turnNo,
		StartAt: ev.OccurredAt,
		Status:  StatusOpen,
	})
	p.turn = idx
}

// closeTurn 收一轮：收口当前轮次行；没有可收口的轮次行时落成孤儿行
// （只有结束事件、没有开始事件），不静默丢弃。
func (p *Projector) closeTurn(ev eventbus.Event) {
	if p.turn >= 0 && p.rows[p.turn].Status == StatusOpen {
		p.rows[p.turn].EndAt = ev.OccurredAt
		p.rows[p.turn].Status = StatusClosed
		return
	}
	p.append(Row{
		Kind:   KindTurn,
		ID:     seqID(KindTurn, p.seq+1),
		Turn:   p.currentTurn(),
		EndAt:  ev.OccurredAt,
		Status: StatusOrphan,
	})
}

// openStep 开一个步骤行。
//
// 环节号是步骤的自然键；没有环节号的步骤只能按先后顺序配对（见 matchOpenStep）。
func (p *Projector) openStep(ev eventbus.Event) {
	r := Row{
		Kind:    KindStep,
		Turn:    p.currentTurn(),
		StartAt: ev.OccurredAt,
		Status:  StatusOpen,
	}
	if id, ok := stepIDOf(ev); ok {
		r.HasStep = true
		r.StepID = id
		r.ID = stepRowID(id)
	} else {
		r.ID = seqID(KindStep, p.seq+1)
	}
	idx := p.append(r)
	p.opens = append(p.opens, idx)
}

// closeStep 收一个步骤行；没有可收口的步骤行时落成孤儿行。
func (p *Projector) closeStep(ev eventbus.Event) {
	idx := p.matchOpenStep(ev)
	if idx < 0 {
		r := Row{
			Kind:   KindStep,
			Turn:   p.currentTurn(),
			EndAt:  ev.OccurredAt,
			Status: StatusOrphan,
		}
		if id, ok := stepIDOf(ev); ok {
			r.HasStep = true
			r.StepID = id
		}
		r.ID = seqID(KindStep, p.seq+1)
		p.append(r)
		return
	}
	p.rows[idx].EndAt = ev.OccurredAt
	p.rows[idx].Status = StatusClosed
	p.dropOpen(idx)
}

// matchOpenStep 找出可与该结束事件配对的步骤行。
//
// 作用域是**当前轮**：配对范围必须与行标识的唯一性范围（轮）一致——上一轮
// 遗留的步骤行不得被下一轮的结束事件收口，否则导出的子 span 会晚于父 span
// 结束（issue #300）。跨轮遗留的行保持 open，由 Finalize 作为未完成行显形；
// 配不上的结束事件落成孤儿行，不静默丢弃。
//
// 带环节号：配**最近**一条同号且仍打开、同属当前轮的步骤行——同一环节号被重新
// 开过一次时（重试），后一条才是本次结束对应的。
// 不带环节号：配**最早**一条仍打开、同属当前轮的无环节号步骤行（先进先出）。
// 都配不上时返回 -1（调用方落孤儿行）。
func (p *Projector) matchOpenStep(ev eventbus.Event) int {
	turn := p.currentTurn()
	if id, ok := stepIDOf(ev); ok {
		for i := len(p.opens) - 1; i >= 0; i-- {
			idx := p.opens[i]
			if p.rows[idx].Turn == turn && p.rows[idx].HasStep && p.rows[idx].StepID == id {
				return idx
			}
		}
		return -1
	}
	for _, idx := range p.opens {
		if p.rows[idx].Turn == turn && !p.rows[idx].HasStep {
			return idx
		}
	}
	return -1
}

// dropOpen 从「仍打开的步骤行」清单里摘掉一行。
func (p *Projector) dropOpen(idx int) {
	for i, open := range p.opens {
		if open == idx {
			p.opens = append(p.opens[:i], p.opens[i+1:]...)
			return
		}
	}
}

// openTool 开一个工具调用行。
//
// 配对键是 call_id。同一 call_id 的第二次声明**不新建行**：第一次观测是真相，
// 改写起点等于伪造实测时长——只记一条 duplicate_call 缺口。
func (p *Projector) openTool(ev eventbus.Event) {
	callID := payloadString(ev, "call_id")
	tool := payloadString(ev, "tool")
	if callID == "" {
		p.openAnonymousTool(ev, tool)
		return
	}
	if idx, ok := p.tools[callID]; ok {
		if p.rows[idx].Kind == KindToolResult {
			// 结果先到、调用后到：就地**完整归并**，不是只补时刻。
			// 归属字段（轮次 / 环节号 / 工具名）以**调用事件**为准——结果行
			// 创建时按「当时的当前轮」记归属，而调用可能落在另一轮；只补
			// 时刻会让第 2 轮真实发生的调用导出成第 1 轮的子 span，工具名
			// 整段消失（issue #299）。
			p.mergeCallIntoResult(idx, ev, tool)
			p.clearIssue(PairingUnpairedResult, callID)
			return
		}
		p.issue(PairingDuplicateCall, callID, p.rows[idx].Seq)
		return
	}
	r := Row{
		Kind:    KindToolCall,
		Turn:    p.currentTurn(),
		CallID:  callID,
		Tool:    tool,
		StartAt: ev.OccurredAt,
		Status:  StatusOpen,
		Pairing: PairingMissingResult,
	}
	if id, ok := stepIDOf(ev); ok {
		r.HasStep = true
		r.StepID = id
	}
	r.ID = toolRowID(callID)
	idx := p.append(r)
	p.tools[callID] = idx
}

// mergeCallIntoResult 把迟到的调用事件完整归并进既有的结果行（issue #299）。
//
// 归属字段以调用事件为准；类目从 tool_result 改成 tool_call 后，摘要按最终
// 类目同源重算。调用方负责清掉结果先到时留下的 unpaired_result 缺口。
func (p *Projector) mergeCallIntoResult(idx int, ev eventbus.Event, tool string) {
	row := &p.rows[idx]
	row.Kind = KindToolCall
	row.Turn = p.currentTurn()
	if id, has := stepIDOf(ev); has {
		row.HasStep = true
		row.StepID = id
	} else {
		row.HasStep = false
		row.StepID = 0
	}
	row.Tool = tool
	row.StartAt = ev.OccurredAt
	row.Status = StatusClosed
	row.Pairing = PairingPaired
	p.refreshSummary(idx)
}

// openAnonymousTool 无 call_id 的调用声明：没有标识时到达顺序是唯一可用的
// 配对信息，故按先进先出与更早到达的无 id 结果归并；否则开一条待结果行。
//
// 这条路径**不记配对缺口**：调用方未提供标识不等于配对失败（issue #306）。
func (p *Projector) openAnonymousTool(ev eventbus.Event, tool string) {
	if idx, ok := p.takeAnonymousResult(); ok {
		p.mergeCallIntoResult(idx, ev, tool)
		return
	}
	r := Row{
		Kind:    KindToolCall,
		Turn:    p.currentTurn(),
		Tool:    tool,
		StartAt: ev.OccurredAt,
		Status:  StatusOpen,
		Pairing: PairingMissingResult,
	}
	if id, ok := stepIDOf(ev); ok {
		r.HasStep = true
		r.StepID = id
	}
	r.ID = seqID(KindToolCall, p.seq+1)
	idx := p.append(r)
	p.anonCalls = append(p.anonCalls, idx)
}

// takeAnonymousResult 取最早一条仍未配对的无 id 结果行（先进先出）。
func (p *Projector) takeAnonymousResult() (int, bool) {
	for len(p.anonResults) > 0 {
		idx := p.anonResults[0]
		p.anonResults = p.anonResults[1:]
		if p.rows[idx].Status == StatusOrphan {
			return idx, true
		}
	}
	return 0, false
}

// closeTool 用结果收口工具调用行。
//
// 第一次配上的结果：结束时刻取结果时刻，时长随之成为实测值。
// 重复结果：**不重写结束时刻**（第一次观测是真相），只记缺口与计数。
// 结果没有对应调用（带 call_id 却查不到）：落成结果行 + unpaired_result
// 缺口，不静默丢弃。
// 不带 call_id 的结果走无 id 路径（closeAnonymousTool）：按到达顺序归并，
// **不记缺口**——「没有标识」与「标识找不到对应调用」是两件事（issue #306）。
func (p *Projector) closeTool(ev eventbus.Event) {
	callID := payloadString(ev, "call_id")
	tool := payloadString(ev, "tool")
	if callID == "" {
		p.closeAnonymousTool(ev, tool)
		return
	}
	idx, ok := p.tools[callID]
	if !ok {
		// 结果没有对应调用：落成结果行 + unpaired_result 缺口，不静默丢弃。
		//
		// 结果事件是「一次工具调用的**结束**」，故时刻记在 EndAt、起点留空
		// （起点缺失 = StatusOrphan）。这样它后来若等到迟到的调用声明，
		// 可以就地补上起点完成配对，而不必搬动时刻。
		r := Row{
			Kind:    KindToolResult,
			Turn:    p.currentTurn(),
			CallID:  callID,
			Tool:    tool,
			EndAt:   ev.OccurredAt,
			Status:  StatusOrphan,
			Pairing: PairingUnpairedResult,
		}
		if id, has := stepIDOf(ev); has {
			r.HasStep = true
			r.StepID = id
		}
		r.ID = toolRowID(callID)
		pi := p.append(r)
		p.tools[callID] = pi
		p.issue(PairingUnpairedResult, callID, p.rows[pi].Seq)
		return
	}
	row := &p.rows[idx]
	if row.EndAt.IsZero() {
		row.EndAt = ev.OccurredAt
		row.Status = StatusClosed
		row.Pairing = PairingPaired
		p.clearIssue(PairingMissingResult, callID)
		return
	}
	row.DupResults++
	if row.Kind == KindToolCall {
		// 已配上的行：状态改成「结果重复」。
		row.Pairing = PairingDuplicateResult
	}
	// 仍是孤儿结果行的（始终没有对应调用）：配对状态保持 unpaired_result——
	// 改成 duplicate_result 会把「压根没配对」这层事实盖掉。两件事都进缺口清单。
	p.issue(PairingDuplicateResult, callID, row.Seq)
}

// closeAnonymousTool 无 call_id 的结果：配最早一条仍等待结果的无标识调用行
// （先进先出；没有标识时到达顺序是唯一可用的配对信息）。
//
// 配不上时落成 PairingNoCallID 孤儿行——**不记 unpaired_result**：调用方未
// 提供标识不等于「结果没有对应调用」（issue #306）。该行留在 anonResults 里，
// 迟到的无 id 调用声明可以就地归并。
func (p *Projector) closeAnonymousTool(ev eventbus.Event, tool string) {
	for i, idx := range p.anonCalls {
		if p.rows[idx].Status != StatusOpen {
			continue
		}
		p.anonCalls = append(p.anonCalls[:i], p.anonCalls[i+1:]...)
		row := &p.rows[idx]
		row.EndAt = ev.OccurredAt
		row.Status = StatusClosed
		row.Pairing = PairingPaired
		if row.Tool == "" && tool != "" {
			// 调用事件没带工具名时用结果的补上（调用事件带了就以调用为准）。
			row.Tool = tool
			p.refreshSummary(idx)
		}
		return
	}
	r := Row{
		Kind:    KindToolResult,
		Turn:    p.currentTurn(),
		Tool:    tool,
		EndAt:   ev.OccurredAt,
		Status:  StatusOrphan,
		Pairing: PairingNoCallID,
	}
	if id, has := stepIDOf(ev); has {
		r.HasStep = true
		r.StepID = id
	}
	r.ID = seqID(KindToolResult, p.seq+1)
	idx := p.append(r)
	p.anonResults = append(p.anonResults, idx)
}

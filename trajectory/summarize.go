// summarize.go —— 行摘要面：摘要只有一个源（行的当前可读字段），与类目同源。
//
// 单成文件是**重新分组**，不是拆行数：摘要的产出（append 的默认标签、宿主钩子
// 的覆盖、就地归并后的重算）是一族以 Row 为输入的纯计算，与投影器的配对逻辑、
// 轮次括号账各是一摊。
package trajectory

// append 追加一行（分配序号、段序、默认摘要）。
func (p *Projector) append(r Row) int {
	p.seq++
	r.Seq = p.seq
	// 段序：同（轮, 标识）内的出现序。同轮重复的标识即「同一逻辑动作的多段 /
	// 同一次提交的重复观测」，段序让它们可区分、可排序（issue #301）。
	k := segKey{turn: r.Turn, id: r.ID}
	p.segCounts[k]++
	r.Segment = p.segCounts[k]
	r.Summary = defaultSummary(r)
	if p.opts.Summarize != nil {
		if s := p.opts.Summarize(r); s != "" {
			r.Summary = s
		}
	}
	p.rows = append(p.rows, r)
	return len(p.rows) - 1
}

// resummarize 就地重算一行摘要（默认标签 + 宿主钩子覆盖）。
//
// 与 append 同源：摘要永远从行的当前可读字段算出。就地归并（结果先到、调用后到）
// 改写类目与归属后必须走这里，否则摘要停留在创建时的类目上，导出面出现
// kind=tool_call 而 summary 写着 tool_result 的自相矛盾行。
func (p *Projector) resummarize(r *Row) {
	r.Summary = defaultSummary(*r)
	if p.opts.Summarize != nil {
		if s := p.opts.Summarize(*r); s != "" {
			r.Summary = s
		}
	}
}

// defaultSummary 默认行摘要：通用标签，不含任何领域词。
func defaultSummary(r Row) string {
	switch r.Kind {
	case KindTurn, KindStep:
		return r.ID
	case KindToolCall, KindToolResult:
		if r.Tool == "" {
			return string(r.Kind)
		}
		return string(r.Kind) + ":" + r.Tool
	case KindSideEffect:
		if r.SideEffectKey == "" {
			return string(r.Kind)
		}
		return string(r.Kind) + ":" + r.SideEffectKey
	case KindCheckpoint:
		return r.ID
	default:
		return string(r.Kind)
	}
}

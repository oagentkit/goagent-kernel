// summarize.go —— 行摘要面：摘要只有一个源（行的当前可读字段），与类目同源；
// 宿主钩子的执行面在投影器内部锁之外（issue #302）。
//
// 单成文件是**重新分组**，不是拆行数：摘要的产出（append 的默认标签、宿主钩子
// 的覆盖、就地归并后的重算）与它在锁外的执行调度是一族自洽的机制，与投影器的
// 配对逻辑、轮次括号账各是一摊。
package trajectory

// append 追加一行（分配序号、段序、默认摘要）。
//
// 默认摘要在此就地算好（任何时刻读 Rows 都看到与类目同源的摘要）；宿主钩子的
// 覆盖延迟到锁释放之后（pending → flushSummaries），调用方已持锁，钩子绝不在
// 锁内执行。
func (p *Projector) append(r Row) int {
	p.seq++
	r.Seq = p.seq
	// 段序：同（轮, 标识）内的出现序。同轮重复的标识即「同一逻辑动作的多段 /
	// 同一次提交的重复观测」，段序让它们可区分、可排序（issue #301）。
	k := segKey{turn: r.Turn, id: r.ID}
	p.segCounts[k]++
	r.Segment = p.segCounts[k]
	r.Summary = defaultSummary(r)
	p.rows = append(p.rows, r)
	if p.opts.Summarize != nil {
		p.pending = append(p.pending, len(p.rows)-1)
	}
	return len(p.rows) - 1
}

// resummarize 重算第 idx 行的摘要：默认标签立即生效（就地归并改写类目与归属后，
// 任何时刻读 Rows 都看到与新类目同源的摘要），宿主钩子的覆盖延迟到锁释放之后。
func (p *Projector) resummarize(idx int) {
	p.rows[idx].Summary = defaultSummary(p.rows[idx])
	if p.opts.Summarize != nil {
		p.pending = append(p.pending, idx)
	}
}

// takePending 取走待算摘要的行下标清单（调用方已持锁；取走后清单归调用方，
// 投影器不再持有——钩子的执行与回填不占用内部锁）。
func (p *Projector) takePending() []int {
	pend := p.pending
	p.pending = nil
	return pend
}

// flushSummaries 在锁外执行宿主 Summarize 钩子并回填摘要（issue #302）。
//
// 钩子是宿主注入的代码，绝不在投影器内部锁内执行——否则钩子里回查投影器
// （Len/Rows/…）即永久死锁（Apply 持锁 → append → 钩子 → Len → 同一把锁），
// 慢钩子还会阻塞所有并发生产者。分三步：
//
//  1. 逐行锁内快照（拷贝），锁外调钩子；
//  2. 钩子返回空串时沿用默认摘要（与 append 的口径一致）；
//  3. 锁内回填，且只回填「快照之后未被改过」的行（整行比对、Summary 除外）：
//     并发投递下另一生产者可能已归并/推进同一行——那次投递自会把它再挂进自己
//     的待算清单，过期摘要不得覆盖新状态。
//
// 批内重复出现的下标（追加后又归并）只算一次：钩子见到的是行的最终形态。
// 钩子的 panic 不在这里 recover：它沿 Apply/ApplyAll 的调用栈抛回宿主——
// 投递路径与钩子都是宿主自己的代码；抛出时投影已完成、锁已释放，投影器完好。
func (p *Projector) flushSummaries(pending []int) {
	if p.opts.Summarize == nil || len(pending) == 0 {
		return
	}
	type calc struct {
		idx int
		row Row // 锁内快照，钩子只读它
	}
	seen := make(map[int]bool, len(pending))
	var calcs []calc
	for _, idx := range pending {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		p.mu.Lock()
		if idx >= len(p.rows) {
			p.mu.Unlock()
			continue
		}
		row := p.rows[idx]
		p.mu.Unlock()
		if s := p.opts.Summarize(row); s != "" {
			row.Summary = s
		}
		calcs = append(calcs, calc{idx: idx, row: row})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range calcs {
		if c.idx >= len(p.rows) {
			continue
		}
		cur := p.rows[c.idx]
		if cur.Summary == c.row.Summary {
			continue
		}
		want, have := c.row, cur
		want.Summary = ""
		have.Summary = ""
		if want != have {
			continue // 行在快照后被改过：它的摘要归最近一次投递的 flush 管
		}
		p.rows[c.idx].Summary = c.row.Summary
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

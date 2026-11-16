// apply.go —— 事件投递与摘要钩子的执行面（issue #302）。
//
// 锁的边界在这里：apply 在锁内只做纯数据投影并登记待算摘要；宿主 Summarize
// 钩子一律在锁外执行（runSummarize），钩子 panic 被 recover。

package trajectory

import (
	"github.com/yi-nology/goagent-kernel/eventbus"
)

// Apply 投一条事件（增量投影）。
//
// 已经被投过的事件不会被重复计入：后续事件只会**更新**既有行（补结束时刻、
// 把配对状态从「未配对」改成「已配对」），不会追加出第二条行。
//
// 宿主 Summarize 钩子在投影器锁释放之后执行（issue #302）。
func (p *Projector) Apply(ev eventbus.Event) {
	applyOneLocked(p, ev)
	runSummarize(p)
}

// ApplyAll 依次投多条事件（增量投影；与逐条 Apply 等价）。
//
// 钩子在本批事件全部落地、锁释放之后统一执行一次（每行一次，行状态是该批
// 事件后的最终状态）。
func (p *Projector) ApplyAll(evs []eventbus.Event) {
	func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, ev := range evs {
			p.apply(ev)
		}
	}()
	runSummarize(p)
}

// applyOneLocked 持锁投一条事件；defer 解锁保证内部 panic 不把锁留下。
func applyOneLocked(p *Projector, ev eventbus.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apply(ev)
}

// append 追加一行（分配序号与轮内唯一标识、填默认摘要）。
func appendRow(p *Projector, r Row) int {
	p.seq++
	r.Seq = p.seq
	r.ID = claimID(p, r.Turn, r.ID)
	p.rows = append(p.rows, r)
	idx := len(p.rows) - 1
	refreshSummary(p, idx)
	return idx
}

// refreshSummary 按行**当前**字段重算默认摘要，并登记宿主钩子待算。
//
// 两个调用点：行创建时；「结果先到、调用后到」的就地归并时——归并会改写类目与
// 归属字段，旧摘要（例如 tool_result）不能留到归并后（issue #299）。
//
// 调用方必须已持投影器锁：本函数只做纯数据计算，**不调用宿主回调**——钩子
// 统一由 runSummarize 在锁外执行（issue #302）。
func refreshSummary(p *Projector, idx int) {
	r := &p.rows[idx]
	r.Summary = defaultSummary(*r)
	if p.opts.Summarize != nil {
		p.pending = append(p.pending, idx)
	}
}

// runSummarize 在**锁外**执行宿主摘要钩子并回填（issue #302）。
//
// 锁内只登记「哪几行需要重算摘要」，并取一份行的值拷贝作为钩子输入（纯数据
// 快照）。钩子因此可以回查 Rows()/Len()、做 I/O；慢钩子只阻塞当前 Apply 的
// 返回，不阻塞其他生产者。钩子 panic 的归属是宿主：该行退回默认摘要，投影
// 路径继续可用。
func runSummarize(p *Projector) {
	if p.opts.Summarize == nil {
		return
	}
	type item struct {
		idx int
		row Row
	}
	p.mu.Lock()
	pending := p.pending
	p.pending = nil
	items := make([]item, 0, len(pending))
	for _, idx := range pending {
		if idx >= 0 && idx < len(p.rows) {
			items = append(items, item{idx: idx, row: p.rows[idx]})
		}
	}
	p.mu.Unlock()

	for _, it := range items {
		summary, ok := callSummarize(p, it.row)
		if !ok || summary == "" {
			// panic 或返回空串：沿用默认摘要。
			continue
		}
		p.mu.Lock()
		// 行可能被后来的事件就地改写（类目变化会重新登记摘要）：只在仍是
		// 同一观测时回填，否则以更晚那次钩子的结果为准。
		if it.idx < len(p.rows) &&
			p.rows[it.idx].Seq == it.row.Seq && p.rows[it.idx].Kind == it.row.Kind {
			p.rows[it.idx].Summary = summary
		}
		p.mu.Unlock()
	}
}

// callSummarize 调一次宿主钩子；panic 被 recover 并把归属写清楚（宿主钩子，
// 不杀投影路径）。
func callSummarize(p *Projector, r Row) (summary string, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			summary, ok = "", false
		}
	}()
	return p.opts.Summarize(r), true
}

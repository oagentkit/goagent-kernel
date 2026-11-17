// check.go —— 快照不变量的**判据面**（issue #195 起按租户身份判定）。
//
// 身份是 (Tenant, ID)：同一 ID 在不同租户下是两条任务；依赖只在同租户内
// 解析（跨租户引用按不存在处理——租户之间本来就不可见）。空租户 = 默认
// 命名空间，此时身份退化为 ID，判定与旧版逐字一致。
package taskboard

import (
	"cmp"
	"slices"
	"strings"

	"github.com/oekit/pkg/slicex"
)

// 不变量类别。
const (
	CodeEmptyID         = "empty_id"
	CodeDuplicateID     = "duplicate_id"
	CodeUnknownState    = "unknown_state"
	CodeUnknownDep      = "unknown_dep"
	CodeSelfDep         = "self_dep"
	CodeCycle           = "dep_cycle"
	CodeClaimedNoLease  = "claimed_without_lease"
	CodeLeaseNoClaim    = "lease_without_claim"
	CodeBlockedClaim    = "blocked_claim"
	CodeTerminalClaimed = "terminal_claimed"
)

// taskRef 任务身份：(Tenant, ID)。
type taskRef struct {
	tenant string
	id     TaskID
}

// refOf 取任务身份。
func refOf(t Task) taskRef { return taskRef{tenant: t.Tenant, id: t.ID} }

// Check 判一份快照的**不变量**，按 (Tenant, Task, Code) 排序返回（确定性）。
//
// 这是宿主随时能跑的「体检」：不必等出问题，配置加载、每个调度周期都可以跑；
// 它判的是**状态自身是否自相矛盾**，不判「策略对不对」。
//
// 快照可以混合多个租户：判重按 (Tenant, ID)，依赖只在同租户内解析。
// 报告行的 Problem.Tenant 指明问题属于哪个租户（空 = 默认命名空间）。
func Check(tasks []Task) []Problem {
	var out []Problem
	byRef := make(map[taskRef]Task, len(tasks))
	dup := map[taskRef]bool{}

	for _, t := range tasks {
		if t.ID == "" {
			out = append(out, Problem{Tenant: t.Tenant, Code: CodeEmptyID,
				Text: "任务没有 id：既无法被依赖引用，也无法被认领定位"})
			continue
		}
		r := refOf(t)
		if _, ok := byRef[r]; ok {
			dup[r] = true
			continue
		}
		byRef[r] = t
	}
	for r := range dup {
		out = append(out, Problem{Tenant: r.tenant, Task: r.id, Code: CodeDuplicateID,
			Text: "同租户内任务 id 重复：依赖引用会指到「其中一个」，而判定方无从知道是哪个"})
	}

	for _, t := range byRef {
		if !t.State.Valid() {
			out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeUnknownState,
				Text: "状态不在词表内（open / claimed / done / failed）"})
		}
		// 认领态与租约必须**同时**成立：只有 owner 没有 token 时，
		// 释放与完成都无从验证（凭内容认人的洞又回来了）。
		if t.State == StateClaimed {
			if t.Owner == "" || t.Lease.Token == "" {
				out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeClaimedNoLease,
					Text: "已认领但没有 owner 或租约令牌：释放与完成都无从验证"})
			}
		} else if t.Owner != "" || !t.Lease.IsZero() {
			// 非认领态残留 owner 或租约任何一列（token/代次/到期时刻）：
			// 宿主会照着它以为「还有人负责」。Until 也要查（issue #220）——
			// 只清 Token/Owner/Fence 而漏 Until 的部分清理，会让「还能不能
			// 被接管」的读路径与「凭 token 认人」的写路径给出相反结论，
			// 而体检这里看不见。
			out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeLeaseNoClaim,
				Text: "非认领态残留 owner/租约（含到期时刻）：读起来像还有人负责，实际没人"})
		}
		// failed 不是终态（见 State.Terminal，issue #375）：失败 + 残留 owner
		// 不报终态占用——它必须还能被重新认领。
		if t.State.Terminal() && t.Owner != "" {
			out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeTerminalClaimed,
				Text: "终态仍带 owner：终态不可重开，残留 owner 会让「谁做完的」与「谁还占着」混在一起"})
		}
		for _, d := range t.Deps {
			if d == t.ID {
				out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeSelfDep,
					Text: "任务依赖自己：永远不会就绪"})
				continue
			}
			if d == "" {
				out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeUnknownDep,
					Text: "依赖里出现空 id"})
				continue
			}
			// 依赖只在同租户内解析：他租户的同 id 任务不可见。
			dep, ok := byRef[taskRef{tenant: t.Tenant, id: d}]
			if !ok {
				out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeUnknownDep,
					Text: "依赖指向不存在的任务: " + string(d)})
				continue
			}
			// 依赖未完成却被认领/完成：前置约束被跳过，两段工作会并行施加。
			// failed 依赖也不算「已完成」——Terminal() 只认 done（issue #375）。
			if t.State == StateClaimed || t.State == StateDone {
				if !dep.State.Terminal() {
					out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeBlockedClaim,
						Text: "依赖 " + string(d) + " 尚未完成，本任务却已认领或完成"})
				}
			}
		}
	}

	// 环按租户分别判：Kahn 拓扑排序排不空即有环。自依赖已在上面单列，
	// 这里排除自环避免重复报。
	for _, tenant := range tenantsOf(byRef) {
		byID := make(map[TaskID]Task)
		for r, t := range byRef {
			if r.tenant == tenant {
				byID[r.id] = t
			}
		}
		if cycle := detectCycle(byID); len(cycle) > 0 {
			out = append(out, Problem{Tenant: tenant, Code: CodeCycle,
				Text: "依赖成环（含 " + joinIDs(cycle) + "）：环上的任务永远不会就绪"})
		}
	}

	slices.SortStableFunc(out, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.Tenant, b.Tenant), cmp.Compare(a.Task, b.Task), cmp.Compare(a.Code, b.Code))
	})
	return out
}

// tenantsOf 返回快照里出现过的租户（排序；缺省空租户也按一项算）。
func tenantsOf(byRef map[taskRef]Task) []string {
	seen := make(map[string]bool, len(byRef))
	out := make([]string, 0, len(byRef))
	for r := range byRef {
		if !seen[r.tenant] {
			seen[r.tenant] = true
			out = append(out, r.tenant)
		}
	}
	slices.Sort(out)
	return out
}

// detectCycle 用 Kahn 算法找出参与环的任务（只报前若干个，够定位就行）。
//
// byID 是**单一租户**内的任务集（调用方已按租户分组）。
func detectCycle(byID map[TaskID]Task) []TaskID {
	indeg := make(map[TaskID]int, len(byID))
	edges := make(map[TaskID][]TaskID, len(byID))
	for id, t := range byID {
		if _, ok := indeg[id]; !ok {
			indeg[id] = 0
		}
		for _, d := range t.Deps {
			if d == id || d == "" {
				continue
			}
			if _, ok := byID[d]; !ok {
				continue
			}
			edges[d] = append(edges[d], id)
			indeg[id]++
		}
	}
	var queue []TaskID
	for id, n := range indeg {
		if n == 0 {
			queue = append(queue, id)
		}
	}
	slices.Sort(queue)
	done := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		done++
		for _, next := range edges[id] {
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if done == len(indeg) {
		return nil
	}
	var left []TaskID
	for id, n := range indeg {
		if n > 0 {
			left = append(left, id)
		}
	}
	slices.Sort(left)
	return left[:min(len(left), 5)]
}

// joinIDs 把 id 列表拼成一段可读文本。
func joinIDs(ids []TaskID) string {
	return strings.Join(slicex.Map(ids, func(id TaskID) string { return string(id) }), ", ")
}

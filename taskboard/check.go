// check.go —— 快照不变量的**判据面**（issue #195 起按租户身份判定）。
//
// 身份是 (Tenant, ID)：同一 ID 在不同租户下是两条任务；依赖只在同租户内
// 解析（跨租户引用按不存在处理——租户之间本来就不可见）。空租户 = 默认
// 命名空间，此时身份退化为 ID，判定与旧版逐字一致。
//
// # 结构取舍（issue #548 分配治理，判据零变化）
//
// 判定语义是判据，分配是成本——两者分开记：语义面由 taskboard_test 的
// 用例集钉住（不变量逐条 / 判重 / 租户隔离 / 环 / 确定性），本文件只动
// **数据形状**。三条取舍：
//
//   - **索引驻留**：身份 → int32 索引一次驻留（瘦值 map），Task 值只住
//     输入切片——旧形 map[taskRef]Task 把胖值（Deps/Lease/Tenant）拷进
//     map 桶，200 任务 ~205 次增长分配（BENCH-BASELINE 回退主因）；
//   - **CSR 邻接**：环检测的邻接表两遍成形（head+to 扁平数组），旧形
//     map[TaskID][]TaskID 逐边 append ~500 次增长分配（基线即有热点）；
//   - **单遍分组**：租户成员集在首轮收集，不再全量重扫重建（旧形逐租户
//     O(T×N) 重扫 + 胖值二拷）。
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

	// 索引驻留（瘦值）+ 租户成员集（单遍）：Task 值不进 map。
	idx := make(map[taskRef]int32, len(tasks))
	members := map[string][]int32{}
	var dup map[taskRef]bool // 惰性：无重复的常态路径零分配（旧形空 map 常驻）

	for i, t := range tasks {
		if t.ID == "" {
			out = append(out, Problem{Tenant: t.Tenant, Code: CodeEmptyID,
				Text: "任务没有 id：既无法被依赖引用，也无法被认领定位"})
			continue
		}
		r := refOf(t)
		if _, ok := idx[r]; ok {
			// 第二个起记重复（按身份归一——A×3 只报一条重复）。
			if dup == nil {
				dup = make(map[taskRef]bool)
			}
			dup[r] = true
			continue
		}
		idx[r] = int32(i)
		members[t.Tenant] = append(members[t.Tenant], int32(i))
	}
	for r := range dup {
		out = append(out, Problem{Tenant: r.tenant, Task: r.id, Code: CodeDuplicateID,
			Text: "同租户内任务 id 重复：依赖引用会指到「其中一个」，而判定方无从知道是哪个"})
	}

	// 不变量逐条：只看每个身份的**首现**（重复的后续行已由上段报过，
	// 不再参与状态判定——与旧形 byRef 只收首现同一语义）。
	for i := range tasks {
		t := tasks[i]
		if t.ID == "" {
			continue
		}
		if idx[refOf(t)] != int32(i) {
			continue
		}
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
			depIdx, ok := idx[taskRef{tenant: t.Tenant, id: d}]
			if !ok {
				out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeUnknownDep,
					Text: "依赖指向不存在的任务: " + string(d)})
				continue
			}
			// 依赖未完成却被认领/完成：前置约束被跳过，两段工作会并行施加。
			// failed 依赖也不算「已完成」——Terminal() 只认 done（issue #375）。
			if t.State == StateClaimed || t.State == StateDone {
				if !tasks[depIdx].State.Terminal() {
					out = append(out, Problem{Tenant: t.Tenant, Task: t.ID, Code: CodeBlockedClaim,
						Text: "依赖 " + string(d) + " 尚未完成，本任务却已认领或完成"})
				}
			}
		}
	}

	// 环按租户分别判：Kahn 拓扑排序排不空即有环。自依赖已在上面单列，
	// 这里排除自环避免重复报。租户序确定性（排序），与旧形 tenantsOf 同。
	tenants := make([]string, 0, len(members))
	for name := range members {
		tenants = append(tenants, name)
	}
	slices.Sort(tenants)
	for _, tenant := range tenants {
		if cycle := detectCycle(tasks, members[tenant], idx); len(cycle) > 0 {
			out = append(out, Problem{Tenant: tenant, Code: CodeCycle,
				Text: "依赖成环（含 " + joinIDs(cycle) + "）：环上的任务永远不会就绪"})
		}
	}

	slices.SortStableFunc(out, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.Tenant, b.Tenant), cmp.Compare(a.Task, b.Task), cmp.Compare(a.Code, b.Code))
	})
	return out
}

// detectCycle 用 Kahn 算法找出参与环的任务（只报前若干个，够定位就行）。
//
// members 是**单一租户**内任务的全局索引（首现去重，调用方已按租户分组）；
// 邻接表 CSR 两遍成形（head/to 扁平数组）——逐边 append 的增长分配是
// 基线里的最大热点（issue #548），两遍换一次分配。
// 参与环 = Kahn 后仍入度 >0 的任务（含环的下游——它们永远不会就绪，
// 与旧形同集）；返回值按 TaskID 排序、至多 5 个。
func detectCycle(tasks []Task, members []int32, idx map[taskRef]int32) []TaskID {
	k := len(members)
	if k == 0 {
		return nil
	}
	// 全局索引 → 局部序（0..k-1），-1 = 非本租户成员。
	local := make([]int32, len(tasks))
	for i := range local {
		local[i] = -1
	}
	for li, g := range members {
		local[g] = int32(li)
	}

	// 边语义与旧形一致：d → id（id 依赖 d ⇒ d 是前置，完成后 id 入度 -1）。
	// 第一遍数边（head 前缀和），第二遍填 to。
	indeg := make([]int32, k)
	head := make([]int32, k+1)
	for li, g := range members {
		t := tasks[g]
		for _, d := range t.Deps {
			if d == t.ID || d == "" {
				continue
			}
			dg, ok := idx[taskRef{tenant: t.Tenant, id: d}]
			if !ok {
				continue // 跨租户/不存在：已由 CodeUnknownDep 报过，不进环图
			}
			dl := local[dg]
			if dl < 0 {
				continue
			}
			head[dl+1]++
			indeg[li]++
		}
	}
	for i := 0; i < k; i++ {
		head[i+1] += head[i]
	}
	to := make([]int32, head[k])
	cursor := make([]int32, k)
	copy(cursor, head[:k])
	for li, g := range members {
		t := tasks[g]
		for _, d := range t.Deps {
			if d == t.ID || d == "" {
				continue
			}
			dg, ok := idx[taskRef{tenant: t.Tenant, id: d}]
			if !ok {
				continue
			}
			dl := local[dg]
			if dl < 0 {
				continue
			}
			to[cursor[dl]] = int32(li)
			cursor[dl]++
		}
	}

	// Kahn：入度 0 入队（处理序不影响结果集——完成集与剩余集都是序无关的）。
	queue := make([]int32, 0, k)
	for li := 0; li < k; li++ {
		if indeg[li] == 0 {
			queue = append(queue, int32(li))
		}
	}
	done := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		done++
		for ei := head[id]; ei < head[id+1]; ei++ {
			next := to[ei]
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if done == k {
		return nil
	}
	var left []TaskID
	for li := 0; li < k; li++ {
		if indeg[li] > 0 {
			left = append(left, tasks[members[li]].ID)
		}
	}
	slices.Sort(left)
	return left[:min(len(left), 5)]
}

// joinIDs 把 id 列表拼成一段可读文本。
func joinIDs(ids []TaskID) string {
	return strings.Join(slicex.Map(ids, func(id TaskID) string { return string(id) }), ", ")
}

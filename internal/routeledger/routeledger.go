// Package routeledger —— 路线达成对账（issue #279 的判据形态兑现）。
//
// 生命周期: Experimental（v0.74.0 定级）——internal 内部件，Go 可见性规则已挡住
// 外部引用，无对外兼容承诺；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 这条判据补的是什么缺口
//
// 总纲单（issue #279）声明的路线此前只以散文存在：issue 正文里的阶段表格 +
// 台账 §4 的「目标态定义」。两处都写着「已全部达成」，但**没有任何机器判据**
// 去核这句话 ⇒ 「路线是否真的全达成」只能靠人读两段散文下结论。
//
// 而总纲单在台账 §9 的**判据形态**写的就是「台账与本文档一致」⇒ 那句话
// 此前**没有执行体**。本包就是它的执行体。
//
// # 三条判据（都能被机器判，失败形态是变红）
//
//  1. **登记面自身形状**：阶段 id 唯一 / 成员非空 / 出口条件非空 / 标题非空 /
//     总纲单已声明 / 同一阶段内单号不重复。形状歪了 ⇒ 后面每条判据都不可信。
//  2. **成员达成**：每个阶段的每个成员，在台账登记面里必须**已合入**
//     （派生状态 = closed，即台账写了「已合入」并给出关闭 PR 号）⇒ 否则红，
//     并**点名**是哪个阶段的哪个单号、台账里它实际是什么状态。
//  3. **总纲单在册**：总纲单在台账登记面里必须有一行 ⇒ 总纲单从台账消失 ⇒
//     台账与本文档不再一致 ⇒ 红。
//
// # 判据与数据源分离
//
// 判据是**纯函数**（登记面 + 台账派生事实 ⇒ 结论）。台账的解析**不在本包做**：
// 单号 → 状态的唯一派生器是 internal/ledgerstatus（#455 立的那条纪律——
// 两处各解析一遍台账，结论必然漂移，而漂移的方向恰好是「门禁判的和台账写的
// 不是一套」）。
//
// # 判据边界（如实登记）
//
// 机器判得了「已登记的路线成员是否都达成」，判不了「什么算一个路线阶段」——
// 那是人的判断。故新增阶段必须人维护登记面；**漏登记的后果是新阶段的成员不被
// 本门禁核**，这是已知边界，不由机器兜。
//
// GitHub 侧的「已关闭」**不在本判据里**：它由 ledgercheck 判据 5 对台账里所有
// 登记单号全量对账 ⇒ 本判据重复判一次就是同一事实两处判定（必然漂移）。
package routeledger

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
)

// Registry 是路线登记面（docs/ROUTE-LEDGER.yaml）的形状。
type Registry struct {
	// Updated 登记面版本戳（YYYY-MM-DD）。
	Updated string `yaml:"updated"`
	// OverviewIssue 总纲单单号（路线总览本身）。
	OverviewIssue int `yaml:"overview_issue"`
	// Stages 路线阶段。
	Stages []Stage `yaml:"stages"`
}

// Stage 一个路线阶段。
type Stage struct {
	// ID 阶段标识（登记面内唯一）。
	ID string `yaml:"id"`
	// Title 阶段标题。
	Title string `yaml:"title"`
	// Issues 该阶段的成员单号（不含 #）。
	Issues []int `yaml:"issues"`
	// ExitCriteria 出口条件（一句话，可机判验收的汇总）。
	ExitCriteria string `yaml:"exit_criteria"`
}

// Result 一次判定的结论。
type Result struct {
	// Problems 阻断级问题（每条都能指到具体阶段 / 单号）。
	Problems []string
	// Stages 阶段数。
	Stages int
	// Members 成员单号数（去重后的唯一单号）。
	//
	// ⚠️ 与 Achieved **必须是同一种量**：一个单号可以跨阶段（#274 同时属 L1/L2），
	// 按出现次数累加会让「18 个成员 / 19 个已达成」这种两个数对不上的报告出现
	// ⇒ 读报告的人会以为多了一个单号。
	Members int
	// Achieved 已达成（台账里已合入）的**唯一**成员单号数。
	Achieved int
}

// Judge 判路线登记面与台账是否一致。
//
// 判据是**纯函数**（登记面 + 台账派生事实 ⇒ 结论），不联网、不读外部数据源。
func Judge(reg *Registry, ledger *ledgerstatus.Ledger) Result {
	var res Result
	if reg == nil || ledger == nil {
		res.Problems = append(res.Problems,
			"路线登记面或台账派生事实缺失：判据跑不起来（跑不起来的门禁等于没有门禁）")
		return res
	}
	res.Stages = len(reg.Stages)

	// 判据 1：登记面自身形状。
	res.Problems = append(res.Problems, checkShape(reg)...)

	// 判据 2：成员逐条达成。
	members := map[int]bool{}
	achieved := map[int]bool{}
	for _, st := range reg.Stages {
		for _, n := range st.Issues {
			members[n] = true
			if !ledger.Registered(n) {
				res.Problems = append(res.Problems, fmt.Sprintf(
					"路线阶段 %s 的成员 #%d 在台账登记面里没有登记：台账与本文档不一致",
					st.ID, n))
				continue
			}
			if ledger.Status(n) != ledgerstatus.StatusClosed {
				res.Problems = append(res.Problems, fmt.Sprintf(
					"路线阶段 %s 的成员 #%d 在台账里是「%s」（未清偿）⇒ 路线未达成",
					st.ID, n, statusText(ledger.Status(n))))
				continue
			}
			achieved[n] = true
		}
	}
	res.Members = len(members)
	res.Achieved = len(achieved)

	// 判据 3：总纲单在册。
	if reg.OverviewIssue > 0 && !ledger.Registered(reg.OverviewIssue) {
		res.Problems = append(res.Problems, fmt.Sprintf(
			"总纲单 #%d 在台账登记面里没有登记：总纲单从台账消失 ⇒ 台账与本文档不再一致",
			reg.OverviewIssue))
	}

	sort.Strings(res.Problems)
	return res
}

// checkShape 判登记面自身的形状。
func checkShape(reg *Registry) []string {
	var problems []string
	if _, err := time.Parse("2006-01-02", reg.Updated); err != nil {
		problems = append(problems, fmt.Sprintf(
			"路线登记面的 updated %q 不是 YYYY-MM-DD：登记面自己的版本戳都不可信", reg.Updated))
	}
	if reg.OverviewIssue <= 0 {
		problems = append(problems, "路线登记面没有声明总纲单（overview_issue）")
	}
	seenStage := map[string]bool{}
	for _, st := range reg.Stages {
		if strings.TrimSpace(st.ID) == "" {
			problems = append(problems, "路线登记面里有一个阶段没有 id（无法定位）")
			continue
		}
		if seenStage[st.ID] {
			problems = append(problems, fmt.Sprintf(
				"路线阶段 id %q 重复登记：同一个 id 有两条阶段 ⇒ 成员与出口条件对不上时无从定位",
				st.ID))
		}
		seenStage[st.ID] = true
		if strings.TrimSpace(st.Title) == "" {
			problems = append(problems, fmt.Sprintf(
				"路线阶段 %s 没有标题：读者要判断「这条现在到底有没有做」，靠的是它", st.ID))
		}
		if len(st.Issues) == 0 {
			problems = append(problems, fmt.Sprintf(
				"路线阶段 %s 没有成员单号：一个没有成员的阶段不是阶段", st.ID))
		}
		if strings.TrimSpace(st.ExitCriteria) == "" {
			problems = append(problems, fmt.Sprintf(
				"路线阶段 %s 没有出口条件：阶段没有可判的完成口径 ⇒ 「已达成」没人能核", st.ID))
		}
		seenIssue := map[int]bool{}
		for _, n := range st.Issues {
			if n <= 0 {
				problems = append(problems, fmt.Sprintf(
					"路线阶段 %s 的成员单号 %d 不合法（单号是正整数）", st.ID, n))
				continue
			}
			if seenIssue[n] {
				problems = append(problems, fmt.Sprintf(
					"路线阶段 %s 的成员 #%d 重复登记", st.ID, n))
			}
			seenIssue[n] = true
		}
	}
	return problems
}

// statusText 把派生状态翻成人话（报告里要能读）。
func statusText(s ledgerstatus.Status) string {
	switch s {
	case ledgerstatus.StatusOpen:
		return "仍开着"
	case ledgerstatus.StatusClosed:
		return "已合入"
	case ledgerstatus.StatusUnknown:
		return "派生不出"
	}
	return string(s)
}

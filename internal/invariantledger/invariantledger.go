// Package invariantledger —— 执行不变量与插点矩阵的绑定对账（issue #523 的判据形态兑现）。
//
// 生命周期: Experimental（v0.81.0 定级）——internal 内部件，Go 可见性规则已挡住
// 外部引用，无对外兼容承诺；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 这条判据补的是什么缺口
//
// 评审（2026-11-27 §3.3 问题二）指出：机制正确不代表系统正确——七类不变量
// （状态迁移 / 任务所有权 / 副作用 / Checkpoint 单调 / 终态唯一 / 事件可恢复 /
// 多副本竞争）散落在各包注释、测试名与演进台账的散文里，**没有一处能回答**
// 「这条不变量的判定机制是什么、哪条测试在守、缺什么」；插点故障矩阵是点名式
// 的（挑高危场景），某个步骤间隙零场景**不显形**。
//
// ⇒ docs/INVARIANTS.md 是单源登记面（两张表）：不变量登记表 + 插点矩阵。
// 本包是它的执行体：登记了却没有对应测试 ⇒ 红并点名；某个间隙既无场景也无
// 书面豁免 ⇒ 红（零静默空隙）。
//
// # 三条判据（都能被机器判，失败形态是变红）
//
//  1. **登记面自身形状**：评审点名的七类不变量 id 齐全且不重复；插点矩阵
//     G1–G7 七个间隙齐全且不重复；每行的机制 / 成文处 / 缺口格非空。
//     形状歪了 ⇒ 后面每条判据都不可信。
//  2. **判据绑定存在**：每条登记的必红判据 / 矩阵场景指针（`dir/TestName`）
//     必须在仓内 *_test.go 里真实存在——「清单里写着一条不存在的测试」
//     等于这条不变量没人守，且比没有清单更坏（读者以为有人守）。
//  3. **零静默空隙**：每个步骤间隙要么至少一条场景指针，要么非空的书面豁免
//     （豁免必须写理由，「—」不算理由）。
//
// # 判据与数据源分离
//
// 判据是**纯函数**（登记面 + 仓内测试事实 ⇒ 结论）。md 表的解析在本包
// （ParseDoc——那是「把声明面读成结构」，不是判定）；测试指针的存在性事实
// 由调用方（cmd/invariantcheck）扫描仓后注入 ⇒ 用例可确定性测、不联网。
//
// # 判据边界（如实登记）
//
// 机器判得了「登记的测试存在」，判不了「这条测试真的会在 CI 跑」——后者由
// `make unit` / `make fault-injection` 的分层口径兑底（两目标都跑 `go test ./...`
// 形态的全量包，TestFault* 归 fault-injection 层）。测试被 t.Skip 掉是否算
// 「没在守」不在本判据内：那是测试自身的质量面，由 review 与偶发红单（如 #465）
// 管。同理，矩阵格里的「缺口」标记只被允许出现在登记时——它表示「这一格
// 已知没场景」，行级仍须有场景或豁免兜底。
package invariantledger

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RequiredInvariants 评审（2026-11-27 §3.3 问题二）点名的七类不变量 id。
//
// 硬编码在判据里而不是登记面里：这七类是本阶段的**验收口径本体**——从登记面
// 删一行就能让门禁绿的清单守不住任何东西。新增第八类 ⇒ 改这里（code review
// 可见），同时在 docs/INVARIANTS.md 补行。
var RequiredInvariants = []string{
	"state-transition",
	"task-ownership",
	"sideeffect-once",
	"checkpoint-monotonic",
	"terminal-uniqueness",
	"event-recoverability",
	"multi-replica-race",
}

// RequiredGaps 插点矩阵的七个步骤间隙 id（链条：创建 → 领取 → 工具执行 →
// 副作用提交 → 事件发布 → Checkpoint 保存 → 终态提交 → 终态事件）。
//
// 硬编码理由同 RequiredInvariants：间隙集合就是评审问的「崩溃发生在任意两个
// 步骤之间会怎样」的那张表，少登记一行 = 有一个插点不再被核。
var RequiredGaps = []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7"}

// Registry 是登记面（docs/INVARIANTS.md 两张表）解析后的形状。
type Registry struct {
	// Updated 登记面版本戳（YYYY-MM-DD）。
	Updated string
	// Invariants 不变量登记表（七行）。
	Invariants []InvariantRow
	// Gaps 插点矩阵（七行）。
	Gaps []GapRow
}

// InvariantRow 不变量登记表的一行。
type InvariantRow struct {
	// ID 不变量 id（RequiredInvariants 之一）。
	ID string
	// Statement 不变量陈述（一句话可判）。
	Statement string
	// Mechanism 判定机制（哪个包哪个函数）。
	Mechanism string
	// Docs 成文处（包注释 / ADR / 文档指针）。
	Docs string
	// Judgments 必红判据（`dir/TestName` 指针，至少一条）。
	Judgments []string
	// Gap 该类已知缺口（必须非空；没有缺口就写「无」——显式声明，不许留白）。
	Gap string
}

// GapRow 插点矩阵的一行。
type GapRow struct {
	// ID 间隙 id（G1–G7）。
	ID string
	// Evidence 六个故障类型格里的场景指针（`dir/TestName`）。
	Evidence []string
	// Waiver 书面豁免（该行零场景时必填理由；有场景时可为「—」）。
	Waiver string
}

// TestIndex 仓内测试事实：`dir/TestName` 全键集合（由调用方扫描注入）。
type TestIndex map[string]bool

// Result 一次判定的结论。
type Result struct {
	// Problems 阻断级问题（每条都能指到具体行 / 指针）。
	Problems []string
	// Invariants 登记的不变量行数。
	Invariants int
	// Gaps 登记的间隙行数。
	Gaps int
	// Refs 登记的测试指针数（去重后）。
	Refs int
	// Waived 靠书面豁免而非场景兜底的间隙数（回显不判红——豁免是合法出口）。
	Waived int
}

// Judge 判登记面与仓内测试事实是否一致。
//
// 判据是**纯函数**（登记面 + 测试事实 ⇒ 结论），不联网、不读文件系统。
func Judge(reg *Registry, idx TestIndex) Result {
	var res Result
	if reg == nil || idx == nil {
		res.Problems = append(res.Problems,
			"不变量登记面或仓内测试事实缺失：判据跑不起来（跑不起来的门禁等于没有门禁）")
		return res
	}
	res.Invariants = len(reg.Invariants)
	res.Gaps = len(reg.Gaps)
	res.Problems = append(res.Problems, checkShape(reg)...)

	// 判据 2：判据绑定存在（登记的每条测试指针必须真实存在）。
	refs := map[string]bool{}
	for _, inv := range reg.Invariants {
		for _, ref := range inv.Judgments {
			refs[ref] = true
			if !idx[ref] {
				res.Problems = append(res.Problems, fmt.Sprintf(
					"不变量 %s 登记的必红判据 %s 在仓内不存在：清单里写着一条没人守的测试，比没有清单更坏",
					inv.ID, ref))
			}
		}
	}
	for _, g := range reg.Gaps {
		for _, ref := range g.Evidence {
			refs[ref] = true
			if !idx[ref] {
				res.Problems = append(res.Problems, fmt.Sprintf(
					"插点 %s 登记的场景 %s 在仓内不存在：矩阵格指着一条不存在的测试",
					g.ID, ref))
			}
		}
	}
	res.Refs = len(refs)

	// 判据 3：零静默空隙（每行要么有场景，要么有书面豁免）。
	for _, g := range reg.Gaps {
		if len(g.Evidence) == 0 {
			if strings.TrimSpace(g.Waiver) == "" || g.Waiver == "—" {
				res.Problems = append(res.Problems, fmt.Sprintf(
					"插点 %s 既无场景指针也无书面豁免：这个步骤间隙是静默空隙（评审问的「崩溃发生在两个步骤之间」它答不上）",
					g.ID))
				continue
			}
			res.Waived++
		}
	}

	sort.Strings(res.Problems)
	return res
}

// checkShape 判登记面自身的形状。
func checkShape(reg *Registry) []string {
	var problems []string
	if _, err := time.Parse("2006-01-02", reg.Updated); err != nil {
		problems = append(problems, fmt.Sprintf(
			"不变量登记面的 Updated %q 不是 YYYY-MM-DD：登记面自己的版本戳都不可信", reg.Updated))
	}

	// 七类不变量：齐全、不重复、id 合法、各格非空、判据至少一条。
	seen := map[string]bool{}
	for _, inv := range reg.Invariants {
		if seen[inv.ID] {
			problems = append(problems, fmt.Sprintf(
				"不变量 %s 重复登记：同一行两条 ⇒ 绑定对不上时无从定位", inv.ID))
		}
		seen[inv.ID] = true
		if !isKnownID(inv.ID, RequiredInvariants) {
			problems = append(problems, fmt.Sprintf(
				"不变量 id %q 不在评审点名的七类里（%s）：id 拼错或自造类别都让「七类全覆盖」这句话失去意义",
				inv.ID, strings.Join(RequiredInvariants, " / ")))
			continue
		}
		for _, field := range []struct{ name, val string }{
			{"陈述", inv.Statement},
			{"判定机制", inv.Mechanism},
			{"成文处", inv.Docs},
			{"缺口", inv.Gap},
		} {
			if strings.TrimSpace(field.val) == "" {
				problems = append(problems, fmt.Sprintf(
					"不变量 %s 的%s格为空：缺口格不许留白——没有缺口就写「无」，显式声明才可复核",
					inv.ID, field.name))
			}
		}
		if len(inv.Judgments) == 0 {
			problems = append(problems, fmt.Sprintf(
				"不变量 %s 没有登记任何必红判据：一条没有测试守着的不变量只是愿望", inv.ID))
		}
	}
	for _, id := range RequiredInvariants {
		if !seen[id] {
			problems = append(problems, fmt.Sprintf(
				"评审点名的不变量 %s 没有登记：七类全覆盖是本阶段的出口条件", id))
		}
	}

	// 七个间隙：齐全、不重复、id 合法。
	gseen := map[string]bool{}
	for _, g := range reg.Gaps {
		if gseen[g.ID] {
			problems = append(problems, fmt.Sprintf(
				"插点 %s 重复登记：同一间隙两条 ⇒ 场景与豁免对不上时无从定位", g.ID))
		}
		gseen[g.ID] = true
		if !isKnownID(g.ID, RequiredGaps) {
			problems = append(problems, fmt.Sprintf(
				"插点 id %q 不在 G1–G7 里：间隙 id 是链条位置的坐标，自造坐标让矩阵无法对账", g.ID))
		}
	}
	for _, id := range RequiredGaps {
		if !gseen[id] {
			problems = append(problems, fmt.Sprintf(
				"步骤间隙 %s 没有登记：矩阵缺一行 = 这个插点不再被核（静默空隙）", id))
		}
	}
	return problems
}

// isKnownID 判 id 是否在允许集里。
func isKnownID(id string, allowed []string) bool {
	for _, a := range allowed {
		if id == a {
			return true
		}
	}
	return false
}

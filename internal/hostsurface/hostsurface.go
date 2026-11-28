// Package hostsurface —— 宿主接入面清单的绑定对账（issue #524 的判据形态兑现）。
//
// 生命周期: Experimental（v0.81.0 定级）——internal 内部件，Go 可见性规则已挡住
// 外部引用，无对外兼容承诺；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 这条判据补的是什么缺口
//
// 评审（2026-11-27 §3.4 方向二）要的「统一运行契约」：让 Runtime 不需要理解
// 几十个包的内部机制——Kernel 对外只暴露少量稳定的高层契约。此前宿主视角的
// 接入叙事散在 49 个包的 README 表与 3000 行 USAGE 里，「宿主必须接哪几个、
// 可选接哪几个、哪些根本不该由宿主碰」没有一份清单，更没有判据去核
// 「文档教宿主 import 的东西是不是内部件」。
//
// ⇒ docs/HOST-SURFACE.md 是声明面（六步主链对照 + 全量包三分类），本包是它的
// 执行体：分类漏一个包 ⇒ 红；文档代码块 import 了内部件 ⇒ 红。
//
// # 三条判据（都能被机器判，失败形态是变红）
//
//  1. **清单覆盖**：分类表必须覆盖 api-lifecycle 登记的**全部**生产包，分类值
//     只许「必须接 / 可选 / 内部件」三档，每行职责一句话非空——漏登记一个包
//     = 那个包对宿主视角隐形，与 api-lifecycle「新增包不登记」同向。
//  2. **主链六步**：创建 / 领取 / 执行 / 提交 / 恢复 / 介入六步齐全，每步
//     「宿主提供」「kernel 兑现」两格非空——没有对照表，「统一运行契约」
//     就只是一句口号。
//  3. **文档 import 白名单**：README 与 USAGE 代码块里的 kernel 自 import，
//     落点必须是清单里「必须接 / 可选」的包——文档教宿主 import 内部件，
//     等于把内部实现当公共 API 卖（评审 §3.3 问题一点名的形态）。
//
// # 判据与数据源分离
//
// 判据是**纯函数**（清单 + 包清单事实 + 文档 import 事实 ⇒ 结论）。md 表解析
// 在本包（ParseDoc）；api-lifecycle 的包全集与文档代码块的抽取由调用方
// （cmd/hostsurfacecheck）注入 ⇒ 用例可确定性测、不联网。
//
// # 判据边界（如实登记）
//
// 机器判得了「文档没教内部件」，判不了「六步对照写得对不对」——那是内容质量，
// 归 review。import 抽取只认代码块里的 import 声明形态（单行与分组），正文
// 里提到的包名不算 import；CHANGELOG 等历史快照不在判定面内（它们按定义
// 记录的是过去的状态）。
package hostsurface

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 三档分类值域（登记表只能取这三个值）。
const (
	ClassRequired = "必须接"
	ClassOptional = "可选"
	ClassInternal = "内部件"
)

// RequiredClasses 允许的分类集合（顺序供报告用）。
var RequiredClasses = []string{ClassRequired, ClassOptional, ClassInternal}

// 主链六步的固定 id（评审要求的「创建 → 领取 → 执行 → 提交 → 恢复 → 介入」
// 对照表；id 硬编码同 invariantledger 的验收口径纪律——少一步 = 那一步的
// 「宿主提供 / kernel 兑现」不再被核）。
//
// 逐个声明成常量而不是在切片里写字面量：`execute` 等词同时属别家词表
// （statemachine / invocation），裸字面量只许出现在常量定义处——引用任一家
// 的常量又可能是语义损坏，故本包自命名（harness-word-table-handcopy 判据）。
const (
	stepCreate    = "create"
	stepClaim     = "claim"
	stepExecute   = "execute"
	stepCommit    = "commit"
	stepRecover   = "recover"
	stepIntervene = "intervene"
)

// RequiredSteps 六步 id 集合（顺序供报告用）。
var RequiredSteps = []string{stepCreate, stepClaim, stepExecute, stepCommit, stepRecover, stepIntervene}

// Registry 是登记面（docs/HOST-SURFACE.md 两张表）解析后的形状。
type Registry struct {
	// Updated 登记面版本戳（YYYY-MM-DD）。
	Updated string
	// Steps 主链六步对照（六行）。
	Steps []StepRow
	// Packages 全量包三分类（覆盖 api-lifecycle 全集）。
	Packages []PackageRow
}

// StepRow 主链一步。
type StepRow struct {
	// ID 步骤 id（RequiredSteps 之一）。
	ID string
	// Name 步骤名（人读标签）。
	Name string
	// HostProvides 宿主提供什么。
	HostProvides string
	// KernelDelivers kernel 兑现什么。
	KernelDelivers string
}

// PackageRow 一个包的接入面分类。
type PackageRow struct {
	// Pkg 包路径（与 api-lifecycle 的 pkg 逐字一致）。
	Pkg string
	// Class 分类（必须接 / 可选 / 内部件）。
	Class string
	// Duty 一句话职责（非空）。
	Duty string
}

// Facts 仓内事实（调用方注入）。
type Facts struct {
	// RegisteredPackages api-lifecycle 登记的全部包（pkg → true）。
	RegisteredPackages map[string]bool
	// DocImports 文档代码块里的 kernel 自 import（包路径 → true，
	// 已归一到清单口径的相对包路径，如 "taskboard/boardtest"）。
	DocImports map[string]bool
}

// Result 一次判定的结论。
type Result struct {
	// Problems 阻断级问题（每条都能指到具体行 / 具体 import）。
	Problems []string
	// Steps 步骤行数。
	Steps int
	// Packages 分类行数。
	Packages int
	// Imports 核过的文档 import 数（去重）。
	Imports int
}

// Judge 判登记面与仓内事实是否一致。
//
// 判据是**纯函数**，不联网、不读文件系统。
func Judge(reg *Registry, f Facts) Result {
	var res Result
	if reg == nil || f.RegisteredPackages == nil || f.DocImports == nil {
		res.Problems = append(res.Problems,
			"接入面登记面或仓内事实缺失：判据跑不起来（跑不起来的门禁等于没有门禁）")
		return res
	}
	res.Steps = len(reg.Steps)
	res.Packages = len(reg.Packages)
	res.Imports = len(f.DocImports)

	res.Problems = append(res.Problems, checkShape(reg)...)
	res.Problems = append(res.Problems, checkCoverage(reg, f)...)
	res.Problems = append(res.Problems, checkImports(reg, f)...)

	sort.Strings(res.Problems)
	return res
}

// checkShape 判登记面自身的形状。
func checkShape(reg *Registry) []string {
	var problems []string
	if _, err := time.Parse("2006-01-02", reg.Updated); err != nil {
		problems = append(problems, fmt.Sprintf(
			"接入面登记面的 Updated %q 不是 YYYY-MM-DD：登记面自己的版本戳都不可信", reg.Updated))
	}

	// 主链六步：齐全、不重复、两格非空。
	seenStep := map[string]bool{}
	for _, st := range reg.Steps {
		if seenStep[st.ID] {
			problems = append(problems, fmt.Sprintf(
				"主链步骤 %s 重复登记：同一步两条 ⇒ 对照对不上时无从定位", st.ID))
		}
		seenStep[st.ID] = true
		if !isKnownID(st.ID, RequiredSteps) {
			problems = append(problems, fmt.Sprintf(
				"主链步骤 id %q 不在六步（%s）里：步骤是接入叙事的坐标，自造坐标让对照表无法对账",
				st.ID, strings.Join(RequiredSteps, " / ")))
			continue
		}
		if strings.TrimSpace(st.HostProvides) == "" {
			problems = append(problems, fmt.Sprintf(
				"主链步骤 %s 的「宿主提供」为空：没写宿主那半边，对照表就只剩 kernel 自说自话", st.ID))
		}
		if strings.TrimSpace(st.KernelDelivers) == "" {
			problems = append(problems, fmt.Sprintf(
				"主链步骤 %s 的「kernel 兑现」为空：没写 kernel 那半边，宿主不知道接了能得到什么", st.ID))
		}
	}
	for _, id := range RequiredSteps {
		if !seenStep[id] {
			problems = append(problems, fmt.Sprintf(
				"主链步骤 %s 没有登记：六步对照缺一步 = 那一步的契约没人写", id))
		}
	}

	// 分类行：id 合法、分类在值域内、职责非空、不重复。
	seenPkg := map[string]bool{}
	for _, p := range reg.Packages {
		if seenPkg[p.Pkg] {
			problems = append(problems, fmt.Sprintf(
				"包 %s 重复登记：同一包两条分类 ⇒ 判到哪条无从定位", p.Pkg))
		}
		seenPkg[p.Pkg] = true
		if !isKnownID(p.Class, RequiredClasses) {
			problems = append(problems, fmt.Sprintf(
				"包 %s 的分类 %q 不在三档（%s）里：分类是接入承诺，值域外的取值宿主读不懂",
				p.Pkg, p.Class, strings.Join(RequiredClasses, " / ")))
		}
		if strings.TrimSpace(p.Duty) == "" {
			problems = append(problems, fmt.Sprintf(
				"包 %s 的职责格为空：一句话职责是「这个包对宿主意味着什么」的唯一提示，不许留白", p.Pkg))
		}
	}
	return problems
}

// checkCoverage 判分类表覆盖 api-lifecycle 全集（双向）。
func checkCoverage(reg *Registry, f Facts) []string {
	var problems []string
	seen := map[string]bool{}
	for _, p := range reg.Packages {
		seen[p.Pkg] = true
		if !f.RegisteredPackages[p.Pkg] {
			problems = append(problems, fmt.Sprintf(
				"分类表里的包 %s 不在 api-lifecycle 登记面里：接入面分类必须挂在生命周期清单上，不许自造包名", p.Pkg))
		}
	}
	for pkg := range f.RegisteredPackages {
		if !seen[pkg] {
			problems = append(problems, fmt.Sprintf(
				"api-lifecycle 登记的包 %s 没有接入面分类：漏一个包 = 那个包对宿主视角隐形（清单必须覆盖全集）", pkg))
		}
	}
	return problems
}

// checkImports 判文档代码块 import 只落点在「必须接 / 可选」。
func checkImports(reg *Registry, f Facts) []string {
	var problems []string
	classOf := map[string]string{}
	for _, p := range reg.Packages {
		classOf[p.Pkg] = p.Class
	}
	for _, imp := range sortedKeys(f.DocImports) {
		class, ok := classOf[imp]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"文档代码块 import 了未登记包 %s：README/USAGE 教的 import 必须能对上接入面分类（先去 api-lifecycle 登记）", imp))
			continue
		}
		if class == ClassInternal {
			problems = append(problems, fmt.Sprintf(
				"文档代码块 import 了内部件 %s：内部件不是宿主面——文档教宿主 import 内部实现，等于把内部件当公共 API 卖", imp))
		}
	}
	return problems
}

// isKnownID 判值是否在允许集里。
func isKnownID(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// sortedKeys 确定性遍历（报告顺序稳定，用例可断言）。
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

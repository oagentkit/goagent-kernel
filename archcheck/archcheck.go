// Package archcheck 是 architecture-policy.yaml 的**可执行面**。
//
// 生命周期: Experimental（v0.33.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 为什么它是一个可导入的包，而不是一个放在 cmd/ 下的脚本
//
// 存在意义：policy 文件若只当文档读，半年后就与代码事实脱节；把它做成门禁，
// 规则违反即非零退出。但门禁执行体若锁在 package main 里，别的仓就**用不上**——
// 于是每个宿主仓要么各写一遍判据（各判一套，漂移到没人发现为止），要么干脆不写
// （policy 的依赖拓扑表变成死条文：躺在文件里、看起来有人维护过，实际一条都不判）。
//
// 所以这一层被下沉成包：宿主仓 import 它、喂自己的 policy、拿自己的判定结果，
// 不复制一行实现。
//
// # 判据与实现分离
//
// 判据是**声明**（policy 文件）+ **执行体**（本包），两者分开：
//
//   - 声明面：modules 段的依赖拓扑、rules 段的正则、facades 段的门面包清单
//   - 执行面：本包的 AST 判定（go/ast 判 import、判函数体、判依赖方向）
//
// 结构性判定一律走 go/ast，不走逐行正则——import 声明有六种以上书写形态
// （单行 / 块 / 别名 / 空白导入 / 点导入 / 括号同行），逐行正则必然漏，
// 且漏得毫无声响。
//
// # 门禁的失败形态是「变绿」，不是「变红」
//
// 判据写错时最危险的不是报错，而是**什么都不判**却照样报「无阻断级违反」。
// 故本包把「给不出结论」与「没违规」严格分开：解析失败、policy 自相矛盾、
// 规则没有执行体——一律响亮失败（返回 error 或报成违反），绝不静默降级。
//
// # 复用方须知
//
//  1. policy 的 modules 段要求每个 root 指向**真实存在**的包目录：陈旧的 root
//     会被判成违反（「陈旧包根当天即红」），因为它让该模块对门禁隐形。
//  2. 跨仓依赖用 `ext:<import 路径前缀>` 表达，如
//     `ext:github.com/yi-nology/goagent-kernel/netguard`。
//  3. 报告用 WriteReport；是否阻断用 Result.HasBlocking。
//
// # 调用面禁令与路径白名单怎么表达
//
// policy **没有** `forbid_call_regex` / `allow_paths` 字段——判据面只有两种：
// **import 路径**（`forbid_import_regex`）与**源码文本**（`forbid_regex`）。
// 常见的两种诉求用现有字段就能表达：
//
//	| 诉求 | 表达方式 |
//	|---|---|
//	| 「某个第三方包只允许经某一个适配层引入」（路径白名单）| `forbid_import_regex` + `scope_exclude: [适配层目录]` |
//	| 「某目录禁止裸 fetch(，但 api 客户端目录与两个 SSE 钩子除外」（调用面禁令 + 路径白名单）| `forbid_regex`（匹配调用形态）+ `scope_exclude`（豁免目录）|
//
// 为什么不再加第三种判据面：每多一种判据面，policy 作者就要多记一套豁免语义
// （负向前置的锚点写在哪、目录豁免算不算……）。本包已经因为「同一字段在两个分支
// 语义不同」吃过一次亏（import 分支不认 `negative_follow`，静默假红），那次的
// 修法是**让两个分支同构**，而不是再加一种。
package archcheck

import (
	"fmt"
	"time"
)

// RuleInfo 是报告用的一条规则元信息（ID、说明、级别）。
//
// 为什么单独一个类型：facades 段与 modules 段的规则**不在 rules 段里**，
// 它们没有 policy 里的 Rule 结构可复用，但报告形状必须一致。
type RuleInfo struct {
	ID       string
	Desc     string
	Severity string
}

// Result 是一次门禁运行的结果。
type Result struct {
	// Violations 是全部违反（含警告级与已命中例外的）。
	Violations []Violation
	// Blocking 是阻断级违反数，Warnings 是警告级数。
	Blocking int
	Warnings int
	// Excused 是命中已登记例外（且未到期）的违反数。
	Excused int
	// ExpiredExceptions 是已到期的例外——它们不再豁免任何东西。
	ExpiredExceptions []string
	// ThresholdFindings 是结构阈值（超长文件 / god object）的发现。
	ThresholdFindings []string
	// ThresholdEnforced 报告阈值是否阻断。
	ThresholdEnforced bool
	// Rules 是报告顺序下的规则清单。
	Rules []RuleInfo

	// TypeAwarePackages 类型感知严格档（RunOptions.TypeAware）实际类型检查的
	// 包数；0 = 未启用。它是「严格档真的跑了」的凭据——开关开着而本仓有包
	// 目录却计到 0 的形态，RunWithOptions 已显式报错（类型解析失败 ≠ 无违规）。
	TypeAwarePackages int
	// Exceptions 是 policy 登记的例外清单（回显用，让债是显式的）。
	Exceptions []Exception

	// excused 记下被例外豁免的违反键，报告时不回显它们。
	excused map[string]bool
}

// HasBlocking 报告本次运行是否应当以非零退出码阻断。
//
// 阈值门禁只在 enforce 为真时阻断——它是「报告口径」还是「真阈值」由 policy 决定。
func (r *Result) HasBlocking() bool {
	if r == nil {
		return false
	}
	if r.Blocking > 0 {
		return true
	}
	return r.ThresholdEnforced && len(r.ThresholdFindings) > 0
}

// ReportOptions 控制报告输出形状。
type ReportOptions struct {
	// Full 为真时输出全部违反，否则每条规则截断到 10 条。
	Full bool
}

// Run 对 root 执行全套门禁，返回结果。
//
// 返回 error 表示门禁**给不出结论**（policy 解析失败、源码解析失败、声明自相矛盾）——
// 调用方必须据此非零退出，绝不能当成「无违反」继续。
// RunOptions Run 的可选开关。
type RunOptions struct {
	// TypeAware 开类型感知严格档（go/packages + go/types，issue #103）：
	// 覆盖赋值形态里 AST 追踪静态不可溯的盲区（函数返回值 / 接口断言 /
	// 深层容器流转）。代价是整仓类型检查（慢一个量级），故是 opt-in 档；
	// 类型解析失败显式报错，绝不当作无违规放行（unmanaged_gotypes.go）。
	TypeAware bool
}

// Run 对 root 执行全套门禁（默认档，不开类型感知严格档）。
func Run(root string, pol *Policy) (*Result, error) {
	return RunWithOptions(root, pol, RunOptions{})
}

// RunWithOptions 对 root 执行全套门禁，opts 控制可选档。
//
// 返回 error 表示门禁**给不出结论**（policy 解析失败、源码解析失败、声明自相矛盾、
// 严格档类型解析失败）——调用方必须据此非零退出，绝不能当成「无违反」继续。
func RunWithOptions(root string, pol *Policy, opts RunOptions) (*Result, error) {
	if pol == nil {
		return nil, fmt.Errorf("policy 为空")
	}
	// 解析根里的符号链接：filepath.Walk 不跟随软链，根是软链时整棵子树都扫不到
	// （见 FindRoot 的注）。宿主复用本执行体时传的根同样可能是软链 ⇒ 解析放在
	// 入口这一处，不依赖调用方自觉。
	real, err := realPath(root)
	if err != nil {
		return nil, err
	}
	root = real

	// 扫描面守门（失败形态必须是**变红**，不是变绿）：仓库里一个 Go 源文件都
	// 没有 ⇒ 门禁根本没判过任何东西 ⇒ 绝不能报「无阻断级违反」。
	//
	// 为什么必须兜底：空扫描面下，「每条规则都没扫到文件」与「没有违规」在
	// 报告里长得一模一样（都是 0 违反），而前者其实是「给不出结论」。
	// 这条兜底让任何让扫描面落空的形态（根是软链没解析、检出只拿到一半、
	// policy 指错根）都在这里变成非零退出，而不是一份绿色的报告。
	files, err := iterFiles(root, []string{"*.go"}, false)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("扫描面为空：仓库里没有任何 Go 源文件（根=%s）——门禁给不出结论，不能报「无违反」", root)
	}
	res := &Result{excused: map[string]bool{}}

	// 规则段：正则式规则，逐条判定。
	for _, r := range pol.Rules {
		vs, err := checkRule(root, r)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// 门面包不可变：AST 判定（facade.go），不走正则规则表——这条纪律是结构性的。
	for _, fr := range pol.Facades {
		vs, err := facadeViolations(root, fr)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// 包间依赖方向：modules[].requires 的执行体（deps.go），同样是 AST 判定。
	// 声明面早就在了，缺的是执行面——没有它，依赖拓扑就是散文。
	if pol.DepDir.on() {
		vs, err := dependencyViolations(root, pol.Modules, pol.DepDir)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// 执行沙箱后端的档位登记：policy 的 sandbox_providers 段 ↔ 代码事实（sandbox.go）。
	//
	// 段缺省也算开启：**有后端实现却没登记**必须报出来——「没登记就当没这回事」
	// 正是这条门禁要防的静默失效。
	sbox := sandboxProvidersOf(pol)
	if sbox.on() {
		vs, err := sandboxProviderViolations(root, sbox)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// 执行路径门禁：执行必须经 sandbox 登记簿（execution.go）。
	//
	// 段缺省也算开启——「执行必须经登记簿」是默认要求，不是「登记了才管」。
	execPaths := executionPathsOf(pol)
	if execPaths.on() {
		vs, err := executionPathViolations(root, execPaths)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// 未受管逃逸门禁：生产代码不得显式选择「无判定环执行」（unmanaged.go）。
	//
	// PTC 的安全默认是 fail-closed（有绑定无判定环 ⇒ 拒绝执行），
	// 但那留了一个逃逸口。逃逸口若能在生产代码里随手写，它就从「显式声明」
	// 退化成「默认路径」——而默认路径是没人会去看的地方。
	vs, err := unmanagedEscapeViolations(root)
	if err != nil {
		return nil, err
	}
	if opts.TypeAware {
		tv, checked, err := typeAwareViolations(root)
		if err != nil {
			return nil, err
		}
		vs = dedupUnmanagedEscape(append(vs, tv...))
		res.TypeAwarePackages = checked
	}
	res.Violations = append(res.Violations, vs...)

	// 子进程出口门禁：每个 spawn 点都要登记出口档位，要求经代理的必须看得见兑现
	// （egresspaths.go）。段缺省也算开启——「这个子进程在强制面内吗」必须有人问。
	egPaths := egressPathsOf(pol)
	if egPaths.on() {
		vs, err := egressPathViolations(root, egPaths)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// 租户过滤漏拼门：服务层「授权上下文 + 租户域返回」必须有隔离证据，
	// 登记在册的共享判定面必须真实存在（tenantgate.go）。段缺省即关闭——
	// 授权类型/租户字段/扫描面是判定前提，没有形状就判不了（声明了段却漏
	// 形状由 policy 自校验报红）。
	if pol.TenantGate != nil && pol.TenantGate.on() {
		vs, err := tenantGateViolations(root, *pol.TenantGate)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	if err := appendSeamViolations(res, root, pol); err != nil {
		return nil, err
	}

	// 判定词表登记门禁：登记的词表必须有导出枚举出口，且消费方必须**引用**
	// 而不是手抄（wordtable.go）。段缺省即关闭——「判哪几张表」由各仓自己登记，
	// 机制面只管「登记了就得守住，登记与事实偏离当天即红」。
	if pol.WordTables != nil && pol.WordTables.on() {
		vs, err := wordTableViolations(root, pol)
		if err != nil {
			return nil, err
		}
		res.Violations = append(res.Violations, vs...)
	}

	// policy 自校验：门禁自己也得能被检查（规则没有执行体、id 重复、
	// 阈值判定面漏包、例外指向不存在的规则、僵尸例外……）。详见 selfcheck.go。
	//
	// 例外对账（「这一条例外有没有命中它声称的那条违反」，issue #447）需要**实际
	// 违规集**：此处业务违规已收集完毕、selfcheck 自己的产出尚未混入 ⇒ 对账面干净。
	violated := make(map[string]bool, len(res.Violations))
	for _, v := range res.Violations {
		violated[exceptionKey(v.Rule, v.Path, v.Line)] = true
	}
	res.Violations = append(res.Violations, selfCheckWithViolationSet(root, pol, violated)...)

	// severity 分流：error 违规阻断，warn 仅报告（口径见 ruleinfo.go）。
	sevOf := severityMap(pol)

	// exceptions 精确豁免：rule + path + line 三元组命中即放行（不做前缀模糊豁免）。
	//
	// **已到期的例外不再豁免**：policy 要求的 `until` 字段到此才真正生效。
	// 旧实现解析了 until 就再没人看过它，于是「写个日期」这个动作没有任何
	// 约束力——一条该在 2026 年清偿的债可以无限期豁免下去（详见 selfcheck.go）。
	now := time.Now()
	exKey := map[string]Exception{}
	res.Exceptions = pol.Exceptions
	for _, ex := range pol.Exceptions {
		exKey[exceptionKey(ex.Rule, ex.Path, ex.Line)] = ex
		if expired, why := exceptionExpiry(ex, now); expired {
			res.ExpiredExceptions = append(res.ExpiredExceptions,
				fmt.Sprintf("%s:%d [%s] %s —— %s", ex.Path, ex.Line, ex.Rule, ex.Violation, why))
		}
	}
	for _, v := range res.Violations {
		key := exceptionKey(v.Rule, v.Path, v.Line)
		if ex, ok := exKey[key]; ok {
			if expired, _ := exceptionExpiry(ex, now); !expired {
				res.Excused++
				res.excused[key] = true
				continue
			}
			// 已过期：不再豁免，该违规照常计入阻断集（债到期即重新欠账）。
		}
		if sevOf[v.Rule] != "warn" {
			res.Blocking++
		} else {
			res.Warnings++
		}
	}

	// 规则清单（报告顺序：rules 段 → 门面包 → 依赖方向 → policy 自校验；
	// 口径见 ruleinfo.go）。
	res.Rules = ruleInfos(pol, sevOf)

	// ── 阈值门禁（结构债）──
	th := pol.Thresholds
	res.ThresholdEnforced = th.Enforce
	findings, err := thresholdFindings(root, th)
	if err != nil {
		return nil, err
	}
	res.ThresholdFindings = findings

	return res, nil
}

// CheckDependencyDirection 只跑包间依赖方向这一条门禁。
//
// 单独暴露它，是因为它是**跨包契约**的门禁：别的仓接入本机制时通常先接这一条
// （policy 里已经有 modules 段，只是没有执行体）。

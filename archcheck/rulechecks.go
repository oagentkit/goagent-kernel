// rulechecks.go —— policy 的**规则级**声明面自校验。
//
// # 为什么单独成文件
//
// selfcheck.go 原本把「规则级」判定（id 唯一 / 有执行体 / check_kind 取值域 /
// scope 存在 / include 模式 / severity 合法 / 样例一致与覆盖 / import_exempt 形状）
// 与「policy 级、仓级」判定（模块根、阈值扫描根、门包、目录豁免、词表、租户门、
// 例外登记）混在一个函数里。规则级那一组随规则字段增多而变长（新增 check_kind
// 一族判定后），把 policy 级判定挤出了 max_file_lines=400 的阈值线。
//
// 两组的归属也不同：前者只读 pol.Rules 与仓库目录，后者要读 policy 的各个段
// ——分开读，各看各的，判据不容易串。
//
// # 返回
//
// 规则 id 集合（供例外登记校验「它指向的规则是否存在」）与问题清单。
package archcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ruleSelfCheck 校验 policy 的规则级声明面。
func ruleSelfCheck(root string, pol *Policy, policyPath string) (map[string]bool, []Violation) {
	var out []Violation
	add := func(path string, line int, format string, args ...any) {
		out = append(out, Violation{
			Rule: selfCheckRuleID,
			Path: path,
			Line: line,
			Text: fmt.Sprintf(format, args...),
			Kind: "policy_self_check",
		})
	}

	// ── 规则 id 唯一 + 每条规则都有执行体 + severity 合法 ──
	seen := map[string]bool{}
	for _, r := range pol.Rules {
		if r.ID == "" {
			add(policyPath, 0, "存在没有 id 的规则（无法定位、无法豁免）")
			continue
		}
		if seen[r.ID] {
			add(policyPath, 0, "规则 id %q 重复：两条规则共用一个 id，豁免与警告都无法定位到具体哪条", r.ID)
		}
		// 与内置规则重名：例外按「rule + path + line」三元组豁免，两条判定共用一个
		// id ⇒ 一条例外会同时落到两条判定上，豁免不再精确（且 built-in 的 severity
		// 恒定覆盖 policy 的声明，写在这里的那条 severity 是装饰）。
		if isBuiltinRule(r.ID) {
			add(policyPath, 0, "规则 id %q 与内置规则重名：内置判定由执行体恒定实现，"+
				"policy 里的这条同名规则既不生效、又会与它共用豁免键", r.ID)
		}
		seen[r.ID] = true

		// 没有执行体的规则 = 永远判不了的规则。旧实现把它当「声明面字段」
		// 静默跳过，于是「写错字段名」与「故意声明一条说明」不可区分。
		//
		// 报错要给出**可执行的下一步**：policy 里没有 forbid_call_regex /
		// allow_paths 这类字段（判据面只有 import 路径与源码文本两种），
		// 写了会被 YAML 解析静默丢弃——于是规则就成了「声明还在、一条都判不了」。
		if r.ForbidRegex == "" && r.ForbidImport == "" && r.CheckKind == "" {
			add(policyPath, 0,
				"规则 %q 没有执行体（forbid_regex、forbid_import_regex 与 check_kind 皆空）：它一条都判不了，"+
					"却仍计入「N 条规则」的报告——这是最隐蔽的门禁失效形态。"+
					"调用面禁令用 forbid_regex 表达、路径白名单用 scope_exclude 表达；"+
					"policy 里没有 forbid_call_regex / allow_paths 这两个字段，写了会被 YAML 静默丢弃",
				r.ID)
		}
		// ── check_kind 的取值域必须封闭，且一条规则只能有一个执行体 ──
		//
		// 写错的类别名若被当成「不是已知类别」，这条登记就**静默降级**成「不判」：
		// 规则在、severity 是 error、报告里照常计入条数，只是永不触发。
		// 失败形态是门禁**变绿**——比变红更贵，因为它看起来一切正常。
		if r.CheckKind != "" {
			if !knownCheckKind(r.CheckKind) {
				add(policyPath, 0,
					"规则 %q 的 check_kind %q 不在已知取值域 %v 内：它没有对应执行体，"+
						"于是这条规则一条都判不了（severity 仍是 error、报告里仍计入条数）",
					r.ID, r.CheckKind, knownCheckKindList())
			}
			if r.ForbidRegex != "" || r.ForbidImport != "" {
				add(policyPath, 0,
					"规则 %q 同时声明了 check_kind 与 forbid_regex/forbid_import_regex："+
						"一条规则只能有一个执行体，否则两套判定各判各的、结论可能相反", r.ID)
			}
			// test_no_direct_call 的判定面**来自** forbid_calls：没声明被禁符号时
			// 执行体一条都判不了（返回零违规），而规则在、severity 是 error、报告里
			// 照常计入条数 ⇒ 与「字段名拼错」同一类的静默失效，失败形态是门禁变绿。
			if r.CheckKind == checkKindTestNoDirectCall && len(r.ForbidCalls) == 0 {
				add(policyPath, 0,
					"规则 %q 声明了 check_kind %s 却没有 forbid_calls：被禁符号是它的判定面，"+
						"不声明就一条都判不了（规则在、severity 是 error、报告里仍计入条数）",
					r.ID, checkKindTestNoDirectCall)
			}
		}

		// 有执行体却 scope 为空 ⇒ 一行都不判。
		//
		// ruleFiles 只遍历 Scope 里的目录，空清单返回空文件集；于是这条规则有正则、
		// severity 是 error、报告里也计入「N 条规则」，但它永远不触发。
		// 这与「字段名拼错」是同一类，只是拼错已由 Load 的严格解析兜住；
		// 而 `scope: []` 是合法 YAML、拼写也对，只能靠这条判据。
		if (r.ForbidRegex != "" || r.ForbidImport != "" || r.CheckKind != "") && len(r.Scope) == 0 {
			add(policyPath, 0,
				"规则 %q 有执行体但 scope 为空：判定面只遍历 scope 里的目录，空清单等于它**一行都不判**——"+
					"规则在、severity 是 error、报告里也计入条数，只是永不触发", r.ID)
		}

		// forbid_import_regex 的**结构**必须可能命中真正的 import 路径（issue #41）。
		//
		// 判据面是 import 路径，而 Go 的 import 路径永远是 clean 形式：不含 '//'、
		// 不以 '/' 结尾。于是「前缀尾部多写一个斜杠」的写法（(前缀/)(/|$)）
		// 一条都匹配不到——规则在、severity 是 error、报告里也计入条数，只是永不触发。
		//
		// 这与「没有执行体」是同一类静默失效，只是更隐蔽：正则非空、编译通过、
		// 报告里看不出任何异常。判据本体在 importpattern.go（走 regexp/syntax
		// 判结构，不走文本比 '//'——后者既会误报字符类 [//]，又抓不到本条
		// 真正要防的 (前缀/)(/|$)：那里面并没有相邻的两个斜杠）。
		if r.ForbidImport != "" {
			if why := importPatternDefect(r.ForbidImport); why != "" {
				add(policyPath, 0,
					"规则 %q 的 forbid_import_regex 结构上命中不了任何 import 路径：%s"+
						"（Go 的 import 路径永远是 clean 形式；边界要写成 (前缀)(/|$)，前缀尾部不带斜杠）",
					r.ID, why)
			}
		}

		// scope 项指向不存在的目录 ⇒ 同样一行都不判。
		//
		// ruleFiles 对不存在的 scope 是静默跳过的（它分不清「拼错」与「这个包
		// 已经改名/迁走」），于是失败形态与上一条同源：规则照常计入「N 条规则」、
		// severity 照常是 error、报告里照常是绿的，唯独不再判任何东西。
		//
		// 这是本仓已经把同一把尺子用在 modules.roots / facades / ignore_dirs 上之后
		// 剩下的最后一处**没有**被要求「指向真实存在的目录」的声明面。包改名是
		// 最普通的一次重构，而它会让整条规则静默失效——这正是「陈旧包根当天
		// 即红」要防的事，只是当时只补了 modules 段。
		for _, scope := range r.Scope {
			scope = strings.TrimSpace(scope)
			if scope == "" {
				add(policyPath, 0,
					"规则 %q 的 scope 里有空条目（空字符串不扫任何目录，只让清单看起来更长）", r.ID)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(scope))); err != nil {
				// 带上底层错误：权限 / I/O 失败与「目录真的不存在」都会走到这里，
				// 而两者的修法完全不同（改 policy vs 改权限）——文案不许把后者说成前者。
				add(policyPath, 0,
					"规则 %q 的 scope 项 %q 不存在或无法读取（%v）：ruleFiles 对该 scope 静默跳过，"+
						"于是这条规则**一行都不判**（报告里仍计入「N 条规则」，只是永不触发）；"+
						"包改名 / 迁走后最容易踩，声明与事实已经偏离", r.ID, scope, err)
			}
		}

		// ── include 模式必须可能命中一个文件名 ──
		//
		// 文件发现只拿 filepath.Base(path) 与模式比（见 scan.go），于是「语法非法」
		// 与「含路径分隔符」两类写法让规则一个文件都不扫却继续绿——与 scope 指向
		// 不存在目录同源。判据本体与理由在 includecheck.go 头注。
		//
		// 跳过没有执行体的规则：那种规则一条都判不了、已由上一条报出，同一件事不说两遍。
		if r.ForbidRegex != "" || r.ForbidImport != "" || r.CheckKind != "" {
			for _, problem := range includePatternProblems(r.ID, r.Include) {
				add(policyPath, 0, "%s", problem)
			}
		}

		switch r.Severity {
		case "", "error", "warn":
		default:
			add(policyPath, 0, "规则 %q 的 severity %q 非法（只允许 error / warn / 缺省）", r.ID, r.Severity)
		}

		// ── 规则自带的样例必须与判据一致（加载期验活）──
		//
		// 这是 P1-5 的落点：把「这条规则还活着吗」从**事故后回答的问题**变成
		// **加载期的断言**。判据复用真实判定路径（见 examples.go 头注）。
		for _, problem := range exampleViolations(r) {
			add(policyPath, 0, "%s", problem)
		}
		if pol.RequireRuleExamples {
			if (r.ForbidRegex != "" || r.ForbidImport != "" || r.CheckKind != "") && len(r.Examples) == 0 {
				add(policyPath, 0,
					"规则 %q 没有自带 examples（require_rule_examples: true 已开）："+
						"有执行体却没有样例的规则，无法在加载期证明它还判得动——词表被改名、"+
						"module 路径被迁走之后，它会静静地不再命中，而门禁继续绿着", r.ID)
			}
			// 「有样例」还不等于「样例够」：只有反例的规则只能证明判据不乱报，
			// 证明不了判据还判得动（见 exampleCoverageProblems 头注）。
			for _, problem := range exampleCoverageProblems(r) {
				add(policyPath, 0, "%s", problem)
			}
			// 双向覆盖仍不等于**每个分支**都被钉住：多分支正则里未被样例命中的
			// 那一支被删掉/改名时，门禁不会变红（见 exampleBranchCoverageProblems
			// 头注，issue #386）。
			for _, problem := range exampleBranchCoverageProblems(r) {
				add(policyPath, 0, "%s", problem)
			}
		}

		// ── import_exempt 项的形状必须可能生效 ──
		//
		// 例外的语义是「放行这一条 import 路径」。若某一项**形状上就不可能
		// 匹配**（空串、带尾斜杠、含通配/正则字符），它的效果是「一条都没放行」。
		//
		// 不判的后果：宿主会看到 import 被拒，却不知道是自己登记的那条豁免
		// 没生效——**报错指向 import，真正的问题在 policy 里**（误诊）。
		// 而「policy 写了例外但它不生效」正是本仓纪律里最贵的那一类静默失效。
		//
		// 判的是**形状**（确定性、不误报），不是「当前有没有命中」
		// （命中与否分不清「写错」与「将来才用」，硬判就是误报）。
		for _, e := range r.ImportExempt {
			if defect := importExemptDefect(e); defect != "" {
				add(policyPath, 0,
					"规则 %q 的 import_exempt 项 %q 无效：%s", r.ID, e, defect)
			}
		}
	}

	return seen, out
}

// selfcheck.go —— policy 自身的自校验 + 例外到期机制。
//
// # 为什么门禁需要检查门禁
//
// 本仓的纪律是「从没见过它触发」的规则等于没有规则。这句话有半边是显然的
// （没人验证过的规则可能根本不触发），另半边容易被忽略：**规则也可能以
// 「声明了但没有执行体」的形式存在**——policy 里写了一条 severity: error 的
// 规则，但它的 forbid_regex 字段名拼错了，于是反序列化后两个正则都是空串，
// 旧实现 `return nil, nil` 把它当成「声明面字段」静默跳过。规则还在、severity
// 还是 error、报告里还写着「7 条规则」，但它一条都判不了。
//
// 这与「门禁失效」的失败形态完全一致：门禁变绿。区别只在于这次绿得特别安详。
//
// 同一类问题还有几处：
//
//   - 规则 id 重复 → 两条规则共用一个 id，豁免与警告都无法定位
//   - thresholds.scan_roots 漏了 modules 里的某个包 → 该包对阈值门禁隐形
//     （本仓真实发生过：guardset 登记在 modules 段却不在 scan_roots 段，
//     于是「超长文件」「god object」两条阈值对它从来不生效）
//   - exceptions 里的规则 id 不存在 → 例外指向一条不存在的规则
//   - rules[].include 写的模式结构上不可能命中一个文件名（"**/*.go" 带路径分隔符、
//     "*.[go" 语法非法）→ 该规则一个文件都不扫，报告里照常计入「N 条规则」
//
// 全部由本文件一次性判掉，且**只判机制、不判内容**——它不评价某条规则该不该存在，
// 只保证「存在着的规则确实有执行体、确实在跑、确实被阈值覆盖」。
//
// # 例外到期
//
// policy 的例外登记格式要求写 `until` 与 `removal`（拆除条件）。旧实现把
// `until` 解析成字段后就再没人看过它——**到期不失效**。于是「写个日期」的
// 动作没有任何约束力：一条例外在 2020 年就该清偿的债，可以无限期豁免下去，
// 且每一条都还躺在 policy 里看着像有人管过。
//
// 本文件让 `until` 真正生效：到期后该例外**不再豁免**它原先豁免的违规，
// 违规重新计入阻断集，同时明确回显「此例外已过期」。这是「债到期」的纪律
// 在机器上的落点：到期不是提醒，是重新欠账。

package archcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// selfCheckRuleID policy 自校验这条规则自己的 ID。
const selfCheckRuleID = "harness-policy-self-check"

// selfCheckViolations 校验 policy 自身的结构一致性。
//
// 判定面是 policy 文件 + 仓库实际目录，不涉及任何业务代码。
//
// 例外对账那一条（「例外命中不到任何违反」）**需要实际违规集**，纯 policy 层
// 判不了 ⇒ 由 selfCheckWithViolationSet 判；本入口传 nil，只判不依赖违规集的
// 那些形态（各判据的单测走这条）。
func selfCheckViolations(root string, pol *Policy) []Violation {
	return selfCheckWithViolationSet(root, pol, nil)
}

// selfCheckWithViolationSet 同 selfCheckViolations，另带本轮**实际查出的违规集**
// （键为 exceptionKey(rule, path, line)），用于例外与实际命中面的对账（issue #447）。
//
// 为什么这条必须拿到违规集：例外的失效形态是**静默的**——站点被改、文件被拆、
// 规则口径变了之后，那条例外命中不到任何违反，门禁照样绿，而它仍躺在声明面上
// 「像是还有人管的已知债」。until 只在例外**命中**时才有意义：命中不到时它连
// 到期都不会报 ⇒ 已不需要的豁免可以永久留在声明面。
//
// violated 为 nil ⇒ 跳过该判据（纯 policy 层调用）。主路径（Run）必须传真实违规集，
// 否则这条判据静默不判——由 TestRunFlagsZombieException 端到端钉住。
func selfCheckWithViolationSet(root string, pol *Policy, violated map[string]bool) []Violation {
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

	// policy 自身的位置：宿主仓的 policy 文件不一定叫 architecture-policy.yaml，
	// 写死文件名会让违规指向一个不存在的文件（复用方看到的位置是错的）。
	policyPath := PolicyPath(root, pol)

	// ── 规则级声明面自校验（主体在 rulechecks.go）──
	seen, ruleProblems := ruleSelfCheck(root, pol, policyPath)
	out = append(out, ruleProblems...)

	// seams 段判据面自检（issue #369/#370）：主体拆到 seamrules.go，保持本文件
	// 在 max_file_lines=400 内，把新增空间留给判据本身而不是搬运。
	checkSeamSelfPolicy(pol, policyPath, add)

	// ── 阈值判定面必须覆盖所有登记在 modules 段的包 ──
	//
	// 漏一个包，该包对「超长文件」「god object」两条阈值门禁**完全隐形**：
	// 它可以长到 1000 行、可以挂 60 个方法，阈值门禁一声不吭。
	// 本仓真实发生过（guardset），所以这条自校验是必须项而非锦上添花。
	if pol.Thresholds.Enforce && (pol.Thresholds.MaxFileLines > 0 || pol.Thresholds.MaxMethodsPerType > 0) {
		for _, m := range pol.Modules {
			for _, r := range m.Roots {
				if r == "" {
					continue
				}
				if !coveredByAnyRoot(pol.Thresholds.ScanRoots, r) {
					add(policyPath, 0,
						"包 %q 登记在 modules 段却不在 thresholds.scan_roots 段："+
							"它对「超长文件」「god object」两条阈值门禁隐形（可长到任意行数而不报）", r)
				}
			}
		}
	}

	// ── 阈值扫描根必须指向真实存在的目录 ──
	//
	// 与上一条同源：scan_roots 项不存在时，那棵树对「超长文件」「god object」
	// 两条阈值**完全隐形**——扫描静默跳过，报告只写着「阈值通过」。
	//
	// 与 modules.roots 的检查互补而不是重复：那一层判「有没有人该扫这个包」，
	// 这一层判「清单里的每一项是不是真的扫到了东西」。路径写错或包迁走后，
	// 前者能过（它只比字符串前缀），后者才红。
	for _, sr := range pol.Thresholds.ScanRoots {
		sr = strings.TrimSpace(sr)
		if sr == "" || sr == "." {
			continue // "." 是仓库根自身，恒存在；空条目由下面的判据单独说
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(sr))); err != nil {
			add(policyPath, 0,
				"thresholds.scan_roots 项 %q 不存在或无法读取（%v）：那棵树对「超长文件」「god object」"+
					"两条阈值完全隐形（扫描静默跳过，报告只写着阈值通过）", sr, err)
		}
	}
	for _, sr := range pol.Thresholds.ScanRoots {
		if strings.TrimSpace(sr) == "" {
			add(policyPath, 0,
				"thresholds.scan_roots 里有空条目（空字符串不扫任何目录，只让清单看起来更长）")
		}
	}

	// ── 转发壳阈值不得为负 ──
	//
	// 负值没有意义：0 已经表达「关闭该区分」（所有方法都算实质方法）。
	// 负值会被判据当成「没有方法是壳」——但它长得像别的意思（有人可能以为
	// 负数 = 无限宽松）。声明面上的歧义要在这里掐掉，而不是留给判据去猜。
	if v := pol.Thresholds.ForwardingShellMaxChars; v != nil && *v < 0 {
		add(policyPath, 0,
			"thresholds.forwarding_shell_max_chars 不得为负（负值没有意义；"+
				"关闭「转发壳不计入阈值」这一区分请显式写 0）")
	}

	// ── 门面包指向的目录必须存在 ──
	for _, fr := range pol.Facades {
		if fr.Package == "" {
			add(policyPath, 0, "门面包缺少 package 字段")
			continue
		}
		if _, err := os.Stat(filepath.Join(root, fr.Package)); err != nil {
			add(policyPath, 0, "门面包 %q 指向的目录不存在：门禁将判一个不存在的包，永远为零违规", fr.Package)
		}
	}

	// ── modules 段登记的包根必须存在 ──
	//
	// 与上面那条同源：声明指向一个不存在的目录时，门禁判的是空气。
	//
	// 这条判定的代价在实测里很具体：机制包迁到外仓那天，policy 里的包根就陈旧了，
	// 而**没有任何东西会读它**——于是声明与事实的偏离一直累积，直到有人把执行体
	// 接上才暴露。接上执行体的第一件事就是让它红：迁出的包根当天即红。
	//
	// 判在 policy 自校验这一层（而不是依赖方向门禁）的原因：自校验恒开，
	// 而依赖方向门禁可以被 policy 关掉；一个指向空目录的包根永远是假的声明。
	for _, m := range pol.Modules {
		for _, r := range m.Roots {
			if r == "" {
				add(policyPath, 0, "模块 %q 的 roots 里有空条目", m.ID)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, r)); err != nil {
				add(policyPath, 0,
					"modules 段模块 %q 登记的包根 %q 不存在：它对依赖方向与阈值门禁都隐形"+
						"（requires 声明随之失效），声明与事实已经偏离", m.ID, r)
			}
		}
	}

	// ── 目录级豁免必须指向真实存在的目录 ──
	//
	// ignore_dirs 是比 exceptions **更宽**的豁免口：按目录整棵跳过、不要求逐项
	// 签字、也不用写拆除条件。它唯一还能自证的东西是「这个目录真的存在」——
	// 指向不存在目录的豁免，与指向空目录的包根是同一种偏离：**声明还在，事实没了**。
	//
	// 而它的失效形态比陈旧包根更隐蔽：陈旧包根至少还有人看报告；一条没人命中过的
	// 豁免会一直躺在 policy 里，等着将来某个恰好同名的新包出现时**无声地把它也
	// 豁免掉**——而那时没有人会重新判断「这个新包该不该被豁免」。
	// 这正是 issue #35 记的那件事：cmd/ 从 2 个包长到 3 个，前缀豁免顺带盖住了
	// 第三個，理由是「archcheck / docsync 是工具」，实际面已经不同。
	for _, sec := range ignoreDirSections(pol) {
		for _, d := range sec.dirs {
			if d == "" {
				add(policyPath, 0, "%s 的 ignore_dirs 有空条目（空字符串不豁免任何目录，只让清单看起来更长）", sec.name)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(d))); err != nil {
				add(policyPath, 0,
					"%s 的 ignore_dirs 项 %q 指向的目录不存在：这条豁免今天什么都没豁免，"+
						"却会在将来出现同名目录时**无声地**把它整棵跳过（豁免必须指向真实存在的目录）",
					sec.name, d)
			}
		}
	}

	// ── 词表登记面（word_tables 段）的声明面一致性 ──
	// 声明面自相矛盾（包名写错、类别写错、归一出口挂错表、重复登记……）
	// 归自校验：它恒开、且提交前就拦；代码事实类（谓词还在不在、出口读不读
	// 那张表）留在词表执行体。同一件事只由一个主人判。
	out = append(out, wordTableSelfCheck(root, policyPath, pol.WordTables)...)

	// ── 租户门（tenant_gate 段）的形状 ──
	//
	// 判定本体在 tenantgate_selfcheck.go：缺形状时执行体没有判定面，必须变红。
	out = append(out, tenantGateSelfCheck(root, policyPath, pol.TenantGate)...)

	// ── 例外登记的完整性 ──
	// 「规则存在」的口径 = policy rules 段的 id ∪ 内置规则 id。
	//
	// 为什么必须并上内置的：内置规则（未受管逃逸门禁、门包门禁、依赖方向门禁等）
	// 不在 rules 段里——它们由本执行体实现或由 policy 的段驱动。旧口径只看 rules 段，
	// 于是宿主照着本执行体的指引（「确需逃逸时在 policy 的 exceptions 里显式登记
	// 豁免与拆除条件」）去登记 PTC 逃逸口的豁免时，自校验反而报它「指向不存在的
	// 规则」——照着指引做就红。而恒开门禁恰恰是最需要豁免的那几条。
	knownRules := make(map[string]bool, len(seen)+len(builtinRuleIDs))
	for id := range seen {
		knownRules[id] = true
	}
	for _, id := range builtinRuleIDs {
		knownRules[id] = true
	}
	for _, ex := range pol.Exceptions {
		// badTarget：例外**指向的东西本身有问题**（规则不存在 / 不在判 / 缺字段……）。
		// 这些形态下它命中不到违反是**已知后果**，下面的僵尸对账不重复报它。
		badTarget := false
		switch {
		case ex.Rule == "":
			badTarget = true
			add(policyPath, 0, "例外登记缺少 rule 字段：无法定位它豁免的是哪条规则")
		case ex.Rule == selfCheckRuleID:
			// 自校验是豁免机制的**前置条件**：豁免它等于让任何声明都能静音——
			// 包括「这条豁免登记本身不合法」这件事。故它不可豁免。
			badTarget = true
			add(policyPath, 0, "例外登记不得豁免自校验规则 %q：自校验是豁免机制的前置条件，"+
				"豁免它等于让任何声明都能静音（含「这条豁免本身不合法」）", ex.Rule)
		case !knownRules[ex.Rule]:
			badTarget = true
			add(policyPath, 0, "例外登记指向不存在的规则 %q：它豁免的东西无从查证", ex.Rule)
		case isBuiltinRule(ex.Rule) && !builtinRuleEnforced(pol, ex.Rule):
			badTarget = true
			add(policyPath, 0, "例外登记指向的规则 %q 当前不在判（该段被显式关闭或该判定面不存在）："+
				"这条豁免今天什么都没豁免，却会在将来那条规则被启用时无声地开始生效", ex.Rule)
		}
		if ex.Until == "" {
			add(policyPath, 0, "例外登记（%s %s:%d）缺少 until 字段：写不出到期日的例外等于永久豁免", ex.Rule, ex.Path, ex.Line)
		}
		if ex.Removal == "" {
			add(policyPath, 0, "例外登记（%s %s:%d）缺少 removal（拆除条件）字段：没有拆除条件的例外是赖账", ex.Rule, ex.Path, ex.Line)
		}

		// ── 僵尸例外（issue #447）：登记了，但命中不到任何违反 ──
		//
		// 判据是**这一条例外有没有命中它声称的那条违反**（按 rule+path+line 三元组
		// 对账），不是「例外条数 == 命中数」那种粗判——不同规则命中数不同、同一站点
		// 可能命中多条规则。
		//
		// 为什么失败形态必须是**变红**：站点被改 / 文件被拆 / 规则口径变了之后，这条例外
		// 就再也命中不到东西，而门禁照样绿 ⇒ 已不需要的豁免可以永久留在声明面上，下一个
		// 看声明面的人会以为「这里还有一条已知债」。本仓最贵的失效形态是「门禁变绿」。
		if violated != nil && !badTarget && !violated[exceptionKey(ex.Rule, ex.Path, ex.Line)] {
			add(policyPath, 0,
				"僵尸例外（%s %s:%d）：命中不到任何违反——该站点今天没有这条规则的违规，"+
					"这条例外什么都没豁免却仍留在声明面上。站点已修复 / 文件已拆 / 规则口径已变的，"+
					"删掉本条例外；确属预防性登记的不成立（例外只豁免**已存在**的违反）",
				ex.Rule, ex.Path, ex.Line)
		}
	}

	return out
}

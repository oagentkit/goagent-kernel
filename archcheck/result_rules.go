// result_rules.go —— 门禁结果的「规则清单」装配（severity 分流 + 报告顺序）。
//
// # 为什么单独成文件
//
// Run() 是门禁的主线：判据 → 结果 → 分流 → 清单。这四步里，「分流」与
// 「清单」是**装配**（把 policy 的声明翻译成报告的形状），不是判据。
// 它们原本长在 Run() 里，把这个文件顶到了 max_file_lines（400 行）附近，
// 而 Run() **还要继续长出判据**——阈值一旦逼着人停止加判据，
// 那条阈值就在保护错误的那个量（与 selfcheck_support.go 同一理由）。
//
// 划界按**用途**而不是按行数：这里全是「给一个 policy，翻译成 severity 表与
// 报告清单」的装配函数，不认识任何判据。

package archcheck

// sandboxBox 取执行沙箱档位登记段（缺省即启用——「有后端却没登记」必须报出来）。
func sandboxBox(pol *Policy) SandboxProviders {
	if pol.SandboxProviders == nil {
		return SandboxProviders{}
	}
	return *pol.SandboxProviders
}

// executionBox 取执行路径门禁段（缺省即启用）。
func executionBox(pol *Policy) ExecutionPaths {
	if pol.ExecutionPaths == nil {
		return ExecutionPaths{}
	}
	return *pol.ExecutionPaths
}

// egressBox 取子进程出口门禁段（缺省即启用）。
func egressBox(pol *Policy) EgressPaths {
	if pol.EgressPaths == nil {
		return EgressPaths{}
	}
	return *pol.EgressPaths
}

// ruleSeverities 装配 severity 表：规则 id → 级别。
//
// 级别缺省即 error：悬空的 warn 等于没有规则（静默放行比误报更贵）。
// 未受管逃逸门禁与 policy 自校验恒为 error——前者是 PTC 安全默认的配套护栏，
// 后者是「给不出结论就必须响亮失败」本身。
func ruleSeverities(pol *Policy) map[string]string {
	sevOf := map[string]string{}
	for _, r := range pol.Rules {
		s := r.Severity
		if s == "" {
			s = "error"
		}
		sevOf[r.ID] = s
	}
	for _, fr := range pol.Facades {
		s := fr.Severity
		if s == "" {
			s = "error"
		}
		sevOf[facadeRuleID] = s
	}
	if pol.DepDir.on() {
		sevOf[depRuleID] = sevOrError(pol.DepDir.Severity)
	}
	if sandboxBox(pol).on() {
		sevOf[sandboxProviderRuleID] = sevOrError(sandboxBox(pol).Severity)
	}
	if executionBox(pol).on() {
		sevOf[executionPathRuleID] = sevOrError(executionBox(pol).Severity)
	}
	if egressBox(pol).on() {
		sevOf[egressPathRuleID] = sevOrError(egressBox(pol).Severity)
	}
	tenantGateSeverities(pol.TenantGate, sevOf)
	// 未受管逃逸门禁恒为 error：它是 PTC 安全默认（缺判定环即拒）的配套护栏——
	// 逃逸口若能在生产代码里随手写，它就从「显式声明」退化成「默认路径」。
	sevOf[unmanagedEscapeRuleID] = "error"
	// 自校验恒为 error：policy 结构有问题时，门禁必须响亮失败，
	// 而不是「其他规则都绿就当通过」——那正是它要防的那种静默失效。
	sevOf[selfCheckRuleID] = "error"
	// 接缝契约门禁：缺省即 error（悬空的 warn 等于没有规则）。
	sevOf[seamRuleID] = seamSeverity(pol)
	return sevOf
}

// appendRuleInfos 把规则清单按报告顺序装配进 Result。
//
// 报告顺序：rules 段 → 门包 → 依赖方向 → 档位登记 → 执行路径 → 出口 →
// 租户 → 未受管逃逸 → policy 自校验 → 接缝契约。
//
// 「新增结构规则必须同时登记进报告清单」：判了却不出现在清单里，
// 报告就看不出它判过什么——那条规则等于「判了但看不见」。
func appendRuleInfos(res *Result, pol *Policy, sevOf map[string]string) {
	for _, r := range pol.Rules {
		res.Rules = append(res.Rules, RuleInfo{ID: r.ID, Desc: r.Desc, Severity: sevOf[r.ID]})
	}
	if len(pol.Facades) > 0 {
		res.Rules = append(res.Rules, RuleInfo{
			ID:       facadeRuleID,
			Desc:     "门包只允许类型别名/常量别名/单条上游转发，实现归上游仓",
			Severity: sevOf[facadeRuleID],
		})
	}
	if pol.DepDir.on() {
		res.Rules = append(res.Rules, RuleInfo{
			ID:       depRuleID,
			Desc:     depDirDesc(pol.DepDir),
			Severity: sevOf[depRuleID],
		})
	}
	if sandboxBox(pol).on() {
		res.Rules = append(res.Rules, RuleInfo{
			ID:       sandboxProviderRuleID,
			Desc:     sandboxProviderDesc(sandboxBox(pol)),
			Severity: sevOf[sandboxProviderRuleID],
		})
	}
	if executionBox(pol).on() {
		res.Rules = append(res.Rules, RuleInfo{
			ID:       executionPathRuleID,
			Desc:     executionPathsDesc(executionBox(pol)),
			Severity: sevOf[executionPathRuleID],
		})
	}
	if egressBox(pol).on() {
		res.Rules = append(res.Rules, RuleInfo{
			ID:       egressPathRuleID,
			Desc:     egressPathsDesc(egressBox(pol)),
			Severity: sevOf[egressPathRuleID],
		})
	}
	res.Rules = append(res.Rules, tenantGateRuleInfos(pol.TenantGate)...)
	// 未受管逃逸门禁恒开（无 policy 段）：它判的是本仓 API 的既定事实
	// （PTC 缺判定环即拒），不是各仓可自选的策略。
	res.Rules = append(res.Rules, RuleInfo{
		ID:       unmanagedEscapeRuleID,
		Desc:     "生产代码不得显式选择未受管执行（ptc 的 AllowUnmanaged 逃逸口）",
		Severity: sevOf[unmanagedEscapeRuleID],
	})
	res.Rules = append(res.Rules, RuleInfo{
		ID:       selfCheckRuleID,
		Desc:     "policy 自身一致性：规则须有执行体、id 唯一、阈值判定面须覆盖全部登记包、例外须写 until 与拆除条件",
		Severity: sevOf[selfCheckRuleID],
	})
	// 接缝契约门禁（AST 类，不在 rules 段——rules 段是正则表，判不了
	// 「这个声明是不是一个接缝声明」）。
	res.Rules = append(res.Rules, seamRuleInfo(pol))
}

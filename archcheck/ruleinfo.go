// ruleinfo.go —— 级别表与报告里的规则清单。
//
// 单独成文件是为了让 archcheck.go 保持在结构阈值之内（max_file_lines: 400）：
// Run() 已经很长了，而级别表与清单是**装配**不是**判定**——继续塞进主线
// 只会让读者在判据里迷路。
package archcheck

// severityMap 汇总各条判定的级别。
//
// 缺省 error：悬空的 warn 等于没有规则（它照常出现在报告里，却不阻断）。
func severityMap(pol *Policy) map[string]string {
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
	if sbox := sandboxProvidersOf(pol); sbox.on() {
		sevOf[sandboxProviderRuleID] = sevOrError(sbox.Severity)
	}
	if execPaths := executionPathsOf(pol); execPaths.on() {
		sevOf[executionPathRuleID] = sevOrError(execPaths.Severity)
	}
	if egPaths := egressPathsOf(pol); egPaths.on() {
		sevOf[egressPathRuleID] = sevOrError(egPaths.Severity)
	}
	if pol.WordTables != nil && pol.WordTables.on() {
		sev := sevOrError(pol.WordTables.Severity)
		sevOf[wordTableEnumRuleID] = sev
		sevOf[wordTableHandCopyRuleID] = sev
		sevOf[wordTableFamilyRuleID] = sev
		sevOf[wordTableCollisionRuleID] = sev
		sevOf[wordTablePredicateRuleID] = sev
		sevOf[wordTableNormalizerRuleID] = sev
	}
	tenantGateSeverities(pol.TenantGate, sevOf)
	seamSeverities(pol, sevOf)
	// 未受管逃逸门禁恒为 error：它是 PTC 安全默认（缺判定环即拒）的配套护栏——
	// 逃逸口若能在生产代码里随手写，它就从「显式声明」退化成「默认路径」。
	sevOf[unmanagedEscapeRuleID] = "error"
	// 自校验恒为 error：policy 结构有问题时，门禁必须响亮失败，
	// 而不是「其他规则都绿就当通过」——那正是它要防的那种静默失效。
	sevOf[selfCheckRuleID] = "error"
	return sevOf
}

// ruleInfos 报告用的规则清单。
//
// 顺序固定（rules 段 → 门包 → 依赖方向 → 各段门禁 → policy 自校验）：
// 门禁的输出也是契约，两次运行必须给出一致的顺序。
func ruleInfos(pol *Policy, sevOf map[string]string) []RuleInfo {
	var out []RuleInfo
	for _, r := range pol.Rules {
		out = append(out, RuleInfo{ID: r.ID, Desc: r.Desc, Severity: sevOf[r.ID]})
	}
	if len(pol.Facades) > 0 {
		out = append(out, RuleInfo{
			ID:       facadeRuleID,
			Desc:     "门面包只允许类型别名/常量别名/单条上游转发，实现归上游仓",
			Severity: sevOf[facadeRuleID],
		})
	}
	if pol.DepDir.on() {
		out = append(out, RuleInfo{
			ID:       depRuleID,
			Desc:     depDirDesc(pol.DepDir),
			Severity: sevOf[depRuleID],
		})
	}
	if sbox := sandboxProvidersOf(pol); sbox.on() {
		out = append(out, RuleInfo{
			ID:       sandboxProviderRuleID,
			Desc:     sandboxProviderDesc(sbox),
			Severity: sevOf[sandboxProviderRuleID],
		})
	}
	if execPaths := executionPathsOf(pol); execPaths.on() {
		out = append(out, RuleInfo{
			ID:       executionPathRuleID,
			Desc:     executionPathsDesc(execPaths),
			Severity: sevOf[executionPathRuleID],
		})
	}
	if egPaths := egressPathsOf(pol); egPaths.on() {
		out = append(out, RuleInfo{
			ID:       egressPathRuleID,
			Desc:     egressPathsDesc(egPaths),
			Severity: sevOf[egressPathRuleID],
		})
	}
	out = append(out, tenantGateRuleInfos(pol.TenantGate)...)
	out = append(out, seamRuleInfos(pol)...)
	if pol.WordTables != nil && pol.WordTables.on() {
		out = append(out, RuleInfo{
			ID:       wordTableEnumRuleID,
			Desc:     "登记的判定词表必须有导出枚举出口，且该出口读的是同一张表",
			Severity: sevOf[wordTableEnumRuleID],
		})
		out = append(out, RuleInfo{
			ID:       wordTableHandCopyRuleID,
			Desc:     "判定词表的取值必须被引用：别的包不得手抄（须自有常量或引用主人常量）",
			Severity: sevOf[wordTableHandCopyRuleID],
		})
		out = append(out, RuleInfo{
			ID:       wordTableFamilyRuleID,
			Desc:     "登记的词表必须覆盖它那一族常量的全部取值（常量加了、表没加当天即红）",
			Severity: sevOf[wordTableFamilyRuleID],
		})
		out = append(out, RuleInfo{
			ID:       wordTableCollisionRuleID,
			Desc:     "两张判定词表有同形取值时必须显式声明关系（same=同一概念 / distinct=不同概念）",
			Severity: sevOf[wordTableCollisionRuleID],
		})
		out = append(out, RuleInfo{
			ID:       wordTablePredicateRuleID,
			Desc:     "登记的判定谓词必须存在且读登记的那张表（判定与词表不得分叉）",
			Severity: sevOf[wordTablePredicateRuleID],
		})
		out = append(out, RuleInfo{
			ID:       wordTableNormalizerRuleID,
			Desc:     "输入归一词表必须有导出的归一出口，且它读登记的那张表",
			Severity: sevOf[wordTableNormalizerRuleID],
		})
	}
	// 未受管逃逸门禁恒开（无 policy 段）：它判的是本仓 API 的既定事实
	// （PTC 缺判定环即拒），不是各仓可自选的策略。
	out = append(out, RuleInfo{
		ID:       unmanagedEscapeRuleID,
		Desc:     "生产代码不得显式选择未受管执行（ptc 的 AllowUnmanaged 逃逸口）",
		Severity: sevOf[unmanagedEscapeRuleID],
	})
	out = append(out, RuleInfo{
		ID:       selfCheckRuleID,
		Desc:     "policy 自身一致性：规则须有执行体、id 唯一、阈值判定面须覆盖全部登记包、例外须写 until 与拆除条件",
		Severity: sevOf[selfCheckRuleID],
	})
	return out
}

// sandboxProvidersOf 取档位登记段（段缺省即开启，故缺省给零值）。
func sandboxProvidersOf(pol *Policy) SandboxProviders {
	if pol.SandboxProviders == nil {
		return SandboxProviders{}
	}
	return *pol.SandboxProviders
}

// executionPathsOf 取执行路径段（段缺省即开启，故缺省给零值）。
func executionPathsOf(pol *Policy) ExecutionPaths {
	if pol.ExecutionPaths == nil {
		return ExecutionPaths{}
	}
	return *pol.ExecutionPaths
}

// egressPathsOf 取子进程出口段（段缺省即开启，故缺省给零值）。
func egressPathsOf(pol *Policy) EgressPaths {
	if pol.EgressPaths == nil {
		return EgressPaths{}
	}
	return *pol.EgressPaths
}

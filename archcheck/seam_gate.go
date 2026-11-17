// seam_gate.go —— 接缝契约门禁的判定（清单完备 + 契约成文）。
//
// # 三条判据
//
//   - A 完备性：扫描器产出的接缝集合 **==** policy 的 seams.registry 登记集合，
//     **双向**判：扫描到但没登记 ⇒ 红「未登记」；登记了但扫描不到 ⇒ 红「陈旧登记」。
//     缺一即红（issue #400 的验收标准）。
//   - B 成文率：每个接缝声明处的文档注释必须带齐三个契约 token
//     （no-reentry / no-blocking / panic-isolated），缺一条即红。
//   - C 自校验：seams 段必须存在、id 不得重复、格式合法（判在 seam_selfcheck.go，
//     与 policy 自校验同一层，恒开）。
//
// # 根本局限（写在这里，也写进 policy 的段头）
//
// 标记是**承诺**，不是事实：声明了 no-reentry 而代码仍在锁内调宿主回调，
// 这条门禁照样绿。它保证「契约被写下」，不保证「契约被兑现」。
// 兑现面靠契约用例（形态 C）与运行期保证。
//
// # 为什么成文率现在必然是红的
//
// 本轮只交付「门禁 + 扫描器 + 清单」，**不给全部接缝补标记行**——
// 标记由后续任务按包补齐。故这条规则当前预期报红，红点直指
// 「哪个接缝缺哪个 token」，正是下一批任务的补标记清单。

package archcheck

import (
	"fmt"
	"sort"
	"strings"
)

// SeamSummary 是接缝清单的统计：总数、按三类分布、按包分组的待补标记清单。
//
// 它是**补标记清单**：下一批任务照着它逐包补标记行即可，不必重新推导清单。
type SeamSummary struct {
	// Total 是接缝总数。
	Total int
	// ByKind 是按三类接缝的分布。
	ByKind map[SeamKind]int
	// Packages 是按包分组的待补标记清单（只含缺标记的包）。
	Packages []SeamPackagePlan
}

// SeamPackagePlan 是一个包的接缝待补标记清单。
type SeamPackagePlan struct {
	// Package 是包的 import 路径。
	Package string
	// Total 是该包的接缝数。
	Total int
	// Pending 是缺契约标记的接缝 id。
	Pending []string
}

// seamContractViolations 判接缝契约门禁的 A（完备性）与 B（成文率）两条判据。
//
// 同时返回接缝清单统计（报告里的补标记清单）。
func seamContractViolations(root string, pol *Policy) ([]Violation, *SeamSummary, error) {
	seams, err := ScanSeams(root)
	if err != nil {
		return nil, nil, err
	}
	registry := map[string]bool{}
	for _, r := range pol.Seams.Registry {
		registry[r.ID] = true
	}
	scanned := map[string]bool{}
	for _, s := range seams {
		scanned[s.ID] = true
	}
	policyPath := PolicyPath(root, pol)
	var out []Violation

	// ── A 完备性：扫描到但没登记 ──
	for _, s := range seams {
		if registry[s.ID] {
			continue
		}
		out = append(out, Violation{
			Rule: seamRuleID,
			Path: s.File,
			Line: s.Line,
			Text: fmt.Sprintf("接缝 %s（%s）未登记进 policy 的 seams.registry 段："+
				"清单必须与代码事实逐条相等（缺一即红）——新增加接缝却没登记，"+
				"它对契约门禁就是隐形的", s.ID, s.Kind),
			Kind: "seam_unregistered",
		})
	}
	// ── A 完备性：登记了但扫描不到（陈旧登记）──
	for _, r := range pol.Seams.Registry {
		if scanned[r.ID] {
			continue
		}
		out = append(out, Violation{
			Rule: seamRuleID,
			Path: policyPath,
			Text: fmt.Sprintf("seams.registry 登记的接缝 %s 扫描不到（陈旧登记）："+
				"接缝被改名 / 迁走 / 删除之后必须同步改登记，否则清单变成死条文", r.ID),
			Kind: "seam_stale_registration",
		})
	}

	// ── B 成文率 ──
	summary := buildSeamSummary(seams)
	for _, s := range seams {
		missing := missingSeamTokens(s)
		if len(missing) == 0 {
			continue
		}
		out = append(out, Violation{
			Rule: seamRuleID,
			Path: s.File,
			Line: s.Line,
			Text: fmt.Sprintf("接缝 %s（%s）缺少契约标记 token：%s"+
				"（声明处的文档注释里必须有一行 %s）",
				s.ID, s.Kind, strings.Join(missing, "、"), seamMarkerTemplate()),
			Kind: "seam_contract_missing",
		})
	}
	return out, summary, nil
}

// seamMarkerTemplate 是契约标记的规范写法（写进报错里，让人照着改）。
func seamMarkerTemplate() string {
	return "// @seam: no-reentry no-blocking panic-isolated"
}

// missingSeamTokens 报告接缝声明处缺哪几个契约 token。
//
// 标记行整体缺失 ⇒ 三个 token 全缺（不是「一个都不缺」也不是「只缺一个」）。
func missingSeamTokens(s Seam) []string {
	toks := seamMarkerTokens(s.Doc)
	var missing []string
	for _, t := range seamRequiredTokens {
		if !toks[t] {
			missing = append(missing, t)
		}
	}
	return missing
}

// seamMarkerTokens 从文档注释里解析 @seam 标记声明的 token 集合。
//
// 口径：注释里出现 @seam: 即为标记行，其后按空白切 token。
// 顺序不校验（规范写法是 no-reentry no-blocking panic-isolated），
// 但三个 token 必须齐全——顺序可变、缺一即红。
func seamMarkerTokens(doc string) map[string]bool {
	toks := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		i := strings.Index(line, "@seam:")
		if i < 0 {
			continue
		}
		for _, t := range strings.Fields(line[i+len("@seam:"):]) {
			toks[t] = true
		}
	}
	return toks
}

// buildSeamSummary 统计接缝清单并生成按包分组的待补标记清单。
func buildSeamSummary(seams []Seam) *SeamSummary {
	s := &SeamSummary{ByKind: map[SeamKind]int{}}
	byPkg := map[string]*SeamPackagePlan{}
	var pkgOrder []string
	for _, sm := range seams {
		s.Total++
		s.ByKind[sm.Kind]++
		p, ok := byPkg[sm.Package]
		if !ok {
			p = &SeamPackagePlan{Package: sm.Package}
			byPkg[sm.Package] = p
			pkgOrder = append(pkgOrder, sm.Package)
		}
		p.Total++
		if len(missingSeamTokens(sm)) > 0 {
			p.Pending = append(p.Pending, sm.ID)
		}
	}
	sort.Strings(pkgOrder)
	for _, name := range pkgOrder {
		p := byPkg[name]
		if len(p.Pending) == 0 {
			continue // 该包已全部写齐，不进补标记清单
		}
		sort.Strings(p.Pending)
		s.Packages = append(s.Packages, *p)
	}
	return s
}

// seamRuleInfo 是这条规则在报告清单里的元信息。
func seamRuleInfo(pol *Policy) RuleInfo {
	desc := "接缝声明必须写齐三条契约（不得重入 / 不得阻塞 / panic 隔离），且接缝清单必须与代码事实逐条相等"
	if pol.Seams != nil && pol.Seams.Desc != "" {
		desc = pol.Seams.Desc
	}
	return RuleInfo{ID: seamRuleID, Desc: desc, Severity: seamSeverity(pol)}
}

// seamSeverity 取这条规则的级别（缺省 error：悬空的 warn 等于没有规则）。
func seamSeverity(pol *Policy) string {
	if pol.Seams == nil {
		return "error"
	}
	return sevOrError(pol.Seams.Severity)
}

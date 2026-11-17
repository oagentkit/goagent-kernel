// seamrules.go —— 宿主接缝契约门禁的声明面与三门聚合（issue #369/#370）。
//
// 三种机判形态与取舍、各自漏判形态，见 docs/SEAM-CONTRACT.md 与
// archcheck/seamcontract.go / seamlock.go / seampanic.go 的文件头。
//
// 实现分成四个文件，按判定形态切开（也避开 400 行结构阈值）：
//
//	seamrules.go     声明面类型、默认值、判定面枚举、三门聚合
//	seamcontract.go  形态 B：契约标记完备性（阻断）
//	seamlock.go      形态 A：锁内调用只读扫描（warn，不阻断）
//	seampanic.go     形态 C：panic 外溢检测（阻断）
package archcheck

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SeamRules 是 seams 段的声明面。
type SeamRules struct {
	// Enabled 段级开关（缺省启用；显式 false 才关）。
	Enabled *bool `yaml:"enabled"`
	// Severity 契约标记门禁的级别（缺省 error；形态 B 零假红，应阻断）。
	Severity string `yaml:"severity"`
	// Marker 可机读标记的前缀（缺省 "@seam:"）。
	Marker string `yaml:"marker"`
	// Required 每条接缝标记必须带齐的约束词（缺省 no-reentry + no-blocking）。
	Required []string `yaml:"required"`
	// PanicDispositions panic 处置的取值域：恰出现一个。panic-isolated =
	// 内核隔离；panic-caller = 内核不 recover、由调用方兜（显式第三态）。
	PanicDispositions []string `yaml:"panic_dispositions"`
	// MinDeclarations 最少应枚举到的接缝声明数（缺省 0 = 不判）。本仓设 1：
	// 判定面若因重构/改包而枚举到 0 个，门禁必须响亮失败而不是静默变绿。
	MinDeclarations int `yaml:"min_declarations"`
	// LockScan 形态 A 只读扫描的配置。
	LockScan *SeamLockScan `yaml:"lock_scan"`
	// PanicScan 形态 C panic 外溢检测的配置。
	PanicScan *SeamPanicScan `yaml:"panic_scan"`
}

// SeamLockScan 形态 A 的配置。
type SeamLockScan struct {
	Enabled *bool `yaml:"enabled"` // 缺省启用
	// Severity 只读扫描的级别（缺省 warn；形态 A 不宜阻断，见文件头）。
	Severity string `yaml:"severity"`
	// Accessors 视为「时钟访问器」的方法名（缺省 now/nowLocked/nowAt/clock）。
	Accessors []string `yaml:"accessors"`
}

// SeamPanicScan 形态 C 的配置。
type SeamPanicScan struct {
	Enabled  *bool  `yaml:"enabled"` // 缺省启用
	Severity string `yaml:"severity"`
	// GuardedCalls 必须 panic 隔离的宿主调用名（方法/函数名精确匹配）。
	// 清单是增量的：未登记的接缝由形态 B 的标记完备性兜底。
	GuardedCalls []string `yaml:"guarded_calls"`
	// Wrappers 视为「panic 隔离包装器」的调用（缺省 subhub.Do / subhub.Invoke
	// / async.GoSafe / async.RunSafe 等）。
	Wrappers []string `yaml:"wrappers"`
}

// 接缝门禁的三条规则 ID（内置规则，不在 policy 的 rules 段里）。
const (
	seamContractRuleID    = "harness-seam-contract"
	seamLockHeldRuleID    = "harness-seam-lock-held"
	seamPanicEscapeRuleID = "harness-seam-panic-escape"
)

// on 报告 seams 段是否启用（段存在即启用，显式 false 才关）。
func (s SeamRules) on() bool { return s.Enabled == nil || *s.Enabled }

// marker 标记前缀。
func (s SeamRules) marker() string {
	if s.Marker == "" {
		return "@seam:"
	}
	return s.Marker
}

// requiredTokens 必须写齐的约束词。
func (s SeamRules) requiredTokens() []string {
	if len(s.Required) == 0 {
		return []string{"no-reentry", "no-blocking"}
	}
	return s.Required
}

// panicTokens panic 处置的取值域。
func (s SeamRules) panicTokens() []string {
	if len(s.PanicDispositions) == 0 {
		return []string{"panic-isolated", "panic-caller"}
	}
	return s.PanicDispositions
}

// lockScan 形态 A 的有效配置。
func (s SeamRules) lockScan() SeamLockScan {
	ls := SeamLockScan{}
	if s.LockScan != nil {
		ls = *s.LockScan
	}
	if ls.Accessors == nil {
		ls.Accessors = []string{"now", "nowLocked", "nowAt", "clock"}
	}
	return ls
}

// panicScan 形态 C 的有效配置。
func (s SeamRules) panicScan() SeamPanicScan {
	ps := SeamPanicScan{}
	if s.PanicScan != nil {
		ps = *s.PanicScan
	}
	if ps.Wrappers == nil {
		ps.Wrappers = []string{
			"subhub.Do", "subhub.Invoke",
			"async.GoSafe", "async.RunSafe",
			"async.GoSafeWithErr", "async.GoSafeWithContext",
		}
	}
	return ps
}

// subRuleEnabled 子开关（缺省启用）。
func subRuleEnabled(b *bool) bool { return b == nil || *b }

// sevOrWarn 缺省级别 warn（形态 A 只读扫描用）。
func sevOrWarn(s string) string {
	if s == "" {
		return "warn"
	}
	return s
}

// seamsOn policy 是否接入接缝门禁。
func seamsOn(pol *Policy) bool { return pol != nil && pol.Seams != nil && pol.Seams.on() }

// seamScanFiles 生产代码判定面：*.go，排除测试文件、cmd/ 与 examples/
// （与 issue #369 的统计口径一致：命令装配层与示例不是内核接缝面的承诺）。
func seamScanFiles(root string) ([]string, error) {
	paths, err := iterFiles(root, []string{"*.go"}, true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "examples/") {
			continue
		}
		out = append(out, path)
	}
	return out, nil
}

// seamViolations 跑接缝门禁的三个形态。
func seamViolations(root string, pol *Policy) ([]Violation, error) {
	if !seamsOn(pol) {
		return nil, nil
	}
	seams := *pol.Seams
	vs, err := seamContractViolations(root, seams, PolicyPath(root, pol))
	if err != nil {
		return nil, err
	}
	if ls := seams.lockScan(); subRuleEnabled(ls.Enabled) {
		more, err := seamLockHeldViolations(root, ls)
		if err != nil {
			return nil, err
		}
		vs = append(vs, more...)
	}
	if ps := seams.panicScan(); subRuleEnabled(ps.Enabled) {
		more, err := seamPanicViolations(root, ps)
		if err != nil {
			return nil, err
		}
		vs = append(vs, more...)
	}
	return vs, nil
}

// appendSeamViolations 把三个形态的违反并入结果（供 RunWithOptions 单行调用）。
func appendSeamViolations(res *Result, root string, pol *Policy) error {
	vs, err := seamViolations(root, pol)
	if err != nil {
		return err
	}
	res.Violations = append(res.Violations, vs...)
	return nil
}

// seamSeverities 把三个子门禁的级别写进 severity 表。
func seamSeverities(pol *Policy, sevOf map[string]string) {
	if !seamsOn(pol) {
		return
	}
	sevOf[seamContractRuleID] = sevOrError(pol.Seams.Severity)
	if ls := pol.Seams.lockScan(); subRuleEnabled(ls.Enabled) {
		sevOf[seamLockHeldRuleID] = sevOrWarn(ls.Severity)
	}
	if ps := pol.Seams.panicScan(); subRuleEnabled(ps.Enabled) {
		sevOf[seamPanicEscapeRuleID] = sevOrError(ps.Severity)
	}
}

// seamRuleInfos 报告里的三条接缝规则。
func seamRuleInfos(pol *Policy) []RuleInfo {
	if !seamsOn(pol) {
		return nil
	}
	seams := *pol.Seams
	out := []RuleInfo{{
		ID:       seamContractRuleID,
		Desc:     "导出时钟接缝声明处必须写齐 no-reentry / no-blocking / panic 处置标记（形态 B）",
		Severity: sevOrError(seams.Severity),
	}}
	if ls := seams.lockScan(); subRuleEnabled(ls.Enabled) {
		out = append(out, RuleInfo{
			ID:       seamLockHeldRuleID,
			Desc:     "只读扫描：时钟访问器疑似在互斥锁区内被调用（形态 A，漏判形态见 docs/SEAM-CONTRACT.md）",
			Severity: sevOrWarn(ls.Severity),
		})
	}
	if ps := seams.panicScan(); subRuleEnabled(ps.Enabled) {
		out = append(out, RuleInfo{
			ID:       seamPanicEscapeRuleID,
			Desc:     "登记的宿主接缝调用必须 panic 隔离（subhub.Do / async.GoSafe / defer recover，形态 C）",
			Severity: sevOrError(ps.Severity),
		})
	}
	return out
}

// seamRel 相对路径（斜杠形态），供各形态报告用。
func seamRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("接缝门禁计算相对路径 %s 失败: %w", path, err)
	}
	return filepath.ToSlash(rel), nil
}

// checkSeamSelfPolicy 校验 policy seams 段的判据面不为空（issue #369/#370）。
//
// 与 rules 段「有执行体但 scope 为空」同一条纪律：接入判据的声明面若没有可判
// 的条目，它仍是 severity: error、仍计入规则数，却一条都不判。这类失效只有
// 门禁自己老实报出来才看得见。从 selfcheck.go 拆出（400 行阈值），划界按用途。
func checkSeamSelfPolicy(pol *Policy, policyPath string, add func(file string, line int, format string, args ...any)) {
	if pol.Seams == nil || !pol.Seams.on() {
		return
	}
	if ls := pol.Seams.lockScan(); subRuleEnabled(ls.Enabled) && len(ls.Accessors) == 0 {
		add(policyPath, 0, "seams.lock_scan 已启用但 accessors 为空：锁内调用扫描一条都不判")
	}
	if ps := pol.Seams.panicScan(); subRuleEnabled(ps.Enabled) && len(ps.GuardedCalls) == 0 {
		add(policyPath, 0, "seams.panic_scan 已启用但 guarded_calls 为空：panic 外溢检测一条都不判")
	}
	if pol.Seams.MinDeclarations < 0 {
		add(policyPath, 0, "seams.min_declarations 不能为负")
	}
}

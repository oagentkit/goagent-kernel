// tenantgate.go —— 租户过滤漏拼门（issue #172）：隔离证据判据 + 登记面漂移检测。
// 租户表登记面三条判据在 tenantmanifest.go（issue #177 补齐到 6 条，与
// bianque tenantcheck 同口径）。
//
// # 为什么需要这条门禁
//
// 多租户隔离若依赖「人记得在查询里拼过滤列」，忘拼一次就是跨租户越权，且
// 没有任何机器能拦住。判据落在**服务层**（repo 层按 id 操作天然无过滤会假红，
// handler 层有授权下沉会假红；服务层才是租户读面的汇聚点）：
//
//  1. tenant-surface-missing-evidence：带授权上下文形参 + 返回租户域类型的
//     函数必须有隔离判定证据——调用登记的共享判定面 / 查询条件含过滤列 /
//     显式 `// tenant:iso <理由>`。三者皆无 = 漏拼即红。
//  2. viewer-dropped：带授权上下文形参却完全不用 = 授权上下文被丢弃 = 红。
//  3. shared-surface-missing：登记在册的共享判定面在代码里不存在 = 清单漂移
//     = 红（一条被静默放宽的登记等于没有登记）。
//
// 声明 tenant_tables_manifest 后再加**租户表登记面**三条（判据落点见
// tenantmanifest.go 头注）：tenant-policy-missing（模型面表缺登记）、
// tenant-policy-drift（清单登记的表模型面已不存在）、tenant-table-name-missing
// （租户域模型没有可解析的表名方法）。不声明则只跑前三条。
//
// # 形状归 policy，执行体通用
//
// 哪些类型算授权上下文、哪些字段算租户域、过滤列叫什么、共享判定面在哪，
// 全部由 policy 的 tenant_gate 段声明（TenantGate）。本文件不出现任何宿主
// 字面——换一个多租户仓只需换 policy，执行体一字不改。
//
// # 租户类型清单由代码推导
//
// 不另立类型清单：推导面（TenantTypeRoots，缺省 = Roots）内任何含
// TenantFields 字段名的 struct 都是租户域类型。新增模型自动入面。
//
// # 判不了什么（写明边界）
//
// 本门禁判的是**静态证据存在性**，不是「过滤是否真的生效」：
//   - 调用了共享判定面但没用返回值、Where 里写了过滤列但条件被注释掉，
//     这类语义缺陷判不了（需要类型/流分析或运行期判据）；
//   - 同包重名类型按包名索引，两个不同包同名时可能误判（与 bianque 原判据
//     同口径的已知边界）；
//   - 登记面清单与宿主 RLS 注册表的一致性靠宿主自己的 dump/校验测试保证
//     （注册表是 Go 符号，执行体调不到，见 tenantmanifest.go 头注）。
package archcheck

import (
	"cmp"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// 租户门的三条规则 id（AST 判定，不在 rules 段的正则表里）。
const (
	tenantSurfaceMissingRuleID = "tenant-surface-missing-evidence"
	tenantViewerDroppedRuleID  = "viewer-dropped"
	tenantSurfaceDriftRuleID   = "shared-surface-missing"
)

// tenantIsoTag 是「隔离在别处完成」的显式声明标签（格式 `// tenant:iso <理由>`，
// 理由必须非空——裸标签等于没声明：谁都不知道隔离在哪一层）。
const tenantIsoTag = "tenant:iso"

// tenantGateSeverities 把租户门规则的 severity 写进分流表（段未启用时不动）。
func tenantGateSeverities(g *TenantGate, sevOf map[string]string) {
	if g == nil || !g.on() {
		return
	}
	sev := sevOrError(g.Severity)
	sevOf[tenantSurfaceMissingRuleID] = sev
	sevOf[tenantViewerDroppedRuleID] = sev
	sevOf[tenantSurfaceDriftRuleID] = sev
	tenantManifestSeverities(g, sevOf) // 登记面三条（声明 manifest 才登记）
}

// tenantGateRuleInfos 报告用的规则元信息（段未启用时为空）。
func tenantGateRuleInfos(g *TenantGate) []RuleInfo {
	if g == nil || !g.on() {
		return nil
	}
	sev := sevOrError(g.Severity)
	infos := []RuleInfo{
		{ID: tenantSurfaceMissingRuleID,
			Desc:     "带授权上下文形参且返回租户域类型的函数必须有隔离判定证据（共享判定面 / 过滤列 / tenant:iso 声明）",
			Severity: sev},
		{ID: tenantViewerDroppedRuleID,
			Desc:     "带授权上下文形参却完全不用（授权上下文被丢弃）",
			Severity: sev},
		{ID: tenantSurfaceDriftRuleID,
			Desc:     tenantGateDesc(*g),
			Severity: sev},
	}
	return append(infos, tenantManifestRuleInfos(g)...)
}

// tenantGateDesc 报告用规则说明（policy 的 desc 优先）。
func tenantGateDesc(g TenantGate) string {
	if g.Desc != "" {
		return g.Desc
	}
	return "登记的共享隔离判定面必须在代码里存在（清单漂移 = 登记项失效）"
}

// tenantGateViolations 执行三条判据。
//
// 形状缺失（roots/auth 类型/租户字段为空）不是本函数报的——那是 policy 声明
// 缺陷，由自校验（tenantgate_selfcheck.go）报红；这里直接判定空面，避免把
// 同一个问题说两遍。
func tenantGateViolations(root string, gate TenantGate) ([]Violation, error) {
	if len(gate.Roots) == 0 || len(gate.AuthContextTypes) == 0 || len(gate.TenantFields) == 0 {
		return nil, nil
	}
	auths := parseTenantTypeNames(gate.AuthContextTypes)
	fields := map[string]bool{}
	for _, f := range gate.TenantFields {
		if f = strings.TrimSpace(f); f != "" {
			fields[f] = true
		}
	}
	typeRoots := gate.TenantTypeRoots
	if len(typeRoots) == 0 {
		typeRoots = gate.Roots
	}

	cache := tenantFileCache{}
	var typeFiles []tenantFile
	for _, r := range typeRoots {
		files, err := cache.files(root, r)
		if err != nil {
			return nil, err
		}
		typeFiles = append(typeFiles, files...)
	}
	// 登记面（判据 4-6）的推导面可单独收窄（issue：服务层载体 DTO 带租户字段
	// 但不映射物理表，跟着读面扫会被误报缺表名方法）；缺省与读面同根。
	tableRoots := gate.TenantTableTypeRoots
	if len(tableRoots) == 0 {
		tableRoots = typeRoots
	}
	var tableFiles []tenantFile
	for _, r := range tableRoots {
		files, err := cache.files(root, r)
		if err != nil {
			return nil, err
		}
		tableFiles = append(tableFiles, files...)
	}
	tenants := tenantTypeIndex(typeFiles, fields)

	surfaceNames := map[string]bool{}
	for _, s := range gate.SharedSurfaces {
		surfaceNames[s.Name] = true
	}

	var out []Violation
	// 判据 3：登记面存在性核对（清单漂移 = 登记项失效）。
	for _, s := range gate.SharedSurfaces {
		if s.Dir == "" || s.Name == "" {
			continue // 形状缺陷由自校验报
		}
		files, err := cache.files(root, s.Dir)
		if err != nil {
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(s.Dir))); statErr != nil {
				out = append(out, Violation{
					Rule: tenantSurfaceDriftRuleID, Path: filepath.ToSlash(s.Dir), Line: 0,
					Text: fmt.Sprintf("登记的共享判定面 %s（%s）所在目录 %s 不存在——清单与代码漂移；"+
						"登记项失效等于没有登记，改名/迁走后须同步改 policy 的 tenant_gate.shared_surfaces",
						s.Name, s.Note, s.Dir),
					Kind: "surface_drift",
				})
				continue
			}
			return nil, err
		}
		found := false
		for _, tf := range files {
			for _, decl := range tf.file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == s.Name {
					found = true
				}
			}
		}
		if !found {
			out = append(out, Violation{
				Rule: tenantSurfaceDriftRuleID, Path: filepath.ToSlash(s.Dir), Line: 0,
				Text: fmt.Sprintf("登记的共享判定面 %s（%s）在 %s 下不存在——清单与代码漂移；"+
					"登记项失效等于没有登记，该面被改名/迁走时须同步改 policy 或撤下本行",
					s.Name, s.Note, s.Dir),
				Kind: "surface_drift",
			})
		}
	}

	// 判据 1/2：服务层函数级判定。
	for _, r := range gate.Roots {
		files, err := cache.files(root, r)
		if err != nil {
			return nil, err
		}
		for _, tf := range files {
			for _, decl := range tf.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				name, hasAuth := tenantAuthParam(fn, auths, tf)
				if !hasAuth {
					continue
				}
				if !tenantUsesIdent(fn.Body, name) {
					out = append(out, Violation{
						Rule: tenantViewerDroppedRuleID, Path: tf.rel,
						Line: tf.fset.Position(fn.Pos()).Line,
						Text: fmt.Sprintf("函数 %s 收了授权上下文形参 %q 却完全没用——授权上下文被丢弃；"+
							"要么用它做隔离判定（共享判定面 / 过滤列），要么这个形参不该存在",
							fn.Name.Name, name),
						Kind: "viewer_dropped",
					})
					// 不 continue：两条判据并列成立时都要报，只报前者会遮蔽后者。
				}
				tenantType, isTenant := tenantReturn(fn, tf, tenants)
				if !isTenant {
					continue
				}
				if _, ok := tenantEvidence(fn, surfaceNames, gate.FilterColumns); ok {
					continue
				}
				if _, ok := tenantIsoDecl(fn, tf.file); ok {
					continue
				}
				out = append(out, Violation{
					Rule: tenantSurfaceMissingRuleID, Path: tf.rel,
					Line: tf.fset.Position(fn.Pos()).Line,
					Text: fmt.Sprintf("函数 %s 返回租户域数据（%s）却无隔离判定证据：未调用登记的共享判定面、"+
						"查询条件未含过滤列、也未声明 `// %s <理由>`——若隔离确在别处，请写明隔离点；"+
						"形状声明见 policy 的 tenant_gate 段", fn.Name.Name, tenantType, tenantIsoTag),
					Kind: "isolation_evidence",
				})
			}
		}
	}

	// 判据 4-6：租户表登记面（声明 tenant_tables_manifest 才启用，issue #177）。
	if gate.TenantTablesManifest != "" {
		mv, err := tenantManifestViolations(root, gate, tableFiles, fields)
		if err != nil {
			return nil, err
		}
		out = append(out, mv...)
	}

	slices.SortFunc(out, func(a, b Violation) int {
		if a.Path != b.Path {
			return cmp.Compare(a.Path, b.Path)
		}
		if a.Line != b.Line {
			return cmp.Compare(a.Line, b.Line)
		}
		return cmp.Compare(a.Rule, b.Rule)
	})
	return out, nil
}

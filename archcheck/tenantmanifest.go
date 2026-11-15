// tenantmanifest.go —— 租户表登记面判据（issue #177，bianque tenantcheck 的
// `tenant-policy-missing` / `tenant-policy-drift` / `tenant-table-name-missing`
// 三条）：模型面推导出的租户表集合与 policy 声明的清单做**双向**核对。
//
// # 为什么是清单不是注册表
//
// 宿主持有的 RLS 注册表是 Go 符号，执行体调不到；tenant_tables_manifest 是
// 宿主自己 dump 的产物（或由一条校验测试保证与注册表一致）。语义与
// shared_surfaces 的「存在性核对」同族：登记面与代码面谁走了谁都不行——
// 单向核对漏掉的正是漂移的另一半。
//
// # 物理表名怎么解析
//
// 每个含租户字段的 struct 必须有可从 AST 识别的表名方法（缺省 TableName，
// policy 的 table_name_method 可改）：方法体单条 `return "字面量"` 才可解析
// ——动态拼接的表名执行体读不出，登记面核对不了这张表，按 deny-by-default
// 报 tenant-table-name-missing（与 bianque 原判据同口径：没有可解析表名的
// 模型本身就是红）。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// 租户表登记面的三条规则 id（与 bianque tenantcheck 同名，AST 判定）。
const (
	tenantPolicyMissingRuleID    = "tenant-policy-missing"
	tenantPolicyDriftRuleID      = "tenant-policy-drift"
	tenantTableNameMissingRuleID = "tenant-table-name-missing"
)

// tenantDefaultTableMethod 物理表名方法的缺省名（GORM 惯例）。
const tenantDefaultTableMethod = "TableName"

// tenantManifestSeverities 把登记面三条规则的 severity 写进分流表
// （未声明 manifest 时不动——三条判据只在登记面声明后启用）。
func tenantManifestSeverities(g *TenantGate, sevOf map[string]string) {
	if g == nil || !g.on() || g.TenantTablesManifest == "" {
		return
	}
	sev := sevOrError(g.Severity)
	sevOf[tenantPolicyMissingRuleID] = sev
	sevOf[tenantPolicyDriftRuleID] = sev
	sevOf[tenantTableNameMissingRuleID] = sev
}

// tenantManifestRuleInfos 报告用的登记面三条规则元信息（未声明 manifest 为空）。
func tenantManifestRuleInfos(g *TenantGate) []RuleInfo {
	if g == nil || !g.on() || g.TenantTablesManifest == "" {
		return nil
	}
	sev := sevOrError(g.Severity)
	return []RuleInfo{
		{ID: tenantPolicyMissingRuleID,
			Desc:     "含租户字段的模型映射出的物理表必须在 tenant_tables_manifest 登记（漏登记 = 隔离策略不覆盖该表）",
			Severity: sev},
		{ID: tenantPolicyDriftRuleID,
			Desc:     "tenant_tables_manifest 登记的表必须在模型面真实存在（登记漂移 = 空登记）",
			Severity: sev},
		{ID: tenantTableNameMissingRuleID,
			Desc:     "含租户字段的模型必须有可从 AST 识别的表名方法（缺省 TableName，table_name_method 可改）——解析不出物理表名，登记面核对不了",
			Severity: sev},
	}
}

// tenantStructRef 一个租户域类型的位置（struct 名 + 所在文件/行）。
type tenantStructRef struct {
	pkg  string
	name string
	file string
	line int
}

// tenantTableRef 一个租户域类型的**可解析**物理表名（表名 + 表名方法位置）。
type tenantTableRef struct {
	pkg   string
	strct string
	table string
	file  string
	line  int
}

// tenantManifestEntry 清单里的一行（表名 + 行号，漂移报错定位用）。
type tenantManifestEntry struct {
	table string
	line  int
}

// tenantManifestViolations 执行登记面三条判据：
//
//	每个租户域 struct 必须有可解析的表名方法（tenant-table-name-missing）；
//	模型面表集合 ⊆ 清单（tenant-policy-missing）；
//	清单 ⊆ 模型面表集合（tenant-policy-drift）。
func tenantManifestViolations(root string, gate TenantGate, typeFiles []tenantFile, fields map[string]bool) ([]Violation, error) {
	method := gate.TableNameMethod
	if strings.TrimSpace(method) == "" {
		method = tenantDefaultTableMethod
	}

	// 模型面：租户域 struct 位置 + 同包表名方法解析。
	structs := tenantStructRefs(typeFiles, fields)
	hasMethod := map[string]bool{} // "pkg.struct" → 表名方法已出现
	resolved := map[string]tenantTableRef{}
	for _, sr := range structs {
		hasMethod[sr.pkg+"."+sr.name] = false
	}
	var out []Violation
	for _, tf := range typeFiles {
		for _, decl := range tf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			recv, ok := tenantRecvTypeName(fn)
			if !ok || fn.Name.Name != method {
				continue
			}
			key := tf.pkg + "." + recv
			if _, known := hasMethod[key]; !known {
				continue // 非租户域类型的同名方法，不在判定面
			}
			hasMethod[key] = true
			table, resolvable := tenantLiteralReturn(fn)
			if !resolvable {
				out = append(out, Violation{
					Rule: tenantTableNameMissingRuleID, Path: tf.rel,
					Line: tf.fset.Position(fn.Pos()).Line,
					Text: fmt.Sprintf("租户域类型 %s 的 %s() 方法体没有可从 AST 解析的字符串字面量返回——"+
						"表名动态计算时执行体读不出，租户表登记面（%s）核对不了这张表；"+
						"方法应单条 return 表名字面量", recv, method, gate.TenantTablesManifest),
					Kind: "table_name_unresolvable",
				})
				continue
			}
			resolved[key] = tenantTableRef{pkg: tf.pkg, strct: recv, table: table,
				file: tf.rel, line: tf.fset.Position(fn.Pos()).Line}
		}
	}
	// 没有方法的租户域 struct：报红（解析不出 = 登记面核对不了）。
	for _, sr := range structs {
		if hasMethod[sr.pkg+"."+sr.name] {
			continue
		}
		out = append(out, Violation{
			Rule: tenantTableNameMissingRuleID, Path: sr.file, Line: sr.line,
			Text: fmt.Sprintf("租户域类型 %s.%s（含租户字段）没有可从 AST 识别的 %s() 方法——"+
				"物理表名解析不出，租户表登记面（%s）无从核对这张表；"+
				"按惯例实现单条 return 表名字面量的方法，或在 policy 的 tenant_gate.table_name_method 声明实际方法名",
				sr.pkg, sr.name, method, gate.TenantTablesManifest),
			Kind: "table_name_missing",
		})
	}

	// 清单面。
	entries, err := readTenantManifest(root, gate.TenantTablesManifest)
	if err != nil {
		return nil, err
	}

	// 双向核对（缺登记 / 登记漂移）。
	for _, tr := range resolved {
		if slices.ContainsFunc(entries, func(e tenantManifestEntry) bool { return e.table == tr.table }) {
			continue
		}
		out = append(out, Violation{
			Rule: tenantPolicyMissingRuleID, Path: tr.file, Line: tr.line,
			Text: fmt.Sprintf("租户表 %q 在登记面（%s）缺登记：模型面 %s.%s 映射到该表，"+
				"清单里没有它——隔离策略不会覆盖这张表；清单每行一个物理表名，登记漏了即越权面",
				tr.table, gate.TenantTablesManifest, tr.pkg, tr.strct),
			Kind: "table_policy_missing",
		})
	}
	for _, e := range entries {
		known := false
		for _, tr := range resolved {
			if tr.table == e.table {
				known = true
				break
			}
		}
		if known {
			continue
		}
		out = append(out, Violation{
			Rule: tenantPolicyDriftRuleID, Path: gate.TenantTablesManifest, Line: e.line,
			Text: fmt.Sprintf("登记面登记的租户表 %q 在模型面已不存在——登记漂移（模型改名/删除后清单未同步），"+
				"空的登记等于没有登记；请同步清单或撤下该行", e.table),
			Kind: "table_policy_drift",
		})
	}
	return out, nil
}

// tenantStructRefs 收集推导面内含租户字段的 struct 位置（同 tenantTypeIndex
// 的推导口径：struct 声明含任一租户字段名即租户域类型）。
func tenantStructRefs(files []tenantFile, fields map[string]bool) []tenantStructRef {
	var out []tenantStructRef
	for _, tf := range files {
		ast.Inspect(tf.file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				hit := false
				for _, nm := range fld.Names {
					if fields[nm.Name] {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
				out = append(out, tenantStructRef{
					pkg:  tf.pkg,
					name: ts.Name.Name,
					file: tf.rel,
					line: tf.fset.Position(ts.Pos()).Line,
				})
				return true
			}
			return true
		})
	}
	return out
}

// tenantRecvTypeName 返回方法接收者的基础类型名（*T 与 T 都算）。
func tenantRecvTypeName(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "", false
	}
	t := fn.Recv.List[0].Type
	if st, ok := t.(*ast.StarExpr); ok {
		t = st.X
	}
	id, ok := t.(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

// tenantLiteralReturn 方法体是否单条 return 字符串字面量（可解析的表名形态）。
func tenantLiteralReturn(fn *ast.FuncDecl) (string, bool) {
	if fn.Body == nil {
		return "", false
	}
	var rets []ast.Expr
	for _, s := range fn.Body.List {
		r, ok := s.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		rets = append(rets, r.Results...)
	}
	if len(rets) != 1 {
		return "", false
	}
	lit, ok := rets[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// readTenantManifest 读登记清单：每行一个物理表名，空行与 `#` 注言行跳过。
// 读失败必须返回 error：清单读不到，双向核对就没人被判定——「给不出结论」
// 不能显示成「没有违规」（与 parseTenantTree 同一纪律）。
func readTenantManifest(root, rel string) ([]tenantManifestEntry, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("租户门: 读租户表登记面 %q 失败: %w", rel, err)
	}
	var out []tenantManifestEntry
	for i, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		out = append(out, tenantManifestEntry{table: text, line: i + 1})
	}
	return out, nil
}

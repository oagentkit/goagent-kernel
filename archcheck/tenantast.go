// tenantast.go —— 租户门的 AST 基础设施：解析、类型推导、形参/证据识别。
//
// 判定流程在 tenantgate.go；本文件只放「从语法树里读出什么」的纯函数，
// 便于把执行体保持在 max_file_lines 阈值以内（拆的是关注点，不是为拆而拆）。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// tenantTypeName 一个可带限定符的类型名（如 identity.Actor / Actor）。
type tenantTypeName struct {
	qualifier string
	name      string
}

// tenantFile 一个已解析的生产源文件。
type tenantFile struct {
	rel     string // 相对仓根（ToSlash）
	pkg     string // 包名
	file    *ast.File
	fset    *token.FileSet
	imports map[string]string // 源级限定符 → import 路径
}

// tenantFileCache 按目录缓存解析结果，避免登记面与扫描面重复解析同一棵树。
type tenantFileCache map[string][]tenantFile

func (c tenantFileCache) files(root, relDir string) ([]tenantFile, error) {
	files, ok := c[relDir]
	if ok {
		return files, nil
	}
	parsed, err := parseTenantTree(root, relDir)
	if err != nil {
		return nil, err
	}
	c[relDir] = parsed
	return parsed, nil
}

// parseTenantTree 递归解析 root/relDir 下的全部生产 .go 文件。
//
// 解析失败必须返回 error：某个文件解析不了，它的函数与类型就没被判定——
// 「给不出结论」不能显示成「没有违规」。
func parseTenantTree(root, relDir string) ([]tenantFile, error) {
	dir := filepath.Join(root, filepath.FromSlash(relDir))
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("租户门: 扫描面 %q 不存在或不可读: %w", relDir, err)
	}
	paths, err := iterFiles(dir, []string{"*.go"}, true)
	if err != nil {
		return nil, fmt.Errorf("租户门: 遍历 %q 失败: %w", relDir, err)
	}
	out := make([]tenantFile, 0, len(paths))
	for _, p := range paths {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if perr != nil {
			return nil, fmt.Errorf("租户门: 解析 %s 失败（该文件未被判定）: %w", p, perr)
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, tenantFile{
			rel:     filepath.ToSlash(rel),
			pkg:     f.Name.Name,
			file:    f,
			fset:    fset,
			imports: tenantImportAliases(f),
		})
	}
	return out, nil
}

// tenantImportAliases 取源级限定符 → import 路径。
//
// 解析别名让 policy 可以写包名（identity.Actor），而代码里 `import id ".../identity"`
// 后写 `id.Actor` 仍然算同一个类型——否则改一个 import 别名就能静默绕过判据。
func tenantImportAliases(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = p
	}
	return out
}

// pkgNameOf 把源级限定符解析成包名（别名 → import 路径末段）。
func (tf tenantFile) pkgNameOf(qual string) string {
	if p, ok := tf.imports[qual]; ok {
		return path.Base(p)
	}
	return qual
}

// qualifierMatches 限定符是否指向 policy 声明的包（包名或完整 import 路径）。
func (tf tenantFile) qualifierMatches(qual, want string) bool {
	if qual == want {
		return true
	}
	if p, ok := tf.imports[qual]; ok {
		return p == want || path.Base(p) == want
	}
	return false
}

// parseTenantTypeNames 把 policy 的类型名清单解析成限定名集合。
func parseTenantTypeNames(values []string) []tenantTypeName {
	out := make([]tenantTypeName, 0, len(values))
	for _, raw := range values {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "*"))
		if s == "" {
			continue
		}
		if i := strings.LastIndex(s, "."); i >= 0 {
			out = append(out, tenantTypeName{qualifier: s[:i], name: s[i+1:]})
			continue
		}
		out = append(out, tenantTypeName{name: s})
	}
	return out
}

// isTenantAuthType 判定类型表达式是否为授权上下文。
//
// 裸名（无限定符）按 bianque 原判据宽松匹配最后一段；限定名按包名/import 别名
// 匹配——这样 `context.Context` 不会被一个叫 Context 的授权类型误伤。
func isTenantAuthType(expr ast.Expr, auths []tenantTypeName, tf tenantFile) bool {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return isTenantAuthType(t.X, auths, tf)
	case *ast.Ident:
		for _, a := range auths {
			if a.name == t.Name && (a.qualifier == "" || a.qualifier == tf.pkg) {
				return true
			}
		}
	case *ast.SelectorExpr:
		q := ""
		if id, ok := t.X.(*ast.Ident); ok {
			q = id.Name
		}
		for _, a := range auths {
			if a.name != t.Sel.Name {
				continue
			}
			if a.qualifier == "" || tf.qualifierMatches(q, a.qualifier) {
				return true
			}
		}
	}
	return false
}

// tenantAuthParam 返回授权上下文形参名。
//
// 与 bianque 同口径：匿名形参无法判定（跳过）；`_` 显式丢弃同样跳过。
func tenantAuthParam(fn *ast.FuncDecl, auths []tenantTypeName, tf tenantFile) (string, bool) {
	if fn.Type.Params == nil {
		return "", false
	}
	for _, p := range fn.Type.Params.List {
		if len(p.Names) == 0 {
			continue
		}
		for _, nm := range p.Names {
			if nm.Name == "_" {
				continue
			}
			if isTenantAuthType(p.Type, auths, tf) {
				return nm.Name, true
			}
		}
	}
	return "", false
}

// tenantTypeIndex 推导租户域类型：包名 → 含租户字段的 struct 名集合。
func tenantTypeIndex(files []tenantFile, fields map[string]bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
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
				if out[tf.pkg] == nil {
					out[tf.pkg] = map[string]bool{}
				}
				out[tf.pkg][ts.Name.Name] = true
				return true
			}
			return true
		})
	}
	return out
}

// mentionsTenant 判定类型表达式是否指向租户域类型，返回可读类型名。
func mentionsTenant(expr ast.Expr, tf tenantFile, tenants map[string]map[string]bool) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		if tenants[tf.pkg][t.Name] {
			return tf.pkg + "." + t.Name, true
		}
	case *ast.SelectorExpr:
		q, ok := t.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		pkg := tf.pkgNameOf(q.Name)
		if tenants[pkg][t.Sel.Name] {
			return pkg + "." + t.Sel.Name, true
		}
	case *ast.ArrayType:
		return mentionsTenant(t.Elt, tf, tenants)
	case *ast.MapType:
		if s, ok := mentionsTenant(t.Key, tf, tenants); ok {
			return s, true
		}
		return mentionsTenant(t.Value, tf, tenants)
	case *ast.StarExpr:
		return mentionsTenant(t.X, tf, tenants)
	case *ast.Ellipsis:
		return mentionsTenant(t.Elt, tf, tenants)
	case *ast.ChanType:
		return mentionsTenant(t.Value, tf, tenants)
	}
	return "", false
}

// tenantReturn 返回函数结果类型里的第一个租户域类型。
func tenantReturn(fn *ast.FuncDecl, tf tenantFile, tenants map[string]map[string]bool) (string, bool) {
	if fn.Type.Results == nil {
		return "", false
	}
	for _, r := range fn.Type.Results.List {
		if s, ok := mentionsTenant(r.Type, tf, tenants); ok {
			return s, true
		}
	}
	return "", false
}

// tenantUsesIdent 判定函数体是否引用了该名字。
//
// 按标识符出现计：遮蔽只会漏判（少报），不会误判（多报）。漏报方向由
// 「声明了却不用」这一判据本身兜底——真正丢弃上下文时形参名不会出现。
func tenantUsesIdent(body *ast.BlockStmt, name string) bool {
	if name == "" {
		return true
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// tenantEvidence 在函数体里找隔离判定证据：调用共享判定面，或查询条件含过滤列。
func tenantEvidence(fn *ast.FuncDecl, surfaceNames map[string]bool, filterCols []string) (string, bool) {
	evidence := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if surfaceNames[fun.Sel.Name] && evidence == "" {
				evidence = "共享判定面 " + fun.Sel.Name
			}
			if fun.Sel.Name == "Where" || fun.Sel.Name == "Raw" {
				for _, a := range call.Args {
					lit, ok := a.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					for _, col := range filterCols {
						if col != "" && strings.Contains(lit.Value, col) && evidence == "" {
							evidence = "查询条件含过滤列 " + col
						}
					}
				}
			}
		case *ast.Ident:
			if surfaceNames[fun.Name] && evidence == "" {
				evidence = "共享判定面 " + fun.Name
			}
		}
		return true
	})
	return evidence, evidence != ""
}

// tenantIsoDecl 查 `// tenant:iso <理由>` 显式声明，返回理由（非空才算数）。
func tenantIsoDecl(fn *ast.FuncDecl, file *ast.File) (string, bool) {
	scan := func(g *ast.CommentGroup) (string, bool) {
		if g == nil {
			return "", false
		}
		for _, c := range g.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if !strings.HasPrefix(text, tenantIsoTag) {
				continue
			}
			reason := strings.TrimSpace(strings.TrimPrefix(text, tenantIsoTag))
			if reason == "" {
				return "", false
			}
			return reason, true
		}
		return "", false
	}
	if reason, ok := scan(fn.Doc); ok {
		return reason, true
	}
	for _, g := range file.Comments {
		if g.Pos() >= fn.Pos() && g.End() <= fn.End() {
			if reason, ok := scan(g); ok {
				return reason, true
			}
		}
	}
	return "", false
}

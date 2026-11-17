// seamcontract.go —— 形态 B：接缝契约标记完备性（issue #369/#370）。
//
// 判据：导出接缝声明点（导出函数/方法/接口方法/结构体字段，参数或字段类型为
// `func() time.Time`）的文档注释必须带齐
//
//	// @seam: no-reentry no-blocking panic-isolated
//
// 三条约束：不得重入 / 不得阻塞 / panic 处置（panic-isolated 或 panic-caller
// 恰一个）。缺一条即红（退出码 1）。
//
// 为什么是**导出声明点**：内部字段/参数是实现的自由，宿主看不到；契约要约束
// 的是宿主能注入的那个面。issue #369 的 20 条统计把字段与参数分开计，本门禁
// 按「声明点」计（一处 option 函数覆盖其字段），口径差异写在
// docs/SEAM-CONTRACT.md，不假装是同一个数。
//
// 根本局限（必须记住）：标记是**承诺不是事实**——声明了 no-reentry 但代码
// 仍在锁内调，本规则照样绿。兑现面由形态 A（只读扫描）与形态 C（panic 隔离）
// 承担，逐条契约用例是 #370 的形态 C 延伸。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// seamDecl 一处导出接缝声明。
type seamDecl struct {
	name string
	doc  *ast.CommentGroup
	pos  token.Pos
}

// seamContractViolations 形态 B：枚举导出时钟接缝声明并检查契约标记。
func seamContractViolations(root string, rules SeamRules, policyPath string) ([]Violation, error) {
	files, err := seamScanFiles(root)
	if err != nil {
		return nil, err
	}
	var out []Violation
	found := 0
	for _, path := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("接缝门禁解析 %s 失败（判定面不完整，门禁给不出结论）: %w", path, err)
		}
		for _, d := range collectSeamDecls(f) {
			found++
			tokens, _ := parseSeamTokens(d.doc, rules.marker())
			missing := missingSeamTokens(tokens, rules)
			if len(missing) == 0 {
				continue
			}
			rel, err := seamRel(root, path)
			if err != nil {
				return nil, err
			}
			out = append(out, Violation{
				Rule: seamContractRuleID,
				Path: rel,
				Line: fset.Position(d.pos).Line,
				Text: fmt.Sprintf("接缝 %s 的契约标记不完整（缺 %s）："+
					"要求在声明处写 %s %s %s —— 三条约束是 #369 的固定模板；"+
					"panic 处置二选一（panic-isolated = 内核隔离，panic-caller = 由调用方兜）",
					d.name, strings.Join(missing, " / "),
					rules.marker(), strings.Join(rules.requiredTokens(), " "),
					strings.Join(rules.panicTokens(), "|")),
				Kind: "seam_contract",
			})
		}
	}
	if found < rules.MinDeclarations {
		out = append(out, Violation{
			Rule: seamContractRuleID,
			Path: policyPath,
			Line: 0,
			Text: fmt.Sprintf("接缝门禁判定面失效：只枚举到 %d 个导出时钟接缝声明（seams.min_declarations=%d）——"+
				"枚举逻辑或包布局已漂移，门禁不能再假装「没违规」", found, rules.MinDeclarations),
			Kind: "seam_contract",
		})
	}
	return out, nil
}

// collectSeamDecls 枚举导出接缝声明点（函数/方法/接口方法/结构体字段）。
func collectSeamDecls(f *ast.File) []seamDecl {
	times := timeAliases(f)
	var out []seamDecl
	add := func(name string, doc *ast.CommentGroup, pos token.Pos) {
		out = append(out, seamDecl{name: name, doc: doc, pos: pos})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() || !hasClockParam(d.Type.Params, times) {
				continue
			}
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = seamRecvName(d.Recv.List[0].Type) + "." + name
			}
			add(name, d.Doc, d.Pos())
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				switch t := ts.Type.(type) {
				case *ast.InterfaceType:
					if t.Methods == nil {
						continue
					}
					for _, m := range t.Methods.List {
						if len(m.Names) == 0 || !m.Names[0].IsExported() {
							continue
						}
						ft, ok := m.Type.(*ast.FuncType)
						if !ok || !hasClockParam(ft.Params, times) {
							continue
						}
						doc := m.Doc
						if doc == nil {
							doc = ts.Doc
						}
						add(ts.Name.Name+"."+m.Names[0].Name, doc, m.Pos())
					}
				case *ast.StructType:
					if t.Fields == nil {
						continue
					}
					for _, fld := range t.Fields.List {
						if !isClockFuncType(fld.Type, times) {
							continue
						}
						for _, n := range fld.Names {
							if !n.IsExported() {
								continue
							}
							doc := fld.Doc
							if doc == nil {
								doc = ts.Doc
							}
							add(ts.Name.Name+"."+n.Name, doc, n.Pos())
						}
					}
				}
			}
		}
	}
	return out
}

// timeAliases 返回本文件里 time 包的限定符集合（支持别名）。
func timeAliases(f *ast.File) map[string]bool {
	out := map[string]bool{"time": true}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path != "time" {
			continue
		}
		name := "time"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name != "_" && name != "." {
			out[name] = true
		}
	}
	return out
}

// hasClockParam 参数列表里是否有 func() time.Time。
func hasClockParam(fl *ast.FieldList, times map[string]bool) bool {
	if fl == nil {
		return false
	}
	for _, f := range fl.List {
		if isClockFuncType(f.Type, times) {
			return true
		}
	}
	return false
}

// isClockFuncType 判定 expr 是否为 func() time.Time。
func isClockFuncType(expr ast.Expr, times map[string]bool) bool {
	ft, ok := expr.(*ast.FuncType)
	if !ok || (ft.Params != nil && len(ft.Params.List) > 0) {
		return false
	}
	if ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	sel, ok := ft.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Time" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && times[id.Name]
}

// seamRecvName 接收者类型名（去掉指针/泛型参数）。
func seamRecvName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return seamRecvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return seamRecvName(t.X)
	case *ast.IndexListExpr:
		return seamRecvName(t.X)
	}
	return ""
}

// parseSeamTokens 从注释组里取标记后面的约束词。
func parseSeamTokens(doc *ast.CommentGroup, marker string) (map[string]bool, bool) {
	if doc == nil {
		return nil, false
	}
	for _, c := range doc.List {
		idx := strings.Index(c.Text, marker)
		if idx < 0 {
			continue
		}
		rest := c.Text[idx+len(marker):]
		tokens := map[string]bool{}
		for _, tok := range strings.Fields(rest) {
			tokens[strings.Trim(tok, ".,;()[]`")] = true
		}
		return tokens, true
	}
	return nil, false
}

// missingSeamTokens 返回缺失的约束词/panic 处置。
func missingSeamTokens(tokens map[string]bool, rules SeamRules) []string {
	var missing []string
	for _, want := range rules.requiredTokens() {
		if !tokens[want] {
			missing = append(missing, want)
		}
	}
	dispositions := rules.panicTokens()
	n := 0
	for _, p := range dispositions {
		if tokens[p] {
			n++
		}
	}
	if n != 1 {
		missing = append(missing, "panic 处置（"+strings.Join(dispositions, "|")+" 恰一个）")
	}
	return missing
}

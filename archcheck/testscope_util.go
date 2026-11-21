// testscope_util.go —— 作用域模型的支撑件（父节点表、局部名、import 本地名、源码渲染）。
//
// 单独成文件是为了让 testscope.go 保持在结构阈值之内（max_file_lines: 400）：
// 作用域模型是**判定**（判「这个等待点落在哪个执行上下文里」），
// 而支撑件是**取值与路径解析**（判据之外的准备工作）——分开读，
// 读者不必在判定主线里找「怎么拿父节点」这种问题。
package archcheck

import (
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

// tsForm 渲染一段源码（单行、截断），供报告点名。
//
// 按 rune 截断：按字节切会把多字节字符切成非法 UTF-8（报告里出现乱码，
// 而报告是门禁的对外输出，乱码等于信息丢失）。
func tsForm(fset *token.FileSet, n ast.Node) string {
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, n); err != nil {
		return "?"
	}
	form := strings.Join(strings.Fields(sb.String()), " ")
	runes := []rune(form)
	if len(runes) > 80 {
		form = string(runes[:80]) + "…"
	}
	return form
}

// tsTBinding 取一个参数表里 testing.T 的绑定名。
//
// 优先认类型 *testing.T（认不出类型就退到「参数名叫 t」），返回的名字与
// 「本作用域是否自己声明了 T」两个事实。闭包没声明时沿用 inherited ⇒
// 它的 t.Fatal 终止的是外层那个 goroutine。
func tsTBinding(params *ast.FieldList, inherited string) (string, bool) {
	if params == nil {
		return inherited, false
	}
	fallback := ""
	for _, p := range params.List {
		if !tsIsTestingT(p.Type) {
			continue
		}
		if len(p.Names) == 0 {
			return inherited, false
		}
		return p.Names[0].Name, true
	}
	for _, p := range params.List {
		for _, n := range p.Names {
			if n.Name == "t" {
				fallback = n.Name
			}
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return inherited, false
}

// tsIsTestingT 判一个类型是不是 *testing.T（语法判定，不 import testing）。
func tsIsTestingT(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "testing" && sel.Sel.Name == "T"
}

// tsAllScopes 列出一个作用域树里的全部作用域（含根）。
func tsAllScopes(root *tsScope) []*tsScope {
	var out []*tsScope
	var walk func(s *tsScope)
	walk = func(s *tsScope) {
		out = append(out, s)
		for _, c := range s.children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// tsParentMap 建 AST 父节点表。
func tsParentMap(root ast.Node) map[ast.Node]ast.Node {
	m := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			m[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return m
}

// tsGoLits 取一个函数里由 go 语句启动的函数字面量。
func tsGoLits(root ast.Node) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	ast.Inspect(root, func(n ast.Node) bool {
		if gs, ok := n.(*ast.GoStmt); ok {
			if fl, ok := gs.Call.Fun.(*ast.FuncLit); ok {
				out[fl] = true
			}
		}
		return true
	})
	return out
}

// tsAncestor 判祖先链上是否有满足 pred 的节点。
func tsAncestor(parents map[ast.Node]ast.Node, n ast.Node, pred func(ast.Node) bool) bool {
	for p := parents[n]; p != nil; p = parents[p] {
		if pred(p) {
			return true
		}
	}
	return false
}

// tsLocalNames 取一个作用域自己的局部名字（参数 + := + var/const/type 声明）。
//
// 嵌套函数面有自己的作用域，不并进来——否则外层的包级调用会被内层的局部名误杀。
func tsLocalNames(root ast.Node) map[string]bool {
	names := map[string]bool{}
	addParams := func(params *ast.FieldList) {
		if params == nil {
			return
		}
		for _, p := range params.List {
			for _, n := range p.Names {
				names[n.Name] = true
			}
		}
	}
	var body ast.Node
	switch v := root.(type) {
	case *ast.FuncDecl:
		addParams(v.Type.Params)
		body = v.Body
	case *ast.FuncLit:
		addParams(v.Type.Params)
		body = v.Body
	}
	if body == nil {
		return names
	}
	ast.Inspect(body, func(x ast.Node) bool {
		switch v := x.(type) {
		case *ast.FuncLit:
			return false
		case *ast.AssignStmt:
			if v.Tok == token.DEFINE {
				for _, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						names[id.Name] = true
					}
				}
			}
		case *ast.DeclStmt:
			gd, ok := v.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names[n.Name] = true
					}
				case *ast.TypeSpec:
					names[s.Name.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// tsImportLocal 取一个文件里某 import 路径的本地名（默认包名，别名优先）。
//
// 找不到时返回 ""（表示「这个包在本文件里不可用」）。
func tsImportLocal(file *ast.File, path string) string {
	local := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		local = path[i+1:]
	}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
	}
	return local
}

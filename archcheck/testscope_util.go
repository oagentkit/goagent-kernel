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

// tsNonBlockingRecv 判一处接收表达式是否**非阻塞**。
//
// 非阻塞 = 它落在某个 select 的 CommClause 里，且那个 select **带 default 分支**
// ⇒ 这个 select 永不挂起（要么某分支就绪、要么直接走 default）⇒ 这处接收
// **不等任何东西**。
//
// # 为什么必须把它与阻塞接收分开
//
// 本仓两条测试面纪律都把「等待点」读成「用例在这等那个 goroutine」。而
// 非阻塞 drain（`select { case ev := <-ch: ...; default: }`）是**断言**
// （看此刻通道里有没有东西），不是 join ⇒
//
//  1. 把它当等待点 ⇒ 「收尾等待必须在 defer 里」这条规则会判错：正确的
//     处方**不是**把 drain 塞进 defer（drain 本来就不等东西，塞进 defer
//     什么也等不到），而是给那个 goroutine **补一个真正的收尾 join**。
//  2. 把它当等待点 ⇒ 「用例内起的 goroutine 必须被等待」那条规则**假阴性**
//     （它以为用例等过了，其实从头到尾没等）。
//
// ⇒ 本包的新规则（defer_cleanup）用**窄口径**：非阻塞接收不算等待点。
// 兄弟规则（goroutine_join）仍用宽口径（任意接收表达式都算）——那是它的
// **已登记边界 1**，本轮不动（改它会把一批现存用例判红，属另一条登记行）。
//
// # 与 select 的另一半形态要分清
//
// `select { case v := <-ch: ...; case <-time.After(d): }` **没有 default**
// ⇒ 仍会挂起 ⇒ 仍是阻塞接收 ⇒ 算等待点。
// tsSelectHasDefault 判一个 select 是否带 default 分支。
//
// go/ast 里 default 分支不单独占字段：它落在 SelectStmt.Body 里，是一个
// Comm 为 nil 的 *ast.CommClause（Case 列表只装真正的 case 分支）。
func tsSelectHasDefault(sel *ast.SelectStmt) bool {
	for _, st := range sel.Body.List {
		if cc, ok := st.(*ast.CommClause); ok && cc.Comm == nil {
			return true
		}
	}
	return false
}

func tsNonBlockingRecv(parents map[ast.Node]ast.Node, n ast.Node) bool {
	for p := parents[n]; p != nil; p = parents[p] {
		switch v := p.(type) {
		case *ast.CommClause:
			// CommClause 的父节点不一定是 SelectStmt：go/ast 的 SelectStmt.Body
			// 是 *ast.BlockStmt，clauses 装在它里面 ⇒ 最多往上跳两跳。
			for q := parents[v]; q != nil; q = parents[q] {
				if sel, ok := q.(*ast.SelectStmt); ok {
					return tsSelectHasDefault(sel)
				}
				if _, ok := q.(*ast.BlockStmt); !ok {
					break
				}
			}
			return false
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		}
	}
	return false
}

// testgoroutine_wait.go —— 等待点与助手链的判定（豁免面）。
//
// # 这条规则怎么判「测试侧等过了」
//
// 豁免只有两种来源：测试函数体里直接有等待点，或它调用的**包内助手**里有等待点。
// 助手必须跟到一定深度（≤2）：收尾助手自己可能再调一个助手，只看一层会把那种
// 形态误判成违规（误报到没人信它 = 门禁等于没有）。
//
// # 为什么等待点不看 goroutine 自己的函数体
//
// go func(){ ... }() 的**函数体内部**的 <-ch 是那个 goroutine 在等**别人**，不是
// 测试侧在等它——把它算成等待点，等于给「起了不等」的写法发免死金牌。故等待点
// 扫描跳过 go 语句自身的函数字面量。
//
// # 为什么只认裸标识符调用
//
// 选择器调用（obj.Method()、pkg.Func()）不是包级函数调用：前者的函数体不在本包，
// 后者的函数体在别的包——都跨不过去，故不作为助手。
package archcheck

import (
	"go/ast"
	"go/token"
)

// gjHasWait 判函数体里有没有等待点（跳过 go 语句自身的函数字面量）。
//
// 跳过的是**那个 goroutine 在等别人**的写法，不是测试侧的等待。
func gjHasWait(fn *ast.FuncDecl) bool {
	return gjHasWaitIn(fn, gjGoroutineBodies(fn))
}

// gjHasWaitInHelpers 判助手里有没有等待点（深度 ≤ gjMaxHelperDepth）。
//
// 助手的等待点按**调用链**找，不是按目录里有没有等待函数找：目录里存在某个
// 带等待的函数、与本用例的收尾无关，不该给本用例发免死金牌。
func gjHasWaitInHelpers(p *gjPkg, tf *gjFunc) bool {
	seen := map[string]bool{}
	frontier := []*gjFunc{tf}
	for depth := 0; depth < gjMaxHelperDepth && len(frontier) > 0; depth++ {
		var next []*gjFunc
		for _, cur := range frontier {
			for _, name := range gjCalleeNames(cur.decl) {
				h, ok := p.helpers[name]
				if !ok || seen[h.name] {
					continue
				}
				seen[h.name] = true
				if gjHasWait(h.decl) {
					return true
				}
				next = append(next, h)
			}
		}
		frontier = next
	}
	return false
}

// gjCalleeNames 取函数体里调用的**包级**函数名（裸标识符调用）。
//
// 只认裸标识符：选择器调用（obj.Method()、pkg.Func()）不是包级函数调用，
// 跨不过去（pkg.Func() 的函数体不在本包，obj.Method() 是方法）。
func gjCalleeNames(fn *ast.FuncDecl) []string {
	locals := gjLocalNames(fn)
	var out []string
	seen := map[string]bool{}
	ast.Inspect(fn, func(x ast.Node) bool {
		if x == nil {
			return false
		}
		ce, ok := x.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := ce.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		// 局部变量/参数遮蔽同名包级函数时，那次调用不是包级函数调用。
		if locals[id.Name] {
			return true
		}
		if !seen[id.Name] {
			seen[id.Name] = true
			out = append(out, id.Name)
		}
		return true
	})
	return out
}

// gjLocalNames 取函数自己的局部名字（参数 + := + var/const/type 声明）。
//
// 用于排除「局部变量遮蔽包级函数名」的误判。嵌套函数字面量有自己的作用域，
// 不并进来——否则外层的包级调用会被内层的局部名误杀。
func gjLocalNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	if fn.Type.Params != nil {
		for _, p := range fn.Type.Params.List {
			for _, n := range p.Names {
				names[n.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(x ast.Node) bool {
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

// gjGoroutineBodies 取该函数里由 go 语句启动的函数字面量（等待点扫描要跳过它们）。
func gjGoroutineBodies(fn *ast.FuncDecl) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	ast.Inspect(fn, func(x ast.Node) bool {
		if x == nil {
			return false
		}
		if gs, ok := x.(*ast.GoStmt); ok {
			if fl, ok := gs.Call.Fun.(*ast.FuncLit); ok {
				out[fl] = true
			}
		}
		return true
	})
	return out
}

// gjHasWaitIn 判子树里有没有等待点（跳过给定的子树）。
func gjHasWaitIn(fn *ast.FuncDecl, skip map[ast.Node]bool) bool {
	found := false
	ast.Inspect(fn, func(x ast.Node) bool {
		if x == nil || found {
			return false
		}
		if fl, ok := x.(*ast.FuncLit); ok && skip[fl] {
			return false
		}
		switch v := x.(type) {
		case *ast.UnaryExpr:
			// 接收表达式 <-ch。
			if v.Op == token.ARROW {
				found = true
				return false
			}
		case *ast.CallExpr:
			// 调用 X.Wait()。
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

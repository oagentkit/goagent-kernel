// testscope.go —— 测试面「执行上下文」的语法模型（defer_cleanup 与 wallclock_ruler 共用）。
//
// # 为什么需要「作用域」这一层
//
// 两条新规则判的都是**结构关系**，而结构关系的粒度既不是文件也不是函数，
// 是**执行上下文**：
//
//   - t.Run 的子用例闭包跑在**另一个 goroutine** 里 ⇒ 它的 t.Fatal 终止的是
//     子用例，不是父用例（父用例照常往下走）；
//   - go 语句的函数字面量跑在**新 goroutine** 里 ⇒ 里面的 <-ch 是那个
//     goroutine 在等别人，不是测试侧在等它；
//   - defer 注册的收尾助手在**函数返回时**才跑 ⇒ 它是「收尾等待」。
//
// 判据若不区分这三者，会把「子用例失败」当成「父用例被终止」（假红），
// 把「goroutine 在等别人」当成「测试侧在等它」（假绿）——两处都是门禁最贵的
// 失效形态（误报到没人信它 / 判不出真问题）。
//
// # 作用域是什么
//
// 一个作用域 = 一个**执行上下文**：测试函数本体，或它内部的一个函数面
// （闭包）。函数面的父作用域是**词法**上的外层作用域；但 testing.T 的绑定
// 可以被闭包自己的参数遮蔽 ⇒ 「T 绑定归属」与「词法父作用域」分开记：
//
//   - 闭包自己声明了 *testing.T 参数（如 t.Run 的 func(t *testing.T)）：
//     它的 t.Fatal 终止的是**子用例**，对父用例的判定不构成「提前终止」；
//   - 闭包没有声明 T 参数：它沿用外层的 T ⇒ 里面的 t.Fatal 终止的是
//     **外层那个** goroutine。
//
// # 这层模型判不了什么（边界）
//
// 本模型只认**语法形状**：它不判「那个等待点等的是不是被起的那个 goroutine」
// （与 testgoroutine.go 头注的已知边界 1~3 同族），也不判「闭包什么时候被调用」。
// 故本文件的结论一律表述为「形状上是否合规」，不表述为「运行时一定 join 了」。
package archcheck

import (
	"go/ast"
	"go/token"
)

// tsScope 一个执行上下文作用域。
type tsScope struct {
	parent   *tsScope
	children []*tsScope
	root     ast.Node // *ast.FuncDecl（测试函数本体）或 *ast.FuncLit（闭包）
	tName    string   // 本作用域里 testing.T 的绑定名（"" = 没有绑定）
	ownsT    bool     // 本作用域自己声明了 T 绑定（t.Run 闭包：它的 t.Fatal 终止的是子用例）
	tOwner   *tsScope // T 绑定的归属作用域（闭包没自己声明 T 时沿用外层）

	goStmts    []*ast.GoStmt
	waits      []tsWait
	terms      []tsTerm
	sleeps     []tsSleep
	calls      []tsCall
	deferCalls []tsCall
}

// tsWait 一个等待点：接收表达式 <-ch，或调用 X.Wait()。
type tsWait struct {
	node    ast.Node
	line    int
	form    string
	inDefer bool // 落在某个 defer 语句内 ⇒ 是「收尾等待」
}

// tsTerm 一个能让用例提前终止的调用（t.Fatal 族 / t.Skip 族 / runtime.Goexit）。
type tsTerm struct {
	pos   token.Pos
	line  int
	form  string
	owner *tsScope // 该调用终止的是哪个作用域的 goroutine（T 绑定的归属）
}

// tsSleep 一处 time.Sleep 调用。
type tsSleep struct {
	pos    token.Pos
	line   int
	form   string
	inLoop bool // 落在 for/range 循环体内 ⇒ 轮询退避，不是尺子
}

// tsCall 一次对**包级助手**的调用（裸标识符调用）。
type tsCall struct {
	name string
	pos  token.Pos
	line int
}

// tsBuilder 建作用域树并收集判定素材。
type tsBuilder struct {
	decl         *ast.FuncDecl
	fset         *token.FileSet
	timeLocal    string
	runtimeLocal string
	parents      map[ast.Node]ast.Node
	goLits       map[ast.Node]bool
	scopes       map[ast.Node]*tsScope
	root         *tsScope
}

// tsNewBuilder 建一个作用域构造器。
func tsNewBuilder(decl *ast.FuncDecl, fset *token.FileSet, timeLocal, runtimeLocal string) *tsBuilder {
	return &tsBuilder{decl: decl, fset: fset, timeLocal: timeLocal, runtimeLocal: runtimeLocal}
}

// build 建作用域树并收集素材，返回根作用域（测试函数本体）。
func (b *tsBuilder) build() *tsScope {
	b.parents = tsParentMap(b.decl)
	b.goLits = tsGoLits(b.decl)
	b.root = &tsScope{root: b.decl}
	b.root.tName, b.root.ownsT = tsTBinding(b.decl.Type.Params, "")
	b.root.tOwner = b.root
	b.scopes = map[ast.Node]*tsScope{b.decl: b.root}

	// 作用域树：逐个函数面建作用域（go 语句自身的函数字面量除外）。
	ast.Inspect(b.decl, func(n ast.Node) bool {
		fl, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if b.goLits[fl] || b.inGoLit(fl) {
			return false
		}
		parent := b.scopeOf(fl)
		s := &tsScope{parent: parent, root: fl}
		s.tName, s.ownsT = tsTBinding(fl.Type.Params, parent.tName)
		if s.ownsT {
			s.tOwner = s
		} else {
			s.tOwner = parent.tOwner
		}
		b.scopes[fl] = s
		parent.children = append(parent.children, s)
		return true
	})

	// 收集素材。
	ast.Inspect(b.decl, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			if b.goLits[v] || b.inGoLit(v) {
				return false
			}
			return true
		case *ast.GoStmt:
			// go 语句本身属于当前作用域；它的**内部**不判（那在新 goroutine 里）。
			b.scopeOf(v).goStmts = append(b.scopeOf(v).goStmts, v)
			return false
		case *ast.DeferStmt:
			b.collectDefer(v)
			return true
		case *ast.CallExpr:
			b.collectCall(v)
			return true
		case *ast.UnaryExpr:
			// 非阻塞 drain（select 带 default）不等任何东西 ⇒ 不是等待点。
			if v.Op == token.ARROW && !tsNonBlockingRecv(b.parents, v) {
				s := b.scopeOf(v)
				s.waits = append(s.waits, tsWait{
					node:    v,
					line:    b.line(v),
					form:    b.form(v),
					inDefer: b.inDefer(v),
				})
			}
			return true
		}
		return true
	})
	return b.root
}

// collectDefer 收 defer 语句里的包级助手调用（defer 调用的助手 = 收尾助手）。
func (b *tsBuilder) collectDefer(d *ast.DeferStmt) {
	s := b.scopeOf(d)
	ast.Inspect(d, func(c ast.Node) bool {
		if c == nil {
			return false
		}
		ce, ok := c.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := ce.Fun.(*ast.Ident); ok {
			s.deferCalls = append(s.deferCalls, tsCall{name: id.Name, pos: ce.Pos(), line: b.line(ce)})
		}
		return true
	})
}

// collectCall 收一处调用表达式上的三类素材：助手调用 / 终止调用 / sleep。
func (b *tsBuilder) collectCall(c *ast.CallExpr) {
	s := b.scopeOf(c)
	// 助手调用（裸标识符）：defer 里的归入 deferCalls。
	if id, ok := c.Fun.(*ast.Ident); ok {
		if b.inDefer(c) {
			s.deferCalls = append(s.deferCalls, tsCall{name: id.Name, pos: c.Pos(), line: b.line(c)})
		} else {
			s.calls = append(s.calls, tsCall{name: id.Name, pos: c.Pos(), line: b.line(c)})
		}
	}
	// X.Wait() 也是等待点。
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" {
		s.waits = append(s.waits, tsWait{
			node:    c,
			line:    b.line(c),
			form:    b.form(c),
			inDefer: b.inDefer(c),
		})
	}
	// 终止调用。
	if form, ok := b.terminating(c, s); ok {
		s.terms = append(s.terms, tsTerm{pos: c.Pos(), line: b.line(c), form: form, owner: s.tOwner})
	}
	// 墙钟 sleep。
	if b.isSleep(c) {
		s.sleeps = append(s.sleeps, tsSleep{
			pos:    c.Pos(),
			line:   b.line(c),
			form:   b.form(c),
			inLoop: b.inLoop(c),
		})
	}
}

// terminating 判一处调用是不是「能让用例提前终止」的调用。
//
// 口径（逐条）：
//   - t.Fatal / t.Fatalf / t.FailNow / t.Skip / t.Skipf：testing.T 的方法，
//     它们内部走 runtime.Goexit ⇒ 用例 goroutine 当场退出；
//   - runtime.Goexit()：显式退出当前 goroutine。
//
// 判据只认**直接调用**：包内助手内部的 t.Fatal 不算（那是助手里自己的事，
// 与本用例的等待点是否被跳过不是一回事）。
func (b *tsBuilder) terminating(c *ast.CallExpr, s *tsScope) (string, bool) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if s.tName != "" && x.Name == s.tName {
		switch sel.Sel.Name {
		case "Fatal", "Fatalf", "FailNow", "Skip", "Skipf":
			return b.form(c), true
		}
	}
	if b.runtimeLocal != "" && x.Name == b.runtimeLocal && sel.Sel.Name == "Goexit" {
		return b.form(c), true
	}
	return "", false
}

// isSleep 判一处调用是不是 time.Sleep(...)。
//
// 认的是 **import 路径**（按本地名解析，含别名），不是文本里的 "time.Sleep"：
// 别名导入（import t "time"）与遮蔽（局部变量叫 time）都存在，只看文本会判错。
func (b *tsBuilder) isSleep(c *ast.CallExpr) bool {
	if b.timeLocal == "" {
		return false
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sleep" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == b.timeLocal
}

// scopeOf 取一个节点所属的作用域（最近的词法外层作用域）。
func (b *tsBuilder) scopeOf(n ast.Node) *tsScope {
	for p := b.parents[n]; p != nil; p = b.parents[p] {
		if s, ok := b.scopes[p]; ok {
			return s
		}
	}
	return b.root
}

// inGoLit 判节点是否落在某个 go 语句自身的函数字面量里（那在新 goroutine 里）。
func (b *tsBuilder) inGoLit(n ast.Node) bool {
	return tsAncestor(b.parents, n, func(a ast.Node) bool {
		fl, ok := a.(*ast.FuncLit)
		return ok && b.goLits[fl]
	})
}

// inDefer 判节点是否落在某个 defer 语句内。
func (b *tsBuilder) inDefer(n ast.Node) bool {
	return tsAncestor(b.parents, n, func(a ast.Node) bool {
		_, ok := a.(*ast.DeferStmt)
		return ok
	})
}

// inLoop 判节点是否落在 for/range 循环体内。
func (b *tsBuilder) inLoop(n ast.Node) bool {
	return tsAncestor(b.parents, n, func(a ast.Node) bool {
		switch a.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return true
		}
		return false
	})
}

// line 取节点的行号。
func (b *tsBuilder) line(n ast.Node) int {
	return b.fset.Position(n.Pos()).Line
}

// form 渲染一段源码（单行、截断），供报告点名。
func (b *tsBuilder) form(n ast.Node) string {
	return tsForm(b.fset, n)
}

// wallclock_threshold.go —— 「固定秒数断言」与「纯计时器窗口」两种墙钟尺子形态
// 的**检测**（issue #481）。
//
// # 这条补的是 #441 的哪一块空
//
// #441 的 `harness-test-wallclock-ruler` 只拦**裸 time.Sleep** 当尺子。issue #481
// 点名另外两类同形面没进判定面：
//
//   - **固定秒数断言**：`if time.Since(start) > 2*time.Second { t.Fatal(...) }`
//     —— 结论（「有没有超过那个秒数」）由墙钟单独决定，与 #411/#441 同形；
//   - **time.After 窗口断言**：窗口到期本身就是结论（「这 d 之内什么都没发生」）。
//
// # 三类形态，三种处置（issue 要求的「逐类给结论」）
//
//   - **退避类**（sleep 落在 for/range 循环体内）⇒ **结构化豁免**：退出条件是
//     循环条件本身，不是窗口长度。这条由 #441 的判定体直接排除，不进分类登记。
//   - **超时上限类**（窗口只作等待上界，结论由等到的**事实**决定）⇒
//     **显式登记豁免 + 写明理由**（policy 的 `ruler_classifications`）。
//   - **尺子类**（结论由窗口长度单独决定）⇒ **必须改写**成可观测事实；
//     登记成尺子类也不豁免（登记只是记账，不是放行）。
//
// # 为什么「超时上限类」要显式登记，而不是让判据自己推
//
// 「阈值比机制自身的时限宽 50 倍 ⇒ 窗口不决定结论」这件事**推不出来**：
// 同一个形状（`elapsed > 15s`）在一条用例里是宽松上限、在另一条里是紧阈值，
// 差别只在「坏实现会耗时多久」，那不在语法里。
//
// ⇒ 结论只能逐条判定并写下来。写下来的代价是**门禁会对账**（对账层在
// wallclock_classify.go）：漏登记即红、登记成尺子类即红、改完不删登记即红。
//
// # 判定面
//
//   - 只扫**测试面**（policy `only_test: true`）——墙钟尺子出现在用例里；
//   - 测试函数体 + **测试文件里的**包级助手（助手是判定面的延伸，#441 已同口径）；
//   - **goroutine 体里的不算**：那是被测机制自己的节奏，不是测试侧的尺子
//     （与 #441 同口径）。
//
// # 已知边界（写在这里，不靠「没报过」反推）
//
//   - `x.Sub(y)` 形态的已流逝时长**不判**：x 的静态类型拿不到（纯语法判定），
//     只认 `time.Since(...)` 与 `time.Now().Sub(...)` 两个可按 import 路径认出的形态。
//   - 阈值里带单位的常量必须**可静态认出**（`2*time.Second`）；`time.Duration(2)*
//     time.Second`、从变量来的阈值不判——它们不是「固定秒数」。
package archcheck

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
)

// wcSite 一处墙钟判定点。
type wcSite struct {
	rel      string // 相对仓库根的路径
	line     int
	form     string // 形态（wcForm*）
	pkgDir   string // 相对仓库根的目录（分类登记的定位面之一）
	funcName string // 归属函数名（测试函数或包内助手）
	detail   string // 形态描述（报告点名用）
}

// wcClassKey 一处墙钟判定点的登记键。
//
// 键是「目录 + 函数名 + 形态」三段：目录与函数名在代码演进中稳定（行号不稳），
// 形态段让同一个函数里的两类判定点分别登记。
type wcClassKey struct {
	pkgDir   string
	funcName string
	form     string
}

func (k wcClassKey) String() string {
	return k.pkgDir + "." + k.funcName + "#" + k.form
}

// ── 检测入口 ──────────────────────────────────────────────────────────

// wcThresholdSites 找出判定面里全部「墙钟阈值判定点」。
func wcThresholdSites(files []gjFile, r Rule) []wcSite {
	var out []wcSite
	for _, p := range gjGroupPackages(files) {
		out = append(out, wcPkgThresholdSites(p, r)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rel != out[j].rel {
			return out[i].rel < out[j].rel
		}
		return out[i].line < out[j].line
	})
	return out
}

// wcPkgThresholdSites 判一个包里的全部墙钟阈值判定点。
func wcPkgThresholdSites(p *gjPkg, r Rule) []wcSite {
	var out []wcSite
	for _, tf := range p.tests {
		out = append(out, wcFuncThresholdSites(tf, r)...)
	}
	// 包级助手：只判**测试文件里的**助手（生产文件的助手不是测试面的尺子）。
	names := make([]string, 0, len(p.helpers))
	for n := range p.helpers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := p.helpers[n]
		if !inTestScope(r, h.rel) {
			continue
		}
		out = append(out, wcFuncThresholdSites(h, r)...)
	}
	return out
}

// wcFuncThresholdSites 判一个函数体里的全部墙钟阈值判定点。
func wcFuncThresholdSites(f *gjFunc, r Rule) []wcSite {
	parents := tsParentMap(f.decl)
	goLits := tsGoLits(f.decl)
	timers := wcTimerVars(f.decl, f.timeLocal)
	elapsed := wcElapsedVars(f.decl, f.timeLocal)
	pkgDir := filepath.Dir(f.rel)
	var out []wcSite
	ast.Inspect(f.decl, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BinaryExpr:
			if !wcIsComparison(v.Op) {
				return true
			}
			if wcInsideGoLit(parents, goLits, v) {
				return true
			}
			if !wcIsFixedSecondsCompare(v, f.timeLocal, elapsed) {
				return true
			}
			out = append(out, wcSite{
				rel: f.rel, line: f.fset.Position(v.Pos()).Line,
				form: wcFormFixedSeconds, pkgDir: pkgDir, funcName: f.name,
				detail: tsForm(f.fset, v),
			})
		case *ast.SelectStmt:
			if wcInsideGoLit(parents, goLits, v) {
				return true
			}
			if !wcIsPureTimerSelect(v, timers, f.timeLocal) {
				return true
			}
			out = append(out, wcSite{
				rel: f.rel, line: f.fset.Position(v.Pos()).Line,
				form: wcFormTimerOnlySelect, pkgDir: pkgDir, funcName: f.name,
				detail: tsForm(f.fset, v),
			})
		}
		return true
	})
	return out
}

// ── 形态识别 ──────────────────────────────────────────────────────────

// wcIsFixedSecondsCompare 判一个比较是不是「固定秒数断言」。
//
// 两侧分别是「已流逝时长」与「固定时长常量」，顺序不限。
//
// 「已流逝时长」有两个来源：
//   - **直接**形态：`time.Since(t)` / `time.Now().Sub(t)`；
//   - **经变量**形态：`el := time.Since(t)` 之后拿 `el` 去比。
//
// 经变量这一形态不是可选的补充：实测三条现存判定里两条是这个形状
// （`el := time.Since(start); el > 15*time.Second`）——只认直接形态的判据
// 会把它们整条漏掉，而漏掉的方向恰好是**门禁变绿**。
func wcIsFixedSecondsCompare(b *ast.BinaryExpr, tl string, elapsed map[string]bool) bool {
	return (wcIsElapsedExpr(b.X, tl, elapsed) && wcIsFixedDuration(b.Y, tl)) ||
		(wcIsElapsedExpr(b.Y, tl, elapsed) && wcIsFixedDuration(b.X, tl))
}

// wcIsComparison 判一个二元运算符是不是「阈值比较」。
//
// `==` / `!=` 不算：那不是阈值判定（「耗时恰好等于 0」不是尺子）。
func wcIsComparison(op token.Token) bool {
	switch op {
	case token.GTR, token.GEQ, token.LSS, token.LEQ:
		return true
	}
	return false
}

// wcIsElapsedExpr 判一个表达式是不是「已流逝时长」。
//
// 认三个形态（都按 **import 路径**解析本地名，别名导入也判得到）：
//   - `time.Since(x)`；
//   - `time.Now().Sub(x)`；
//   - 名字落在 elapsed 集合里的局部变量（由 time.Since 赋值而来）。
func wcIsElapsedExpr(e ast.Expr, tl string, elapsed map[string]bool) bool {
	if id, ok := e.(*ast.Ident); ok {
		return elapsed[id.Name]
	}
	if tl == "" {
		return false
	}
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if x, ok := sel.X.(*ast.Ident); ok && x.Name == tl && sel.Sel.Name == "Since" {
		return true
	}
	if sel.Sel.Name == "Sub" {
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		isel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		ix, ok := isel.X.(*ast.Ident)
		return ok && ix.Name == tl && isel.Sel.Name == "Now"
	}
	return false
}

// wcElapsedVars 找出「由已流逝时长表达式赋值的局部变量名」。
//
// 覆盖两种写法：`el := time.Since(start)` 与 `if el := time.Since(start); el > d {}`
// （后者的赋值在 if 的 init 语句里，同样是 := 赋值）。
func wcElapsedVars(decl ast.Node, tl string) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(decl, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE {
			return true
		}
		for i, rhs := range as.Rhs {
			if !wcIsElapsedExpr(rhs, tl, nil) || i >= len(as.Lhs) {
				continue
			}
			if id, ok := as.Lhs[i].(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// wcIsFixedDuration 判一个表达式是不是「固定时长常量」。
//
// 认两种形态：`time.Second` 这类单位常量，以及 `2*time.Second` 这类
// **整数字面量 × 单位常量**。从变量来的阈值不判——那不是「固定秒数」。
func wcIsFixedDuration(e ast.Expr, tl string) bool {
	if tl == "" {
		return false
	}
	switch v := e.(type) {
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		return ok && x.Name == tl && wcIsDurationUnit(v.Sel.Name)
	case *ast.BinaryExpr:
		if v.Op != token.MUL {
			return false
		}
		if wcIsFixedDuration(v.Y, tl) {
			return wcIsIntLit(v.X)
		}
		if wcIsFixedDuration(v.X, tl) {
			return wcIsIntLit(v.Y)
		}
	}
	return false
}

// wcIsDurationUnit 判一个名字是不是 time 包里的时长单位。
func wcIsDurationUnit(name string) bool {
	switch name {
	case "Nanosecond", "Microsecond", "Millisecond", "Second", "Minute", "Hour":
		return true
	}
	return false
}

// wcIsIntLit 判一个表达式是不是整数字面量。
func wcIsIntLit(e ast.Expr) bool {
	bl, ok := e.(*ast.BasicLit)
	return ok && bl.Kind == token.INT
}

// wcTimerVars 找出「由 time.NewTimer 赋值的变量名」。
//
// 用来认 `case <-timer.C:` 这一形态：计时器在上一条语句建好，select 里收到的是
// 它的 C 通道 ⇒ 光看 Comm 表达式认不出那是计时器 ⇒ 漏判且漏得毫无声响。
func wcTimerVars(decl ast.Node, tl string) map[string]bool {
	out := map[string]bool{}
	if tl == "" {
		return out
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range as.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewTimer" {
				continue
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != tl || i >= len(as.Lhs) {
				continue
			}
			if id, ok := as.Lhs[i].(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// wcIsPureTimerSelect 判一个 select 是不是「纯计时器窗口」。
//
// 口径：**每个**分支都只等计时器，且**没有 default**。
//   - 带 default ⇒ 这个 select 永不挂起，它不是窗口；
//   - 有事实分支（等一个事件/信号/ctx）⇒ 窗口是「等事实的上限」，不是尺子。
func wcIsPureTimerSelect(sel *ast.SelectStmt, timers map[string]bool, tl string) bool {
	cases := 0
	for _, st := range sel.Body.List {
		cc, ok := st.(*ast.CommClause)
		if !ok {
			continue
		}
		if cc.Comm == nil {
			return false // default 分支 ⇒ 非阻塞，不是窗口
		}
		cases++
		if !wcCommIsTimer(cc.Comm, timers, tl) {
			return false
		}
	}
	return cases > 0
}

// wcCommIsTimer 判一个 select 分支等的是不是计时器。
func wcCommIsTimer(comm ast.Stmt, timers map[string]bool, tl string) bool {
	found := false
	ast.Inspect(comm, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != tl {
				return true
			}
			switch sel.Sel.Name {
			case "After", "NewTimer", "Tick":
				found = true
			}
		case *ast.SelectorExpr:
			// timer.C 形态：通道来自 time.NewTimer 赋值的变量。
			if v.Sel.Name != "C" {
				return true
			}
			if id, ok := v.X.(*ast.Ident); ok && timers[id.Name] {
				found = true
			}
		}
		return true
	})
	return found
}

// wcInsideGoLit 判一个节点是不是落在某个 go 语句自身的函数字面量里。
func wcInsideGoLit(parents map[ast.Node]ast.Node, goLits map[ast.Node]bool, n ast.Node) bool {
	for p := parents[n]; p != nil; p = parents[p] {
		if goLits[p] {
			return true
		}
	}
	return false
}

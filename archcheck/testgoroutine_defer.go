// testgoroutine_defer.go —— 「收尾等待点必须写成 defer 形态」的判定体。
//
// # 这条规则判什么
//
// issue #440：测试用例里起的 goroutine，其**收尾等待点**必须写在 `defer` 里。
//
// 背景：兄弟规则 harness-test-goroutine-joined 判的是**静态形状**——「测试侧
// 有没有等待点」。它判不出「那个等待点会不会被 Goexit 跳过」：
// runtime.Goexit 会执行所有 defer，但**不执行**它之后的普通语句 ⇒ 用例体
// 末尾的普通语句形式的等待点，在失败路径上根本不执行 ⇒ goroutine 实际未被
// join，而兄弟规则判它合规（这条边界写在 testgoroutine.go 头注的「已知边界
// 第 4 条」，并注明已开单 #440）。
//
// 本规则把那第 4 条边界关掉。
//
// # 判定口径（逐条）
//
//  1. 判定面：**只扫测试文件**（policy only_test: true）、**只判测试函数**
//     （Test / Benchmark / Fuzz 开头的包级函数；TestMain 不在面内）。
//  2. 触发：测试函数体内任意深度（含 t.Run 子用例闭包内）有 go 语句。
//  3. 等待点：接收表达式 <-ch，或调用 X.Wait()；**跳过 go 语句自身的函数字面量**
//     （那是 goroutine 在等别人，与兄弟规则同口径）。
//  4. 等待点的三种落法：
//     - **收尾等待**（合规）：等待点在测试函数体的 defer 语句内，或在一个
//     **由 defer 调用的包内助手**里（深度 ≤2）⇒ 函数返回时一定会执行。
//     - **同步等待**（合规）：等待点在测试函数体的普通语句里，且从该 go 语句
//     到该等待点之间**不存在**任何可能提前终止测试 goroutine 的调用
//     （t.Fatal / t.Fatalf / t.FailNow / t.Skip / t.Skipf / runtime.Goexit
//     的直接调用）⇒ goroutine 一定在用例继续之前结束。
//     - **收尾等待（违规）**：等待点在普通语句里，且上面那类调用**存在**
//     ⇒ Goexit 会跳过这一步 ⇒ 必须改成 defer 形态。
//
// # 为什么「有终止调用」才判违规
//
// 因为 goroutine 一定在**同步等待**的形态下结束：等待点在 go 语句之后、
// 且此前没有任何能终止用例的调用 ⇒ 无论用例是绿是红，那一步都会执行 ⇒
// 失败路径也不会漏 join。把它判成违规就是误报，而误报到没人信它，门禁
// 等于没有。
//
// # 判定为什么必须区分「执行上下文」
//
// t.Run 的子用例闭包跑在另一个 goroutine 里 ⇒ 它的 t.Fatal 终止的是**子用例**，
// 不是父用例（父用例照常往下走）⇒ 拿它当「父用例被提前终止」的证据是误判。
// 故本规则的「终止调用」按**T 绑定的归属**记账（见 testscope.go 的作用域模型）：
// 子用例闭包自己声明的 t 绑定归子用例，不影响父用例。
//
// # 报告必须点名
//
// 文件:行、测试函数名、go 语句形态、等待点形态，以及**为什么**判成「收尾等待」
// —— 即中间那个能让用例提前终止的调用是什么（第几行、什么形态）。
// 「判了但看不出是哪条判的」与「没判」在排查时等价。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
)

// deferCleanupViolations 是本规则的执行体入口。
func deferCleanupViolations(root string, r Rule) ([]Violation, error) {
	files, err := gjScanFiles(root, r)
	if err != nil {
		return nil, err
	}
	return dcJudgeFiles(files, r.ID), nil
}

// dcJudgeFiles 是本规则的**唯一判定核**（真判定与 policy 样例共用）。
func dcJudgeFiles(files []gjFile, ruleID string) []Violation {
	pkgs := gjGroupPackages(files)
	var out []Violation
	for _, p := range pkgs {
		out = append(out, dcJudgePackage(p, ruleID)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// dcJudgePackage 判一个包里的全部测试函数。
func dcJudgePackage(p *gjPkg, ruleID string) []Violation {
	var out []Violation
	for _, tf := range p.tests {
		out = append(out, dcJudgeFunc(p, tf, ruleID)...)
	}
	return out
}

// dcJudgeFunc 判一个测试函数里的全部 go 语句。
func dcJudgeFunc(p *gjPkg, tf *gjFunc, ruleID string) []Violation {
	b := tsNewBuilder(tf.decl, tf.fset, tf.timeLocal, tf.runtimeLocal)
	scopes := tsAllScopes(b.build())
	var out []Violation
	for _, s := range scopes {
		for _, gs := range s.goStmts {
			if v, ok := dcJudgeGoStmt(p, tf, scopes, s, gs, ruleID); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

// dcCandidate 一个普通语句里的等待点候选（附带它在源码里的落点）。
type dcCandidate struct {
	site token.Pos // 等待点（或助手调用）在源码里的位置：与 go 语句之间的一段
	line int
	form string
	desc string
}

// dcJudgeGoStmt 判一个 go 语句的收尾等待点形态。
//
// 返回 (违规, true) 表示判成违规；返回 (零值, false) 表示合规或不在面内。
func dcJudgeGoStmt(p *gjPkg, tf *gjFunc, scopes []*tsScope, s *tsScope, gs *ast.GoStmt, ruleID string) (Violation, bool) {
	// ① 存在 defer 形态的等待点 ⇒ 收尾等待 ⇒ 合规。
	for _, sc := range scopes {
		for _, w := range sc.waits {
			if w.inDefer {
				return Violation{}, false
			}
		}
	}
	// ② 存在「由 defer 调用的包内助手」且助手里有等待点 ⇒ 合规。
	for _, sc := range scopes {
		for _, c := range sc.deferCalls {
			if len(dcHelperChainWaits(p, c.name)) > 0 {
				return Violation{}, false
			}
		}
	}
	// ③ 普通语句里的等待点候选。
	cands := dcCandidates(p, s, gs)
	if len(cands) == 0 {
		return Violation{}, false
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].site < cands[j].site })
	// ④ 逐个判：go 语句与等待点之间有没有能让用例提前终止的调用。
	var offender *dcCandidate
	var offenderTerm tsTerm
	for i := range cands {
		term, ok := dcTerminatingBetween(scopes, s.tOwner, gs, cands[i].site)
		if !ok {
			// 同步等待：goroutine 一定在用例继续之前结束 ⇒ 合规。
			return Violation{}, false
		}
		if offender == nil {
			offender = &cands[i]
			offenderTerm = term
		}
	}
	// ⑤ 全部候选都是收尾等待 ⇒ 违规。
	pos := tf.fset.Position(gs.Pos())
	return Violation{
		Rule: ruleID,
		Path: tf.rel,
		Line: pos.Line,
		Text: fmt.Sprintf(
			"用例 %s 起的 goroutine（%s，第 %d 行）的等待点（第 %d 行 %s，%s）是**收尾等待**："+
				"等待点写在普通语句里，而它与 go 语句之间存在会让用例提前终止的调用"+
				"（第 %d 行 %s）⇒ 用例失败时 runtime.Goexit 会跳过这一步（Goexit 只执行 defer，"+
				"不执行它之后的普通语句），goroutine 实际未被 join ⇒ 等待点必须写成 defer 形态"+
				"（defer func(){ %s }() 或 defer 收尾助手(...)）",
			tf.name, gjGoForm(tf.fset, gs), pos.Line,
			offender.line, offender.form, offender.desc,
			offenderTerm.line, offenderTerm.form,
			offender.form),
		Kind: "test_defer_cleanup",
	}, true
}

// dcCandidates 收一个作用域里、位置在 go 语句之后的普通等待点候选。
func dcCandidates(p *gjPkg, s *tsScope, gs *ast.GoStmt) []dcCandidate {
	var out []dcCandidate
	for _, w := range s.waits {
		if w.inDefer || w.node.Pos() <= gs.End() {
			continue
		}
		out = append(out, dcCandidate{
			site: w.node.Pos(),
			line: w.line,
			form: w.form,
			desc: "写在用例体的普通语句里",
		})
	}
	locals := tsLocalNames(s.root)
	for _, c := range s.calls {
		if c.pos <= gs.End() || locals[c.name] {
			continue
		}
		ws := dcHelperChainWaits(p, c.name)
		if len(ws) == 0 {
			continue
		}
		out = append(out, dcCandidate{
			site: c.pos,
			line: c.line,
			form: dcHelperWaitForm(ws),
			desc: "在包内助手 " + c.name + " 里（助手由普通语句调用，不是 defer 调用的收尾助手）",
		})
	}
	return out
}

// dcTerminatingBetween 判 go 语句与某个落点之间有没有能让用例提前终止的调用。
//
// 只认 owner == owner 的终止调用：T 绑定归属别的作用域（t.Run 子用例自己
// 声明的 t）时，子用例的 t.Fatal 终止的是子用例，不是本作用域的 goroutine。
func dcTerminatingBetween(scopes []*tsScope, owner *tsScope, gs *ast.GoStmt, site token.Pos) (tsTerm, bool) {
	for _, sc := range scopes {
		for _, t := range sc.terms {
			if t.owner != owner {
				continue
			}
			if t.pos > gs.End() && t.pos < site {
				return t, true
			}
		}
	}
	return tsTerm{}, false
}

// dcHelperChainWaits 取助手链（深度 ≤2）里的等待点。
//
// 深度 ≤2 的理由与兄弟规则同款：收尾助手自己可能再调一个助手；深度 3 以上
// 就跨出「这个用例自己的收尾链」⇒ 判据失去意义。
func dcHelperChainWaits(p *gjPkg, name string) []tsWait {
	var out []tsWait
	seen := map[string]bool{}
	frontier := []string{name}
	for depth := 0; depth < gjMaxHelperDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, cur := range frontier {
			h, ok := p.helpers[cur]
			if !ok || seen[h.name] {
				continue
			}
			seen[h.name] = true
			out = append(out, dcHelperWaits(h)...)
			next = append(next, gjCalleeNames(h.decl)...)
		}
		frontier = next
	}
	return out
}

// dcHelperWaits 取一个助手函数体里的等待点（跳过它自己的 goroutine 体）。
func dcHelperWaits(h *gjFunc) []tsWait {
	var out []tsWait
	goLits := tsGoLits(h.decl)
	add := func(n ast.Node) {
		out = append(out, tsWait{
			node: n,
			line: h.fset.Position(n.Pos()).Line,
			form: tsForm(h.fset, n),
		})
	}
	ast.Inspect(h.decl, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			if goLits[v] {
				return false
			}
			return true
		case *ast.UnaryExpr:
			if v.Op == token.ARROW {
				add(v)
			}
			return true
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Wait" {
				add(v)
			}
			return true
		}
		return true
	})
	return out
}

// dcHelperWaitForm 渲染助手链里第一个等待点的形态（报告用）。
func dcHelperWaitForm(ws []tsWait) string {
	if len(ws) == 0 {
		return "<-?"
	}
	return ws[0].form
}

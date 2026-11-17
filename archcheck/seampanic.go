// seampanic.go —— 形态 C：宿主接缝的 panic 外溢检测（issue #370）。
//
// 判据：policy 登记在 `seams.panic_scan.guarded_calls` 的宿主调用，其调用点必须
// 满足二者之一：
//
//  1. 位于隔离包装器的回调内（subhub.Do / subhub.Invoke / async.GoSafe /
//     async.RunSafe 等）——包装器自己 recover；
//  2. 所在函数带 `defer func(){ ... recover() ... }()` 兜底。
//
// 这正是 #368 的验收形状：同一宿主接缝在批量 / 并发两条路径上的 panic 归属
// 必须相同；逃出心跳协程一次就是全部在途租约失保。
//
// 局限（与形态 A 同一条纪律：说清判不了什么）：
//   - 只按**调用名**精确匹配，不做类型/接口解析；同名但不同接缝的调用会被一并
//     要求隔离（宁可多要求，不漏判）；
//   - recover 只认 deferred 函数字面量内的调用，直接 `defer recover()` 不算；
//   - 包装器清单是声明面：新增隔离原语要先登记，否则会被误报（清单缺失的
//     失败方向是**变红**，不是变绿——比反向漏判安全）。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// seamPanicViolations 形态 C：登记的宿主接缝调用必须 panic 隔离。
func seamPanicViolations(root string, scan SeamPanicScan) ([]Violation, error) {
	files, err := seamScanFiles(root)
	if err != nil {
		return nil, err
	}
	guarded := map[string]bool{}
	for _, name := range scan.GuardedCalls {
		guarded[name] = true
	}
	wrappers := map[string]bool{}
	for _, w := range scan.Wrappers {
		wrappers[w] = true
	}
	var out []Violation
	for _, path := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("panic 外溢扫描解析 %s 失败: %w", path, err)
		}
		rel, err := seamRel(root, path)
		if err != nil {
			return nil, err
		}
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			stack = append(stack, n)
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !guarded[sel.Sel.Name] {
				return true
			}
			if inPanicIsolation(stack, wrappers) || enclosingFuncGuarded(stack) {
				return true
			}
			out = append(out, Violation{
				Rule: seamPanicEscapeRuleID,
				Path: rel,
				Line: fset.Position(call.Pos()).Line,
				Text: fmt.Sprintf("宿主接缝调用 %s 未做 panic 隔离：调用点不在 %s 内，"+
					"所在函数也没有 defer recover —— 宿主 panic 会外溢（#368 的失效形态）",
					sel.Sel.Name, strings.Join(sortedBoolKeys(wrappers), " / ")),
				Kind: "seam_panic_escape",
			})
			return true
		})
	}
	return out, nil
}

// inPanicIsolation 判断调用点是否位于隔离包装器的回调内。
func inPanicIsolation(stack []ast.Node, wrappers map[string]bool) bool {
	for i, n := range stack {
		if _, ok := n.(*ast.FuncLit); !ok || i == 0 {
			continue
		}
		parent, ok := stack[i-1].(*ast.CallExpr)
		if !ok {
			continue
		}
		if key := callKey(parent); key != "" && wrappers[key] {
			return true
		}
	}
	return false
}

// enclosingFuncGuarded 判断调用点所在函数（含匿名函数）是否有 defer recover。
func enclosingFuncGuarded(stack []ast.Node) bool {
	for _, n := range stack {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if hasRecoverGuard(x.Body) {
				return true
			}
		case *ast.FuncLit:
			if hasRecoverGuard(x.Body) {
				return true
			}
		}
	}
	return false
}

// hasRecoverGuard 函数体里是否有 deferred recover 兜底。
//
// 只认 `defer func(...){ ... recover() ... }(...)` 形态：直接 `defer recover()`
// 没有意义（recover 只在 deferred 函数内生效），不当作隔离。
func hasRecoverGuard(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false // 嵌套函数自己的兜底不算本函数的
			}
			return true
		}
		fl, ok := d.Call.Fun.(*ast.FuncLit)
		if !ok {
			return false
		}
		ast.Inspect(fl.Body, func(m ast.Node) bool {
			if _, isLit := m.(*ast.FuncLit); isLit {
				return false
			}
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "recover" {
				found = true
				return false
			}
			return true
		})
		return false
	})
	return found
}

// callKey 调用的稳定键（"pkg.Fn" 形态）。
func callKey(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return exprKey(fn)
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

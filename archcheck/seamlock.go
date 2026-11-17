// seamlock.go —— 形态 A：时钟访问器「在锁内被调」的只读扫描（issue #370）。
//
// 这是**一阶 AST 启发式**，不是判定；默认 severity: warn，不阻断。它的输出
// 是疑似清单，供人工复核。漏判形态（必须与工具一起记住）：
//
//  1. **闭包作用域**：函数字面量按独立判定面处理。外层锁不传递给闭包（正确），
//     但闭包在锁内被调用时，闭包里的接缝调用不会被判出来（漏判）。
//  2. **多出口解锁**：按源码顺序回放 Lock/Unlock。早退路径各自 Unlock 会让
//     后续语句被误认为「已解锁」（漏判；#366 的 quota_store.go:306 正是此形态）。
//  3. **helper 形态**：持锁调本包函数、该函数内调接缝 ⇒ 不做跨函数调用图（漏判）。
//  4. **接口接缝的自实现**：静态类型是接口时无法区分内核自调与宿主实现（不判）。
//  5. **非 mutex 互斥**：channel 信号量 / atomic.Pointer 的 copy-on-write 判不了。
//
// 为什么仍然值得进 make check：它能在**修复后的代码**上把「把 m.now() 挪回锁内」
// 这类回归直接显示出来（warn 级），并且零依赖、零误报优势明显——它只对
// `receiver.now()/clock()` 这类明确形态发声。
package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

type seamEventKind int

const (
	seamLock seamEventKind = iota
	seamUnlock
	seamDeferUnlock // defer Unlock：锁保持到函数返回
	seamAccessor    // 时钟访问器调用
)

type seamEvent struct {
	pos  token.Pos
	kind seamEventKind
	key  string
}

// seamLockHeldViolations 形态 A：时钟访问器在互斥锁区内被调用。
func seamLockHeldViolations(root string, scan SeamLockScan) ([]Violation, error) {
	files, err := seamScanFiles(root)
	if err != nil {
		return nil, err
	}
	accessors := map[string]bool{}
	for _, a := range scan.Accessors {
		accessors[a] = true
	}
	var out []Violation
	for _, path := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("锁内接缝扫描解析 %s 失败: %w", path, err)
		}
		rel, err := seamRel(root, path)
		if err != nil {
			return nil, err
		}
		for _, body := range funcBodies(f) {
			events := collectSeamEvents(body, accessors)
			if len(events) == 0 {
				continue
			}
			sort.SliceStable(events, func(i, j int) bool { return events[i].pos < events[j].pos })
			open := map[string]int{}
			for _, ev := range events {
				switch ev.kind {
				case seamLock:
					open[ev.key]++
				case seamUnlock:
					if open[ev.key] > 0 {
						open[ev.key]--
						if open[ev.key] == 0 {
							delete(open, ev.key)
						}
					}
				case seamDeferUnlock:
					// defer 在函数返回后才执行：锁区保持打开（保守近似）。
				case seamAccessor:
					if len(open) == 0 {
						continue
					}
					out = append(out, Violation{
						Rule: seamLockHeldRuleID,
						Path: rel,
						Line: fset.Position(ev.pos).Line,
						Text: fmt.Sprintf("疑似在锁内调用时钟访问器 %s（持锁: %s）："+
							"宿主回调重入本包会永久死锁。形态 A 只是只读扫描——"+
							"闭包作用域 / 多出口解锁 / helper 跨函数 / channel-atomic 互斥可能漏判",
							ev.key, strings.Join(sortedKeys(open), ",")),
						Kind: "seam_lock_held",
					})
				}
			}
		}
	}
	return out, nil
}

// funcBodies 收集文件里所有函数体（含函数字面量；每个独立判定）。
func funcBodies(f *ast.File) []*ast.BlockStmt {
	var out []*ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Body != nil {
				out = append(out, x.Body)
			}
		case *ast.FuncLit:
			if x.Body != nil {
				out = append(out, x.Body)
			}
		}
		return true
	})
	return out
}

// collectSeamEvents 顺序收集一个函数体里的锁事件与时钟访问器调用。
//
// 函数字面量视为独立函数（不入外层事件流）：外层锁不会泄漏进闭包，
// 闭包内的锁也不会污染外层。代价是「闭包在锁内被调」漏判（文件头已声明）。
func collectSeamEvents(body *ast.BlockStmt, accessors map[string]bool) []seamEvent {
	var events []seamEvent
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			return false // 独立函数体，由 funcBodies 另行扫描
		case *ast.DeferStmt:
			if sel, ok := x.Call.Fun.(*ast.SelectorExpr); ok && isUnlockName(sel.Sel.Name) {
				events = append(events, seamEvent{pos: x.Pos(), kind: seamDeferUnlock, key: exprKey(sel.X)})
				return false
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			key := exprKey(sel.X)
			switch {
			case sel.Sel.Name == "Lock" || sel.Sel.Name == "RLock":
				events = append(events, seamEvent{pos: x.Pos(), kind: seamLock, key: key})
			case isUnlockName(sel.Sel.Name):
				events = append(events, seamEvent{pos: x.Pos(), kind: seamUnlock, key: key})
			case accessors[sel.Sel.Name]:
				events = append(events, seamEvent{pos: x.Pos(), kind: seamAccessor, key: sel.Sel.Name})
			}
		}
		return true
	})
	return events
}

// isUnlockName 解锁方法名（含读写锁）。
func isUnlockName(name string) bool { return name == "Unlock" || name == "RUnlock" }

// exprKey 表达式的稳定文本键（只覆盖锁接收者的常见形态）。
func exprKey(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprKey(x.X) + "." + x.Sel.Name
	case *ast.StarExpr:
		return "*" + exprKey(x.X)
	case *ast.IndexExpr:
		return exprKey(x.X) + "[" + exprKey(x.Index) + "]"
	case *ast.ParenExpr:
		return exprKey(x.X)
	}
	return fmt.Sprintf("%T", expr)
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

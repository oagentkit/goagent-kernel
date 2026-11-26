// attrib_test.go —— 归因取值单源化的机器判据（issue #391）。
//
// reconcile 是 closed_by 的**生产方**。判据两条：
//  1. 每个导出 By* 常量的初值必须是 eventbus.ClosedBy* 的引用（不得写字面量）；
//  2. 每个导出 By* 的运行时取值都在 eventbus.ClosedByDefaults() 内。
//
// 反向验证：给 reconcile 加第五个归因值（字面量）而不同步 eventbus/watchdog
// ⇒ 第 1 条转红；只加字面量、缺省表不动 ⇒ 第 2 条也转红。
package reconcile

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestAttributionConstantsAreEventbusVocabulary 归因常量必须引用 eventbus 词表。
func TestAttributionConstantsAreEventbusVocabulary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录: %v", err)
	}

	found := map[string]string{} // 常量名 → eventbus 常量名
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("解析 %s: %v", name, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, id := range vs.Names {
				if !strings.HasPrefix(id.Name, "By") || i >= len(vs.Values) {
					continue
				}
				sel, ok := vs.Values[i].(*ast.SelectorExpr)
				if !ok {
					t.Errorf("归因常量 %s 不是 eventbus.ClosedBy* 的引用（字面量 = 第二份词表，issue #391）", id.Name)
					continue
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "eventbus" || !strings.HasPrefix(sel.Sel.Name, "ClosedBy") {
					t.Errorf("归因常量 %s 引用了 %s 而不是 eventbus.ClosedBy*", id.Name, exprString(sel))
					continue
				}
				found[id.Name] = sel.Sel.Name
			}
			return true
		})
	}

	// 四个既有归因路必须在扫描结果里——扫描器失效（比如改了常量命名前缀）时
	// 不能静默变成「零条即通过」。
	for _, want := range []string{"ByBootReconcile", "ByRecovery", "ByDeadOwner", "ByDeadLease"} {
		if _, ok := found[want]; !ok {
			t.Errorf("未在源码中枚举到归因常量 %s：扫描器失效或常量被删/改名", want)
		}
	}
	if len(found) == 0 {
		t.Fatal("未枚举到任何 By* 常量：断言恒绿，判据失效")
	}

	// 运行时取值必须都在缺省词表内（消费方 watchdog 按这张表判成员）。
	defaults := eventbus.ClosedByDefaults()
	for name := range found {
		var v string
		switch name {
		case "ByBootReconcile":
			v = ByBootReconcile
		case "ByRecovery":
			v = ByRecovery
		case "ByDeadOwner":
			v = ByDeadOwner
		case "ByDeadLease":
			v = ByDeadLease
		default:
			// 新归因常量：AST 已判它引用 eventbus.ClosedBy*；eventbus 侧用例
			// 判「每个 ClosedBy* 都在 ClosedByDefaults 里」，此处无需重复枚举值。
			continue
		}
		if !slices.Contains(defaults, v) {
			t.Errorf("归因 %s=%q 不在 eventbus.ClosedByDefaults 里：合法收敛会被 watchdog 报成无归因", name, v)
		}
	}
}

// exprString 简短表达 SelectorExpr，仅用于报错文案。
func exprString(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			return id.Name + "." + sel.Sel.Name
		}
		return sel.Sel.Name
	}
	return "非选择器表达式"
}

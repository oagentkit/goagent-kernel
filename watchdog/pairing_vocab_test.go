// pairing_vocab_test.go —— 配对缺口 reason 与 trajectory.Pairing 的单源断言
// （issue #392）。
//
// 同一事实两处实现时，两边都不报错、只是数出来的口径对不上。这里把两个面
// 钉在一起：
//  1. 三条同名 reason 必须**引用** trajectory 的导出常量（值逐字相等）；
//  2. declared_not_executed 必须是 eventbus 登记的别名，规范 token 等于
//     trajectory.PairingMissingResult；
//  3. 生产源码里不得再出现 trajectory 配对取值的字面量（改用字面量 = 第二份
//     词表复活，反向验证必须转红）。
package watchdog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/oagentkit/goagent-kernel/eventbus"
	"github.com/oagentkit/goagent-kernel/trajectory"
)

// TestPairingReasonsAlignWithTrajectory 词表逐值对齐（含别名归一）。
func TestPairingReasonsAlignWithTrajectory(t *testing.T) {
	same := []struct {
		name string
		got  string
		want trajectory.Pairing
	}{
		{"unpaired_result", ReasonUnpairedResult, trajectory.PairingUnpairedResult},
		{"duplicate_result", ReasonDuplicateResult, trajectory.PairingDuplicateResult},
		{"duplicate_call", ReasonDuplicateCall, trajectory.PairingDuplicateCall},
	}
	for _, tc := range same {
		if tc.got != string(tc.want) {
			t.Errorf("watchdog reason %s=%q 与 trajectory.Pairing %q 不一致：同一事实两个 token", tc.name, tc.got, tc.want)
		}
	}

	// declared_not_executed 必须是注册别名，且规范 token 是 missing_result。
	if !eventbus.IsPairingReasonAlias(ReasonDeclaredNotExecuted) {
		t.Errorf("ReasonDeclaredNotExecuted=%q 未在 eventbus 登记为别名：同一事实又被写成独立词表", ReasonDeclaredNotExecuted)
	}
	if got, want := eventbus.CanonicalPairingReason(ReasonDeclaredNotExecuted), string(trajectory.PairingMissingResult); got != want {
		t.Errorf("declared_not_executed 的规范 token = %q，want %q（trajectory.PairingMissingResult）", got, want)
	}

	// 非别名值归一后必须原样返回（防止别名表过度匹配）。
	if got := eventbus.CanonicalPairingReason(ReasonUnpairedResult); got != ReasonUnpairedResult {
		t.Errorf("非别名 reason 被改写: %q → %q", ReasonUnpairedResult, got)
	}

	// 线路值是 Stable 包的兼容承诺：同时钉字面量。生产侧仍只有单源，
	// 但 trajectory 单方面改名（如 unpaired_result → result_without_call）
	// 会在这里转红，逼一次显式的「对外词表变更」决定。
	for got, want := range map[string]string{
		ReasonDeclaredNotExecuted:                                  "declared_not_executed",
		eventbus.CanonicalPairingReason(ReasonDeclaredNotExecuted): "missing_result",
		ReasonUnpairedResult:                                       "unpaired_result",
		ReasonDuplicateResult:                                      "duplicate_result",
		ReasonDuplicateCall:                                        "duplicate_call",
	} {
		if got != want {
			t.Errorf("reason 线路值 %q 与既有承诺 %q 不一致：改对外词表必须显式（Stable 包）", got, want)
		}
	}
}

// TestPairingReasonConstantsAreDerived 常量初值必须是 trajectory/eventbus 的
// 引用，不得是字符串字面量。
//
// 反向验证：把 ReasonUnpairedResult 改回 "unpaired_result" 字面量 ⇒ 转红。
func TestPairingReasonConstantsAreDerived(t *testing.T) {
	want := map[string]string{
		"ReasonDeclaredNotExecuted": "eventbus.PairingReasonDeclaredNotExecuted",
		"ReasonUnpairedResult":      "string(trajectory.PairingUnpairedResult)",
		"ReasonDuplicateResult":     "string(trajectory.PairingDuplicateResult)",
		"ReasonDuplicateCall":       "string(trajectory.PairingDuplicateCall)",
	}
	got := map[string]string{}
	forEachWatchdogSource(t, func(path string, f *ast.File, _ *token.FileSet) {
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, id := range vs.Names {
				if !strings.HasPrefix(id.Name, "Reason") || i >= len(vs.Values) {
					continue
				}
				if _, ok := want[id.Name]; !ok {
					continue
				}
				got[id.Name] = renderExpr(vs.Values[i])
			}
			return true
		})
	})
	for name, expr := range want {
		if got[name] != expr {
			t.Errorf("常量 %s 的初值是 %q，want %q：配对 reason 必须从单源派生", name, got[name], expr)
		}
	}
}

// TestNoPairingLiteralInProductionSource 生产源码不得出现 trajectory 配对取值的
// 字面量——防止 alert 调用点绕过常量直接写字面量。
func TestNoPairingLiteralInProductionSource(t *testing.T) {
	canonical := map[string]bool{
		string(trajectory.PairingPaired):          true,
		string(trajectory.PairingMissingResult):   true,
		string(trajectory.PairingDuplicateResult): true,
		string(trajectory.PairingUnpairedResult):  true,
		string(trajectory.PairingDuplicateCall):   true,
		string(trajectory.PairingNoCallID):        true,
	}
	forEachWatchdogSource(t, func(path string, f *ast.File, fset *token.FileSet) {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if canonical[v] {
				pos := fset.Position(lit.Pos())
				t.Errorf("%s: 配对取值字面量 %q 绕过 trajectory/eventbus 常量（issue #392）", pos, v)
			}
			return true
		})
	})
}

// forEachWatchdogSource 遍历本包非测试源文件（cwd = 包目录）。
func forEachWatchdogSource(t *testing.T, fn func(path string, f *ast.File, fset *token.FileSet)) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录: %v", err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("解析 %s: %v", name, perr)
		}
		fn(name, f, fset)
		seen++
	}
	if seen == 0 {
		t.Fatal("未扫描到任何生产源文件：判据恒绿")
	}
}

// renderExpr 把初值表达式渲染成稳定的简短形式，供断言比较。
func renderExpr(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Value
	case *ast.SelectorExpr:
		return renderExpr(v.X) + "." + v.Sel.Name
	case *ast.Ident:
		return v.Name
	case *ast.CallExpr:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = renderExpr(a)
		}
		return renderExpr(v.Fun) + "(" + strings.Join(args, ", ") + ")"
	default:
		return "?"
	}
}

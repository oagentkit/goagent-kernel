// state_terminal_test.go —— 状态常量注释与 Terminal() 必须说同一件事（issue #375）。
//
// StateFailed 的注释写「终态」，同包 Terminal() 却明写「只有 done 是终态」，
// 两处相距 7 行。维护者先读到的是常量注释（文档第一入口），据此写出
// 「failed 不可重做」的逻辑，与包契约相反。
//
// 判据不是「注释里有某个词」这么宽：凡注释声明「终态」的状态，Terminal()
// 必须为真；声明「非终态」的必须为假。两条互斥，改一边就会红。

package taskboard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// stateConstantValue 取 ValueSpec 第 i 个名字对应的字符串常量值。
func stateConstantValue(vs *ast.ValueSpec, i int) (string, bool) {
	var expr ast.Expr
	switch {
	case len(vs.Values) == len(vs.Names):
		expr = vs.Values[i]
	case len(vs.Values) == 1:
		expr = vs.Values[0]
	default:
		return "", false
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// TestStateConstantCommentsMatchTerminal 常量注释里的终态声明必须与
// Terminal() 一致：声明「非终态」的不得为终态，声明「终态」的必须为终态。
func TestStateConstantCommentsMatchTerminal(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "taskboard.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 taskboard.go 失败: %v", err)
	}

	seen := map[State]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "State") || vs.Doc == nil {
					continue
				}
				value, ok := stateConstantValue(vs, i)
				if !ok {
					continue
				}
				doc := vs.Doc.Text()
				state := State(value)
				seen[state] = true
				switch {
				case strings.Contains(doc, "非终态"):
					if state.Terminal() {
						t.Errorf("%s 的注释声明「非终态」，Terminal() 却为 true", name.Name)
					}
				case strings.Contains(doc, "终态"):
					if !state.Terminal() {
						t.Errorf("%s 的注释声明「终态」，Terminal() 却为 false——注释比实现宽", name.Name)
					}
				}
			}
		}
	}
	if len(seen) < 4 {
		t.Fatalf("只解析到 %d 个 State 常量的注释（应至少 4 个）——判据面可能失明", len(seen))
	}
}

// TestOnlyDoneIsTerminal 状态词表的终态结论必须表驱动可判：只有 done 是
// 终态，failed 必须可重新认领。
func TestOnlyDoneIsTerminal(t *testing.T) {
	states := []State{StateOpen, StateClaimed, StateDone, StateFailed}
	for _, s := range states {
		if !s.Valid() {
			t.Errorf("%q 应在状态词表内", s)
		}
		want := s == StateDone
		if got := s.Terminal(); got != want {
			t.Errorf("State(%q).Terminal()=%v，want %v", s, got, want)
		}
	}
	if StateFailed.Terminal() {
		t.Fatal("failed 是终态意味着失败的任务不能重做——与包契约相反")
	}
}

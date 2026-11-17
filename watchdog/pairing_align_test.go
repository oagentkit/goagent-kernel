package watchdog

// pairing_align_test.go —— 配对缺口 reason 词表与 trajectory 的对齐（issue #392）。
//
// 「工具调用配对缺口」是同一事实：投影面（trajectory 的 Pairing）与监视面
// （本包的告警 reason）此前各写一份——三个 token 逐字同名、第四个是同一事实
// 的两个 token（declared_not_executed vs missing_result）。宿主按投影面的词表
// 筛本包的告警会漏掉 declared_not_executed 这一类；两边数出来的口径也对不上。
//
// 本文件把三条判据钉住：值逐字对应、别名是**注册**的、词表集合恒等。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/trajectory"
)

// TestPairingReasonsAlignWithTrajectory 本包的四个 reason 值与 trajectory 的
// Pairing 词表逐字对应；declared_not_executed 必须是 PairingMissingResult 的
// **注册别名**，不是独立字面量。
func TestPairingReasonsAlignWithTrajectory(t *testing.T) {
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"ReasonUnpairedResult", ReasonUnpairedResult, string(trajectory.PairingUnpairedResult)},
		{"ReasonDuplicateResult", ReasonDuplicateResult, string(trajectory.PairingDuplicateResult)},
		{"ReasonDuplicateCall", ReasonDuplicateCall, string(trajectory.PairingDuplicateCall)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q，应与 trajectory 词表逐字相同（%q）：两份枚举各自长大，看板就拼不起来了",
				c.name, c.got, c.want)
		}
	}

	canonical, ok := eventbus.PairingReasonAliases[ReasonDeclaredNotExecuted]
	if !ok {
		t.Fatalf("declared_not_executed 必须在 eventbus.PairingReasonAliases 里注册为别名（issue #392）")
	}
	if canonical != string(trajectory.PairingMissingResult) {
		t.Errorf("别名登记的规范 token = %q，应为 trajectory.PairingMissingResult = %q",
			canonical, trajectory.PairingMissingResult)
	}
	for _, g := range trajectory.PairingGapValues() {
		if ReasonDeclaredNotExecuted == g {
			t.Errorf("declared_not_executed 与规范 token %q 逐字相同：那就不是别名，是改了名", g)
		}
	}
}

// TestPairingReasonVocabularyCoversTrajectoryGaps 归一别名之后，本包的 reason
// 词表与 trajectory 的缺口词表**恒等**：投影面加了第五类缺口而监视面没跟，
// 或监视面多出一个不在册的 token，都当场红。
func TestPairingReasonVocabularyCoversTrajectoryGaps(t *testing.T) {
	got := map[string]bool{}
	for _, r := range []string{
		ReasonDeclaredNotExecuted, ReasonUnpairedResult,
		ReasonDuplicateResult, ReasonDuplicateCall,
	} {
		canonical, ok := eventbus.PairingReasonAliases[r]
		if !ok {
			canonical = r
		}
		got[canonical] = true
	}
	want := map[string]bool{}
	for _, g := range trajectory.PairingGapValues() {
		want[g] = true
	}
	for g := range want {
		if !got[g] {
			t.Errorf("trajectory 的缺口 %q 不在本包的 reason 词表里：投影面加了缺口、监视面没跟", g)
		}
	}
	for g := range got {
		if !want[g] {
			t.Errorf("本包的 reason %q 不在 trajectory 的缺口词表里", g)
		}
	}
}

// TestTrajectoryPairingGapsAreSingleSourced 投影面的缺口词表必须与 eventbus 单源
// 恒等——本包的 reason 常量引用的是投影面的常量，投影面自成一份实现时两侧就各说
// 各话（且没有任何用例会红）。
//
// 反向验证：把 trajectory 的 PairingUnpairedResult 改成一个独立字面量
// （=「改名而不同步」）⇒ 本用例红。
func TestTrajectoryPairingGapsAreSingleSourced(t *testing.T) {
	got := trajectory.PairingGapValues()
	want := eventbus.AllPairingGaps()
	if len(got) != len(want) {
		t.Fatalf("投影面缺口词表基数 %d 与 eventbus 单源 %d 不一致：%v vs %v", len(got), len(want), got, want)
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("eventbus 单源的缺口 %q 不在投影面词表里：投影面与监视面会各说各话", w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("投影面缺口词表里的 %q 不在 eventbus 单源里：投影面自成了一份实现", g)
		}
	}
}

// TestPairingReasonsAreDerivedFromSharedSource reason 常量不得是独立字面量：
// 「同一事实两份实现」正是 issue #392 的成因——字面量一改两边就漂，且没有用例
// 会红。判据走 go/ast（常量声明的形式是结构问题，逐行正则判不出）。
//
// 反向验证：把某个 reason 常量改回字面量 ⇒ 本用例红。
func TestPairingReasonsAreDerivedFromSharedSource(t *testing.T) {
	consts := map[string]bool{
		"ReasonDeclaredNotExecuted": true,
		"ReasonUnpairedResult":      true,
		"ReasonDuplicateResult":     true,
		"ReasonDuplicateCall":       true,
	}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	found := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", f, err)
		}
		for _, decl := range file.Decls {
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
					if !consts[name.Name] {
						continue
					}
					if i >= len(vs.Values) {
						t.Errorf("%s 没有值表达式（%s）", name.Name, fset.Position(name.Pos()))
						continue
					}
					found[name.Name] = true
					val := vs.Values[i]
					if lit, ok := val.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						t.Errorf("%s 是独立字面量 %s：它必须是上游词表的引用，不是第二份实现（issue #392）",
							name.Name, strconv.Quote(lit.Value))
						continue
					}
					if !referencesSharedVocabulary(val) {
						t.Errorf("%s 的值既不是字面量也不引用 trajectory/eventbus 的词表（%s）：它必须是**上游词表的引用**",
							name.Name, fset.Position(name.Pos()))
					}
				}
			}
		}
	}
	for name := range consts {
		if !found[name] {
			t.Errorf("生产代码里找不到常量 %s 的声明（被挪走或删了？）", name)
		}
	}
}

// referencesSharedVocabulary 值表达式是否引用了 trajectory / eventbus 的词表。
func referencesSharedVocabulary(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "trajectory" || id.Name == "eventbus") {
			found = true
		}
		return true
	})
	return found
}

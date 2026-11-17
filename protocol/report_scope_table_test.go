// report_scope_table_test.go —— scope 词表单一真相断言（issue #382）。
//
// # 这一组钉住什么
//
// scope 词表此前在同一个包内有**三份实现**：词表本体（scopeAll）、归一面的
// case 清单（case "in", "out"）、判据面的字面量（r.Scope == "out"）。三处必须
// 同改而**没有任何判据会红**：
//
//   - 词表扩展漏改归一面 ⇒ 该值在判定面合法、在归一面被判成词表外 ⇒ 一整个
//     词表内的取值被整份报告 critical 拒掉（更严）；
//   - 词表扩展漏改红线 ⇒ 「转出」路由信号静默丢失（更松）；
//   - 宿主手抄 "in"/"out" ⇒ 内核改词值时宿主编译期不报错、运行期静默失效。
//
// 现在：三处同源（词表 → 谓词 → 归一面/判据面），且 scope 有了导出常量。

package protocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// TestScopeConstantsMatchVocabulary 导出常量必须与词表一致。
//
// 宿主按常量构造取值，不按字面量——内核改词值时宿主编译期就报错。
func TestScopeConstantsMatchVocabulary(t *testing.T) {
	if ScopeIn != "in" || ScopeOut != "out" {
		t.Fatalf("scope 常量取值漂移: ScopeIn=%q ScopeOut=%q", ScopeIn, ScopeOut)
	}
	for _, c := range []string{ScopeIn, ScopeOut} {
		if !scopeAll[c] {
			t.Fatalf("常量 %q 不在 scope 词表内（常量与词表必须同源）", c)
		}
		if !slices.Contains(AllScopes(), c) {
			t.Fatalf("常量 %q 未出现在枚举出口里", c)
		}
	}
}

// TestNormalizeScopeAcceptsWholeVocabulary 词表内每个取值的大小写/空白变体都
// 必须被归一为该取值——否则词表内的值会被判成词表外，整份报告被拒。
//
// 同时钉住词表外的取值**保持原值**（第三轮 #321 的修法不被回退）。
func TestNormalizeScopeAcceptsWholeVocabulary(t *testing.T) {
	for legal := range scopeAll {
		for _, variant := range writingVariants(legal) {
			m := map[string]any{}
			normalizeScope(m, "scope", variant)
			if got, _ := m["scope"].(string); got != legal {
				t.Fatalf("scope 变体 %q 未归一为 %q（实得 %q）", variant, legal, got)
			}
		}
	}
	for _, outside := range []string{"external", "out_of_scope", "转出"} {
		m := map[string]any{}
		normalizeScope(m, "scope", outside)
		if got, _ := m["scope"].(string); got != outside {
			t.Fatalf("词表外 scope %q 应保持原值（实得 %q）", outside, got)
		}
	}
}

// TestNormalizeScopeFollowsVocabularyTable 归一面必须**跟着词表走**：往
// scopeAll 里加一个取值，归一面立刻认它。
//
// 这条把「归一面不得自带第二份清单」从注释变成判据：归一面若回到
// case "in", "out" 的写法，这里立刻转红（红点直指「该值在判定面词表内却会被
// 判成词表外」）。
func TestNormalizeScopeFollowsVocabularyTable(t *testing.T) {
	const probe = "__probe_scope__"
	scopeAll[probe] = true
	defer delete(scopeAll, probe)

	m := map[string]any{}
	normalizeScope(m, "scope", strings.ToUpper(probe))
	if got, _ := m["scope"].(string); got != probe {
		t.Fatalf("词表新增取值未被归一面认（实得 %q）：归一面自带了第二份清单", got)
	}
	if !IsScopeValid(probe) {
		t.Fatal("词表新增取值未被谓词认：谓词与词表不同源")
	}
}

// TestScopeFacesUseConstantsNotLiterals 归一面与判据面不得再出现 scope 的
// 字面量清单——它们必须引用常量/词表（结构性判定走 go/ast）。
func TestScopeFacesUseConstantsNotLiterals(t *testing.T) {
	for _, file := range []string{"report_validate.go", "report_normalize.go"} {
		for _, lit := range stringLiterals(t, file) {
			if lit == ScopeIn || lit == ScopeOut {
				t.Errorf("%s: 不得出现 scope 字面量 %q（必须引用 ScopeIn/ScopeOut 常量）", file, lit)
			}
		}
	}
}

// stringLiterals 列出文件里的全部字符串字面量（走 go/ast，不走逐行正则）。
func stringLiterals(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, readSource(t, file), 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			out = append(out, strings.Trim(lit.Value, "\x22"))
		}
		return true
	})
	return out
}

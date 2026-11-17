// statusclass_test.go —— checkpoint 的三态分类必须走**统一判定面**（issue #363）。
//
// 此前 checkpoint 与 controlplane 各复制了一份「什么算词表外」的三态边界，
// 两处注释都声明「不在本包另立一份」——却立了两份。修法是两边都调
// contract.ClassifyStatus。
//
// # 为什么用 go/ast 扫函数体
//
// 「这个函数体里有没有另列一份词表」是**结构性**问题：逐行正则分不出
// switch case 里的枚举与普通表达式。本仓纪律要求结构性判定走语法树。
package checkpoint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

// TestClassifyStatusMatchesContract 分类结果必须与 contract.ClassifyStatus
// 逐字一致（全词表 + 词表外取值）。
//
// 反向验证：在本包的分类器里改一个状态名（只改一处）⇒ 本条转红。
func TestClassifyStatusMatchesContract(t *testing.T) {
	values := append([]string{}, contract.AllStatuses()...)
	values = append(values, "", "aborted", "degraded", "weird_state", "PAUSED")
	for _, v := range values {
		want := contract.ClassifyStatus(v)
		if got := classifyStatus(v); got != want {
			t.Fatalf("状态 %q 的分类不一致：checkpoint=%v，contract=%v", v, got, want)
		}
	}
}

// TestClassifyStatusHasNoSecondVocabulary 分类器函数体里只允许出现
// contract.ClassifyStatus——不得另列 contract.Status* 清单。
//
// 反向验证：把分类器改回自己列举 ⇒ 本条转红。
func TestClassifyStatusHasNoSecondVocabulary(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "resume.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 resume.go 失败: %v", err)
	}
	for _, decl := range parsed.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "classifyStatus" {
			continue
		}
		var got []string
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "contract" {
				got = append(got, "contract."+sel.Sel.Name)
			}
			return true
		})
		if len(got) != 1 || got[0] != "contract.ClassifyStatus" {
			t.Fatalf("classifyStatus 必须只调用统一判定面 contract.ClassifyStatus（实得 %v）——"+
				"分类器里另列词表就是「立了第二份」的复发（issue #363）", got)
		}
		return
	}
	t.Fatal("resume.go 里找不到 classifyStatus")
}

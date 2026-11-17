// statusclass_test.go —— 三态分类的**单一判定面**（issue #363）。
//
// checkpoint 与 controlplane 各自复制了一份「什么算词表外」的三态边界，
// 两处注释都写着「不在本包另立一份」——但立的是两份。修法是两边都调
// contract.ClassifyStatus（contract 侧的单一事实源）。
//
// # 为什么用 go/ast 扫函数体
//
// 「这个函数体里有没有另列一份词表」是**结构性**问题：逐行正则分不出
// switch case 里的枚举与普通表达式，也分不出「调用统一判定面」与
// 「自己列一张表」。本仓纪律明确要求结构性判定走语法树。
package controlplane

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

// pkgDir 本包源码目录（测试的工作目录即包目录，这里取调用方文件路径更稳）。
func pkgDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("取不到调用方文件路径")
	}
	return filepath.Dir(file)
}

// classifierSelectors 取某个分类器函数体里出现的 contract.* 选择器。
func classifierSelectors(t *testing.T, file, fn string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	for _, decl := range parsed.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		var out []string
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "contract" {
				out = append(out, "contract."+sel.Sel.Name)
			}
			return true
		})
		return out
	}
	t.Fatalf("%s 里找不到函数 %s", file, fn)
	return nil
}

// TestStatusClassifiersShareOneJudgmentSurface 两处三态分类必须调同一个
// 判定面：函数体里只允许出现 contract.ClassifyStatus。
//
// 这条同时钉住「逐字一致」：两个分类器都是 contract.ClassifyStatus 的
// 转发 ⇒ 对**任何**输入都给出同一个结果（不止是词表内那几个取值）。
//
// 反向验证：把任一分类器改回自己列举 contract.Status* ⇒ 本条转红。
func TestStatusClassifiersShareOneJudgmentSurface(t *testing.T) {
	dir := pkgDir(t)
	cases := []struct {
		file string
		fn   string
	}{
		{filepath.Join(dir, "storeplane.go"), "classifyRunStatus"},
		{filepath.Join(dir, "..", "checkpoint", "resume.go"), "classifyStatus"},
	}
	for _, c := range cases {
		got := classifierSelectors(t, c.file, c.fn)
		if len(got) != 1 || got[0] != "contract.ClassifyStatus" {
			t.Errorf("%s 的 %s 必须只调用统一判定面 contract.ClassifyStatus（实得 %v）——"+
				"分类器里另列词表就是「立了第二份」的复发（issue #363）",
				c.file, c.fn, got)
		}
	}
}

// TestClassifyRunStatusMatchesContract controlplane 的分类结果必须与
// contract.ClassifyStatus 逐字一致（全词表 + 词表外取值）。
//
// 反向验证：在 checkpoint 的分类器里改一个状态名（只改一处）⇒ 两边对
// 同一个取值给出相反结论 ⇒ 本条转红（checkpoint 侧另有同款用例）。
func TestClassifyRunStatusMatchesContract(t *testing.T) {
	values := append([]string{}, contract.AllStatuses()...)
	values = append(values, "", "aborted", "degraded", "weird_state", "PAUSED")
	for _, v := range values {
		want := contract.ClassifyStatus(v)
		if got := classifyRunStatus(v); got != want {
			t.Fatalf("状态 %q 的分类不一致：controlplane=%v，contract=%v", v, got, want)
		}
	}
}

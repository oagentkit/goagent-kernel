package statemachine

// alias_test.go —— 与 contract 的重叠常量必须是**别名**而非第二份字面量
// （issue #357）。

import (
	"go/ast"
	"go/token"
	"slices"
	"testing"

	"github.com/yi-nology/goagent-kernel/internal/gosrc"

	"github.com/yi-nology/goagent-kernel/contract"
)

// overlapConstNames 两包共有的常量名（6 个）。
var overlapConstNames = []string{
	"StatusCreated",
	"StatusRunning",
	"StatusWaitingApproval",
	"StatusSucceeded",
	"StatusFailed",
	"StatusCancelled",
}

// TestOverlappingConstantValuesMatch 跨包**同值**断言：两包共有的每个取值
// 必须逐字相等（issue #357）。
//
// 这是「宿主从两包各引一个同名字符串常量、写进同一张表的同一列」那条
// 触发路径的守卫：两包各按自己的表判同一个值，会给出不同结论。
func TestOverlappingConstantValuesMatch(t *testing.T) {
	cases := []struct {
		name          string
		session, step string
	}{
		{"created", contract.StatusCreated, StatusCreated},
		{"running", contract.StatusRunning, StatusRunning},
		{"waiting_approval", contract.StatusWaitingApproval, StatusWaitingApproval},
		{"succeeded", contract.StatusSucceeded, StatusSucceeded},
		{"failed", contract.StatusFailed, StatusFailed},
		{"cancelled", contract.StatusCancelled, StatusCancelled},
	}
	for _, c := range cases {
		if c.session != c.step {
			t.Fatalf("%s 在两包的取值不同：contract=%q，statemachine=%q", c.name, c.session, c.step)
		}
	}
}

// TestOverlappingConstantsAreAliases 别名必须**真的同源**：常量声明的值
// 必须是 contract 的引用，而不是第二份字面量（issue #357）。
//
// 为什么走 go/ast：常量是不是别名是**结构性**问题（值是选择器还是字面量），
// 逐行正则分不出 `StatusCreated = contract.StatusCreated` 与
// `StatusCreated = "created"` ——而这正是本条要钉住的边界：两份字面量
// 在值相同时与别名**逐字同形**，只有语法结构能分辨。
func TestOverlappingConstantsAreAliases(t *testing.T) {
	sources := constSources(t)
	for _, name := range overlapConstNames {
		want := "contract." + name
		if got := sources[name]; got != want {
			t.Fatalf("常量 %s 的定义来源是 %q，必须是别名 %q——"+
				"「一处定义 + 一处别名」是本仓纪律，两份字面量迟早漂移", name, got, want)
		}
	}
}

// TestOverlapSetDoesNotDrift 两包**共有**取值的个数必须恰好 6。
//
// 新增取值必须**同时**更新两侧并同步这个数字，否则转漂移（issue #357）。
// 共有集合是从两包各自的枚举出口**算出来**的，不是抄进用例的常量表。
func TestOverlapSetDoesNotDrift(t *testing.T) {
	session := contract.AllStatuses()
	step := AllStepStatuses()
	var overlap []string
	for _, s := range session {
		if slices.Contains(step, s) {
			overlap = append(overlap, s)
		}
	}
	if len(overlap) != 6 {
		t.Fatalf("两包共有取值的个数 = %d（%v），契约要求恰好 6——"+
			"新增取值必须同时更新两侧并同步这个数字", len(overlap), overlap)
	}
	want := []string{"cancelled", "created", "failed", "running", "succeeded", "waiting_approval"}
	if !slices.Equal(overlap, want) {
		t.Fatalf("共有取值集合漂移：%v，期望 %v", overlap, want)
	}
	// 各包**独有**的取值是有意的，不合并成一张大表：
	// 会话侧独有 timed_out，环节侧独有 approved / rejected / expired /
	// interrupted / rolled_back / skipped。
	sessionOnly := []string{contract.StatusTimedOut}
	stepOnly := []string{
		string(StatusApproved), string(StatusRejected), string(StatusExpired),
		string(StatusInterrupted), string(StatusRolledBack), string(StatusSkipped),
	}
	for _, s := range sessionOnly {
		if slices.Contains(step, s) {
			t.Fatalf("%q 是会话侧独有的取值，不得混进环节词表", s)
		}
	}
	for _, s := range stepOnly {
		if slices.Contains(session, s) {
			t.Fatalf("%q 是环节侧独有的取值，不得混进会话词表", s)
		}
	}
}

// constSources 取本包常量声明的**定义来源**（go/ast）。
//
// 值的形态："contract.StatusCreated" = 别名；"\"created\"" = 第二份字面量。
func constSources(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := gosrc.ParsePackage(fset, ".", true)
	if err != nil {
		t.Fatalf("解析本包源码失败: %v", err)
	}
	out := map[string]string{}
	for _, file := range files {
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
					if i >= len(vs.Values) {
						continue
					}
					out[name.Name] = sourceOf(vs.Values[i])
				}
			}
		}
	}
	return out
}

// sourceOf 把常量定义表达式压成可比对的短形态。
func sourceOf(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		if x, ok := v.X.(*ast.Ident); ok {
			return x.Name + "." + v.Sel.Name
		}
	case *ast.BasicLit:
		return v.Value
	case *ast.Ident:
		return v.Name
	}
	return "?"
}

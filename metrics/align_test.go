// align_test.go —— 跨包标签取值域的恒等断言（issue #395）。
//
// 同族维度（ExecKind ↔ trajectory.Kind、RetryPath ↔ retryplan.Path）早已钉住，
// 唯独 ExecStatus ↔ trajectory.SpanStatus 和 LeaseOutcome ↔ sideeffect.Outcome
// 是手抄且无断言：上游加一个值，metrics 侧要到「标签越界」报错时才知道。
//
// 本文件补两条恒等断言 + 一条「Why 写了同源就必须有对齐用例」的元断言。
package metrics

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/oagentkit/goagent-kernel/sideeffect"
)

// TestExecStatusAlignsWithTrajectorySpanStatus 执行状态取值域与
// trajectory.SpanStatus 恒等。
//
// 反向验证：给 trajectory 加第四个 SpanStatus（如 cancelled）⇒ 转红；
// 把 metrics.ExecStatusError 改成 "failure" ⇒ 转红。
func TestExecStatusAlignsWithTrajectorySpanStatus(t *testing.T) {
	dim := labelDim(t, NameExecDuration, LabelExecStatus)
	want := spanStatusValues(t)

	if dim.Card() != len(want) {
		t.Fatalf("%s 取值数 %d 与 trajectory.SpanStatus 取值数 %d 不一致：两边必须同步加", LabelExecStatus, dim.Card(), len(want))
	}
	for _, w := range want {
		if !slices.Contains(dim.Values, w) {
			t.Errorf("trajectory.SpanStatus %q 不在 %s 的取值域里：trajectory 加了状态、metrics 没跟上", w, LabelExecStatus)
		}
	}
	for _, got := range dim.Values {
		if !slices.Contains(want, got) {
			t.Errorf("%s 的取值 %q 不在 trajectory.SpanStatus 里：metrics 自长了状态", LabelExecStatus, got)
		}
	}
}

// TestLeaseOutcomeAlignsWithSideEffectOutcome 租约拒绝形态与
// sideeffect.Outcome 的 String() 恒等（int 枚举，比对 String 而非常量）。
//
// 反向验证：sideeffect 把 OutcomeContended 的 String() 改成 "busy" ⇒ 转红；
// 从 metrics 的取值域删掉 uncertain ⇒ 转红。
func TestLeaseOutcomeAlignsWithSideEffectOutcome(t *testing.T) {
	dim := labelDim(t, NameLeaseContended, LabelLeaseOutcome)
	want := []string{
		sideeffect.OutcomeContended.String(),
		sideeffect.OutcomeUncertain.String(),
	}
	if dim.Card() != len(want) {
		t.Fatalf("%s 取值数 %d 与 sideeffect 拒绝形态数 %d 不一致", LabelLeaseOutcome, dim.Card(), len(want))
	}
	for _, w := range want {
		if !slices.Contains(dim.Values, w) {
			t.Errorf("sideeffect.Outcome %q 不在 %s 的取值域里：上游改了 String()、metrics 没跟上", w, LabelLeaseOutcome)
		}
	}
	for _, got := range dim.Values {
		if !slices.Contains(want, got) {
			t.Errorf("%s 的取值 %q 不对应任何 sideeffect 拒绝形态", LabelLeaseOutcome, got)
		}
	}
}

// alignmentTests Why 声明了跨包对齐的标签维度 → 对应的对齐用例函数名。
//
// 新增此类维度必须同时登记用例名：元断言会检查「用例真的存在」。
var alignmentTests = map[string]string{
	LabelExecKind:     "TestExecKindAlignsWithTrajectory",
	LabelExecStatus:   "TestExecStatusAlignsWithTrajectorySpanStatus",
	LabelRetryPath:    "TestRetryPathAlignsWithRetryPlan",
	LabelLeaseOutcome: "TestLeaseOutcomeAlignsWithSideEffectOutcome",
}

// TestSameSourceWhyHasAlignmentTest 凡 Why 里写了「同源 / 逐字对齐 / 须两边同步 /
// 对齐」的维度，都必须有登记的对齐用例，且该用例函数真实存在。
//
// 反向验证：删掉 TestExecStatusAlignsWithTrajectorySpanStatus ⇒ 转红；
// 新增一个写「同源」的维度但不登记用例 ⇒ 转红。
func TestSameSourceWhyHasAlignmentTest(t *testing.T) {
	tests := testFuncNames(t)
	covered := map[string]bool{}
	for _, m := range Specs() {
		for _, d := range m.Labels {
			if !claimsAlignment(d.Why) {
				continue
			}
			name, ok := alignmentTests[d.Key]
			if !ok {
				t.Errorf("指标 %s 的维度 %s 在 Why 里声明了跨包对齐，但 alignmentTests 没有对应用例", m.Name, d.Key)
				continue
			}
			covered[d.Key] = true
			if !tests[name] {
				t.Errorf("维度 %s 登记的对齐用例 %s 不存在（用例被删/改名 ⇒ 断言只剩声明）", d.Key, name)
			}
		}
	}
	// 反向：登记表里的维度必须真的出现在 Specs 中，防止删除维度后映射变孤儿。
	seen := map[string]bool{}
	for _, m := range Specs() {
		for _, d := range m.Labels {
			seen[d.Key] = true
		}
	}
	for key := range alignmentTests {
		if !seen[key] {
			t.Errorf("alignmentTests 登记了不存在的维度 %s", key)
		}
		if covered[key] != seen[key] {
			t.Errorf("维度 %s 的对齐用例登记与实际声明不一致", key)
		}
	}
}

// claimsAlignment Why 是否声明了跨包对齐。
func claimsAlignment(why string) bool {
	for _, kw := range []string{"同源", "逐字对齐", "须两边同步", "对齐"} {
		if strings.Contains(why, kw) {
			return true
		}
	}
	return false
}

// labelDim 取指定指标 + 标签键的维度（缺失即失败）。
func labelDim(t *testing.T, metric, key string) LabelDim {
	t.Helper()
	spec, ok := SpecOf(metric)
	if !ok {
		t.Fatalf("指标 %s 未登记", metric)
	}
	for _, d := range spec.Labels {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("指标 %s 没有 %s 维度", metric, key)
	return LabelDim{}
}

// spanStatusValues 从 trajectory/otel.go 的常量声明里枚举全部 SpanStatus 取值
// ——显式列三个常量无法发现「上游加了第四个」。
func spanStatusValues(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "trajectory", "otel.go"), nil, 0)
	if err != nil {
		t.Fatalf("解析 trajectory/otel.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		id, ok := vs.Type.(*ast.Ident)
		if !ok || id.Name != "SpanStatus" || len(vs.Values) != len(vs.Names) {
			return true
		}
		for _, v := range vs.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				t.Fatalf("SpanStatus 字面量无法解析: %v", uerr)
			}
			out = append(out, s)
		}
		return true
	})
	if len(out) == 0 {
		t.Fatal("未枚举到任何 SpanStatus 常量：判据恒绿")
	}
	return out
}

// testFuncNames 收集本包测试函数名（用例存在性由 AST 判，不靠字符串模板）。
func testFuncNames(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录: %v", err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("解析 %s: %v", name, perr)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				out[fn.Name.Name] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("未收集到任何测试函数：元断言恒绿")
	}
	return out
}

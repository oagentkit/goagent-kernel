// errorkind_test.go —— 失败类别词表的**单一真相**判据（issue #365 / #364）。
//
// 这组用例钉的是三条：
//
//  1. 已是 toolfail 分类的值必须**幂等透传**（#364）：宿主绑定抛出的规范失败
//     带的分类本身就是处置口径，再走一遍映射表会把它折回 unknown——可重试的
//     transient / rate_limited / self_reported 被降级成不可重试。
//  2. 词表里的每个取值都必须在口径表里有**明确归属**（分类 + 建议），
//     且口径表里不能有词表之外的行（#365）：新增类别而不同步 ⇒ 断言转红。
//  3. 本包声明的每个 ErrorKind* 常量都必须进词表（#365）：常量与词表是同一份。
package ptc

import (
	"context"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/internal/gosrc"

	"github.com/oagentkit/guardx/sandbox"
	"github.com/oagentkit/guardx/toolfail"
)

// TestKindForErrorKindPassesThroughToolfailClasses 已是 toolfail 分类的值
// 必须原样透传（幂等）。
//
// 表驱动逐条独立报告：一张表 + 一个共享 Fatalf 只会让第一条跑，后面那些
// 形态**从未被执行过**，却让用例看起来钉住了全部形态。
func TestKindForErrorKindPassesThroughToolfailClasses(t *testing.T) {
	for _, c := range toolfail.AllClasses() {
		t.Run(string(c), func(t *testing.T) {
			got := kindForErrorKind(string(c))
			if got != string(c) {
				t.Fatalf("kindForErrorKind(%q) = %q，应原样透传 %q（已是处置口径，再映射一次就折回 unknown）",
					c, got, c)
			}
		})
	}
}

// TestKindForErrorKindIsIdempotent 映射对处置口径必须是幂等的：连映射两次
// 与映射一次同值（否则「已经分类过」的值会被二次降级）。
func TestKindForErrorKindIsIdempotent(t *testing.T) {
	for _, c := range toolfail.AllClasses() {
		t.Run(string(c), func(t *testing.T) {
			once := kindForErrorKind(string(c))
			twice := kindForErrorKind(once)
			if once != twice {
				t.Fatalf("kindForErrorKind 不幂等：%q → %q → %q", c, once, twice)
			}
		})
	}
}

// TestLocalVocabularyMappingUnchanged 本地诊断词表那条路保持不变：
// syntax / deadline 等仍映射到 permanent / transient。
func TestLocalVocabularyMappingUnchanged(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{ErrorKindSyntax, string(toolfail.ClassPermanent)},
		{ErrorKindBadResult, string(toolfail.ClassPermanent)},
		{ErrorKindRuntime, string(toolfail.ClassTransient)},
		{ErrorKindPending, string(toolfail.ClassPermanent)},
		{ErrorKindBindingCollision, string(toolfail.ClassPermanent)},
		{ErrorKindBindingSignature, string(toolfail.ClassPermanent)},
		{ErrorKindDeadline, string(toolfail.ClassTransient)},
		{ErrorKindBudgetCalls, string(toolfail.ClassRateLimited)},
		{ErrorKindBudgetBytes, string(toolfail.ClassRateLimited)},
		{ErrorKindHostPanic, string(toolfail.ClassPermanent)},
		{ErrorKindStageDenied, string(toolfail.ClassPermissionDenied)},
		{ErrorKindStageFailed, string(toolfail.ClassUnknown)},
		{ErrorKindPermissionDenied, string(toolfail.ClassPermissionDenied)},
		{ErrorKindUnmanaged, string(toolfail.ClassPermanent)},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if got := kindForErrorKind(tc.kind); got != tc.want {
				t.Fatalf("kindForErrorKind(%q) = %q，应为 %q", tc.kind, got, tc.want)
			}
		})
	}
}

// TestToolfailClassificationSurvivesHostBinding 端到端：宿主绑定抛
// toolfail 的 transient 失败 ⇒ 分类一路透传到结果与登记簿的失败分类，
// 且「可重试」这条处置信息不丢。
//
// 这是 #364 的核心形态：分类在最后一跳被丢弃成 unknown ⇒ 编排层的退避
// 重试对 transient 类失败不再触发。
func TestToolfailClassificationSurvivesHostBinding(t *testing.T) {
	bind := Binding{Name: "alpha", Call: func(context.Context, string) (string, error) {
		return "", toolfail.New(toolfail.ClassTransient, "probe", "注入的瞬时失败")
	}}
	res := Run(context.Background(), `return host.alpha("x")`, []Binding{bind},
		Options{Stages: allowStages()})
	if res.OK {
		t.Fatal("绑定抛错时执行必须失败")
	}
	if res.ErrorKind != string(toolfail.ClassTransient) {
		t.Fatalf("ErrorKind 应原样透传 %q，实得 %q", toolfail.ClassTransient, res.ErrorKind)
	}
	if got := kindForErrorKind(res.ErrorKind); got != string(toolfail.ClassTransient) {
		t.Fatalf("kindForErrorKind(%q) 应透传 %q，实得 %q", res.ErrorKind, toolfail.ClassTransient, got)
	}
	if !toolfail.PolicyOf(toolfail.Class(res.ErrorKind)).Retryable {
		t.Fatalf("%q 的处置策略应仍可重试（降级成 unknown 就丢了这条信息）", res.ErrorKind)
	}

	// 登记簿路径（sandbox.Failure.Kind）走同一份映射，分类必须一致。
	p := Provider{Bindings: []Binding{bind}, Stages: allowStages()}
	out, err := p.Exec(context.Background(), sandbox.Request{Program: `return host.alpha("x")`})
	if err != nil {
		t.Fatalf("登记簿路径不应返回 err: %v", err)
	}
	f, ok := out.Err.(*sandbox.Failure)
	if !ok {
		t.Fatalf("失败分类应是 *sandbox.Failure，实得 %T", out.Err)
	}
	if f.Kind != string(toolfail.ClassTransient) {
		t.Fatalf("登记簿失败分类应为 %q，实得 %q", toolfail.ClassTransient, f.Kind)
	}
}

// TestAllErrorKindsHaveClassAndSuggestions 词表里的每个取值都必须在口径表
// 里有明确归属：失败分类（处置口径）与修复建议（诊断口径）缺一不可。
//
// 失效形态（此前真实存在）：新增一个 ErrorKind ⇒ 两份手写 switch 都静默落
// 各自的 default ⇒ 既没有分类也没有建议，**没有任何测试会红**。
func TestAllErrorKindsHaveClassAndSuggestions(t *testing.T) {
	for _, kind := range AllErrorKinds() {
		t.Run(kind, func(t *testing.T) {
			spec, ok := errorKindTable[kind]
			if !ok {
				t.Fatalf("词表取值 %q 在口径表里没有归属：分类与建议都会落 default", kind)
			}
			if spec.class == "" {
				t.Fatalf("词表取值 %q 没有失败分类（处置口径缺失）", kind)
			}
			if len(spec.suggestions) == 0 {
				t.Fatalf("词表取值 %q 没有修复建议（模型侧拿不到任何指引）", kind)
			}
		})
	}
}

// TestErrorKindTableHasNoOrphanRows 口径表里不能有词表之外的行：两份清单
// 必须是同一份（否则有一类永远不会被判到）。
func TestErrorKindTableHasNoOrphanRows(t *testing.T) {
	vocab := make(map[string]bool, len(AllErrorKinds()))
	for _, k := range AllErrorKinds() {
		vocab[k] = true
	}
	for kind := range errorKindTable {
		if !vocab[kind] {
			t.Errorf("口径表里有词表之外的行 %q——两份清单必须同一份", kind)
		}
	}
}

// TestErrorKindConstantsAreInVocabulary 本包声明的每个 ErrorKind* 常量
// 都必须进词表。
//
// 走 go/ast 而不是逐行正则看源码文本：常量声明的书写形态不止一种
// （单值 / 多值 / 带类型 / 跨行），逐行正则必然漏。
func TestErrorKindConstantsAreInVocabulary(t *testing.T) {
	vocab := make(map[string]bool, len(AllErrorKinds()))
	for _, k := range AllErrorKinds() {
		vocab[k] = true
	}
	for _, v := range errorKindConstValues(t) {
		if !vocab[v] {
			t.Errorf("常量 %q 不在 AllErrorKinds() 里：常量集与词表必须同一份（新增类别请三步同改）", v)
		}
	}
}

// TestUnmanagedHasDedicatedClassification unmanaged 必须有自己的失败分类
// 与修复建议，不落 unknown 桶。
//
// 它与 stage_denied / stage_failed 的处置完全不同（unmanaged.go 的论证）：
// 合成一类的话，调用方会去改一段本来没问题的程序，而真正该做的是把门禁
// 接上。此前它落 default（= 不可分类 = 不可重试），与「判定环自己坏了」
// 折成同一类——那正是「声明与实现不符仍算缺陷」的那一类。
func TestUnmanagedHasDedicatedClassification(t *testing.T) {
	got := kindForErrorKind(ErrorKindUnmanaged)
	if got == string(toolfail.ClassUnknown) {
		t.Fatal("unmanaged 必须有专属分类，不能落 unknown 桶（它与 stage_denied/stage_failed 的处置完全不同）")
	}
	if got != string(toolfail.ClassPermanent) {
		t.Fatalf("unmanaged 的专属分类应为 %q，实得 %q", toolfail.ClassPermanent, got)
	}
	if len(suggestionsFor(ErrorKindUnmanaged)) == 0 {
		t.Fatal("unmanaged 必须有专属修复建议（模型侧要知道该做的是把门禁接上）")
	}
}

// TestSuggestionsCoverEveryErrorKind 建议面覆盖词表全集（与分类面同一份
// 口径表，见 errorkind.go）。
func TestSuggestionsCoverEveryErrorKind(t *testing.T) {
	for _, kind := range AllErrorKinds() {
		t.Run(kind, func(t *testing.T) {
			if len(suggestionsFor(kind)) == 0 {
				t.Fatalf("%q 必须有修复建议", kind)
			}
		})
	}
}

// errorKindConstValues 扫出本包生产代码里声明的 ErrorKind* 常量及其取值。
//
// 只认字符串字面量：非字面量的取值无法在静态层面判定它是否进了词表，
// 遇到就直接失败（宁可红，不要静默放过）。
func errorKindConstValues(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := gosrc.ParsePackage(fset, ".", false)
	if err != nil {
		t.Fatalf("解析本包源码失败: %v", err)
	}
	var out []string
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
					if !strings.HasPrefix(name.Name, "ErrorKind") {
						continue
					}
					if i >= len(vs.Values) {
						t.Fatalf("常量 %s 的取值不是字面量（本判据只认字面量）", name.Name)
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("常量 %s 的取值不是字符串字面量（本判据只认字面量）", name.Name)
					}
					v, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						t.Fatalf("常量 %s 的字面量无法解析: %v", name.Name, uerr)
					}
					out = append(out, v)
				}
			}
		}
	}
	return out
}

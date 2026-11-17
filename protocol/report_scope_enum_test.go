// report_scope_enum_test.go —— scope 词表的单源判据（issue #382）。
//
// scope 是路由信号（out = 转出/转派，redirect 必填）。此前同一张词表在包里
// 有三份实现：判定面 scopeAll、归一面自带的 `case "in", "out"`、校验面写死的
// `r.Scope == "out"`；而宿主连 ScopeIn/ScopeOut 常量都拿不到，只能手抄字面量。
//
// 本文件把三件事钉住：
//   - 词表本体 = 导出常量 ScopeIn/ScopeOut（宿主可构造，不再手抄）；
//   - 归一面接受集覆盖词表全集（新增取值不需要第二处 case）；
//   - redirect 红线按归一后的取值判定（"OUT" 与 "out" 同结论）。

package protocol

import (
	"slices"
	"strings"
	"testing"
)

// TestScopeVocabularySingleSource 词表本体、枚举出口、导出常量必须同一份。
func TestScopeVocabularySingleSource(t *testing.T) {
	if ScopeIn != "in" || ScopeOut != "out" {
		t.Fatalf("导出常量的字面值变了：ScopeIn=%q, ScopeOut=%q", ScopeIn, ScopeOut)
	}
	want := []string{ScopeIn, ScopeOut}
	slices.Sort(want)
	got := AllScopes()
	if !slices.Equal(got, want) {
		t.Fatalf("AllScopes() = %v，应恰好是导出常量的集合 %v（词表扩展/删除必须同步）", got, want)
	}
	if len(scopeAll) != len(want) || !scopeAll[ScopeIn] || !scopeAll[ScopeOut] {
		t.Fatalf("scopeAll 与导出常量脱钩：%v", sortedKeys(scopeAll))
	}
	for _, v := range want {
		if !IsScopeValid(v) {
			t.Fatalf("词表值 %q 未通过 IsScopeValid", v)
		}
	}
}

// TestScopeNormalizationCoversEveryVocabularyValue 归一面不得自带第二份清单：
// 词表里的每个取值，其大小写/空白变体都必须被归一为该取值。
//
// 这条用例就是 issue #382 的机判形式——若归一面写回 `case "in", "out"`，
// 日后给 scopeAll 新增取值时这里会红，而不是让词表内的值被判成词表外。
func TestScopeNormalizationCoversEveryVocabularyValue(t *testing.T) {
	for _, canonical := range AllScopes() {
		for _, variant := range spellingVariants(canonical) {
			parsed, err := ParseReport(scopeReportJSON(variant))
			if err != nil {
				t.Fatalf("解析 scope=%q 失败: %v", variant, err)
			}
			if parsed.Scope != canonical {
				t.Errorf("scope 词值 %q 的写法变体 %q 未被归一为 %q（实得 %q）——该值在判定面词表内却会被判成词表外",
					canonical, variant, canonical, parsed.Scope)
			}
			if !IsScopeValid(variant) {
				t.Errorf("scope 词值 %q 的写法变体 %q 未通过谓词", canonical, variant)
			}
		}
	}
}

// TestScopeRedirectRuleUsesNormalizedValue 红线判据必须与谓词同一归一：
// 转出语义不因大小写而逃过 redirect 必填。
func TestScopeRedirectRuleUsesNormalizedValue(t *testing.T) {
	for _, variant := range []string{ScopeOut, "OUT", "Out", " out ", "  OUT  "} {
		r := &Report{Confidence: ConfidenceHigh, Scope: variant}
		if _, err := r.Validate(); err == nil || !strings.Contains(err.Error(), "redirect") {
			t.Errorf("scope=%q 缺 redirect 必须被拒（转出语义被大小写逃掉了）: %v", variant, err)
		}
	}
	for _, variant := range []string{ScopeIn, "IN", "In", " in "} {
		r := &Report{Confidence: ConfidenceHigh, Scope: variant}
		if _, err := r.Validate(); err != nil {
			t.Errorf("scope=%q 是范围内的写法变体，不该被拒: %v", variant, err)
		}
	}
}

// TestScopeUnknownValueKeepsOriginal 词表外取值保持原值（不兜底成 in），
// 判定交给 Validate——这一条与 #361 的边界一致，防止归一面重新变宽松。
func TestScopeUnknownValueKeepsOriginal(t *testing.T) {
	for _, s := range []string{"external", "out_of_scope", "转出", "EXTERNAL"} {
		parsed, err := ParseReport(scopeReportJSON(s))
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", s, err)
		}
		if parsed.Scope != s {
			t.Errorf("词表外 scope %q 应保持原值，实得 %q（归一面把它映射成了已知值）", s, parsed.Scope)
		}
		if _, err := parsed.Validate(); err == nil {
			t.Errorf("词表外 scope %q 必须被校验面拒绝", s)
		}
	}
}

package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func scopeReportJSON(scope string) string {
	return fmt.Sprintf(
		`{"agent_name":"a","domain":"d","conclusion":"c","confidence":"high","scope":%q}`,
		scope)
}

// TestNormalizeScopeDoesNotRewriteUnknownToIn scope 是路由信号（out = 转出/
// 转派）。词表外的字符串不得被静默改写成 "in"——那会把「超出范围、要转人工」
// 记成「在范围内」，且 scope=out 的 redirect 红线随之失效。
//
// 解析面与直构面对同一个值必须给出同一结论：两条判据方向相反，宿主按哪条
// 判都可能是错的。
func TestNormalizeScopeDoesNotRewriteUnknownToIn(t *testing.T) {
	unknown := []string{"external", "out_of_scope", "unclear", "unknown", "转出", "OUT_OF_SCOPE", ""}
	for _, scope := range unknown {
		t.Run(fmt.Sprintf("%q", scope), func(t *testing.T) {
			parsed, err := ParseReport(scopeReportJSON(scope))
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if parsed.Scope == "in" && scope != "in" {
				t.Fatalf("词表外 scope %q 被改写成了 %q（路由信号被静默反转）", scope, parsed.Scope)
			}
			if parsed.Scope != scope {
				t.Fatalf("词表外 scope 应保持原值 %q，实得 %q", scope, parsed.Scope)
			}

			direct := &Report{Confidence: ConfidenceHigh, Scope: scope}
			warns, perr := parsed.Validate()
			_, derr := direct.Validate()
			if (perr == nil) != (derr == nil) {
				t.Fatalf("解析面与直构面结论相反：parse err=%v, direct err=%v（scope=%q）", perr, derr, scope)
			}
			if scope != "" && perr == nil {
				t.Fatalf("词表外 scope %q 必须被校验面拒绝（critical）", scope)
			}
			if scope != "" && !hasScopeWarn(warns) {
				t.Fatalf("词表外 scope %q 必须在 warn 列表里可见：%v", scope, warns)
			}
		})
	}
}

// TestNormalizeScopeKeepsKnownValues scope 的合法值与大小写变体仍按规范形态
// 收敛：归一化只收拾写法差异，不改变判定。
func TestNormalizeScopeKeepsKnownValues(t *testing.T) {
	cases := map[string]string{
		"in":    "in",
		"IN":    "in",
		" in ":  "in",
		"out":   "out",
		"OUT":   "out",
		" Out ": "out",
	}
	for in, want := range cases {
		parsed, err := ParseReport(scopeReportJSON(in))
		if err != nil {
			t.Fatalf("解析 %q 失败：%v", in, err)
		}
		if parsed.Scope != want {
			t.Fatalf("scope %q 应归一为 %q，实得 %q", in, want, parsed.Scope)
		}
	}
}

func hasScopeWarn(warns []string) bool {
	for _, w := range warns {
		if strings.Contains(w, "scope") {
			return true
		}
	}
	return false
}

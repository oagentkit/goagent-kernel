// report_vocab_variant_test.go —— 写法变体在三条路径上必须给同一结论（issue #371）。
//
// # 这一组钉住什么
//
// 「写法差异不该改变语义」是本包自己的立意（见 report_normalize.go 头注），但
// 此前只有**解析面**归一，五个导出谓词仍是精确查表：
//
//	confidence="HIGH" → 解析面归一为 high 并放行；IsConfidenceValid("HIGH") = false
//	scope="OUT"       → 解析面归一为 out 并放行；IsScopeValid("OUT") = false
//
// ⇒ 同一份字节，解析路径与直构路径得到两个裁决；宿主拿到的错误信息是
// 「不在白名单」，看不出这其实只是写法变体。
//
// 现在谓词先归一再查表（与 IsSeverityValid 同形），本组断言三件事：
//
//  1. 谓词面认写法变体；
//  2. 解析面把写法变体归一成规范值；
//  3. 直构面**不因写法变体**判「不在白名单」（判据面与解析面同一结论）。

package protocol

import (
	"strings"
	"testing"
)

// variantCase 一张词表在三条路径上的注入方式。
type variantCase struct {
	name  string
	table map[string]bool
	valid func(string) bool
	// parse 走解析面注入写法变体，返回规范形态与解析结果。
	parse func(t *testing.T, variant string) (string, *Report)
	// direct 走直构面注入写法变体，返回手工构造的报告。
	direct func(variant string) *Report
}

// TestWritingVariantsAgreeOnEveryPath 写法变体在谓词面 / 解析面 / 直构面上
// 给出同一结论。
//
// 反向验证：把 IsScopeValid 改回精确查表（scopeAll[s]）⇒ 本条转红。
func TestWritingVariantsAgreeOnEveryPath(t *testing.T) {
	for _, c := range variantCases() {
		t.Run(c.name, func(t *testing.T) {
			for legal := range c.table {
				for _, variant := range writingVariants(legal) {
					if !c.valid(variant) {
						t.Fatalf("谓词面拒绝写法变体 %q（应与 %q 同待遇）", variant, legal)
					}
					got, parsed := c.parse(t, variant)
					if got != legal {
						t.Fatalf("解析面把 %q 归一为 %q（应归一为 %q）", variant, got, legal)
					}
					if isWhitelistViolation(parsed.Validate) {
						t.Fatalf("解析面把写法变体 %q 判成「不在白名单」（与谓词面矛盾）", variant)
					}
					direct := c.direct(variant)
					if isWhitelistViolation(direct.Validate) {
						t.Fatalf("直构面把写法变体 %q 判成「不在白名单」（与解析面矛盾）", variant)
					}
				}
			}
		})
	}
}

// TestScopePredicateIsNotExactLookup 单独钉住 scope 谓词：它必须与
// IsSeverityValid 同形（先归一再查表），不得是精确查表。
//
// 这条是上面那条的**红点定位器**：谓词改回精确查表时，它先报出 scope 一条。
func TestScopePredicateIsNotExactLookup(t *testing.T) {
	if !IsScopeValid(ScopeOut) || !IsScopeValid(strings.ToUpper(ScopeOut)) {
		t.Fatal("scope 谓词必须认写法变体")
	}
	if !IsScopeValid(" " + ScopeIn + " ") {
		t.Fatal("scope 谓词必须认空白变体")
	}
	if IsScopeValid("out_of_scope") {
		t.Fatal("scope 谓词不得接受词表外取值")
	}
}

// variantCases 六张判定词表在三条路径上的注入方式。
func variantCases() []variantCase {
	return []variantCase{
		{
			name:  "confidence",
			table: confidenceAll,
			valid: IsConfidenceValid,
			parse: func(t *testing.T, v string) (string, *Report) {
				r := mustParse(t, `{"agent_name":"X","confidence":"`+v+`","conclusion":"c","summary":"s"}`)
				return r.Confidence, r
			},
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: v, Conclusion: "c", Summary: "s"}
			},
		},
		{
			name:  "decision",
			table: decisionAll,
			valid: IsDecisionValid,
			parse: func(t *testing.T, v string) (string, *Report) {
				r := mustParse(t, `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","decision":"`+v+`"}`)
				return r.Decision, r
			},
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s", Decision: v}
			},
		},
		{
			name:  "scope",
			table: scopeAll,
			valid: IsScopeValid,
			parse: func(t *testing.T, v string) (string, *Report) {
				r := mustParse(t, `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","scope":"`+v+`","redirect":"转出到人工"}`)
				return r.Scope, r
			},
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s",
					Scope: v, Redirect: "转出到人工"}
			},
		},
		{
			name:  "action",
			table: actionWhitelist,
			valid: IsActionValid,
			parse: func(t *testing.T, v string) (string, *Report) {
				r := mustParse(t, `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",`+
					`"recommendation":{"action":"`+v+`","requires_approval":true}}`)
				return r.Recommendation.Action, r
			},
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s",
					Recommendation: &Rec{Action: v, RequiresApproval: true}}
			},
		},
		{
			name:  "severity",
			table: severityAll,
			valid: IsSeverityValid,
			parse: func(t *testing.T, v string) (string, *Report) {
				r := mustParse(t, `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","severity":"`+v+`"}`)
				return r.Severity, r
			},
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s", Severity: v}
			},
		},
		{
			name:  "stance",
			table: stanceAll,
			valid: IsStanceValid,
			parse: func(t *testing.T, v string) (string, *Report) {
				r := mustParse(t, `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",`+
					`"verdicts":[{"agent":"a1","stance":"`+v+`"}]}`)
				if len(r.Verdicts) != 1 {
					t.Fatalf("stance 变体 %q 的条目被丢弃", v)
				}
				return r.Verdicts[0].Stance, r
			},
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s",
					Verdicts: Verdicts{{Agent: "a1", Stance: v}}}
			},
		},
	}
}

// writingVariants 一个取值的写法变体集合（大小写 + 空白）。
func writingVariants(legal string) []string {
	return []string{
		legal,
		strings.ToUpper(legal),
		strings.ToLower(legal),
		" " + legal,
		legal + " ",
		" " + legal + " ",
	}
}

// isWhitelistViolation 判定校验结果里是否含「不在白名单」类违规。
//
// 只看白名单这一类：pending_approval 的治理红线（requires_approval 必填）在解析
// 路径上会被**补严**修复，在直构路径上则要宿主自己填——那不是白名单口径的差异。
func isWhitelistViolation(validate func() ([]string, error)) bool {
	_, err := validate()
	return err != nil && strings.Contains(err.Error(), "不在白名单")
}

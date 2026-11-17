// report_decision_direction_test.go —— 词表外取值的「沉默方向」同包一致性断言（issue #361）。
//
// # 这一组钉住什么
//
// 归一化面此前把「不认识」写成零值：
//
//   - 词表外 decision → 抹成 ""（无决策）⇒ Validate 的白名单判据永远够不着它
//     ⇒ 同包的一条 fail-closed 红线在解析路径上被 disarm 掉；
//   - 词表外 stance → **整个条目静默消失**⇒ Validate 的 stance 判据同样够不着。
//
// 同包的 scope（第三轮 #321）却是「保持原值、交 Validate 判 critical」——
// 同一个包内两种相反口径。
//
// 现在口径统一：**词表外的取值必须保持可见**（不得抹成零值、不得丢弃），
// 由判据面 fail-closed 裁决。本组用一张方向表驱动五个词表，任何一个词表
// 偏离方向都会转红。

package protocol

import (
	"strings"
	"testing"
)

// vocabularyDirection 一张词表的「词表外」方向：注入值、解析面读回位置、
// 判据面的反应（critical 拦截 / warn 留痕）。
type vocabularyDirection struct {
	name  string
	value string
	// raw 是注入该值的报告 JSON 模板（%s 为取值位置）。
	raw string
	// read 从解析结果里读回该值。
	read func(*Report) string
	// direct 走直构面构造同一取值的报告（直构路径必须给同一结论）。
	direct func(v string) *Report
	// critical 为真表示判据面必须 critical 拦截；为假表示只 warn 留痕。
	critical bool
}

// vocabularyDirections 五张判定词表的「词表外」方向表。
//
// severity 的反应是 warn 而非 critical：它是**增强面**（聚合按 unknown 保守
// 处理，不因它拦整份报告）——这是本包写死的口径，与「沉默方向」无关：
// 五个词表的沉默方向一致（都保持可见），判据面的**反应强度**按面设计。
func vocabularyDirections() []vocabularyDirection {
	return []vocabularyDirection{
		{
			name:  "decision",
			value: "escalate_to_human",
			raw:   `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","decision":"%s"}`,
			read:  func(r *Report) string { return r.Decision },
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s", Decision: v}
			},
			critical: true,
		},
		{
			name:  "scope",
			value: "external",
			raw:   `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","scope":"%s"}`,
			read:  func(r *Report) string { return r.Scope },
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s", Scope: v}
			},
			critical: true,
		},
		{
			name:  "action",
			value: "escalate",
			raw: `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",` +
				`"recommendation":{"action":"%s","requires_approval":true}}`,
			read: func(r *Report) string { return r.Recommendation.Action },
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s",
					Recommendation: &Rec{Action: v, RequiresApproval: true}}
			},
			critical: true,
		},
		{
			name:  "confidence",
			value: "certain",
			raw:   `{"agent_name":"X","confidence":"%s","conclusion":"c","summary":"s"}`,
			read:  func(r *Report) string { return r.Confidence },
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: v, Conclusion: "c", Summary: "s"}
			},
			critical: true,
		},
		{
			name:  "severity",
			value: "blocker",
			raw:   `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","severity":"%s"}`,
			read:  func(r *Report) string { return r.Severity },
			direct: func(v string) *Report {
				return &Report{AgentName: "X", Confidence: ConfidenceHigh, Conclusion: "c", Summary: "s", Severity: v}
			},
			critical: false,
		},
	}
}

// TestOutOfVocabularyStaysVisible 五张词表的「词表外」沉默方向必须一致：
// 取值**保持可见**（不被抹成零值、不被丢弃），并由判据面裁决。
//
// 反向验证：把 normalizeDecision 的 default 分支改回 m[k] = "" ⇒ 本条转红。
func TestOutOfVocabularyStaysVisible(t *testing.T) {
	for _, c := range vocabularyDirections() {
		t.Run(c.name, func(t *testing.T) {
			parsed := mustParse(t, strings.Replace(c.raw, "%s", c.value, 1))
			if got := c.read(parsed); got != c.value {
				t.Fatalf("词表外的 %s 取值被抹掉了（读到 %q）：沉默方向与其他词表不一致", c.name, got)
			}
			warns, err := parsed.Validate()
			if c.critical {
				if err == nil {
					t.Fatalf("词表外的 %s 取值必须被 Validate 判 critical（fail-closed）", c.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("severity 是增强面：词表外取值只 warn 不拦（实得 %v）", err)
			}
			if len(warns) == 0 {
				t.Fatal("词表外的 severity 取值必须在 warn 面留痕")
			}
		})
	}
}

// TestParseAndDirectPathsAgreeOnOutOfVocabulary 解析路径与直构路径对词表外
// 取值必须给**同一结论**——同一份字节，两个裁决是最贵的形态。
func TestParseAndDirectPathsAgreeOnOutOfVocabulary(t *testing.T) {
	for _, c := range vocabularyDirections() {
		t.Run(c.name, func(t *testing.T) {
			parsed := mustParse(t, strings.Replace(c.raw, "%s", c.value, 1))
			_, perr := parsed.Validate()
			_, derr := c.direct(c.value).Validate()
			if (perr == nil) != (derr == nil) {
				t.Fatalf("解析路径与直构路径结论相反：parse err=%v, direct err=%v", perr, derr)
			}
		})
	}
}

// TestPendingApprovalWithoutApprovalIsTightened decision=pending_approval 而
// requires_approval 为假时，**补严**（requires_approval=true，与
// fixRecommendationContradiction 同向），**不是**删 decision。
//
// 反向验证：把 normalizeDecision 里的补严改回 m[k] = "" ⇒ 本条转红。
func TestPendingApprovalWithoutApprovalIsTightened(t *testing.T) {
	raw := `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s","decision":"pending_approval"}`
	r := mustParse(t, raw)
	if r.Decision != DecisionPending {
		t.Fatalf("decision 不得被静默删成无决策（读到 %q）", r.Decision)
	}
	if r.Recommendation == nil || !r.Recommendation.RequiresApproval {
		t.Fatal("pending_approval 而无审批语义时应补严 requires_approval=true")
	}
	if _, err := r.Validate(); err != nil {
		t.Fatalf("补严后应通过校验: %v", err)
	}
}

// TestOutOfVocabularyStanceIsKeptAndRejected 词表外的 stance 条目不得静默
// 消失：保留原值交 Validate 判 critical。
//
// 反向验证：把 report_types.go 里丢 stance 条目的 continue 改回丢弃 ⇒ 本条转红。
func TestOutOfVocabularyStanceIsKeptAndRejected(t *testing.T) {
	raw := `{"agent_name":"X","confidence":"high","conclusion":"c","summary":"s",` +
		`"verdicts":[{"agent":"a1","stance":"rejected","reason":"r"}]}`
	r := mustParse(t, raw)
	if len(r.Verdicts) != 1 {
		t.Fatalf("词表外 stance 的条目不得静默消失（实得 %d 条）", len(r.Verdicts))
	}
	if r.Verdicts[0].Stance != "rejected" {
		t.Fatalf("词表外 stance 应保持原值（实得 %q）", r.Verdicts[0].Stance)
	}
	if _, err := r.Validate(); err == nil {
		t.Fatal("词表外 stance 必须被 Validate 判 critical（fail-closed）")
	}
}

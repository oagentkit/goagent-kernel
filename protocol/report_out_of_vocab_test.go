// report_out_of_vocab_test.go —— 词表外枚举值的沉默方向判据（issue #361）。
//
// # 这一组在钉什么
//
// 归一化面对「读不懂」的处理必须与判据面同向：**保留原值 + 让 Validate 报信号**，
// 不得把未知值改写成某个合法/零值后静默放行。旧实现的两处 fail-open：
//
//   - decision 未知值 → ""（= 无决策）⇒ Validate 的 `!= ""` 护栏看不到它；
//   - stance 未知值 → 整条裁决丢弃 ⇒ 「否决」可以凭空消失，连 warn 都没有。
//
// # 反向验证
//
//   - 把 normalizeDecision 的 default 改回 `m[k] = ""` ⇒ decision 用例转红；
//   - 把 pending 矛盾修复改回 `m[k] = ""` ⇒ pending 用例转红（决策被静默清空）；
//   - 把 Verdicts.UnmarshalJSON 的 `out = append(out, it)` 改回 `continue`
//     ⇒ stance 用例转红。
package protocol

import (
	"strings"
	"testing"
)

// mustParseLLM 走模型输出来源解析（issue #361 的解析路径）。
func mustParseLLM(t *testing.T, raw string) *Report {
	t.Helper()
	r, err := ParseLLMReport(raw)
	if err != nil {
		t.Fatalf("ParseLLMReport 意外失败: %v\n%s", err, raw)
	}
	return r
}

// TestOutOfVocabDecisionPreserved 词表外 decision 保留原值，Validate 判 critical。
func TestOutOfVocabDecisionPreserved(t *testing.T) {
	raw := `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
		"confidence":"high","decision":"escalate_to_human"}`
	r := mustParseLLM(t, raw)

	if r.Decision != "escalate_to_human" {
		t.Fatalf("词表外 decision 必须保留原值，实得 %q（被改写成零值 = 信号被抹掉）", r.Decision)
	}
	_, err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "decision 不在白名单") {
		t.Fatalf("词表外 decision 必须被 Validate 判 critical，实得 err=%v", err)
	}
}

// TestPendingApprovalContradictionFixedTight pending_approval 缺审批位时补严，
// 不得把 decision 静默清空。
func TestPendingApprovalContradictionFixedTight(t *testing.T) {
	t.Run("有 recommendation 补 requires_approval", func(t *testing.T) {
		raw := `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
			"confidence":"high","decision":"pending_approval",
			"recommendation":{"action":"tune","steps":["echo ok"]}}`
		r := mustParseLLM(t, raw)

		if r.Decision != DecisionPending {
			t.Fatalf("decision 不得被静默清空，实得 %q", r.Decision)
		}
		if r.Recommendation == nil || !r.Recommendation.RequiresApproval {
			t.Fatalf("矛盾修复必须补 requires_approval=true（与 fixRecommendationContradiction 同向），实得 %+v",
				r.Recommendation)
		}
		if _, err := r.Validate(); err != nil {
			t.Fatalf("补严后报告应自洽，实得 %v", err)
		}
	})

	t.Run("无 recommendation 保留 decision 由 Validate 报错", func(t *testing.T) {
		raw := `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
			"confidence":"high","decision":"pending_approval"}`
		r := mustParseLLM(t, raw)

		if r.Decision != DecisionPending {
			t.Fatalf("decision 不得被静默清空，实得 %q", r.Decision)
		}
		if _, err := r.Validate(); err == nil ||
			!strings.Contains(err.Error(), "requires_approval 必须为 true") {
			t.Fatalf("无 recommendation 时 Validate 必须报矛盾，实得 err=%v", err)
		}
	})
}

// TestOutOfVocabStanceKeptAndRejected 词表外 stance 不得静默丢弃。
func TestOutOfVocabStanceKeptAndRejected(t *testing.T) {
	for _, stance := range []string{"rejected", "reject_with_reason", "驳回"} {
		t.Run(stance, func(t *testing.T) {
			raw := `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
				"confidence":"high","verdicts":[{"agent":"a","stance":"` + stance + `","reason":"r"}]}`
			r := mustParseLLM(t, raw)

			if len(r.Verdicts) != 1 {
				t.Fatalf("stance=%q 的裁决条目不得消失，实得 %d 条", stance, len(r.Verdicts))
			}
			if r.Verdicts[0].Stance != stance {
				t.Fatalf("stance=%q 必须保留原值，实得 %q", stance, r.Verdicts[0].Stance)
			}
			if _, err := r.Validate(); err == nil ||
				!strings.Contains(err.Error(), "verdicts.stance 非法") {
				t.Fatalf("stance=%q 必须被 Validate 判 critical，实得 err=%v", stance, err)
			}
		})
	}
}

// TestEnumOutOfVocabDirectionConsistent 五个枚举位的「词表外」沉默方向必须一致：
// 全部保留原值；decision/scope/action/confidence 判 critical，severity 判 warn
// （增强面，不因它拦整份报告——但它必须留痕，不得静默降级成「未评估」）。
func TestEnumOutOfVocabDirectionConsistent(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		read         func(*Report) string
		want         string
		wantCritical bool
		wantWarn     string
	}{
		{
			name: "decision",
			raw: `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
				"confidence":"high","decision":"escalate_to_human"}`,
			read:         func(r *Report) string { return r.Decision },
			want:         "escalate_to_human",
			wantCritical: true,
		},
		{
			name: "scope",
			raw: `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
				"confidence":"high","scope":"external"}`,
			read:         func(r *Report) string { return r.Scope },
			want:         "external",
			wantCritical: true,
			wantWarn:     "scope",
		},
		{
			name: "action",
			raw: `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
				"confidence":"high","recommendation":{"action":"explode","requires_approval":true}}`,
			read: func(r *Report) string {
				if r.Recommendation == nil {
					return ""
				}
				return r.Recommendation.Action
			},
			want:         "explode",
			wantCritical: true,
		},
		{
			name: "confidence",
			raw: `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
				"confidence":"certain"}`,
			read:         func(r *Report) string { return r.Confidence },
			want:         "certain",
			wantCritical: true,
		},
		{
			name: "severity",
			raw: `{"agent_name":"X","domain":"d","conclusion":"c","summary":"s",
				"confidence":"high","severity":"extreme"}`,
			read:         func(r *Report) string { return r.Severity },
			want:         "extreme",
			wantCritical: false,
			wantWarn:     "severity",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustParseLLM(t, tc.raw)
			if got := tc.read(r); got != tc.want {
				t.Fatalf("%s 词表外值必须保留原值 %q，实得 %q（不得改写成零值/已知档）",
					tc.name, tc.want, got)
			}
			warns, err := r.Validate()
			if tc.wantCritical {
				if err == nil {
					t.Fatalf("%s 词表外值必须判 critical，实得零告警通过", tc.name)
				}
			} else if err != nil {
				t.Fatalf("%s 是增强面，不应拦整份报告，实得 %v", tc.name, err)
			}
			if tc.wantWarn != "" {
				joined := strings.Join(warns, "；")
				if !strings.Contains(joined, tc.wantWarn) {
					t.Fatalf("%s 词表外值必须留 warn（不得静默），实得 %v", tc.name, warns)
				}
			}
		})
	}
}

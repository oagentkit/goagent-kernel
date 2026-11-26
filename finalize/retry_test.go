package finalize_test

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/finalize"
)

// TestAdjudicateRetry 失败臂排队重试裁决（P1-2）四态全覆盖：
// 缺省关与无排队都收敛到「放弃 + 明示结算」（保守取向，坏场景不无限重跑）。
func TestAdjudicateRetry(t *testing.T) {
	cases := []struct {
		name string
		f    finalize.RetryFacts
		want finalize.RetryDecision
	}{
		{"缺省关落 failed 结算", finalize.RetryFacts{}, finalize.RetryAbandon},
		{"开关开但无排队", finalize.RetryFacts{RetryEnabled: true}, finalize.RetryAbandon},
		{"开关关不看预算", finalize.RetryFacts{HasQueue: true, BudgetOK: true}, finalize.RetryAbandon},
		{"排队开且预算尽按预算收敛", finalize.RetryFacts{RetryEnabled: true, HasQueue: true}, finalize.RetryBudgetExhausted},
		{"排队开预算未尽回 created 续跑", finalize.RetryFacts{RetryEnabled: true, HasQueue: true, BudgetOK: true}, finalize.RetryRequeue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := finalize.AdjudicateRetry(tc.f); got != tc.want {
				t.Fatalf("AdjudicateRetry(%+v) = %v, want %v", tc.f, got, tc.want)
			}
		})
	}
	if (finalize.RetryDecision).String(finalize.RetryAbandon) == "" {
		t.Fatal("RetryDecision.String 不得为空串")
	}
}

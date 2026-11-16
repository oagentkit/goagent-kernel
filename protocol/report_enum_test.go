package protocol

import (
	"slices"
	"testing"
)

// TestVocabularyEnumerationMatchesPredicates 每张判定词表的枚举出口与
// Is*Valid 判定面必须**双向**一致：枚举里的每个值都过谓词；谓词为真的
// 每个值都在枚举里。
//
// 单向断言（只测「枚举出的值合法」）会漏掉「词表里有但没导出」——那正是
// 宿主手抄清单漂移的入口。长度相等 + 逐值包含把两个方向都钉住。
func TestVocabularyEnumerationMatchesPredicates(t *testing.T) {
	cases := []struct {
		name  string
		all   func() []string
		table map[string]bool
		valid func(string) bool
	}{
		{"verdicts.stance", AllStances, stanceAll, IsStanceValid},
		{"recommendation.action", AllActions, actionWhitelist, IsActionValid},
		{"confidence", AllConfidences, confidenceAll, IsConfidenceValid},
		{"decision", AllDecisions, decisionAll, IsDecisionValid},
		{"scope", AllScopes, scopeAll, IsScopeValid},
		{"severity", AllSeverities, severityAll, IsSeverityValid},
		{"decision.source", AllDecisionSources, decisionSources, IsDecisionSourceValid},
		{"confidence.kind", AllConfidenceKinds, confidenceKinds, IsConfidenceKindValid},
		{"escalation.reason", AllEscalationReasons, escalationReasons, IsEscalationReasonValid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.all()
			if len(got) != len(c.table) {
				t.Fatalf("枚举出口漏值：All*= %d 条，词表 %d 条（%v）", len(got), len(c.table), got)
			}
			if !slices.IsSorted(got) {
				t.Fatalf("枚举出口必须返回排序后的切片：%v", got)
			}
			for i, v := range got {
				if !c.table[v] {
					t.Fatalf("枚举出口出现词表外的值 %q（判定面不认识它）", v)
				}
				if !c.valid(v) {
					t.Fatalf("枚举出的值 %q 未通过对应 Is*Valid", v)
				}
				if i > 0 && got[i-1] == v {
					t.Fatalf("枚举出口有重复值 %q", v)
				}
			}
			for v := range c.table {
				if !slices.Contains(got, v) {
					t.Fatalf("词表里的值 %q 没有出现在枚举出口里", v)
				}
			}
			// 枚举返回的是副本：改写它不得影响下一次枚举 / 判定面。
			if len(got) > 0 {
				got[0] = "__mutated_by_host__"
				again := c.all()
				if slices.Contains(again, "__mutated_by_host__") {
					t.Fatal("枚举出口返回了共享底层数组：宿主改写污染了词表")
				}
				if !c.table[again[0]] {
					t.Fatal("枚举出口返回的切片不是副本")
				}
			}
		})
	}
}

// TestNewVocabularyPredicates 此前 4 张词表只有私有 map、没有合法性谓词；
// 补齐后每条都要能单独判「合法接受 / 非法拒绝」。
func TestNewVocabularyPredicates(t *testing.T) {
	cases := []struct {
		name  string
		valid func(string) bool
		good  string
	}{
		{"stance", IsStanceValid, VerdictAdopt},
		{"action", IsActionValid, ActionTune},
		{"decision", IsDecisionValid, DecisionApproved},
		{"scope", IsScopeValid, "out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.valid(c.good) {
				t.Fatalf("合法值 %q 被拒", c.good)
			}
			for _, bad := range []string{"", "__unknown__", "OUT"} {
				if c.valid(bad) {
					t.Fatalf("非法值 %q 被接受", bad)
				}
			}
		})
	}
}

// TestSeverityRanksMatchVocabulary severityRank 的取值域必须与 severityAll
// 逐字一致，且每条序与 SeverityRankOf 相同。
func TestSeverityRanksMatchVocabulary(t *testing.T) {
	ranks := SeverityRanks()
	if len(ranks) != len(severityRank) || len(ranks) != len(severityAll) {
		t.Fatalf("SeverityRanks 条数 %d，severityRank %d，severityAll %d",
			len(ranks), len(severityRank), len(severityAll))
	}
	for _, e := range ranks {
		if !severityAll[e.Severity] {
			t.Fatalf("SeverityRanks 出现 severityAll 外的值 %q", e.Severity)
		}
		if got := SeverityRankOf(e.Severity); got != e.Rank {
			t.Fatalf("%q 的序漂移：SeverityRanks=%d, SeverityRankOf=%d", e.Severity, e.Rank, got)
		}
	}
	for v := range severityRank {
		if !slices.ContainsFunc(ranks, func(e SeverityRankEntry) bool { return e.Severity == v }) {
			t.Fatalf("severityRank 里的值 %q 没有出现在 SeverityRanks 里", v)
		}
	}
	// 副本：改写返回值不影响下一次。
	ranks[0].Rank = -1
	if SeverityRanks()[0].Rank == -1 {
		t.Fatal("SeverityRanks 返回了共享状态")
	}
}

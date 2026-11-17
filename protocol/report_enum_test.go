package protocol

import (
	"slices"
	"strings"
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
			if len(got) == 0 {
				t.Fatalf("枚举出口不得为空：%s 的词表有 %d 个取值", c.name, len(c.table))
			}
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

// TestEnumerationsFollowTheirOwnTable 每个枚举出口必须读**自己**那张表：
// 往一张表里加一个探针值，只有对应的出口认它。
//
// 这是「出口接错表」的判据（issue #383）：例：AllScopes 误接 severityAll ⇒
// 宿主持 scope 清单做前置校验时，会把严重度取值当成合法 scope 放行（更松），
// 而 scope 真正的取值一个都没进清单（更严）。两种失效都不会让任何东西变红，
// 除非有判据。
//
// 反向验证：把 AllScopes 改成返回 sortedKeys(severityAll) ⇒ 本条转红。
func TestEnumerationsFollowTheirOwnTable(t *testing.T) {
	const probe = "__probe_enum__"
	severityAll[probe] = true
	defer delete(severityAll, "__probe_enum__")

	if !slices.Contains(AllSeverities(), probe) {
		t.Fatal("AllSeverities 必须认 severityAll 新增的取值")
	}
	others := map[string][]string{
		"AllStances":           AllStances(),
		"AllActions":           AllActions(),
		"AllConfidences":       AllConfidences(),
		"AllDecisions":         AllDecisions(),
		"AllScopes":            AllScopes(),
		"AllDecisionSources":   AllDecisionSources(),
		"AllConfidenceKinds":   AllConfidenceKinds(),
		"AllEscalationReasons": AllEscalationReasons(),
	}
	for name, got := range others {
		if slices.Contains(got, probe) {
			t.Errorf("%s 认了 severityAll 的取值：枚举出口接错了表", name)
		}
	}
}

// TestNewVocabularyPredicates 此前 4 张词表只有私有 map、没有合法性谓词；
// 补齐后每条都要能单独判「合法接受 / 非法拒绝」。
//
// # 口径变更（issue #371）：谓词认**写法变体**
//
// 谓词面此前是精确查表，"OUT" 被判非法——而解析面把 "OUT" 归一为 "out" 并放行，
// 同一份字节在两条路径上得到两个裁决。现在谓词先归一（大小写/空白）再查表，
// 与 IsSeverityValid 同形：写法变体与规范值是同一个取值。
func TestNewVocabularyPredicates(t *testing.T) {
	cases := []struct {
		name  string
		valid func(string) bool
		good  string
	}{
		{"stance", IsStanceValid, VerdictAdopt},
		{"action", IsActionValid, ActionTune},
		{"decision", IsDecisionValid, DecisionApproved},
		{"scope", IsScopeValid, ScopeOut},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.valid(c.good) {
				t.Fatalf("合法值 %q 被拒", c.good)
			}
			for _, bad := range []string{"", "__unknown__", "out_of_scope"} {
				if c.valid(bad) {
					t.Fatalf("非法值 %q 被接受", bad)
				}
			}
			// 写法变体与规范值同待遇（归一后查表）。
			for _, variant := range []string{strings.ToUpper(c.good), " " + c.good + " "} {
				if !c.valid(variant) {
					t.Fatalf("写法变体 %q 应与 %q 同待遇（被接受）", variant, c.good)
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

// report_enum.go —— 判定词表的导出枚举面。
//
// # 为什么必须可枚举
//
// 词表是**判定依据**：宿主接报告协议时要知道 confidence / decision /
// severity / stance / action / scope 各有哪些合法取值。此前这些 map 全是
// 包私有且没有任何枚举出口，宿主只能手抄一份常量清单——内核新增一个取值，
// 宿主那份不会跟着变，且没有任何判据会红（「三份手抄表 = 三份会漂的真相」）。
//
// 本文件的出口**直接读判定用的同一张私有 map**，不另写列表：枚举与判定
// 同源，新增取值只需改一处。返回的切片是**新分配的副本**（排序后），宿主
// 改写它不影响判定面。
//
// # 与 Is*Valid 的关系
//
// 枚举回答「全部合法值」，谓词回答「这个值是否合法」。两者必须逐字一致：
// 枚举里的每个值都过谓词；谓词为真的每个值都在枚举里。用例做双向断言
// （单向断言会漏掉「词表里有但没导出」）。
package protocol

import (
	"slices"
	"strings"
)

// canonicalEnumValue 枚举位的规范形态：ToLower + TrimSpace。
//
// # 为什么是单一实现
//
// 「写法差异不该改变语义」这条纪律此前散在两处：解析面（normalize）自己写一遍
// ToLower，谓词面（Is*Valid）却是**精确查表**（issue #371）——同一份字节在
// 解析路径与直构路径得到两个裁决。
//
// 本函数是那件事的**唯一实现**：谓词、归一面、判据面全部走它。
func canonicalEnumValue(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// canonicalEnum 枚举位的规范形态：命中词表则返回规范值；词表外**保持原值**。
//
// 词表外的取值不得被映射成某个已知值，也不得被抹成零值——那是判据面
// （Validate）的事：它要看见原始取值才能给出准确的留痕。
func canonicalEnum(s string, valid func(string) bool) string {
	if c := canonicalEnumValue(s); valid(c) {
		return c
	}
	return s
}

// canonicalOrUnset 与 canonicalEnum 同款，但**纯空白**归一为零值（未声明）：
// 空白字段不携带信息，与缺省同待遇。判据面用 TrimSpace 判「是否声明」，
// 两边对同一份字节给同一结论。
func canonicalOrUnset(s string, valid func(string) bool) string {
	if canonicalEnumValue(s) == "" {
		return ""
	}
	return canonicalEnum(s, valid)
}

// canonicalAction action 的规范形态（归一面与判据面共用）。
func canonicalAction(s string) string { return canonicalEnum(s, IsActionValid) }

// canonicalSeverity severity 的规范形态（归一面与判据面共用）。
func canonicalSeverity(s string) string { return canonicalEnum(s, IsSeverityValid) }

// canonicalDecision decision 的规范形态（归一面与判据面共用）。
func canonicalDecision(s string) string { return canonicalEnum(s, IsDecisionValid) }

// canonicalScope scope 的规范形态（归一面与判据面共用）。
func canonicalScope(s string) string { return canonicalEnum(s, IsScopeValid) }

// sortedKeys 把 map 词表投影为排序后的切片。每次调用都新建切片，
// 调用方改写返回值不会牵动判定面。
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// AllStances 返回 verdicts[].stance 的全部合法值（排序后的副本）。
func AllStances() []string { return sortedKeys(stanceAll) }

// IsStanceValid verdicts[].stance 是否在词表内（大小写/空白变体先归一）。
//
// 归一口径与 IsSeverityValid 同形（issue #371）：模型写 "ADOPT" / " adopt "
// 是同一个词，不是两个裁决——谓词面与解析面必须给同一结论。
func IsStanceValid(s string) bool { return stanceAll[canonicalEnumValue(s)] }

// AllActions 返回 recommendation.action 的全部合法值（排序后的副本）。
func AllActions() []string { return sortedKeys(actionWhitelist) }

// IsActionValid recommendation.action 是否在词表内（大小写/空白变体先归一）。
func IsActionValid(s string) bool { return actionWhitelist[canonicalEnumValue(s)] }

// AllConfidences 返回 confidence 的全部合法值（排序后的副本）。
func AllConfidences() []string { return sortedKeys(confidenceAll) }

// AllDecisions 返回 decision 的全部合法值（排序后的副本）。
func AllDecisions() []string { return sortedKeys(decisionAll) }

// IsDecisionValid decision 是否在词表内（大小写/空白变体先归一；空串 = 未声明，不在此列）。
func IsDecisionValid(s string) bool { return decisionAll[canonicalEnumValue(s)] }

// AllScopes 返回 scope 的全部合法值（排序后的副本）。
func AllScopes() []string { return sortedKeys(scopeAll) }

// IsScopeValid scope 是否在词表内（大小写/空白变体先归一；空串 = 未声明，不在此列）。
func IsScopeValid(s string) bool { return scopeAll[canonicalEnumValue(s)] }

// AllSeverities 返回 severity 的全部合法值（排序后的副本）。
//
// 空串表示「未评估」：它合法但不是一个取值，故不出现在枚举里
// （IsSeverityValid("") 为 true，见 report.go）。
func AllSeverities() []string { return sortedKeys(severityAll) }

// AllDecisionSources 返回判定来源（Verdict.Source）的全部合法值。
func AllDecisionSources() []string { return sortedKeys(decisionSources) }

// AllConfidenceKinds 返回置信语义种类（Verdict.Kind）的全部合法值。
func AllConfidenceKinds() []string { return sortedKeys(confidenceKinds) }

// AllEscalationReasons 返回升级/回落原因的全部合法值。
func AllEscalationReasons() []string { return sortedKeys(escalationReasons) }

// SeverityRankEntry severity 取值与其序（越大越重）。
type SeverityRankEntry struct {
	// Severity 严重度取值。
	Severity string
	// Rank 严重度序。
	Rank int
}

// SeverityRanks 返回 severityRank 词表的全部取值与序（按 Rank 升序，同序按
// 取值字典序）。与 AllSeverities 同源：两张表的取值集合必须逐字一致。
func SeverityRanks() []SeverityRankEntry {
	out := make([]SeverityRankEntry, 0, len(severityRank))
	for s, r := range severityRank {
		out = append(out, SeverityRankEntry{Severity: s, Rank: r})
	}
	slices.SortFunc(out, func(a, b SeverityRankEntry) int {
		if a.Rank != b.Rank {
			return a.Rank - b.Rank
		}
		return stringsCompare(a.Severity, b.Severity)
	})
	return out
}

// stringsCompare 三值比较（避免为一次排序引入更多依赖）。
func stringsCompare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

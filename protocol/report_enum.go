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
//
// # 四者同源（issue #371）
//
// 枚举 / 谓词 / 解析面（report_normalize.go）/ 校验面（Validate）必须是
// **同一份事实**：写法差异（大小写、空白）的归一只在 normalizeEnum 定义一次，
// 谓词一律经 validEnum 归一后查表，解析面与校验面复用同一谓词。此前解析面
// 自己写 ToLower、谓词精确查表，同一份字节在 ParseReport 与直构 + Validate
// 两条路上会得到两个裁决。
package protocol

import (
	"slices"
	"strings"
)

// normalizeEnum 枚举值的写法规整：ToLower + TrimSpace。
//
// 这是**判定面与解析面共用的唯一归一口径**（issue #371）。此前解析面自己
// 各写一遍 ToLower，谓词面精确查表，于是同一份字节在 ParseReport 与
// json.Unmarshal + Validate 两条路上得到两个裁决。写法差异只在这一个函数里
// 定义：谓词先归一后查表，解析面先归一后落值。
func normalizeEnum(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// validEnum 词表判定：先归一，再查表。所有 Is*Valid 谓词都必须走这里，
// 不得直接 `table[s]` 精确匹配（那会与解析面的归一结果分叉）。
func validEnum(table map[string]bool, s string) bool {
	return table[normalizeEnum(s)]
}

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
func IsStanceValid(s string) bool { return validEnum(stanceAll, s) }

// AllActions 返回 recommendation.action 的全部合法值（排序后的副本）。
func AllActions() []string { return sortedKeys(actionWhitelist) }

// IsActionValid recommendation.action 是否在词表内（大小写/空白变体先归一）。
func IsActionValid(s string) bool { return validEnum(actionWhitelist, s) }

// AllConfidences 返回 confidence 的全部合法值（排序后的副本）。
func AllConfidences() []string { return sortedKeys(confidenceAll) }

// IsConfidenceValid confidence 是否在词表内（大小写/空白变体先归一）。
func IsConfidenceValid(c string) bool { return validEnum(confidenceAll, c) }

// AllDecisions 返回 decision 的全部合法值（排序后的副本）。
func AllDecisions() []string { return sortedKeys(decisionAll) }

// IsDecisionValid decision 是否在词表内（空串 = 未声明，不在此列；
// 大小写/空白变体先归一）。
func IsDecisionValid(s string) bool { return validEnum(decisionAll, s) }

// AllScopes 返回 scope 的全部合法值（排序后的副本）。
func AllScopes() []string { return sortedKeys(scopeAll) }

// IsScopeValid scope 是否在词表内（空串 = 未声明，不在此列；
// 大小写/空白变体先归一）。
func IsScopeValid(s string) bool { return validEnum(scopeAll, s) }

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

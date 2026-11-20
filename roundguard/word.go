package roundguard

import "slices"

// roundReasonAll 回合结局词表 → 成员（单一事实源）。
//
// 判定（Reason.Valid）与枚举出口（AllRoundReasons）都读这一张表，新增取值
// 只需改一处。
//
// 键是 **Reason**（不是 string）：键就是常量本身，成员判定与枚举出口都不必
// 转字符串——键型与取值型不一致时，多一次转换就多一处能写错的地方。
var roundReasonAll = map[Reason]bool{
	ReasonCompleted:       true,
	ReasonToolCalls:       true,
	ReasonHandedOff:       true,
	ReasonPaused:          true,
	ReasonFailed:          true,
	ReasonNoOutcome:       true,
	ReasonAborted:         true,
	ReasonBudgetExhausted: true,
	ReasonForcedFinal:     true,
	ReasonStalled:         true,
}

// AllRoundReasons 返回回合结局词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染结局选择器 / 校验落库值，不必手抄常量清单——手抄清单必然漂移：
// 内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红（issue #356）。
// 返回的是副本：调用方改写它不影响判定面。
func AllRoundReasons() []string {
	out := make([]string, 0, len(roundReasonAll))
	for s := range roundReasonAll {
		out = append(out, string(s))
	}
	slices.Sort(out)
	return out
}

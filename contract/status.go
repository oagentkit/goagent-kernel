// status.go —— 会话状态词表：判定面与枚举面的**单一事实源**。
//
// # 会话终态与环节终态是**两个概念**
//
// 会话（编排的一轮）终态与环节（状态机的一步）终态**各有各的词表**，
// 互不覆盖：会话侧有 timed_out，环节侧有 rolled_back / skipped；
// failed 的归属还**相反**：
//
//   - 会话侧 failed 是终态（这一轮编排不会再自行前进，落库为 failed 的
//     会话已经结束）；
//   - 环节侧 failed **不是**终态（它还有出边 → rolled_back：失败的工作
//     通常要重做，回滚是失败后的正常去向）。
//
// ⇒ 两个判据必须**分名**：本包判**会话**终态（IsSessionTerminal），
// statemachine 判**环节**终态（IsStepTerminal）。同名会让读者以为两者
// 是同一份事实，而它们的结论对 12 个环节取值里有 6 个相反
// （issue #354）。两张词表也**不可能同时满足**——不存在一个既是会话
// 终态词表又是环节终态词表的取值集，所以「让宿主对齐一份词表」这条路
// 本身走不通。
//
// # 单一事实源
//
// 词表与分类在一张表里（statusClass）：判定（IsKnownStatus /
// ClassifyStatus / IsSessionTerminal）与枚举（AllStatuses）都读它，
// 新增取值只需改一处。
package contract

import "slices"

// StatusClass 会话状态的分类（三态：过程 / 终态 / 读不懂）。
type StatusClass int

const (
	// StatusOpen 过程态：会话还会继续前进。
	//
	// 含**尚未声明状态**的空串——空串不是词表成员（IsKnownStatus 为假），
	// 但它表示「还没开始」，不是「读不懂」。
	StatusOpen StatusClass = iota
	// StatusTerminal 终态：会话不会再自行前进。
	StatusTerminal
	// StatusUnknown 词表外：内核读不懂这个状态值。
	StatusUnknown
)

// statusClass 会话状态词表 → 分类（单一事实源）。
//
// 词表成员 = 本表的键（7 个）。分类：
//
//   - created / running / waiting_approval ⇒ StatusOpen（过程态）；
//   - succeeded / failed / cancelled / timed_out ⇒ StatusTerminal（终态）。
var statusClass = map[string]StatusClass{
	StatusCreated:         StatusOpen,
	StatusRunning:         StatusOpen,
	StatusWaitingApproval: StatusOpen,
	StatusSucceeded:       StatusTerminal,
	StatusCancelled:       StatusTerminal,
	StatusFailed:          StatusTerminal,
	StatusTimedOut:        StatusTerminal,
}

// AllStatuses 返回会话状态词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染状态选择器 / 校验落库值，不必手抄常量清单——手抄清单必然
// 漂移：内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红。
// 返回的是**副本**：调用方改写它不影响判定面。
func AllStatuses() []string {
	out := make([]string, 0, len(statusClass))
	for s := range statusClass {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// IsKnownStatus 报告 status 是否为会话状态词表成员。
//
// 与 ClassifyStatus 的区别：**空串不是词表成员**（本函数为假），但它被
// 判成 StatusOpen（尚未声明状态）——「不是词表成员」与「读不懂」不是
// 一回事，前者是「还没说」，后者是「说了但不认识」。
func IsKnownStatus(status string) bool {
	_, ok := statusClass[status]
	return ok
}

// ClassifyStatus 把状态值归成三类：过程态 / 终态 / 读不懂。
//
//   - 空串 ⇒ StatusOpen（尚未声明状态，按过程态处理）；
//   - 词表内 ⇒ 该取值在 statusClass 里的分类；
//   - 词表外 ⇒ StatusUnknown。
//
// 三态与 checkpoint / controlplane 的运行状态分类同一口径（同一事实、
// 同一方向）：宿主据此分「还在跑 / 结束了 / 读不懂」三路处置，而不是
// 把「读不懂」混进「还在跑」。
func ClassifyStatus(status string) StatusClass {
	if status == "" {
		return StatusOpen
	}
	if c, ok := statusClass[status]; ok {
		return c
	}
	return StatusUnknown
}

// IsSessionTerminal 报告 status 是否为**会话**终态。
//
// 终态 = 一轮会话不会再自行前进的收尾状态（succeeded / failed /
// cancelled / timed_out）。created / running / waiting_approval 是过程态。
//
// 这是**词表成员判定**，不是「这个终态是否与事实一致」的裁决——后者需要
// 会话存储面与恢复路径，归宿主。机制面只需要一个共同的词表真相，故在
// 这里单点导出：消费方（如 watchdog 的轮括号校验）不必各抄一份终态清单
// （第二份词表必然漂移）。
//
// 注意与 statemachine.IsStepTerminal 的区别：那是**环节**终态，词表不同，
// failed 的归属也相反（见本文件包注释「会话终态与环节终态是两个概念」）。
func IsSessionTerminal(status string) bool {
	return ClassifyStatus(status) == StatusTerminal
}

// IsTerminalStatus 报告 status 是否为会话终态词表成员。
//
// **已更名，见 IsSessionTerminal**（issue #354）：本名在 #354 之前与
// statemachine.IsTerminal 同名，而两者判的是两个不同概念、结论对 12 个
// 环节取值里有 6 个相反。保留本名只为兼容既有调用点，语义与
// IsSessionTerminal **逐字相同**。
func IsTerminalStatus(status string) bool {
	return IsSessionTerminal(status)
}

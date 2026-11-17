// status.go —— 状态词表：成员判定与三态分类的**单一事实源**。
//
// # 为什么这张表在本包
//
// 「什么算词表外」这条**边界**此前没有单源：contract 只导出「终态成员判定」
// （IsTerminalStatus），没有导出「已知状态」谓词，于是每个消费方各自推导出
// 这条边界——checkpoint 与 controlplane 各立了一份逐字同构的三态分类器
// （issue #363）。两处注释都声明「不在本包另立一份」，但立的是两份。
//
// # 三态的含义
//
//   - StatusOpen：已知**过程态**（含尚未声明状态的空串——崩溃快照的常态）；
//   - StatusTerminal：已知终态；
//   - StatusUnknown：词表外，语义判不了。
//
// StatusUnknown 的处置方向由消费方各自决定（checkpoint 阻塞续跑、controlplane
// 拒绝终止），但**分类结果本身只有一份**。
//
// # 归一口径
//
// 判定一律先归一（ToLower + TrimSpace）再查表，与本包 IsTerminalStatus 的
// 旧实现、statemachine 的 normalized() 同口径。此前 checkpoint 的分类器
// 只对终态侧归一（走 IsTerminalStatus）、对过程态侧精确比对，于是同一个
// "RUNNING" 在内核一处被认、在 checkpoint 一处被判「读不懂」——同一判据
// 两个口径。归一后两侧同源。
//
// # 与 statemachine 的划界
//
// 本表的词表覆盖**会话轴与环节轴两根轴的并集**（13 值）：会话独有 timed_out，
// 环节独有 approved / rejected / expired / interrupted / rolled_back / skipped，
// 两轴共有的 7 个取值单点定义在 contract（issue #354/#357）。statemachine 侧
// 只认**环节轴**的 12 个取值（timed_out 是会话独有取值，环节侧按词表外处理）
// ——那是**轴的边界**，不是判据分叉：同一取值在两根轴上的分类逐条一致，由
// statemachine 的跨包用例钉住。
package contract

import (
	"slices"
	"strings"
)

// StatusClass 状态的三态分类。
type StatusClass int

const (
	// StatusOpen 已知过程态：状态还会继续前进。
	//
	// 含**尚未声明状态**的空串（含只含空白的变体）——空串不是词表成员
	// （IsKnownStatus 为假），但它表示「还没开始」，不是「读不懂」。
	StatusOpen StatusClass = iota
	// StatusTerminal 已知终态：状态不会再自行前进。
	StatusTerminal
	// StatusUnknown 词表外：内核读不懂这个状态值。
	//
	// 「不是词表成员」与「读不懂」不是一回事：前者是「还没说」，
	// 后者是「说了但不认识」。
	StatusUnknown
)

// statusClass 状态词表 → 分类（单一事实源）。
//
// 词表成员 = 本表的键（13 个）。分类：
//
//   - created / running / waiting_approval / approved ⇒ StatusOpen（过程态）；
//   - succeeded / failed / cancelled / timed_out / rejected / expired /
//     interrupted / rolled_back / skipped ⇒ StatusTerminal（终态）。
//
// 判定（IsKnownStatus / ClassifyStatus / IsTerminalStatus）与枚举
// （AllStatuses）都读这一张表，新增取值只需改一处。
var statusClass = map[string]StatusClass{
	// 会话轴。
	StatusCreated:         StatusOpen,
	StatusRunning:         StatusOpen,
	StatusWaitingApproval: StatusOpen,
	StatusSucceeded:       StatusTerminal,
	StatusFailed:          StatusTerminal,
	StatusCancelled:       StatusTerminal,
	StatusTimedOut:        StatusTerminal,
	// 环节轴独有。
	StatusApproved:    StatusOpen,
	StatusRejected:    StatusTerminal,
	StatusExpired:     StatusTerminal,
	StatusInterrupted: StatusTerminal,
	StatusRolledBack:  StatusTerminal,
	StatusSkipped:     StatusTerminal,
}

// normalizeStatus 状态值的写法规整：ToLower + TrimSpace。
//
// 与本包 IsTerminalStatus 的旧实现、statemachine 的 normalized() 同口径。
// 状态值来自库 / 配置 / 外部来源，大小写与空白是常见漂移，不应让同一个状态
// 在两个判据面得到相反结论。
func normalizeStatus(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// AllStatuses 返回状态词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染状态选择器 / 校验落库值，不必手抄常量清单——手抄清单必然
// 漂移：内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红
// （issue #356）。返回的是副本：调用方改写它不影响判定面。
func AllStatuses() []string {
	out := make([]string, 0, len(statusClass))
	for s := range statusClass {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// IsKnownStatus 报告 status 是否为内核状态词表成员（大小写/空白变体先归一）。
//
// 与 ClassifyStatus 的区别：**空串不是词表成员**（本函数为假），但它被
// 判成 StatusOpen（尚未声明状态）——「不是词表成员」与「读不懂」不是
// 一回事，前者是「还没说」，后者是「说了但不认识」。
func IsKnownStatus(status string) bool {
	_, ok := statusClass[normalizeStatus(status)]
	return ok
}

// ClassifyStatus 把状态值归成三类：过程态 / 终态 / 读不懂。
//
//   - 空串 ⇒ StatusOpen（尚未声明状态，按过程态处理）；
//   - 词表内 ⇒ 该取值在 statusClass 里的分类；
//   - 词表外 ⇒ StatusUnknown。
//
// 三态与 checkpoint / controlplane 的运行状态分类同一口径（同一事实、同一
// 方向）：宿主据此分「还在跑 / 结束了 / 读不懂」三路处置，而不是把「读不懂」
// 混进「还在跑」。
//
// # 谁在用它（issue #363）
//
// checkpoint 与 controlplane 的「什么算词表外」此前各有一份逐字同构的
// 分类器（两份注释都写着「不在本包另立一份」，但立的是两份）。本函数是
// 那条边界的唯一出口：两处改为调用它，边界只剩一份。
func ClassifyStatus(status string) StatusClass {
	n := normalizeStatus(status)
	if n == "" {
		return StatusOpen
	}
	if c, ok := statusClass[n]; ok {
		return c
	}
	return StatusUnknown
}

// IsTerminalStatus 报告 status 是否为内核终态词表成员（会话/环节共用一份判据）。
//
// 终态 = 一轮编排不会再**自行**前进的收尾状态：
//
//   - 会话轴：succeeded / failed / cancelled / timed_out；
//   - 环节轴：succeeded / failed / cancelled / rejected / expired /
//     interrupted / rolled_back / skipped。
//
// **failed 在两根轴上都是终态。** 环节的 failed → rolled_back 是回滚补偿：
// 由裁决面/恢复面显式驱动，不是状态机自行前进，故不改变「failed 已收尾」
// 的判定（issue #354 把两侧口径收敛到这一条）。
//
// created / running / waiting_approval / approved 是过程态；词表外取值
// （含宿主自定义终态）一律 false——本函数只判**内核词表成员**，不臆断
// 自定义状态。#334 的三态口径不变：词表外按「判不了」处置，不得冒充终态。
//
// 这是**词表成员判定**，不是「这个终态是否与事实一致」的裁决——后者需要存储
// 面与恢复路径，归宿主。机制面只需要一个共同的词表真相，故在这里单点导出：
// 消费方（checkpoint / controlplane / watchdog / 参考实现）一律引用本函数
// 或 ClassifyStatus，不各抄一份终态清单（第二套词表必然漂移）。
//
// statemachine 是**环节侧**的类型化入口：IsTerminal 从自己的转移表派生
// （无自行推进出边=终态，#374），不直接调用本函数——两者对 12 个环节状态
// 的结论由 statemachine 的跨包表驱动用例逐条钉住；转移表若变，用例会同时
// 逼本函数与 IsTerminal 对齐（#354）。timed_out 是会话独有取值，环节侧
// 按「词表外」返回 false，这是轴的边界而不是判据分叉。
//
// 实现读 statusClass 表（与 ClassifyStatus 同一张）：判定与枚举同源，
// 不另写一份终态清单——两份清单迟早漂移，且漂移是静默的。
func IsTerminalStatus(status string) bool {
	return ClassifyStatus(status) == StatusTerminal
}

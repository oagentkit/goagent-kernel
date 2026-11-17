// enum.go —— 环节状态 / 环节类别的导出枚举面。
//
// # 为什么必须可枚举
//
// 词表是**判定依据**：宿主接编排协议时要知道环节有哪些合法状态与类别。
// 此前这两张词表全是包私有且没有任何枚举出口，宿主只能手抄一份常量清单
// ——内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红
// （「三份手抄表 = 三份会漂的真相」，issue #356）。
//
// 本文件的出口**直接读判定用的同一张私有 map**，不另写列表：枚举与判定
// 同源，新增取值只需改一处。返回的切片是**新分配的副本**（排序后），宿主
// 改写它不影响判定面。
//
// # 与 Valid 的关系
//
// 枚举回答「全部合法值」，谓词回答「这个值是否合法」。两者必须逐字一致：
// 枚举里的每个值都过谓词；谓词为真的每个值都在枚举里。用例做双向断言
// （单向断言会漏掉「词表里有但没导出」）。
//
// # 为什么是**字符串集合**而不是 []StepStatus
//
// 本包的环节状态常量是 contract 的**无类型别名**（issue #354/#357：状态
// 词表的主人只有一个），既能作 StepStatus 用、也能作 string 直传那些按
// 字符串取参的位点。宿主手里的「环节有哪些状态」也多是字符串（落库列、
// 协议字段），故枚举出口按字符串集合给，与 protocol 的 All* 出口同形。
package statemachine

import "slices"

// stepStatusAll 环节状态词表（12 值）——判定与枚举的**同一张表**。
//
// 覆盖**环节轴**的全部取值：会话轴独有的 timed_out 不在其中（环节侧按
// 词表外处理——那是轴的边界，不是判据分叉，见 contract.IsTerminalStatus）。
var stepStatusAll = map[StepStatus]bool{
	StatusCreated:         true,
	StatusRunning:         true,
	StatusWaitingApproval: true,
	StatusApproved:        true,
	StatusRejected:        true,
	StatusExpired:         true,
	StatusSucceeded:       true,
	StatusFailed:          true,
	StatusInterrupted:     true,
	StatusRolledBack:      true,
	StatusSkipped:         true,
	StatusCancelled:       true,
}

// stepTypeAll 环节类别词表（5 值）——判定与枚举的**同一张表**。
var stepTypeAll = map[StepType]bool{
	TypeReadonly: true,
	TypePlan:     true,
	TypeApproval: true,
	TypeExecute:  true,
	TypeVerify:   true,
}

// AllStepStatuses 返回环节状态的**字符串集合**（排序后的新切片）。
//
// 宿主据此渲染环节状态选择器 / 校验落库值，不必手抄常量清单。
func AllStepStatuses() []string {
	out := make([]string, 0, len(stepStatusAll))
	for s := range stepStatusAll {
		out = append(out, string(s))
	}
	slices.Sort(out)
	return out
}

// AllStepTypes 返回环节类别的**字符串集合**（排序后的新切片）。
func AllStepTypes() []string {
	out := make([]string, 0, len(stepTypeAll))
	for t := range stepTypeAll {
		out = append(out, string(t))
	}
	slices.Sort(out)
	return out
}

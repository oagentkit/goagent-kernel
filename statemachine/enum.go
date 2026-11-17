// enum.go —— 判定词表的导出枚举面（issue #356）。
//
// # 为什么必须可枚举
//
// 词表是**判定依据**：宿主接环节状态机时要知道内核认哪些环节状态 /
// 环节类别。此前这些取值只有包私有常量与谓词，宿主只能手抄一份常量清单
// ——内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红
// （「手抄表 = 会漂的真相」）。
//
// 本文件的出口**直接读判定用的同一张私有 map**（stepStatusAll /
// stepTypeAll）：枚举与判定同源，新增取值只需改一处。返回的切片是
// **新分配的副本**，宿主改写它不影响判定面。
//
// # 与 Valid 的关系
//
// 枚举回答「全部合法值」，谓词回答「这个值是否合法」。两者必须逐字一致：
// 枚举里的每个值都过谓词；谓词为真的每个值都在枚举里。用例做双向断言
// （单向断言会漏掉「词表里有但没导出」）。
//
// # 取值形态：字符串集合
//
// 本包的常量是**无类型常量**（同一取值既可作 StepStatus 用，也可作
// string 直传），故枚举出口按**字符串集合**给，不是 []StepStatus：
// 宿主拿它去比对外部读到的状态值不必先转类型。
package statemachine

import "slices"

// stepStatusAll 环节状态词表（**单一事实源**：判定与枚举都读它）。
//
// 键是规范形态（小写下划线）；判定面按**归一后**的形态查表，故大小写与
// 空白变体不另立键——否则同一个词会有两份真相（issue #373）。
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

// stepTypeAll 环节类别词表（**单一事实源**）。
var stepTypeAll = map[StepType]bool{
	TypeReadonly: true,
	TypePlan:     true,
	TypeApproval: true,
	TypeExecute:  true,
	TypeVerify:   true,
}

// AllStepStatuses 返回环节状态词表的全集（排序后的**新切片**）。
func AllStepStatuses() []string {
	out := make([]string, 0, len(stepStatusAll))
	for s := range stepStatusAll {
		out = append(out, string(s))
	}
	slices.Sort(out)
	return out
}

// AllStepTypes 返回环节类别词表的全集（排序后的**新切片**）。
func AllStepTypes() []string {
	out := make([]string, 0, len(stepTypeAll))
	for t := range stepTypeAll {
		out = append(out, string(t))
	}
	slices.Sort(out)
	return out
}

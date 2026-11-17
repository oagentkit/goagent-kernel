// state.go —— 任务状态的导出枚举面。
//
// # 为什么必须可枚举
//
// 词表是**判定依据**：宿主接任务板协议时要知道任务有哪些合法状态。此前
// 这张词表是包私有且没有任何枚举出口，宿主只能手抄一份常量清单——内核新增
// 一个取值，宿主那份不会跟着变，且没有任何判据会红（「三份手抄表 = 三份会
// 漂的真相」，issue #356）。
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
package taskboard

import "slices"

// taskStateAll 任务状态词表（4 值）——判定与枚举的**同一张表**。
var taskStateAll = map[State]bool{
	StateOpen:    true,
	StateClaimed: true,
	StateDone:    true,
	StateFailed:  true,
}

// AllStates 返回任务状态词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染任务状态选择器 / 校验落库值，不必手抄常量清单。
//
// 注意 done 与 failed 的**词表成员身份**与**终态身份**是两件事：failed 在
// 词表内但非终态（可重新认领，见 State.Terminal），枚举出口给的是词表成员
// 集合，不是终态集合。
func AllStates() []State {
	out := make([]State, 0, len(taskStateAll))
	for s := range taskStateAll {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

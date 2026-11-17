// state.go —— 任务状态词表：判定面与枚举面的**单一事实源**。
//
// # 三个判定，各司其职
//
//   - Valid()：这个状态在内核词表内吗？（词表成员判定）
//   - Terminal()：这个状态不可再被认领吗？（终态判定）
//   - Claimable()：这个状态能被认领吗？不能的话**为什么**不能。
//
// Claimable 是**写路径的唯一判据**（issue #360）：所有 Board.Claim 实现
// 必须调用它，不得各写各的闸。此前契约文本的「必须报错」清单里**没有**
// ErrUnknownState，四份参考实现里只有一份拒词表外状态（#323 只修了一个），
// 另三份照常领到租约并铸令牌——失效形态是绿的：认领成功、令牌可用、
// 副作用照常施加，没有任何错误也没有告警。
//
// # 词表与终态：failed 不是终态（刻意）
//
// 只有 done 是终态。failed 的任务必须能重做——一次失败就把子任务永久
// 钉死的话，宿主的补救路径（重新派一个人）会静默失效。
package taskboard

import "slices"

// State 任务状态。
type State string

const (
	// StateOpen 待认领。
	StateOpen State = "open"
	// StateClaimed 已被认领（owner + lease 必须有值）。
	StateClaimed State = "claimed"
	// StateDone 已完成（终态）。
	StateDone State = "done"
	// StateFailed 已失败（**非终态**——允许重新认领：失败的工作通常要重做）。
	StateFailed State = "failed"
)

// stateAll 任务状态词表（**单一事实源**：判定与枚举都读它）。
var stateAll = map[State]bool{
	StateOpen:    true,
	StateClaimed: true,
	StateDone:    true,
	StateFailed:  true,
}

// AllStates 返回任务状态词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染状态选择器 / 校验落库值，不必手抄常量清单——手抄清单必然
// 漂移：内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红。
// 返回的是**副本**：调用方改写它不影响判定面。
func AllStates() []State {
	out := make([]State, 0, len(stateAll))
	for s := range stateAll {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// Terminal 报告该状态是否**不可再被认领**。
//
// 只有 done 是终态。failed 在词表上**非终态**，这是刻意的：失败的任务
// 必须能重做，否则一次失败就把子任务永久钉死，而宿主的补救路径（重新派
// 一个人）会静默失效。
func (s State) Terminal() bool { return s == StateDone }

// Valid 报告状态是否在内核词表内。
//
// # 为什么必须导出（issue #323）
//
// 词表外状态代表「内核读不懂这个状态」：体检面（Check）会报
// CodeUnknownState，但写路径此前只判 Terminal()（未知状态不是终态）⇒
// 同一个状态「体检说非法、Claim 说可领」。宿主需要能自己区分
// 「状态合法且非终态」与「状态不在词表内」——Valid() 就是这个判据。
//
// Terminal() 对词表外状态仍返回 false（不能把「读不懂」说成「终态」），
// 所以写路径必须显式消费 Valid()（或直接调 Claimable），不能靠
// Terminal() 反推。
func (s State) Valid() bool { return stateAll[s] }

// Claimable 报告该状态能否被认领，不能则给出**为什么**不能。
//
// 这是写路径的**单一判据**（issue #360）：所有 Board.Claim 实现必须调用
// 它——Board.Claim 契约的「必须报错」清单把 ErrUnknownState 列成第五条，
// 实现不得各写各的闸（闸各写各的 ⇒ 有的实现漏一道，而漏的那份照常铸
// 令牌，失效形态是绿的）。
//
//   - 词表外 ⇒ ErrUnknownState：「内核读不懂这个状态」，写路径 fail-closed
//     不认领——否则一条脏状态行会让副作用照常施加；
//   - 词表内且终态 ⇒ ErrTerminal；
//   - 词表内且非终态 ⇒ nil。
//
// 判定顺序有意如此：**先判读不懂，再判终态**。词表外状态不是终态
// （Terminal() 对它返回 false），把它报成 ErrTerminal 会掩盖「读不懂」
// 的事实，把宿主导向错误的排查方向。
func (s State) Claimable() error {
	if !stateAll[s] {
		return ErrUnknownState
	}
	if s.Terminal() {
		return ErrTerminal
	}
	return nil
}

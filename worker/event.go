// event.go —— 资格类路径的留痕事件（issue #179）。
//
// 心跳与续期是两条资格路径，此前失败只剩「取消 ctx」一个副作用、本身完全
// 静默：租约易主后的 Complete/Release 被拒按成功路径吞掉，续期连续失败
// 没有任何信号——下游只能从「副作用做了两遍」反推。事件面把「资格不可用」
// 变成宿主可观测的结论。
package worker

import (
	"github.com/oekit/logx"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// EventKind 驱动留痕事件的类别。
type EventKind string

const (
	// EventLeaseLost 租约已易主：Renew 报 ErrLeaseLost，驱动已取消在途
	// 执行的 ctx。本次执行不得再施加副作用（危险窗口在施加之前封口）。
	EventLeaseLost EventKind = "lease_lost"
	// EventLeaseUnconfirmed 持有资格无法确认：续期连续失败跨过一整个租约
	// 周期（期间没有一次成功）。按 Renew 的契约，最后一次确认 + TTL 之后
	// 租约必然终止——驱动按 fail-safe 取消在途 ctx、停止发起新副作用，
	// 避免与新持有者重复施加。
	EventLeaseUnconfirmed EventKind = "lease_unconfirmed"
	// EventFinalizeRejected 收场被拒：Complete/Release 报 ErrLeaseLost，
	// 本次收场未生效（闸已易主，任务归新持有者）。副作用是否已经施加
	// 归 handler 的收口决定——此事件是下游对账的入口，不是成功路径。
	EventFinalizeRejected EventKind = "finalize_rejected"
)

// Event 一次资格类留痕事件（Config.OnEvent）。
type Event struct {
	// Kind 事件类别。
	Kind EventKind
	// Task 事件涉及的任务。
	Task taskboard.TaskID
	// Err 触发事件的底层错误（Renew / Complete / Release 的原始返回）。
	Err error
}

// log 资格路径的默认留痕：logx.Delegating 懒解析宿主后端，公开 API 不焊
// printf 回调（AGENTS.md 红线）。宿主不接 OnEvent 也有一份默认轨迹。
var log = logx.Delegating("worker")

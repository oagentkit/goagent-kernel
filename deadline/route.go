package deadline

import (
	"context"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
)

// Outcome 三分路由的结论（超时 / 取消 / 停机，见包头表）。
type Outcome int

const (
	// OutcomeNone ctx 未取消（正常编排中，没有收敛动作）。
	OutcomeNone Outcome = iota
	// OutcomeTimedOut 会话墙钟到点：落 timed_out 终态。
	//
	// deny-by-default 家族——超时轮的产出是**没跑完**的，不可信：
	// 不得落 succeeded，也不得借排队续跑把死 ctx 再跑一轮。
	OutcomeTimedOut
	// OutcomeCancelled 用户/调用方显式取消：落 cancelled 终态。
	OutcomeCancelled
	// OutcomeShutdown 进程停机：**不落终态**（running/waiting_approval 留给
	// 恢复循环/启动对账收敛），也不写失败报告——停机不是这次编排的结论。
	OutcomeShutdown
)

// String 机读名（进日志与测试断言）。
func (o Outcome) String() string {
	switch o {
	case OutcomeNone:
		return "none"
	case OutcomeTimedOut:
		return "timed_out"
	case OutcomeCancelled:
		return "cancelled"
	case OutcomeShutdown:
		return "shutdown"
	}
	return "unknown"
}

// StatusOf 结论 → 终态词表值的映射钩子（issue #237）。
//
// 宿主的状态词表不归内核假定：contract 的词表只是**缺省**，使用自有状态机
// （不 import contract、或状态名不同）的宿主可注入自己的映射，不必先把
// Outcome 转成 contract 的状态常量再自映射。返回空串 = 该结论不落终态
// （与缺省映射对 None / Shutdown 的处理同一口径）。
type StatusOf func(Outcome) string

// DefaultStatusOf 缺省映射：**消费 contract 的词表**，不另造一份。
//
// 不落终态的两档（None / Shutdown）返回空串：空串即「本轮没有终态要写」，
// 宿主据此跳过状态迁移，而不是写一个空状态。
//
// 独立导出是为了让宿主能「只覆盖个别档、其余仍走缺省」——在注入的
// StatusOf 里回落本函数即可（见 StatusWith）。
func DefaultStatusOf(o Outcome) string {
	switch o {
	case OutcomeTimedOut:
		return contract.StatusTimedOut
	case OutcomeCancelled:
		return contract.StatusCancelled
	default:
		return ""
	}
}

// Status 该结论对应的缺省终态词表值（contract 词表）。
//
// 行为与既有版本逐字一致（issue #237 的零值兼容要求）：不注入映射的调用方
// 一行不改。自有词表的宿主用 StatusWith 注入 StatusOf。
func (o Outcome) Status() string { return DefaultStatusOf(o) }

// StatusWith 按注入的映射钩子取终态词；mapper 为 nil 时回落 DefaultStatusOf
// （contract 词表）——「没注入」与「注入缺省实现」是同一个行为。
//
// 映射是逐次参数而不是全局开关：同一个进程里 contract 模型与自有模型的
// 宿主可以各用各的，互不污染。
func (o Outcome) StatusWith(mapper StatusOf) string {
	if mapper == nil {
		return DefaultStatusOf(o)
	}
	return mapper(o)
}

// Route 收尾段的三分路由（判据面；宿主的 finalize 路由表按同一形状实现）。
//
// # 次序纪律（硬约束）
//
// **timed_out 臂必须排在停机臂（`ctx.Err() != nil`）之前。** 墙钟到点时
// ctx 同样已取消（DeadlineExceeded），停机臂的条件对它同样成立——次序写反，
// 每次会话超时都会被判成停机、不落终态，会话永久滞留 running。
// Route 的第一臂就是 timed_out，测试里用「已取消 + cause=ErrSessionDeadline」
// 的 ctx 钉住（该 ctx 同时满足停机臂的条件，走错臂当场红）。
//
// 三分的前提是三者各有自己的 cause：
//
//   - 墙钟：WithTimeoutCause(..., ErrSessionDeadline)（本包 WithCause）；
//   - 用户取消：宿主用 context.WithCancelCause(哨兵) 表达，哨兵传进 userCancel；
//   - 停机：父 ctx 取消传播下来的 cause（或裸 cancel）。
//
// userCancel 缺省（不传）时没有取消档：非本包 cause 的取消一律判停机——
// 裸 cancel 与停机在 Cause 上不可分，宿主要区分就必须给取消一个专属 cause。
func Route(ctx context.Context, userCancel ...error) Outcome {
	if ctx.Err() == nil {
		return OutcomeNone
	}
	cause := context.Cause(ctx)
	// ① timed_out 最先（次序纪律）：它同时满足停机臂的条件，必须先被判走。
	if errors.Is(cause, ErrSessionDeadline) {
		return OutcomeTimedOut
	}
	// ② 用户取消（宿主哨兵，可给多个）。
	for _, sentinel := range userCancel {
		if sentinel != nil && errors.Is(cause, sentinel) {
			return OutcomeCancelled
		}
	}
	// ③ 进程停机：不落终态，留给恢复循环。
	return OutcomeShutdown
}

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

// StatusOf Outcome → 终态词表的**可注入映射钩子**（issue #237，装配期注入）。
//
// nil（缺省）⇒ [DefaultStatus]：消费 contract 的词表（StatusTimedOut /
// StatusCancelled）。非 contract 模型的宿主把自有词表的映射函数赋到这里
// （一次、装配期；运行期换映射是宿主自己的并发决定），此后全部 Status()
// 读数随宿主词表——宿主不必再把 contract 词表自映射一遍。
//
// 钩子收到的 o 已是**纯 enum**（Route 的返回值就是它）：只想要枚举、
// 不想要任何词表的宿主直接 switch o，可以不碰本钩子。
var StatusOf func(Outcome) string

// DefaultStatus 内建映射：Outcome → contract 终态词表（[StatusOf] 缺省所指）。
//
// 单独导出是因为宿主钩子的典型形态是「只重映射个别档、其余回落内建」——
// 没有这个名字，回落就得把 contract 词表抄一遍（第二份真相）。
//
// 不落终态的两档（None / Shutdown）返回空串：空串即「本轮没有终态要写」，
// 宿主据此跳过状态迁移，而不是写一个空状态。
func DefaultStatus(o Outcome) string {
	switch o {
	case OutcomeTimedOut:
		return contract.StatusTimedOut
	case OutcomeCancelled:
		return contract.StatusCancelled
	default:
		return ""
	}
}

// Status 该结论对应的终态词表值：经 [StatusOf] 钩子映射（nil ⇒ 内建
// [DefaultStatus]，即 contract 的词表——**消费它，不另造一份**）。
//
// 宿主词表（非 contract 模型）经装配期给 StatusOf 注入自有映射（issue #237）；
// 注入前/后的零值语义不变：不落终态的两档（None / Shutdown）映射为空串
// 「本轮没有终态要写」（钩子实现应保持这一约定）。
func (o Outcome) Status() string {
	if StatusOf != nil {
		return StatusOf(o)
	}
	return DefaultStatus(o)
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

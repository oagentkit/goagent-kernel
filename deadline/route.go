package deadline

import (
	"context"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/finalize"
)

// Outcome 收尾路由的结论（超时 / 取消 / 停机 / 接管，见包头表）。
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
	// OutcomeTakeover 接管：**不落终态回 created**（载荷与续跑语义归 steer）。
	//
	// 追加在末尾而非插在中间：既有取值的数值不变（宿主可能持久化这些整数）。
	OutcomeTakeover
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
	case OutcomeTakeover:
		return "takeover"
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
// 宿主据此跳过状态迁移，而不是写一个空状态。接管档落 created（与 finalize
// 同一口径：接管回起点重排，不是终态）。
//
// 独立导出是为了让宿主能「只覆盖个别档、其余仍走缺省」——在注入的
// StatusOf 里回落本函数即可（见 StatusWith）。
func DefaultStatusOf(o Outcome) string {
	switch o {
	case OutcomeTimedOut:
		return contract.StatusTimedOut
	case OutcomeCancelled:
		return contract.StatusCancelled
	case OutcomeTakeover:
		// 接管回 created（与 finalize.ActionTakeover.Status 同一口径）。
		return contract.StatusCreated
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

// Route 收尾路由的**投影**（issue #355）：判定面在 finalize（收尾路由的
// 唯一收口），本函数只把它的结论映射成本包的 Outcome。
//
// # 为什么不再自己判
//
// 此前本包与 finalize **各写一张收尾路由表**，判据已分叉：同一个接管的 ctx
// 在两包得到**相反**结论（本包判 shutdown「不落终态」、finalize 判 takeover
// 「落 created」）——接管场景下该收敛的会话被永久留在 running，而两包零交叉
// 断言，失效形态是绿的（两边都返回正常值，只是结论相反）。
//
// 现在只有一份判据：本函数 = finalize.RouteWith 的投影（Outcome ← Action
// 一一映射）。**次序纪律（timeout 臂必须先于停机臂）与接管臂的认领**都由
// finalize 的路由表承担；交叉断言（同一组 ctx 同时喂两包、断言结论一致）
// 见 finalize 包的用例。
//
// # userCancel 变参
//
// 宿主注入的「用户取消」哨兵原样传给 finalize.RouteWith：注入的哨兵与内核
// 缺省的 finalize.ErrUserCancelled 同等对待（命中任一即判 cancelled）。
//
// 与从前的一处差别：缺省的 finalize.ErrUserCancelled 现在**也**判 cancelled
// （从前只有宿主显式注入的哨兵才判）。这是「单一判据」的必然结果——同一个
// 专属 cause 在两包得到不同结论，正是本次要消掉的那类分叉。
func Route(ctx context.Context, userCancel ...error) Outcome {
	switch finalize.RouteWith(ctx, userCancel...) {
	case finalize.ActionTakeover:
		return OutcomeTakeover
	case finalize.ActionCancelled:
		return OutcomeCancelled
	case finalize.ActionTimedOut:
		return OutcomeTimedOut
	case finalize.ActionShutdown:
		return OutcomeShutdown
	default:
		return OutcomeNone
	}
}

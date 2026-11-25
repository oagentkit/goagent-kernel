package deadline

import (
	"context"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/steer"
)

// Outcome 收尾路由的结论（接管 / 超时 / 取消 / 停机，见包头表）。
//
// 本枚举是**唯一判据**的结论面（issue #355）：finalize.Action 是它的动作投影，
// 两包不再各写一份路由表。
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
	// OutcomeTakeover 接管（steer.ErrSteered）：**不落终态回 created**。
	//
	// 与停机臂的语义相反：停机是「这一轮不收敛」，接管是「这一轮由新输入
	// 续跑」——归 created 交给 steer.Run 承接。deadline 此前不认识这个 cause，
	// 同一 ctx 在 deadline 判 shutdown、在 finalize 判 takeover（issue #355）。
	//
	// 追加在末尾（不插队）：既有结论的数值不因新增臂而漂移。
	OutcomeTakeover
)

// 结论的机读名（String() 的取值）。
//
// 与 contract 状态词表**同形**的两档（timed_out / cancelled）直接引用
// contract 的常量：本包的 DefaultStatusOf 也把这两档映射到同一批词，
// 两处各写一份拼写就是「同一取值两份真相」——判定词表登记门禁
// （harness-word-table-handcopy）会当场报红。
// 另三档（none / shutdown / takeover）是收尾结论自有词，不在 contract 词表里。
const (
	outcomeNameNone      = "none"
	outcomeNameTimedOut  = contract.StatusTimedOut
	outcomeNameCancelled = contract.StatusCancelled
	outcomeNameShutdown  = "shutdown"
	outcomeNameTakeover  = "takeover"
)

// String 机读名（进日志与测试断言）。
func (o Outcome) String() string {
	switch o {
	case OutcomeNone:
		return outcomeNameNone
	case OutcomeTimedOut:
		return outcomeNameTimedOut
	case OutcomeCancelled:
		return outcomeNameCancelled
	case OutcomeShutdown:
		return outcomeNameShutdown
	case OutcomeTakeover:
		return outcomeNameTakeover
	}
	return "unknown"
}

// StatusOf 结论 → 收尾目标状态的映射钩子（issue #237）。
//
// 宿主的状态词表不归内核假定：contract 的词表只是**缺省**，使用自有状态机
// （不 import contract、或状态名不同）的宿主可注入自己的映射，不必先把
// Outcome 转成 contract 的状态常量再自映射。返回空串 = 该结论不写状态
// （与缺省映射对 None / Shutdown 的处理同一口径）。
type StatusOf func(Outcome) string

// DefaultStatusOf 缺省映射：**消费 contract 的词表**，不另造一份。
//
// 不写状态的两档（None / Shutdown）返回空串：空串即「本轮没有状态要写」，
// 宿主据此跳过状态迁移，而不是写一个空状态。Takeover 返回 created——
// 它是回迁目标（可认领态），不是终态，但仍是收尾动作要写的状态。
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
		return contract.StatusCreated
	default:
		return ""
	}
}

// Status 该结论对应的缺省收尾目标状态（contract 词表）。
//
// 行为与既有版本逐字一致（issue #237 的零值兼容要求）：不注入映射的调用方
// 一行不改。自有词表的宿主用 StatusWith 注入 StatusOf。新增的 Takeover 臂
// 返回 created（与 finalize.ActionTakeover.Status() 同值，issue #355）。
func (o Outcome) Status() string { return DefaultStatusOf(o) }

// StatusWith 按注入的映射钩子取状态词；mapper 为 nil 时回落 DefaultStatusOf
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

// Route 收尾段的五路路由（**唯一判据**；finalize.Route 是它的动作投影，
// issue #355）。
//
// # 五路
//
//	接管（steer.ErrSteered）      takeover   不落终态回 created
//	会话墙钟（ErrSessionDeadline） timed_out  终态
//	用户取消（契约/宿主哨兵）      cancelled  终态
//	其余 ctx 已取消（进程停机）    shutdown   不落终态，留给恢复循环
//	ctx 未取消                     none       无收敛动作
//
// # 次序纪律（硬约束）
//
// **timed_out 臂必须排在停机臂（`ctx.Err() != nil`）之前。** 墙钟到点时
// ctx 同样已取消（DeadlineExceeded），停机臂的条件对它同样成立——次序写反，
// 每次会话超时都会被判成停机、不落终态，会话永久滞留 running。
// 测试里用「已取消 + cause=ErrSessionDeadline」的 ctx 钉住（该 ctx 同时满足
// 停机臂的条件，走错臂当场红）。**这一条判据只在本函数存在**：finalize 只做
// Outcome→Action 的投影，不再另写路由表与臂序。
//
// 接管臂最先、用户取消臂先于超时臂：两者都是显式意图（新输入续跑 / 用户
// 主动停止），与墙钟同时出现时不把会话判成 timed_out（与 finalize 既有
// 臂序一致）。timed_out 与 shutdown 的相对次序仍是硬约束。
//
// 五路的前提是各有自己的 cause：
//
//   - 墙钟：WithTimeoutCause(..., ErrSessionDeadline)（本包 WithCause）；
//   - 用户取消：context.WithCancelCause(contract.ErrUserCancelled)（全仓唯一
//     身份，缺省即认），宿主自有哨兵可经 userCancel 追加；
//   - 接管：context.WithCancelCause(steer.ErrSteered)（steer.Signal）；
//   - 停机：父 ctx 取消传播下来的 cause（或裸 cancel）。
//
// 裸 cancel 与停机在 Cause 上不可分：宿主要区分用户取消就必须给取消一个
// 专属 cause（推荐 contract.ErrUserCancelled）。
func Route(ctx context.Context, userCancel ...error) Outcome {
	if ctx.Err() == nil {
		return OutcomeNone
	}
	cause := context.Cause(ctx)
	// ① 接管最先：新输入续跑是显式意图，不落终态也不留恢复循环。
	if errors.Is(cause, steer.ErrSteered) {
		return OutcomeTakeover
	}
	// ② 用户取消：内核契约哨兵 + 宿主注入的自有哨兵（可给多个）。
	// 与 finalize 既有臂序一致：显式取消优先于墙钟（两者同时成立时按用户意图）。
	if errors.Is(cause, contract.ErrUserCancelled) {
		return OutcomeCancelled
	}
	for _, sentinel := range userCancel {
		if sentinel != nil && errors.Is(cause, sentinel) {
			return OutcomeCancelled
		}
	}
	// ③ timed_out 先于停机臂（次序纪律）：它同时满足停机臂的条件。
	if errors.Is(cause, ErrSessionDeadline) {
		return OutcomeTimedOut
	}
	// ④ 进程停机：不落终态，留给恢复循环。
	return OutcomeShutdown
}

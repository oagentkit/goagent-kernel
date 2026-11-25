// finalize.go —— 终态写入 + 终态事件的同事务与 Outbox 补做（issue #272）。
//
// # 复现结论（先复现再定性）
//
// 旧路径的动作序是「ConvergeCAS → CloseBracket → Pub」。ConvergeCAS 的
// 宿主实现原子完成终态写入与结算，但 Pub 是 fire-and-forget：发布失败时
// 会话已经终态、事件永久缺失，而会话不再出现在 running 列表里，下一轮
// 扫描也不会再发。TestSettlementEventSplitReproduced 把这条分裂钉成可跑
// 事实 ⇒ **坐实**（只实现 Store 的宿主）。
//
// # 修法：Finalize Intent + Outbox
//
//   - FinalizeStore.Finalize：条件迁移 + 结算 + 终态事件行**同一事务**；
//     返回 false = 他方已处理（零写入，不重复写 Outbox 行）；
//   - FlushFinalizeOutbox：逐行投递，成功才标记已投递；投递/标记失败时行
//     保留，下一轮或进程重启后补做；
//   - FinalizeEvent.ID 稳定（finalize:<session>:<terminal-status>），重放
//     使用同一 ID ⇒ 订阅方可按 ID 去重，投递是至少一次而不是「可能丢」。
//
// # 未实现 FinalizeStore 时
//
// 仍走旧路径（ConvergeCAS + Pub），但 FinalizeGuarantee 如实报告
// Atomic=false / ConfirmedDelivery=false。这是**显式降级**：本包不假装
// 「终态事件归宿主」就等于边界清楚——状态与事件的分裂可以被观测到。
package reconcile

import (
	"context"
	"fmt"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/eventbus"
)

// finalizeOutboxBatch 一次补做的最大行数（宿主周期调用 FlushFinalizeOutbox；
// 单批有界避免长事务/长扫描，剩余行留给下一轮）。
const finalizeOutboxBatch = 64

// FinalizeEvent 一条终态事件（Outbox 行的逻辑载荷）。
//
// ID 由内核生成且稳定（同一会话的同一终态迁移恒为同一 ID）；宿主落库时
// 应把它作为唯一键。Type/Level/Payload 与旧 Pub 发送的 session_finished
// 逐字一致，订阅方不需要区分两条路径。
type FinalizeEvent struct {
	// ID 稳定事件标识（幂等键；重放不重复生效的依据）。
	ID string
	// SessionID 会话标识。
	SessionID string
	// CaseID 案例标识（旧 Pub 的透传字段；可为空）。
	CaseID string
	// Type 事件类型（eventbus 词表；终态恒为 session_finished）。
	Type string
	// Level 事件级别（与旧路径一致："error"）。
	Level string
	// Payload 事件载荷（至少含 by 归因）。
	Payload map[string]any
}

// FinalizeStore Store 的可选能力：终态写入与终态事件同一事务（issue #272）。
//
// 实现本接口 = 声明下面三件事：
//
//   - Finalize **原子**：from→to 条件迁移、宿主侧的恢复报告/排队结算/
//     悬挂环节收口（与 Store.ConvergeCAS 同义）与 ev 的 Outbox 行写在
//     一次事务里；false = 他方已处理（零写入，不得写第二行）；
//   - PendingFinalEvents 返回**未投递**的 Outbox 行（按写入序，limit 有界）；
//   - MarkFinalEventDelivered **幂等**：同一 ID 重复标记不报错、不产生
//     第二行（已投递的行不再出现在 Pending 里）。
//
// 不实现本接口的 Store 仍可用：Reconciler 走旧路径，FinalizeGuarantee
// 报降级。
type FinalizeStore interface {
	Store
	Finalize(ctx context.Context, sessionID, from, to string, cause Cause, ev FinalizeEvent) (bool, error)
	PendingFinalEvents(ctx context.Context, limit int) ([]FinalizeEvent, error)
	MarkFinalEventDelivered(ctx context.Context, eventID string) error
}

// FinalizeDispatcher 可确认投递的终态事件发布面（issue #272）。
//
// 返回 error = 未投递（Outbox 行保留待补做）；nil = 已投递。与 Pub 的
// 区别只有一件事：**失败可被判**。未配置时内核回退到 Pub，并把该行的
// 投递视作已完成（fire-and-forget）——这是显式降级，由 FinalizeGuarantee
// 的 ConfirmedDelivery 字段报告。
type FinalizeDispatcher func(ctx context.Context, ev FinalizeEvent) error

// FinalizeGuarantee 终态事件的交付保证形态（可查事实，不是散文）。
type FinalizeGuarantee struct {
	// Atomic 终态写入与终态事件是否同一事务（Store 实现 FinalizeStore）。
	Atomic bool
	// ConfirmedDelivery 投递结果是否可确认（配置了 FinalizeDispatch）。
	// false 时事件行仍可能同事务落库，但「已投递」只能按调用成功推定。
	ConfirmedDelivery bool
}

// FinalizeGuarantee 报告本 Reconciler 的终态事件交付形态（issue #272）。
//
// 宿主启动自检可断言它为 (true, true)；只拿到 (false, false) 的部署必须
// 接受「终态后事件可能缺失」的旧口径，或补 FinalizeStore + FinalizeDispatch。
func (r *Reconciler) FinalizeGuarantee() FinalizeGuarantee {
	return FinalizeGuarantee{
		Atomic:            r.finalizer != nil,
		ConfirmedDelivery: r.cfg.FinalizeDispatch != nil,
	}
}

// finalizeEvent 构造终态事件（ID 稳定，重放同键）。
func (r *Reconciler) finalizeEvent(s Session, cause Cause) FinalizeEvent {
	return FinalizeEvent{
		ID:        fmt.Sprintf("finalize:%s:%s", s.ID, contract.StatusFailed),
		SessionID: s.ID,
		CaseID:    s.CaseID,
		Type:      eventbus.EventSessionFinished,
		Level:     "error",
		Payload: map[string]any{
			"error": "编排中断恢复：会话按失败收敛（by=" + cause.By + "）",
			"by":    cause.By,
		},
	}
}

// reclaimFinalize FinalizeStore 路径的收敛体：同事务写终态 + Outbox 行，
// 然后关闭括号、补做投递（失败并入聚合错误，行保留）。
func (r *Reconciler) reclaimFinalize(ctx context.Context, s Session, cause Cause) (bool, error) {
	ev := r.finalizeEvent(s, cause)
	ok, err := r.finalizer.Finalize(ctx, s.ID, contract.StatusRunning, contract.StatusFailed, cause, ev)
	if err != nil {
		return false, errors.Wrap(err, "终态写入/Outbox 同事务失败", "session", s.ID)
	}
	if !ok {
		return false, nil // 他方已处理（条件迁移零行，Outbox 也不得写）
	}
	var errs error
	if berr := r.store.CloseBracket(ctx, s.ID, cause.By); berr != nil {
		errs = errors.Append(errs, errors.Wrap(berr, "关闭 turn 括号失败（收敛已成立）",
			"session", s.ID, "closed_by", cause.By))
	}
	if _, ferr := r.FlushFinalizeOutbox(ctx); ferr != nil {
		errs = errors.Append(errs, ferr)
	}
	return true, errs
}

// FlushFinalizeOutbox 幂等补做终态事件 Outbox（issue #272）。
//
// 语义：
//   - 只有 Store 实现 FinalizeStore 时有事可做；否则返回 (0, nil)；
//   - 逐行投递（保序）：一条失败就停在它，不越过它投后面的行；
//   - 投递成功后才标记已投递；标记失败时行保留（下一轮重投，ID 稳定）；
//   - 已投递的行不再出现在 PendingFinalEvents 里，重放不会重复发送。
//
// 返回实际标记成功的行数与聚合错误（投递/标记失败都在里面）。
//
// RecoverBootOrphans / RecoverInterrupted 每轮开始时也会先调用一次：进程
// 重启或上一轮「Finalize 已提交、Flush 未完成」留下的行无需宿主额外接线
// 就会重试（宿主也可按自己的节奏单独调用，两者幂等）。
func (r *Reconciler) FlushFinalizeOutbox(ctx context.Context) (int, error) {
	if r.finalizer == nil {
		return 0, nil
	}
	pending, err := r.finalizer.PendingFinalEvents(ctx, finalizeOutboxBatch)
	if err != nil {
		return 0, errors.Wrap(err, "读终态事件 Outbox 失败")
	}
	delivered := 0
	var errs error
	for _, ev := range pending {
		if derr := r.dispatchFinalize(ctx, ev); derr != nil {
			errs = errors.Append(errs, errors.Wrap(derr,
				"投递终态事件失败（Outbox 行保留待补做）",
				"session", ev.SessionID, "event", ev.ID))
			break // 保序：失败行之后的不越过它投递
		}
		if merr := r.finalizer.MarkFinalEventDelivered(ctx, ev.ID); merr != nil {
			errs = errors.Append(errs, errors.Wrap(merr,
				"标记终态事件已投递失败（行保留，重放按 Event.ID 幂等）",
				"session", ev.SessionID, "event", ev.ID))
			break
		}
		delivered++
	}
	return delivered, errs
}

// dispatchFinalize 投递一条终态事件：优先可确认的 FinalizeDispatch，
// 未配置时回退旧 Pub（fire-and-forget，调用即视为已投递）。
func (r *Reconciler) dispatchFinalize(ctx context.Context, ev FinalizeEvent) error {
	if r.cfg.FinalizeDispatch != nil {
		return r.cfg.FinalizeDispatch(ctx, ev)
	}
	r.pub(ev.SessionID, ev.CaseID, nil, ev.Type, ev.Level, ev.Payload)
	return nil
}

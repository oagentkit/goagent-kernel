// turn.go —— INV3 轮级括号配对（issue #171）。
//
// INV1/INV2（watchdog.go）是**工具调用级**配对：声明↔结果。本文件是它的外层——
// **轮级**配对：turn_started 开括号、turn_ended 闭括号。任何以事件流呈现轮生命
// 周期的宿主都有这对括号，且都面对同一组异常态：无归因的闭合、重复闭合、停机留
// 开放（**合法**）、开放超时（泄漏）。
//
// # 只告警不裁决
//
// 与既有 INV 同一章程：本文件只登记、校验与归因告警，**闭合动作仍由宿主恢复
// 路径执行**（recovery/boot-reconcile/panic 各自发 closed_by 的 turn_ended）。
//
// # 判得了什么，判不了什么（刻意写明的边界）
//
//   - 判得了：闭合有没有归因（closed_by ∈ 词表）、status 是否在 contract 终态
//     词表内、闭合有没有对应的开括号、开放括号是否超 TTL。
//   - 判不了：「闭合 status 与会话终态一致」。那需要会话存储面（本轮到底收敛成
//     什么），watchdog 没有存储面，也不该有——语义裁决归宿主恢复路径。本包只判
//     词表成员与配对/归因，**不判断归因内容对不对**（例如 recovery 是否真的由
//     恢复循环发起）。
//
// # 停机留开放 = 合法
//
// 进程停机路径刻意不落 turn_ended，让括号保持开放、由下次启动的对账/恢复关闭。
// 因此 Run 因 ctx 取消退出时**不做扫尾告警**：扫尾只在进程存活、事件持续到达时
// 按 TTL 触发。否则每个宿主的正常停机都会误报一片泄漏。
package watchdog

import (
	"time"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/eventbus"
)

// 闭合归因（closed_by）缺省词表。宿主可在 Config.ClosedBy 里扩充，
// 内核只判「有没有（在册的）归因」，不判归因语义。
// 取值引用 eventbus 的 closed_by 词表（issue #391）：本包是消费方、reconcile
// 是生产方，两包各抄一份时本包的缺省表漏了 reconcile 的死主/租约归因，把
// 合法收敛报成「无归因」。词表只有一份（eventbus.ClosedBy* + ClosedByDefaults）。
const (
	// ClosedByLoop 收尾漏斗正常出口（宿主编排循环自己闭合）。
	ClosedByLoop = eventbus.ClosedByLoop
	// ClosedByRecovery 恢复循环归因（中断会话的统一收敛体）。
	ClosedByRecovery = eventbus.ClosedByRecovery
	// ClosedByBootReconcile 启动对账归因（发版重启后的孤儿回收）。
	ClosedByBootReconcile = eventbus.ClosedByBootReconcile
	// ClosedByPanic 编排 panic 归因（recoverPanic 就地收口）。
	ClosedByPanic = eventbus.ClosedByPanic
	// ClosedByDeadOwner 他副本死主回收归因（reconcile.ByDeadOwner 的同源值）。
	ClosedByDeadOwner = eventbus.ClosedByDeadOwner
	// ClosedByDeadLease 他副本租约过期回收归因（reconcile.ByDeadLease 的同源值）。
	ClosedByDeadLease = eventbus.ClosedByDeadLease
)

// defaultClosedBy 缺省归因词表：所有宿主共有的收尾路径 + 内核 reconcile 的
// 四路归因（issue #391 单源化；不再手抄字面量）。
var defaultClosedBy = eventbus.ClosedByDefaults()

// 轮括号告警 reason 词表（EventTurnBracketViolation 的 payload["reason"]）。
const (
	// ReasonTurnEndUnattributed 闭合无归因，或归因不在宿主注册的词表内。
	ReasonTurnEndUnattributed = "turn_end_unattributed"
	// ReasonTurnEndUnknownStatus 闭合携带的 status 不在 contract 终态词表内
	// （含缺失）。语义裁决归宿主，这里只判词表成员。
	ReasonTurnEndUnknownStatus = "turn_end_unknown_status"
	// ReasonTurnEndUnopened 闭合没有对应的开括号（重复闭合 / 事件乱序丢开括号）。
	ReasonTurnEndUnopened = "turn_end_unopened"
	// ReasonTurnOpenLeak 开放括号超 TTL 未闭合（进程存活时按 TTL 清扫）。
	// 停机退出不触发——停机留开放是合法态。
	ReasonTurnOpenLeak = "turn_open_leak"
)

// Config INV3 轮括号的可选配置。零值 = 缺省语义（New 即零值配置）。
type Config struct {
	// ClosedBy 宿主注册的闭合归因词表。缺省（空）时使用 defaultClosedBy；
	// 填了则在缺省词表基础上**扩充**（不是替换）——缺省值含所有宿主共有的
	// 收尾路径与内核 reconcile 的四路归因，只增不减才能避免「宿主少写一个值
	// 就把合法归因判成红」。
	ClosedBy []string
	// OpenTTL 开放括号的泄漏判定阈值；缺省（<=0）复用 openIdleTTL。
	OpenTTL time.Duration
	// SweepEvery 清扫间隔；缺省（<=0）复用 sweepEvery。
	SweepEvery time.Duration
}

// turnBracket 一个开放中的轮括号。
type turnBracket struct {
	openedAt time.Time
	caseID   string
}

// newTurnState 按配置初始化轮括号状态。
func newTurnState(cfg Config) (map[string]bool, map[string]turnBracket, time.Duration, time.Duration) {
	closedBy := make(map[string]bool, len(defaultClosedBy)+len(cfg.ClosedBy))
	for _, r := range defaultClosedBy {
		closedBy[r] = true
	}
	for _, r := range cfg.ClosedBy {
		if r != "" {
			closedBy[r] = true
		}
	}
	ttl := cfg.OpenTTL
	if ttl <= 0 {
		ttl = openIdleTTL
	}
	every := cfg.SweepEvery
	if every <= 0 {
		every = sweepEvery
	}
	return closedBy, map[string]turnBracket{}, ttl, every
}

// observeTurnStarted 开括号（按 sessionID）。
//
// 同一 session 已有开放括号时不刷新 openedAt：重复开括号本身就是「上一轮丢了
// 闭合」的信号，保留最早的时刻才能让 TTL 泄漏按时显形；newest caseID 补进
// 诊断字段（caseID 只是事件元数据，不是归因判据）。
func (w *Watcher) observeTurnStarted(ev eventbus.Event) {
	if ev.SessionID == "" {
		return
	}
	if b, ok := w.turnOpen[ev.SessionID]; ok {
		if ev.CaseID != "" {
			b.caseID = ev.CaseID
			w.turnOpen[ev.SessionID] = b
		}
		return
	}
	w.turnOpen[ev.SessionID] = turnBracket{openedAt: time.Now(), caseID: ev.CaseID}
}

// observeTurnEnded 闭括号：配对 + 归因 + status 词表三条独立判定，各自告警。
//
// 三条判据**互不遮蔽**：一个无开括号的闭合同时缺归因时，两条告警都要报——
// 只报一条会把「补归因」误当成完整修法（红点指向错的地方是假红的经典形态）。
func (w *Watcher) observeTurnEnded(ev eventbus.Event) {
	status, _ := ev.Payload["status"].(string)
	closedBy, _ := ev.Payload["closed_by"].(string)

	if _, ok := w.turnOpen[ev.SessionID]; !ok {
		w.alertTurn(ev, ReasonTurnEndUnopened, map[string]any{
			"status": status, "closed_by": closedBy,
		})
	} else {
		delete(w.turnOpen, ev.SessionID)
	}

	if !w.closedBy[closedBy] {
		w.alertTurn(ev, ReasonTurnEndUnattributed, map[string]any{
			"status": status, "closed_by": closedBy,
		})
	}
	if !contract.IsTerminalStatus(status) {
		w.alertTurn(ev, ReasonTurnEndUnknownStatus, map[string]any{
			"status": status, "closed_by": closedBy,
		})
	}
}

// sweepTurns 开放括号的 TTL 清扫（由 maybeSweep 内联调用）。
//
// 只在 Run 的事件消费循环里触发——Run 因 ctx 取消退出时不会调用本函数，
// 「停机留开放」因此不会被误报成泄漏。
func (w *Watcher) sweepTurns(now time.Time) {
	for sid, b := range w.turnOpen {
		age := now.Sub(b.openedAt)
		if age <= w.turnTTL {
			continue
		}
		delete(w.turnOpen, sid)
		w.alertTurn(eventbus.Event{SessionID: sid, CaseID: b.caseID, Type: eventbus.EventTurnStarted},
			ReasonTurnOpenLeak, map[string]any{
				"opened_at": b.openedAt.UTC().Format(time.RFC3339),
				"age":       age.String(),
			})
	}
}

// alertTurn 发一条轮括号告警（warn 级；pub 未装配时静默丢弃，不阻塞事件流）。
func (w *Watcher) alertTurn(ev eventbus.Event, reason string, extra map[string]any) {
	if w.pub == nil {
		return
	}
	payload := map[string]any{"reason": reason, "event": ev.Type}
	for k, v := range extra {
		payload[k] = v
	}
	w.pub(ev.SessionID, ev.CaseID, nil, eventbus.EventTurnBracketViolation, "warn", payload)
}

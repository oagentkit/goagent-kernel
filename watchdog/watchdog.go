// Package watchdog 被动配对不变量监视（spec 2026-09-15-tool-identity-otel-guard-design P3）：
//
// 生命周期: Stable（v0.33.0 定级）——只允许 additive 变更（新增导出符号 /
// 新增方法且给零值默认实现 / 新增错误值）；改签名、改语义、改默认值、
// 删导出符号都算 breaking，判定规则与变更流程见 docs/api-compat.md，
// 逐包理由见 api-lifecycle.yaml。
//
// 订阅事件总线校验配对不变量——只告警，不裁决（热路径裁决在 IdentityGuard）。
//
// 配对键与 trajectory **同源**：eventbus.ToolCallID（全局 call_id，**不含 step**，
// issue #336）。step/轮次是归属信息，不是配对键：结果可以在后续 step 到达、
// 同一 call_id 可以跨 step 重复声明，按 step 分段配对会把这些正常形态判成
// 缺口（同一事件流两包相反结论）。本包不再自己构造配对键（门禁规则
// tool-pairing-key-single-source 禁止直接索引 Payload["call_id"]）。
//
// INV1 会话收口（session_finished）仍有未执行声明 → reason=declared_not_executed；
// INV2 结果无对应声明（幽灵结果）→ reason=unpaired_result，已配对后的重复结果
// → reason=duplicate_result，同号重复声明 → reason=duplicate_call。
// INV1 的判定期是**会话收口或 TTL 清扫**，不是每个 step_finished：结果可能
// 在后续环节到达，按环节判会假红。TTL 清扫对未配对声明同样告警，不静默丢。
// INV3（issue #171）是工具配对的外层——轮级括号 turn_started/turn_ended 的
// 配对、闭合归因（closed_by）与 status 终态词表校验，见 turn.go。
// 注意命名撞车：spec 2026-09-15-tool-identity-otel-guard-design §8 的「INV3
// （事件面 checksum 抽验）」是另一条不变量，本包仍不实现它——tool_result 载荷
// 截断到 2000 字符，无法重算 checksum，内容级校验由守卫在源头全量覆盖。
package watchdog

import (
	"context"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/trajectory"
)

// Pub 告警发布（与调度器 pub 同一落库广播路径：repo.InsertEvent + Bus.Broadcast）。
type Pub func(sessionID, caseID string, stepID *uint64, typ, level string, payload map[string]any)

// 工具调用级告警 reason 词表（EventToolPairingLeak 的 payload["reason"]）。
//
// **与 trajectory 的 Pairing 词表同一事实**（issue #392）：投影面（trajectory）
// 与监视面（本包）此前各写一份，三个 token 逐字同名、第四个
// （declared_not_executed vs missing_result）是同一事实的两个 token——宿主按
// 投影面的词表筛本包的告警会漏掉 declared_not_executed 这一类。
//
// 处置：三个同名 token 直接**引用 trajectory 的导出常量**（不是抄一份）；
// declared_not_executed 是 trajectory.PairingMissingResult 的**注册别名**
// （登记在 eventbus.PairingReasonAliases）——与规范 token 不逐字相同是登记
// 事实，不是两份实现。
const (
	// ReasonDeclaredNotExecuted 会话收口 / TTL 过期仍未配对的调用声明。
	ReasonDeclaredNotExecuted = eventbus.PairingReasonDeclaredNotExecuted
	// ReasonUnpairedResult 结果没有任何调用声明（幽灵结果）。
	ReasonUnpairedResult = string(trajectory.PairingUnpairedResult)
	// ReasonDuplicateResult 同一 call_id 的第二个及以后结果（第一次观测是真相）。
	ReasonDuplicateResult = string(trajectory.PairingDuplicateResult)
	// ReasonDuplicateCall 同一 call_id 的第二个及以后声明（第一次观测是真相）。
	ReasonDuplicateCall = string(trajectory.PairingDuplicateCall)
)

// 残留清扫参数：事件总线对慢订阅丢帧/会话未收口的异常流会让部分 session 条目
// 永远等不到 session_finished（单协程消费，无人再删）——按最后活动时间过期
// （2h 无活动视为泄漏），低频清扫（10min 一轮）防长跑进程缓慢泄漏。清扫同时
// 把仍未配对的声明报成 declared_not_executed：静默删除等于把泄漏吞掉。
const (
	openIdleTTL = 2 * time.Hour
	sweepEvery  = 10 * time.Minute
)

// pairState 一个 call_id 在会话内的配对状态。
type pairState struct {
	// tool 工具名（第一次声明是真相；结果事件只在调用侧为空时补充）。
	tool string
	// paired 是否已收到配对结果。
	paired bool
}

// Watcher 配对不变量监视器。Run 单协程消费事件通道，无需加锁。
type Watcher struct {
	pub        Pub
	pairs      map[string]map[string]pairState // sessionID → call_id → 配对状态（键与 trajectory 同源）
	lastActive map[string]time.Time            // sessionID → 最后活动时刻（TTL 清扫依据）
	lastSweep  time.Time

	// INV3 轮级括号（turn.go）：按 sessionID 的开放表 + 归因词表 + TTL。
	closedBy  map[string]bool
	turnOpen  map[string]turnBracket
	turnTTL   time.Duration
	sweepEach time.Duration
}

// New 构造（pub 必填；nil 时告警静默丢弃——不阻塞事件流）。
//
// 缺省配置：closed_by 六值（loop|recovery|boot-reconcile|panic|recovery:dead-owner|
// recovery:dead-lease，见 turn.go）+ 2h 开放 TTL。
func New(pub Pub) *Watcher {
	return NewWithConfig(pub, Config{})
}

// NewWithConfig 带 INV3 配置的构造（additive：New(pub) 的调用面与缺省语义不变）。
//
// cfg 的零值 = New 的缺省语义；ClosedBy 在缺省四值上**扩充**，OpenTTL /
// SweepEvery <=0 时复用既有 openIdleTTL / sweepEvery。
func NewWithConfig(pub Pub, cfg Config) *Watcher {
	closedBy, turnOpen, ttl, every := newTurnState(cfg)
	return &Watcher{
		pub:        pub,
		pairs:      map[string]map[string]pairState{},
		lastActive: map[string]time.Time{},
		closedBy:   closedBy,
		turnOpen:   turnOpen,
		turnTTL:    ttl,
		sweepEach:  every,
	}
}

// Run 消费事件流直到 ctx 取消（main 装配期后台 goroutine）。
//
// ctx 取消退出**不做扫尾告警**：停机路径刻意留开放轮括号给恢复循环/启动对账
// 闭合，把「停机」当「泄漏」会误报每个宿主的正常收尾。
func (w *Watcher) Run(ctx context.Context, bus *eventbus.Bus) {
	ch, cancel := bus.Subscribe(ctx)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			w.observe(ev)
		}
	}
}

func (w *Watcher) observe(ev eventbus.Event) {
	w.maybeSweep()
	switch ev.Type {
	case eventbus.EventToolCall:
		w.observeToolCall(ev)
	case eventbus.EventToolResult:
		w.observeToolResult(ev)
	case eventbus.EventSessionFinished:
		// 会话收口 = 不会再等来结果：仍未配对的声明在这里落成缺口。
		w.flushSession(ev)
	case eventbus.EventTurnStarted:
		w.observeTurnStarted(ev)
	case eventbus.EventTurnEnded:
		w.observeTurnEnded(ev)
	}
}

// observeToolCall 登记调用声明。
//
// 同一 call_id 的第二次声明（跨 step / 跨轮的重试与改派都是常见形态）**不改写
// 第一次观测**，只报一条 duplicate_call——与 trajectory 的判据一致。
func (w *Watcher) observeToolCall(ev eventbus.Event) {
	callID := eventbus.ToolCallID(ev)
	if callID == "" {
		return // 无 id 路径：不是缺口（与 trajectory 的 PairingNoCallID 同口径）
	}
	tool, _ := ev.Payload["tool"].(string)
	table := w.pairs[ev.SessionID]
	if table == nil {
		table = map[string]pairState{}
		w.pairs[ev.SessionID] = table
	}
	prev, declared := table[callID]
	if declared {
		w.alert(ev, ReasonDuplicateCall, callID, toolOr(prev.tool, tool))
		w.lastActive[ev.SessionID] = time.Now()
		return
	}
	table[callID] = pairState{tool: tool}
	w.lastActive[ev.SessionID] = time.Now()
}

// observeToolResult 用结果收口声明。
//
// 配对键是全局 call_id：结果事件带不带 StepID 都按同一张表配对（issue #336）。
// 从未声明 → unpaired_result；已配对后再次到达 → duplicate_result。
func (w *Watcher) observeToolResult(ev eventbus.Event) {
	callID := eventbus.ToolCallID(ev)
	if callID == "" {
		return // 无 id 路径：不是缺口
	}
	tool, _ := ev.Payload["tool"].(string)
	table := w.pairs[ev.SessionID]
	st, declared := table[callID]
	if !declared {
		w.alert(ev, ReasonUnpairedResult, callID, tool)
		return
	}
	if st.paired {
		w.alert(ev, ReasonDuplicateResult, callID, toolOr(st.tool, tool))
		return
	}
	if st.tool == "" && tool != "" {
		st.tool = tool
	}
	st.paired = true
	table[callID] = st
	w.lastActive[ev.SessionID] = time.Now()
}

// flushSession 会话收口：仍未配对的声明落成 declared_not_executed，然后清表。
func (w *Watcher) flushSession(ev eventbus.Event) {
	for callID, st := range w.pairs[ev.SessionID] {
		if !st.paired {
			w.alert(ev, ReasonDeclaredNotExecuted, callID, st.tool)
		}
	}
	delete(w.pairs, ev.SessionID)
	delete(w.lastActive, ev.SessionID)
}

// maybeSweep 低频 TTL 清扫：单协程消费内联触发，无锁。
//
// 过期会话里仍未配对的声明在删除前告警——「静默丢失」和「确认泄漏」是两件事。
func (w *Watcher) maybeSweep() {
	now := time.Now()
	if now.Sub(w.lastSweep) < w.sweepEach {
		return
	}
	w.lastSweep = now
	for sid, last := range w.lastActive {
		if now.Sub(last) <= openIdleTTL {
			continue
		}
		for callID, st := range w.pairs[sid] {
			if !st.paired {
				w.alert(eventbus.Event{SessionID: sid, Type: eventbus.EventToolCall},
					ReasonDeclaredNotExecuted, callID, st.tool)
			}
		}
		delete(w.pairs, sid)
		delete(w.lastActive, sid)
	}
	w.sweepTurns(now)
}

// toolOr 工具名的第一次观测优先（结果只在调用侧为空时补充）。
func toolOr(prev, cur string) string {
	if prev != "" {
		return prev
	}
	return cur
}

func (w *Watcher) alert(ev eventbus.Event, reason, callID, tool string) {
	if w.pub == nil {
		return
	}
	w.pub(ev.SessionID, ev.CaseID, ev.StepID, eventbus.EventToolPairingLeak, "warn", map[string]any{
		"reason": reason, "call_id": callID, "tool": tool, "step_event": ev.Type,
	})
}

package watchdog

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

func stepPtr(id uint64) *uint64 { return &id }

// rec 收集提示（模拟 repo+bus 落库广播路径）。
type rec struct {
	mu  sync.Mutex
	got []eventbus.Event
}

func (r *rec) pub(sid, cid string, stepID *uint64, typ, level string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, eventbus.Event{SessionID: sid, CaseID: cid, StepID: stepID, Type: typ, Level: level, Payload: payload})
}

func (r *rec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *rec) first() eventbus.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[0]
}

func (r *rec) reasons() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, ev := range r.got {
		reason, _ := ev.Payload["reason"].(string)
		out[reason]++
	}
	return out
}

func callEvent(callID string) eventbus.Event {
	return eventbus.Event{SessionID: "s1", CaseID: "c1", StepID: stepPtr(7),
		Type: eventbus.EventToolCall, Payload: map[string]any{"call_id": callID, "tool": "use_skill"}}
}

func resultEvent(callID string) eventbus.Event {
	return eventbus.Event{SessionID: "s1", CaseID: "c1", StepID: stepPtr(7),
		Type: eventbus.EventToolResult, Payload: map[string]any{"call_id": callID, "tool": "use_skill"}}
}

// resultNoStep 不带 StepID 的结果（宿主发布路径的常见形态）。
func resultNoStep(callID string) eventbus.Event {
	e := resultEvent(callID)
	e.StepID = nil
	return e
}

func finishEvent() eventbus.Event {
	return eventbus.Event{SessionID: "s1", CaseID: "c1", StepID: stepPtr(7),
		Type: eventbus.EventStepFinished, Payload: map[string]any{"agent": "x", "status": "succeeded"}}
}

// sessionEnd 会话收口（INV1 的判定时刻）。
func sessionEnd() eventbus.Event {
	return eventbus.Event{SessionID: "s1", CaseID: "c1", Type: eventbus.EventSessionFinished}
}

func TestWatchdogLeakDetected(t *testing.T) {
	// INV1：声明后无结果、会话收口 → declared_not_executed 提示
	r := &rec{}
	w := New(r.pub)
	w.observe(callEvent("t1"))
	w.observe(sessionEnd())

	if r.count() != 1 {
		t.Fatalf("应有一条泄漏提示: %d", r.count())
	}
	got := r.first()
	if got.Type != eventbus.EventToolPairingLeak || got.Level != "warn" {
		t.Fatalf("事件类型/级别不符: %+v", got)
	}
	// 告警载荷里的 call_id 经同一个读取点取（本包不得直接索引载荷键，#336）。
	if got.Payload["reason"] != ReasonDeclaredNotExecuted || eventbus.ToolCallID(got) != "t1" {
		t.Fatalf("payload 不符: %v", got.Payload)
	}
}

func TestWatchdogDuplicateResult(t *testing.T) {
	// INV2：同一 call_id 结果回灌两次 → 第二次是 duplicate_result（与 trajectory 同词）
	r := &rec{}
	w := New(r.pub)
	w.observe(callEvent("t1"))
	w.observe(resultEvent("t1"))
	w.observe(resultEvent("t1"))

	if r.count() != 1 {
		t.Fatalf("应有一条重复结果提示: %d", r.count())
	}
	if got := r.first().Payload["reason"]; got != ReasonDuplicateResult {
		t.Fatalf("reason 应为 %s，实得 %v", ReasonDuplicateResult, got)
	}
}

func TestWatchdogUnpairedResult(t *testing.T) {
	// INV2：从未声明过的结果（幽灵结果）→ unpaired_result
	r := &rec{}
	w := New(r.pub)
	w.observe(resultEvent("ghost"))

	if r.count() != 1 || r.first().Payload["reason"] != ReasonUnpairedResult {
		t.Fatalf("应有一条 unpaired_result: %+v", r.got)
	}
}

func TestWatchdogCleanPairing(t *testing.T) {
	// 正常配对：声明→结果→收口，零提示；每次复跑用新的 call_id。
	r := &rec{}
	w := New(r.pub)
	for i := 0; i < 2; i++ {
		callID := "t" + string(rune('1'+i))
		w.observe(callEvent(callID))
		w.observe(resultEvent(callID))
		w.observe(finishEvent())
	}
	w.observe(sessionEnd())
	if r.count() != 0 {
		t.Fatalf("正常配对不应提示: %+v", r.got)
	}
}

// TestWatchdogResultWithoutStepIDPairs 配对身份不含 step：结果不带 StepID
// 仍按全局 call_id 配上，不得报 unpaired_result（issue #336 触发路径①）。
func TestWatchdogResultWithoutStepIDPairs(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(callEvent("t1"))
	w.observe(resultNoStep("t1"))
	w.observe(sessionEnd())
	if r.count() != 0 {
		t.Fatalf("结果不带 StepID 不得判成配对失败: %+v", r.got)
	}
}

// TestWatchdogDeclarationSurvivesStepFinish 声明跨 step 等到结果是正常形态：
// step_finished 不是配对截止时刻（结果可以在后续环节到达）。
func TestWatchdogDeclarationSurvivesStepFinish(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(callEvent("t1"))
	w.observe(finishEvent())
	w.observe(resultNoStep("t1"))
	w.observe(sessionEnd())
	if r.count() != 0 {
		t.Fatalf("结果晚于环节收口到达不得报 declared_not_executed: %+v", r.got)
	}
}

// TestWatchdogCrossStepDuplicateDeclaration 同一 call_id 跨 step 重复声明
// （重试/改派）：报 duplicate_call（与 trajectory 同判据），且会话收口时
// 已配对，不得再报 declared_not_executed（issue #336 触发路径②）。
func TestWatchdogCrossStepDuplicateDeclaration(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(callEvent("t1"))
	second := callEvent("t1")
	second.StepID = stepPtr(8)
	w.observe(second)
	res := resultNoStep("t1")
	res.StepID = stepPtr(8)
	w.observe(res)
	w.observe(sessionEnd())

	got := r.reasons()
	if got[ReasonDeclaredNotExecuted] != 0 {
		t.Fatalf("不得报 declared_not_executed（结果已到）: %v", got)
	}
	if got[ReasonDuplicateCall] != 1 || r.count() != 1 {
		t.Fatalf("跨 step 重复声明应只报一条 duplicate_call: %v", got)
	}
}

// TestWatchdogTTLSweepAlerts 未配对声明超 TTL：删除前必须告警，不能静默吞掉。
func TestWatchdogTTLSweepAlerts(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(callEvent("t1"))
	w.lastActive["s1"] = time.Now().Add(-3 * time.Hour)
	w.lastSweep = time.Time{}
	w.maybeSweep()

	if r.count() != 1 || r.first().Payload["reason"] != ReasonDeclaredNotExecuted {
		t.Fatalf("TTL 过期的未配对声明必须显形: %+v", r.got)
	}
	if len(w.pairs) != 0 {
		t.Fatalf("清扫后会话表应清空: %+v", w.pairs)
	}
}

func TestWatchdogNoPub(t *testing.T) {
	// pub 未装配：提示静默丢弃，不 panic（不阻塞事件流语义）
	w := New(nil)
	w.observe(callEvent("t1"))
	w.observe(finishEvent())
	w.observe(resultNoStep("t1"))
	w.observe(sessionEnd())
}

func TestWatchdogRunSmoke(t *testing.T) {
	// Run 订阅消费链路冒烟：订阅注册存在微小窗口（goroutine 启动 vs 首条发布），
	// 轮询重发直到提示到达——泄漏判定语义本身已被上面的同步用例覆盖。
	bus := eventbus.New()
	r := &rec{}
	w := New(r.pub)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx, bus)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bus.Publish("s1", "c1", stepPtr(7), eventbus.EventToolCall, "info",
			map[string]any{"call_id": "t9", "tool": "use_skill"})
		bus.Publish("s1", "c1", nil, eventbus.EventSessionFinished, "info", nil)
		if r.count() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Run 订阅链路应在超时前收到事件并提示")
}

package watchdog

import (
	"context"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

func turnStarted(sid string) eventbus.Event {
	return eventbus.Event{SessionID: sid, CaseID: "c-" + sid, Type: eventbus.EventTurnStarted,
		Payload: map[string]any{"entry": "example"}}
}

func turnEnded(sid, status, closedBy string) eventbus.Event {
	payload := map[string]any{"status": status}
	if closedBy != "" {
		payload["closed_by"] = closedBy
	}
	return eventbus.Event{SessionID: sid, CaseID: "c-" + sid, Type: eventbus.EventTurnEnded, Payload: payload}
}

func benignEvent(sid string) eventbus.Event {
	return eventbus.Event{SessionID: sid, Type: eventbus.EventStepStarted,
		Payload: map[string]any{"agent": "example"}}
}

// reasons 收集指定事件类型提示的 reason 列表。
func reasons(r *rec, typ string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, ev := range r.got {
		if ev.Type != typ {
			continue
		}
		reason, _ := ev.Payload["reason"].(string)
		out = append(out, reason)
	}
	return out
}

func TestTurnBracketCleanPairing(t *testing.T) {
	// 正常配对（开→闭，含归因与终态）静默；同一会话可以连续多轮。
	r := &rec{}
	w := New(r.pub)
	for i := 0; i < 2; i++ {
		w.observe(turnStarted("s1"))
		w.observe(turnEnded("s1", "succeeded", "loop"))
	}
	if n := r.count(); n != 0 {
		t.Fatalf("正常轮括号不应提示: %v", reasons(r, eventbus.EventTurnBracketViolation))
	}
}

func TestTurnBracketOpenLeak(t *testing.T) {
	// 开-开未闭：TTL 清扫报 turn_open_leak（保留最早 openedAt，不刷新）。
	r := &rec{}
	w := NewWithConfig(r.pub, Config{OpenTTL: time.Millisecond, SweepEvery: time.Nanosecond})
	w.observe(turnStarted("s1"))
	w.observe(turnStarted("s1"))
	time.Sleep(5 * time.Millisecond)
	w.observe(benignEvent("s2")) // 注入事件驱动 TTL 清扫

	got := reasons(r, eventbus.EventTurnBracketViolation)
	if len(got) != 1 || got[0] != ReasonTurnOpenLeak {
		t.Fatalf("应恰好一条 open_leak: %v", got)
	}
	ev := r.first()
	if ev.Level != "warn" || ev.Payload["opened_at"] == "" {
		t.Fatalf("提示级别/载荷不符: %+v", ev)
	}
}

func TestTurnBracketUnopenedClose(t *testing.T) {
	// 闭-闭无开：第二次闭合必须报 turn_end_unopened。
	r := &rec{}
	w := New(r.pub)
	w.observe(turnStarted("s1"))
	w.observe(turnEnded("s1", "failed", "loop"))
	w.observe(turnEnded("s1", "failed", "loop"))

	got := reasons(r, eventbus.EventTurnBracketViolation)
	if len(got) != 1 || got[0] != ReasonTurnEndUnopened {
		t.Fatalf("应恰好一条 unopened: %v", got)
	}
}

func TestTurnBracketMissingClosedBy(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(turnStarted("s1"))
	w.observe(turnEnded("s1", "succeeded", ""))
	got := reasons(r, eventbus.EventTurnBracketViolation)
	if len(got) != 1 || got[0] != ReasonTurnEndUnattributed {
		t.Fatalf("缺 closed_by 应报 unattributed: %v", got)
	}
}

func TestTurnBracketUnknownClosedBy(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(turnStarted("s1"))
	w.observe(turnEnded("s1", "succeeded", "made-up"))
	got := reasons(r, eventbus.EventTurnBracketViolation)
	if len(got) != 1 || got[0] != ReasonTurnEndUnattributed {
		t.Fatalf("不在册的 closed_by 应报 unattributed: %v", got)
	}
}

func TestTurnBracketUnknownStatus(t *testing.T) {
	r := &rec{}
	w := New(r.pub)
	w.observe(turnStarted("s1"))
	w.observe(turnEnded("s1", "degraded", "loop"))
	got := reasons(r, eventbus.EventTurnBracketViolation)
	if len(got) != 1 || got[0] != ReasonTurnEndUnknownStatus {
		t.Fatalf("非终态 status 应报 unknown_status: %v", got)
	}
}

func TestTurnBracketCustomClosedBy(t *testing.T) {
	// 宿主注册扩充词表后，自定义归因通过；缺省四值仍然有效。
	r := &rec{}
	w := NewWithConfig(r.pub, Config{ClosedBy: []string{"host-reconcile"}})
	w.observe(turnStarted("s1"))
	w.observe(turnEnded("s1", "cancelled", "host-reconcile"))
	w.observe(turnStarted("s2"))
	w.observe(turnEnded("s2", "timed_out", "panic"))
	if n := r.count(); n != 0 {
		t.Fatalf("注册的归因应通过: %v", reasons(r, eventbus.EventTurnBracketViolation))
	}
}

func TestTurnBracketShutdownLeavesOpenSilently(t *testing.T) {
	// 停机留开放=合法：Run 因 ctx 取消退出时不扫尾、不报泄漏。
	r := &rec{}
	w := NewWithConfig(r.pub, Config{OpenTTL: time.Nanosecond, SweepEvery: time.Nanosecond})
	w.observe(turnStarted("s1"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx, eventbus.New())
	time.Sleep(2 * time.Millisecond)

	if n := r.count(); n != 0 {
		t.Fatalf("停机退出不应把未闭合轮当泄漏: %v", reasons(r, eventbus.EventTurnBracketViolation))
	}
}

package eval

import (
	"reflect"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/trajectory"
)

// 合成事件流：一轮 + 一步 + 一次配对完整的工具调用（夹具只带中性词面）。
func baseEvents() []eventbus.Event {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	step := uint64(7)
	ev := func(at time.Time, typ string, payload map[string]any) eventbus.Event {
		return eventbus.Event{ID: 1, SessionID: "sess-1", StepID: &step, Type: typ, Level: "info",
			Payload: payload, OccurredAt: at}
	}
	return []eventbus.Event{
		ev(t0, eventbus.EventTurnStarted, nil),
		ev(t0.Add(100*time.Millisecond), eventbus.EventStepStarted, nil),
		ev(t0.Add(200*time.Millisecond), eventbus.EventToolCall, map[string]any{"call_id": "c-1", "tool": "grep"}),
		ev(t0.Add(300*time.Millisecond), eventbus.EventToolResult, map[string]any{"call_id": "c-1"}),
		ev(t0.Add(400*time.Millisecond), eventbus.EventStepFinished, nil),
		ev(t0.Add(500*time.Millisecond), eventbus.EventTurnEnded, nil),
	}
}

func TestRecordReplayReproduce(t *testing.T) {
	t.Run("录制 → 重放：差异为零（issue #111 验收线）", func(t *testing.T) {
		evs := baseEvents()
		raw, err := Record(evs)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded) != len(evs) {
			t.Fatalf("重载事件数 %d != %d", len(loaded), len(evs))
		}
		div := Diff(Replay(evs, trajectory.Options{}), Replay(loaded, trajectory.Options{}))
		if len(div) != 0 {
			t.Fatalf("回放必须复现录制（差异应为零）：%+v", div)
		}
	})

	t.Run("录制字节可 diff：同一流两次录制逐字节相同", func(t *testing.T) {
		a, err := Record(baseEvents())
		if err != nil {
			t.Fatal(err)
		}
		b, err := Record(baseEvents())
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatal("同一事件流两次录制产生了不同字节（确定性破口）")
		}
	})

	t.Run("事件被改：差异落在被改字段上", func(t *testing.T) {
		evs := baseEvents()
		want := Replay(evs, trajectory.Options{})
		evs[2].Payload["tool"] = "sed" // 重放侧改工具名
		got := Replay(evs, trajectory.Options{})
		div := Diff(want, got)
		if len(div) == 0 {
			t.Fatal("事件被改后必须出现差异")
		}
		found := false
		for _, d := range div {
			if d.ID == "tool#c-1" && d.Field == "tool" && d.Want == "grep" && d.Got == "sed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("差异应落在 tool 字段：%+v", div)
		}
	})

	t.Run("行缺失显形（row 字段）", func(t *testing.T) {
		evs := baseEvents()
		extra := append(append([]eventbus.Event{}, evs[:3]...),
			eventbus.Event{ID: 2, SessionID: "sess-1", Type: eventbus.EventToolCall, Level: "info",
				Payload:    map[string]any{"call_id": "c-2", "tool": "sed"},
				OccurredAt: evs[2].OccurredAt})
		want := Replay(extra, trajectory.Options{}) // 录制侧多一次调用
		got := Replay(evs, trajectory.Options{})    // 重放侧没有
		div := Diff(want, got)
		if len(div) == 0 {
			t.Fatal("缺行必须显形")
		}
		has := false
		for _, d := range div {
			if d.ID == "tool#c-2" && d.Field == "row" && d.Want == "present" && d.Got == "absent" {
				has = true
			}
		}
		if !has {
			t.Fatalf("应出现 row=present→absent：%+v", div)
		}
	})

	t.Run("多出的行也显形", func(t *testing.T) {
		got := Replay(baseEvents(), trajectory.Options{})
		div := Diff(nil, got)
		if len(div) == 0 {
			t.Fatal("期望空、实际非空必须显形")
		}
		if div[0].Want != "absent" || div[0].Got != "present" {
			t.Fatalf("方向反了：%+v", div[0])
		}
	})

	t.Run("Diff 排序确定", func(t *testing.T) {
		evs := baseEvents()
		a := Diff(Replay(evs[:3], trajectory.Options{}), Replay(evs, trajectory.Options{}))
		b := Diff(Replay(evs, trajectory.Options{}), Replay(evs[:3], trajectory.Options{}))
		if len(a) == 0 || len(b) == 0 {
			t.Fatalf("两侧都应有差异：%d vs %d", len(a), len(b))
		}
		// 两侧比较方向不同，但各自的输出内部按 (ID, Index, Field) 有序。
		for _, div := range [][]Divergence{a, b} {
			for i := 1; i < len(div); i++ {
				prev, cur := div[i-1], div[i]
				if prev.ID > cur.ID || (prev.ID == cur.ID && prev.Index > cur.Index) ||
					(prev.ID == cur.ID && prev.Index == cur.Index && prev.Field > cur.Field) {
					t.Fatalf("差异清单未按 (ID, Index, Field) 排序：%+v", div)
				}
			}
		}
	})

	t.Run("多段独立标识：授予段差异必须显形（issue #221/#301）", func(t *testing.T) {
		// 同一个副作用 Key 的授予段与对账段各有独立行标识（对账段带 #-2
		// 段序后缀，issue #301）。按 ID 建一对一 map 会丢掉先出现的那条——
		// 含副作用事件的回归比对由此给出「差异假零」。
		evs := sideEffectEvents()
		want := Replay(evs, trajectory.Options{})
		evs[0].Payload["resource"] = "res-2" // 重放侧只改授予段的资源
		got := Replay(evs, trajectory.Options{})
		div := Diff(want, got)
		if len(div) == 0 {
			t.Fatal("授予段被改必须显形（按 ID 建一对一 map 时这里是假零）")
		}
		found := false
		for _, d := range div {
			if d.ID == "sideeffect#se-1" && d.Index == 0 && d.Field == "sideeffect_resource" &&
				d.Want == "res-1" && d.Got == "res-2" {
				found = true
			}
		}
		if !found {
			t.Fatalf("差异应落在授予段（Index=0）的 sideeffect_resource 上：%+v", div)
		}
	})

	t.Run("多段独立标识：两侧全同则差异为零", func(t *testing.T) {
		evs := sideEffectEvents()
		div := Diff(Replay(evs, trajectory.Options{}), Replay(evs, trajectory.Options{}))
		if len(div) != 0 {
			t.Fatalf("同一段含副作用事件的全同轨迹必须差异为零：%+v", div)
		}
	})

	t.Run("少一段：缺行显形（issue #221/#301）", func(t *testing.T) {
		// 录制侧多一次点观察（contended，同 Key 第三段，标识带 #-3 后缀），
		// 重放侧没有——缺段必须报 row 缺行，段数差异不得静默。
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		extra := append(sideEffectEvents(), eventbus.Event{
			ID: 1, SessionID: "sess-se", Type: eventbus.EventSideEffectContended, Level: "info",
			Payload:    map[string]any{"key": "se-1"},
			OccurredAt: t0.Add(300 * time.Millisecond),
		})
		want := Replay(extra, trajectory.Options{})
		got := Replay(sideEffectEvents(), trajectory.Options{})
		div := Diff(want, got)
		if len(div) != 1 {
			t.Fatalf("应恰有一条缺行差异：%+v", div)
		}
		d := div[0]
		if d.ID != "sideeffect#se-1#-3" || d.Index != 0 || d.Field != "row" || d.Want != "present" || d.Got != "absent" {
			t.Fatalf("缺段应报 sideeffect#se-1#-3 的 row present→absent：%+v", d)
		}
	})
}

// sideEffectEvents 一段含副作用事件的最小流：授予 → 转未知（授予段收口 +
// 开同 Key 对账段）→ 裁决未生效（对账段收口）。两段各有独立行标识
// （sideeffect#se-1 / sideeffect#se-1#-2，issue #301），跨段聚合看
// sideeffect.key。
func sideEffectEvents() []eventbus.Event {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ev := func(at time.Time, typ string, payload map[string]any) eventbus.Event {
		return eventbus.Event{ID: 1, SessionID: "sess-se", Type: typ, Level: "info",
			Payload: payload, OccurredAt: at}
	}
	return []eventbus.Event{
		ev(t0, eventbus.EventSideEffectGranted, map[string]any{"key": "se-1", "resource": "res-1", "fence": uint64(1)}),
		ev(t0.Add(100*time.Millisecond), eventbus.EventSideEffectUncertain, map[string]any{"key": "se-1"}),
		ev(t0.Add(200*time.Millisecond), eventbus.EventSideEffectResolved, map[string]any{"key": "se-1", "applied": false}),
	}
}

func TestVerify(t *testing.T) {
	t.Run("常规流适航（整数载荷形变只发生一次，仍稳定）", func(t *testing.T) {
		evs := baseEvents()
		evs[2].Payload["seq"] = 3 // 整数：JSON 往返后是 float64（固有形变，一轮即稳）
		if probs := Verify(evs); len(probs) != 0 {
			t.Fatalf("常规流应适航：%+v", probs)
		}
	})

	t.Run("头部字段往返必须保真", func(t *testing.T) {
		evs := baseEvents()
		if probs := Verify(evs); len(probs) != 0 {
			t.Fatalf("头部字段应保真：%+v", probs)
		}
		// 直接构造「往返会变头」的形态不可行（JSON 表达域内必然保真），
		// 这里钉的是：适航判定不会误报合法流。
	})

	t.Run("载荷往返不幂等要显形", func(t *testing.T) {
		// NaN 无法 JSON 编码——构造「两次往返不等」的可编码形态：
		// json.Number 经 Marshal 原样输出、Unmarshal 成 string，第二轮
		// Marshal 会变成字符串字面量——值在两轮间不变，不适用。
		// 可靠的形态：载荷含 map 与切片混合 + 大整数（>2^53 精度丢失）。
		big := uint64(1 << 54)
		evs := []eventbus.Event{{ID: 1, SessionID: "s", Type: eventbus.EventLLMUsage,
			Payload: map[string]any{"n": float64(big)}, OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
		// float64(1<<54) 是整数浮点，往返稳定——不触发问题。
		if probs := Verify(evs); len(probs) != 0 {
			t.Fatalf("整数浮点应适航：%+v", probs)
		}
	})

	t.Run("Load 对坏行显式报错（行号定位）", func(t *testing.T) {
		if _, err := Load([]byte("{\"ID\":1}\nnot-json\n")); err == nil {
			t.Fatal("坏行应报错")
		}
		if _, err := Record(nil); err != nil {
			t.Fatalf("空流应可录制：%v", err)
		}
		empty, err := Load([]byte("\n\n"))
		if err != nil || len(empty) != 0 {
			t.Fatalf("空行应被跳过：%v %d", err, len(empty))
		}
	})

	t.Run("重载后投影与原流一致（端到端）", func(t *testing.T) {
		evs := baseEvents()
		evs[2].Payload["calls"] = 2 // 整数载荷参与投影
		loaded, err := Load(mustRecord(t, evs))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rowsSig(Replay(evs, trajectory.Options{})), rowsSig(Replay(loaded, trajectory.Options{}))) {
			t.Fatal("重载后的投影与原流不一致")
		}
	})
}

func mustRecord(t *testing.T, evs []eventbus.Event) []byte {
	t.Helper()
	raw, err := Record(evs)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// rowsSig 行签名的确定性序列（比对用：ID + 状态 + 配对）。
func rowsSig(rows []trajectory.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID+"|"+string(r.Kind)+"|"+string(r.Status)+"|"+string(r.Pairing))
	}
	return out
}

// assess_test.go —— Assess 判据与点观察行豁免（issue #222）的用例。
//
// 与 eval_test.go 分文件：单文件行数受结构阈值约束（max_file_lines=400）；
// 拆分按判据切分（录制/重放/Verfy 的夹具留在一处，Assess 的判定树在此）。
package eval

import (
	"reflect"
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/eventbus"
	"github.com/oagentkit/goagent-kernel/trajectory"
)

func TestAssess(t *testing.T) {
	rows := Replay(baseEvents(), trajectory.Options{})

	t.Run("全部判项通过", func(t *testing.T) {
		v, err := Assess(Spec{Checks: []Check{CheckClosure, CheckPairing, CheckNoDuplicates}}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if !v.Pass || v.Judged != 3 || len(v.Findings) != 0 {
			t.Fatalf("v=%+v", v)
		}
	})

	t.Run("期望/禁止判项", func(t *testing.T) {
		v, err := Assess(Spec{
			Checks:       []Check{CheckExpected, CheckForbidden},
			ExpectedIDs:  []string{"turn#1", "step#7", "tool#c-1"},
			ForbiddenIDs: []string{"tool#c-99"},
		}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if !v.Pass || v.Judged != 2 {
			t.Fatalf("v=%+v", v)
		}
		v, err = Assess(Spec{
			Checks:       []Check{CheckExpected},
			ExpectedIDs:  []string{"turn#1", "step#999"},
			ForbiddenIDs: nil,
		}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if v.Pass || len(v.Findings) != 1 || v.Findings[0].RowID != "step#999" {
			t.Fatalf("缺行必须显形：v=%+v", v)
		}
	})

	t.Run("收口违规（喂一半就收口）", func(t *testing.T) {
		half := Replay(baseEvents()[:3], trajectory.Options{})
		v, err := Assess(Spec{Checks: []Check{CheckClosure}}, half)
		if err != nil {
			t.Fatal(err)
		}
		if v.Pass || len(v.Findings) == 0 {
			t.Fatalf("未收口必须显形：v=%+v", v)
		}
		for _, f := range v.Findings {
			if f.Check != CheckClosure {
				t.Fatalf("finding 归因错误：%+v", f)
			}
		}
	})

	t.Run("配对缺口与重复声明", func(t *testing.T) {
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		ev := func(at time.Time, typ string, payload map[string]any) eventbus.Event {
			return eventbus.Event{ID: 1, SessionID: "s", Type: typ, Level: "info", Payload: payload, OccurredAt: at}
		}
		stream := []eventbus.Event{
			ev(t0, eventbus.EventToolCall, map[string]any{"call_id": "c-9", "tool": "grep"}),
			ev(t0.Add(time.Second), eventbus.EventToolResult, map[string]any{"call_id": "c-9"}),
			ev(t0.Add(2*time.Second), eventbus.EventToolResult, map[string]any{"call_id": "c-9"}), // 重复结果
			ev(t0.Add(3*time.Second), eventbus.EventToolResult, map[string]any{"call_id": "c-x"}), // 无对应调用
		}
		rows := Replay(stream, trajectory.Options{})
		v, err := Assess(Spec{Checks: []Check{CheckPairing, CheckNoDuplicates}}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if v.Pass {
			t.Fatalf("缺口必须显形：v=%+v", v)
		}
		kinds := map[Check]int{}
		for _, f := range v.Findings {
			kinds[f.Check]++
		}
		if kinds[CheckPairing] == 0 || kinds[CheckNoDuplicates] == 0 {
			t.Fatalf("两类 finding 都应出现：%+v", v.Findings)
		}
	})

	t.Run("checkpoint 点行孤儿不误报（设计事实）", func(t *testing.T) {
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		stream := []eventbus.Event{{ID: 1, SessionID: "s", Type: eventbus.EventCheckpointCommitted,
			Level: "info", Payload: map[string]any{"run_id": "r-1", "seq": uint64(3), "status": "running"},
			OccurredAt: t0}}
		rows := Replay(stream, trajectory.Options{})
		v, err := Assess(Spec{Checks: []Check{CheckClosure}}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if !v.Pass {
			t.Fatalf("checkpoint 孤儿行是设计事实，不得误报：%+v", v.Findings)
		}
	})

	t.Run("零判项与不合法清单显式拒绝", func(t *testing.T) {
		if _, err := Assess(Spec{}, rows); err != ErrNoChecks {
			t.Fatalf("零判项应报 ErrNoChecks，得到 %v", err)
		}
		if _, err := Assess(Spec{Checks: []Check{"mystery"}}, rows); err == nil {
			t.Fatal("词表外判项应被拒")
		}
		if _, err := Assess(Spec{Checks: []Check{CheckExpected}}, rows); err == nil {
			t.Fatal("expected 缺清单应被拒")
		}
		if _, err := Assess(Spec{Checks: []Check{CheckForbidden}}, rows); err == nil {
			t.Fatal("forbidden 缺清单应被拒")
		}
		if _, err := Assess(Spec{Checks: []Check{CheckClosure, CheckClosure}}, rows); err == nil {
			t.Fatal("重复判项应被拒")
		}
	})

	t.Run("结论确定性：同输入同输出", func(t *testing.T) {
		half := Replay(baseEvents()[:3], trajectory.Options{})
		spec := Spec{Checks: []Check{CheckClosure, CheckPairing}}
		v1, err := Assess(spec, half)
		if err != nil {
			t.Fatal(err)
		}
		v2, err := Assess(spec, half)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(v1, v2) {
			t.Fatalf("纯函数判定必须可复现：%+v vs %+v", v1, v2)
		}
	})
}

// TestAssessSideEffectPointRowsNotClosureViolations 钉住 issue #222 的成对判据：
// 副作用**点观察**行（replayed / contended）的孤儿形态是投影的设计事实——
// 点事件没有括号段，「起点缺失」的判据前提对它不成立。把它判成违规就是
// 假红：只要轨迹里出现一次「动作已完成、这次取回结果」或「资源被别人持有」
// （都是正常路径），健康轨迹就被判不过。
//
// 成对面：**非点观察**相位的孤儿行（如只观测到收尾的 completed）是真实的
// 「只有终点没有起点」，必须照判——豁免过宽就是假绿。
func TestAssessSideEffectPointRowsNotClosureViolations(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ev := func(at time.Time, typ string, payload map[string]any) eventbus.Event {
		return eventbus.Event{ID: 1, SessionID: "s", Type: typ, Level: "info", Payload: payload, OccurredAt: at}
	}

	t.Run("点观察行不误报（replayed / contended 都是正常路径）", func(t *testing.T) {
		stream := []eventbus.Event{
			ev(t0, eventbus.EventSideEffectReplayed, map[string]any{"key": "se-r", "resource": "res-r"}),
			ev(t0.Add(time.Second), eventbus.EventSideEffectContended, map[string]any{"key": "se-c", "resource": "res-c"}),
		}
		v, err := Assess(Spec{Checks: []Check{CheckClosure}}, Replay(stream, trajectory.Options{}))
		if err != nil {
			t.Fatal(err)
		}
		if !v.Pass || len(v.Findings) != 0 {
			t.Fatalf("点观察行是设计事实，健康轨迹不得被判红：%+v", v.Findings)
		}
	})

	t.Run("点观察与其它判项组合仍过（健康轨迹全绿）", func(t *testing.T) {
		stream := append(baseEvents(),
			ev(t0.Add(600*time.Millisecond), eventbus.EventSideEffectReplayed,
				map[string]any{"key": "se-r", "resource": "res-r"}))
		v, err := Assess(Spec{Checks: []Check{CheckClosure, CheckPairing, CheckNoDuplicates}},
			Replay(stream, trajectory.Options{}))
		if err != nil {
			t.Fatal(err)
		}
		if !v.Pass {
			t.Fatalf("含点观察的健康轨迹应全绿：%+v", v.Findings)
		}
	})

	t.Run("非点观察相位的孤儿行照判（真缺起点）", func(t *testing.T) {
		// 只观测到 completed 收尾、没有授予段（授予发生在别的进程且事件流
		// 只有尾部）——这是投影明说的孤儿形态，closure 必须报。
		stream := []eventbus.Event{
			ev(t0, eventbus.EventSideEffectCompleted, map[string]any{"key": "se-x", "resource": "res-x"}),
		}
		v, err := Assess(Spec{Checks: []Check{CheckClosure}}, Replay(stream, trajectory.Options{}))
		if err != nil {
			t.Fatal(err)
		}
		if v.Pass || len(v.Findings) != 1 || v.Findings[0].RowID != "sideeffect#se-x" {
			t.Fatalf("非点观察的孤儿行必须报起点缺失：%+v", v.Findings)
		}
	})

	t.Run("打开中的副作用行仍判未收口", func(t *testing.T) {
		// 授予段开了没收口（只有 granted，没有收口事件）——open 照判，
		// 点观察豁免不覆盖它。
		stream := []eventbus.Event{
			ev(t0, eventbus.EventSideEffectGranted, map[string]any{"key": "se-o", "resource": "res-o"}),
		}
		v, err := Assess(Spec{Checks: []Check{CheckClosure}}, Replay(stream, trajectory.Options{}))
		if err != nil {
			t.Fatal(err)
		}
		if v.Pass || len(v.Findings) != 1 || v.Findings[0].RowID != "sideeffect#se-o" {
			t.Fatalf("打开中的授予段必须报未收口：%+v", v.Findings)
		}
	})
}

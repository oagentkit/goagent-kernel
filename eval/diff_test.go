// diff_test.go —— 差异比对对「同 ID 多行」的语义（issue #221）。
//
// 轨迹行 ID 不唯一是 trajectory 的设计事实：同一个副作用 Key 的授予段、
// 对账段、点观察共用一个标识。旧实现按 ID 建 map（后写覆盖先写），
// 授予段整条被丢弃 ⇒ 只改授予段的回归比对给出「差异为零」的假绿。
package eval

import (
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/trajectory"
)

var diffT0 = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// sameIDSegments 同一副作用 Key 的两段（授予段 + 对账段），行标识相同。
func sameIDSegments() (granted, reconcile trajectory.Row) {
	granted = trajectory.Row{
		Seq: 1, Kind: trajectory.KindSideEffect, ID: "sideeffect#act-a",
		SideEffectKey: "act-a", SideEffectPhase: trajectory.SideEffectGranted,
		SideEffectResource: "res-a", StartAt: diffT0, EndAt: diffT0.Add(50 * time.Millisecond),
		Status: trajectory.StatusClosed,
	}
	reconcile = trajectory.Row{
		Seq: 2, Kind: trajectory.KindSideEffect, ID: "sideeffect#act-a",
		SideEffectKey: "act-a", SideEffectPhase: trajectory.SideEffectResolvedApplied,
		SideEffectResource: "res-a", StartAt: diffT0.Add(50 * time.Millisecond),
		EndAt: diffT0.Add(200 * time.Millisecond), Status: trajectory.StatusClosed,
	}
	return granted, reconcile
}

// TestDiffSameIDRowsBothParticipate 同 ID 两行都必须参与比对：
// 只改授予段（数组里的前一条）的一个字段，Diff 必须非空。
func TestDiffSameIDRowsBothParticipate(t *testing.T) {
	granted, reconcile := sameIDSegments()
	changed := granted
	changed.StartAt = granted.StartAt.Add(time.Millisecond)

	div := Diff([]trajectory.Row{granted, reconcile}, []trajectory.Row{changed, reconcile})
	if len(div) == 0 {
		t.Fatal("同 ID 的授予段被 map 覆盖，差异假零（#221）")
	}
	found := false
	for _, d := range div {
		if d.ID == "sideeffect#act-a" && d.Field == "start_at" {
			found = true
		}
	}
	if !found {
		t.Fatalf("差异应落在授予段的 start_at 上：%+v", div)
	}
}

// TestDiffSameIDMissingSegment 一侧少一段必须显形（row=present→absent），
// 不能因为同 ID 的后一条还在就判 present↔present。
func TestDiffSameIDMissingSegment(t *testing.T) {
	granted, reconcile := sameIDSegments()
	div := Diff([]trajectory.Row{granted, reconcile}, []trajectory.Row{reconcile})
	if len(div) == 0 {
		t.Fatal("少一段必须显形（同 ID 行被 map 合并）")
	}
	foundMissing := false
	for _, d := range div {
		if d.ID == "sideeffect#act-a" && d.Field == "row" && d.Want == "present" && d.Got == "absent" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("应报 row=present→absent：%+v", div)
	}
}

// seEvent 一条副作用事件（载荷键面与 eventbus 词表注释一致）。
func seEvent(typ string, at time.Time, key, resource string, extra map[string]any) eventbus.Event {
	payload := map[string]any{"key": key, "resource": resource, "fence": uint64(1)}
	for k, v := range extra {
		payload[k] = v
	}
	return eventbus.Event{ID: 1, SessionID: "sess-1", Type: typ, Level: "info",
		Payload: payload, OccurredAt: at}
}

// TestDiffSideEffectTraceRegression 端到端（录制 → 重放投影）：只改授予事件的
// 时刻，Diff 必须非空——修复前 byID 只留下对账段，这条回归测试假绿。
func TestDiffSideEffectTraceRegression(t *testing.T) {
	events := func(grantedAt time.Time) []eventbus.Event {
		return []eventbus.Event{
			seEvent(eventbus.EventSideEffectGranted, grantedAt, "act-a", "res-a", nil),
			seEvent(eventbus.EventSideEffectUncertain, diffT0.Add(100*time.Millisecond), "act-a", "res-a", nil),
			seEvent(eventbus.EventSideEffectResolved, diffT0.Add(200*time.Millisecond), "act-a", "res-a",
				map[string]any{"applied": true}),
		}
	}
	want := Replay(events(diffT0), trajectory.Options{})
	got := Replay(events(diffT0.Add(time.Millisecond)), trajectory.Options{})
	// 前提：投影确实产出同 ID 两行（授予段 + 对账段）。
	count := 0
	for _, r := range want {
		if r.ID == "sideeffect#act-a" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("夹具前提不成立：同 ID 应有两行，实得 %d（%+v）", count, want)
	}

	div := Diff(want, got)
	if len(div) == 0 {
		t.Fatal("只改授予事件时刻，Diff 必须非空（#221 验收线）")
	}
	found := false
	for _, d := range div {
		if d.ID == "sideeffect#act-a" && d.Field == "start_at" {
			found = true
		}
	}
	if !found {
		t.Fatalf("差异应落在授予段 start_at：%+v", div)
	}
}

// TestDiffSideEffectMissingSegment 端到端：一侧授予→完成（1 行），另一侧
// 授予→未知→裁决（同 ID 2 行）⇒ 缺一段必须报 row。
func TestDiffSideEffectMissingSegment(t *testing.T) {
	short := []eventbus.Event{
		seEvent(eventbus.EventSideEffectGranted, diffT0, "act-a", "res-a", nil),
		seEvent(eventbus.EventSideEffectCompleted, diffT0.Add(50*time.Millisecond), "act-a", "res-a", nil),
	}
	long := []eventbus.Event{
		seEvent(eventbus.EventSideEffectGranted, diffT0, "act-a", "res-a", nil),
		seEvent(eventbus.EventSideEffectUncertain, diffT0.Add(50*time.Millisecond), "act-a", "res-a", nil),
		seEvent(eventbus.EventSideEffectResolved, diffT0.Add(150*time.Millisecond), "act-a", "res-a",
			map[string]any{"applied": true}),
	}
	div := Diff(Replay(long, trajectory.Options{}), Replay(short, trajectory.Options{}))
	foundMissing := false
	for _, d := range div {
		if d.ID == "sideeffect#act-a" && d.Field == "row" && d.Want == "present" && d.Got == "absent" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("少一段必须报 row=present→absent：%+v", div)
	}
}

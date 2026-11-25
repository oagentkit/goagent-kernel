// diff_test.go —— 差异比对对「同 ID 多行」的语义（issue #221）与行标识
// 「轮内唯一」（issue #301）的对拍。
//
// 两条判据：
//   - 跨轮同名环节号（step#7 在两轮里各出现一次）共用同一个 ID：Diff 必须
//     按出现序逐条配对，先出现的那条不得被后写覆盖（#221 的假绿形态）。
//   - 副作用多段（授予段 / 对账段）自 #301 起各有独立标识（对账段带 #-2
//     后缀），两段必须都参与比对；跨段聚合用 sideeffect.key，不靠 row_id。
package eval

import (
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/eventbus"
	"github.com/oagentkit/goagent-kernel/trajectory"
)

var diffT0 = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// diffSegments 同一副作用 Key 的两段（授予段 + 对账段）：行标识**不同**
// （轮内唯一；对账段带 "#-2" 段序后缀，issue #301），但 sideeffect.key 相同。
func diffSegments() (granted, reconcile trajectory.Row) {
	granted = trajectory.Row{
		Seq: 1, Kind: trajectory.KindSideEffect, ID: "sideeffect#act-a",
		SideEffectKey: "act-a", SideEffectPhase: trajectory.SideEffectGranted,
		SideEffectResource: "res-a", StartAt: diffT0, EndAt: diffT0.Add(50 * time.Millisecond),
		Status: trajectory.StatusClosed,
	}
	reconcile = trajectory.Row{
		Seq: 2, Kind: trajectory.KindSideEffect, ID: "sideeffect#act-a#-2",
		SideEffectKey: "act-a", SideEffectPhase: trajectory.SideEffectResolvedApplied,
		SideEffectResource: "res-a", StartAt: diffT0.Add(50 * time.Millisecond),
		EndAt: diffT0.Add(200 * time.Millisecond), Status: trajectory.StatusClosed,
	}
	return granted, reconcile
}

// TestDiffSideEffectSegmentsBothParticipate 两段各有独立标识：改任意一段
// 都必须显形（#301 之后不再有「同 ID 后写覆盖」的丢失面）。
func TestDiffSideEffectSegmentsBothParticipate(t *testing.T) {
	granted, reconcile := diffSegments()

	changedGrant := granted
	changedGrant.StartAt = granted.StartAt.Add(time.Millisecond)
	div := Diff([]trajectory.Row{granted, reconcile}, []trajectory.Row{changedGrant, reconcile})
	if !hasDivergence(div, "sideeffect#act-a", "start_at") {
		t.Fatalf("授予段的差异必须显形：%+v", div)
	}

	changedRecon := reconcile
	changedRecon.SideEffectPhase = trajectory.SideEffectResolvedCleared
	div = Diff([]trajectory.Row{granted, reconcile}, []trajectory.Row{granted, changedRecon})
	if !hasDivergence(div, "sideeffect#act-a#-2", "sideeffect_phase") {
		t.Fatalf("对账段的差异必须显形：%+v", div)
	}
}

// sameIDRows 跨轮同名环节号：两行共用 step#7（行标识只在轮内唯一）。
func sameIDRows() (first, second trajectory.Row) {
	first = trajectory.Row{
		Seq: 1, Kind: trajectory.KindStep, ID: "step#7", Turn: 1, HasStep: true, StepID: 7,
		StartAt: diffT0, EndAt: diffT0.Add(50 * time.Millisecond), Status: trajectory.StatusClosed,
	}
	second = trajectory.Row{
		Seq: 2, Kind: trajectory.KindStep, ID: "step#7", Turn: 2, HasStep: true, StepID: 7,
		StartAt: diffT0.Add(100 * time.Millisecond), EndAt: diffT0.Add(150 * time.Millisecond),
		Status: trajectory.StatusClosed,
	}
	return first, second
}

// TestDiffSameIDRowsBothParticipate 同 ID 两行都必须参与比对：只改先出现的
// 那一行（数组里的前一条）的一个字段，Diff 必须非空且按 Index 定位。
func TestDiffSameIDRowsBothParticipate(t *testing.T) {
	first, second := sameIDRows()
	changed := first
	changed.StartAt = first.StartAt.Add(time.Millisecond)

	div := Diff([]trajectory.Row{first, second}, []trajectory.Row{changed, second})
	if len(div) == 0 {
		t.Fatal("同 ID 的第一行被 map 覆盖，差异假零（#221）")
	}
	found := false
	for _, d := range div {
		if d.ID == "step#7" && d.Index == 0 && d.Field == "start_at" {
			found = true
		}
	}
	if !found {
		t.Fatalf("差异应落在第一行（Index=0）的 start_at 上：%+v", div)
	}
}

// TestDiffSameIDMissingSegment 一侧少一段必须显形（row=present→absent），
// 不能因为同 ID 的后一条还在就判 present↔present。
func TestDiffSameIDMissingSegment(t *testing.T) {
	first, second := sameIDRows()
	div := Diff([]trajectory.Row{first, second}, []trajectory.Row{second})
	if len(div) == 0 {
		t.Fatal("少一段必须显形（同 ID 行被 map 合并）")
	}
	foundMissing := false
	for _, d := range div {
		if d.ID == "step#7" && d.Index == 1 && d.Field == "row" && d.Want == "present" && d.Got == "absent" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("应报 Index=1 的 row=present→absent（第二条在 got 侧缺失）：%+v", div)
	}
}

// hasDivergence 差异清单里是否有指定 (ID, Field)。
func hasDivergence(div []Divergence, id, field string) bool {
	for _, d := range div {
		if d.ID == id && d.Field == field {
			return true
		}
	}
	return false
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
// 时刻，Diff 必须落在授予段（sideeffect#act-a）上——两行不再共用标识，
// 授予段不会被对账段覆盖。
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
	// 前提：投影产出两段独立标识（授予段 + 对账段，#301）。
	ids := map[string]bool{}
	for _, r := range want {
		if r.Kind == trajectory.KindSideEffect {
			ids[r.ID] = true
		}
	}
	if !ids["sideeffect#act-a"] || !ids["sideeffect#act-a#-2"] || len(ids) != 2 {
		t.Fatalf("夹具前提不成立：应有两段独立标识，实得 %v（%+v）", ids, want)
	}

	div := Diff(want, got)
	if !hasDivergence(div, "sideeffect#act-a", "start_at") {
		t.Fatalf("差异应落在授予段 start_at：%+v", div)
	}
}

// TestDiffSideEffectMissingSegment 端到端：一侧授予→完成（1 行），另一侧
// 授予→未知→裁决（2 行，独立标识）⇒ 缺一段必须报 row。
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
		if d.ID == "sideeffect#act-a#-2" && d.Field == "row" && d.Want == "present" && d.Got == "absent" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("少一段必须报 row=present→absent（标识带段序后缀）：%+v", div)
	}
}

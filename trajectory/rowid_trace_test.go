// rowid_trace_test.go —— 行标识轮内唯一性的判据（issue #301）。
//
// 与 otelmap_test.go 的 TestRowIDsAreUniqueWithinTurnOnly 互补：那条钉「跨轮
// 同名环节号合法」的 trace 边界前提，这条钉「轮内不撞标识」（含副作用多段、
// 重复快照、同轮重试的同号环节）与契约文本的同源。

package trajectory

import (
	"strings"
	"testing"

	"github.com/oagentkit/goagent-kernel/eventbus"
)

// TestRowIDsUniqueWithinTurnIncludingMultiSegment 行标识在**轮内**唯一——
// 副作用的多段（授予段→对账段）、重复快照提交、同轮重试的同号环节都不能撞
// 同一个标识（issue #301）。多段靠标识后缀区分，跨段聚合用 sideeffect.key
// （快照用 run_id + seq）。
func TestRowIDsUniqueWithinTurnIncludingMultiSegment(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		{Type: eventbus.EventTurnStarted, OccurredAt: at(0)},
		seEv(eventbus.EventSideEffectGranted, 10, "act-a", "res-a", "", 1, nil),
		seEv(eventbus.EventSideEffectUncertain, 20, "act-a", "res-a", "", 1, nil),
		ckEv(30, "run-a", 5, "running"),
		ckEv(40, "run-a", 5, "running"),
		stepEv(eventbus.EventStepStarted, 50, 7),
		stepEv(eventbus.EventStepStarted, 60, 7),
		stepEv(eventbus.EventStepFinished, 70, 7),
		stepEv(eventbus.EventStepFinished, 80, 7),
	})
	rows := p.Rows()

	byTurn := map[int]map[string]int{}
	var seRows, ckRows []Row
	for _, r := range rows {
		if r.ID == "" {
			t.Fatalf("行标识不该为空：%+v", r)
		}
		if byTurn[r.Turn] == nil {
			byTurn[r.Turn] = map[string]int{}
		}
		byTurn[r.Turn][r.ID]++
		switch r.Kind {
		case KindSideEffect:
			seRows = append(seRows, r)
		case KindCheckpoint:
			ckRows = append(ckRows, r)
		}
	}
	for turn, ids := range byTurn {
		for id, n := range ids {
			if n > 1 {
				t.Fatalf("第 %d 轮内行标识重复 %q（出现 %d 次）：%+v", turn, id, n, rows)
			}
		}
	}

	// 同 Key 两段：标识必须不同，聚合口径（sideeffect.key）必须相同。
	if len(seRows) != 2 {
		t.Fatalf("应有两段 sideeffect 行（授予段 + 对账段），实得 %d", len(seRows))
	}
	if seRows[0].ID == seRows[1].ID {
		t.Fatalf("同一轮内两段副作用行撞了同一个标识：%q", seRows[0].ID)
	}
	if seRows[0].SideEffectKey != "act-a" || seRows[1].SideEffectKey != "act-a" {
		t.Fatalf("两段必须可按 sideeffect.key 聚合：%+v", seRows)
	}

	// 重复快照提交：标识必须不同，且 run_id + seq 仍可读。
	if len(ckRows) != 2 {
		t.Fatalf("重复提交应落成两行（每次提交都是一次观测），实得 %d", len(ckRows))
	}
	if ckRows[0].ID == ckRows[1].ID {
		t.Fatalf("重复快照提交撞了同一个标识：%q", ckRows[0].ID)
	}
	for _, r := range ckRows {
		if r.CheckpointRunID != "run-a" || r.CheckpointSeq != 5 {
			t.Fatalf("快照行的聚合口径（run_id + seq）不得被标识后缀改写：%+v", r)
		}
	}

	// OTel 消费面：同一 trace（本夹具只有一轮）内 row_id 不得重复。
	seen := map[string]bool{}
	for _, s := range SpansOf(rows) {
		id, _ := s.Attributes[AttrRowID].(string)
		if id == "" {
			continue
		}
		if seen[id] {
			t.Fatalf("同一 trace 内重复出现 row_id %q（下游按它聚合/去重会静默丢段）", id)
		}
		seen[id] = true
	}
}

// TestRowIDContractMatchesActualUniqueness 契约文本必须写出**真实**的唯一性
// 语义与多段聚合口径——契约说「轮内唯一」而实现撞 ID 是自相矛盾；实现改成
// 真唯一后契约却回退成旧措辞，同一条用例必须红（issue #301 反向验证）。
func TestRowIDContractMatchesActualUniqueness(t *testing.T) {
	spec, ok := AttrSpecOf(AttrRowID)
	if !ok {
		t.Fatal("AttrRowID 没有契约")
	}
	note := spec.Domain.Note
	if !strings.Contains(note, "唯一范围是") || !strings.Contains(note, "轮内") {
		t.Fatalf("契约必须写明唯一性范围是轮内，实得 %q", note)
	}
	if !strings.Contains(note, "段序") {
		t.Fatalf("契约必须写明多段以段序后缀区分（否则同一 trace 内撞标识无从解释），实得 %q", note)
	}
}

package trajectory

// 行标识的**多段语义**判据（issue #301）。
//
// 「同一个 Key 的授予段、对账段、点观察共用一个标识」与「同一 (run_id, seq) 的
// 重复快照投递同 ID」是投影器的**设计事实**——它们是同一个逻辑动作的先后段、
// 同一次提交的重复观测，不是撞号。契约与 trace 边界论证必须如实承认这一点，
// 而 OTel 导出面必须把「同轮内 row_id 重复」显式标注为多段（段序属性区分），
// 下游按 (row_id, segment) 聚合才不会静默丢段。
//
// 同轮内标识唯一性判据的**明确结论**：标识在（轮, 段序）内唯一——同轮重复的
// row_id 必带递增段序，跨轮重复（如两轮各开一次的 step#7）段序各自从 1 起算。

import (
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestMultiSegmentRowIDsAreExplicitlyAnnotated 含 SideEffectUncertain 与重复快照
// 的夹具：同轮内 sideeffect#act-a 出现 2 次（授予段 → uncertain 收口 + 对账段
// 新开）、checkpoint#run-a#5 出现 2 次（同 (run_id, seq) 重复投递）——判据给出
// 明确结论：多段是设计事实，每段以递增段序区分，OTel 导出显式标注。
func TestMultiSegmentRowIDsAreExplicitlyAnnotated(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		seEv(eventbus.EventSideEffectGranted, 100, "act-a", "res-a", "", 1, nil),
		seEv(eventbus.EventSideEffectUncertain, 150, "act-a", "res-a", "", 1, nil), // 授予段收口 + 对账段新开
		ckEv(160, "run-a", 5, "running"),
		ckEv(170, "run-a", 5, "running"), // 同 (run_id, seq) 重复投递
	})

	rows := p.Rows()
	countByID := map[string]int{}
	for _, r := range rows {
		countByID[r.ID]++
	}
	if countByID["sideeffect#act-a"] != 2 || countByID["checkpoint#run-a#5"] != 2 {
		t.Fatalf("夹具应产出多段行（设计事实，issue #301 实证）：%v", countByID)
	}

	// 判据：同（轮, 标识）的多段段序递增且首段为 1——（轮, 段序）内唯一。
	for _, id := range []string{"sideeffect#act-a", "checkpoint#run-a#5"} {
		var segs []int
		for _, r := range rows {
			if r.ID == id {
				segs = append(segs, r.Segment)
			}
		}
		if len(segs) != 2 || segs[0] != 1 || segs[1] != 2 {
			t.Fatalf("%s 的多段段序应为 [1 2]，实得 %v", id, segs)
		}
	}

	// OTel 侧：同一 trace 内重复 row_id 的 span 必须被显式标注为多段——
	// 第一段不写 segment 属性（单段行不带噪声），第二段起写递增段序。
	spans := SpansOf(rows)
	segAttrs := map[string][]any{}
	for _, s := range spans {
		id, _ := s.Attributes[AttrRowID].(string)
		if id == "sideeffect#act-a" || id == "checkpoint#run-a#5" {
			segAttrs[id] = append(segAttrs[id], s.Attributes[AttrRowSegment])
		}
	}
	for _, id := range []string{"sideeffect#act-a", "checkpoint#run-a#5"} {
		got := segAttrs[id]
		if len(got) != 2 || got[0] != nil || got[1] != int64(2) {
			t.Fatalf("%s 的 span 段序标注应为 [nil 2]（首段不写、次段显式），实得 %v", id, got)
		}
	}
}

// TestSegmentOrdinalResetsPerTurn 段序的计数范围是（轮, 标识）：同一个标识在
// 两轮里各出现一次（合法的跨轮重复）时，段序各自从 1 起算——轮仍是唯一性
// 的边界，跨轮重复不是多段。
func TestSegmentOrdinalResetsPerTurn(t *testing.T) {
	p := NewProjector(Options{})
	for turn := 0; turn < 2; turn++ {
		base := turn * 100
		p.ApplyAll([]eventbus.Event{
			ev(eventbus.EventTurnStarted, base, nil),
			ckEv(base+10, "run-a", 3, "running"),
			ckEv(base+20, "run-a", 3, "running"), // 同轮内重复投递：多段
			ev(eventbus.EventTurnEnded, base+30, nil),
		})
	}
	rows := p.Rows()
	var segs []int
	for _, r := range rows {
		if r.ID == "checkpoint#run-a#3" {
			segs = append(segs, r.Segment)
		}
	}
	if len(segs) != 4 || segs[0] != 1 || segs[1] != 2 || segs[2] != 1 || segs[3] != 2 {
		t.Fatalf("段序应按轮分段重置（[1 2 1 2]），实得 %v", segs)
	}
}

// TestRowIDContractStatesMultiSegmentScope 契约文本钉子：AttrRowID 的取值域
// 说明必须如实承认「同轮内标识可重复 = 同一逻辑动作的多段，以段序区分」，
// 不得再声称单值的「唯一范围是轮内」——那是被投影器自己按设计打破的前提
// （issue #301）。文本改回去而用例不改 ⇒ 这条红。
func TestRowIDContractStatesMultiSegmentScope(t *testing.T) {
	spec, ok := AttrSpecOf(AttrRowID)
	if !ok {
		t.Fatal("AttrRowID 没有契约条目")
	}
	if strings.Contains(spec.Domain.Note, "唯一范围是轮内") {
		t.Fatalf("AttrRowID 契约不得再声称单值的「唯一范围是轮内」（多段共用标识是设计事实）: %s", spec.Domain.Note)
	}
	for _, phrase := range []string{"段序", "多段"} {
		if !strings.Contains(spec.Domain.Note, phrase) {
			t.Fatalf("AttrRowID 契约必须写明多段语义（缺 %q）: %s", phrase, spec.Domain.Note)
		}
	}
	// 聚合口径也要有主：下游拿什么去重排序，契约必须写出来。
	segSpec, ok := AttrSpecOf(AttrRowSegment)
	if !ok {
		t.Fatalf("segment 属性没有契约条目（多段标注的映射产物必须有主）")
	}
	for _, phrase := range []string{"row_id", "段序"} {
		if !strings.Contains(segSpec.Domain.Note, phrase) {
			t.Fatalf("segment 契约必须写明聚合口径（缺 %q）: %s", phrase, segSpec.Domain.Note)
		}
	}
}

package trajectory

// 载荷数值的**范围纪律**判据（issue #305）。
//
// payloadUint 此前对超范围 float64 直接转换：1e30 与 +Inf 落成 MaxUint64
// （看起来合法），NaN 落成 0，导出时 int64(MaxUint64) 溢出成 -1——下游按它做
// 单调性判断会得出「倒退」的结论。判据：
//
//  1. 超范围（≥2^64）/ NaN / ±Inf 的处置与「未设置」**区分得开**——投影器不猜，
//     字段按未设置处理，越界本身在行上显形（SideEffectFenceOutOfRange 等标记）；
//  2. 导出面不出现 -1 这种「溢出伪装成合法值」的结果——不可表示就不写该属性，
//     以溢出诊断位显式标注；
//  3. 范围内的小数按声明口径向零取整（注释声明，不静默）。

import (
	"math"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// fenceEv 一条副作用授予事件（fence 值可注入任意 any）。
func fenceEv(ms int, fence any) eventbus.Event {
	return eventbus.Event{
		Type:       eventbus.EventSideEffectGranted,
		OccurredAt: at(ms),
		Payload:    map[string]any{"key": "act-a", "resource": "res-a", "fence": fence},
	}
}

// TestOutOfRangeFenceIsDistinguishableFromUnset 1e30 / +Inf / NaN 的处置结果
// 必须与「载荷没带 fence」区分得开：字段按未设置处理，但越界标记置位。
func TestOutOfRangeFenceIsDistinguishableFromUnset(t *testing.T) {
	cases := []struct {
		name  string
		fence any
	}{
		{"1e30", 1e30},                    // 合法 JSON 数字，超出 uint64 可表示范围
		{"+Inf", math.Inf(1)},             // 进程内构造的事件载荷可能携带
		{"-Inf", math.Inf(-1)},            //
		{"NaN", math.NaN()},               //
		{"负数", -1.5},                     // 栅栏代数不可能为负
		{"2^64", 18446744073709551616.0},  // 恰好越界
	}
	for _, c := range cases {
		p := NewProjector(Options{})
		p.Apply(fenceEv(100, c.fence))
		row := p.Rows()[0]
		if row.SideEffectFence != 0 {
			t.Fatalf("%s：投影器不得猜一个看起来合法的值，实得 %d", c.name, row.SideEffectFence)
		}
		if !row.SideEffectFenceOutOfRange {
			t.Fatalf("%s：越界处置必须可与「未设置」区分（标记应置位）", c.name)
		}
		s := SpanOf(row)
		if _, has := s.Attributes[AttrSideEffectFence]; has {
			t.Fatalf("%s：越界时不得写出 fence 属性", c.name)
		}
		if got, has := s.Attributes[AttrSideEffectFenceOverflow]; !has || got != true {
			t.Fatalf("%s：导出面应以溢出诊断位显式标注，实得 %v (has=%v)", c.name, got, has)
		}
	}

	// 对照组：载荷没带 fence —— 标记不置位（这才是「未设置」）。
	p := NewProjector(Options{})
	p.Apply(eventbus.Event{
		Type:       eventbus.EventSideEffectGranted,
		OccurredAt: at(100),
		Payload:    map[string]any{"key": "act-a"},
	})
	row := p.Rows()[0]
	if row.SideEffectFence != 0 || row.SideEffectFenceOutOfRange {
		t.Fatalf("未设置的 fence 应保持 0 且不置越界标记: %+v", row)
	}
	if _, has := SpanOf(row).Attributes[AttrSideEffectFenceOverflow]; has {
		t.Fatal("未设置不得写出溢出诊断位")
	}
}

// TestInRangeFenceStillMeasured 范围内的值照常取用：整数逐字、JSON 数字形态
// （float64）逐字、小数按声明口径向零取整。
func TestInRangeFenceStillMeasured(t *testing.T) {
	cases := []struct {
		name  string
		fence any
		want  uint64
	}{
		{"uint64", uint64(7), 7},
		{"int64", int64(7), 7},
		{"float64 整数", float64(9), 9},
		{"小数向零取整", 3.7, 3},
	}
	for _, c := range cases {
		p := NewProjector(Options{})
		p.Apply(fenceEv(100, c.fence))
		row := p.Rows()[0]
		if row.SideEffectFence != c.want {
			t.Fatalf("%s：实得 %d，想 %d", c.name, row.SideEffectFence, c.want)
		}
		if row.SideEffectFenceOutOfRange {
			t.Fatalf("%s：范围内不该置越界标记", c.name)
		}
	}
}

// TestCheckpointSeqOverflowSameDiscipline 快照序号同一纪律：越界不猜、显形、
// 导出不伪装。
func TestCheckpointSeqOverflowSameDiscipline(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(ckEv(100, "run-a", 5, "running"))
	p.Apply(ckEv(110, "run-a", 0, "running")) // seq=0：不是一次真实提交（未设置）
	p.Apply(eventbus.Event{Type: eventbus.EventCheckpointCommitted, OccurredAt: at(120),
		Payload: map[string]any{"run_id": "run-a", "seq": 1e30, "status": "running"}})

	rows := p.Rows()
	if rows[0].CheckpointSeq != 5 || rows[0].CheckpointSeqOutOfRange {
		t.Fatalf("合法 seq 照常取用: %+v", rows[0])
	}
	if rows[1].CheckpointSeq != 0 || rows[1].CheckpointSeqOutOfRange {
		t.Fatalf("未设置的 seq 应保持 0 且不置标记: %+v", rows[1])
	}
	if rows[2].CheckpointSeq != 0 || !rows[2].CheckpointSeqOutOfRange {
		t.Fatalf("越界的 seq 应保持 0 且置标记（可与未设置区分）: %+v", rows[2])
	}
	s := SpanOf(rows[2])
	if _, has := s.Attributes[AttrCheckpointSeq]; has {
		t.Fatal("越界时不得写出 seq 属性")
	}
	if got, has := s.Attributes[AttrCheckpointSeqOverflow]; !has || got != true {
		t.Fatalf("越界的 seq 应以溢出诊断位显式标注，实得 %v (has=%v)", got, has)
	}
}

// TestExportNeverDisguisesOverflowAsLegalValue 导出面（含宿主自构 Row 的路径）：
// 超出 int64 可表示范围的字段值一律不写原属性、不产出 -1，以诊断位标注。
func TestExportNeverDisguisesOverflowAsLegalValue(t *testing.T) {
	rows := []Row{
		{
			Seq: 1, Kind: KindSideEffect, ID: "sideeffect#act-a", Turn: 1,
			SideEffectKey: "act-a", SideEffectPhase: SideEffectGranted,
			SideEffectFence: math.MaxUint64, // 直接构造的行：导出面必须自守
			StartAt:         at(0), Status: StatusOpen,
		},
		{
			Seq: 2, Kind: KindCheckpoint, ID: "checkpoint#run-a#1", Turn: 1,
			CheckpointRunID: "run-a", CheckpointSeq: math.MaxUint64,
			EndAt: at(10), Status: StatusOrphan,
		},
	}
	spans := SpansOf(rows)
	if got, has := spans[0].Attributes[AttrSideEffectFence]; has {
		t.Fatalf("MaxUint64 的 fence 不得经 int64 溢出导出（会伪装成 %v），不该写出该属性", got)
	}
	if got, has := spans[0].Attributes[AttrSideEffectFenceOverflow]; !has || got != true {
		t.Fatalf("不可表示的 fence 应以诊断位标注，实得 %v (has=%v)", got, has)
	}
	if got, has := spans[1].Attributes[AttrCheckpointSeq]; has {
		t.Fatalf("MaxUint64 的 seq 不得经 int64 溢出导出（会伪装成 %v），不该写出该属性", got)
	}
	if got, has := spans[1].Attributes[AttrCheckpointSeqOverflow]; !has || got != true {
		t.Fatalf("不可表示的 seq 应以诊断位标注，实得 %v (has=%v)", got, has)
	}
	// 对照：可表示范围内的值照常写出 int64。
	ok := Row{
		Seq: 3, Kind: KindSideEffect, ID: "sideeffect#act-b", Turn: 1,
		SideEffectKey: "act-b", SideEffectPhase: SideEffectCompleted,
		SideEffectFence: 3, StartAt: at(0), EndAt: at(10), Status: StatusClosed,
	}
	if got := SpanOf(ok).Attributes[AttrSideEffectFence]; got != int64(3) {
		t.Fatalf("范围内的 fence 应照常导出 int64，实得 %v", got)
	}
}

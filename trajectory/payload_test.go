package trajectory

// 载荷数值面的判据单独成文件（issue #305）：超范围 / NaN / Inf / 非整数一律
// 显式拒绝，不静默产出一个「看起来合法」的值；导出侧不得靠溢出语义把它变成 -1。

import (
	"math"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// ckRaw 一条快照事件（seq 用原始载荷值，便于喂病态数值）。
func ckRaw(ms int, payload map[string]any) eventbus.Event {
	return eventbus.Event{Type: eventbus.EventCheckpointCommitted, OccurredAt: at(ms), Payload: payload}
}

// ckPayload 带 run_id/status 的快照载荷；seq 由调用方决定（nil=不带该键）。
func ckPayload(seq any) map[string]any {
	m := map[string]any{"run_id": "run-a", "status": "running"}
	if seq != nil {
		m["seq"] = seq
	}
	return m
}

// TestPayloadUintRejectsInvalidNumeric 病态数值的处置：字段归零（未设置），
// 属性面不写，并进 PayloadErrors——与「键缺失」（未设置、不进错误面）可区分。
func TestPayloadUintRejectsInvalidNumeric(t *testing.T) {
	cases := []struct {
		name    string
		val     any // nil = 载荷不带 seq（未设置）
		want    uint64
		wantErr bool
	}{
		{"合法 float64 整数", float64(9), 9, false},
		{"未设置", nil, 0, false},
		{"超范围 float64", 1e30, 0, true},
		{"正无穷", math.Inf(1), 0, true},
		{"负无穷", math.Inf(-1), 0, true},
		{"NaN", math.NaN(), 0, true},
		{"非整数 3.7", 3.7, 0, true},
		{"负值", float64(-1), 0, true},
		{"int64 负值", int64(-1), 0, true},
		{"uint64", uint64(42), 42, false},
		{"int64", int64(42), 42, false},
		{"int", 42, 42, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewProjector(Options{})
			p.Apply(ckRaw(0, ckPayload(c.val)))
			rows := p.Rows()
			if len(rows) != 1 {
				t.Fatalf("应落一行：%+v", rows)
			}
			if rows[0].CheckpointSeq != c.want {
				t.Fatalf("seq = %d，want %d（病态值必须归零/未设置）", rows[0].CheckpointSeq, c.want)
			}
			errs := p.PayloadErrors()
			if c.wantErr {
				if len(errs) != 1 || errs[0].Key != "seq" || errs[0].Reason == "" {
					t.Fatalf("病态 seq 必须进显式错误面（与「未设置」可区分）：%+v", errs)
				}
				if _, has := SpanOf(rows[0]).Attributes[AttrCheckpointSeq]; has {
					t.Fatal("未设置/被拒绝的 seq 不得写出属性（不拿 0 冒充）")
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("正常值/未设置不得进错误面：%+v", errs)
			}
		})
	}
}

// TestPayloadUintFenceInvalid 副作用栅栏令牌走同一条判据。
func TestPayloadUintFenceInvalid(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(seEv(eventbus.EventSideEffectGranted, 0, "act-a", "res-a", "", 0, map[string]any{"fence": 1e30}))
	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("应落一行：%+v", rows)
	}
	if rows[0].SideEffectFence != 0 {
		t.Fatalf("超范围 fence 必须归零（未设置），实得 %d", rows[0].SideEffectFence)
	}
	if _, has := SpanOf(rows[0]).Attributes[AttrSideEffectFence]; has {
		t.Fatal("被拒绝的 fence 不得写出属性")
	}
	errs := p.PayloadErrors()
	if len(errs) != 1 || errs[0].Key != "fence" {
		t.Fatalf("病态 fence 必须进显式错误面：%+v", errs)
	}
}

// TestSpanOfSkipsOverflowingUint64 导出侧不得把 > MaxInt64 的 uint64 用溢出
// 语义写成 -1（那会让「未设置」与「超范围」撞在同一个数上）。
func TestSpanOfSkipsOverflowingUint64(t *testing.T) {
	se := Row{Kind: KindSideEffect, SideEffectPhase: SideEffectGranted, SideEffectFence: math.MaxUint64}
	s := SpanOf(se)
	if _, has := s.Attributes[AttrSideEffectFence]; has {
		t.Fatalf("超出 int64 的栅栏令牌不得导出（溢出会变成 -1）：%v", s.Attributes[AttrSideEffectFence])
	}
	if _, has := s.Attributes[AttrSideEffectFence]; has && s.Attributes[AttrSideEffectFence] == int64(-1) {
		t.Fatal("导出侧出现了 -1 这种溢出伪装成合法值的结果")
	}

	ck := Row{Kind: KindCheckpoint, CheckpointRunID: "run-a", CheckpointSeq: math.MaxUint64, CheckpointStatus: "running"}
	cs := SpanOf(ck)
	if v, has := cs.Attributes[AttrCheckpointSeq]; has {
		t.Fatalf("超出 int64 的快照序号不得导出，实得 %v", v)
	}

	// 边界内仍正常导出（不做无谓的收紧）。
	ok := Row{Kind: KindCheckpoint, CheckpointRunID: "run-a", CheckpointSeq: math.MaxInt64, CheckpointStatus: "running"}
	if got := SpanOf(ok).Attributes[AttrCheckpointSeq]; got != int64(math.MaxInt64) {
		t.Fatalf("MaxInt64 应原样导出，实得 %v", got)
	}
}

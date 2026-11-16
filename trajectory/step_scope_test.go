package trajectory

// 步骤配对的**轮次作用域**判据（issue #300）：配对作用域与行标识唯一性范围（轮）
// 一致——上一轮遗留的打开行不得被下一轮的结束事件收口；跨轮遗留的行以显式形态
// （open 行进 Finalize、结束事件落孤儿行）呈现；导出的 span 不出现
// 「子 span 结束晚于父 span 结束」。

import (
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// assertNoChildOutlivesParent 批量导出的 span 里不许出现父子时序倒挂：
// 子 span 的结束时刻晚于父 span 的结束时刻，trace 里就是「子比父活得久」。
// （夹具内行标识唯一，父子按 row_id 一步定位。）
func assertNoChildOutlivesParent(t *testing.T, rows []Row) {
	t.Helper()
	byID := make(map[string]Span, len(rows))
	for _, s := range SpansOf(rows) {
		if id, ok := s.Attributes[AttrRowID].(string); ok && id != "" {
			byID[id] = s
		}
	}
	for _, s := range byID {
		if s.ParentID == "" {
			continue
		}
		parent, ok := byID[s.ParentID]
		if !ok {
			continue // 悬空父已清成根，不在本断言范围
		}
		if s.EndAt.IsZero() || parent.EndAt.IsZero() {
			continue // 未收口的一侧没有「活得久」可言
		}
		if s.EndAt.After(parent.EndAt) {
			t.Fatalf("子 span %s（止于 %v）比父 span %s（止于 %v）活得久",
				s.ParentID, parent.EndAt, s.ParentID, s.EndAt)
		}
	}
}

// TestStepFinishFromNextTurnDoesNotRewritePriorTurnRow 实证场景（issue #300）：
// 宿主不用环节号，第 1 轮遗留打开的步骤行，被第 2 轮的第一个 step_finished
// 收掉 ⇒ 该行声称属于第 1 轮，时长却越过第 1 轮的结束时刻。
// 修复后：第 2 轮的结束事件不改写第 1 轮的行，自身落成第 2 轮的孤儿行。
func TestStepFinishFromNextTurnDoesNotRewritePriorTurnRow(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		ev(eventbus.EventStepStarted, 100, nil), // 无环节号
		ev(eventbus.EventTurnEnded, 200, nil),   // 第 1 轮收口时步骤仍未收口
		ev(eventbus.EventTurnStarted, 300, nil),
		ev(eventbus.EventStepFinished, 400, nil), // 第 2 轮的第一个结束事件
		ev(eventbus.EventTurnEnded, 500, nil),
	})

	rows := p.Rows()
	// 期望 4 行：turn#1、遗留步骤行（open）、turn#2、孤儿步骤行（Turn=2）。
	if len(rows) != 4 {
		t.Fatalf("应投影出 4 行，实得 %d：%+v", len(rows), rows)
	}
	legacy := rows[1]
	if legacy.Kind != KindStep || legacy.Status != StatusOpen || !legacy.EndAt.IsZero() {
		t.Fatalf("第 1 轮遗留的步骤行不得被第 2 轮的结束事件改写: %+v", legacy)
	}
	if legacy.Turn != 1 {
		t.Fatalf("遗留步骤行仍应归属第 1 轮，得到 %d", legacy.Turn)
	}
	if d, ok := legacy.Duration(); ok {
		t.Fatalf("遗留行未收口，不该报得出实测时长 %v", d)
	}
	orphan := rows[3]
	if orphan.Kind != KindStep || orphan.Status != StatusOrphan || orphan.Turn != 2 {
		t.Fatalf("第 2 轮的结束事件应落成第 2 轮的孤儿行（显式形态，不静默收口）: %+v", orphan)
	}
	if !orphan.EndAt.Equal(at(400)) {
		t.Fatalf("孤儿行应记下结束时刻 400ms，得到 %v", orphan.EndAt)
	}
	// 遗留行以 open 形态进 Finalize——「真的没跑完」显形，不与配对错误混同。
	open := p.Finalize()
	if len(open) != 1 || open[0].Seq != legacy.Seq {
		t.Fatalf("收口应报出第 1 轮的遗留步骤行，实得 %+v", open)
	}
	assertNoChildOutlivesParent(t, rows)
}

// TestStepIDFinishFromNextTurnDoesNotRewritePriorTurnRow 带环节号同样受轮次
// 作用域约束：环节号在两轮里各开一次是合法的（行标识唯一性范围就是轮），
// 第 2 轮的 step_finished 配的是第 2 轮的行，不是第 1 轮的同号遗留行。
func TestStepIDFinishFromNextTurnDoesNotRewritePriorTurnRow(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		stepEv(eventbus.EventStepStarted, 100, 7), // 第 1 轮的 step#7，未收口
		ev(eventbus.EventTurnEnded, 200, nil),
		ev(eventbus.EventTurnStarted, 300, nil),
		stepEv(eventbus.EventStepFinished, 400, 7), // 第 2 轮没有打开的 step#7
		ev(eventbus.EventTurnEnded, 500, nil),
	})

	rows := p.Rows()
	if len(rows) != 4 {
		t.Fatalf("应投影出 4 行，实得 %d：%+v", len(rows), rows)
	}
	legacy := rows[1]
	if legacy.Status != StatusOpen || !legacy.EndAt.IsZero() {
		t.Fatalf("第 1 轮的 step#7 不得被第 2 轮的结束事件改写: %+v", legacy)
	}
	if rows[3].Status != StatusOrphan || rows[3].Turn != 2 {
		t.Fatalf("第 2 轮的 step_finished(7) 应落成第 2 轮的孤儿行: %+v", rows[3])
	}
	assertNoChildOutlivesParent(t, rows)
}

// TestStepFinishAfterTurnCloseIsTurnlessOrphan 轮已收口、又没有新轮打开时到达
// 的结束事件：不配对（它声称的轮已经结束，收进去就是子比父活得久），落成
// **轮次之外**的孤儿行——作为根导出，不挂回已结束的轮。
func TestStepFinishAfterTurnCloseIsTurnlessOrphan(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		ev(eventbus.EventStepStarted, 100, nil),
		ev(eventbus.EventTurnEnded, 200, nil),
		ev(eventbus.EventStepFinished, 400, nil), // 迟到：第 1 轮已收口，无新轮
	})

	rows := p.Rows()
	if len(rows) != 3 {
		t.Fatalf("应投影出 3 行，实得 %d：%+v", len(rows), rows)
	}
	if rows[1].Status != StatusOpen || !rows[1].EndAt.IsZero() {
		t.Fatalf("遗留步骤行不得被收口: %+v", rows[1])
	}
	orphan := rows[2]
	if orphan.Status != StatusOrphan || orphan.Turn != 0 {
		t.Fatalf("轮次之外到达的结束事件应落成 Turn=0 的孤儿行（不挂回已结束的轮）: %+v", orphan)
	}
	if !orphan.EndAt.Equal(at(400)) {
		t.Fatalf("孤儿行应记下结束时刻，得到 %v", orphan.EndAt)
	}
	assertNoChildOutlivesParent(t, rows)
}

// TestSameTurnStepPairingUnaffected 轮次作用域不破坏同轮配对：
// 同轮两条无环节号步骤行仍按先进先出配对（既有语义，回归钉）。
func TestSameTurnStepPairingUnaffected(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventTurnStarted, 0, nil),
		ev(eventbus.EventStepStarted, 100, nil),
		ev(eventbus.EventStepStarted, 150, nil),
		ev(eventbus.EventStepFinished, 200, nil), // 配第一条（FIFO）
		ev(eventbus.EventStepFinished, 250, nil), // 配第二条
		ev(eventbus.EventTurnEnded, 300, nil),
	})

	rows := p.Rows()
	if len(rows) != 3 {
		t.Fatalf("应投影出 3 行，实得 %d：%+v", len(rows), rows)
	}
	first, second := rows[1], rows[2]
	if !first.EndAt.Equal(at(200)) || !second.EndAt.Equal(at(250)) {
		t.Fatalf("同轮无环节号步骤仍应先进先出配对: %v / %v", first.EndAt, second.EndAt)
	}
	d1, ok1 := first.Duration()
	d2, ok2 := second.Duration()
	if !ok1 || d1 != 100*time.Millisecond || !ok2 || d2 != 100*time.Millisecond {
		t.Fatalf("同轮配对的实测时长应为 100ms/100ms，实得 %v(ok=%v) / %v(ok=%v)", d1, ok1, d2, ok2)
	}
	if open := p.Finalize(); len(open) != 0 {
		t.Fatalf("同轮配对后不应有未收口行: %+v", open)
	}
	assertNoChildOutlivesParent(t, rows)
}

// TestTurnlessStepsStillPair 没有任何轮次的事件流（轮次外上下文）里，
// 无环节号步骤照常配对——作用域收紧不误伤轮次外的正常投影。
func TestTurnlessStepsStillPair(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		ev(eventbus.EventStepStarted, 100, nil),
		ev(eventbus.EventStepFinished, 200, nil),
	})

	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("轮次外上下文应照常配对出 1 行，实得 %d", len(rows))
	}
	if rows[0].Turn != 0 || rows[0].Status != StatusClosed {
		t.Fatalf("轮次外步骤行 Turn=0 且已收口: %+v", rows[0])
	}
	if d, ok := rows[0].Duration(); !ok || d != 100*time.Millisecond {
		t.Fatalf("轮次外配对的实测时长应为 100ms，实得 %v (ok=%v)", d, ok)
	}
}

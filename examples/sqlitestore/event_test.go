// event_test.go —— 事件面的契约判据（游标铸造、水位线、无空洞推进）。
package sqlitestore

import (
	"context"
	"testing"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

func evtOf(typ string) eventbus.Event {
	return eventbus.Event{Type: typ, Level: "info", Payload: map[string]any{"k": "v"}}
}

// TestEventCursorFromWatermark 游标由存储铸造且与已提交水位线同值同源。
func TestEventCursorFromWatermark(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ev := s.Events()

	// 空表：游标空间为空。
	maxID, err := ev.MaxID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if maxID != 0 {
		t.Fatalf("空表 MaxID 应为 0，实得 %d", maxID)
	}
	wm, err := ev.CommittedWatermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wm != maxID {
		t.Fatalf("MaxID 与已提交水位线必须同值同源：%d vs %d", maxID, wm)
	}

	for i := range 3 {
		id, err := ev.Insert(ctx, evtOf("e"))
		if err != nil {
			t.Fatal(err)
		}
		if id != uint64(i+1) {
			t.Fatalf("游标必须连续：第 %d 条实得 %d", i+1, id)
		}
	}
	// 输入侧 ID 一律忽略（游标由存储铸造）。
	stale := evtOf("e")
	stale.ID = 999
	id, err := ev.Insert(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	if id != 4 {
		t.Fatalf("输入侧 ID 必须被忽略，实得 %d", id)
	}

	maxID, err = ev.MaxID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if maxID != 4 {
		t.Fatalf("MaxID 应为最新已提交游标，实得 %d", maxID)
	}
	wm, err = ev.CommittedWatermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wm != maxID {
		t.Fatalf("MaxID 与已提交水位线必须同值同源：%d vs %d", maxID, wm)
	}
}

// TestEventListAfterContiguous 尾随读侧返回段连续可见（无空洞推进）。
func TestEventListAfterContiguous(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ev := s.Events()

	for range 5 {
		if _, err := ev.Insert(ctx, evtOf("e")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ev.ListAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("应读回 5 条，实得 %d", len(got))
	}
	for i, e := range got {
		if e.ID != uint64(i+1) {
			t.Fatalf("返回段必须从 cursor+1 起连续可见：第 %d 条实得 %d", i+1, e.ID)
		}
	}
	// 游标推进后只读新的。
	got, err = ev.ListAfter(ctx, 3, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 4 {
		t.Fatalf("游标之后应只剩 2 条且从 4 起：%+v", got)
	}
	// 单拍上限生效。
	got, err = ev.ListAfter(ctx, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("单拍上限应生效，实得 %d 条", len(got))
	}
}

// TestEventRollbackDoesNotBurnID 回滚不烧号：失败的事务不推进水位线。
func TestEventRollbackDoesNotBurnID(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	boom := oerrors.New("注入的失败")
	err = s.Apply(ctx, func(tx *Tx) error {
		if _, err := tx.Events().Insert(ctx, evtOf("e")); err != nil {
			return err
		}
		return boom
	})
	if err == nil {
		t.Fatal("回调返回错误时事务必须失败")
	}
	// 水位线没动 ⇒ 下一个事务重新从同一点派生（没烧号）。
	id, err := s.Events().Insert(ctx, evtOf("e"))
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("回滚不烧号：实得 id=%d", id)
	}
	wm, err := s.Events().CommittedWatermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wm != 1 {
		t.Fatalf("水位线应为 1，实得 %d", wm)
	}
}

// TestEventPersistsAcrossInstances 事件行跨实例可见（重启不丢事件）。
func TestEventPersistsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s1.Close() })
	for range 3 {
		if _, err := s1.Events().Insert(ctx, evtOf("e")); err != nil {
			t.Fatal(err)
		}
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got, err := s2.Events().ListAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("重启后事件应完整，实得 %d 条", len(got))
	}
}

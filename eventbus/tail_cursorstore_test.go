package eventbus

// issue #183 的 CursorStore 判据（游标持久化接缝）：重启补灌 / Load 失败停摆 /
// Save 失败不停摆 / 首次部署回退 MaxID。
//
// 从 tail_cursor_test.go 拆出（拆分时该文件已顶到 max_file_lines 门禁）：
// 游标持久化是 #183 的独立契约面，与「无空洞推进」的形态互证判据分文件各管
// 一段；判据本体一字未改。

import (
	"context"
	"sync"
	"testing"
	"time"
)

// memCursorStore 进程内 CursorStore 替身。
type memCursorStore struct {
	mu       sync.Mutex
	cur      uint64
	saved    bool
	failLoad int
	failSave int
}

func (c *memCursorStore) LoadCursor(context.Context) (uint64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failLoad > 0 {
		c.failLoad--
		return 0, false, errFakeRead
	}
	return c.cur, c.saved, nil
}

func (c *memCursorStore) SaveCursor(_ context.Context, cursor uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failSave > 0 {
		c.failSave--
		return errFakeRead
	}
	c.cur, c.saved = cursor, true
	return nil
}

// TestTailerCursorPersistsAcrossRestart 重启补灌（#183 判据二）：停机窗口内
// 发布的事件，重启后从持久化游标续随仍被补灌。
//
// 对照面（缺省形态）：重启走 MaxID 初始化 ⇒ 停机窗口内的事件被整个跳过。
func TestTailerCursorPersistsAcrossRestart(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	cursor := &memCursorStore{}
	hub := NewHub()

	// 第一程：初始化（MaxID=5）并干净退出（等 Run 真返回，避免取消在途时
	// 它再处理一拍——停机窗口的起点必须确定）。
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx1)
		close(done1)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for tail.maxIDs() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("第一程 2s 内未完成初始化")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel1()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("第一程 ctx 取消后未返回")
	}

	// 停机窗口：他副本发布 6。
	tail.append(Event{ID: 6, SessionID: "s", Type: "during-downtime"})

	// 第二程（重启）：必须从持久化游标 5 续随——不得走 MaxID（那会跳过 6）。
	maxIDBeforeRestart := tail.maxIDs()
	ch, stop := hub.Subscribe("s")
	defer stop()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx2)

	ev := waitTail(t, ch)
	if ev.ID != 6 {
		t.Fatalf("停机窗口内的事件必须被补灌（实得 %d——MaxID 初始化把它跳过了）", ev.ID)
	}
	if got := tail.maxIDs(); got != maxIDBeforeRestart {
		t.Fatalf("第二程不得重走 MaxID 初始化（%d → %d）", maxIDBeforeRestart, got)
	}
	// 游标推进后已保存：重启场景下持久化值应为 6。
	//
	// 必须**等**它落盘：实现是「逐条 Publish（同步写入订阅者 channel）→ 整批
	// 处理完再 SaveCursor」（见 tail.go 的 Publish 与 SaveCursor 两处）。订阅者
	// 拿到事件的那一刻，Tailer 还没走到保存——直接读会读到上一批的值。CI 实测
	// 偶发红（run 43896997349：实得 5），本地高频复跑不复现（窗口在微秒级）。
	//
	// 等的是「最终持久化」（与实现契约一致）；超时说明真的没存，那就是实现
	// 缺陷，报错带实得值便于定位。
	var saved uint64
	persistDeadline := time.Now().Add(2 * time.Second)
	for {
		cursor.mu.Lock()
		saved = cursor.cur
		cursor.mu.Unlock()
		if saved == 6 {
			break
		}
		if time.Now().After(persistDeadline) {
			t.Fatalf("游标推进后应持久化（2s 内仍实得 %d）", saved)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTailerCursorLoadFailureStallsNeverSkips Load 失败停摆重试（#183）：
// 绝不静默回退 MaxID——回退 = 把停机窗口整个跳过（宁可停摆不可跳读）。
func TestTailerCursorLoadFailureStallsNeverSkips(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	tail.append(Event{ID: 6, SessionID: "s", Type: "during-downtime"})
	cursor := &memCursorStore{saved: true, cur: 5, failLoad: 1000} // 持续失败：先验证停摆语义
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx)

	// 多拍之后：MaxID 不得被调（回退即跳读），事件不得送达（停摆中）。
	time.Sleep(80 * time.Millisecond)
	if got := tail.maxIDs(); got != 0 {
		t.Fatalf("Load 失败不得回退 MaxID 初始化（调了 %d 次 = 跳读停机窗口）", got)
	}
	if len(tail.cursors()) != 0 {
		t.Fatal("Load 失败期间不得读存量")
	}
	// 恢复后从持久化游标续随，补灌照常。
	cursor.mu.Lock()
	cursor.failLoad = 0
	cursor.mu.Unlock()
	if ev := waitTail(t, ch); ev.ID != 6 {
		t.Fatalf("Load 恢复后应从游标 5 续随并补灌 6, got %d", ev.ID)
	}
}

// TestTailerCursorSaveFailureKeepsTailing Save 失败不停摆：游标落后只会
// 导致重启后重读（订阅侧幂等吸收），停摆反而拆了实时补灌桥。
func TestTailerCursorSaveFailureKeepsTailing(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	cursor := &memCursorStore{failSave: 1}
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx)

	tail.append(Event{ID: 6, SessionID: "s", Type: "x1"})
	if ev := waitTail(t, ch); ev.ID != 6 {
		t.Fatalf("Save 失败不得停摆投递, got %d", ev.ID)
	}
	tail.append(Event{ID: 7, SessionID: "s", Type: "x2"})
	if ev := waitTail(t, ch); ev.ID != 7 {
		t.Fatalf("后续事件应照常补灌, got %d", ev.ID)
	}
	// 恢复后的推进仍会落盘：最终持久化值应跟上游标。
	deadline := time.Now().Add(2 * time.Second)
	for {
		cursor.mu.Lock()
		saved := cursor.cur
		cursor.mu.Unlock()
		if saved == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Save 恢复后游标应落盘到 7, 实得 %d", saved)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTailerCursorNotFoundFallsBackToMaxID 首次部署（从未存过游标）回退
// MaxID 初始化——持久化面不改变「补灌不是回放」的缺省语义。
func TestTailerCursorNotFoundFallsBackToMaxID(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	cursor := &memCursorStore{} // saved=false
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for tail.maxIDs() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("未存过游标应回退 MaxID 初始化（2s 内未发生）")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case ev := <-ch:
		t.Fatalf("回退 MaxID 后存量不得回放（补灌不是回放）: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

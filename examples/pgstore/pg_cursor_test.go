// pg_cursor_test.go —— 尾随游标持久化（eventbus.CursorStore）的真 PG 判据
// （issue #264 的「崩溃点重启续读 / 并发尾随」验收面）。
//
// 跳过与硬失败的边界与 pg_event_test.go 同口径：场景硬失败；唯一的 skip 在
// 环境入口（GAK_PGREF_DSN 未设置，Skipf 写明原因与起法）。机制逻辑另有
// eventbus 的进程内判据（tail_cursorstore_test.go），两处同源互证。
//
// 判据：
//   - TestTailCursorStoreRestartResumesFromSavedCursor：第一程停在哪，第二程
//     从哪续——停机窗口内发布的事件必须补达，且不得重走 MaxID 初始化
//     （那会把窗口内的事件跳过）；
//   - TestTailCursorStoreCrashBeforeSaveRereadsNoSkip：崩溃在「投递后、保存
//     前」⇒ 持久化游标落后 ⇒ 重启后重读（不跳号），订阅侧幂等吸收；
//   - TestTailCursorStoreConcurrentTailers：两个副本各持独立游标并发尾随
//     同一事件面，双方都按序收齐全部帧，无空洞；
//   - TestTailCursorStoreMonotonicSave：游标单调不减（陈旧副本的迟到保存
//     不得把游标拉回去）。
package pgstore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// countingTailStore 数 MaxID 调用的 TailStore 包装：重启续读必须走
// LoadCursor，不得回退 MaxID 初始化（回退 = 跳过停机窗口）。
type countingTailStore struct {
	eventbus.TailStore
	maxIDCalls atomic.Int32
}

func (s *countingTailStore) MaxID(ctx context.Context) (uint64, error) {
	s.maxIDCalls.Add(1)
	return s.TailStore.MaxID(ctx)
}

// waitTailEvent 等一帧事件（3s 超时即硬失败）。
func waitTailEvent(t *testing.T, ch <-chan eventbus.Event, what string) eventbus.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatalf("3s 内未收到%s", what)
		return eventbus.Event{}
	}
}

// waitTailCursor 等持久化游标到达 want（轮询到 3s 超时）。
func waitTailCursor(t *testing.T, cs *TailCursorStore, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, found, err := cs.LoadCursor(context.Background())
		if err != nil {
			t.Fatalf("读游标失败: %v", err)
		}
		if found && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("3s 内游标未到 %d（found=%v got=%d）", want, found, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func mustInsert(t *testing.T, evs *EventStore, typ string) uint64 {
	t.Helper()
	id, err := evs.Insert(context.Background(), eventbus.Event{SessionID: "s", Type: typ})
	if err != nil {
		t.Fatalf("发布 %s 失败: %v", typ, err)
	}
	return id
}

func assertNoGap(t *testing.T, gaps <-chan [2]uint64, wait time.Duration) {
	t.Helper()
	select {
	case g := <-gaps:
		t.Fatalf("守约实现不得触发空洞哨兵: [%d,%d]", g[0], g[1])
	case <-time.After(wait):
	}
}

// TestTailCursorStoreRestartResumesFromSavedCursor 崩溃点重启续读：第一程
// 初始化游标 0 → 消费 1..3 → 持久化 3 → 退出；停机窗口发布 4..5；第二程
// 必须从 3 续随并补达 4..5，且**不得重走 MaxID 初始化**。
func TestTailCursorStoreRestartResumesFromSavedCursor(t *testing.T) {
	evs, pool := freshEventStore(t)
	cs := NewTailCursorStore(pool, "replica-a")
	ctx := context.Background()

	if _, found, err := cs.LoadCursor(ctx); err != nil || found {
		t.Fatalf("首次部署应 found=false：found=%v err=%v", found, err)
	}

	hub1 := eventbus.NewHub()
	ch1, stop1 := hub1.Subscribe("s")
	defer stop1()
	run1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		eventbus.NewTailer(evs, hub1,
			eventbus.WithTailInterval(10*time.Millisecond),
			eventbus.WithTailCursorStore(cs),
		).Run(run1)
		close(done1)
	}()
	waitTailCursor(t, cs, 0) // 初始化完成并落盘，才能确定停机窗口起点

	for i, typ := range []string{"e1", "e2", "e3"} {
		if id := mustInsert(t, evs, typ); id != uint64(i+1) {
			t.Fatalf("%s 应铸 %d, got %d", typ, i+1, id)
		}
	}
	for i, typ := range []string{"e1", "e2", "e3"} {
		ev := waitTailEvent(t, ch1, typ)
		if ev.ID != uint64(i+1) || ev.Type != typ {
			t.Fatalf("第一程应按序收到 %d(%s), got %d(%s)", i+1, typ, ev.ID, ev.Type)
		}
	}
	waitTailCursor(t, cs, 3)

	cancel1()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("第一程 ctx 取消后未返回")
	}

	// 停机窗口：他副本继续发布。
	mustInsert(t, evs, "during-downtime-4")
	mustInsert(t, evs, "during-downtime-5")

	counted := &countingTailStore{TailStore: evs}
	hub2 := eventbus.NewHub()
	ch2, stop2 := hub2.Subscribe("s")
	defer stop2()
	gaps := make(chan [2]uint64, 8)
	run2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go eventbus.NewTailer(counted, hub2,
		eventbus.WithTailInterval(10*time.Millisecond),
		eventbus.WithTailCursorStore(cs),
		eventbus.WithTailOnGap(func(from, to uint64) { gaps <- [2]uint64{from, to} }),
	).Run(run2)

	if ev := waitTailEvent(t, ch2, "停机窗口内的事件"); ev.ID != 4 {
		t.Fatalf("第二程必须从持久化游标 3 续随（先送 4），实得 %d", ev.ID)
	}
	if ev := waitTailEvent(t, ch2, "停机窗口内的事件"); ev.ID != 5 {
		t.Fatalf("停机窗口内的事件必须补达 5, 实得 %d", ev.ID)
	}
	if n := counted.maxIDCalls.Load(); n != 0 {
		t.Fatalf("重启续读不得重走 MaxID 初始化（调了 %d 次 = 跳过停机窗口）", n)
	}
	assertNoGap(t, gaps, 80*time.Millisecond)
	waitTailCursor(t, cs, 5)
}

// TestTailCursorStoreCrashBeforeSaveRereadsNoSkip 崩溃点在「投递后、保存前」：
// 持久化游标落后于已投递位置 ⇒ 重启后重读（订阅侧幂等吸收），绝不跳号。
func TestTailCursorStoreCrashBeforeSaveRereadsNoSkip(t *testing.T) {
	evs, pool := freshEventStore(t)
	cs := NewTailCursorStore(pool, "replica-a")
	ctx := context.Background()

	for _, typ := range []string{"e1", "e2", "e3"} {
		mustInsert(t, evs, typ)
	}
	// 上一程投递了 1..2 后崩溃，游标只落盘到 2（3 尚未保存）。
	if err := cs.SaveCursor(ctx, 2); err != nil {
		t.Fatal(err)
	}

	hub := eventbus.NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()
	gaps := make(chan [2]uint64, 8)
	run, cancel := context.WithCancel(context.Background())
	defer cancel()
	go eventbus.NewTailer(evs, hub,
		eventbus.WithTailInterval(10*time.Millisecond),
		eventbus.WithTailCursorStore(cs),
		eventbus.WithTailOnGap(func(from, to uint64) { gaps <- [2]uint64{from, to} }),
	).Run(run)

	if ev := waitTailEvent(t, ch, "重读的 3"); ev.ID != 3 {
		t.Fatalf("保存前崩溃必须重读 3（不得跳过），实得 %d", ev.ID)
	}
	mustInsert(t, evs, "e4")
	if ev := waitTailEvent(t, ch, "崩溃点之后的 4"); ev.ID != 4 {
		t.Fatalf("崩溃点之后应续读 4，实得 %d", ev.ID)
	}
	assertNoGap(t, gaps, 80*time.Millisecond)
	waitTailCursor(t, cs, 4)
}

// TestTailCursorStoreConcurrentTailers 并发尾随：两个副本各持独立游标并发
// 读同一事件面，双方都按序收齐全部帧——读侧并发不破坏「无空洞推进」。
func TestTailCursorStoreConcurrentTailers(t *testing.T) {
	evs, pool := freshEventStore(t)
	const n = 8
	for i := 1; i <= n; i++ {
		if id := mustInsert(t, evs, "e"); id != uint64(i) {
			t.Fatalf("第 %d 铸号应为 %d, got %d", i, i, id)
		}
	}

	type replica struct {
		cs     *TailCursorStore
		hub    *eventbus.Hub
		ch     <-chan eventbus.Event
		gaps   chan [2]uint64
		cancel context.CancelFunc
		done   chan struct{}
	}
	reps := make([]*replica, 0, 2)
	for _, key := range []string{"replica-a", "replica-b"} {
		r := &replica{
			cs:   NewTailCursorStore(pool, key),
			hub:  eventbus.NewHub(),
			gaps: make(chan [2]uint64, 8),
			done: make(chan struct{}),
		}
		var stop func()
		r.ch, stop = r.hub.Subscribe("s")
		t.Cleanup(stop)
		run, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		go func(r *replica) {
			defer close(r.done)
			eventbus.NewTailer(evs, r.hub,
				eventbus.WithTailInterval(10*time.Millisecond),
				eventbus.WithTailStartCursor(0),
				eventbus.WithTailCursorStore(r.cs),
				eventbus.WithTailOnGap(func(from, to uint64) { r.gaps <- [2]uint64{from, to} }),
			).Run(run)
		}(r)
		reps = append(reps, r)
	}
	t.Cleanup(func() {
		for _, r := range reps {
			r.cancel()
			<-r.done
		}
	})

	for _, r := range reps {
		for i := 1; i <= n; i++ {
			ev := waitTailEvent(t, r.ch, "并发尾随帧")
			if ev.ID != uint64(i) {
				t.Fatalf("并发尾随必须按序收齐（期望 %d，实得 %d）", i, ev.ID)
			}
		}
	}
	for _, r := range reps {
		assertNoGap(t, r.gaps, 80*time.Millisecond)
		waitTailCursor(t, r.cs, n)
	}
}

// TestTailCursorStoreMonotonicSave 游标单调不减：陈旧副本的迟到保存不得把
// 游标拉回去；不同 consumer key 互不干扰。
func TestTailCursorStoreMonotonicSave(t *testing.T) {
	_, pool := freshEventStore(t)
	ctx := context.Background()
	cs := NewTailCursorStore(pool, "replica-a")

	if err := cs.SaveCursor(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := cs.SaveCursor(ctx, 3); err != nil { // 迟到回退写：吸收，不落库
		t.Fatal(err)
	}
	got, found, err := cs.LoadCursor(ctx)
	if err != nil || !found || got != 5 {
		t.Fatalf("回退写不得拉低游标：found=%v got=%d err=%v", found, got, err)
	}
	if err := cs.SaveCursor(ctx, 5); err != nil { // 等值保存：幂等
		t.Fatal(err)
	}
	if got, _, _ := cs.LoadCursor(ctx); got != 5 {
		t.Fatalf("等值保存后游标应仍为 5, got %d", got)
	}

	other := NewTailCursorStore(pool, "replica-b")
	if _, found, err := other.LoadCursor(ctx); err != nil || found {
		t.Fatalf("不同 consumer key 必须互不干扰（应 found=false）：found=%v err=%v", found, err)
	}
}

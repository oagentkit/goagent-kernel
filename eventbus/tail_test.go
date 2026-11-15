package eventbus

// event-tail 跨副本补灌面契约（issue #166）：尾随补达 / 游标初始化 /
// 读失败游标不动 / 重复帧由订阅侧游标幂等吸收 / ctx 取消干净退出。

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeTailStore 进程内 TailStore 替身（并发安全：Run 在测试 goroutine 外跑）。
type fakeTailStore struct {
	mu sync.Mutex

	maxID      uint64
	evs        []Event // 按 ID 升序
	failMaxID  int     // 前 N 次 MaxID 返回错误
	failList   int     // 前 N 次 ListAfter 返回错误
	maxIDCalls int
	listCalls  []uint64 // 被请求的游标序列
}

func (s *fakeTailStore) MaxID(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxIDCalls++
	if s.failMaxID > 0 {
		s.failMaxID--
		return 0, errFakeRead
	}
	return s.maxID, nil
}

func (s *fakeTailStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls = append(s.listCalls, cursor)
	if s.failList > 0 {
		s.failList--
		return nil, errFakeRead
	}
	var out []Event
	for _, ev := range s.evs {
		if ev.ID <= cursor {
			continue
		}
		out = append(out, ev)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *fakeTailStore) append(evs ...Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, evs...)
	for i := len(s.evs) - 1; i > 0; i-- { // 保序（测试插入无序也按升序契约回出）
		if s.evs[i].ID < s.evs[i-1].ID {
			s.evs[i], s.evs[i-1] = s.evs[i-1], s.evs[i]
		}
	}
}

func (s *fakeTailStore) cursors() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.listCalls...)
}

func (s *fakeTailStore) maxIDs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxIDCalls
}

// errFakeRead 读失败替身错误。
var errFakeRead = errTailFake("存储瞬时抖动")

// errTailFake 测试用哨兵（合成数据，不入 docs/error-semantics.md——只在 _test.go 内）。
type errTailFake string

func (e errTailFake) Error() string { return string(e) }

// waitTail 等订阅通道给出下一帧（失败即 t.Fatal）。
func waitTail(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到尾随帧")
		return Event{}
	}
}

// TestTailerDefaultsMatchHostParams 钉住缺省参数（1s / 1000，与宿主原实现同参）。
//
// 「他副本事件 ≤1s 进 Hub」这条承诺由**缺省间隔**兑现，行为用例刻意用短间隔
// 跑（1s 真实等待在 -race 与负载下余量不足——时间上界判据要么差分要么给足
// 余量，见 AGENTS.md），故缺省值必须有单独的钉子。
func TestTailerDefaultsMatchHostParams(t *testing.T) {
	tailer := NewTailer(&fakeTailStore{}, NewHub())
	if tailer.interval != time.Second {
		t.Fatalf("缺省尾随间隔应为 1s（≤1s 补达的来源），got %s", tailer.interval)
	}
	if tailer.limit != 1000 {
		t.Fatalf("缺省批量应为 1000，got %d", tailer.limit)
	}
	if !tailer.useMaxID {
		t.Fatal("缺省应走 MaxID 初始化")
	}
}

// TestTailerDeliversNewEventsToHub 尾随补达：他副本（或直投窗口缺口）的新事件
// 经轮询进入本进程 Hub，帧携带存储铸造的权威 ID。
func TestTailerDeliversNewEventsToHub(t *testing.T) {
	store := &fakeTailStore{maxID: 0}
	hub := NewHub()
	ch, stop := hub.Subscribe("sess-tail")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub, WithTailInterval(10*time.Millisecond)).Run(ctx)

	store.append(Event{ID: 1, SessionID: "sess-tail", Type: "x"})
	ev := waitTail(t, ch)
	if ev.ID != 1 {
		t.Fatalf("帧应携带存储铸造的权威 ID=1, got %d", ev.ID)
	}
	if ev.SessionID != "sess-tail" {
		t.Fatalf("帧应投给对应会话, got %q", ev.SessionID)
	}
}

// TestTailerInitializesCursorFromMaxID 初始化取 MaxID：首拍只读游标、不回放
// 存量（本面是补灌不是回放），此后才按 id>游标 读新事件。
func TestTailerInitializesCursorFromMaxID(t *testing.T) {
	store := &fakeTailStore{
		maxID: 5,
		evs: []Event{ // 存量 1..5：不得被回灌
			{ID: 1, SessionID: "s", Type: "old"},
			{ID: 5, SessionID: "s", Type: "old"},
		},
	}
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub, WithTailInterval(10*time.Millisecond)).Run(ctx)

	// 等初始化拍落地（MaxID 被调、尚未读到新事件）。
	deadline := time.Now().Add(2 * time.Second)
	for store.maxIDs() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("2s 内 MaxID 未被调用（初始化拍缺失）")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case ev := <-ch:
		t.Fatalf("存量事件不得被回灌（本面是补灌不是回放）: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	store.append(Event{ID: 6, SessionID: "s", Type: "new"})
	ev := waitTail(t, ch)
	if ev.ID != 6 {
		t.Fatalf("新事件应以权威 ID 送达, got %d", ev.ID)
	}
	if got := store.maxIDs(); got != 1 {
		t.Fatalf("MaxID 只应在初始化时调用一次, got %d", got)
	}
}

// TestTailerReadFailureKeepsCursor 读失败游标不动：下一拍用同一游标幂等补读，
// 不丢不重；初始化失败同理（不推进任何状态、不拿 0 去读全量）。
func TestTailerReadFailureKeepsCursor(t *testing.T) {
	store := &fakeTailStore{maxID: 5, failMaxID: 1, failList: 1}
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub, WithTailInterval(10*time.Millisecond)).Run(ctx)

	store.append(Event{ID: 6, SessionID: "s", Type: "x"})

	ev := waitTail(t, ch) // 失败拍之后的那一帧必须到达
	if ev.ID != 6 {
		t.Fatalf("补读应送达 ID=6, got %d", ev.ID)
	}
	cursors := store.cursors()
	if len(cursors) == 0 {
		t.Fatal("ListAfter 从未被调用")
	}
	for i, c := range cursors {
		if c != 5 {
			t.Fatalf("第 %d 次读请求游标应保持 5（初始化值），got %d（游标被失败推动即丢事件）", i, c)
		}
	}
	if got := store.maxIDs(); got < 2 {
		t.Fatalf("初始化失败后应重试 MaxID（不得拿 0 读全量）, got %d 次", got)
	}
}

// TestTailerDuplicateFrameAbsorbedBySubscriberCursor 重复帧不归 Tailer 去重：
// 直投帧与稍后尾随读回的同一条帧都到达订阅者，由订阅侧 after 游标幂等吸收
// （与 #160「ID 由谁赋值」同一分工纪律——Tailer 只保证带上权威 ID）。
func TestTailerDuplicateFrameAbsorbedBySubscriberCursor(t *testing.T) {
	store := &fakeTailStore{maxID: 5}
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub, WithTailInterval(10*time.Millisecond)).Run(ctx)

	// 本副本直投（权威行 6 已落库，稍后尾随会再读回同一条）。
	dup := Event{ID: 6, SessionID: "s", Type: "x"}
	hub.Publish(dup)
	store.append(dup)

	// 订阅侧游标幂等吸收（SSE after-cursor 同款判定）。
	var frames []Event
	var applied []uint64
	var after uint64
	deadline := time.Now().Add(2 * time.Second)
	for len(applied) < 1 || len(frames) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("2s 内未凑齐重复投递帧（frames=%d applied=%d）", len(frames), len(applied))
		}
		select {
		case ev := <-ch:
			frames = append(frames, ev)
			if ev.ID <= after {
				continue // 重复帧：幂等吸收
			}
			after = ev.ID
			applied = append(applied, ev.ID)
		case <-time.After(500 * time.Millisecond):
			// 直投帧先到、尾随帧后到：窗口内只到一帧是正常的，继续等。
		}
	}
	if len(applied) != 1 || applied[0] != 6 {
		t.Fatalf("同一 ID 只应被消费侧应用一次, applied=%v", applied)
	}
	if len(frames) < 2 {
		t.Fatalf("重复帧应实际到达两次（去重不归 Tailer）, frames=%d", len(frames))
	}
	for _, ev := range frames {
		if ev.ID != 6 {
			t.Fatalf("两帧都应携带权威 ID=6, got %d", ev.ID)
		}
	}
}

// TestTailerRunReturnsCleanlyOnCancel ctx 取消即退出：循环干净返回、此后不再
// 碰存储（无泄漏 goroutine——Run 同步返回，ticker 随 defer 停止）。
func TestTailerRunReturnsCleanlyOnCancel(t *testing.T) {
	store := &fakeTailStore{maxID: 0}
	hub := NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewTailer(store, hub, WithTailInterval(10*time.Millisecond)).Run(ctx)
		close(done)
	}()

	// 先让循环转起来（至少完成初始化拍）。
	deadline := time.Now().Add(2 * time.Second)
	for store.maxIDs() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("2s 内循环未启动")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 2s 内 Run 未返回（循环退出不干净）")
	}
	before := len(store.cursors())
	time.Sleep(50 * time.Millisecond)
	if after := len(store.cursors()); after != before {
		t.Fatalf("Run 返回后仍在读存储（泄漏）: %d → %d", before, after)
	}
}

// TestTailerStartCursorSkipsMaxInit 显式起始游标跳过 MaxID 初始化。
func TestTailerStartCursorSkipsMaxInit(t *testing.T) {
	store := &fakeTailStore{maxID: 99}
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub, WithTailInterval(10*time.Millisecond), WithTailStartCursor(5)).Run(ctx)

	store.append(Event{ID: 6, SessionID: "s", Type: "x"})
	ev := waitTail(t, ch)
	if ev.ID != 6 {
		t.Fatalf("应从显式游标 5 起随并送达 6, got %d", ev.ID)
	}
	if got := store.maxIDs(); got != 0 {
		t.Fatalf("显式游标模式不得调用 MaxID, got %d 次", got)
	}
	for _, c := range store.cursors() {
		if c != 5 {
			t.Fatalf("游标应恒为显式起点 5, got %d", c)
		}
	}
}

// TestNewTailerRejectsNilDeps 装配点 fail fast：nil 存储/Hub 直接 panic
// （半成品装配只会空转，不留到运行时）。
func TestNewTailerRejectsNilDeps(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil store 应 panic")
		}
	}()
	NewTailer(nil, NewHub())
}

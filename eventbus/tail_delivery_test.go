package eventbus

// event-tail 跨副本补灌面的**补达与初始化**契约面（issue #166）：
// 缺省参数（1s / 1000，与宿主原实现同参）/ 新事件经轮询进入本进程 Hub /
// 初始化取 MaxID（首拍只读游标、**不回灌存量**——本面是补灌不是回放）/
// 显式起始游标跳过 MaxID 初始化。
//
// 本文件由 eventbus/tail_test.go 按关注点拆出（#443：原文件 379/400 行，逼近
// 结构门禁 max_file_lines ⇒ 再加一条判据就撞门禁）。夹具与共享助手在
// tail_fixture_test.go，只此一份。

import (
	"context"
	"testing"
	"time"
)

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
		t.Fatalf("缺省批量应为 1000, got %d", tailer.limit)
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
//
// # 缺席断言的尺子：可观测的读拍数，不是墙钟窗口（#432 / #443）
//
// 原写法是「等初始化落地 → 睡 50ms → 断言窗口内无事件」：窗口落空时
// 什么都没验却绿（假红），坏实现慢一点才回灌时守卫也抓不住（白测）。
// 现在改成三步，每步都落在**可观测事实**上：
//
//  1. 等初始化拍落地（MaxID 被调过——事实）；
//  2. 等尾随**跑满 20 读拍**（夹具数出来的读拍数——事实，与负载无关）；
//  3. 此刻判「通道里仍无事件」（非阻塞 drain）⇒ 有就是「跑够拍还吐存量」。
//
// 第 2 步的 20 拍不是随手写的：旧的 50ms 窗口在 10ms 节拍下约等于 5 拍，
// 只给前 5 拍做坏事的机会；20 拍把「慢十几拍才回灌」的坏实现也盖住
// （坏实现只要在自己的某一拍里回灌，就跑不掉）。
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
	// 等机制确实跑过 N 读拍 ⇒ 再判缺席（尺子见 waitTailReadBeats）。
	waitTailReadBeats(t, store, tailAbsenceReadBeats)
	assertNoTailFrame(t, ch, tailAbsenceReadBeats)

	store.append(Event{ID: 6, SessionID: "s", Type: "new"})
	ev := waitTail(t, ch)
	if ev.ID != 6 {
		t.Fatalf("新事件应以权威 ID 送达, got %d", ev.ID)
	}
	if got := store.maxIDs(); got != 1 {
		t.Fatalf("MaxID 只应在初始化时调用一次, got %d", got)
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

package eventbus

// event-tail 跨副本补灌面的**读失败与幂等**契约面（issue #166）：
// 读失败游标不动（下一拍用同一游标幂等补读，不丢不重）/ 重复帧不归 Tailer
// 去重（直投帧与尾随读回的同一帧都到达订阅者，由订阅侧 after 游标幂等吸收）。
//
// 本文件由 eventbus/tail_test.go 按关注点拆出（#443：原文件 379/400 行，逼近
// 结构门禁 max_file_lines）。夹具与共享助手在 tail_fixture_test.go，只此一份。

import (
	"context"
	"testing"
	"time"
)

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
	tailerDone := make(chan struct{})
	go func() {
		defer close(tailerDone)
		NewTailer(store, hub, WithTailInterval(10*time.Millisecond)).Run(ctx)
	}()
	// 收尾等待必须写成 defer 形态（#440）：失败路径上 t.Fatalf 触发的
	// runtime.Goexit 只执行 defer、不执行它之后的普通语句 ⇒ 写在普通语句里的
	// 等待点会被跳过 ⇒ goroutine 实际未被 join。
	//
	// 注册顺序：**先注册 join、后注册 cancel** ⇒ LIFO ⇒ 退出时先 cancel 再 join
	//（反过来会让 join 等一个「不取消就不返回」的 goroutine ⇒ 挂死）。
	defer func() {
		select {
		case <-tailerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("2s 内尾随未退出（失败路径收尾超时）")
		}
	}()
	defer cancel()

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

package eventbus

// event-tail 跨副本补灌面的**取消与泄漏**契约面（issue #166）：
// ctx 取消即退出——循环干净返回、此后不再碰存储（无泄漏 goroutine：
// Run 同步返回，ticker 随 defer 停止）。
//
// 本文件由 eventbus/tail_test.go 按关注点拆出（#443：原文件 379/400 行，逼近
// 结构门禁 max_file_lines）。夹具与共享助手在 tail_fixture_test.go，只此一份。

import (
	"context"
	"testing"
	"time"
)

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
	// Run 返回后：读数必须**不再增长**。
	//
	// # 尺子不是墙钟，是「读数不再增长」这个可观测事实
	//
	// 原判据是「睡 50ms 再比读数」：墙钟窗口当尺子的两个毛病是——窗口落空
	// （窗口内恰好没读）照样绿，且窗口长短与被测机制无关。这里改成「等读数
	// 稳定」：轮询读数，读数**一旦增长就是泄漏**（不等窗口走完就红）；
	// 连续多次不变 ⇒ 判定「读存储已停摆」。
	// 轮询间隔（1ms）不是尺子，尺子是「读数不再增长」。
	//
	// # 为什么采样跨度要跨过尾随节拍（10ms）
	//
	// 采样跨度必须 **> 2×节拍**，否则「恰好没落在采样点」会被误判成停摆：
	// 节拍 10ms，而泄漏出来的循环**第 1 拍是初始化拍**（走 MaxID，不读存量），
	// 第 2 拍才第一次读存量 ⇒ 跨度只压到 1 个节拍时，「读数没变」只是没赶上。
	// 取 30 次 × 1ms = 30ms（> 2×10ms）：任何 30ms 窗口里至少落进 2 次读，
	// 「读数没变」只可能是真没变，不是没赶上。
	//
	// # 这条断言在当前实现下**恒真**——它的价值是拦住将来某个实现
	//
	// Tailer.Run 是**同步循环、无内部 goroutine**（tail.go：for { select {
	// ctx.Done → return } }），Run 一返回，循环就结束了 ⇒ Run 返回后不可能
	// 再有读存储的动作。故原来的 50ms 窗口在当前实现下零信息量：它测的不是
	// 「Run 返回后没再读存储」，而是「时间过去了」。
	//
	// 保留它的意义在**将来**：若有人给 Run 加停机扫尾/异步收尾（Run 先返回、
	// 另起 goroutine 继续收尾），读数会继续增长，这条断言当场转红
	// （反向验证实测：把 Run 改成「循环挪进后台 goroutine」⇒ 本用例转红）。
	//
	// Run 返回**之前**的可观测证据在上面那一段（等 maxIDs() != 0 ⇒ 循环真转过）。
	const settlePolls = 30
	before := len(store.cursors())
	stable := 0
	settleDeadline := time.Now().Add(2 * time.Second)
	for {
		after := len(store.cursors())
		if after != before {
			t.Fatalf("Run 返回后仍在读存储（泄漏）: %d → %d", before, after)
		}
		stable++
		if stable >= settlePolls {
			break
		}
		if time.Now().After(settleDeadline) {
			t.Fatalf("Run 返回后 2s 内读数未增长却未达稳定判据（稳定 %d/%d 次）——判据自身失效",
				stable, settlePolls)
		}
		time.Sleep(time.Millisecond)
	}
}

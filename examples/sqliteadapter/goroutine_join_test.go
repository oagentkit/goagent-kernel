// goroutine_join_test.go —— 「收尾助手必须等 goroutine 真退出」的语义侧回归。
//
// # 这条用例钉住什么
//
// issue #428 的纪律是「用例内起的 goroutine 必须在用例返回前被等待」。规则侧
// （archcheck）判的是**形态**（有没有等待点），判不出**语义**（那个等待点是不是
// 真的等到了退出）。后者只能靠真实并发语义来钉：收尾助手 stopTailer 若只
// cancel 不等，形态照样合规 ⇒ 规则绿着，而 goroutine 仍在用例返回后跑。
//
// # 为什么用「退出标志」而不是 sleep
//
// sleep 是**概率**：睡够多久都可能不够。标志法是**确定**的：goroutine 自己在
// 退出前把标志置上，而置位严格发生在 close(done) **之前** ⇒ stopTailer 返回
// （它等的是 done）之后标志必然为真。断言失败即说明助手没等。
package sqliteadapter

import (
	"context"
	"sync/atomic"
	"testing"
)

// TestStopTailerWaitsForGoroutineExit 收尾助手必须等 goroutine 真退出。
func TestStopTailerWaitsForGoroutineExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var exited atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		// 退出标志必须**在** close(done) 之前置位：断言方等的是 done，
		// 于是 done 关闭 ⇒ 标志已置位（顺序反了断言就没有意义）。
		exited.Store(true)
	}()

	stopTailer(t, cancel, done, "合成收尾")

	if !exited.Load() {
		t.Fatal("stopTailer 返回时 goroutine 尚未退出：" +
			"收尾助手只取消了上下文、没有等它退出")
	}
}

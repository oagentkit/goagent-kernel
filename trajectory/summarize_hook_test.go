package trajectory

// 宿主回调（Options.Summarize）的**锁外面**单独成文件（issue #302）。
//
// 判据三件：回查投影器不得死锁、慢钩子不得阻塞另一 goroutine 的 Apply、
// 钩子 panic 不得杀死投影路径。前面两条用「goroutine + 超时」判——死锁
// 不会自己失败，只会把整个测试进程挂住；超时把「挂住」变成可机判的红。

import (
	"sync"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestSummarizeHookMayCallProjector 钩子里回查投影器（Len / Rows）必须正常
// 返回。旧实现把钩子放在 Apply 的锁内执行 ⇒ 同一把不可重入锁二次加锁 ⇒
// 事件投递方 goroutine 永久挂死。
func TestSummarizeHookMayCallProjector(t *testing.T) {
	var p *Projector
	p = NewProjector(Options{Summarize: func(r Row) string {
		// 宿主的常见诉求：按当前行数/既有行生成展示文案。
		if n := p.Len(); n == 0 {
			t.Errorf("钩子执行时行已应可查，Len()=%d", n)
		}
		_ = p.Rows()
		return "hooked"
	}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Summarize 内回查投影器永久阻塞：宿主回调必须在投影器锁外执行")
	}
	rows := p.Rows()
	if len(rows) != 1 || rows[0].Summary != "hooked" {
		t.Fatalf("钩子结果应回填到行摘要：%+v", rows)
	}
}

// TestSlowSummarizeDoesNotBlockOtherApply 慢钩子（I/O、拉展示文案）不得阻塞
// 另一 goroutine 的 Apply：钩子执行期间投影器锁必须是释放的。
func TestSlowSummarizeDoesNotBlockOtherApply(t *testing.T) {
	var once sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	var p *Projector
	p = NewProjector(Options{Summarize: func(Row) string {
		once.Do(func() {
			close(entered)
			<-release
		})
		return ""
	}})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("钩子未被调用（或调用路径不可观测）")
	}

	// 钩子仍阻塞着；此时另一生产者的 Apply 必须能完成。
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		p.Apply(toolEv(eventbus.EventToolResult, 200, 1, "c1", "alpha_fn"))
	}()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("慢钩子阻塞了另一 goroutine 的 Apply：钩子仍在投影器锁内执行")
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("释放慢钩子后首个 Apply 仍未返回")
	}

	row := findTool(t, p.Rows(), "c1")
	if row.Status != StatusClosed || row.Pairing != PairingPaired {
		t.Fatalf("慢钩子期间的结果事件应已生效：%+v", row)
	}
}

// TestSummarizeHookPanicIsContained 宿主钩子 panic 的归属是宿主，不得穿过
// 投影路径：当前行退回默认摘要，投影继续可用。
func TestSummarizeHookPanicIsContained(t *testing.T) {
	p := NewProjector(Options{Summarize: func(r Row) string {
		if r.Kind == KindToolCall {
			panic("host hook boom")
		}
		return ""
	}})

	// 旧实现会把这个 panic 直接抛给事件投递方。
	p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("钩子 panic 不得丢掉行：%+v", rows)
	}
	if rows[0].Summary != "tool_call:alpha_fn" {
		t.Fatalf("钩子 panic 后应退回默认摘要，实得 %q", rows[0].Summary)
	}

	// 投影路径继续可用。
	p.Apply(toolEv(eventbus.EventToolResult, 200, 1, "c1", "alpha_fn"))
	row := findTool(t, p.Rows(), "c1")
	if row.Status != StatusClosed || row.Pairing != PairingPaired {
		t.Fatalf("钩子 panic 后投影应继续工作：%+v", row)
	}
}

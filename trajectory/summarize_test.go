package trajectory

// 宿主 Summarize 钩子的**执行面**判据（issue #302）。
//
// 钩子是宿主注入的代码，必须在投影器内部锁**释放之后**执行：锁内执行意味着
// 钩子里回查投影器（Len/Rows/…）即永久死锁（实证栈：Apply → apply → append →
// 宿主闭包 → Len → sync.Mutex.Lock），慢钩子还会阻塞所有并发生产者的投递。

import (
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestSummarizeHookMayQueryProjector 钩子内回查投影器必须正常返回——
// 取行数、取会话上下文是宿主钩子的常见诉求，不该是「一查即挂死」的陷阱。
func TestSummarizeHookMayQueryProjector(t *testing.T) {
	var p *Projector
	p = NewProjector(Options{Summarize: func(r Row) string {
		if n := p.Len(); n <= 0 {
			t.Errorf("Len() 在钩子内应可用且 >0，实得 %d", n)
		}
		if rows := p.Rows(); len(rows) == 0 {
			t.Error("Rows() 在钩子内应可用")
		}
		if len(p.Issues()) != 0 {
			t.Error("Issues() 在钩子内应可用")
		}
		return ""
	}})
	p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
	p.Apply(ev(eventbus.EventTurnStarted, 50, nil))
	if p.Len() != 2 {
		t.Fatalf("钩子回查不该影响投影结果，实得 %d 行", p.Len())
	}
}

// TestSlowSummarizeDoesNotBlockOtherProducers 慢钩子不得阻塞另一 goroutine 的
// Apply。判据是**确定性**的：生产者 A 的钩子阻塞在 channel 上直到 B 的投递
// 完成为止——若钩子仍在投影器锁内执行，B 永远拿不到锁，这条会挂死（2 秒超时
// 兜底，挂死时先放行钩子再报错，不拖死整个测试进程）。
func TestSlowSummarizeDoesNotBlockOtherProducers(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	p := NewProjector(Options{Summarize: func(r Row) string {
		if r.CallID == "c1" {
			close(entered)
			<-release
		}
		return ""
	}})

	aDone := make(chan struct{})
	go func() {
		p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
		close(aDone)
	}()
	<-entered // A 的钩子已进入（旧实现：此刻投影器锁仍被 A 持有）

	bDone := make(chan struct{})
	go func() {
		p.Apply(toolEv(eventbus.EventToolCall, 150, 1, "c2", "beta_fn"))
		close(bDone)
	}()
	select {
	case <-bDone:
		// B 的投递在 A 的慢钩子阻塞期间完成 —— 钩子确实在锁外执行。
	case <-time.After(2 * time.Second):
		close(release)
		<-aDone
		t.Fatal("另一 goroutine 的 Apply 被慢钩子阻塞：钩子在投影器锁内执行")
	}
	close(release)
	<-aDone

	rows := p.Rows()
	if len(rows) != 2 || rows[0].Summary != "" && rows[0].Summary != "tool_call:alpha_fn" {
		t.Fatalf("两个生产者的行都应完整投影: %+v", rows)
	}
}

// TestSummarizePanicBelongsToHost 钩子的 panic 归宿主：它沿 Apply 的调用栈
// 抛回投递方（投递路径与钩子都是宿主自己的代码），本包不吞不救——抛出时
// 投影已完成、内部锁已释放，投影器保持完好可用，不会留下死锁或半途状态。
func TestSummarizePanicBelongsToHost(t *testing.T) {
	p := NewProjector(Options{Summarize: func(r Row) string {
		panic("宿主钩子的 panic")
	}})
	func() {
		defer func() {
			if rv := recover(); rv != "宿主钩子的 panic" {
				t.Errorf("钩子的 panic 应沿 Apply 抛回宿主（宿主自行决定兜底），实得 %v", rv)
			}
		}()
		p.Apply(ev(eventbus.EventTurnStarted, 0, nil))
	}()
	// panic 发生在摘要阶段：行已投影、锁已释放。不新建行的后续投递不触发钩子，
	// 照常推进——投影器完好，不是「一次 panic 全盘报废」。
	if p.Len() != 1 {
		t.Fatalf("panic 后投影器应保持完好（行已投影），实得 %d 行", p.Len())
	}
	p.Apply(ev(eventbus.EventTurnEnded, 100, nil))
	rows := p.Rows()
	if len(rows) != 1 || rows[0].Status != StatusClosed {
		t.Fatalf("panic 后投影照常推进: %+v", rows)
	}
}

// TestSummarizeStillOverridesAfterUnlock 钩子的覆盖语义不变（回归钉）：
// 摘要重算挪出锁之后，宿主注入的文案照常生效。
func TestSummarizeStillOverridesAfterUnlock(t *testing.T) {
	p := NewProjector(Options{Summarize: func(r Row) string {
		if r.Kind == KindToolCall {
			return "call:" + r.Tool
		}
		return ""
	}})
	p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
	rows := p.Rows()
	if rows[0].Summary != "call:alpha_fn" {
		t.Fatalf("宿主注入的摘要应生效，得到 %q", rows[0].Summary)
	}
}

// TestBatchSummarizeSeesFinalForm 批内追加又就地归并的行，钩子只在批末见到
// **最终形态**（类目已是 tool_call）——钩子不会看到行的中间状态。
func TestBatchSummarizeSeesFinalForm(t *testing.T) {
	var kinds []Kind
	p := NewProjector(Options{Summarize: func(r Row) string {
		kinds = append(kinds, r.Kind)
		return ""
	}})
	p.ApplyAll([]eventbus.Event{
		{Type: eventbus.EventToolResult, OccurredAt: at(50), Payload: map[string]any{"call_id": "c1"}},
		toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"),
	})
	row := findTool(t, p.Rows(), "c1")
	if len(kinds) != 1 || kinds[0] != KindToolCall {
		t.Fatalf("批内归并的行应只算一次摘要且见到最终类目，实得 %v", kinds)
	}
	if row.Summary != "tool_call:alpha_fn" {
		t.Fatalf("默认摘要应与最终类目同源，得到 %q", row.Summary)
	}
}

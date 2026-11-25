// publish_test.go —— 写库成功但实时发布失败（矩阵行二，Standalone/Cluster）。
package faultmatrix

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/eventbus"
)

// journalStore 事件落库替身：Insert 铸造单调游标，TailStore 读侧同源。
// 与 eventbus.MemoryStore 的区别只有「可同时当 TailStore 用」，语义按契约写：
// 游标单调、ListAfter 升序且无空洞（这里本来就无并发提交窗口）。
type journalStore struct {
	mu   sync.Mutex
	seq  uint64
	rows []eventbus.Event
}

func (j *journalStore) Insert(_ context.Context, ev eventbus.Event) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	ev.ID = j.seq // 游标由存储铸造（调用方传入的 ID 不采信）
	j.rows = append(j.rows, ev)
	return ev.ID, nil
}

func (j *journalStore) MaxID(context.Context) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.seq, nil
}

func (j *journalStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]eventbus.Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []eventbus.Event
	for _, ev := range j.rows {
		if ev.ID <= cursor {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, ev)
	}
	return out, nil
}

// dropRelay 实时跨副本中继的「发布失败」替身：事件被丢弃，失败由实现自担
// （eventbus.Relay 契约没有返回值——可靠面是存储游标，实时面是尽力而为）。
type dropRelay struct {
	attempts atomic.Int64
}

func (r *dropRelay) Publish(eventbus.Event) { r.attempts.Add(1) }

// runDurableWritePublishFailure 写库成功（Insert 返回权威游标）后，实时
// 跨副本发布失败（Relay 丢弃）。判据：
//
//   - 落库是权威事实：存储里恰好一行、游标由存储铸造；
//   - 实时失败不回滚、不重试成双写；
//   - 另一副本经 event-tail 从存储回放补达同一条事件（ID 与落库行一致）。
//
// 旧形态「先广播后落库」在这里必红：广播失败时存储里没有行，回放读不到。
func runDurableWritePublishFailure(t *testing.T) {
	ctx := context.Background()
	journal := &journalStore{}
	bus := eventbus.New()
	publisher := eventbus.NewDurablePublisher(bus, journal)

	sub, cancelSub := bus.Subscribe(ctx)
	defer cancelSub()

	ev, err := publisher.Publish(ctx, eventbus.Event{
		SessionID: "session-fm-1",
		Type:      eventbus.EventStepStarted,
		Payload:   map[string]any{"step": "synthetic"},
	})
	if err != nil {
		t.Fatalf("写库成功路径不得报错: %v", err)
	}
	if ev.ID == 0 {
		t.Fatal("落库后必须拿到存储铸造的权威游标")
	}
	select {
	case got := <-sub:
		if got.ID != ev.ID {
			t.Fatalf("实时帧必须携带存储游标 %d（实得 %d）", ev.ID, got.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("落库成功后本副本实时广播必须到达")
	}

	// 实时外发失败：Relay 丢弃。事件已经落库，失败不得回滚/双写。
	relay := &dropRelay{}
	live := eventbus.NewHub()
	live.SetRelay(relay)
	live.Publish(ev)
	if relay.attempts.Load() != 1 {
		t.Fatalf("实时发布必须被尝试恰好一次（实得 %d）", relay.attempts.Load())
	}

	// 另一副本从权威存储回放：实时面丢的帧由 event-tail 补达。
	replayHub := eventbus.NewHub()
	replayCh, cancelReplay := replayHub.Subscribe("session-fm-1")
	defer cancelReplay()
	tailer := eventbus.NewTailer(journal, replayHub,
		eventbus.WithTailInterval(time.Millisecond),
		eventbus.WithTailStartCursor(0)) // 从 0 起随 = 回放存量（对账场景）
	tailCtx, stopTail := context.WithCancel(context.Background())
	defer stopTail()
	go tailer.Run(tailCtx)

	select {
	case got := <-replayCh:
		if got.ID != ev.ID || got.Type != eventbus.EventStepStarted {
			t.Fatalf("回放帧必须与落库行一致（want id=%d type=%s，实得 id=%d type=%s）",
				ev.ID, eventbus.EventStepStarted, got.ID, got.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("实时发布失败后必须能从权威存储回放补达——写库成功的事实不因实时面故障丢失")
	}

	max, err := journal.MaxID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if max != 1 {
		t.Fatalf("发布失败不得触发重试/双写（存储行数实得 %d，want 1）", max)
	}
}

// eventreorder_test.go —— 矩阵行十一：事件乱序提交（Standalone + Cluster）。
//
// 两个发布者先拿号后提交：ID=2 先提交（可见），ID=1 的提交在途。尾随器
// 的初始化游标取**已提交水位线**（不是裸 max(id）），因此不得越过 2 去
// 跳过 1；ID=1 提交后，1、2 必须按序补达，不丢帧也不乱序。
package faultmatrix

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// reorderStore Outbox 形态的水位线替身：
//   - allocate 只拿号，不推进可见水位；
//   - commit 按号提交，只有从 1 起连续提交时才推进 watermark；
//   - MaxID = 已提交水位线（不是已分配的最大号）；
//   - ListAfter 只返回水位线以内的连续段。
type reorderStore struct {
	mu        sync.Mutex
	next      uint64
	committed map[uint64]eventbus.Event
	watermark uint64
	reads     int // ListAfter 读拍数（缺席断言的尺子，#532）
}

// readAttempts 返回 ListAfter 被调用过的次数（读拍数）——线程安全。
func (s *reorderStore) readAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func newReorderStore() *reorderStore {
	return &reorderStore{committed: map[uint64]eventbus.Event{}}
}

func (s *reorderStore) allocate() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return s.next
}

func (s *reorderStore) commit(id uint64, ev eventbus.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev.ID = id
	s.committed[id] = ev
	for {
		next := s.watermark + 1
		if _, ok := s.committed[next]; !ok {
			return
		}
		s.watermark = next
	}
}

func (s *reorderStore) MaxID(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watermark, nil
}

func (s *reorderStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]eventbus.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	var out []eventbus.Event
	for id := cursor + 1; id <= s.watermark; id++ {
		ev, ok := s.committed[id]
		if !ok {
			break // 契约要求无空洞；这里防御性截断
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// reorderAbsenceReadBeats 缺席断言要求的读拍数：本夹具 TailInterval=1ms，
// 旧墙钟窗口 50ms ≈ 50 拍 ⇒ 取 50 保持覆盖力（#532 清偿口径）。
const reorderAbsenceReadBeats = 50

// waitReorderReadBeats 等尾随读拍跑满 n 拍（1 拍 = 一次 ListAfter 读）。
// 尺子必须是可观测事实而不是墙钟窗口（#443/#532）：拍数没跑满就判缺席
// 等于什么都没验 ⇒ 直接红；墙钟 2s 只作兜底上限，防挂死、不参与结论。
func waitReorderReadBeats(t *testing.T, store *reorderStore, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for store.readAttempts() < n+1 {
		if time.Now().After(deadline) {
			t.Fatalf("2s 内尾随读拍未跑满 %d 拍（实得 %d 拍）——机制没跑够拍，"+
				"缺席断言没有证据价值（此刻判绿等于什么都没验）", n, store.readAttempts())
		}
		time.Sleep(time.Millisecond)
	}
}

func runEventOutOfOrderCommit(t *testing.T) {
	ctx := context.Background()
	store := newReorderStore()
	hub := eventbus.NewHub()
	ch, cancel := hub.Subscribe("session-reorder")
	defer cancel()

	// 先拿号：发布者 A=1（慢），发布者 B=2（快）。
	idA := store.allocate()
	idB := store.allocate()
	if idA != 1 || idB != 2 {
		t.Fatalf("游标分配必须单调（A=%d B=%d）", idA, idB)
	}
	store.commit(idB, eventbus.Event{
		SessionID: "session-reorder", Type: eventbus.EventStepFinished,
		Payload: map[string]any{"seq": "B"},
	})

	tailer := eventbus.NewTailer(store, hub, eventbus.WithTailInterval(time.Millisecond))
	tailCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go tailer.Run(tailCtx)

	// ID=2 已提交，但水位线仍是 0：尾随器不得把 2 当成「已有 2 帧」跳过 1。
	// 尺子=读拍计数（与 eventbus waitTailReadBeats 同法，#443/#532）：
	// 本夹具节拍 1ms，旧 50ms 窗口≈50 拍 ⇒ 取 50 拍不降覆盖力；墙钟 2s
	// 只作兜底上限，拍没跑满直接红（此刻判缺席等于什么都没验）。
	waitReorderReadBeats(t, store, reorderAbsenceReadBeats)
	select {
	case ev := <-ch:
		t.Fatalf("水位线未推进时不得投递乱序帧（实得 id=%d）", ev.ID)
	default:
	}

	store.commit(idA, eventbus.Event{
		SessionID: "session-reorder", Type: eventbus.EventStepStarted,
		Payload: map[string]any{"seq": "A"},
	})

	var got []uint64
	timeout := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-ch:
			got = append(got, ev.ID)
		case <-timeout:
			t.Fatalf("乱序提交后必须补达两帧（已收 %v）——ID=1 不得被跳过", got)
		}
	}
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("补达顺序必须是 1,2（实得 %v）——乱序提交不得变成乱序投递或丢帧", got)
	}
	watermark, err := store.MaxID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if watermark != 2 {
		t.Fatalf("两帧都提交后水位线必须是 2（实得 %d）", watermark)
	}
}

// finalize_test.go —— issue #272：交接收尾（终态写入 + 终态事件）是否同事务，
// 先复现再定性；坐实后由 Finalize Intent + Outbox 闭合。
//
// 复现的判定形态：
//   - 旧 Store 路径：ConvergeCAS 已把会话写成终态，Pub 是 fire-and-forget，
//     「发事件失败」后没有重试/补做入口 ⇒ 状态终态、事件永久缺失。本文件
//     用丢事件的 Pub 把这个分裂钉成可跑事实（TestSettlementEventSplitReproduced）。
//   - FinalizeStore 路径：终态写入与 Outbox 行同一事务；投递失败只影响
//     「已投递」标记，行仍在，FlushFinalizeOutbox 幂等补做（重放不重复、
//     进程重启不丢）。
package reconcile

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// legacySplitStore 只实现 Store（旧路径）的会话存储替身：ConvergeCAS 把
// 会话移出 running，但没有任何事件表/Outbox——终态事件只靠 Pub 实时发送。
type legacySplitStore struct {
	mu       sync.Mutex
	sessions []Session
	terminal map[string]bool
	casCalls int
}

func newLegacySplitStore() *legacySplitStore {
	return &legacySplitStore{
		sessions: []Session{{ID: "s1", CaseID: "c1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		terminal: map[string]bool{},
	}
}

func (s *legacySplitStore) ListRunning(context.Context, string) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Session
	for _, session := range s.sessions {
		if !s.terminal[session.ID] {
			out = append(out, session)
		}
	}
	return out, nil
}

func (s *legacySplitStore) LastActivity(context.Context, string) (time.Time, error) {
	return rbase.Add(-(DefaultStall + time.Second)), nil
}

func (s *legacySplitStore) ListBeats(context.Context) (map[string]time.Time, error) {
	return map[string]time.Time{}, nil
}

func (s *legacySplitStore) ConvergeCAS(_ context.Context, id, _, _ string, _ Cause) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal[id] {
		return false, nil
	}
	s.terminal[id] = true
	s.casCalls++
	return true, nil
}

func (s *legacySplitStore) CloseBracket(context.Context, string, string) error { return nil }

func (s *legacySplitStore) isTerminal(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal[id]
}

// dropPub 模拟「实时发布失败」：调用被尝试，但事件没有到达任何订阅者。
// 旧 Pub 签名没有返回值，失败只能表现为「事件消失」。
type dropPub struct {
	mu       sync.Mutex
	attempts int
	events   []string
	drop     bool // true = 调用被尝试但事件被丢弃（模拟实时发布失败）
}

func (p *dropPub) fn(sessionID, _ string, _ *uint64, typ, _ string, _ map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.drop {
		return
	}
	p.events = append(p.events, sessionID+":"+typ)
}

func (p *dropPub) snapshot() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts, append([]string(nil), p.events...)
}

// finalizeStore 实现 FinalizeStore 的替身：Finalize 在同一临界区内完成
// 「条件迁移 + Outbox 行写入」；投递标记独立、可失败、可重放。
type finalizeStore struct {
	mu sync.Mutex

	sessions  []Session
	terminal  map[string]bool
	outbox    []FinalizeEvent
	delivered map[string]bool

	finalizeErr error
	pendingErr  error
	markErr     error

	finalizeCalls int
	bracketCalls  int
}

func newFinalizeStore() *finalizeStore {
	return &finalizeStore{
		sessions:  []Session{{ID: "s1", CaseID: "c1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		terminal:  map[string]bool{},
		delivered: map[string]bool{},
	}
}

func (s *finalizeStore) ListRunning(context.Context, string) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Session
	for _, session := range s.sessions {
		if !s.terminal[session.ID] {
			out = append(out, session)
		}
	}
	return out, nil
}

func (s *finalizeStore) LastActivity(context.Context, string) (time.Time, error) {
	return rbase.Add(-(DefaultStall + time.Second)), nil
}

func (s *finalizeStore) ListBeats(context.Context) (map[string]time.Time, error) {
	return map[string]time.Time{}, nil
}

func (s *finalizeStore) ConvergeCAS(_ context.Context, id, _, _ string, _ Cause) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal[id] {
		return false, nil
	}
	s.terminal[id] = true
	return true, nil
}

func (s *finalizeStore) CloseBracket(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bracketCalls++
	return nil
}

func (s *finalizeStore) Finalize(_ context.Context, id, from, to string, _ Cause, ev FinalizeEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCalls++
	if s.finalizeErr != nil {
		return false, s.finalizeErr
	}
	if from != "running" || to != "failed" {
		return false, errors.New("finalizeStore: 意外的迁移 " + from + "→" + to)
	}
	if s.terminal[id] {
		return false, nil // 他方已处理：不得重复写 Outbox 行
	}
	s.terminal[id] = true
	s.outbox = append(s.outbox, ev)
	return true, nil
}

func (s *finalizeStore) PendingFinalEvents(_ context.Context, limit int) ([]FinalizeEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingErr != nil {
		return nil, s.pendingErr
	}
	var out []FinalizeEvent
	for _, ev := range s.outbox {
		if s.delivered[ev.ID] {
			continue
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *finalizeStore) MarkFinalEventDelivered(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markErr != nil {
		return s.markErr
	}
	s.delivered[id] = true
	return nil
}

func (s *finalizeStore) isTerminal(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal[id]
}

func (s *finalizeStore) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.outbox {
		if !s.delivered[ev.ID] {
			n++
		}
	}
	return n
}

var errDeliveryBoom = errors.New("reconcile-test: 注入终态事件投递失败")

// TestSettlementEventSplitReproduced 复现（旧 Store 路径）：
// 结算成功（会话已经终态）后发布失败，状态与事件分裂；第二次扫描不再
// 看到 running 会话，事件没有任何补做入口。
//
// 结论：**坐实**。这是 FinalizeStore/Outbox 存在的理由；旧路径保留时
// FinalizeGuarantee.Atomic 必须如实报 false。
func TestSettlementEventSplitReproduced(t *testing.T) {
	store := newLegacySplitStore()
	pub := &dropPub{drop: true}
	r := NewWithClock(store, pub.fn, Config{InstanceID: "replica-a"}, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatalf("旧路径收敛本身应成功（事件失败不阻塞状态迁移）: %v", err)
	}
	if n != 1 {
		t.Fatalf("应收敛 1 个会话（实得 %d）", n)
	}
	if !store.isTerminal("s1") {
		t.Fatal("结算必须已成功（会话进入终态）")
	}
	attempts, events := pub.snapshot()
	if attempts != 1 {
		t.Fatalf("旧路径必须尝试发布一次（实得 %d）", attempts)
	}
	if len(events) != 0 {
		t.Fatalf("注入的发布失败应让事件消失（实得 %v）", events)
	}

	// 再扫一轮：会话已不在 running，旧路径不会再发事件——分裂永久化。
	n, err = r.RecoverInterrupted(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("第二轮不应再收敛（n=%d err=%v）", n, err)
	}
	attempts, _ = pub.snapshot()
	if attempts != 1 {
		t.Fatalf("旧路径没有补做入口，第二轮不得重发（实得尝试 %d 次）", attempts)
	}
	if g := r.FinalizeGuarantee(); g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("旧 Store 路径必须如实报告非原子/未确认投递: %+v", g)
	}
}

// TestFinalizeOutboxNoLossNoDuplicate 坐实后的修法判据：
//   - Finalize 与终态事件同一事务：投递失败时事件在 Outbox 里（不丢）；
//   - 重启后的 FlushFinalizeOutbox 幂等补做：重放成功一次；
//   - 已投递的行再 Flush 不重复发送。

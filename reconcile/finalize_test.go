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
func TestFinalizeOutboxNoLossNoDuplicate(t *testing.T) {
	store := newFinalizeStore()
	pub := &dropPub{}
	fail := true
	var dispatched []string
	dispatch := func(_ context.Context, ev FinalizeEvent) error {
		if fail {
			return errDeliveryBoom
		}
		dispatched = append(dispatched, ev.ID)
		return nil
	}
	cfg := Config{InstanceID: "replica-a", FinalizeDispatch: dispatch}
	r := NewWithClock(store, pub.fn, cfg, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if err == nil {
		t.Fatal("投递失败必须带出留痕（不得静默）")
	}
	if !errors.Is(err, errDeliveryBoom) {
		t.Fatalf("留痕应包含投递失败原因（实得 %v）", err)
	}
	if n != 1 || !store.isTerminal("s1") {
		t.Fatalf("终态写入必须成立（n=%d terminal=%v）", n, store.isTerminal("s1"))
	}
	if store.pendingCount() != 1 {
		t.Fatalf("投递失败后 Outbox 必须保留 1 行（实得 %d）", store.pendingCount())
	}
	if len(dispatched) != 0 {
		t.Fatalf("失败的那次不应计入已投递（实得 %v）", dispatched)
	}

	// 进程重启：新的 Reconciler 从同一存储补做。
	fail = false
	r2 := NewWithClock(store, pub.fn, cfg, func() time.Time { return rbase })
	delivered, ferr := r2.FlushFinalizeOutbox(context.Background())
	if ferr != nil {
		t.Fatalf("补做不应失败: %v", ferr)
	}
	if delivered != 1 || len(dispatched) != 1 {
		t.Fatalf("Outbox 必须恰好补投一次（delivered=%d dispatched=%v）", delivered, dispatched)
	}
	if dispatched[0] != "finalize:s1:failed" {
		t.Fatalf("终态事件 ID 必须稳定（实得 %q）", dispatched[0])
	}
	if store.pendingCount() != 0 {
		t.Fatalf("补做成功后不得残留待投递行（实得 %d）", store.pendingCount())
	}

	// 重放：已投递的行不得重复发送。
	delivered, ferr = r2.FlushFinalizeOutbox(context.Background())
	if ferr != nil || delivered != 0 {
		t.Fatalf("重放不得重复投递（delivered=%d err=%v）", delivered, ferr)
	}
	if len(dispatched) != 1 {
		t.Fatalf("重放后投递次数必须仍是 1（实得 %d）", len(dispatched))
	}
	if g := r2.FinalizeGuarantee(); !g.Atomic || !g.ConfirmedDelivery {
		t.Fatalf("FinalizeStore + 可确认投递必须报告原子/已确认: %+v", g)
	}
}

// TestFinalizeAtomicFailureNoSplit 反向判据：Finalize 事务失败 ⇒ 状态不变、
// Outbox 无行（不允许「事件先落库、状态没写成」的反向分裂）；重试后同事务
// 一起成立。
func TestFinalizeAtomicFailureNoSplit(t *testing.T) {
	store := newFinalizeStore()
	store.finalizeErr = errDeliveryBoom
	pub := &dropPub{}
	r := NewWithClock(store, pub.fn, Config{InstanceID: "replica-a"}, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if n != 0 || err == nil {
		t.Fatalf("Finalize 失败必须整轮失败（n=%d err=%v）", n, err)
	}
	if store.isTerminal("s1") {
		t.Fatal("Finalize 失败不得留下终态（状态与事件必须同生共死）")
	}
	if store.pendingCount() != 0 {
		t.Fatal("Finalize 失败不得留下 Outbox 行")
	}

	store.mu.Lock()
	store.finalizeErr = nil
	store.mu.Unlock()
	// 无 dispatcher：回退旧 Pub（Outbox 行仍先落库，再标记已投递）。
	n, err = r.RecoverInterrupted(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("重试后应成功（n=%d err=%v）", n, err)
	}
	if !store.isTerminal("s1") || store.pendingCount() != 0 {
		t.Fatalf("同事务成功后状态/Outbox 必须一致（terminal=%v pending=%d）",
			store.isTerminal("s1"), store.pendingCount())
	}
	attempts, events := pub.snapshot()
	if attempts != 1 || len(events) != 1 {
		t.Fatalf("无 dispatcher 时回退 Pub 必须投递一次（attempts=%d events=%v）", attempts, events)
	}
	if g := r.FinalizeGuarantee(); !g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("无 dispatcher 的 FinalizeStore 必须报原子但投递未确认: %+v", g)
	}
}

// TestFinalizeOutboxMarkFailureReplays 标记已投递失败：行保留，下一轮补做。
// 投递是至少一次（Event.ID 稳定，订阅方按 ID 去重），但绝不丢。
func TestFinalizeOutboxMarkFailureReplays(t *testing.T) {
	store := newFinalizeStore()
	store.markErr = errDeliveryBoom
	pub := &dropPub{}
	var ids []string
	dispatch := func(_ context.Context, ev FinalizeEvent) error {
		ids = append(ids, ev.ID)
		return nil
	}
	r := NewWithClock(store, pub.fn, Config{InstanceID: "replica-a", FinalizeDispatch: dispatch}, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if n != 1 || err == nil {
		t.Fatalf("标记失败必须带出留痕（n=%d err=%v）", n, err)
	}
	if store.pendingCount() != 1 {
		t.Fatalf("标记失败时 Outbox 行不得被吞（实得 %d）", store.pendingCount())
	}
	if len(ids) != 1 {
		t.Fatalf("投递本身已发生一次（实得 %v）", ids)
	}

	store.mu.Lock()
	store.markErr = nil
	store.mu.Unlock()
	delivered, ferr := r.FlushFinalizeOutbox(context.Background())
	if ferr != nil || delivered != 1 {
		t.Fatalf("补做必须成功（delivered=%d err=%v）", delivered, ferr)
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("重放使用同一稳定 Event.ID（实得 %v）——订阅方据此去重", ids)
	}
	if store.pendingCount() != 0 {
		t.Fatal("补做成功后不得残留 Outbox 行")
	}
}

// TestFinalizeGuaranteeReportsModes 可查事实的三种组合。
func TestFinalizeGuaranteeReportsModes(t *testing.T) {
	legacy := NewWithClock(newLegacySplitStore(), (&dropPub{}).fn,
		Config{InstanceID: "replica-a"}, func() time.Time { return rbase })
	if g := legacy.FinalizeGuarantee(); g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("legacy: %+v", g)
	}

	atomic := NewWithClock(newFinalizeStore(), (&dropPub{}).fn,
		Config{InstanceID: "replica-a", FinalizeDispatch: func(context.Context, FinalizeEvent) error { return nil }},
		func() time.Time { return rbase })
	if g := atomic.FinalizeGuarantee(); !g.Atomic || !g.ConfirmedDelivery {
		t.Fatalf("atomic+dispatch: %+v", g)
	}

	unconfirmed := NewWithClock(newFinalizeStore(), (&dropPub{}).fn,
		Config{InstanceID: "replica-a"}, func() time.Time { return rbase })
	if g := unconfirmed.FinalizeGuarantee(); !g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("atomic+legacy-pub: %+v", g)
	}
}

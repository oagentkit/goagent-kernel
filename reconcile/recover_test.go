package reconcile

// 三路扫描与收敛体契约（issue #165）：启动锚点路 / 常规路（静默+租约）/
// 收敛体动作序 / 时钟注入确定性 / 错误语义。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var rbase = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// fakeStore Store 接缝替身：记录调用序与入参，行为可编排。
type fakeStore struct {
	mu sync.Mutex

	sessions  []Session
	ownerHint []string
	listErr   error

	last    map[string]time.Time
	lastErr map[string]error

	beats     map[string]time.Time
	beatsErr  error
	beatsCall int

	casErr   error
	refused  map[string]bool
	lastCast struct {
		from, to string
		cause    Cause
	}

	calls []string
}

func (s *fakeStore) ListRunning(_ context.Context, owner string) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownerHint = append(s.ownerHint, owner)
	s.calls = append(s.calls, "list:"+owner)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]Session(nil), s.sessions...), nil
}

func (s *fakeStore) LastActivity(_ context.Context, id string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "last:"+id)
	if err, ok := s.lastErr[id]; ok {
		return time.Time{}, err
	}
	return s.last[id], nil // 缺项 = 零值（无事件行）
}

func (s *fakeStore) ListBeats(context.Context) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beatsCall++
	s.calls = append(s.calls, "beats")
	if s.beatsErr != nil {
		return nil, s.beatsErr
	}
	return s.beats, nil
}

func (s *fakeStore) ConvergeCAS(_ context.Context, id, from, to string, cause Cause) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fmt.Sprintf("cas:%s:%s->%s:%s", id, from, to, cause.By))
	s.lastCast = struct {
		from, to string
		cause    Cause
	}{from, to, cause}
	if s.casErr != nil {
		return false, s.casErr
	}
	return !s.refused[id], nil
}

func (s *fakeStore) CloseBracket(_ context.Context, id, closedBy string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "bracket:"+id+":"+closedBy)
}

func (s *fakeStore) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// recorder Pub 替身（与 Store 同一条调用序日志，动作序断言要含事件）。
type recorder struct {
	mu     sync.Mutex
	calls  *[]string
	events []string
}

func newRecorder(store *fakeStore) *recorder {
	return &recorder{calls: &store.calls}
}

func (r *recorder) pub(sessionID, _ string, _ *uint64, typ, _ string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := "pub:" + sessionID + ":" + typ + ":by=" + fmt.Sprint(payload["by"])
	r.events = append(r.events, entry)
	*r.calls = append(*r.calls, entry)
}

// newRig 组装 Reconciler + 替身（固定时钟）。
func newRig(t *testing.T, cfg Config, store *fakeStore) (*Reconciler, *recorder) {
	t.Helper()
	rec := newRecorder(store)
	r := NewWithClock(store, rec.pub, cfg, func() time.Time { return rbase })
	return r, rec
}

func mustRecover(t *testing.T, r *Reconciler, boot bool) int {
	t.Helper()
	var (
		n   int
		err error
	)
	if boot {
		n, err = r.RecoverBootOrphans(context.Background())
	} else {
		n, err = r.RecoverInterrupted(context.Background())
	}
	if err != nil {
		t.Fatalf("扫描不应带错: %v", err)
	}
	return n
}

// ── 常规路：事件静默 ────────────────────────────────────────────────────

func TestInterruptedMineReclaimedBySilence(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", CaseID: "c1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-(DefaultStall + time.Second))},
		beats:    map[string]time.Time{}, // 心跳面正常但与本副本判定无关
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n := mustRecover(t, r, false)
	if n != 1 {
		t.Fatalf("本副本静默超阈应收敛, n=%d", n)
	}
	got := store.log()
	want := []string{
		"list:",
		"beats",
		"last:s1",
		"cas:s1:running->failed:recovery",
		"bracket:s1:recovery",
		"pub:s1:session_finished:by=recovery",
	}
	if len(got) != len(want) {
		t.Fatalf("动作序不符: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("动作序第 %d 步: got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestInterruptedMineNotReclaimedUnderThreshold(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-(DefaultStall - time.Second))},
		beats:    map[string]time.Time{},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n := mustRecover(t, r, false)
	if n != 0 {
		t.Fatalf("未超静默阈值不该收, n=%d", n)
	}
}

// ── 常规路：心跳租约 ────────────────────────────────────────────────────

func TestInterruptedFreshLeaseIsExemptFromSilence(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-b", CreatedAt: rbase.Add(-2 * time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-2 * time.Hour)}, // 静默远超阈值
		beats:    map[string]time.Time{"replica-b": rbase.Add(-10 * time.Second)},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n := mustRecover(t, r, false)
	if n != 0 {
		t.Fatalf("他副本心跳新鲜（活编排名下）即使长期静默也免检, n=%d", n)
	}
}

func TestInterruptedExpiredLeaseReclaimedWithoutWaitingSilence(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", CaseID: "c1", Owner: "replica-b", CreatedAt: rbase.Add(-time.Minute)}},
		last:     map[string]time.Time{"s1": rbase.Add(-5 * time.Second)}, // 事件还很新：静默路不会收
		beats: map[string]time.Time{
			"replica-b": rbase.Add(-(DefaultLeaseTTL + GraceWindow + time.Second)),
		},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n := mustRecover(t, r, false)
	if n != 1 {
		t.Fatalf("租约过期应不等静默立即回收, n=%d", n)
	}
	// 归因与理由：dead-lease，阈值是租约+宽限。
	if store.lastCast.cause.By != ByDeadLease {
		t.Fatalf("归因应 recovery:dead-lease, got %q", store.lastCast.cause.By)
	}
	if !strings.Contains(store.lastCast.cause.Reason, (DefaultLeaseTTL + GraceWindow).String()) {
		t.Fatalf("理由应写明租约+宽限阈值, got %q", store.lastCast.cause.Reason)
	}
	// 关键差异：租约死不查最后事件（LastActivity 不在动作序里）。
	for _, c := range store.log() {
		if strings.HasPrefix(c, "last:") {
			t.Fatalf("租约过期路不得再查事件静默: %v", store.log())
		}
	}
}

func TestInterruptedNoBeatRowFallsBackToDeadOwnerSilence(t *testing.T) {
	// 无心跳行（存量副本）：回落事件静默，阈值是更宽的 DeadOwner（30m）。
	old := &fakeStore{
		sessions: []Session{{ID: "old", Owner: "replica-b", CreatedAt: rbase.Add(-2 * time.Hour)}},
		last:     map[string]time.Time{"old": rbase.Add(-(DefaultDeadOwner + time.Second))},
		beats:    map[string]time.Time{}, // 表里没有 replica-b
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, old)
	if n := mustRecover(t, r, false); n != 1 {
		t.Fatalf("无心跳行且回落静默超阈应收敛, n=%d", n)
	}
	if old.lastCast.cause.By != ByDeadOwner {
		t.Fatalf("归因应 recovery:dead-owner, got %q", old.lastCast.cause.By)
	}

	fresh := &fakeStore{
		sessions: []Session{{ID: "near", Owner: "replica-b", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"near": rbase.Add(-(DefaultDeadOwner - time.Minute))},
		beats:    map[string]time.Time{},
	}
	r2, _ := newRig(t, Config{InstanceID: "replica-a"}, fresh)
	if n := mustRecover(t, r2, false); n != 0 {
		t.Fatalf("回落静默未达 30m 不该收（阈值不是 Stall 的 10m）, n=%d", n)
	}
}

func TestInterruptedBeatsReadErrorDegradesButIsReported(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-b", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-5 * time.Second)}, // 静默未超
		beatsErr: fmt.Errorf("心跳面抖动"),
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	var err error
	if n, e := r.RecoverInterrupted(context.Background()); n != 0 {
		t.Fatalf("降级后按静默判定，未超阈不该收, n=%d", n)
	} else {
		err = e
	}
	if err == nil || !strings.Contains(err.Error(), "降级为事件静默判定") {
		t.Fatalf("心跳面读失败必须随返回值带出（不静默）, got %v", err)
	}
	if store.beatsCall != 1 {
		t.Fatalf("ListBeats 应被调用一次, got %d", store.beatsCall)
	}
}

func TestInterruptedNegativeLeaseTTLDisablesBeatsRead(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-b", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-(DefaultDeadOwner + time.Second))},
		beats:    map[string]time.Time{"replica-b": rbase.Add(-time.Second)}, // 若读了就会免检
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a", LeaseTTL: -1}, store)

	n := mustRecover(t, r, false)
	if store.beatsCall != 0 {
		t.Fatalf("LeaseTTL<0（关闭心跳面）不得读 ListBeats, got %d 次", store.beatsCall)
	}
	if n != 1 {
		t.Fatalf("心跳面关闭时他副本回落事件静默并超阈应收敛, n=%d", n)
	}
}

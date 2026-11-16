package eventbus

// issue #264 的可判口子判据：Outbox 形态的机制替身与已提交水位线语义。
//
// tail_cursor_test.go 承载三类形态的端到端互证（违约自增直读 / 读侧截断 /
// 回滚空洞 e2e，与既有违约哨兵用例同文件）；本文件承载 Outbox 机制替身
// 本体与水位线读法（WatermarkTailStore）的可机判口径——两者同包互证，
// 真 PG 上的同判据在 examples/pgstore 跑（pg_event_test.go）。

import (
	"context"
	"sync"
	"testing"
)

// outboxStore 被承诺的生产形态替身：Outbox（issue #264）。
//
// 写侧机制面（与 pgstore.EventStore 在真 PG 上的落库形态同构）：
//   - 分配经水位行串行化——同时至多一个在途事务（真实现里第二个发布者在
//     水位行锁上等待；替身以 panic 钉死此前提）；
//   - 提交与水位线推进原子（同刻可见）；回滚既不推进水位线也不烧号（下一
//     次分配复用同一 ID）⇒ 序列空洞结构性地不存在；
//   - 读侧以已提交水位线为上界：(cursor, 水位线] 内逐号取行——「已铸造
//     未可见」的 ID 卡进返回段结构性地不可能。
type outboxStore struct {
	mu        sync.Mutex
	watermark uint64           // 已提交水位线（≤ 此值的 ID 已全部提交且可见）
	inflight  *outboxTx        // 水位行锁串行化的替身形态：至多一个在途
	rows      map[uint64]Event // 已提交可见行
}

func newOutboxStore() *outboxStore { return &outboxStore{rows: map[uint64]Event{}} }

// outboxTx 在途事务（拿号未提交；生死归调用方——commit / rollback 二选一）。
type outboxTx struct {
	s  *outboxStore
	id uint64
	ev Event
}

// begin 铸造下一个 ID（水位线+1）并在途持有事件行——同构于真实现的两步
// 「UPDATE 水位行 RETURNING 新值 → INSERT 事件行」。并发 begin 即 panic：
// 水位行锁串行化下第二个事务必须等待，替身不模拟等待、直接拒绝。
func (s *outboxStore) begin(ev Event) *outboxTx {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight != nil {
		panic("outboxStore: 水位行锁串行化下不允许并发在途事务（第二个发布者必须等待）")
	}
	s.inflight = &outboxTx{s: s, id: s.watermark + 1, ev: ev}
	return s.inflight
}

// commit 提交在途事务：事件行与水位线推进同刻可见（同事务提交）。
func (t *outboxTx) commit() {
	s := t.s
	s.mu.Lock()
	defer s.mu.Unlock()
	row := t.ev
	row.ID = t.id // 权威行携带存储铸造的 ID（同 INSERT ... RETURNING id）
	s.rows[t.id] = row
	s.watermark = t.id
	s.inflight = nil
}

// rollback 回滚在途事务：行不落、水位线不动、ID 不烧号（下一次分配复用）。
func (t *outboxTx) rollback() {
	s := t.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = nil
}

// 编译期断言：本替身带已提交水位线读法声明（Outbox 形态的实现声明面，#264）。
var _ WatermarkTailStore = (*outboxStore)(nil)

// MaxID 无空洞边界：Outbox 形态下即已提交水位线（同值同源，#264 收紧后的
// MaxID 契约——初始化读法不得越过在途小 ID）。
func (s *outboxStore) MaxID(ctx context.Context) (uint64, error) { return s.CommittedWatermark(ctx) }

// CommittedWatermark 已提交水位线（WatermarkTailStore）：与 ListAfter 共用
// 同一条无空洞边界——初始化读法与逐拍读法不分裂。
func (s *outboxStore) CommittedWatermark(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watermark, nil
}

func (s *outboxStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 水位线读法：返回段被水位线上界封顶——(cursor, watermark] 逐号取行。
	// rows 缺号即机制被破（结构性地不可能），防御性终止而不是跳过。
	var out []Event
	for id := cursor + 1; id <= s.watermark && len(out) < limit; id++ {
		ev, ok := s.rows[id]
		if !ok {
			break
		}
		out = append(out, ev)
	}
	return out, nil
}

// TestOutboxWatermarkAdvancesOnlyWithCommit 水位线语义判据（#264 可判口子，
// 在进程内同构上钉住；真 PG 上的同判据在 pgstore 跑）：
//   - 水位线只在提交时推进：在途铸造不推它；
//   - 回滚既不推水位线也不烧号（下一次分配复用同一 ID）；
//   - MaxID 与水位线同值（初始化不越过在途小 ID）；
//   - 未提交行不可见，提交后按 (cursor, 水位线] 连续返回。
func TestOutboxWatermarkAdvancesOnlyWithCommit(t *testing.T) {
	store := newOutboxStore()
	ctx := context.Background()

	assertWatermark := func(stage string, want uint64) {
		t.Helper()
		w, err := store.CommittedWatermark(ctx)
		if err != nil {
			t.Fatalf("%s: CommittedWatermark 失败: %v", stage, err)
		}
		max, err := store.MaxID(ctx)
		if err != nil {
			t.Fatalf("%s: MaxID 失败: %v", stage, err)
		}
		if w != want || max != want {
			t.Fatalf("%s: 水位线与 MaxID 应同为 %d, got watermark=%d maxID=%d", stage, want, w, max)
		}
	}

	assertWatermark("空表", 0)

	pending := store.begin(Event{SessionID: "s", Type: "pending"}) // 铸 1，在途
	assertWatermark("在途铸造不得推进水位线", 0)
	if evs, err := store.ListAfter(ctx, 0, 10); err != nil || len(evs) != 0 {
		t.Fatalf("未提交行不得可见（got %d 帧, err=%v）", len(evs), err)
	}

	pending.commit() // 提交：水位线与行同刻可见
	assertWatermark("提交后", 1)
	if evs, _ := store.ListAfter(ctx, 0, 10); len(evs) != 1 || evs[0].Type != "pending" {
		t.Fatalf("提交后应从 0 起连续读到 1 帧, got %d", len(evs))
	}

	rolled := store.begin(Event{SessionID: "s", Type: "rolled"}) // 铸 2
	rolled.rollback()                                            // 回滚：不烧号
	assertWatermark("回滚不得推进水位线", 1)

	reuse := store.begin(Event{SessionID: "s", Type: "reused"}) // 下一铸必须复用 2
	if reuse.id != 2 {
		t.Fatalf("回滚不烧号：下一次分配应复用 ID=2, got %d", reuse.id)
	}
	reuse.commit()
	assertWatermark("复用号提交后", 2)
	if evs, _ := store.ListAfter(ctx, 1, 10); len(evs) != 1 || evs[0].Type != "reused" {
		t.Fatalf("(1, 水位线] 应连续读到复用号那一帧, got %d", len(evs))
	}
}

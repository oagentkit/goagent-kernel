package eventbus

// issue #264 的判据：可靠交付的**形态互证**——违约自增直读（丢帧实证 +
// 哨兵响）与被承诺的 Outbox 形态（回滚空洞不触发永久丢失判据）对照。
//
// 从 tail_cursor_test.go 按关注点拆出（issue #448）：本文件只留**两种存储
// 形态的对照**这一组（判据逐字不变，拆分只动文件的内部组织）。
//
//   - 回滚空洞用例（#264 判据二）：Outbox 形态回滚不烧号、水位线不动 ⇒
//     不触发「永久丢失」判据（哨兵全程静默、无帧丢失）；
//   - 违约形态实证（#264）：自增/序列直读（naiveStore）的丢帧序列——哨兵
//     响但帧已永久丢失（告警不是兜底，该形态被契约显式声明不支持）。
//
// 乱序与空洞那组在 tail_cursor_gap_test.go（同一次拆分，判据不变）。

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

type naiveStore struct {
	mu       sync.Mutex
	seq      uint64           // 已铸造的最高 ID（含在途、含回滚烧掉的号）
	commited map[uint64]Event // 已提交可见行
}

func newNaiveStoreFrom(base uint64) *naiveStore {
	return &naiveStore{seq: base, commited: map[uint64]Event{}}
}

// naiveTx 在途事务（序列形态：回滚烧号）。
type naiveTx struct {
	s  *naiveStore
	id uint64
	ev Event
}

// mint 从序列铸造下一个 ID（在途；回滚则号烧掉——序列不回收）。
func (s *naiveStore) mint(ev Event) *naiveTx {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return &naiveTx{s: s, id: s.seq, ev: ev}
}

func (t *naiveTx) commit() {
	s := t.s
	s.mu.Lock()
	defer s.mu.Unlock()
	row := t.ev
	row.ID = t.id // 权威行携带存储铸造的 ID（同 INSERT ... RETURNING id）
	s.commited[t.id] = row
}

func (t *naiveTx) rollback() {} // 序列号已烧：行不落，号也不回收

func (s *naiveStore) MaxID(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 裸读法：已提交的最大 ID——更小的 ID 还在途也照推（违约点之一：
	// 初始化游标越过在途小 ID = 初始化即丢帧）。
	var max uint64
	for id := range s.commited {
		if id > max {
			max = id
		}
	}
	return max, nil
}

func (s *naiveStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]uint64, 0, len(s.commited))
	for id := range s.commited {
		if id > cursor {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []Event
	for _, id := range ids {
		out = append(out, s.commited[id])
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// TestNaiveAutoIncrementFormLosesFrames 缺陷实证（#264）：自增直读形态的
// 丢帧序列——A 铸 10 未提交、B 铸 11 先提交、游标被推到 11、A 提交 ⇒
// 10 永远读不到。哨兵为 [10,10] 响了，帧也补不回来——告警不是兜底。
// 该形态被 TailStore 契约显式声明不支持；本用例是排除它的实证钉子
// （与 TestTailerGapAlarmFiresOnHole 同文件互证：哨兵响 = 实现已违约）。
func TestNaiveAutoIncrementFormLosesFrames(t *testing.T) {
	store := newNaiveStoreFrom(9)
	late := store.mint(Event{SessionID: "s", Type: "late"})   // A：铸 10，未提交
	store.mint(Event{SessionID: "s", Type: "early"}).commit() // B：铸 11，先提交

	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	gaps := make(chan [2]uint64, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub,
		WithTailInterval(10*time.Millisecond),
		WithTailStartCursor(9),
		WithTailOnGap(func(from, to uint64) { gaps <- [2]uint64{from, to} }),
	).Run(ctx)

	// 裸读法直出已提交的 11：哨兵为被跳过的 [10,10] 响，游标被推到 11。
	select {
	case g := <-gaps:
		if g[0] != 10 || g[1] != 10 {
			t.Fatalf("哨兵应报被跳区间 [10,10], got [%d,%d]", g[0], g[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内哨兵未响（违约形态下哨兵必须响）")
	}
	if ev := waitTail(t, ch); ev.ID != 11 {
		t.Fatalf("违约形态下 11 先送达（游标即将越过 10）, got %d", ev.ID)
	}

	// A 迟到提交：游标已越过 10 ⇒ 10 永远读不到（静默丢帧已成立）。
	late.commit()
	select {
	case ev := <-ch:
		t.Fatalf("自增直读形态下迟到的 10 不可能再送达（游标已越过）——若送达说明形态判定有误: %+v", ev)
	case <-time.After(120 * time.Millisecond):
	}

	// 回滚烧号（与 Outbox 形态「回滚不烧号」的对照面，tail_outbox_test.go）：
	// 序列不回收 ⇒ 被烧的号是永不回填的空洞——「自增直读 + 截断读法只会
	// 永久停摆」的另一半实证。
	store.mint(Event{SessionID: "s", Type: "burned"}).rollback() // 铸 12 即烧掉
	next := store.mint(Event{SessionID: "s", Type: "after-burn"})
	if next.id != 13 {
		t.Fatalf("序列形态回滚烧号：下一次分配必须跳过被烧的 12, got %d", next.id)
	}
}

// TestTailerOutboxFormSurvivesRollbackAndDeliversAll 承诺形态端到端（#264
// 判据一 + 判据二）：中途回滚留下的不是空洞（不烧号、水位线不动），随后
// 全部提交的事件按铸造序送达，空洞哨兵全程静默——不触发永久丢失判据。
func TestTailerOutboxFormSurvivesRollbackAndDeliversAll(t *testing.T) {
	store := newOutboxStore()
	store.begin(Event{SessionID: "s", Type: "e1"}).commit()         // 1
	store.begin(Event{SessionID: "s", Type: "e-rolled"}).rollback() // 回滚：不烧号
	store.begin(Event{SessionID: "s", Type: "e2"}).commit()         // 2（复用回滚号）
	store.begin(Event{SessionID: "s", Type: "e3"}).commit()         // 3

	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	gaps := make(chan [2]uint64, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub,
		WithTailInterval(10*time.Millisecond),
		WithTailStartCursor(0),
		WithTailOnGap(func(from, to uint64) { gaps <- [2]uint64{from, to} }),
	).Run(ctx)

	for _, want := range []struct {
		id  uint64
		typ string
	}{{1, "e1"}, {2, "e2"}, {3, "e3"}} {
		ev := waitTail(t, ch)
		if ev.ID != want.id || ev.Type != want.typ {
			t.Fatalf("应按铸造序送达 %d(%s), got %d(%s)——回滚空洞不得永久跳过任何帧",
				want.id, want.typ, ev.ID, ev.Type)
		}
	}

	// 尾随进行中再回滚一铸、提交下一铸：号复用，帧照常送达。
	store.begin(Event{SessionID: "s", Type: "e-rolled2"}).rollback()
	store.begin(Event{SessionID: "s", Type: "e4"}).commit() // 4
	if ev := waitTail(t, ch); ev.ID != 4 || ev.Type != "e4" {
		t.Fatalf("回滚后的下一铸应复用号并送达, got %d(%s)", ev.ID, ev.Type)
	}

	// 哨兵全程静默：回滚空洞不是永久丢失（#264 判据二）。
	select {
	case g := <-gaps:
		t.Fatalf("回滚空洞不得触发永久丢失判据（哨兵静默）: [%d,%d]", g[0], g[1])
	case <-time.After(80 * time.Millisecond):
	}
}

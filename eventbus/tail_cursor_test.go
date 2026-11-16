package eventbus

// issue #183 的判据：游标契约的「ID 序 == 提交可见序」前提。
// issue #264 的判据：可靠交付的形态互证——违约自增直读（丢帧实证 + 哨兵响）
// 与被承诺的 Outbox 形态（回滚空洞不触发永久丢失判据）同文件对照。
//
//   - 乱序提交用例（#183 判据一，#264 判据一）：id=11 先提交、id=10 后提交，
//     消费者不得跳过 10（读侧截断形态 lateCommitStore 下端到端跑通；被承诺
//     的 Outbox 形态下乱序提交结构性地不存在——分配经水位线串行化，
//     机制替身与水位线可判口径在 tail_outbox_test.go，同包互证）；
//   - 空洞哨兵：违约 TailStore 返回跳号段时哨兵必须响（缺省 log 之外的
//     可注入通道——注入即接管，宿主接告警/指标）；
//   - 回滚空洞用例（#264 判据二）：Outbox 形态回滚不烧号、水位线不动 ⇒
//     不触发「永久丢失」判据（哨兵全程静默、无帧丢失）；
//   - 违约形态实证（#264）：自增/序列直读（naiveStore）的丢帧序列——哨兵
//     响但帧已永久丢失（告警不是兜底，该形态被契约显式声明不支持）；
//   - 重启补灌 / Load 失败停摆 / Save 失败不停摆 / 首次部署回退：#183 的
//     CursorStore 判据在 tail_cursorstore_test.go（拆分自本文件，判据不变）。

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

// lateCommitStore 守约 TailStore 替身：水位线语义（模拟 Outbox / 无空洞读法）。
//
// 事件先铸造（mint）后提交（commit）；ListAfter 只返回「从 cursor+1 起连续
// 已提交」的段——被卡在中间的未提交 ID 之后的行一律扣住不返。这正是
// TailStore 契约「无空洞推进」的可跑形态：守约实现下，迟到提交的小 ID
// 永远不会被跳过。
type lateCommitStore struct {
	mu       sync.Mutex
	minted   []Event // 已铸造（含未提交），按 ID 升序
	commited map[uint64]bool
}

func newLateCommitStore() *lateCommitStore {
	return &lateCommitStore{commited: map[uint64]bool{}}
}

func (s *lateCommitStore) mint(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minted = append(s.minted, ev)
	for i := len(s.minted) - 1; i > 0 && s.minted[i].ID < s.minted[i-1].ID; i-- {
		s.minted[i], s.minted[i-1] = s.minted[i-1], s.minted[i]
	}
}

func (s *lateCommitStore) commit(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commited[id] = true
}

func (s *lateCommitStore) MaxID(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 无空洞边界（#264 收紧后的 MaxID 契约）：已提交的最高连续 ID——未提交
	// 的小 ID 卡在中间时不得越过它（初始化游标越过在途小 ID = 初始化即丢帧，
	// 与 ListAfter 的跳号违约同罪）。最低铸造号之下视为已消费基线。
	if len(s.minted) == 0 {
		return 0, nil
	}
	w := s.minted[0].ID - 1
	for _, ev := range s.minted {
		if ev.ID != w+1 || !s.commited[ev.ID] {
			break
		}
		w = ev.ID
	}
	return w, nil
}

func (s *lateCommitStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, ev := range s.minted {
		if ev.ID <= cursor {
			continue
		}
		if !s.commited[ev.ID] {
			break // 无空洞推进：卡住的未提交 ID 之后的行全部扣住
		}
		out = append(out, ev)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// holeStore 违约 TailStore 替身：跳号返回（模拟「11 先提交、10 迟到」下
// 不设防的 READ COMMITTED 读法）。
type holeStore struct{ fake fakeTailStore }

func (s *holeStore) MaxID(ctx context.Context) (uint64, error) { return s.fake.MaxID(ctx) }

func (s *holeStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.fake.mu.Lock()
	defer s.fake.mu.Unlock()
	s.fake.listCalls = append(s.fake.listCalls, cursor)
	if s.fake.failList > 0 {
		s.fake.failList--
		return nil, errFakeRead
	}
	var out []Event
	for _, ev := range s.fake.evs {
		if ev.ID <= cursor {
			continue
		}
		out = append(out, ev) // 不查空洞：违约形态——cursor=9 时直接回 11
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// TestTailerOutOfOrderCommitNotSkipped 乱序提交验收（#183 判据一）：
// id=11 先提交、id=10 后提交，消费者不得跳过 10。
//
// 守约 TailStore（水位线/Outbox 语义）在 10 未提交时扣住 11；10 提交后
// 一次性回 [10,11]——Tailer 逐条投递，订阅者按序收到 10 与 11。
func TestTailerOutOfOrderCommitNotSkipped(t *testing.T) {
	store := newLateCommitStore()
	store.mint(Event{ID: 10, SessionID: "s", Type: "late"})  // 铸造 10，未提交
	store.mint(Event{ID: 11, SessionID: "s", Type: "early"}) // 铸造 11
	store.commit(11)                                         // 11 先提交（乱序形态）

	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(store, hub, WithTailInterval(10*time.Millisecond), WithTailStartCursor(9)).Run(ctx)

	// 11 已提交但 10 未提交：守约存储必须扣住整段（不得先送 11）。等若干拍
	// 确认没有「只送 11」的窗口——这是契约的可观测结论。
	select {
	case ev := <-ch:
		t.Fatalf("10 未提交时不得投递任何帧（先送 %d 即永久跳过 10）", ev.ID)
	case <-time.After(50 * time.Millisecond):
	}

	store.commit(10) // 10 迟到提交
	ev := waitTail(t, ch)
	if ev.ID != 10 {
		t.Fatalf("迟到提交的 10 必须先送达（实得 %d——被跳过即静默丢帧）", ev.ID)
	}
	if ev2 := waitTail(t, ch); ev2.ID != 11 {
		t.Fatalf("11 应在 10 之后送达, got %d", ev2.ID)
	}
}

// TestTailerGapAlarmFiresOnHole 空洞哨兵（#183 判据一的反向面）：违约
// TailStore 跳号返回时哨兵必须响——静默丢帧从此变成响亮信号。
func TestTailerGapAlarmFiresOnHole(t *testing.T) {
	store := &holeStore{fake: fakeTailStore{maxID: 9}}
	store.fake.append(Event{ID: 11, SessionID: "s", Type: "x"}) // 10 缺席，直回 11

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

	select {
	case g := <-gaps:
		if g[0] != 10 || g[1] != 10 {
			t.Fatalf("哨兵应报被跳区间 [10,10], got [%d,%d]", g[0], g[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内哨兵未响（跳号被静默放过 = 丢帧不可见）")
	}
	// 事件段照常投递（停摆只会让缺口更大）；下一拍游标已到 11，不再重复报警。
	if ev := waitTail(t, ch); ev.ID != 11 {
		t.Fatalf("空洞段的事件仍应投递, got %d", ev.ID)
	}
	select {
	case g := <-gaps:
		t.Fatalf("同一空洞不得重复报警: [%d,%d]", g[0], g[1])
	case <-time.After(60 * time.Millisecond):
	}
}

// ── issue #264：可靠事件交付的形态互证（违约自增直读 ↔ 被承诺的 Outbox）────
//
// naiveStore 与 holeStore 同为违约替身，违约面不同：holeStore 是「返回段里
// 有跳号」（哨兵的直接触发面）；naiveStore 是「分配 + 裸读」的完整自增形态
// ——它遵守「升序返回」的表象，丢帧发生在语义层（游标越过在途小 ID），
// 哨兵响但帧补不回来。Outbox 机制替身（outboxStore）与水位线可判口径在
// tail_outbox_test.go；真 PG 上的同判据在 examples/pgstore（pg_event_test.go）。

// naiveStore 违约 TailStore 替身之二：自增/序列 ID 直读形态（BIGSERIAL 分配
// + READ COMMITTED 下 WHERE id > cursor 的裸读法）。
//
// 铸造走序列（回滚烧号，号不回收）；提交使行可见；ListAfter 不查空洞——
// cursor 之后的已提交行全部直出。这是 TailStore 契约**显式声明不支持**的
// 形态：本替身把它钉在测试里作丢帧实证，不是可用实现的样板。
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

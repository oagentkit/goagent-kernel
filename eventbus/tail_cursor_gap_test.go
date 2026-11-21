package eventbus

// issue #183 的判据：游标契约的「ID 序 == 提交可见序」前提。
//
// 从 tail_cursor_test.go 按关注点拆出（issue #448）：本文件只留**乱序与空洞**
// 这一组（判据逐字不变，拆分只动文件的内部组织）。
//
//   - 乱序提交用例（#183 判据一，#264 判据一）：id=11 先提交、id=10 后提交，
//     消费者不得跳过 10（读侧截断形态 lateCommitStore 下端到端跑通；被承诺
//     的 Outbox 形态下乱序提交结构性地不存在——分配经水位线串行化，
//     机制替身与水位线可判口径在 tail_outbox_test.go，同包互证）；
//   - 空洞哨兵：违约 TailStore 返回跳号段时哨兵必须响（缺省 log 之外的
//     可注入通道——注入即接管，宿主接告警/指标）；
//   - 重启补灌 / Load 失败停摆 / Save 失败不停摆 / 首次部署回退：#183 的
//     CursorStore 判据在 tail_cursorstore_test.go（拆分自本文件，判据不变）；
//   - 形态互证（naiveStore 丢帧实证 vs Outbox 回滚存活）在
//     tail_cursor_form_test.go（同一次拆分，判据不变）。

import (
	"context"
	"sync"
	"testing"
	"time"
)

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

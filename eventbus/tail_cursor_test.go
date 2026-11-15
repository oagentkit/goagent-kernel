package eventbus

// issue #183 的判据：游标契约的「ID 序 == 提交可见序」前提 + 游标持久化接缝。
//
//   - 乱序提交用例：id=11 先提交、id=10 后提交，消费者不得跳过 10
//     （守约 TailStore 的水位线语义下端到端跑通）；
//   - 空洞哨兵：违约 TailStore 返回跳号段时哨兵必须响（缺省 log 之外的
//     可注入通道——注入即接管，宿主接告警/指标）；
//   - 重启补灌：停机窗口内发布的事件，重启后从持久化游标续随仍被补灌
//     （缺省 MaxID 初始化会把它整个跳过——正是 #183 的缺口二）；
//   - Load 失败停摆：绝不静默回退 MaxID（宁可停摆不可跳读）；
//   - Save 失败不停摆：游标落后只会重读（订阅侧幂等吸收），方向安全。

import (
	"context"
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
	if len(s.minted) == 0 {
		return 0, nil
	}
	return s.minted[len(s.minted)-1].ID, nil
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
type holeStore struct{ fakeTailStore }

func (s *holeStore) ListAfter(_ context.Context, cursor uint64, limit int) ([]Event, error) {
	s.fakeTailStore.mu.Lock()
	defer s.fakeTailStore.mu.Unlock()
	s.fakeTailStore.listCalls = append(s.fakeTailStore.listCalls, cursor)
	if s.fakeTailStore.failList > 0 {
		s.fakeTailStore.failList--
		return nil, errFakeRead
	}
	var out []Event
	for _, ev := range s.fakeTailStore.evs {
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

// memCursorStore 进程内 CursorStore 替身。
type memCursorStore struct {
	mu       sync.Mutex
	cur      uint64
	saved    bool
	failLoad int
	failSave int
}

func (c *memCursorStore) LoadCursor(context.Context) (uint64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failLoad > 0 {
		c.failLoad--
		return 0, false, errFakeRead
	}
	return c.cur, c.saved, nil
}

func (c *memCursorStore) SaveCursor(_ context.Context, cursor uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failSave > 0 {
		c.failSave--
		return errFakeRead
	}
	c.cur, c.saved = cursor, true
	return nil
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
	inner := &fakeTailStore{maxID: 9}
	store := &holeStore{fakeTailStore: *inner}
	store.append(Event{ID: 11, SessionID: "s", Type: "x"}) // 10 缺席，直回 11

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

// TestTailerCursorPersistsAcrossRestart 重启补灌（#183 判据二）：停机窗口内
// 发布的事件，重启后从持久化游标续随仍被补灌。
//
// 对照面（缺省形态）：重启走 MaxID 初始化 ⇒ 停机窗口内的事件被整个跳过。
func TestTailerCursorPersistsAcrossRestart(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	cursor := &memCursorStore{}
	hub := NewHub()

	// 第一程：初始化（MaxID=5）并干净退出（等 Run 真返回，避免取消在途时
	// 它再处理一拍——停机窗口的起点必须确定）。
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx1)
		close(done1)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for tail.maxIDs() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("第一程 2s 内未完成初始化")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel1()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("第一程 ctx 取消后未返回")
	}

	// 停机窗口：他副本发布 6。
	tail.append(Event{ID: 6, SessionID: "s", Type: "during-downtime"})

	// 第二程（重启）：必须从持久化游标 5 续随——不得走 MaxID（那会跳过 6）。
	maxIDBeforeRestart := tail.maxIDs()
	ch, stop := hub.Subscribe("s")
	defer stop()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx2)

	ev := waitTail(t, ch)
	if ev.ID != 6 {
		t.Fatalf("停机窗口内的事件必须被补灌（实得 %d——MaxID 初始化把它跳过了）", ev.ID)
	}
	if got := tail.maxIDs(); got != maxIDBeforeRestart {
		t.Fatalf("第二程不得重走 MaxID 初始化（%d → %d）", maxIDBeforeRestart, got)
	}
	// 游标推进后已保存：重启场景下持久化值应为 6。
	cursor.mu.Lock()
	saved := cursor.cur
	cursor.mu.Unlock()
	if saved != 6 {
		t.Fatalf("游标推进后应持久化（实得 %d）", saved)
	}
}

// TestTailerCursorLoadFailureStallsNeverSkips Load 失败停摆重试（#183）：
// 绝不静默回退 MaxID——回退 = 把停机窗口整个跳过（宁可停摆不可跳读）。
func TestTailerCursorLoadFailureStallsNeverSkips(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	tail.append(Event{ID: 6, SessionID: "s", Type: "during-downtime"})
	cursor := &memCursorStore{saved: true, cur: 5, failLoad: 1000} // 持续失败：先验证停摆语义
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx)

	// 多拍之后：MaxID 不得被调（回退即跳读），事件不得送达（停摆中）。
	time.Sleep(80 * time.Millisecond)
	if got := tail.maxIDs(); got != 0 {
		t.Fatalf("Load 失败不得回退 MaxID 初始化（调了 %d 次 = 跳读停机窗口）", got)
	}
	if len(tail.cursors()) != 0 {
		t.Fatal("Load 失败期间不得读存量")
	}
	// 恢复后从持久化游标续随，补灌照常。
	cursor.mu.Lock()
	cursor.failLoad = 0
	cursor.mu.Unlock()
	if ev := waitTail(t, ch); ev.ID != 6 {
		t.Fatalf("Load 恢复后应从游标 5 续随并补灌 6, got %d", ev.ID)
	}
}

// TestTailerCursorSaveFailureKeepsTailing Save 失败不停摆：游标落后只会
// 导致重启后重读（订阅侧幂等吸收），停摆反而拆了实时补灌桥。
func TestTailerCursorSaveFailureKeepsTailing(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	cursor := &memCursorStore{failSave: 1}
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx)

	tail.append(Event{ID: 6, SessionID: "s", Type: "x1"})
	if ev := waitTail(t, ch); ev.ID != 6 {
		t.Fatalf("Save 失败不得停摆投递, got %d", ev.ID)
	}
	tail.append(Event{ID: 7, SessionID: "s", Type: "x2"})
	if ev := waitTail(t, ch); ev.ID != 7 {
		t.Fatalf("后续事件应照常补灌, got %d", ev.ID)
	}
	// 恢复后的推进仍会落盘：最终持久化值应跟上游标。
	deadline := time.Now().Add(2 * time.Second)
	for {
		cursor.mu.Lock()
		saved := cursor.cur
		cursor.mu.Unlock()
		if saved == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Save 恢复后游标应落盘到 7, 实得 %d", saved)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTailerCursorNotFoundFallsBackToMaxID 首次部署（从未存过游标）回退
// MaxID 初始化——持久化面不改变「补灌不是回放」的缺省语义。
func TestTailerCursorNotFoundFallsBackToMaxID(t *testing.T) {
	tail := &fakeTailStore{maxID: 5}
	cursor := &memCursorStore{} // saved=false
	hub := NewHub()
	ch, stop := hub.Subscribe("s")
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewTailer(tail, hub, WithTailInterval(10*time.Millisecond), WithTailCursorStore(cursor)).Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for tail.maxIDs() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("未存过游标应回退 MaxID 初始化（2s 内未发生）")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case ev := <-ch:
		t.Fatalf("回退 MaxID 后存量不得回放（补灌不是回放）: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

package ticket

import (
	"testing"
	"time"
)

// 本文件是 StorageClockBackend 接缝（issue #186）的判据：
//   - 契约测试：两个节点时钟互相偏移，过期判定**只随存储时钟**——
//     快钟签发不得提前过期、慢钟签发不得推迟过期；
//   - memoryClockBackend 用注入时钟实现同一语义（先删后判、满容量拒发）；
//   - Store 经 NewWithStorageClock 注入后：签发侧时钟不参与判定，
//     Issue 返回的过期时刻是存储落下的权威值。

// Test过期判定只随存储时钟 —— issue #186 验收原句：「注入两个不同时钟的节点，
// 过期判定只随存储时钟」。存储时钟固定在 T0：
//   - 节点时钟快 30s 时 TTL=1s 的票「看起来已过期」，存储时钟说它没过期
//     ⇒ 必须消费成功（快钟不得偷走票据）；
//   - 存储时钟判过期后，慢钟节点（以为还有余量）必须消费失败
//     （慢钟不得重放过期授权）。
func Test过期判定只随存储时钟(t *testing.T) {
	T0 := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	storageClock := func() time.Time { return T0 }
	b := NewMemoryBackendWithClock(0, storageClock)

	// 存储落的权威过期时刻 = 存储时钟 + TTL（签发侧节点时钟不参与）。
	exp, err := b.Put("fast-ticket", Binding{Subject: "a"}, 1*time.Second)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !exp.Equal(T0.Add(1 * time.Second)) {
		t.Fatalf("过期时刻必须由存储时钟落下（T0+1s），实得 %v", exp)
	}
	// 快 30s 的节点时钟认为它已过期（T0+1s < T0+30s）——但判定归存储：
	// 快钟不得把未过期票偷走（先删后判 ⇒ 误删不可恢复）。
	bnd, ok, err := b.Take("fast-ticket")
	if err != nil || !ok {
		t.Fatalf("快钟偏移不得改写判定（存储时钟 T0 未到 T0+1s）：ok=%v err=%v", ok, err)
	}
	if bnd.Subject != "a" {
		t.Fatalf("绑定载荷应原样取回，实得 %+v", bnd)
	}

	// 存储落两张票后再拨存储时钟：TTL=1s 的已过期、TTL=1h 的未过期——
	// 节点时钟无论快慢都改写不了这两条判定。
	if _, err := b.Put("will-expire", Binding{Subject: "b"}, time.Second); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := b.Put("long-lived", Binding{Subject: "c"}, time.Hour); err != nil {
		t.Fatalf("Put: %v", err)
	}
	T0 = T0.Add(2 * time.Second) // 存储时钟前进（模拟时间流逝）
	// 慢 30s 的节点时钟（T0-30s）对 will-expire 判「还有 31s」；判定仍归存储。
	if _, ok, _ := b.Take("will-expire"); ok {
		t.Fatal("存储时钟判过期后必须消费失败（慢钟偏移不得重放过期授权）")
	}
	// 先删后判：过期票虽然拒consume，但已被删除（只剩 long-lived）。
	if n, _ := b.Len(); n != 1 {
		t.Fatalf("过期票应已被 Take 删除（先删后判），应剩 1 张未过期的，实得 %d", n)
	}
	// 未过期的票不受任何节点时钟偏移影响。
	if _, ok, _ := b.Take("long-lived"); !ok {
		t.Fatal("存储时钟判未过期的票必须可消费")
	}
}

// TestMemoryClockBackend同一语义 —— 参考实现与旧 memoryBackend 判据同构：
// 未知 id 失败、单次消费、满容量显式拒发、GC 按存储时钟清理。
func TestMemoryClockBackend同一语义(t *testing.T) {
	T0 := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	current := T0
	b := NewMemoryBackendWithClock(2, func() time.Time { return current })

	if _, _, err := b.Take("nope"); err != nil {
		t.Fatalf("未知 id 不得报错（ok=false），实得 %v", err)
	}
	if _, err := b.Put("t1", Binding{Subject: "s"}, time.Minute); err != nil {
		t.Fatalf("Put t1: %v", err)
	}
	if _, err := b.Put("t2", Binding{Subject: "s"}, time.Minute); err != nil {
		t.Fatalf("Put t2: %v", err)
	}
	if _, err := b.Put("t3", Binding{Subject: "s"}, time.Minute); err == nil {
		t.Fatal("满容量必须显式拒发（满了就说满，不默默淘汰）")
	}
	// 单次消费。
	if _, ok, _ := b.Take("t1"); !ok {
		t.Fatal("未过期票必须消费成功")
	}
	if _, ok, _ := b.Take("t1"); ok {
		t.Fatal("同一张票第二次消费必须失败（用过即焚）")
	}
	// GC 按存储时钟：时钟前进 2 分钟，t2 已过期。
	current = T0.Add(2 * time.Minute)
	n, err := b.GC()
	if err != nil || n != 1 {
		t.Fatalf("GC 应清 1 张过期票，实得 n=%d err=%v", n, err)
	}
	if l, _ := b.Len(); l != 0 {
		t.Fatalf("GC 后应为空，实得 %d", l)
	}
}

// TestStore经存储时钟签发与消费 —— Store 机制面在 scb 形态下的分工：
// 签发侧时钟不参与判定；Issue 返回存储落下的权威过期时刻；
// GC / Len / 满容量拒发经同一接缝生效。
func TestStore经存储时钟签发与消费(t *testing.T) {
	T0 := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	current := T0
	b := NewMemoryBackendWithClock(1, func() time.Time { return current })
	s := NewWithStorageClock(b)
	// 签发侧时钟快 1 小时（多副本里的「快钟节点」）：旧形态下这 1 小时
	// 会直接写进过期时刻，本形态必须被无视。
	s.now = func() time.Time { return T0.Add(time.Hour) }

	id, exp, err := s.Issue(Binding{Subject: "op", Purpose: "console"}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !exp.Equal(T0.Add(time.Minute)) {
		t.Fatalf("返回的过期时刻必须是存储落下的权威值（T0+1m），实得 %v", exp)
	}
	if got, ok := s.Consume(id); !ok || got.Subject != "op" {
		t.Fatalf("消费应成功且原样取回载荷，实得 ok=%v %+v", ok, got)
	}

	// 第二张票在存储时钟前进**之前**落库，前进后消费：
	// 存储时钟判过期 ⇒ 不可消费（签发侧快钟不得续命）。
	id2, _, err := s.Issue(Binding{Subject: "op"}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	current = T0.Add(2 * time.Minute)
	if _, ok := s.Consume(id2); ok {
		t.Fatal("存储时钟判过期后必须消费失败（签发侧快钟不得续命）")
	}
	// 观测面与清理面经同一接缝。
	if l := s.Len(); l != 0 {
		t.Fatalf("过期票已删，Len 应为 0，实得 %d", l)
	}
	if n := s.GC(); n != 0 {
		t.Fatalf("无过期票时 GC 应为 0，实得 %d", n)
	}
	// 满容量拒发口径经 scb 生效。
	if _, err := b.Put("fill", Binding{}, time.Minute); err != nil {
		t.Fatalf("Put fill: %v", err)
	}
	if _, _, err := s.Issue(Binding{}, time.Minute); err == nil {
		t.Fatal("容量满必须显式拒发")
	}
}

// Test双节点同Store契约 —— 两个「节点」用互相偏移的节点时钟签发与消费，
// 共享一份存储时钟权威 Backend：彼此的偏移都不改写对方的判定
// （issue 验收的「两个不同时钟的节点」直译形态）。
func Test双节点同Store契约(t *testing.T) {
	T0 := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	storage := T0
	b := NewMemoryBackendWithClock(0, func() time.Time { return storage })

	nodeA := NewWithStorageClock(b) // 节点 A：快 30s（签发侧时钟被无视）
	nodeA.now = func() time.Time { return T0.Add(30 * time.Second) }
	nodeB := NewWithStorageClock(b) // 节点 B：慢 30s
	nodeB.now = func() time.Time { return T0.Add(-30 * time.Second) }

	// 节点 A 签发（快钟），节点 B 消费（慢钟）：判定随存储时钟（未过期）⇒ 成功。
	idA, expA, err := nodeA.Issue(Binding{Subject: "a"}, 5*time.Second)
	if err != nil {
		t.Fatalf("A 签发: %v", err)
	}
	if !expA.Equal(T0.Add(5 * time.Second)) {
		t.Fatalf("A 的过期时刻应随存储时钟，实得 %v", expA)
	}
	if _, ok := nodeB.Consume(idA); !ok {
		t.Fatal("跨节点消费判定必须只随存储时钟（A 快钟签发不得被判过期）")
	}

	// 节点 B 签发 1s 票（存储时钟前进**之前**落库），前进 2s ⇒ 过期；
	// B 的慢钟（T0-30s）以为还有 31s 余量——判定仍归存储 ⇒ 失败。
	idB, _, err := nodeB.Issue(Binding{Subject: "b"}, time.Second)
	if err != nil {
		t.Fatalf("B 签发: %v", err)
	}
	storage = T0.Add(2 * time.Second) // 存储时钟前进（模拟时间流逝）
	if _, ok := nodeB.Consume(idB); ok {
		t.Fatal("存储时钟已判过期（T0+2s > T0+1s），慢钟不得重放")
	}
	// 存储时钟 T0+2s 下新签发的 5s 票（过期时刻 T0+7s）未过期 ⇒ 可消费
	// （此前 idA 已被 B 消费过——用过即焚，单次语义不受时钟形态影响）。
	idA2, _, err := nodeA.Issue(Binding{Subject: "a2"}, 5*time.Second)
	if err != nil {
		t.Fatalf("A 二次签发: %v", err)
	}
	if _, ok := nodeB.Consume(idA2); !ok {
		t.Fatal("按存储时钟未过期的票不得被误判（节点时钟偏移无关判定）")
	}
}

// TestNewWithStorageClock缺省退化 —— nil 接缝退化为参考实现；
// NewMemoryBackendWithClock 的 nil 时钟退化为 time.Now。
func TestNewWithStorageClock缺省退化(t *testing.T) {
	s := NewWithStorageClock(nil)
	id, exp, err := s.Issue(Binding{Subject: "s"}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if id == "" {
		t.Fatal("缺省退化必须可用（签发出真实 id）")
	}
	if exp.Before(time.Now().Add(time.Minute - time.Second)) {
		t.Fatalf("nil 时钟退化应为 time.Now 口径，实得 %v", exp)
	}
	b := NewMemoryBackendWithClock(0, nil)
	if _, err := b.Put("x", Binding{}, time.Minute); err != nil {
		t.Fatalf("nil 时钟退化 Put: %v", err)
	}
}

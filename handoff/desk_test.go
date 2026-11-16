// desk_test.go —— 持久化 Store 面的用例：跨实例交接、回执寻址、代次防 ABA。
//
// 夹具全部合成数据（proc-a / proc-b / step-7），不携带任何领域词面。
package handoff

import (
	"context"
	"testing"
	"time"

	kerrors "github.com/oekit/pkg/errors"
)

// newTestDesks 两份客户端共用同一个 Ledger（模拟两个进程客户端）。
func newTestDesks() (*Desk, *Desk, *MemoryLedger, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	ledger := NewMemoryLedger(WithLedgerClock(clock.Now))
	return NewDesk(ledger), NewDesk(ledger), ledger, clock
}

// TestDeskCrossInstanceHandoff 跨实例（两个 Store 客户端）完成一次交接。
func TestDeskCrossInstanceHandoff(t *testing.T) {
	ctx := context.Background()
	source, target, _, _ := newTestDesks()

	offered, err := source.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	if offered.Generation != 1 {
		t.Fatalf("首代应为 1，得到 %+v", offered)
	}

	// 另一个客户端的快照必须看到同一单（共享持久化存储，不是各持一份）。
	snap, err := target.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 || snap[0].Key != "step-7" || snap[0].State != StateOffered {
		t.Fatalf("跨实例快照不一致：%+v", snap)
	}

	h, err := target.Take(ctx, "step-7", "proc-b")
	if err != nil {
		t.Fatal(err)
	}
	if h.Token == "" || h.Fence != 1 {
		t.Fatalf("handle 应由存储铸造令牌与代次：%+v", h)
	}
	if err := target.Reply(ctx, "step-7", h.Token, Receipt{Status: ReceiptDone, Summary: "ok"}); err != nil {
		t.Fatal(err)
	}

	// 源方在**自己的地址**上取回执（回执寻址）；不用问领取方要。
	got, err := source.Deliveries(ctx, "reply://proc-a/inbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应取到一条回执，得到 %+v", got)
	}
	if got[0].Key != "step-7" || got[0].Generation != 1 || got[0].Receipt.Status != ReceiptDone {
		t.Fatalf("回执内容不完整：%+v", got[0])
	}

	// 已收口：领取方再领被拒（终态），源方快照可见 closed。
	if _, err := target.Take(ctx, "step-7", "proc-b"); !kerrors.Is(err, ErrAlreadySettled) {
		t.Fatalf("终态再领取应 ErrAlreadySettled，得到 %v", err)
	}
	snap, _ = source.Snapshot(ctx)
	if len(snap) != 1 || snap[0].State != StateClosed || snap[0].ReceiptAt.IsZero() {
		t.Fatalf("收口状态未共享：%+v", snap)
	}
}

// TestDeskReceiptAddressing 回执只送到声明的地址；Delete 之后仍可取回。
func TestDeskReceiptAddressing(t *testing.T) {
	ctx := context.Background()
	d, _, ledger, _ := newTestDesks()

	mk := func(key Key, replyTo string) Ticket {
		tk := baseTicket()
		tk.Key = key
		tk.ReplyTo = replyTo
		return tk
	}
	for _, tk := range []Ticket{
		mk("step-a", "reply://inbox-a"),
		mk("step-b", "reply://inbox-b"),
	} {
		if _, err := d.Offer(ctx, tk); err != nil {
			t.Fatal(err)
		}
		h, err := d.Take(ctx, tk.Key, "proc-b")
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Reply(ctx, tk.Key, h.Token, Receipt{Status: ReceiptDone, Summary: "ok"}); err != nil {
			t.Fatal(err)
		}
	}

	a, err := d.Deliveries(ctx, "reply://inbox-a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := d.Deliveries(ctx, "reply://inbox-b")
	none, _ := d.Deliveries(ctx, "reply://inbox-z")
	if len(a) != 1 || a[0].Key != "step-a" {
		t.Fatalf("inbox-a 只应收到 step-a 的回执：%+v", a)
	}
	if len(b) != 1 || b[0].Key != "step-b" {
		t.Fatalf("inbox-b 只应收到 step-b 的回执：%+v", b)
	}
	if len(none) != 0 {
		t.Fatalf("未声明的地址不应收到任何回执：%+v", none)
	}

	// 记录删除后回执仍在（源方崩溃重启仍能按地址取件）。
	if err := ledger.Delete(ctx, "step-a"); err != nil {
		t.Fatal(err)
	}
	a, _ = d.Deliveries(ctx, "reply://inbox-a")
	if len(a) != 1 || a[0].Key != "step-a" {
		t.Fatalf("Delete 不应删除已投递的回执：%+v", a)
	}
	if _, err := ledger.Load(ctx, "step-a"); !kerrors.Is(err, ErrUnknownKey) {
		t.Fatalf("Delete 后 Load 应 ErrUnknownKey，得到 %v", err)
	}
}

// TestDeskOwnershipEpochMonotonic 代次跨终态与 Delete 严格递增；旧代拒绝。
func TestDeskOwnershipEpochMonotonic(t *testing.T) {
	ctx := context.Background()
	d, _, ledger, clock := newTestDesks()

	first, err := d.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(ctx, "step-7", "proc-a"); err != nil {
		t.Fatal(err)
	}
	second, err := d.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 {
		t.Fatalf("收回后重新交接应开第二代：%+v", second)
	}

	// Delete 之后重新挂出必须继续递增（代次水位保留，防 ABA）。
	if err := ledger.Delete(ctx, "step-7"); err != nil {
		t.Fatal(err)
	}
	watermark, err := ledger.Epoch(ctx, "step-7")
	if err != nil {
		t.Fatal(err)
	}
	if watermark != 2 {
		t.Fatalf("Delete 必须保留代次水位（应为 2，得到 %d）", watermark)
	}
	third, err := d.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	if third.Generation != 3 {
		t.Fatalf("Delete 后重新挂出代次必须大于水位：%+v", third)
	}

	// 旧代领取被拒；当前代可领。
	if _, err := d.TakeAt(ctx, "step-7", "proc-b", Epoch(first.Generation)); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("旧代领取应 ErrStaleEpoch，得到 %v", err)
	}
	if _, err := d.TakeAt(ctx, "step-7", "proc-b", Epoch(second.Generation)); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("旧代领取应 ErrStaleEpoch，得到 %v", err)
	}
	h, err := d.TakeAt(ctx, "step-7", "proc-b", Epoch(third.Generation))
	if err != nil {
		t.Fatalf("当前代应可领取：%v", err)
	}
	if h.Fence != uint64(third.Generation) {
		t.Fatalf("Fence 应为当前代次：%+v vs %d", h, third.Generation)
	}

	// 旧代直接写 Ledger 也必须被拒（不是只有 Desk 层拦）。
	stale := third
	stale.Generation = uint64(first.Generation)
	if _, err := ledger.Save(ctx, Record{Ticket: stale, Epoch: Epoch(first.Generation), Revision: 0}); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("旧代 Save 应 ErrStaleEpoch，得到 %v", err)
	}

	// 过期后重新交接 = 新代，旧令牌的回执必被拒。
	clock.Advance(2 * time.Minute)
	if err := d.Reply(ctx, "step-7", h.Token, Receipt{Status: ReceiptDone, Summary: "late"}); !kerrors.Is(err, ErrExpired) {
		t.Fatalf("迟到回执应 ErrExpired（懒式落账），得到 %v", err)
	}
	next, err := d.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != 4 {
		t.Fatalf("过期后重新交接应开第四代：%+v", next)
	}
	if err := d.Reply(ctx, "step-7", h.Token, Receipt{Status: ReceiptDone, Summary: "late"}); !kerrors.Is(err, ErrLeaseLost) {
		t.Fatalf("旧代令牌的迟到回执应 ErrLeaseLost，得到 %v", err)
	}
}

// TestDeskRevisionConflict 并发写者用旧版本覆盖 → ErrConflict 且不写入。
func TestDeskRevisionConflict(t *testing.T) {
	ctx := context.Background()
	_, _, ledger, _ := newTestDesks()

	firstTicket := baseTicket()
	firstTicket.State = StateOffered
	if _, err := ledger.Save(ctx, Record{Ticket: firstTicket}); err != nil {
		t.Fatal(err)
	}
	rec, err := ledger.Load(ctx, "step-7")
	if err != nil {
		t.Fatal(err)
	}
	// 第一位写者推进版本。
	rec.Ticket.State = StateAccepted
	rec.Ticket.Holder = "proc-b"
	saved, err := ledger.Save(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision == rec.Revision {
		t.Fatal("Save 必须推进版本")
	}
	// 第二位写者仍拿旧版本：必须被拒，不得覆盖第一位的结果。
	rec.Ticket.Holder = "proc-c"
	if _, err := ledger.Save(ctx, rec); !kerrors.Is(err, ErrConflict) {
		t.Fatalf("旧版本覆盖应 ErrConflict，得到 %v", err)
	}
	cur, _ := ledger.Load(ctx, "step-7")
	if cur.Ticket.Holder != "proc-b" {
		t.Fatalf("冲突写不得落地：%+v", cur.Ticket)
	}
}

// TestDeskConcurrentTakeTwoClients 两个客户端同时领取同一单：赢家恰好 1 个。
func TestDeskConcurrentTakeTwoClients(t *testing.T) {
	ctx := context.Background()
	a, b, _, _ := newTestDesks()
	if _, err := a.Offer(ctx, baseTicket()); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type result struct {
		h   Handle
		err error
	}
	results := make(chan result, 2)
	for _, desk := range []*Desk{a, b} {
		d := desk
		go func() {
			<-start
			h, err := d.Take(ctx, "step-7", "proc-b")
			results <- result{h: h, err: err}
		}()
	}
	close(start)

	winners, losers := 0, 0
	for range 2 {
		r := <-results
		switch {
		case r.err == nil:
			winners++
		case kerrors.Is(r.err, ErrTaken):
			losers++
		default:
			t.Fatalf("输家必须得到 ErrTaken，得到 %v", r.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("并发领取赢家应为 1（得到 %d 赢 / %d 输）", winners, losers)
	}
}

// TestLocalDeskDeliveriesAndTakeAt 默认实现也接上了回执寻址与代次校验。
func TestLocalDeskDeliveriesAndTakeAt(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	d := NewLocalDesk(WithClock(clock.Now))

	tk, err := d.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.TakeAt(ctx, "step-7", "proc-b", Epoch(tk.Generation+1)); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("错误代次应 ErrStaleEpoch，得到 %v", err)
	}
	h, err := d.TakeAt(ctx, "step-7", "proc-b", Epoch(tk.Generation))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Reply(ctx, "step-7", h.Token, Receipt{Status: ReceiptDone, Summary: "ok"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Deliveries(ctx, "reply://proc-a/inbox")
	if err != nil || len(got) != 1 {
		t.Fatalf("LocalDesk 应可取回执：%+v, %v", got, err)
	}
}

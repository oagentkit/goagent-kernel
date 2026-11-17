// unknownstate_test.go —— 写路径对词表外状态必须有闸（issue #359）。
//
// 体检面（Check）早已能认出词表外状态并报 CodeUnknownState，写路径此前
// 一个都不消费这个事实：词表外状态被当成「非终态」，交接单照常被领走——
// 内核在自己读不懂的记录上铸造了领取令牌，还把状态改写成 accepted
// （原始状态被抹掉，事后无从区分「这单本来就是词表外」与「正常领取」）。
package handoff

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// dirtyState 词表外状态（脏数据：跨版本存储 / 宿主自造状态名）。
const dirtyState = State("mystery")

// seedDirtyRecord 把一条词表外状态的记录直接塞进账里。
//
// 落库面（MemoryLedger.Save）现在会拦这种记录——那正是本条修的；这里绕过
// 它是为了构造「脏数据已经在库里」的场景（它可能来自旧版本或别的写入方）。
func seedDirtyRecord(t *testing.T, l *MemoryLedger, tk Ticket) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records[tk.Key] = cloneRecord(Record{Ticket: tk, Epoch: 1, Revision: 1})
	l.epochs[tk.Key] = 1
	l.revs[tk.Key] = 1
}

// dirtyLedger 一个账里躺着一条词表外状态的交接单。
func dirtyLedger(t *testing.T) (*Desk, *MemoryLedger, Ticket) {
	t.Helper()
	ledger := NewMemoryLedger()
	tk := baseTicket()
	tk.State = dirtyState
	tk.Deadline = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Minute)
	seedDirtyRecord(t, ledger, tk)
	return NewDesk(ledger), ledger, tk
}

// TestDeskTakeRejectsUnknownState 领取必须报 ErrUnknownState，且不得铸令牌、
// 不得改写状态。
func TestDeskTakeRejectsUnknownState(t *testing.T) {
	d, ledger, tk := dirtyLedger(t)
	h, err := d.Take(context.Background(), tk.Key, "proc-b")
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	if h.Token != "" || h.Fence != 0 {
		t.Fatalf("被拒的领取不得铸令牌/代次：%+v", h)
	}
	rec, lerr := ledger.Load(context.Background(), tk.Key)
	if lerr != nil {
		t.Fatalf("Load: %v", lerr)
	}
	if rec.Ticket.State != dirtyState {
		t.Fatalf("被拒的领取不得改写状态：%q → %q", dirtyState, rec.Ticket.State)
	}
	if rec.Token != "" || rec.Ticket.Holder != "" {
		t.Fatalf("被拒的领取不得留下占用痕迹：token=%q holder=%q", rec.Token, rec.Ticket.Holder)
	}
}

// TestDeskReplyRejectsUnknownState 交回执同样必须拒绝（写路径同向）。
func TestDeskReplyRejectsUnknownState(t *testing.T) {
	d, ledger, tk := dirtyLedger(t)
	err := d.Reply(context.Background(), tk.Key, "take-whatever",
		Receipt{Status: ReceiptDone, Summary: "合成结论"})
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	rec, lerr := ledger.Load(context.Background(), tk.Key)
	if lerr != nil {
		t.Fatalf("Load: %v", lerr)
	}
	if rec.Ticket.State != dirtyState {
		t.Fatalf("被拒的回执不得改写状态：%q → %q", dirtyState, rec.Ticket.State)
	}
}

// TestDeskCancelRejectsUnknownState 收回同样必须拒绝（写路径同向）。
func TestDeskCancelRejectsUnknownState(t *testing.T) {
	d, ledger, tk := dirtyLedger(t)
	err := d.Cancel(context.Background(), tk.Key, "proc-a")
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	rec, lerr := ledger.Load(context.Background(), tk.Key)
	if lerr != nil {
		t.Fatalf("Load: %v", lerr)
	}
	if rec.Ticket.State != dirtyState {
		t.Fatalf("被拒的收回不得改写状态：%q → %q", dirtyState, rec.Ticket.State)
	}
}

// TestMemoryLedgerSaveRejectsUnknownState 落库面是最后一道防线：词表外状态
// 不得进库。
//
// 反向验证：去掉 Save 里的 Valid() 闸 ⇒ 本条转红。
func TestMemoryLedgerSaveRejectsUnknownState(t *testing.T) {
	ledger := NewMemoryLedger()
	tk := baseTicket()
	tk.State = dirtyState
	if _, err := ledger.Save(context.Background(), Record{Ticket: tk}); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("落库面必须拒绝词表外状态，实得 %v", err)
	}
}

// TestUnknownStateIsSameFactInBothFaces 同包方向一致性：同一个事实，
// 体检面报 CodeUnknownState、写路径返 ErrUnknownState——同包不得一个
// fail-closed 一个 fail-open。
func TestUnknownStateIsSameFactInBothFaces(t *testing.T) {
	tk := baseTicket()
	tk.State = dirtyState
	tk.Deadline = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Minute)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	// 体检面：认得出词表外状态。
	var found bool
	for _, p := range Check([]Ticket{tk}, now) {
		if p.Code == CodeUnknownState {
			found = true
		}
	}
	if !found {
		t.Fatalf("体检面必须报 %s", CodeUnknownState)
	}

	// 写路径：同一个事实 ⇒ 同一个方向（拒绝写）。
	ledger := NewMemoryLedger()
	seedDirtyRecord(t, ledger, tk)
	d := NewDesk(ledger)
	if _, err := d.Take(context.Background(), tk.Key, "proc-b"); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("写路径必须与体检面同向（拒绝），实得 %v", err)
	}
}

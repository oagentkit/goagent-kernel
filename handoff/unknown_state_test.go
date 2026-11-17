// unknown_state_test.go —— 词表外状态在写路径上的 fail-closed 判据（issue #359）。
//
// # 反向验证
//
// 每条用例都对应一个「去掉闸就转红」的注入点：
//   - 去掉 Desk.take 的 Valid() 闸 ⇒ Take 会铸造令牌并把状态改写成 accepted；
//   - 去掉 Desk.Reply / Cancel 的 Valid() 闸 ⇒ 会返回 ErrLeaseLost / 走到
//     状态改写分支而不是 ErrUnknownState；
//   - 去掉 MemoryLedger.Save 的 Valid() 闸 ⇒ 脏状态可落库，后续用例的
//     直接注入就不再是「唯一入口」，落库面重新 fail-open。
//
// 注入形态刻意走「已合法挂出 → 同代改成词表外状态」这条真实路径：MemoryLedger
// 的内部 map 代表跨版本存储里的存量行，不是绕过契约的测试捷径（新建分支本就
// 拒绝非 offered，故脏数据只可能从同代更新/外部存储进来）。
package handoff

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

const dirtyState State = "mystery"

// newDirtyDesk 摆一条合法挂出后同代改成词表外状态的记录（模拟脏存量数据）。
func newDirtyDesk(t *testing.T) (*Desk, *MemoryLedger, Ticket) {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	m := NewMemoryLedger(WithLedgerClock(clock))
	d := NewDesk(m)

	if _, err := d.Offer(ctx, Ticket{
		Key: "k1", From: "src", To: "dst", ReplyTo: "inbox", TTL: time.Minute,
	}); err != nil {
		t.Fatalf("合法挂出失败: %v", err)
	}

	// 同代改成词表外状态：直接改存储里的行（脏数据 / 跨版本 schema 的真实形态；
	// Save 已有闸，这条路径只能在存储侧发生）。
	m.mu.Lock()
	rec := m.records["k1"]
	rec.Ticket.State = dirtyState
	m.records["k1"] = rec
	m.mu.Unlock()

	loaded, err := m.Load(ctx, "k1")
	if err != nil {
		t.Fatalf("读脏记录失败: %v", err)
	}
	return d, m, loaded.Ticket
}

// TestTakeRejectsUnknownStateWithoutMintingToken 词表外状态不得领取、不得铸令牌、
// 不得改写状态（issue #359 的核心判据）。
func TestTakeRejectsUnknownStateWithoutMintingToken(t *testing.T) {
	ctx := context.Background()
	d, m, _ := newDirtyDesk(t)

	h, err := d.Take(ctx, "k1", "dst")
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("Take 必须报 ErrUnknownState，实得 %v（令牌=%+v）", err, h)
	}
	if h.Token != "" || h.Fence != 0 {
		t.Fatalf("被拒的领取不得铸造令牌/代次，实得 %+v", h)
	}

	after, lerr := m.Load(ctx, "k1")
	if lerr != nil {
		t.Fatalf("读回记录失败: %v", lerr)
	}
	if after.Ticket.State != dirtyState {
		t.Fatalf("状态被改写了：原 %q，现 %q", dirtyState, after.Ticket.State)
	}
	if after.Token != "" {
		t.Fatalf("脏记录上不得出现领取令牌，实得 %q", after.Token)
	}
	if after.Ticket.Holder != "" {
		t.Fatalf("脏记录上不得出现负责人，实得 %q", after.Ticket.Holder)
	}

	// 同包方向一致性：体检面说「未知状态」，写路径也报同一事实。
	problems := Check([]Ticket{after.Ticket}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	found := false
	for _, p := range problems {
		if p.Code == CodeUnknownState {
			found = true
		}
	}
	if !found {
		t.Fatalf("Check 应报 CodeUnknownState，实得 %+v", problems)
	}
}

// TestReplyRejectsUnknownState 回执不得在词表外记录上推进状态。
func TestReplyRejectsUnknownState(t *testing.T) {
	ctx := context.Background()
	d, m, before := newDirtyDesk(t)

	err := d.Reply(ctx, "k1", "any-token", Receipt{Status: ReceiptDone, Summary: "done"})
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("Reply 必须报 ErrUnknownState，实得 %v", err)
	}

	after, lerr := m.Load(ctx, "k1")
	if lerr != nil {
		t.Fatalf("读回记录失败: %v", lerr)
	}
	if after.Ticket.State != dirtyState || after.Ticket.ReceiptAt != before.ReceiptAt {
		t.Fatalf("回执被拒后状态不得变化：before=%+v after=%+v", before, after.Ticket)
	}
	if after.Receipt != nil {
		t.Fatalf("回执被拒后不得落回执索引: %+v", after.Receipt)
	}
}

// TestCancelRejectsUnknownState 收回不得在词表外记录上推进状态。
func TestCancelRejectsUnknownState(t *testing.T) {
	ctx := context.Background()
	d, m, before := newDirtyDesk(t)

	err := d.Cancel(ctx, "k1", "src")
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("Cancel 必须报 ErrUnknownState，实得 %v", err)
	}

	after, lerr := m.Load(ctx, "k1")
	if lerr != nil {
		t.Fatalf("读回记录失败: %v", lerr)
	}
	if after.Ticket.State != dirtyState || after.Ticket.Holder != before.Holder {
		t.Fatalf("收回被拒后状态不得变化：before=%+v after=%+v", before, after.Ticket)
	}
}

// TestOfferRejectsUnknownState 词表外存量记录不得被当作「重放」原样交回。
func TestOfferRejectsUnknownState(t *testing.T) {
	ctx := context.Background()
	d, m, _ := newDirtyDesk(t)

	_, err := d.Offer(ctx, Ticket{Key: "k1", From: "src", To: "dst", ReplyTo: "inbox", TTL: time.Minute})
	if !errors.Is(err, ErrUnknownState) {
		t.Fatalf("Offer 必须报 ErrUnknownState（脏单既不是重放也不是可开新代），实得 %v", err)
	}

	after, lerr := m.Load(ctx, "k1")
	if lerr != nil {
		t.Fatalf("读回记录失败: %v", lerr)
	}
	if after.Ticket.State != dirtyState {
		t.Fatalf("被拒的挂出不得改写状态，实得 %q", after.Ticket.State)
	}
}

// TestMemoryLedgerSaveRejectsUnknownState 落库面是最后一道防线：词表外状态不得入库。
func TestMemoryLedgerSaveRejectsUnknownState(t *testing.T) {
	ctx := context.Background()
	base := Ticket{Key: "k1", From: "src", To: "dst", ReplyTo: "inbox", TTL: time.Minute}

	t.Run("新建", func(t *testing.T) {
		m := NewMemoryLedger()
		rec := Record{Ticket: base}
		rec.Ticket.State = dirtyState
		if _, err := m.Save(ctx, rec); !errors.Is(err, ErrUnknownState) {
			t.Fatalf("新建词表外状态必须报 ErrUnknownState，实得 %v", err)
		}
		if _, err := m.Load(ctx, "k1"); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("被拒的记录不得落库，实得 %v", err)
		}
	})

	t.Run("同代更新", func(t *testing.T) {
		_, m, _ := newDirtyDesk(t)
		rec, err := m.Load(ctx, "k1")
		if err != nil {
			t.Fatalf("读脏记录失败: %v", err)
		}
		if _, err := m.Save(ctx, rec); !errors.Is(err, ErrUnknownState) {
			t.Fatalf("同代更新词表外状态必须报 ErrUnknownState，实得 %v", err)
		}
		after, lerr := m.Load(ctx, "k1")
		if lerr != nil {
			t.Fatalf("读回记录失败: %v", lerr)
		}
		if after.Ticket.State != dirtyState {
			t.Fatalf("被拒的写入不得改动记录，实得 %q", after.Ticket.State)
		}
	})
}

// desk_settle.go —— Desk 的**收场面**：交回执 / 收回 / 快照 / 取回执。
//
// 从 desk.go 拆出以守住文件行数阈值（判据是单文件 400 行）。
//
// 划界按**职责**：desk.go 留「装配 + 挂出 + 领取」这半边（谁把单挂出去、
// 谁来领），本文件留「这单怎么收场」那半边（凭令牌交回执、源方收回、
// 一致快照、按地址取回执）。两半共用一个 Desk 与一个 Ledger，边界只在
// 「这一笔交接走到哪一步」，不在数据面。
package handoff

import (
	"cmp"
	"context"
	"slices"

	"github.com/oekit/pkg/errors"
)

// Reply 交回执（凭领取令牌；迟到与令牌不匹配都拒收；语义见 Store.Reply）。
func (d *Desk) Reply(ctx context.Context, key Key, token string, r Receipt) error {
	if d.ledger == nil {
		return errors.New("handoff: 未装配持久化存储，无法交回执")
	}
	if err := r.Validate(); err != nil {
		return errors.New("handoff: " + err.Error())
	}
	for range casRetries {
		now, err := d.nowAt(ctx)
		if err != nil {
			return err
		}
		rec, err := d.ledger.Load(ctx, key)
		if err != nil {
			if errors.Is(err, ErrUnknownKey) {
				return ErrUnknownKey
			}
			return errors.Wrap(err, "handoff: 读取交接单失败", "key", string(key))
		}
		// 读不懂就不写（issue #359）：与 Take 同一道闸，方向一致。
		if err := requireKnownState(rec, key, "写回执"); err != nil {
			return err
		}
		if rec.Ticket.State.Terminal() {
			return ErrAlreadySettled
		}
		if rec.Ticket.State != StateAccepted || rec.Token != token {
			return ErrLeaseLost
		}
		if now.After(rec.Ticket.Deadline) {
			expired := rec
			expired.Ticket.State = StateExpired // 责任期已过：回执迟到，单落 expired
			if _, serr := d.ledger.Save(ctx, expired); errors.Is(serr, ErrConflict) {
				continue
			} else if serr != nil {
				return serr
			}
			return ErrExpired
		}
		next := rec
		next.Ticket.State = StateClosed
		next.Ticket.ReceiptAt = now
		delivery := AddressedReceipt{
			Key:        key,
			Generation: uint64(rec.Epoch),
			Address:    rec.Ticket.ReplyTo,
			From:       rec.Ticket.From,
			To:         rec.Ticket.Holder,
			Receipt:    r,
			ClosedAt:   now,
		}
		next.Receipt = &delivery
		if _, serr := d.ledger.Save(ctx, next); errors.Is(serr, ErrConflict) {
			continue
		} else if serr != nil {
			return serr
		}
		return nil
	}
	return errors.Wrap(ErrConflict, "handoff: 交回执的并发重试已耗尽", "key", string(key))
}

// Cancel 源方收回（仅 StateOffered；语义见 Store.Cancel）。
func (d *Desk) Cancel(ctx context.Context, key Key, by Agent) error {
	if d.ledger == nil {
		return errors.New("handoff: 未装配持久化存储，无法收回交接单")
	}
	for range casRetries {
		rec, err := d.ledger.Load(ctx, key)
		if err != nil {
			if errors.Is(err, ErrUnknownKey) {
				return ErrUnknownKey
			}
			return errors.Wrap(err, "handoff: 读取交接单失败", "key", string(key))
		}
		// 读不懂就不写（issue #359）：与 Take 同一道闸，方向一致。
		if err := requireKnownState(rec, key, "收回"); err != nil {
			return err
		}
		if rec.Ticket.State.Terminal() {
			return ErrAlreadySettled
		}
		if rec.Ticket.State == StateAccepted {
			return ErrTaken
		}
		if by != rec.Ticket.From {
			return ErrNotSource
		}
		next := rec
		next.Ticket.State = StateWithdrawn
		next.Ticket.Holder = ""
		if _, serr := d.ledger.Save(ctx, next); errors.Is(serr, ErrConflict) {
			continue
		} else if serr != nil {
			return serr
		}
		return nil
	}
	return errors.Wrap(ErrConflict, "handoff: 收回交接单的并发重试已耗尽", "key", string(key))
}

// Snapshot 当前代的全部交接单（按 Key 升序，确定性；语义见 Store.Snapshot）。
func (d *Desk) Snapshot(ctx context.Context) ([]Ticket, error) {
	if d.ledger == nil {
		return nil, errors.New("handoff: 未装配持久化存储，无法读取快照")
	}
	recs, err := d.ledger.List(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "handoff: 读取交接单快照失败")
	}
	out := make([]Ticket, 0, len(recs))
	for _, rec := range recs {
		t := rec.Ticket
		t.Payload = append([]byte(nil), rec.Ticket.Payload...)
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Ticket) int { return cmp.Compare(a.Key, b.Key) })
	return out, nil
}

// Deliveries 取回发往 addr 的回执（回执寻址的取件半边）。
//
// addr 是挂出时写的 Ticket.ReplyTo 原样值；不解析、不做前缀匹配。回执在
// 交接单被 Delete 后仍然留存（源方崩溃重启后仍能取回）。
func (d *Desk) Deliveries(ctx context.Context, addr string) ([]AddressedReceipt, error) {
	if d.ledger == nil {
		return nil, errors.New("handoff: 未装配持久化存储，无法取回执")
	}
	out, err := d.ledger.Receipts(ctx, addr)
	if err != nil {
		return nil, errors.Wrap(err, "handoff: 取回执失败", "address", addr)
	}
	return out, nil
}

// 编译期断言：Desk 实现语义 Store。
var _ Store = (*Desk)(nil)

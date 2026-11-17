// Package handoff 的 Offer / 代次生成面（从 desk.go 拆出，结构阈值 400 行）。

package handoff

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"
)

// Offer 挂出交接单（原子 + 幂等；语义见 Store.Offer）。
//
// 同一 Key 未终态 ⇒ 原样返回（重放）；终态 ⇒ 开新一代（代次由 Ledger 铸，
// 跨 Delete 不复用）；否则首代。Deadline 按存储时钟从 TTL 推算。
func (d *Desk) Offer(ctx context.Context, t Ticket) (Ticket, error) {
	if d.ledger == nil {
		return Ticket{}, errors.New("handoff: 未装配持久化存储，无法挂出交接单")
	}
	if err := t.Validate(); err != nil {
		return Ticket{}, errors.New("handoff: " + err.Error())
	}
	for range casRetries {
		now, err := d.nowAt(ctx)
		if err != nil {
			return Ticket{}, err
		}
		cur, err := d.ledger.Load(ctx, t.Key)
		switch {
		case err == nil:
			// 词表外状态既不是「重放」，也不是「可开新代」：内核读不懂这条
			// 记录时不能把脏单原样交回（issue #359）。
			if !cur.Ticket.State.Valid() {
				return Ticket{}, errors.Wrap(ErrUnknownState,
					"交接单状态不在词表内，拒绝挂出", "key", string(t.Key), "state", string(cur.Ticket.State))
			}
			if !cur.Ticket.State.Terminal() {
				return cur.Ticket, nil // 重放：同一 Key 的重复挂出不开新单
			}
			fresh := beginGeneration(t, now, cur.Epoch+1)
			saved, serr := d.ledger.Save(ctx, Record{Ticket: fresh, Epoch: cur.Epoch + 1})
			if errors.Is(serr, ErrConflict) {
				continue // 并发写者已改：重读后重新判
			}
			if serr != nil {
				return Ticket{}, serr
			}
			return saved.Ticket, nil
		case errors.Is(err, ErrUnknownKey):
			// 记录不存在（含 Delete 之后）：代次由 Ledger 按水位铸造。
			fresh := beginGeneration(t, now, 0)
			saved, serr := d.ledger.Save(ctx, Record{Ticket: fresh})
			if errors.Is(serr, ErrConflict) || errors.Is(serr, ErrStaleEpoch) {
				// 可能是并发首挂（另一方先落），也可能是旧代声明。
				// 旧代声明不重试：重试也不会变合法。
				if errors.Is(serr, ErrStaleEpoch) {
					return Ticket{}, serr
				}
				continue
			}
			if serr != nil {
				return Ticket{}, serr
			}
			return saved.Ticket, nil
		default:
			return Ticket{}, errors.Wrap(err, "handoff: 读取交接单失败", "key", string(t.Key))
		}
	}
	return Ticket{}, errors.Wrap(ErrConflict, "handoff: 挂出交接单的并发重试已耗尽", "key", string(t.Key))
}

// beginGeneration 把调用方给的单子收成「新一代待领取」形态。
func beginGeneration(t Ticket, now time.Time, epoch Epoch) Ticket {
	fresh := t
	if epoch == 0 {
		fresh.Generation = 0 // 由 Ledger 铸造
	} else {
		fresh.Generation = uint64(epoch)
	}
	fresh.Deadline = now.Add(t.TTL)
	fresh.State = StateOffered
	fresh.Holder = ""
	fresh.ReceiptAt = time.Time{}
	return fresh
}

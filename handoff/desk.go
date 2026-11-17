// desk.go —— 语义面的**可持久化实现**：Desk 只依赖 Ledger，不持有进程内状态。
//
// 两个 Desk 共用一个 Ledger 就是「两个进程客户端」的形状：Offer / Take /
// Reply / Cancel 全部走「Load → 判 → Save（条件写）」，并发冲突靠 Revision
// 重试而不是进程内互斥。LocalDesk 是 Desk + MemoryLedger 的薄包装，行为与
// 旧版逐条一致（默认路径零回归）。
//
// 构造器按部署形态分流（issue #270）：NewDesk 是单机/测试语义（允许进程
// 本地时钟回退）；NewDistributedDesk 要求 Ledger 实现 ClockedLedger，缺
// 存储时钟即装配期失败——跨副本判定不得用「本进程以为的现在」。
package handoff

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/oekit/pkg/errors"
)

// casRetries 条件写冲突后的重新判定上限。
//
// 冲突不是失败：并发 Take 的输家必须重新 Load 读到 accepted 才能返回
// ErrTaken。上限只防「存储一直报冲突」的活锁，不把冲突悄悄吞掉。
const casRetries = 16

// Desk 交接台：语义 Store 的持久化实现（每个方法经 Ledger 的条件写完成）。
//
// 零值不可用，请用 NewDesk。Desk 自身无共享可变状态，可并发使用；
// 多个 Desk 实例共用一个 Ledger 即模拟多个进程客户端。
type Desk struct {
	ledger Ledger
	now    func() time.Time
}

// DeskOption 交接台的装配项。
type DeskOption func(*Desk)

// WithClock 注入时钟（缺省读 Ledger 自己的时钟，见 ClockedLedger）。
//
// 注意：跨进程部署时**不要**用进程本地时钟代替存储时钟——截止时刻是
// 存储侧的事实。注入只用于确定性测试；生产路径应实现 ClockedLedger。
func WithClock(clock func() time.Time) DeskOption {
	return func(d *Desk) {
		if clock != nil {
			d.now = clock
			if u, ok := d.ledger.(interface{ UseClock(func() time.Time) }); ok {
				u.UseClock(clock)
			}
		}
	}
}

// NewDesk 构造持久化交接台，语义是**单机/本地**：Ledger 未实现
// ClockedLedger 时回退到进程本地时钟（time.Now 或 WithClock）。
//
// 这个回退只在单进程内成立。跨进程/跨副本部署必须用 NewDistributedDesk：
// 它要求存储时钟（ClockedLedger），缺失即装配期失败——两个副本的时钟偏差
// 会直接变成「谁以为交接过期了」的差异（issue #270）。ledger 为 nil 时
// 各方法显式报错，不静默成功。
func NewDesk(ledger Ledger, opts ...DeskOption) *Desk {
	d := &Desk{ledger: ledger, now: time.Now}
	for _, o := range opts {
		o(d)
	}
	return d
}

// ErrNoStoreClock 分布式交接台缺存储时钟（Ledger 未实现 ClockedLedger）。
//
// 装配期错误而不是运行期失真：缺存储时钟时 Desk 只能退回进程本地时间，
// 多副本的截止/过期判定各按各的钟走——先过期的一方回收、后过期的一方
// 还在执行。fail-closed 在构造点（issue #270）。
var ErrNoStoreClock = errors.New("handoff: 分布式交接台需要存储时钟（Ledger 必须实现 ClockedLedger）")

// NewDistributedDesk 构造跨进程/跨副本交接台：**强制要求** Ledger 实现
// ClockedLedger（存储侧时钟）。缺失 ⇒ 构造失败，不留到运行期用进程本地
// 时钟静默失真。WithClock 仍可用于确定性测试（同时转写 MemoryLedger 的
// 存储时钟），但它不能替代 ClockedLedger 这条装配判据。
func NewDistributedDesk(ledger Ledger, opts ...DeskOption) (*Desk, error) {
	if ledger == nil {
		return nil, errors.Wrap(ErrNoStoreClock, "handoff: 分布式交接台缺 Ledger")
	}
	if _, ok := ledger.(ClockedLedger); !ok {
		return nil, errors.Wrap(ErrNoStoreClock,
			"handoff: Ledger 未实现 ClockedLedger——跨进程截止/过期判定必须用存储时钟，不得回退进程本地时钟")
	}
	return NewDesk(ledger, opts...), nil
}

// nowAt 判定用当前时刻：优先存储时钟（ClockedLedger），否则用注入时钟。
func (d *Desk) nowAt(ctx context.Context) (time.Time, error) {
	if cl, ok := d.ledger.(ClockedLedger); ok {
		t, err := cl.Now(ctx)
		if err != nil {
			return time.Time{}, errors.Wrap(err, "handoff: 读存储时钟失败")
		}
		return t, nil
	}
	return d.now(), nil
}

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

// Take 领取交接单（语义见 Store.Take）。
func (d *Desk) Take(ctx context.Context, key Key, to Agent) (Handle, error) {
	return d.take(ctx, key, to, 0)
}

// TakeAt 领取并校验所有权代次：expected 与当前记录不一致 ⇒ ErrStaleEpoch。
//
// 这是「Take 侧校验代次单调」的入口：一个拿着旧代交接单的客户端（源方
// 重交接之后、或 Delete 重建之后）不能凭旧代领取新一单的工作。
// expected 为 0 一律拒绝——0 不是一次真实挂出的代次。
func (d *Desk) TakeAt(ctx context.Context, key Key, to Agent, expected Epoch) (Handle, error) {
	if expected == 0 {
		return Handle{}, errors.Wrap(ErrStaleEpoch, "领取校验需要当前所有权代次（0 不是真实代次）", "key", string(key))
	}
	return d.take(ctx, key, to, expected)
}

func (d *Desk) take(ctx context.Context, key Key, to Agent, expected Epoch) (Handle, error) {
	if d.ledger == nil {
		return Handle{}, errors.New("handoff: 未装配持久化存储，无法领取交接单")
	}
	for range casRetries {
		now, err := d.nowAt(ctx)
		if err != nil {
			return Handle{}, err
		}
		rec, err := d.ledger.Load(ctx, key)
		if err != nil {
			if errors.Is(err, ErrUnknownKey) {
				return Handle{}, ErrUnknownKey
			}
			return Handle{}, errors.Wrap(err, "handoff: 读取交接单失败", "key", string(key))
		}
		// 词表外状态是「内核读不懂」，不是「待领取」：Valid() 必须在任何
		// 「已领取 / 终态」判定之前消费，否则 mystery 两条都不命中，会走到
		// 下面的 StateAccepted 改写并铸造令牌（issue #359）。
		if !rec.Ticket.State.Valid() {
			return Handle{}, errors.Wrap(ErrUnknownState,
				"交接单状态不在词表内，拒绝领取", "key", string(key), "state", string(rec.Ticket.State))
		}
		if expected != 0 && rec.Epoch != expected {
			return Handle{}, errors.Wrap(ErrStaleEpoch, "领取者出示的代次不是当前代",
				"key", string(key), "epoch", expected, "current", rec.Epoch)
		}
		if rec.Ticket.State == StateAccepted {
			return Handle{}, ErrTaken
		}
		if rec.Ticket.State.Terminal() {
			return Handle{}, ErrAlreadySettled
		}
		if to != rec.Ticket.To {
			return Handle{}, ErrNotAddressee
		}
		if now.After(rec.Ticket.Deadline) {
			expired := rec
			expired.Ticket.State = StateExpired // 懒式过期：读到即落账
			if _, serr := d.ledger.Save(ctx, expired); errors.Is(serr, ErrConflict) {
				continue
			} else if serr != nil {
				return Handle{}, serr
			}
			return Handle{}, ErrExpired
		}
		next := rec
		next.Ticket.State = StateAccepted
		next.Ticket.Holder = to
		next.Token = "" // 由 Ledger 在本次原子写里铸造
		saved, serr := d.ledger.Save(ctx, next)
		if errors.Is(serr, ErrConflict) {
			continue // 输家重新读取：会读到 accepted ⇒ ErrTaken
		}
		if serr != nil {
			return Handle{}, serr
		}
		return Handle{Token: saved.Token, Fence: uint64(saved.Epoch), Until: saved.Ticket.Deadline}, nil
	}
	return Handle{}, errors.Wrap(ErrConflict, "handoff: 领取交接单的并发重试已耗尽", "key", string(key))
}

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
		if !rec.Ticket.State.Valid() {
			return errors.Wrap(ErrUnknownState,
				"交接单状态不在词表内，拒绝回执", "key", string(key), "state", string(rec.Ticket.State))
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
		if !rec.Ticket.State.Valid() {
			return errors.Wrap(ErrUnknownState,
				"交接单状态不在词表内，拒绝收回", "key", string(key), "state", string(rec.Ticket.State))
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

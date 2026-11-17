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
	"context"
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
//
// 接缝契约（宿主注入的时钟回调；内核在过期判定里调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：**内核侧兑现**。Desk.nowAt 直接调用该回调，调用点**不持任何
// 内核锁**（Desk 无共享可变状态）——回调里重入本包不会自锁；但不得等待
// 正在进行的交接判定完成。
// no-blocking：**宿主侧兑现**。回调在过期判定路径上被**高频**同步调用
// （每次重判都重取时刻），必须廉价有界：不得等锁、不得做长耗时 I/O。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本回调的 panic（本包无
// recover），宿主实现须自行保证不 panic 或在实现内部 recover 成 error。
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
		// 读不懂就不铸令牌、不改写状态（issue #359）：内核不得在自己读不懂
		// 的记录上发放执行权。词表外状态若被当成「非终态」放行，接下来的
		// 状态改写会把原始的词表外状态抹掉——事后无法区分「这单本来就是
		// 词表外状态」与「正常领取」。
		if err := requireKnownState(rec, key, "领取"); err != nil {
			return Handle{}, err
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

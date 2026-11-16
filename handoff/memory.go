// memory.go —— Ledger 的进程内参考实现（从 ledger.go 拆出：结构阈值按判据
// 的一侧切——契约面与参考实现分开，各自可读）。
package handoff

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"
)

// MemoryLedger Ledger 的进程内参考实现。
//
// 边界与 LocalDesk 的注释同源：它证明契约**可满足**，不承诺跨进程原子性。
// 跨进程落库形态由宿主实现（表结构、事务与时钟域归宿主；参考 examples/pgstore
// 对 taskboard / sideeffect 的分工）。
type MemoryLedger struct {
	mu    sync.Mutex
	clock func() time.Time

	records  map[Key]Record
	epochs   map[Key]Epoch  // 代次水位（Delete 不清）
	revs     map[Key]uint64 // 版本水位（Delete 不清）
	receipts map[string][]AddressedReceipt
}

// LedgerOption MemoryLedger 的装配项。
type LedgerOption func(*MemoryLedger)

// WithLedgerClock 注入 Ledger 自己的时钟（存储侧时钟语义见 ClockedLedger）。
func WithLedgerClock(clock func() time.Time) LedgerOption {
	return func(m *MemoryLedger) {
		if clock != nil {
			m.clock = clock
		}
	}
}

// NewMemoryLedger 构造内存参考实现。
func NewMemoryLedger(opts ...LedgerOption) *MemoryLedger {
	m := &MemoryLedger{
		clock:    time.Now,
		records:  map[Key]Record{},
		epochs:   map[Key]Epoch{},
		revs:     map[Key]uint64{},
		receipts: map[string][]AddressedReceipt{},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// UseClock 注入时钟（与 LocalDesk 的 WithClock 同一形态：过期场景可确定性测）。
func (m *MemoryLedger) UseClock(clock func() time.Time) {
	if clock == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = clock
}

// Now 存储侧当前时刻（ClockedLedger 的实现；Desk 用它推算截止时刻与过期）。
func (m *MemoryLedger) Now(context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clock(), nil
}

// Save 条件写入（语义见 Ledger.Save 的三形态）。
func (m *MemoryLedger) Save(_ context.Context, rec Record) (Record, error) {
	if err := rec.Ticket.Validate(); err != nil {
		return Record{}, errors.New("handoff: " + err.Error())
	}
	if rec.Receipt != nil {
		if err := rec.Receipt.Receipt.Validate(); err != nil {
			return Record{}, errors.New("handoff: " + err.Error())
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := rec.Ticket.Key
	cur, exists := m.records[key]
	watermark := m.epochs[key]

	switch {
	case !exists && rec.Ticket.State.Terminal():
		return Record{}, errors.New("handoff: 新建记录必须是待领取态（终态只能由一次真实交接产生）")
	case !exists:
		if rec.Revision != 0 {
			return Record{}, errors.Wrap(ErrConflict, "新建记录的版本必须为 0", "key", string(key))
		}
		if rec.Ticket.State != StateOffered {
			return Record{}, errors.New("handoff: 新建记录必须是待领取态")
		}
		switch {
		case rec.Epoch == 0:
			rec.Epoch = watermark + 1
		case rec.Epoch <= watermark:
			return Record{}, errors.Wrap(ErrStaleEpoch, "旧代不得重建交接单", "key", string(key), "epoch", rec.Epoch, "watermark", watermark)
		case rec.Epoch != watermark+1:
			return Record{}, errors.Wrap(ErrStaleEpoch, "代次必须紧接水位（不接受跨越式声明）", "key", string(key), "epoch", rec.Epoch, "watermark", watermark)
		}
	default:
		// 已存在：同代更新，或终态之上开新代。
		if rec.Epoch == 0 {
			return Record{}, errors.Wrap(ErrConflict, "已存在的记录必须带当前代次", "key", string(key))
		}
		if rec.Epoch == cur.Epoch {
			if rec.Revision != cur.Revision {
				return Record{}, errors.Wrap(ErrConflict, "记录已被并发写者修改", "key", string(key), "revision", rec.Revision, "current", cur.Revision)
			}
			// 同代更新不允许把状态改回待领取：那是新代的事。
			if rec.Ticket.State == StateOffered && cur.Ticket.State != StateOffered {
				return Record{}, errors.Wrap(ErrStaleEpoch, "同代不得回退到待领取（重新挂出必须开新代）", "key", string(key))
			}
		} else if rec.Epoch == cur.Epoch+1 && rec.Epoch > watermark {
			if !cur.Ticket.State.Terminal() {
				return Record{}, errors.Wrap(ErrStaleEpoch, "非终态不得开新代", "key", string(key), "state", cur.Ticket.State)
			}
			if rec.Revision != 0 || rec.Ticket.State != StateOffered {
				return Record{}, errors.Wrap(ErrConflict, "新代必须从初始版本、待领取态开始", "key", string(key))
			}
		} else {
			return Record{}, errors.Wrap(ErrStaleEpoch, "代次不是当前代也不是下一代", "key", string(key), "epoch", rec.Epoch, "current", cur.Epoch)
		}
	}

	rec.Ticket.Generation = uint64(rec.Epoch)
	rec.Revision = m.revs[key] + 1
	rec = normalizeRecord(rec)
	m.records[key] = cloneRecord(rec)
	m.epochs[key] = rec.Epoch
	m.revs[key] = rec.Revision
	if rec.Receipt != nil {
		m.indexReceiptLocked(*rec.Receipt)
	}
	return cloneRecord(rec), nil
}

// Load 读当前记录（无记录 → ErrUnknownKey）。
func (m *MemoryLedger) Load(_ context.Context, key Key) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[key]
	if !ok {
		return Record{}, ErrUnknownKey
	}
	return cloneRecord(rec), nil
}

// Delete 删除当前记录（代次与版本水位保留）。
func (m *MemoryLedger) Delete(_ context.Context, key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[key]; !ok {
		return ErrUnknownKey
	}
	delete(m.records, key)
	return nil
}

// List 全部当前记录（按 Key 升序，确定性）。
func (m *MemoryLedger) List(_ context.Context) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Record, 0, len(m.records))
	for _, rec := range m.records {
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticket.Key < out[j].Ticket.Key })
	return out, nil
}

// Receipts 按地址取回执（按 Key、代次升序）。
func (m *MemoryLedger) Receipts(_ context.Context, addr string) ([]AddressedReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.receipts[addr]
	out := make([]AddressedReceipt, 0, len(src))
	for _, r := range src {
		out = append(out, cloneReceipt(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Generation < out[j].Generation
	})
	return out, nil
}

// Epoch 返回代次水位（未知 Key → 0）。
func (m *MemoryLedger) Epoch(_ context.Context, key Key) (Epoch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.epochs[key], nil
}

// indexReceiptLocked 把回执按地址索引（同一 Key+代次只留一条）。
func (m *MemoryLedger) indexReceiptLocked(r AddressedReceipt) {
	for _, have := range m.receipts[r.Address] {
		if have.Key == r.Key && have.Generation == r.Generation {
			return
		}
	}
	m.receipts[r.Address] = append(m.receipts[r.Address], cloneReceipt(r))
}

// normalizeRecord 把「状态决定的可空字段」收成正交形态（与 LocalDesk 的落点一致）。
//
// 注意：closed 的 Holder 刻意保留（现有 LocalDesk 的语义：回执收口后仍能读出
// 「谁做的」；Check 对它的判断见 judge.go，不在本函数里改写状态）。
func normalizeRecord(rec Record) Record {
	switch rec.Ticket.State {
	case StateAccepted:
		if rec.Token == "" {
			rec.Token = mintToken()
		}
	case StateOffered:
		rec.Ticket.Holder = ""
		rec.Ticket.ReceiptAt = time.Time{}
		rec.Token = ""
	default:
		rec.Token = ""
	}
	if rec.Ticket.State != StateClosed {
		rec.Receipt = nil
	}
	return rec
}

// mintToken 铸造领取令牌（128 位随机；跨进程不复用不重合）。
func mintToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 凭据铸造失败时宁可 panic 也不发一份可预测的令牌（与 sideeffect.MintHolder
		// 同一条纪律：凭据由判据面铸造，铸造失败不能被降级成「用了个弱凭据」）。
		panic(fmt.Sprintf("handoff: 铸造领取令牌失败: %v", err))
	}
	return "take-" + hex.EncodeToString(b[:])
}

// cloneRecord 深拷贝一条记录（Payload / 回执产出物不共享底层数组）。
func cloneRecord(rec Record) Record {
	out := rec
	out.Ticket.Payload = append([]byte(nil), rec.Ticket.Payload...)
	if rec.Receipt != nil {
		r := cloneReceipt(*rec.Receipt)
		out.Receipt = &r
	}
	return out
}

// cloneReceipt 深拷贝一条回执。
func cloneReceipt(r AddressedReceipt) AddressedReceipt {
	out := r
	out.Receipt.Artifacts = append([]string(nil), r.Receipt.Artifacts...)
	return out
}

// 编译期断言：内存参考实现满足持久化契约。
var _ Ledger = (*MemoryLedger)(nil)

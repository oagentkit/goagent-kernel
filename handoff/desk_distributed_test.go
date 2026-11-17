// desk_distributed_test.go —— 分布式交接台的装配判据与四类交接语义
// （issue #270）：分区 / 旧代迟到 / 超时 / 恢复。
package handoff

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	kerrors "github.com/oekit/pkg/errors"
)

// ledgerOnly 只暴露 Ledger 面的包装：**不**实现 ClockedLedger。
type ledgerOnly struct{ inner Ledger }

func (l ledgerOnly) Load(ctx context.Context, k Key) (Record, error) { return l.inner.Load(ctx, k) }
func (l ledgerOnly) Save(ctx context.Context, r Record) (Record, error) {
	return l.inner.Save(ctx, r)
}
func (l ledgerOnly) Delete(ctx context.Context, k Key) error    { return l.inner.Delete(ctx, k) }
func (l ledgerOnly) List(ctx context.Context) ([]Record, error) { return l.inner.List(ctx) }
func (l ledgerOnly) Receipts(ctx context.Context, addr string) ([]AddressedReceipt, error) {
	return l.inner.Receipts(ctx, addr)
}
func (l ledgerOnly) Epoch(ctx context.Context, k Key) (Epoch, error) { return l.inner.Epoch(ctx, k) }

// flakyLedger 可注入分区的 Ledger（读写失败；句柄/回执面仍走底层）。
type flakyLedger struct {
	Ledger
	down atomic.Bool
}

var errPartitioned = kerrors.New("handoff: 网络分区（替身）")

func (l *flakyLedger) Load(ctx context.Context, k Key) (Record, error) {
	if l.down.Load() {
		return Record{}, errPartitioned
	}
	return l.Ledger.Load(ctx, k)
}

func (l *flakyLedger) Save(ctx context.Context, r Record) (Record, error) {
	if l.down.Load() {
		return Record{}, errPartitioned
	}
	return l.Ledger.Save(ctx, r)
}

func (l *flakyLedger) Now(ctx context.Context) (time.Time, error) {
	return l.Ledger.(ClockedLedger).Now(ctx)
}

// TestNewDistributedDeskRequiresStoreClock 构造期分流（issue #270）：
// 缺 ClockedLedger 的 Ledger 必须装配失败；单机 NewDesk / NewLocalDesk
// 仍可用进程本地时钟。
func TestNewDistributedDeskRequiresStoreClock(t *testing.T) {
	plain := ledgerOnly{inner: NewMemoryLedger()}
	if _, err := NewDistributedDesk(plain); !kerrors.Is(err, ErrNoStoreClock) {
		t.Fatalf("Ledger 未实现 ClockedLedger 时 NewDistributedDesk 必须构造失败, got %v", err)
	}
	if _, err := NewDistributedDesk(nil); !kerrors.Is(err, ErrNoStoreClock) {
		t.Fatalf("nil Ledger 必须构造失败, got %v", err)
	}
	if _, err := NewDistributedDesk(NewMemoryLedger()); err != nil {
		t.Fatalf("实现 ClockedLedger 的 Ledger 应装配成功: %v", err)
	}

	ctx := context.Background()
	// 单机语义不回退：NewDesk 允许进程本地时钟回退。
	local := NewDesk(plain)
	if _, err := local.Offer(ctx, baseTicket()); err != nil {
		t.Fatalf("NewDesk 的本地语义必须可用: %v", err)
	}
	if _, err := NewLocalDesk().Offer(ctx, baseTicket()); err != nil {
		t.Fatalf("NewLocalDesk 必须可用: %v", err)
	}
}

// TestDeskDistributedSemantics 四类交接语义在同一份 Ledger 客户端上跑通。
func TestDeskDistributedSemantics(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	ledger := NewMemoryLedger(WithLedgerClock(clock.Now))
	source, err := NewDistributedDesk(ledger)
	if err != nil {
		t.Fatal(err)
	}
	flaky := &flakyLedger{Ledger: ledger}
	target, err := NewDistributedDesk(flaky)
	if err != nil {
		t.Fatal(err)
	}

	// ── 分区：目标方读不到存储时不得留下半条状态；恢复后可继续 ──
	offered, err := source.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatal(err)
	}
	flaky.down.Store(true)
	if _, err := target.Take(ctx, offered.Key, "proc-b"); !kerrors.Is(err, errPartitioned) {
		t.Fatalf("分区期间 Take 应如实失败, got %v", err)
	}
	flaky.down.Store(false)
	snap, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 || snap[0].State != StateOffered {
		t.Fatalf("分区失败不得改写交接单状态: %+v", snap)
	}
	h1, err := target.Take(ctx, offered.Key, "proc-b")
	if err != nil {
		t.Fatalf("分区恢复后 Take 应成功: %v", err)
	}

	// ── 超时：责任期过后回执迟到 ⇒ 单落 expired，回执被拒 ──
	clock.Advance(2 * time.Minute) // baseTicket TTL = 1min
	if err := source.Reply(ctx, offered.Key, h1.Token, Receipt{Status: ReceiptCompleted, Summary: "ok"}); !kerrors.Is(err, ErrExpired) {
		t.Fatalf("超时后的回执必须 ErrExpired, got %v", err)
	}
	snap, _ = source.Snapshot(ctx)
	if len(snap) != 1 || snap[0].State != StateExpired {
		t.Fatalf("超时后交接单应落 expired: %+v", snap)
	}

	// ── 恢复：过期后重新挂出开新一代；旧代令牌迟到回执被拒 ──
	offered2, err := source.Offer(ctx, baseTicket())
	if err != nil {
		t.Fatalf("过期后重新挂出: %v", err)
	}
	if offered2.Generation != offered.Generation+1 {
		t.Fatalf("恢复必须开新一代（%d → %d）", offered.Generation, offered2.Generation)
	}
	h2, err := target.Take(ctx, offered2.Key, "proc-b")
	if err != nil {
		t.Fatalf("新一代领取: %v", err)
	}

	// ── 旧代迟到：gen1 的令牌不得关掉 gen2 的单 ──
	if err := source.Reply(ctx, offered2.Key, h1.Token, Receipt{Status: ReceiptCompleted, Summary: "late"}); !kerrors.Is(err, ErrLeaseLost) {
		t.Fatalf("旧代迟到回执必须 ErrLeaseLost, got %v", err)
	}
	if err := source.Reply(ctx, offered2.Key, h2.Token, Receipt{Status: ReceiptCompleted, Summary: "ok"}); err != nil {
		t.Fatalf("新持有者的回执应成功（旧代拒绝不得波及新一代）: %v", err)
	}
	got, err := source.Deliveries(ctx, baseTicket().ReplyTo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Generation != offered2.Generation {
		t.Fatalf("回执应只记新一代一次: %+v", got)
	}
}

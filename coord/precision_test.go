// precision_test.go —— issue #271：「读时刻—提交」窗口的误差预算与后端原子判定。
//
// 判据分两半，成对存在（只测「原子路径能过」会让「非原子路径静默接受
// 窗口内过期」永远没有信号）：
//
//  1. 原子后端（实现 AtomicDecision）：取时、快照、判定、提交在同一事务内
//     ⇒ 窗口内过期必须被拒绝，LeasePrecision 报 Atomic/Window=0；
//  2. 非原子后端：同一时序下续期可能已按旧快照提交，但必须留下**可解释的
//     提示**（观测窗口、预算、是否越过到期时刻），LeasePrecision 显式标记
//     Degraded 与预算数值。
//
// 时间源固定为存储时钟（ClockSource）：节点本地时钟只在 SingleMachine
// 声明下成立，且本文件的窗口判定不依赖进程墙钟。
package coord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

var precisionBase = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// windowStorage 非原子存储时钟后端：Txn 前可推进存储时钟，模拟
// 「判定读时刻」与「提交」之间发生的到期。
type windowStorage struct {
	*clockedStorage
	beforeTxn func()
}

func (w *windowStorage) Txn(ctx context.Context, asserts []Assert, mutations []Mutation) (bool, error) {
	if w.beforeTxn != nil {
		w.beforeTxn()
	}
	return w.MemoryBackend.Txn(ctx, asserts, mutations)
}

// atomicStorage 实现 AtomicDecision 的存储时钟后端：DecideTx 在锁内取时、
// 读快照、执行判定、提交——判定时刻与提交不重合。
type atomicStorage struct {
	*clockedStorage
	beforeDecide func()
}

func (a *atomicStorage) DecideTx(ctx context.Context, decide func(now time.Time, read func(keys ...string) (map[string]Entry, error)) ([]Assert, []Mutation, error)) (bool, time.Time, error) {
	if a.beforeDecide != nil {
		a.beforeDecide()
	}
	a.MemoryBackend.mu.Lock()
	defer a.MemoryBackend.mu.Unlock()
	now := a.nowLocked()
	read := func(keys ...string) (map[string]Entry, error) {
		return a.snapshotLocked(keys...), nil
	}
	asserts, mutations, err := decide(now, read)
	if err != nil {
		return false, now, err
	}
	applied := a.txnLocked(asserts, mutations)
	return applied, now, nil
}

// nowLocked 读存储时钟（clockedStorage 的时钟字段用同一把锁保护）。
func (c *clockedStorage) nowLocked() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func newWindowStorage(t *testing.T) *windowStorage {
	t.Helper()
	return &windowStorage{clockedStorage: newClockedStorage(precisionBase)}
}

func newAtomicStorage(t *testing.T) *atomicStorage {
	t.Helper()
	return &atomicStorage{clockedStorage: newClockedStorage(precisionBase)}
}

func claimForRenew(t *testing.T, board *BoardAdapter, ttl time.Duration) taskboard.Task {
	t.Helper()
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	task, err := board.Claim(context.Background(), "t1", "worker-a", taskboard.ClaimRequest{TTL: ttl})
	if err != nil {
		t.Fatalf("认领失败: %v", err)
	}
	return task
}

// TestLeasePrecisionDeclaresBudgetAndSource 误差预算是可查事实，不是散文：
// 原子后端窗口 = 0；非原子后端窗口 = 预算，且显式标记降级；来源按
// 存储时钟 / 节点时钟分档。
func TestLeasePrecisionDeclaresBudgetAndSource(t *testing.T) {
	atomicBoard, err := NewBoardAdapter(newAtomicStorage(t))
	if err != nil {
		t.Fatal(err)
	}
	p := atomicBoard.LeasePrecision()
	if !p.Atomic || p.Degraded {
		t.Fatalf("原子后端必须报 Atomic 且非降级: %+v", p)
	}
	if p.Window != 0 {
		t.Fatalf("原子判定与提交不重合 ⇒ 窗口必须为 0（实得 %v）", p.Window)
	}
	if p.Budget != DefaultLeaseErrorBudget {
		t.Fatalf("预算必须可查（want %v，实得 %v）", DefaultLeaseErrorBudget, p.Budget)
	}
	if p.Source != TimeSourceStorageClock {
		t.Fatalf("来源必须写明为存储时钟（实得 %q）", p.Source)
	}

	degradedBoard, err := NewBoardAdapter(newWindowStorage(t))
	if err != nil {
		t.Fatal(err)
	}
	p = degradedBoard.LeasePrecision()
	if p.Atomic || !p.Degraded {
		t.Fatalf("非原子后端必须显式标记降级: %+v", p)
	}
	if p.Window != DefaultLeaseErrorBudget {
		t.Fatalf("非原子路径的窗口声明必须等于预算（want %v，实得 %v）", DefaultLeaseErrorBudget, p.Window)
	}
	if p.Source != TimeSourceStorageClock {
		t.Fatalf("同一条存储时钟路径，来源必须一致（实得 %q）", p.Source)
	}

	memBoard, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	p = memBoard.LeasePrecision()
	if p.Source != TimeSourceLocalClock {
		t.Fatalf("仅单机声明的后端时间源是节点本地时钟（实得 %q）", p.Source)
	}
}

// TestRenewWindowExpiryAtomicRejected 同一时序（判定后、提交前存储时钟越过
// 到期）在原子路径上必须被拒绝：后端在事务内取时，判定看到的是提交时刻。
func TestRenewWindowExpiryAtomicRejected(t *testing.T) {
	storage := newAtomicStorage(t)
	board, err := NewBoardAdapter(storage)
	if err != nil {
		t.Fatal(err)
	}
	claimed := claimForRenew(t, board, 10*time.Second)
	oldUntil := claimed.Lease.Until

	// 判定发生在事务内；进入事务后存储时钟越过旧到期时刻。
	storage.beforeDecide = func() { storage.advance(11 * time.Second) }

	_, verdict, err := board.RenewWithVerdict(context.Background(), "t1", claimed.Lease.Token,
		taskboard.ClaimRequest{TTL: 10 * time.Second})
	if !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("原子路径必须在窗口内过期时拒绝续期（实得 %v）", err)
	}
	if !verdict.Atomic || verdict.ExpiredInWindow {
		t.Fatalf("被拒绝的原子判定不应报告「窗口内过期」（它根本没提交）: %+v", verdict)
	}

	// 任务停在旧租约上：这次拒绝没有留下任何写入。
	tasks, err := board.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || !tasks[0].Lease.Until.Equal(oldUntil) {
		t.Fatalf("被拒绝的续期不得修改租约（want until=%v，实得 %+v）", oldUntil, tasks)
	}
}

// TestRenewWindowExpiryDegradedWarns 非原子路径的同一时序：续期会按旧快照
// 提交成功，但必须留下可解释提示——窗口、预算、越过到期的事实都在
// 结构化字段里（不是只写一句散文）。
func TestRenewWindowExpiryDegradedWarns(t *testing.T) {
	storage := newWindowStorage(t)
	board, err := NewBoardAdapter(storage)
	if err != nil {
		t.Fatal(err)
	}
	claimed := claimForRenew(t, board, 10*time.Second)

	storage.beforeTxn = func() { storage.advance(11 * time.Second) }

	task, verdict, err := board.RenewWithVerdict(context.Background(), "t1", claimed.Lease.Token,
		taskboard.ClaimRequest{TTL: 20 * time.Second})
	if err != nil {
		t.Fatalf("非原子路径按旧快照提交，续期本身不应报错（实得 %v）", err)
	}
	if !task.Lease.Until.After(claimed.Lease.Until) {
		t.Fatalf("续期应延长租约（旧 %v，新 %v）", claimed.Lease.Until, task.Lease.Until)
	}
	if verdict.Atomic {
		t.Fatal("非原子后端不得自称原子")
	}
	if !verdict.Observed {
		t.Fatal("提交后必须能观测到存储时钟（否则预算不可解释）")
	}
	if !verdict.ExpiredInWindow {
		t.Fatalf("窗口内已越过旧到期时刻，必须留下事实标记: %+v", verdict)
	}
	if verdict.Window < 11*time.Second {
		t.Fatalf("观测窗口必须覆盖实际推进量（实得 %v）", verdict.Window)
	}
	if !verdict.BudgetExceeded {
		t.Fatalf("窗口 %v 明显超过预算 %v，必须标记超预算", verdict.Window, verdict.Budget)
	}
	warn := verdict.Warning()
	if !strings.Contains(warn, "窗口") || !strings.Contains(warn, "预算") {
		t.Fatalf("提示必须可解释（窗口/预算都要出现）: %q", warn)
	}
}

// TestRenewWindowExpiryWithinBudgetWarns 窗口内过期但**没有超预算**：
// 提示仍要指出「窗口内过期」，但不得误报超预算——宿主才能按预算口径
// 决定「可接受的风险」还是「后端行为异常」。
func TestRenewWindowExpiryWithinBudgetWarns(t *testing.T) {
	storage := newWindowStorage(t)
	board, err := NewBoardAdapter(storage)
	if err != nil {
		t.Fatal(err)
	}
	claimed := claimForRenew(t, board, 100*time.Millisecond)

	storage.beforeTxn = func() { storage.advance(100 * time.Millisecond) }

	_, verdict, err := board.RenewWithVerdict(context.Background(), "t1", claimed.Lease.Token,
		taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("非原子路径续期不应报错（实得 %v）", err)
	}
	if !verdict.ExpiredInWindow {
		t.Fatalf("到期时刻落在窗口内，必须标记: %+v", verdict)
	}
	if verdict.BudgetExceeded {
		t.Fatalf("窗口 %v 未超预算 %v，不得误报超预算", verdict.Window, verdict.Budget)
	}
	warn := verdict.Warning()
	if !strings.Contains(warn, "窗口内过期") {
		t.Fatalf("预算内的窗口过期也必须可见: %q", warn)
	}
}

// TestRenewAtomicNoWindow 无异常时序下原子路径的窗口事实：Window=0、
// Observed=true、无提示（正例，防止实现把所有续期都报成降级）。
func TestRenewAtomicNoWindow(t *testing.T) {
	storage := newAtomicStorage(t)
	board, err := NewBoardAdapter(storage)
	if err != nil {
		t.Fatal(err)
	}
	claimed := claimForRenew(t, board, time.Minute)

	task, verdict, err := board.RenewWithVerdict(context.Background(), "t1", claimed.Lease.Token,
		taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Atomic || !verdict.Observed || verdict.Window != 0 || verdict.ExpiredInWindow || verdict.BudgetExceeded {
		t.Fatalf("原子路径的窗口事实不成立: %+v", verdict)
	}
	if verdict.Warning() != "" {
		t.Fatalf("无异常的原子续期不得提示: %q", verdict.Warning())
	}
	if !task.Lease.Until.Equal(precisionBase.Add(time.Minute)) {
		t.Fatalf("续期到期时刻必须由存储时钟算（实得 %v）", task.Lease.Until)
	}
}

// TestStoreRenewWindowExpiryPair StoreAdapter 的同一判据成对覆盖：
// 非原子路径留下窗口提示；原子路径拒绝。
func TestStoreRenewWindowExpiryPair(t *testing.T) {
	act := sideeffect.Action{
		Key:      sideeffect.Key("precision:apply"),
		Resource: "resource-precision",
	}

	t.Run("degraded", func(t *testing.T) {
		storage := newWindowStorage(t)
		store, err := NewStoreAdapter(storage)
		if err != nil {
			t.Fatal(err)
		}
		if _, lease, _, err := store.Acquire(context.Background(), act, 10*time.Second); err != nil {
			t.Fatalf("预占失败: %v", err)
		} else {
			storage.beforeTxn = func() { storage.advance(11 * time.Second) }
			_, verdict, err := store.RenewWithVerdict(context.Background(), act, lease, 10*time.Second)
			if err != nil {
				t.Fatalf("非原子路径按旧快照续期不应报错（实得 %v）", err)
			}
			if !verdict.ExpiredInWindow || verdict.BudgetExceeded != true {
				t.Fatalf("Store 非原子路径必须留下窗口过期/超预算事实: %+v", verdict)
			}
		}
	})

	t.Run("atomic", func(t *testing.T) {
		storage := newAtomicStorage(t)
		store, err := NewStoreAdapter(storage)
		if err != nil {
			t.Fatal(err)
		}
		_, lease, _, err := store.Acquire(context.Background(), act, 10*time.Second)
		if err != nil {
			t.Fatalf("预占失败: %v", err)
		}
		storage.beforeDecide = func() { storage.advance(11 * time.Second) }
		if _, _, err := store.RenewWithVerdict(context.Background(), act, lease, 10*time.Second); !errors.Is(err, sideeffect.ErrFenceStale) {
			t.Fatalf("原子路径必须拒绝窗口内过期（实得 %v）", err)
		}
	})
}

// quota_reclaim_test.go —— 配额悬挂预占的回收协议（issue #267）。
//
// 判据：
//   - Worker 失联（预占到期 + 任务租约已失效）⇒ 回收后余额回到预期；
//   - 任务租约仍有效 ⇒ 不得回收（旧任务可能还在真跑）；
//   - 迟到的旧 Release 不得二次归还/把余额扣成负数；
//   - 重建判据在存储锁外调用（回查账本不得死锁）。
package scheduling

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// fakeOracle 任务租约存活判定替身。
type fakeOracle struct {
	alive map[string]bool
	err   error
	calls atomic.Int32
}

func (o *fakeOracle) TaskLeaseAlive(_ context.Context, taskID string) (bool, error) {
	o.calls.Add(1)
	if o.err != nil {
		return false, o.err
	}
	return o.alive[taskID], nil
}

func quotaTestClock(t *testing.T) (*time.Time, func() time.Time) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &now, func() time.Time { return now }
}

// TestQuotaReclaimAfterWorkerLost Worker 失联后的安全回收全链路。
func TestQuotaReclaimAfterWorkerLost(t *testing.T) {
	now, clock := quotaTestClock(t)
	oracle := &fakeOracle{alive: map[string]bool{"t1": true}}
	store, err := NewMemQuotaStore(map[string]int64{"slot": 10},
		WithQuotaClock(clock), WithQuotaLeaseOracle(oracle))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	lease, err := store.ReserveLease(ctx, QuotaRequest{
		Resource: "slot", Amount: 10, TaskID: "t1", Owner: "worker-1", TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("ReserveLease: %v", err)
	}
	if lease.TaskID != "t1" || lease.Owner != "worker-1" || lease.Until.IsZero() {
		t.Fatalf("租约必须带归属与有效期: %+v", lease)
	}
	if got := store.Usage("slot").Used; got != 10 {
		t.Fatalf("预占后 Used 应为 10, got %d", got)
	}

	// 未到期：不得回收（Worker 可能还在真跑）。
	if err := store.Reclaim(ctx, lease); !errors.Is(err, ErrLeaseNotExpired) {
		t.Fatalf("未到期回收应 ErrLeaseNotExpired, got %v", err)
	}
	*now = now.Add(2 * time.Minute)

	// 到期但任务租约仍有效：不得回收（这正是 #265 的世界：心跳停了不等于任务没跑）。
	if err := store.Reclaim(ctx, lease); !errors.Is(err, ErrLeaseStillHeld) {
		t.Fatalf("任务租约仍有效时回收应 ErrLeaseStillHeld, got %v", err)
	}
	if got := store.Usage("slot").Used; got != 10 {
		t.Fatalf("被拒的回收不得改账, Used=%d", got)
	}

	// Worker 失联确认（任务租约失效）：安全回收，余额回到预期。
	oracle.alive["t1"] = false
	if err := store.Reclaim(ctx, lease); err != nil {
		t.Fatalf("失联确认后回收应成功: %v", err)
	}
	if got := store.Usage("slot").Used; got != 0 {
		t.Fatalf("回收后 Used 必须归零, got %d", got)
	}

	// 迟到的旧 Release：不得二次归还/负余额。
	if err := store.Release(ctx, lease); !errors.Is(err, ErrOverRelease) {
		t.Fatalf("回收后的迟到 Release 必须 ErrOverRelease, got %v", err)
	}
	if got := store.Usage("slot").Used; got != 0 {
		t.Fatalf("迟到 Release 不得改账, Used=%d", got)
	}

	// 回收的额度可被重新预占（新令牌）。
	again, err := store.ReserveLease(ctx, QuotaRequest{Resource: "slot", Amount: 10, TaskID: "t2", TTL: time.Minute})
	if err != nil {
		t.Fatalf("回收后应可重新预占: %v", err)
	}
	if again.Token == lease.Token {
		t.Fatal("重新预占必须铸造新令牌")
	}
}

// TestQuotaReclaimFailClosedWithoutOracle 带任务归属但无判定面 ⇒ 拒绝回收。
func TestQuotaReclaimFailClosedWithoutOracle(t *testing.T) {
	now, clock := quotaTestClock(t)
	store, err := NewMemQuotaStore(map[string]int64{"slot": 5}, WithQuotaClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ReserveLease(context.Background(), QuotaRequest{
		Resource: "slot", Amount: 5, TaskID: "t1", TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if err := store.Reclaim(context.Background(), lease); !errors.Is(err, ErrNoLeaseOracle) {
		t.Fatalf("缺 TaskLeaseOracle 时回收应 fail-closed, got %v", err)
	}
	if got := store.Usage("slot").Used; got != 5 {
		t.Fatalf("拒绝回收不得改账, Used=%d", got)
	}
}

// TestQuotaSweepExpired Reconciler 扫描：只回收「到期 + 任务租约已失效」的预占。
func TestQuotaSweepExpired(t *testing.T) {
	now, clock := quotaTestClock(t)
	oracle := &fakeOracle{alive: map[string]bool{"t1": false, "t2": true}}
	store, err := NewMemQuotaStore(map[string]int64{"slot": 100},
		WithQuotaClock(clock), WithQuotaLeaseOracle(oracle))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mustReserve := func(req QuotaRequest) QuotaLease {
		t.Helper()
		l, rerr := store.ReserveLease(ctx, req)
		if rerr != nil {
			t.Fatalf("ReserveLease: %v", rerr)
		}
		return l
	}
	mustReserve(QuotaRequest{Resource: "slot", Amount: 10, TaskID: "t1", TTL: time.Second}) // 到期 + 失联 ⇒ 回收
	mustReserve(QuotaRequest{Resource: "slot", Amount: 10, TaskID: "t2", TTL: time.Second}) // 到期 + 活着 ⇒ 留
	mustReserve(QuotaRequest{Resource: "slot", Amount: 5, TaskID: "t3"})                    // 不限期 ⇒ 留
	mustReserve(QuotaRequest{Resource: "slot", Amount: 10, TaskID: "t4", TTL: time.Hour})   // 未到期 ⇒ 留
	if got := store.Usage("slot").Used; got != 35 {
		t.Fatalf("预置后 Used 应为 35, got %d", got)
	}

	*now = now.Add(time.Minute)
	n, err := store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("只应回收 1 条（到期且任务租约失效），实得 %d", n)
	}
	if got := store.Usage("slot").Used; got != 25 {
		t.Fatalf("扫描后 Used 应为 25, got %d", got)
	}
	if n, err := store.SweepExpired(ctx); err != nil || n != 0 {
		t.Fatalf("重复扫描不应再回收（n=%d err=%v）", n, err)
	}
}

// TestQuotaOracleNotCalledUnderStoreLock 判定面回查账本不得死锁。
func TestQuotaOracleNotCalledUnderStoreLock(t *testing.T) {
	now, clock := quotaTestClock(t)
	var store *MemQuotaStore
	oracle := &funcOracle{fn: func(context.Context, string) (bool, error) {
		_ = store.Usage("slot") // 同 goroutine 回查账本：判定面必须在存储锁外
		return false, nil
	}}
	store, err := NewMemQuotaStore(map[string]int64{"slot": 5},
		WithQuotaClock(clock), WithQuotaLeaseOracle(oracle))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ReserveLease(context.Background(), QuotaRequest{
		Resource: "slot", Amount: 5, TaskID: "t1", TTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)

	done := make(chan error, 1)
	go func() { done <- store.Reclaim(context.Background(), lease) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Reclaim: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TaskLeaseOracle 回查账本时死锁（判定面仍在存储锁内）")
	}
}

// funcOracle 函数适配器。
type funcOracle struct {
	fn func(context.Context, string) (bool, error)
}

func (o *funcOracle) TaskLeaseAlive(ctx context.Context, taskID string) (bool, error) {
	return o.fn(ctx, taskID)
}

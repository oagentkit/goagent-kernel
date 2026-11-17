// quota_clock_reentry_test.go —— 宿主时钟回调不得在存储锁内被调用（issue #366）。
//
// WithQuotaClock 是公开装配接缝。宿主回调里回查本包用量是自然写法
// （读一次账本、记一次指标）；实现若在 m.mu 内调 clock()，sync.Mutex
// 不可重入 ⇒ 永久死锁，且形态是**静默挂起**（无错误、无超时）。
//
// 三条调用路径（ReserveLease / Reclaim / SweepExpired）各配一条判据：
// 回调重入本包时，用例必须在 3s 内自行返回。
package scheduling

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// mustNotDeadlockQuota 在 3s 内没返回即判死锁（goroutine 泄漏由测试进程收场）。
func mustNotDeadlockQuota(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s 在持锁期间调用了宿主时钟回调：回调重入本包即死锁（3s 未返回）", what)
	}
}

// reentrantQuotaStore 返回一个时钟回调会回查 Usage 的配额账本。
//
// reenter=false 时用于播种（避免构造/首次预占阶段就撞锁）；播种完成后置 true。
// 时钟取可变值，便于构造「已到期」场景而不 sleep。
type reentrantQuotaStore struct {
	store   *MemQuotaStore
	reenter atomic.Bool
	now     atomic.Int64 // UnixNano
}

func newReentrantQuotaStore(t *testing.T, limits map[string]int64) *reentrantQuotaStore {
	t.Helper()
	r := &reentrantQuotaStore{}
	r.now.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	var m *MemQuotaStore
	m, err := NewMemQuotaStore(limits, WithQuotaClock(func() time.Time {
		if r.reenter.Load() {
			// 宿主回查账本：会抢同一把 m.mu。不触发时钟，故不会递归。
			_ = m.Usage("slot")
		}
		return time.Unix(0, r.now.Load())
	}))
	if err != nil {
		t.Fatalf("NewMemQuotaStore: %v", err)
	}
	r.store = m
	return r
}

// TestQuotaReserveLeaseClockCallbackReentry ReserveLease 的时钟回调
// 重入 Usage 必须正常返回（issue #366 调用点 ①）。
func TestQuotaReserveLeaseClockCallbackReentry(t *testing.T) {
	r := newReentrantQuotaStore(t, map[string]int64{"slot": 10})
	r.reenter.Store(true)
	ctx := context.Background()
	mustNotDeadlockQuota(t, "ReserveLease", func() {
		lease, err := r.store.ReserveLease(ctx, QuotaRequest{
			Resource: "slot", Amount: 1, TTL: time.Minute,
		})
		if err != nil {
			t.Errorf("ReserveLease: %v", err)
			return
		}
		if lease.Until.IsZero() {
			t.Error("带 TTL 的预占必须有到期时刻")
		}
	})
}

// TestQuotaReclaimClockCallbackReentry Reclaim 的时钟回调重入 Usage
// 必须正常返回（issue #366 调用点 ②，多出口解锁路径）。
func TestQuotaReclaimClockCallbackReentry(t *testing.T) {
	r := newReentrantQuotaStore(t, map[string]int64{"slot": 10})
	ctx := context.Background()
	lease, err := r.store.ReserveLease(ctx, QuotaRequest{
		Resource: "slot", Amount: 1, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("播种预占: %v", err)
	}
	// 把存储时钟推到租约到期之后（不 sleep）。
	r.now.Store(time.Unix(0, r.now.Load()).Add(2 * time.Minute).UnixNano())
	r.reenter.Store(true)
	mustNotDeadlockQuota(t, "Reclaim", func() {
		if err := r.store.Reclaim(ctx, lease); err != nil {
			t.Errorf("过期预占应可回收, got %v", err)
		}
	})
}

// TestQuotaSweepExpiredClockCallbackReentry SweepExpired 的时钟回调
// 重入 Usage 必须正常返回（issue #366 调用点 ③）。
func TestQuotaSweepExpiredClockCallbackReentry(t *testing.T) {
	r := newReentrantQuotaStore(t, map[string]int64{"slot": 10})
	ctx := context.Background()
	if _, err := r.store.ReserveLease(ctx, QuotaRequest{
		Resource: "slot", Amount: 1, TTL: time.Minute,
	}); err != nil {
		t.Fatalf("播种预占: %v", err)
	}
	r.now.Store(time.Unix(0, r.now.Load()).Add(2 * time.Minute).UnixNano())
	r.reenter.Store(true)
	mustNotDeadlockQuota(t, "SweepExpired", func() {
		n, err := r.store.SweepExpired(ctx)
		if err != nil {
			t.Errorf("SweepExpired: %v", err)
			return
		}
		if n != 1 {
			t.Errorf("应回收 1 条到期预占, got %d", n)
		}
	})
}

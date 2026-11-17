// clock_reentry_test.go —— 宿主时钟回调不得在 m.mu + commitMu 内被调用（issue #367）。
//
// UseClock 是公开装配接缝。宿主回调里回查本包快照是自然写法（快照要带
// 「判断时刻」、指标要记一次读数）；实现若在双锁内调 m.clock()，
// sync.Mutex 不可重入 ⇒ 永久死锁，且 commitMu 会把写路径全线一起卡住。
//
// Claim 与 Renew 两条路径各配一条判据：回调重入 SnapshotTask 时，
// 用例必须在 3s 内自行返回。
package controlplane

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// mustNotDeadlockCP 在 3s 内没返回即判死锁（goroutine 泄漏由测试进程收场）。
func mustNotDeadlockCP(t *testing.T, what string, fn func()) {
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

// reentrantMemStore 返回一个时钟回调会回查 SnapshotTask 的控制面存储。
//
// reenter=false 时用于播种；播种完成后置 true。
type reentrantMemStore struct {
	store   *MemStore
	reenter atomic.Bool
}

func newReentrantMemStore(t *testing.T) *reentrantMemStore {
	t.Helper()
	r := &reentrantMemStore{}
	m := NewMemStore(nil)
	r.store = m
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.UseClock(func() time.Time {
		if r.reenter.Load() {
			// 宿主回查快照：会抢同一把 m.mu。不触发时钟，故不会递归。
			_, _ = m.SnapshotTask(context.Background(), "t1")
		}
		return fixed
	})
	if err := m.SeedRun("r1", "", []taskboard.Task{{ID: "t1", State: taskboard.StateOpen}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	return r
}

// TestMemStoreClaimClockCallbackReentry Claim 的时钟回调重入快照必须正常返回。
func TestMemStoreClaimClockCallbackReentry(t *testing.T) {
	r := newReentrantMemStore(t)
	r.reenter.Store(true)
	ctx := context.Background()
	mustNotDeadlockCP(t, "Claim", func() {
		if _, err := r.store.Claim(ctx, "t1", "a1", time.Minute); err != nil {
			t.Errorf("Claim: %v", err)
		}
	})
}

// TestMemStoreRenewClockCallbackReentry Renew 的时钟回调重入快照必须正常返回。
func TestMemStoreRenewClockCallbackReentry(t *testing.T) {
	r := newReentrantMemStore(t)
	ctx := context.Background()
	task, err := r.store.Claim(ctx, "t1", "a1", time.Minute)
	if err != nil {
		t.Fatalf("播种认领: %v", err)
	}
	r.reenter.Store(true)
	mustNotDeadlockCP(t, "Renew", func() {
		if _, err := r.store.Renew(ctx, "t1", task.Lease.Token,
			taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
			t.Errorf("Renew: %v", err)
		}
	})
}

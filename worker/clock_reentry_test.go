package worker

// 注册表参考实现的宿主时钟不得在持锁期间调用（issue #317）。
//
// WithRegistryClock 是公开装配接缝（契约套件的过期场景靠它确定性推进）。
// 宿主回调里重入本包（读一次注册表 / 注销）是自然写法——实现若在 m.mu 内
// 调 now()，sync.Mutex 不可重入 ⇒ 永久死锁。

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func mustNotDeadlock(t *testing.T, what string, fn func()) {
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

// newReentrantRegistry 返回一个时钟回调会重入 Deregister 的注册表。
// reenter=false 时先播种一条注册，避免播种阶段就撞锁。
func newReentrantRegistry(t *testing.T, reenter *atomic.Bool) *MemRegistry {
	t.Helper()
	var m *MemRegistry
	m = NewMemRegistry(WithRegistryClock(func() time.Time {
		if reenter.Load() {
			// Deregister 会拿同一把 m.mu；不触发时钟，故不会递归。
			_ = m.Deregister(context.Background(), "reentry", "bad-token")
		}
		return time.Unix(1700000000, 0)
	}))
	return m
}

// TestRegisterClockCallbackReentry Register 的时钟回调重入注销必须正常返回。
func TestRegisterClockCallbackReentry(t *testing.T) {
	var reenter atomic.Bool
	reenter.Store(true)
	m := newReentrantRegistry(t, &reenter)
	mustNotDeadlock(t, "Register", func() {
		if _, err := m.Register(context.Background(), Spec{ID: "w1", TTL: time.Minute}); err != nil {
			t.Errorf("Register: %v", err)
		}
	})
}

// TestHeartbeatClockCallbackReentry Heartbeat 的时钟回调重入注销必须正常返回。
func TestHeartbeatClockCallbackReentry(t *testing.T) {
	var reenter atomic.Bool
	m := newReentrantRegistry(t, &reenter)
	reg, err := m.Register(context.Background(), Spec{ID: "w1", TTL: time.Minute})
	if err != nil {
		t.Fatalf("播种注册失败: %v", err)
	}
	reenter.Store(true)
	mustNotDeadlock(t, "Heartbeat", func() {
		if _, err := m.Heartbeat(context.Background(), "w1", reg.Token, time.Minute); err != nil {
			t.Errorf("Heartbeat: %v", err)
		}
	})
}

// TestListClockCallbackReentry List 的时钟回调重入注销必须正常返回。
func TestListClockCallbackReentry(t *testing.T) {
	var reenter atomic.Bool
	m := newReentrantRegistry(t, &reenter)
	if _, err := m.Register(context.Background(), Spec{ID: "w1", TTL: time.Minute}); err != nil {
		t.Fatalf("播种注册失败: %v", err)
	}
	reenter.Store(true)
	mustNotDeadlock(t, "List", func() {
		if _, err := m.List(context.Background()); err != nil {
			t.Errorf("List: %v", err)
		}
	})
}

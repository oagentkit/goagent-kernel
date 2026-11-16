package handoff

// 宿主注入的 Ledger 时钟不得在持锁期间调用（issue #317）。
//
// WithLedgerClock 是公开装配接缝；MemoryLedger.Now 是 ClockedLedger 的实现，
// Desk 每次推算截止时刻/过期都会走到它。回调里重入本包（Load/Epoch 等）是
// 自然写法——实现若在 m.mu 内调 clock()，sync.Mutex 不可重入 ⇒ 永久死锁。

import (
	"context"
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

// TestNowClockCallbackReentry Now 的时钟回调重入 Load 必须正常返回。
func TestNowClockCallbackReentry(t *testing.T) {
	want := time.Unix(1700000000, 0)
	// 回调要引用 ledger 本身（重入 Load），用 holder 承接「构造后再赋值」。
	type holder struct{ l *MemoryLedger }
	h := &holder{}
	h.l = NewMemoryLedger(WithLedgerClock(func() time.Time {
		_, _ = h.l.Load(context.Background(), Key("reentry")) // 重入本包
		return want
	}))
	mustNotDeadlock(t, "Now", func() {
		got, err := h.l.Now(context.Background())
		if err != nil {
			t.Errorf("Now: %v", err)
			return
		}
		if !got.Equal(want) {
			t.Errorf("Now = %v, want %v", got, want)
		}
	})
}

// TestUseClockCallbackReentry UseClock 注入的时钟同样不得在持锁期间调用。
func TestUseClockCallbackReentry(t *testing.T) {
	want := time.Unix(1700000000, 0)
	l := NewMemoryLedger()
	l.UseClock(func() time.Time {
		_, _ = l.Epoch(context.Background(), Key("reentry"))
		return want
	})
	mustNotDeadlock(t, "UseClock+Now", func() {
		got, err := l.Now(context.Background())
		if err != nil {
			t.Errorf("Now: %v", err)
			return
		}
		if !got.Equal(want) {
			t.Errorf("Now = %v, want %v", got, want)
		}
	})
}

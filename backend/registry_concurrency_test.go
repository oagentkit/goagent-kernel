package backend

// issue #329：登记簿不得在持锁期间调用宿主声明方法（ID / Isolation）。
//
// 判据两条：
//   - 慢 ID() 只拖慢它自己那次调用，不拖住并发 Register/Candidates；
//   - ID() 回查登记簿不得死锁（宿主方法在锁外调用）。

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// slowID 声明方法很慢的后端：ID() 睡 delay（模拟问配置中心/容器运行时）。
type slowID struct {
	id    string
	delay time.Duration
	enter chan<- struct{} // 可选：首次进入 ID() 时通知
	once  atomic.Bool
}

func (s *slowID) ID() string {
	if s.enter != nil && s.once.CompareAndSwap(false, true) {
		s.enter <- struct{}{}
	}
	time.Sleep(s.delay)
	return s.id
}

func (s *slowID) Isolation() Isolation { return IsolationProcess }

func (s *slowID) Exec(context.Context, Job) (Result, error) { return Result{OK: true}, nil }

// reentrantID ID() 回查登记簿一次的后端：回归「同一 goroutine 二次抢锁」。
type reentrantID struct {
	reg   *Registry
	inner string
	once  atomic.Bool
}

func (b *reentrantID) ID() string {
	if b.once.CompareAndSwap(false, true) {
		if _, err := b.reg.Candidates(Spec{}); err != nil {
			panic("回查 Candidates 不应失败: " + err.Error())
		}
	}
	return b.inner
}

func (b *reentrantID) Isolation() Isolation                      { return IsolationNone }
func (b *reentrantID) Exec(context.Context, Job) (Result, error) { return Result{OK: true}, nil }

func TestRegistryHostMethodsOutsideLock(t *testing.T) {
	const delay = 200 * time.Millisecond

	t.Run("慢 ID 不拖住并发 Register", func(t *testing.T) {
		r := &Registry{}
		entered := make(chan struct{}, 1)
		slow := &slowID{id: "slow", delay: delay, enter: entered}
		if err := r.Register(slow); err != nil { // 首次登记本来就慢（锁外，可接受）
			t.Fatalf("登记慢后端失败: %v", err)
		}

		candsDone := make(chan struct{})
		go func() {
			defer close(candsDone)
			if _, err := r.Candidates(Spec{}); err != nil {
				t.Errorf("Candidates 失败: %v", err)
			}
		}()
		select {
		case <-entered: // Candidates 已在宿主 ID() 内
		case <-time.After(2 * time.Second):
			t.Fatal("Candidates 未进入宿主 ID()")
		}

		start := time.Now()
		if err := r.Register(&stub{id: "fast", iso: IsolationNone}); err != nil {
			t.Fatalf("并发登记失败: %v", err)
		}
		if waited := time.Since(start); waited >= delay/2 {
			t.Fatalf("并发 Register 被慢 ID() 拖住 %v（delayed host call 不应落在锁内）", waited)
		}
		<-candsDone
	})

	t.Run("慢 ID 不拖住并发 Candidates", func(t *testing.T) {
		r := &Registry{}
		entered := make(chan struct{}, 1)
		slow := &slowID{id: "slow", delay: delay, enter: entered}
		fast := &stub{id: "fast", iso: IsolationNone}
		if err := r.Register(fast); err != nil {
			t.Fatal(err)
		}

		regDone := make(chan struct{})
		go func() {
			defer close(regDone)
			if err := r.Register(slow); err != nil {
				t.Errorf("登记慢后端失败: %v", err)
			}
		}()
		select {
		case <-entered: // Register 已在宿主 ID() 内
		case <-time.After(2 * time.Second):
			t.Fatal("Register 未进入宿主 ID()")
		}

		start := time.Now()
		cands, err := r.Candidates(Spec{})
		if err != nil {
			t.Fatalf("并发 Candidates 失败: %v", err)
		}
		if waited := time.Since(start); waited >= delay/2 {
			t.Fatalf("并发 Candidates 被慢 ID() 拖住 %v", waited)
		}
		if len(cands) != 1 || cands[0].ID() != "fast" {
			t.Fatalf("快照应只含已登记后端, got %v", cands)
		}
		<-regDone
	})

	t.Run("ID 回查登记簿不得死锁", func(t *testing.T) {
		r := &Registry{}
		done := make(chan error, 1)
		go func() { done <- r.Register(&reentrantID{reg: r, inner: "reentrant"}) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("回查登记簿的 ID() 应可完成: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("ID() 回查登记簿时死锁（宿主方法被锁在登记簿锁内）")
		}
		if _, err := r.Candidates(Spec{}); err != nil {
			t.Fatalf("回查后的登记簿应可用: %v", err)
		}
	})
}

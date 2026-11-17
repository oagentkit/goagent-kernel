// quota_clock_test.go —— 存储时钟不得在持锁期间调用（issue #366）。
//
// 时钟是**宿主注入的回调**。宿主在回调里回查本包（读一次用量是极自然的
// 写法）⇒ 内核若在 m.mu 内回调它，就是二次抢同一把不可重入的锁 ⇒
// **永久死锁**。失效形态是**静默挂起**：调用方拿不到任何错误，只看到这次
// 调用永远不返回（比报错更难查）。
//
// 三条调用路径各配一条：ReserveLease / Reclaim / SweepExpired。
//
// 判据是**时间上界**：用例必须在 3s 内**自行返回**——不是靠测试超时兜底
// （超时会把「挂死」报成测试超时，红点不直指根因）。形状参照
// worker/clock_reentry_test.go。
//
// # 为什么预置步骤不碰时钟
//
// 每条用例只让**被验的那条路径**碰时钟：预置用的预占一律 TTL=0（不限期，
// 不读时钟）。否则预置步骤自己就会挂死，红点指不到被验的路径。
package scheduling

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// mustReturnWithin 断言 fn 在时限内自行返回（挂死 = 红，且红点直指根因）。
func mustReturnWithin(t *testing.T, what string, fn func()) {
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

// reentrantClockStore 造一个时钟回调会回查本包的存储。
//
// 回调里读一次用量——这正是宿主最自然的写法。
func reentrantClockStore(t *testing.T) *MemQuotaStore {
	t.Helper()
	var m *MemQuotaStore
	m, err := NewMemQuotaStore(map[string]int64{"slot": 10}, WithQuotaClock(func() time.Time {
		_ = m.Usage("slot") // 回查本包：持锁调用它 = 永久死锁
		return time.Unix(1700000000, 0)
	}))
	if err != nil {
		t.Fatalf("构造存储失败: %v", err)
	}
	return m
}

// TestQuotaClockReentryReserveLease ReserveLease 路径：时钟回调回查本包时
// 必须自行返回。
func TestQuotaClockReentryReserveLease(t *testing.T) {
	m := reentrantClockStore(t)
	mustReturnWithin(t, "ReserveLease", func() {
		_, err := m.ReserveLease(context.Background(), QuotaRequest{
			Resource: "slot", Amount: 1, TTL: time.Minute,
		})
		if err != nil {
			t.Errorf("ReserveLease: %v", err)
		}
	})
}

// TestQuotaClockReentryReclaim Reclaim 路径：时钟回调回查本包时必须自行返回。
func TestQuotaClockReentryReclaim(t *testing.T) {
	m := reentrantClockStore(t)
	// 预置：不带有效期的预占（TTL=0 ⇒ 不读时钟）。
	lease, err := m.ReserveLease(context.Background(), QuotaRequest{
		Resource: "slot", Amount: 1,
	})
	if err != nil {
		t.Fatalf("预置预占失败: %v", err)
	}
	mustReturnWithin(t, "Reclaim", func() {
		err := m.Reclaim(context.Background(), lease)
		// 不限期的预占不得回收（判据不满足 ⇒ 明确拒，不是静默放过）。
		if !errors.Is(err, ErrLeaseNotExpired) {
			t.Errorf("不限期的预占必须报 ErrLeaseNotExpired（实得 %v）", err)
		}
	})
}

// TestQuotaClockReentrySweepExpired SweepExpired 路径：时钟回调回查本包时
// 必须自行返回。
func TestQuotaClockReentrySweepExpired(t *testing.T) {
	m := reentrantClockStore(t)
	lease, err := m.ReserveLease(context.Background(), QuotaRequest{
		Resource: "slot", Amount: 1,
	})
	if err != nil {
		t.Fatalf("预置预占失败: %v", err)
	}
	// 把这条预占改成已到期（进程内参考实现，测试可以直接改存储记录），
	// 让扫描真的走一遍回收判据——而回收判据里还要再读一次时钟。
	m.mu.Lock()
	rec := m.leases[lease.Token]
	rec.Until = time.Unix(1600000000, 0) // 早于存储时钟
	m.leases[lease.Token] = rec
	m.mu.Unlock()

	mustReturnWithin(t, "SweepExpired", func() {
		n, err := m.SweepExpired(context.Background())
		if err != nil {
			t.Errorf("SweepExpired: %v", err)
		}
		if n != 1 {
			t.Errorf("到期预占应被回收 1 条，实得 %d", n)
		}
	})
}

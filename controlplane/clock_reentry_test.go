// clock_reentry_test.go —— 宿主注入的时钟回调不得在持内核锁时被调用
// （issue #367）。
//
// 时钟回调是宿主接缝：回调里回查本包（读一次快照是极自然的写法）是
// 正常写法，而 sync.Mutex 不可重入 ⇒ 持锁调用它就是**永久死锁**——
// 现场只剩静默挂起：没有错误、没有超时兜底。
//
// 断言形状必须是「**自行返回**」而不是靠测试超时兜底：超时会把「挂死」
// 报成测试超时，红点不直指根因（与 worker/clock_reentry_test.go 同款）。
package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// mustNotDeadlock 断言 fn 在 3s 内自行返回。
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

// reentrantClockStore 构造一个时钟回调会回查本包的 MemStore。
func reentrantClockStore(id taskboard.TaskID) *MemStore {
	m := NewMemStore(&MemAuditor{})
	m.UseClock(func() time.Time {
		// 宿主回调里回查本包：读一次快照（自然写法）。
		_, _ = m.SnapshotTask(context.Background(), id)
		return time.Unix(1700000000, 0)
	})
	return m
}

// TestMemStoreClaimClockCallbackReentry Claim 的时钟回调重入必须自行返回。
func TestMemStoreClaimClockCallbackReentry(t *testing.T) {
	m := reentrantClockStore("t1")
	if err := m.SeedRun("r1", "", []taskboard.Task{{ID: "t1", State: taskboard.StateOpen}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	ctx := context.Background()
	mustNotDeadlock(t, "Claim", func() {
		if _, err := m.Claim(ctx, "t1", "a1", time.Minute); err != nil {
			t.Errorf("Claim: %v", err)
		}
	})
}

// TestMemStoreRenewClockCallbackReentry Renew 的时钟回调重入必须自行返回。
//
// 认领态**直接播种**（不走 Claim）：这样本用例只考验 Renew 一条路径——
// 若播种也走 Claim，挂死会先在 Claim 上暴露，Renew 那条路径就没被单独验到。
func TestMemStoreRenewClockCallbackReentry(t *testing.T) {
	m := reentrantClockStore("t1")
	seeded := taskboard.Task{
		ID:    "t1",
		State: taskboard.StateClaimed,
		Owner: "a1",
		Lease: taskboard.Lease{Token: "t-1", Fence: 1},
	}
	if err := m.SeedRun("r1", "", []taskboard.Task{seeded}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	ctx := context.Background()
	mustNotDeadlock(t, "Renew", func() {
		if _, err := m.Renew(ctx, "t1", "t-1",
			taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
			t.Errorf("Renew: %v", err)
		}
	})
}

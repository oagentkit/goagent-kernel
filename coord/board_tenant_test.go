// board_tenant_test.go —— 租户进存储键与领取条件的可判定证据（issue #195）。
//
// 三条线：
//  1. 共享契约套件 boardtest.RunTenant 跑在租户作用域适配器上（跨租户
//     不可见/不可领、同 id 互不覆盖、并发、令牌、缺租户 fail-closed）；
//  2. 键布局：空租户 = 旧布局**逐字不变**；租户布局带独立根与转义；
//  3. 装配面：Seed 不得跨作用域静默落键。
package coord

import (
	"bytes"
	"context"
	"sort"
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/taskboard/boardtest"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// TestTenantBoardAdapterContractSuite 租户作用域适配器接入共享契约套件。
func TestTenantBoardAdapterContractSuite(t *testing.T) {
	boardtest.RunTenant(t, func(t *testing.T, clock *boardtest.FakeClock) boardtest.ClockedBoard {
		t.Helper()
		b, err := NewTenantBoardAdapter(NewMemoryBackend())
		if err != nil {
			t.Fatal(err)
		}
		b.UseClock(clock.Now)
		return b
	})
}

// TestLegacyKeyLayoutUnchanged 空租户 = 现有布局逐字不变（零回归的可判定面）。
//
// 除了键名，还钉住任务记录的 JSON 不新增租户字段（omitempty）：旧存储的
// 字节布局也不因本次改动漂移。
func TestLegacyKeyLayoutUnchanged(t *testing.T) {
	ctx := tenant.WithContext(context.Background(), tenant.Context{})
	inner := NewMemoryBackend()
	b, err := NewBoardAdapter(inner)
	if err != nil {
		t.Fatal(err)
	}
	if b.TenantScoped() {
		t.Fatal("NewBoardAdapter 必须是单租户作用域（默认路径零回归）")
	}
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	if _, err := b.Claim(ctx, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if got, want := keysOf(t, inner), []string{"coord/task/t1", "coord/taskfence/t1"}; !equalStrings(got, want) {
		t.Fatalf("单租户键布局漂移: got %v want %v", got, want)
	}
	snap, err := inner.Snapshot(ctx, "coord/task/t1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snap["coord/task/t1"].Val, []byte(`"Tenant"`)) {
		t.Fatalf("空租户任务的记录不应包含 Tenant 字段（旧布局逐字不变）: %s", snap["coord/task/t1"].Val)
	}
	tasks, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Tenant != "" {
		t.Fatalf("空租户 List 必须返回 Tenant 为空的任务（实得 %+v）", tasks)
	}
}

// TestTenantKeyLayout 租户布局：独立根 + 转义段 + 与单租户键互不可见。
func TestTenantKeyLayout(t *testing.T) {
	inner := NewMemoryBackend()
	b, err := NewTenantBoardAdapter(inner)
	if err != nil {
		t.Fatal(err)
	}
	if !b.TenantScoped() {
		t.Fatal("NewTenantBoardAdapter 必须报告租户作用域")
	}
	b.Seed(
		taskboard.Task{Tenant: "tenant-a", ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{Tenant: "tenant-b", ID: "t1", State: taskboard.StateOpen},
	)
	ctxA := tenant.WithContext(context.Background(), tenant.Context{Tenant: "tenant-a"})
	ctxB := tenant.WithContext(context.Background(), tenant.Context{Tenant: "tenant-b"})
	if _, err := b.Claim(ctxA, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Claim(ctxB, "t1", "worker-b", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	got := keysOf(t, inner)
	want := []string{
		"coord/t/tenant-a/task/t1",
		"coord/t/tenant-a/taskfence/t1",
		"coord/t/tenant-b/task/t1",
		"coord/t/tenant-b/taskfence/t1",
	}
	if !equalStrings(got, want) {
		t.Fatalf("租户键布局不符: got %v want %v", got, want)
	}
	// 单租户适配器看不到租户键下的任务（可见性按作用域隔离）。
	legacy, err := NewBoardAdapter(inner)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := legacy.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("单租户 List 不得看到租户键下的任务（实得 %d 条）", len(tasks))
	}
}

// TestTenantKeySegmentEscaped 租户段转义：租户名不能伪造出额外的键分段。
func TestTenantKeySegmentEscaped(t *testing.T) {
	inner := NewMemoryBackend()
	b, err := NewTenantBoardAdapter(inner)
	if err != nil {
		t.Fatal(err)
	}
	// 两个租户名一个含分隔符、一个是对照；任务 id 同样构造碰撞尝试。
	b.Seed(
		taskboard.Task{Tenant: "tenant/a", ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{Tenant: "tenant", ID: "a/task/t1", State: taskboard.StateOpen},
	)
	got := keysOf(t, inner)
	want := []string{
		"coord/t/tenant%2Fa/task/t1",
		"coord/t/tenant/task/a/task/t1",
	}
	if !equalStrings(got, want) {
		t.Fatalf("租户段转义不符: got %v want %v", got, want)
	}
}

// TestTenantSeedRejectsCrossScope Seed 不得跨作用域静默落键。
func TestTenantSeedRejectsCrossScope(t *testing.T) {
	legacy, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	mustPanic(t, "单租户适配器摆入租户任务", func() {
		legacy.Seed(taskboard.Task{Tenant: "tenant-a", ID: "t1"})
	})
	scoped, err := NewTenantBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	mustPanic(t, "租户适配器摆入空租户任务", func() {
		scoped.Seed(taskboard.Task{ID: "t1"})
	})
}

// mustPanic 断言 f 必定 panic（装配期错误不静默）。
func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s 必须 panic", what)
		}
	}()
	f()
}

// keysOf 列出后端的全部存活键（测试尺度：内存后端）。
func keysOf(t *testing.T, b *MemoryBackend) []string {
	t.Helper()
	keys, err := b.Keys(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	return keys
}

// equalStrings 报告两个已排序的键集是否相同。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

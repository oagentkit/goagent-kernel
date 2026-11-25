// tenant_test.go —— 矩阵行八：跨租户任务碰撞（Standalone + Cluster）。
//
// 同一 TaskID 在两个租户下必须互不可见、互不覆盖、令牌互不认。租户作用域
// 适配器的键布局与领取条件（issue #195）是判据本体：碰撞若发生，A 租户的
// 认领/完成会改写 B 租户的任务——这是最贵的一类串租户缺陷。
package faultmatrix

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// runCrossTenantTaskCollision 同 ID 跨租户：
//   - 两个租户都能认领自己的同名任务（互不覆盖）；
//   - List 只含本租户任务；
//   - 用 B 的令牌完成 A 的任务必须被拒；
//   - 缺租户上下文 fail-closed（不得退化成单租户命名空间）。
func runCrossTenantTaskCollision(t *testing.T) {
	ctxA := tenant.WithContext(context.Background(), tenant.Context{Tenant: "tenant-a"})
	ctxB := tenant.WithContext(context.Background(), tenant.Context{Tenant: "tenant-b"})
	board, err := coord.NewTenantBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatalf("装配租户任务板失败: %v", err)
	}
	const id taskboard.TaskID = "shared-task-id"
	board.Seed(
		taskboard.Task{ID: id, Tenant: "tenant-a", State: taskboard.StateOpen},
		taskboard.Task{ID: id, Tenant: "tenant-b", State: taskboard.StateOpen},
	)

	a, err := board.Claim(ctxA, id, "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("租户 A 认领自己的任务失败: %v", err)
	}
	b, err := board.Claim(ctxB, id, "worker-b", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("租户 B 认领同名任务失败（不得被 A 的认领挡住）: %v", err)
	}
	if a.Lease.Token == b.Lease.Token {
		t.Fatal("跨租户同名任务不得共享令牌——撞键的直接信号")
	}

	listA, err := board.List(ctxA)
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0].Tenant != "tenant-a" || listA[0].Owner != "worker-a" {
		t.Fatalf("租户 A 的 List 只能看到自己的任务: %+v", listA)
	}
	listB, err := board.List(ctxB)
	if err != nil {
		t.Fatal(err)
	}
	if len(listB) != 1 || listB[0].Tenant != "tenant-b" || listB[0].Owner != "worker-b" {
		t.Fatalf("租户 B 的 List 只能看到自己的任务: %+v", listB)
	}

	// 跨租户令牌：B 的令牌不得收场 A 的任务。
	if err := board.Complete(ctxA, id, b.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("B 的令牌完成 A 的同名任务必须 ErrLeaseLost（实得 %v）", err)
	}
	// 各自完成自己的任务。
	if err := board.Complete(ctxA, id, a.Lease.Token); err != nil {
		t.Fatalf("A 用自己的令牌完成失败: %v", err)
	}
	if err := board.Complete(ctxB, id, b.Lease.Token); err != nil {
		t.Fatalf("B 用自己的令牌完成失败: %v", err)
	}

	// 缺租户上下文：fail-closed，不得落到某个默认命名空间。
	if _, err := board.List(context.Background()); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("缺租户上下文必须 ErrNoContext（实得 %v）", err)
	}
}

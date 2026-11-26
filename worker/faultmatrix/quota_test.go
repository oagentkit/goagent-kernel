// quota_test.go —— 矩阵行十二：配额悬挂（Cluster）。
//
// Worker 失联后，预占额度不能凭「心跳消失」就归还：必须先确认预占过期
// **且**关联任务的租约已失效（issue #267）。任务租约仍活时回收会让旧任务
// 与新调度者共享同一份额度（超卖）；回收后迟到的 Release 不得二次归还。
package faultmatrix

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/scheduling"
)

// quotaOracle 任务租约存活判定替身（回收判据的依赖面）。
type quotaOracle struct {
	alive atomic.Bool
	err   error
}

func (o *quotaOracle) TaskLeaseAlive(context.Context, string) (bool, error) {
	return o.alive.Load(), o.err
}

func runQuotaDanglingReclaim(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	oracle := &quotaOracle{}
	store, err := scheduling.NewMemQuotaStore(
		map[string]int64{"gpu": 1},
		scheduling.WithQuotaClock(clock.Now),
		scheduling.WithQuotaLeaseOracle(oracle),
	)
	if err != nil {
		t.Fatalf("装配配额存储失败: %v", err)
	}

	lease, err := store.ReserveLease(ctx, scheduling.QuotaRequest{
		Resource: "gpu", Amount: 1, TaskID: "task-quota", Owner: "worker-a", TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("预占失败: %v", err)
	}
	if got := store.Usage("gpu").Used; got != 1 {
		t.Fatalf("预占后用量必须是 1（实得 %d）", got)
	}

	// 预占过期，但关联任务租约仍活：不得回收（旧任务可能还在真跑）。
	clock.Advance(2 * time.Minute)
	oracle.alive.Store(true)
	reclaimed, err := store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("扫描不应因「任务仍活」失败: %v", err)
	}
	if reclaimed != 0 {
		t.Fatalf("任务租约仍活时不得回收配额（实得回收 %d 条）", reclaimed)
	}
	if got := store.Usage("gpu").Used; got != 1 {
		t.Fatalf("拒绝回收后用量必须保持 1（实得 %d）", got)
	}

	// 任务租约也失效：这轮才允许回收。
	oracle.alive.Store(false)
	reclaimed, err = store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("回收扫描失败: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("任务租约失效后必须回收悬挂预占（实得 %d）", reclaimed)
	}
	if got := store.Usage("gpu").Used; got != 0 {
		t.Fatalf("回收后用量必须归零（实得 %d）", got)
	}

	// 迟到的 Release 不得二次归还。
	if err := store.Release(ctx, lease); !errors.Is(err, scheduling.ErrOverRelease) {
		t.Fatalf("回收后迟到的 Release 必须 ErrOverRelease（实得 %v）", err)
	}
	if got := store.Usage("gpu").Used; got != 0 {
		t.Fatalf("迟到 Release 不得把用量改成负数或重复归还（实得 %d）", got)
	}

	// 额度必须重新可用（回收的目的）。
	if _, err := store.ReserveLease(ctx, scheduling.QuotaRequest{
		Resource: "gpu", Amount: 1, TaskID: "task-new", Owner: "worker-b", TTL: time.Minute,
	}); err != nil {
		t.Fatalf("回收后额度必须重新可预占: %v", err)
	}
	if got := store.Usage("gpu").Used; got != 1 {
		t.Fatalf("重新预占后用量必须是 1（实得 %d）", got)
	}
}

// controlrace_test.go —— 矩阵行十三：控制面并发介入（Cluster）。
//
// 控制面「强制释放」与 Worker「完成」并发时，必须恰好一方生效：两者都
// 落到同一份权威任务板（issue #268），输家得到可观测的 ErrLeaseLost，
// 任务最终只有一次有效状态转换（done 或 open，不得停在/分裂成第二份状态）。
package faultmatrix

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/controlplane"
	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

func runControlPlaneConcurrentIntervention(t *testing.T) {
	ctx := tenant.WithContext(context.Background(), tenant.Context{Tenant: "tenant-a"})
	board, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatalf("装配权威任务板失败: %v", err)
	}
	aud := &controlplane.MemAuditor{}
	store := controlplane.NewMemStore(aud)
	plane, err := controlplane.NewAuthoritativeStorePlane(store, aud, board, nil)
	if err != nil {
		t.Fatalf("装配权威控制面失败: %v", err)
	}

	const rounds = 20
	for i := 0; i < rounds; i++ {
		id := taskboard.TaskID(fmt.Sprintf("task-%02d", i))
		board.Seed(taskboard.Task{ID: id, State: taskboard.StateOpen})
		claimed, err := board.Claim(ctx, id, "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
		if err != nil {
			t.Fatalf("第 %d 轮认领失败: %v", i, err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			errs <- plane.ForceRelease(ctx, controlplane.ForceRelease{
				TaskID: id, Operator: "op-1", Basis: "合成并发介入判据",
			})
		}()
		go func() {
			<-start
			errs <- board.Complete(ctx, id, claimed.Lease.Token)
		}()
		close(start)

		winners, losers := 0, 0
		for range 2 {
			err := <-errs
			switch {
			case err == nil:
				winners++
			case errors.Is(err, taskboard.ErrLeaseLost), errors.Is(err, taskboard.ErrNotFound):
				losers++
			default:
				t.Fatalf("第 %d 轮落败方必须是可观测的租约丢失类错误（实得 %v）", i, err)
			}
		}
		if winners != 1 || losers != 1 {
			t.Fatalf("第 %d 轮必须恰好一方生效（winners=%d losers=%d）", i, winners, losers)
		}

		tasks, err := board.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got *taskboard.Task
		for j := range tasks {
			if tasks[j].ID == id {
				got = &tasks[j]
				break
			}
		}
		if got == nil {
			t.Fatalf("第 %d 轮任务消失", i)
		}
		switch got.State {
		case taskboard.StateDone:
			if got.Owner != "" {
				t.Fatalf("完成胜出后不得残留 owner（实得 %+v）", got)
			}
		case taskboard.StateOpen:
			if got.Owner != "" || got.Lease.Token != "" {
				t.Fatalf("强释胜出后必须是干净的 open 态（实得 %+v）", got)
			}
		default:
			t.Fatalf("并发介入后任务停在意外状态 %q（实得 %+v）——只允许一次有效转换", got.State, got)
		}
	}
}

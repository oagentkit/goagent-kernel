// longlease_test.go —— 矩阵行九：长任务租约失效（Cluster）。
//
// 长任务执行超过 TTL、续期中断后，另一副本按过期认领接管；旧持有者的
// Renew/Complete 必须被同一份任务板拒收（令牌不匹配），代次严格递增。
// 判据不读节点墙钟：过期由存储时钟（可注入替身）回答。
package faultmatrix

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/worker"
)

func runLongTaskLeaseExpiry(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	board := newBoardWithClock(t, clock)
	const id taskboard.TaskID = "task-long"
	const ttl = 30 * time.Second
	board.Seed(taskboard.Task{ID: id, State: taskboard.StateOpen})

	first, err := board.Claim(ctx, id, "worker-a", taskboard.ClaimRequest{TTL: ttl})
	if err != nil {
		t.Fatalf("首次认领失败: %v", err)
	}

	// 长任务执行超过 TTL（存储时钟推进），A 没有续期。
	clock.Advance(2 * ttl)

	took, err := worker.Recover(ctx, board, "worker-b", ttl)
	if err != nil {
		t.Fatalf("B 接管过期认领失败: %v", err)
	}
	if len(took) != 1 || took[0] != id {
		t.Fatalf("B 必须接管 A 的过期任务（实得 %v）", took)
	}
	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Owner != "worker-b" || tasks[0].State != taskboard.StateClaimed {
		t.Fatalf("接管后任务必须归 B 且仍在认领态（实得 %+v）", tasks)
	}
	if tasks[0].Lease.Fence <= first.Lease.Fence {
		t.Fatalf("接管代次必须严格递增（A=%d B=%d）", first.Lease.Fence, tasks[0].Lease.Fence)
	}

	// 旧持有者迟到的一切动作都被拒：续期、完成都不能复活过期租约。
	if _, err := board.Renew(ctx, id, first.Lease.Token, taskboard.ClaimRequest{TTL: ttl}); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("过期后旧令牌续期必须 ErrLeaseLost（实得 %v）", err)
	}
	if err := board.Complete(ctx, id, first.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("过期后旧令牌完成必须 ErrLeaseLost（实得 %v）", err)
	}

	// 新持有者正常收场。
	if err := board.Complete(ctx, id, tasks[0].Lease.Token); err != nil {
		t.Fatalf("B 完成任务失败: %v", err)
	}
	tasks, err = board.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone || tasks[0].Owner != "" {
		t.Fatalf("收场后任务必须是 done 且无 owner（实得 %+v）", tasks)
	}
}

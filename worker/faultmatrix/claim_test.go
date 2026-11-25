// claim_test.go —— 并发认领唯一性（矩阵行一，Embedded/Standalone/Cluster）。
package faultmatrix

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// runConcurrentClaimUniqueness 100 个执行者同时认领同一任务：成功者必须
// **恰好 1 个**。0 个 = 谁都领不到；>1 个 = 存储没有把「读-判-写」做成
// 一次原子操作（两个执行者都以为自己是负责人）。
//
// 用 taskboard.ConcurrentClaim 而不是手写并发循环：它是包内导出的契约判据，
// 判据本身（恰好一个赢家、输家必须 ErrTaken、其它错误不算输家）由
// taskboard 自己的用例钉住——这里复用同一份判据，不另写一套容易漏判的口径。
func runConcurrentClaimUniqueness(t *testing.T) {
	clock := newFakeClock()
	board := newBoardWithClock(t, clock)
	board.Seed(taskboard.Task{ID: "task-claim", State: taskboard.StateOpen})

	const workers = 100
	agents := make([]taskboard.AgentID, workers)
	for i := range agents {
		agents[i] = taskboard.AgentID(fmt.Sprintf("worker-%03d", i))
	}
	winners, err := taskboard.ConcurrentClaim(context.Background(), board, "task-claim", agents, time.Minute)
	if err != nil {
		t.Fatalf("并发认领判据无法给出结论: %v", err)
	}
	if winners != 1 {
		t.Fatalf("并发认领必须恰好一个赢家（实得 %d）：0=没人领到，>1=认领不是原子操作", winners)
	}

	tasks, err := board.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateClaimed {
		t.Fatalf("赢家必须把任务留在 claimed（实得 %+v）", tasks)
	}
	if tasks[0].Owner == "" || tasks[0].Lease.Token == "" || tasks[0].Lease.Fence == 0 {
		t.Fatalf("认领必须带 owner / 存储铸造的 token / 递增代次（实得 %+v）", tasks[0])
	}
}

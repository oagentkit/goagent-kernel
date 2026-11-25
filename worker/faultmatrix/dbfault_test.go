// dbfault_test.go —— 数据库故障及恢复（矩阵行三，Standalone/Cluster）。
package faultmatrix

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// runDBOutageRecovery 数据库整体不可用期间：
//
//   - 读面/写面都必须**显式失败**——List 返回空快照会被调用方误读成
//     「板上没有任务」，Claim 静默成功则等于凭幻觉认领；
//   - 存储故障**不得**被判定成租约资格丢失（ErrLeaseLost）：把 DB 抖动
//     当成「闸已易主」，在存储恢复前就会误杀在途执行；
//   - 恢复后同一租约可继续续期/收场，认领也重新可用。
func runDBOutageRecovery(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	backend := &faultBackend{MemoryBackend: coord.NewMemoryBackend()}
	board, err := coord.NewBoardAdapter(backend)
	if err != nil {
		t.Fatalf("装配任务板失败: %v", err)
	}
	board.UseClock(clock.Now)
	board.Seed(taskboard.Task{ID: "task-db", State: taskboard.StateOpen})

	// ── 故障期：显式失败，不给「空结果即无事」的假绿。 ──
	backend.down.Store(true)
	if tasks, listErr := board.List(ctx); listErr == nil {
		t.Fatalf("DB 故障期 List 必须显式失败，不得返回快照（实得 %v）", tasks)
	}
	if _, claimErr := board.Claim(ctx, "task-db", "worker-a", taskboard.ClaimRequest{TTL: time.Minute}); claimErr == nil {
		t.Fatal("DB 故障期 Claim 必须显式失败，不得静默成功")
	}

	// ── 恢复：认领重新可用。 ──
	backend.down.Store(false)
	claimed, err := board.Claim(ctx, "task-db", "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("DB 恢复后必须可认领: %v", err)
	}

	// ── 故障期续期：存储错误不是资格丢失。 ──
	backend.down.Store(true)
	_, renewErr := board.Renew(ctx, "task-db", claimed.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute})
	if renewErr == nil {
		t.Fatal("DB 故障期 Renew 必须显式失败")
	}
	if errors.Is(renewErr, taskboard.ErrLeaseLost) {
		t.Fatalf("存储故障不得伪装成 ErrLeaseLost（否则存储恢复前就误杀在途）: %v", renewErr)
	}

	// ── 恢复后续期并收场：同一令牌仍是有效凭据。 ──
	backend.down.Store(false)
	clock.Advance(10 * time.Second) // 仍在 TTL 内
	if _, err := board.Renew(ctx, "task-db", claimed.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatalf("DB 恢复后同一租约必须可续期: %v", err)
	}
	if err := board.Complete(ctx, "task-db", claimed.Lease.Token); err != nil {
		t.Fatalf("DB 恢复后必须能正常收场: %v", err)
	}
	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
		t.Fatalf("恢复后任务必须正常完成（实得 %+v）", tasks)
	}
}

package pgstore

// pg_board_gate_test.go —— PostgreSQL 后端任务认领面的两条闸
//（issue #360 / #379）：词表外状态必须被拒（ErrUnknownState）且不得铸令牌；
// 返回值不得与板内共享 Deps 底层数组（拷贝义务）。
//
// # 跳过与硬失败的边界
//
// 场景本身**硬失败**（本文件没有任何场景级 skip）：唯一的 skip 发生在环境
// 入口（requireDSN）——没有可连的 PostgreSQL 时无从构造被测实现，跳过时
// 写明原因与起法，与「静默跳过伪装成通过」是两回事。

import (
	"context"
	"testing"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// dirtyState 词表外的任务状态（内核读不懂 ⇒ 写路径必须拒绝认领）。
const dirtyState = taskboard.State("weird_state")

// TestBoardClaimRejectsUnknownState 词表外状态必须报 ErrUnknownState，
// 且不得铸令牌、不得改写状态（落库记录仍是原状态）。
func TestBoardClaimRejectsUnknownState(t *testing.T) {
	dsn := requireDSN(t)
	_, _, board, _ := freshStores(t, dsn)
	board.Seed(taskboard.Task{ID: "t1", State: dirtyState})

	_, err := board.Claim(context.Background(), "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if !oerrors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("词表外状态必须报 taskboard.ErrUnknownState，实得 %v", err)
	}

	tasks, lerr := board.List(context.Background())
	if lerr != nil {
		t.Fatalf("读板失败: %v", lerr)
	}
	for _, task := range tasks {
		if task.State != dirtyState {
			t.Fatalf("被拒的认领不得改写状态，实得 %q（应为 %q）", task.State, dirtyState)
		}
		if task.Owner != "" || task.Lease.Token != "" {
			t.Fatalf("被拒的认领不得铸令牌/占位：owner=%q token=%q", task.Owner, task.Lease.Token)
		}
	}
}

// TestBoardCopyObligation 返回的 Task 不得与板内共享 Deps 底层数组。
func TestBoardCopyObligation(t *testing.T) {
	dsn := requireDSN(t)
	_, _, board, _ := freshStores(t, dsn)
	boardtest.RunCopyObligation(t, board)
}

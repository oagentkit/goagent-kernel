package sqlitestore

// board_gate_test.go —— 文件后端任务认领面的两条闸（issue #360 / #379）：
// 词表外状态必须被拒（ErrUnknownState）且不得铸令牌；返回值不得与板内共享
// Deps 底层数组（拷贝义务）。

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

// TestClaimRejectsUnknownState 词表外状态必须报 ErrUnknownState，
// 且不得铸令牌、不得改写状态。
func TestClaimRejectsUnknownState(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Seed(taskboard.Task{ID: "t1", State: dirtyState})

	_, err = s.Claim(context.Background(), "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if !oerrors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("词表外状态必须报 taskboard.ErrUnknownState，实得 %v", err)
	}

	// 被拒的认领不得留下任何占用痕迹（落库记录仍是原状态）。
	tasks, lerr := s.List(context.Background())
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

// TestCopyObligation 返回的 Task 不得与板内共享 Deps 底层数组。
func TestCopyObligation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	boardtest.RunCopyObligation(t, s)
}

package main

// board_gate_test.go —— 任务认领面的两条闸（issue #360 / #379）：
//
//   - 词表外状态必须被拒（ErrUnknownState）且**不得铸令牌**；
//   - 返回的 Task 不得与板内共享 Deps 底层数组（拷贝义务）。
//
// 两条都是**参考实现**必须过的契约判据：宿主照抄这份实现，两条闸照抄不到
// 就得自己重写——而漏掉任何一条的失效形态都是绿的（认领成功、令牌可用、
// 副作用照常施加；或依赖图被调用方改穿），没有任何告警。

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// dirtyState 词表外的任务状态（内核读不懂 ⇒ 写路径必须拒绝认领）。
const dirtyState = taskboard.State("weird_state")

// TestBoardClaimRejectsUnknownState 词表外状态必须报 ErrUnknownState，
// 且不得铸令牌、不得改写状态。
func TestBoardClaimRejectsUnknownState(t *testing.T) {
	b := newBoardStore()
	b.Seed(taskboard.Task{ID: "t1", State: dirtyState})

	_, err := b.Claim(context.Background(), "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if !errors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("词表外状态必须报 taskboard.ErrUnknownState，实得 %v", err)
	}

	// 被拒的认领不得留下任何占用痕迹：状态原样、无 owner、无令牌。
	tasks, lerr := b.List(context.Background())
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
//
// 共享的失效形态是绿的：调用方改写返回值的 Deps[0]，板内的依赖图被改穿，
// 依赖判定读到的依赖被换掉——板自己毫无察觉。
func TestBoardCopyObligation(t *testing.T) {
	boardtest.RunCopyObligation(t, newBoardStore())
}

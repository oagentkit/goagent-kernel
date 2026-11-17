package boardtest

// claimgate_test.go —— 写路径的**单一判据**与拷贝义务
// （issue #360 / #379）。
//
// 这两条是对**实现**的要求（不是对词表的要求）：
//
//   - 词表外状态 ⇒ Claim 必须报 ErrUnknownState，且**不得铸令牌**；
//   - List / Claim 返回的 Task 不得与实现内部共享 Deps 底层数组。
//
// 参考实现（memBoard）必须自己先过这两条：套件验证的是**契约**，
// 而套件自身的正例若不满足契约，整套判据就是在给一个违规实现背书。

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestClaimRejectsUnknownState 词表外状态必须被拒，且**不得铸令牌**。
func TestClaimRejectsUnknownState(t *testing.T) {
	b := rawMemBoard()
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.State("weird_state")})

	_, err := b.Claim(context.Background(), "t1", "a1", taskboard.ClaimRequest{TTL: time.Minute})
	if !errors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	// 不得铸令牌、不得改写状态：被拒的认领不得留下占用痕迹。
	tasks, lerr := b.List(context.Background())
	if lerr != nil {
		t.Fatalf("列出任务失败: %v", lerr)
	}
	for _, task := range tasks {
		if task.ID != "t1" {
			continue
		}
		if task.Lease.Token != "" || task.Owner != "" || task.State != taskboard.State("weird_state") {
			t.Fatalf("被拒的认领不得铸令牌或改写状态：state=%q owner=%q token=%q",
				task.State, task.Owner, task.Lease.Token)
		}
		return
	}
	t.Fatal("板内找不到 t1（判据前提不成立）")
}

// TestCopyObligation 参考实现必须过拷贝义务判据（issue #379）。
func TestCopyObligation(t *testing.T) {
	RunCopyObligation(t, rawMemBoard())
}

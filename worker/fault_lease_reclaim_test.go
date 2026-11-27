// fault_lease_reclaim_test.go —— 插点 G2/F1（领取 → 执行）的正向崩溃注入
// （issue #523）：领取完成后、执行开始之前进程死掉（没有任何续期动作），
// 租约过期后任务必须重新可被领取，且旧持有者的一切后续提交在「过期窗口」
// 与「已被接管」两个时点都被拒绝。
//
// 时间全部走注入时钟推进（newTestClock + BoardAdapter.UseClock）：租约过期
// 是被测事实，断言的尺子是「时钟推过 TTL」这个动作本身，不睡墙钟。
package worker

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestFaultClaimedWorkerDiesBeforeExecuteReclaim 领取完成 → 进程死（执行前）
// → 注入时钟推过 TTL → 任务重新可领 → 旧持有者的提交被拒。
//
// 期望失败形态（坏实现必红的对照）：
//   - 过期后任务仍被占死（无人可领）⇒ 第二步领取红；
//   - 过期持有者仍能提交终态（Complete 不判有效期）⇒ 过期窗口的提交断言红；
//   - 接管后旧令牌仍能收口（Complete 只认状态不认令牌）⇒ 接管后的提交断言红。
func TestFaultClaimedWorkerDiesBeforeExecuteReclaim(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	board := newTestBoard(clock)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	// A 领取成功（受管执行器的第一步）。此后 A 死在执行之前：不开会话、
	// 不续期、不执行——板上只留下这次认领铸造的凭据。
	taskA, err := board.Claim(ctx, "t1", "w-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("领取是被测前置，必须成功: %v", err)
	}
	if taskA.State != taskboard.StateClaimed || taskA.Lease.Token == "" || taskA.Lease.Fence == 0 {
		t.Fatalf("认领必须留下认领态与存储铸造的令牌/代次: %+v", taskA)
	}

	// 租约过期：注入时钟推过 TTL（A 已死，无人续期）。
	clock.Advance(2 * time.Minute)

	// ① 过期窗口内（尚未被接管）：A 的迟到提交必须被拒——资格在到期时刻
	// 终止；拒绝后任务保持原状，可被接管，不得被改成终态。
	if err := board.Complete(ctx, "t1", taskA.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("过期持有者的提交必须被拒（ErrLeaseLost），实得 %v", err)
	}
	if _, err := board.Renew(ctx, "t1", taskA.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("过期持有者的续期必须被拒（ErrLeaseLost），实得 %v", err)
	}
	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateClaimed || tasks[0].Owner != "w-a" {
		t.Fatalf("被拒的提交不得改动任务（仍是原认领态，可被接管）: %+v", tasks)
	}

	// ② 租约过期后任务重新可被 B 领取（重启方接管），代次严格递增。
	taskB, err := board.Claim(ctx, "t1", "w-b", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("租约过期后任务必须重新可领: %v", err)
	}
	if taskB.Lease.Fence <= taskA.Lease.Fence {
		t.Fatalf("接管必须推进存储代次（A=%d，B=%d）", taskA.Lease.Fence, taskB.Lease.Fence)
	}

	// ③ 接管之后：A 的迟到提交凭旧令牌必须仍被拒（令牌不匹配），且不
	// 波及现任持有者。
	if err := board.Complete(ctx, "t1", taskA.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("被接管后旧令牌的提交必须被拒（ErrLeaseLost），实得 %v", err)
	}

	// ④ B 凭自己的令牌正常收场；板上恰好一行、恰好转为终态。
	if err := board.Complete(ctx, "t1", taskB.Lease.Token); err != nil {
		t.Fatalf("现任持有者凭自己的令牌应能收场: %v", err)
	}
	tasks, err = board.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
		t.Fatalf("收场后板上应恰好一行终态（重领不产生重复任务）: %+v", tasks)
	}
}

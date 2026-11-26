// complete.go —— 「租约过期 ⇒ 不得提交终态」的契约场景（issue #497）。
//
// # 它补的是哪一段
//
// 套件原先只钉了「过期**接管后**旧持有者提交被拒」——那条路径上令牌已经换代，
// 靠的是「令牌不匹配」。真正漏掉的是**过期但还没有人接管**的那个窗口：令牌
// 仍然匹配，实现若只判「状态 + 令牌」，旧持有者照样能把任务标记完成 ⇒ 终态
// 内容取决于「谁先提交」而不是「谁有资格提交」，而两者在同一任务上往往给出
// 不同结论。
//
// 与 Renew 同口径（renew.go 的路径三）：资格在到期时刻终止。
//
// # 为什么 Release 不在这条判据里
//
// Release 是**自愿放弃**、不写终态：过期持有者归还不产生错误结论（它至多让
// 任务回到 open 让别人来领）。把 Release 一并钉住等于改掉「归还是自愿」这条
// 语义，而它不是本单要改的东西。故这条判据只钉 Complete。
package boardtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// checkCompleteRejectsExpiredLease 租约过期后旧持有者提交终态被拒（issue #497）。
//
// 场景：t1 认领（TTL 1min）→ 时钟前进 2min（租约已过期，**尚未被接管**）→
// 旧持有者 Complete 必须报 ErrLeaseLost，且**不做任何修改**（任务仍是认领态，
// 可被接管）→ 接管者能认领并由**它**完成。
//
// 「不做任何修改」这半条同样重要：拒绝若顺手把任务标成完成，错误就从「提交
// 了不该提交的东西」变成「提交了假结论」——后者更难发现。
func checkCompleteRejectsExpiredLease(ctx context.Context, b taskboard.Board, clock *FakeClock) error {
	if err := seedBoard(b, taskboard.Task{ID: "t1", State: taskboard.StateOpen}); err != nil {
		return err
	}
	first, err := b.Claim(ctx, "t1", "a1", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: 首次认领应当成功")
	}
	// 租约到期，但**还没有别的持有者接管**——令牌仍然匹配。
	clock.Advance(2 * time.Minute)
	if err := b.Complete(ctx, "t1", first.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		return fmt.Errorf("boardtest: 租约过期后旧持有者的完成必须报 ErrLeaseLost（实得 %v）——"+
			"放行的话，终态内容取决于「谁先提交」而不是「谁有资格提交」", err)
	}
	// 拒绝必须**不做任何修改**：任务仍是认领态（可被接管），不是被标成完成。
	cur, err := taskByID(b, ctx, "t1")
	if err != nil {
		return err
	}
	if cur.State != taskboard.StateClaimed {
		return fmt.Errorf("boardtest: 过期完成被拒后任务必须仍是认领态（实得 %q）——"+
			"拒绝若顺手写成终态，错误就从「提交了不该提交的东西」变成「提交了假结论」", cur.State)
	}
	// 接管者能认领（过期租约可被接管），并由**它**完成——终态归有资格的人。
	takeover, err := b.Claim(ctx, "t1", "a2", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: 过期租约必须可被接管")
	}
	if err := b.Complete(ctx, "t1", takeover.Lease.Token); err != nil {
		return errors.Wrap(err, "boardtest: 接管者的完成应当成功")
	}
	return nil
}

func runCompleteRejectsExpiredLease(t *testing.T, factory Factory) {
	t.Helper()
	b, clock := newBoard(t, factory)
	if err := checkCompleteRejectsExpiredLease(context.Background(), b, clock); err != nil {
		t.Fatal(err)
	}
}

// taskByID 从快照里取一条任务。
func taskByID(b taskboard.Board, ctx context.Context, id taskboard.TaskID) (taskboard.Task, error) {
	tasks, err := b.List(ctx)
	if err != nil {
		return taskboard.Task{}, errors.Wrap(err, "boardtest: 读快照应当成功")
	}
	for _, tk := range tasks {
		if tk.ID == id {
			return tk, nil
		}
	}
	return taskboard.Task{}, errors.New("boardtest: 任务 " + string(id) + " 不在快照里——" +
		"拒绝过期完成时不得改写或删除记录")
}

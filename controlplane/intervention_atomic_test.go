// intervention_atomic_test.go —— 权威强释的原子路径探测与两步回退判据。
package controlplane

import (
	"context"
	"testing"

	kerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/tenant"
)

// atomicTaskBoard 实现 ForceReleaseAtomic 的任务权威替身：
// 同事务语义由它自己保证（这里记录调用面供断言），审计失败时它**不放行释放**
// （模拟同一事务回滚）。
type atomicTaskBoard struct {
	memTaskBoard
	calls        int // ForceReleaseWithAudit 被调次数
	releaseCalls int // 分步 Release 被调次数（原子路径下必须为 0）
	auditErr     error
}

func (b *atomicTaskBoard) ForceReleaseWithAudit(_ context.Context, id taskboard.TaskID, token string, entry AuditEntry) error {
	b.calls++
	if b.auditErr != nil {
		// 同一事务回滚的语义等价物：审计失败 ⇒ 释放不放行。
		return b.auditErr
	}
	return b.memTaskBoard.Release(context.Background(), id, token)
}

func (b *atomicTaskBoard) Release(ctx context.Context, id taskboard.TaskID, token string) error {
	b.releaseCalls++
	return b.memTaskBoard.Release(ctx, id, token)
}

// memTaskBoard 最小的 TaskAuthority 参考替身（单任务内存板）。
type memTaskBoard struct {
	task    taskboard.Task
	source  string
	listErr error
}

func (b *memTaskBoard) List(_ context.Context) ([]taskboard.Task, error) {
	if b.listErr != nil {
		return nil, b.listErr
	}
	return []taskboard.Task{b.task}, nil
}

func (b *memTaskBoard) Release(_ context.Context, id taskboard.TaskID, token string) error {
	if b.task.ID != id || b.task.Lease.Token != token || b.task.State != taskboard.StateClaimed {
		return taskboard.ErrLeaseLost
	}
	b.task.State = taskboard.StateOpen
	b.task.Lease = taskboard.Lease{}
	return nil
}

func (b *memTaskBoard) TaskSourceID() string { return b.source }

func claimedBoardTask() taskboard.Task {
	return taskboard.Task{
		ID: "t1", State: taskboard.StateClaimed, Title: "合成任务",
		Lease: taskboard.Lease{Token: "tok-1", Fence: 1},
	}
}

func newAtomicTestPlane(t *testing.T, tasks TaskAuthority) *StorePlane {
	t.Helper()
	p, err := NewAuthoritativeStorePlane(NewMemStore(&MemAuditor{}), &MemAuditor{}, tasks, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestForceReleasePrefersAtomicPath 探测判据：任务权威实现 ForceReleaseAtomic
// ⇒ StorePlane 走原子路径（只调 ForceReleaseWithAudit，不碰分步 Release）。
func TestForceReleasePrefersAtomicPath(t *testing.T) {
	board := &atomicTaskBoard{memTaskBoard: memTaskBoard{task: claimedBoardTask()}}
	p := newAtomicTestPlane(t, board)

	err := p.ForceRelease(tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"}), ForceRelease{
		TaskID: "t1", Operator: "op", Basis: "合成判据",
	})
	if err != nil {
		t.Fatalf("原子路径强释失败: %v", err)
	}
	if board.calls != 1 {
		t.Fatalf("原子路径应恰好调一次 ForceReleaseWithAudit，got %d", board.calls)
	}
	if board.releaseCalls != 0 {
		t.Fatalf("原子路径不得再调分步 Release，got %d", board.releaseCalls)
	}
	if board.task.State != taskboard.StateOpen {
		t.Fatalf("强释后任务应回 open，got %s", board.task.State)
	}
}

// TestForceReleaseAtomicAuditFailureKeepsState 原子路径的窗口闭合判据：
// 审计写失败 ⇒ 释放不放行（实现方同一事务回滚），状态一字不动。
func TestForceReleaseAtomicAuditFailureKeepsState(t *testing.T) {
	board := &atomicTaskBoard{
		memTaskBoard: memTaskBoard{task: claimedBoardTask()},
		auditErr:     kerrors.New("审计落库失败（替身注入）"),
	}
	p := newAtomicTestPlane(t, board)

	err := p.ForceRelease(tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"}), ForceRelease{
		TaskID: "t1", Operator: "op", Basis: "合成判据",
	})
	if err == nil {
		t.Fatal("审计失败必须上抛，不得静默成功")
	}
	if board.task.State != taskboard.StateClaimed {
		t.Fatalf("审计失败 ⇒ 释放必须回滚：任务仍应 claimed，got %s", board.task.State)
	}
}

// TestForceReleaseFallsBackToTwoStep 未实现接口的任务权威保留两步形态：
// 先分步 Release、后 appendAudit（行为兼容，窗口如实存在）。
func TestForceReleaseFallsBackToTwoStep(t *testing.T) {
	board := &memTaskBoard{task: claimedBoardTask()}
	p := newAtomicTestPlane(t, board)

	err := p.ForceRelease(tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"}), ForceRelease{
		TaskID: "t1", Operator: "op", Basis: "合成判据",
	})
	if err != nil {
		t.Fatalf("两步形态强释失败: %v", err)
	}
	if board.task.State != taskboard.StateOpen {
		t.Fatalf("两步形态释放应生效，got %s", board.task.State)
	}
}

// TestForceReleaseAuditorCheckedBeforeMutation 留痕面缺位 ⇒ 介入在任何状态
// 变更之前被拒：任务仍 claimed，且原子路径不被调用。
func TestForceReleaseAuditorCheckedBeforeMutation(t *testing.T) {
	board := &atomicTaskBoard{memTaskBoard: memTaskBoard{task: claimedBoardTask()}}
	store := NewMemStore(nil)
	p, err := NewAuthoritativeStorePlane(store, nil, board, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = p.ForceRelease(tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"}), ForceRelease{
		TaskID: "t1", Operator: "op", Basis: "合成判据",
	})
	if !kerrors.Is(err, ErrNoAuditor) {
		t.Fatalf("缺留痕面必须 ErrNoAuditor，got %v", err)
	}
	if board.task.State != taskboard.StateClaimed {
		t.Fatalf("拒绝不得改动状态：任务仍应 claimed，got %s", board.task.State)
	}
	if board.calls != 0 {
		t.Fatalf("拒绝不得触碰原子路径，got %d 次调用", board.calls)
	}
}

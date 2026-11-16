// memstore_board_test.go —— MemStore 作为 taskboard 参考实现的契约面用例
// （issue #335）。参考实现必须能被共享契约套件直接跑：判据一旦为参考实现
// 单独写一套，两边就会漂移。
package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// memStoreBoard 把 MemStore 适配成 boardtest.ClockedBoard：套件需要一个
// 摆初始任务的入口（Seed），且 Board.Claim 收 ClaimRequest 而 MemStore 的
// 运行时面收 TTL（两者语义相同，字段形态不同）。判据本体仍跑 MemStore 的
// Claim/Renew/Release/Complete——适配层只做字段换算与场景摆盘。
type memStoreBoard struct{ *MemStore }

func (b *memStoreBoard) Claim(ctx context.Context, id taskboard.TaskID,
	agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	return b.MemStore.Claim(ctx, id, agent, req.TTL)
}

func (b *memStoreBoard) Seed(tasks ...taskboard.Task) {
	b.MemStore.mu.Lock()
	defer b.MemStore.mu.Unlock()
	b.MemStore.ensureLocked()
	for _, t := range tasks {
		if t.State == "" {
			t.State = taskboard.StateOpen
		}
		b.MemStore.tasks[t.ID] = &memTask{task: cloneTask(t), gen: t.Lease.Fence}
	}
}

func (b *memStoreBoard) UseClock(now func() time.Time) { b.MemStore.UseClock(now) }

// TestMemStoreBoardContract 共享契约套件（含过期接管、续期、并发认领）。
func TestMemStoreBoardContract(t *testing.T) {
	boardtest.Run(t, func(_ *testing.T, _ *boardtest.FakeClock) boardtest.ClockedBoard {
		return &memStoreBoard{MemStore: NewMemStore(&MemAuditor{})}
	})
}

// TestMemStoreClaimHonorsExpiredLease issue #335 的核心回归：租约过期后
// 第二次 Claim 必须成功（持有者崩溃后任务可被接管），且换新令牌、新代次。
// 旧行为只看 State == claimed，租约早已过期仍报 ErrTaken——任务永久占用。
func TestMemStoreClaimHonorsExpiredLease(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := NewMemStore(&MemAuditor{})
	m.UseClock(func() time.Time { return now })
	if err := m.SeedRun("r1", "", []taskboard.Task{{ID: "t1", State: taskboard.StateOpen}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	ctx := context.Background()

	first, err := m.Claim(ctx, "t1", "a1", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	// 租约未过期：仍被活着持有。
	if _, err := m.Claim(ctx, "t1", "a2", 50*time.Millisecond); !errors.Is(err, taskboard.ErrTaken) {
		t.Fatalf("租约有效时必须 ErrTaken, got %v", err)
	}

	now = now.Add(150 * time.Millisecond) // 租约已过期，持有者不再心跳（崩溃）
	second, err := m.Claim(ctx, "t1", "a2", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("租约过期后必须可被接管（旧行为报 ErrTaken = 永久占用）: %v", err)
	}
	if second.Lease.Token == first.Lease.Token {
		t.Fatal("接管必须铸造新令牌")
	}
	if second.Lease.Fence <= first.Lease.Fence {
		t.Fatalf("接管代次必须严格递增（%d → %d）", first.Lease.Fence, second.Lease.Fence)
	}
	if second.Owner != "a2" {
		t.Fatalf("新持有者应为 a2, got %q", second.Owner)
	}
	// 旧持有者的迟到提交被拒（不波及新持有者）。
	if err := m.Complete(ctx, "t1", first.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("旧令牌完成必须 ErrLeaseLost, got %v", err)
	}
	if err := m.Complete(ctx, "t1", second.Lease.Token); err != nil {
		t.Fatalf("新持有者完成应成功: %v", err)
	}
}

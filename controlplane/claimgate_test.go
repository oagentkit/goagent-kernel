// claimgate_test.go —— 认领闸：词表外状态必须被拒，且不得铸令牌（issue #360）。
//
// 契约层的单一判据是 taskboard.State.Claimable（写路径只判终态的实现会
// 让脏状态行领到租约并铸令牌，失效形态是绿的：认领成功、令牌可用、
// 副作用照常施加，没有任何错误也没有告警）。
package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// dirtyState 词表外状态（脏状态行：跨版本存储 / 宿主自造状态名）。
const dirtyState = taskboard.State("weird_state")

// TestMemStoreClaimRejectsUnknownState MemStore.Claim 对词表外状态必须报
// ErrUnknownState，且**不得铸令牌、不得改写状态**。
func TestMemStoreClaimRejectsUnknownState(t *testing.T) {
	m := NewMemStore(&MemAuditor{})
	if err := m.SeedRun("r1", "", []taskboard.Task{{ID: "t1", State: dirtyState}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	ctx := context.Background()

	if _, err := m.Claim(ctx, "t1", "a1", time.Minute); !errors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	assertNoClaimTrace(t, m, "t1")
}

// TestMemPlaneClaimRejectsUnknownState MemPlane.Claim 走同一道闸（它是最小
// 运行时面，宿主照它接线）。
func TestMemPlaneClaimRejectsUnknownState(t *testing.T) {
	m := NewMemPlane(&MemAuditor{})
	if err := m.SeedRun("r1", "", []taskboard.Task{{ID: "t1", State: dirtyState}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	ctx := context.Background()

	if _, err := m.Claim(ctx, "t1", "a1", time.Minute); !errors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	assertNoClaimTrace(t, m.store, "t1")
}

// assertNoClaimTrace 被拒的认领不得留下占用痕迹（状态、负责人、令牌）。
func assertNoClaimTrace(t *testing.T, m *MemStore, id taskboard.TaskID) {
	t.Helper()
	snap, err := m.SnapshotTask(context.Background(), id)
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	if snap.Task.State != dirtyState {
		t.Fatalf("被拒的认领不得改写状态：%q → %q", dirtyState, snap.Task.State)
	}
	if snap.Task.Owner != "" || snap.Task.Lease.Token != "" || snap.Task.Lease.Fence != 0 {
		t.Fatalf("被拒的认领不得铸令牌/指派负责人：owner=%q lease=%+v",
			snap.Task.Owner, snap.Task.Lease)
	}
}

// TestMemStoreClaimRejectsTerminalState 终态仍报 ErrTerminal（闸的顺序：
// 先判读不懂，再判终态——把「读不懂」报成「终态」会把宿主导向错误的
// 排查方向）。
func TestMemStoreClaimRejectsTerminalState(t *testing.T) {
	m := NewMemStore(&MemAuditor{})
	if err := m.SeedRun("r1", "", []taskboard.Task{{ID: "t1", State: taskboard.StateDone}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	_, err := m.Claim(context.Background(), "t1", "a1", time.Minute)
	if !errors.Is(err, taskboard.ErrTerminal) {
		t.Fatalf("终态必须报 ErrTerminal，实得 %v", err)
	}
	if errors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("终态不是「读不懂」，不得报 ErrUnknownState：%v", err)
	}
}

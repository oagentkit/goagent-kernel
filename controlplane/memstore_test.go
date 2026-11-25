// memstore_test.go —— Store 参考实现的版本锚与留痕原子性用例。
//
// 这两条是 Store 契约的硬要求，StorePlane 的用例只覆盖了「从 Plane 看」
// 的一面；这里直接对 Store 判：快照之后目标被改过 ⇒ 提交必失败且零写入；
// 留痕失败 ⇒ 状态与版本一字不动。
package controlplane

import (
	"context"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// TestMemStoreVersionConflict 钉版本锚：旧快照的提交必须 ErrStoreConflict，
// 且不写入；重新取快照后同一笔变更可以提交。
func TestMemStoreVersionConflict(t *testing.T) {
	aud := &MemAuditor{}
	st := seededStore(t, aud)
	ctx := context.Background()

	old, err := st.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	// 另一副本（或运行时）先改了一笔：全局版本前进。
	if err := st.SeedEffect("effect-2", false); err != nil {
		t.Fatalf("SeedEffect: %v", err)
	}

	next := old.Task
	next.State = taskboard.StateFailed
	next.Seq++
	entry := AuditEntry{Operator: "op-1", Action: ActionForceRelease, Target: "t1", Basis: "b"}
	err = st.CommitTask(ctx, old.Version, next, entry)
	if !errors.Is(err, ErrStoreConflict) {
		t.Fatalf("旧版本提交必须 ErrStoreConflict（实得 %v）", err)
	}
	// 零写入：任务仍是 seed 时的 open 状态。
	after, err := st.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask after: %v", err)
	}
	if after.Task.State != taskboard.StateOpen || after.Task.Seq != old.Task.Seq {
		t.Fatalf("被拒的提交不得写入： %+v -> %+v", old.Task, after.Task)
	}

	// 用新快照重判后提交成功。
	next = after.Task
	next.State = taskboard.StateFailed
	next.Seq++
	if err := st.CommitTask(ctx, after.Version, next, entry); err != nil {
		t.Fatalf("新版本提交: %v", err)
	}
	final, err := st.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask final: %v", err)
	}
	if final.Task.State != taskboard.StateFailed {
		t.Fatalf("提交后的状态漂移： %+v", final.Task)
	}
}

// TestMemStoreAuditFailureAtomic 钉「留痕失败即整体失败」：Commit 的状态
// 与版本都不得前进（含运行与效果键两种记录）。
func TestMemStoreAuditFailureAtomic(t *testing.T) {
	broken := brokenAuditor{}
	st := seededStore(t, broken)
	ctx := context.Background()

	run0, err := st.SnapshotRun(ctx, "r1")
	if err != nil {
		t.Fatalf("SnapshotRun: %v", err)
	}
	eff0, err := st.SnapshotEffect(ctx, "effect-1")
	if err != nil {
		t.Fatalf("SnapshotEffect: %v", err)
	}
	entry := AuditEntry{Operator: "op-1", Action: ActionTerminate, Target: "r1", Basis: "b"}

	if err := st.CommitRun(ctx, run0.Version, "r1", RunState{Status: "cancelled", Gen: run0.Gen + 1}, entry); err == nil {
		t.Fatal("留痕失败时 CommitRun 必须失败")
	}
	run, err := st.SnapshotRun(ctx, "r1")
	if err != nil {
		t.Fatalf("SnapshotRun after: %v", err)
	}
	if run.Version != run0.Version || run.Status != run0.Status || run.Gen != run0.Gen {
		t.Fatalf("留痕失败改了运行状态/版本： %+v -> %+v", run0, run)
	}

	if err := st.CommitEffect(ctx, eff0.Version, "effect-1", EffectState{Uncertain: false, Resolved: VerdictApplied}, entry); err == nil {
		t.Fatal("留痕失败时 CommitEffect 必须失败")
	}
	eff, err := st.SnapshotEffect(ctx, "effect-1")
	if err != nil {
		t.Fatalf("SnapshotEffect after: %v", err)
	}
	if eff.Version != eff0.Version || eff.Effect != eff0.Effect {
		t.Fatalf("留痕失败改了裁决账/版本： %+v -> %+v", eff0, eff)
	}
}

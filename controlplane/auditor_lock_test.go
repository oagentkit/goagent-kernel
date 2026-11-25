// auditor_lock_test.go —— 宿主留痕接缝的并发面（issue #327）。
//
// MemStore 是控制面的参考实现兼形态示范：留痕 Append 若在 m.mu 内调用，
// 宿主审计（生产形态是 DB 落盘，且常要回查被审计状态）会与全库查询/
// 提交互相排队，回查 Snapshot* 更是同 goroutine 二次抢锁死锁。判据：
//   - Append 里回查 Snapshot* 必须自行返回；
//   - 慢 Append 不得拖住 Snapshot*（等待时间与阻塞时长解耦）；
//   - Append panic 被隔离为 ErrAuditorPanic，状态一字不动。
package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// funcAuditor 用函数做 Append 的留痕替身。
type funcAuditor struct {
	appendFn func(context.Context, AuditEntry) error
}

func (a *funcAuditor) Append(ctx context.Context, e AuditEntry) error {
	return a.appendFn(ctx, e)
}

func (a *funcAuditor) Entries(context.Context, string) ([]AuditEntry, error) { return nil, nil }

// seedAuditedStore 建一个 r1/t1 的最小场景。
func seedAuditedStore(t *testing.T, aud Auditor) *MemStore {
	t.Helper()
	m := NewMemStore(aud)
	if err := m.SeedRun("r1", contract.StatusRunning,
		[]taskboard.Task{{ID: "t1", Title: "原始", State: taskboard.StateOpen}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	return m
}

func commitTaskEntry() AuditEntry {
	return AuditEntry{Operator: "op-1", Action: ActionForceRelease, Target: "t1", Basis: "b"}
}

// TestMemStoreAuditorMayQuerySnapshots Append 内回查 SnapshotTask 不得死锁。
func TestMemStoreAuditorMayQuerySnapshots(t *testing.T) {
	m := NewMemStore(nil)
	aud := &funcAuditor{appendFn: func(ctx context.Context, _ AuditEntry) error {
		// 审计行要带「改动前快照」是最自然的实现——它必须在锁外可读。
		_, err := m.SnapshotTask(ctx, "t1")
		return err
	}}
	m.aud = aud
	if err := m.SeedRun("r1", contract.StatusRunning,
		[]taskboard.Task{{ID: "t1", State: taskboard.StateOpen}}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}

	ctx := context.Background()
	snap, err := m.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	next := snap.Task
	next.Title = "已强释"
	errCh := make(chan error, 1)
	go func() { errCh <- m.CommitTask(ctx, snap.Version, next, commitTaskEntry()) }()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("CommitTask: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Auditor.Append 回查 SnapshotTask 时死锁（留痕仍在状态锁内）")
	}
}

// TestMemStoreSlowAuditorDoesNotBlockSnapshots 慢 Append 期间 Snapshot* 必须快速返回。
func TestMemStoreSlowAuditorDoesNotBlockSnapshots(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	m := seedAuditedStore(t, &funcAuditor{appendFn: func(context.Context, AuditEntry) error {
		close(entered)
		<-release
		return nil
	}})
	ctx := context.Background()
	snap, err := m.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	next := snap.Task
	next.Title = "已强释"
	commitDone := make(chan error, 1)
	go func() { commitDone <- m.CommitTask(ctx, snap.Version, next, commitTaskEntry()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Auditor.Append 未被调用")
	}

	snapDone := make(chan error, 1)
	go func() {
		_, serr := m.SnapshotTask(ctx, "t1")
		snapDone <- serr
	}()
	// 尺子 = **可观测事实**，不是墙钟上界（issue #481）：慢 Auditor 在
	// close(release) 之前一直持着状态锁 ⇒ Snapshot 在它**仍持锁时**就返回了
	// ⇒ 它没有等这把锁（被阻塞住的实现根本回不来，会落进下面的 2s 上限）。
	//
	// 改写前这里还有一句「耗时 > 100ms 即红」的墙钟上界：它量的是「这台机器
	// 此刻有多快」（机器一忙就假红），而它想拦的形态——「被慢 Auditor 阻塞」——
	// 已经由上面这个事实完全覆盖 ⇒ 删掉。
	select {
	case serr := <-snapDone:
		if serr != nil {
			t.Fatalf("慢留痕期间 SnapshotTask 失败: %v", serr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Snapshot 被慢 Auditor 阻塞超过 2s——留痕仍在状态锁内")
	}
	close(release)
	if err := <-commitDone; err != nil {
		t.Fatalf("CommitTask: %v", err)
	}
}

// TestMemStoreAuditorPanicIsolated panic 被隔离成 ErrAuditorPanic，状态不变。
func TestMemStoreAuditorPanicIsolated(t *testing.T) {
	m := seedAuditedStore(t, &funcAuditor{appendFn: func(context.Context, AuditEntry) error {
		panic("probe: 宿主留痕面 panic")
	}})
	ctx := context.Background()
	before, err := m.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	next := before.Task
	next.Title = "已强释"
	if err := m.CommitTask(ctx, before.Version, next, commitTaskEntry()); !errors.Is(err, ErrAuditorPanic) {
		t.Fatalf("留痕 panic 必须隔离为 ErrAuditorPanic, got %v", err)
	}
	after, err := m.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask after: %v", err)
	}
	if after.Task.Title != before.Task.Title || after.Version != before.Version {
		t.Fatalf("留痕失败 ⇒ 状态一字不动（before=%+v after=%+v）", before, after)
	}
}

// TestStorePlaneAuditorPanicIsolated 拒绝路径的留痕与成功路径同一口径。
func TestStorePlaneAuditorPanicIsolated(t *testing.T) {
	store := seedAuditedStore(t, &funcAuditor{appendFn: func(context.Context, AuditEntry) error {
		panic("probe: 宿主留痕面 panic")
	}})
	plane, err := NewStorePlane(store, store.aud)
	if err != nil {
		t.Fatalf("NewStorePlane: %v", err)
	}
	err = plane.ForceRelease(tenantCtx(), ForceRelease{
		TaskID: "missing", Operator: "op-1", Basis: "b",
	})
	if !errors.Is(err, ErrAuditorPanic) {
		t.Fatalf("拒绝路径的留痕 panic 也必须隔离为 ErrAuditorPanic, got %v", err)
	}
}

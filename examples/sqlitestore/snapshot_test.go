// snapshot_test.go —— 进度快照面的契约判据（严格递增 / 拒收 Seq==0）。
package sqlitestore

import (
	"context"
	"testing"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
)

// TestSnapshotSaveIfNewerStrictIncrease 序号必须严格递增，且 Seq==0 被拒。
func TestSnapshotSaveIfNewerStrictIncrease(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sn := s.Snapshots()

	// Seq == 0 不是一次真实提交 ⇒ 拒收且不写入。
	if err := sn.SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r1", Seq: 0}); !oerrors.Is(err, checkpoint.ErrStaleSeq) {
		t.Fatalf("Seq==0 必须被拒（ErrStaleSeq），实得 %v", err)
	}
	if _, err := sn.Load(ctx, "r1"); !oerrors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("被拒的写入不得落库，实得 %v", err)
	}
	// 首写通过。
	if err := sn.SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r1", Seq: 5, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	// 同序号与更小序号都被拒（恢复点不得回退）。
	if err := sn.SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r1", Seq: 5}); !oerrors.Is(err, checkpoint.ErrStaleSeq) {
		t.Fatalf("同序号必须被拒，实得 %v", err)
	}
	if err := sn.SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r1", Seq: 3}); !oerrors.Is(err, checkpoint.ErrStaleSeq) {
		t.Fatalf("更小序号必须被拒，实得 %v", err)
	}
	// 更大序号通过。
	if err := sn.SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r1", Seq: 6, Done: []string{"s1"}}); err != nil {
		t.Fatal(err)
	}
	got, err := sn.Load(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 6 || len(got.Done) != 1 || got.Done[0] != "s1" {
		t.Fatalf("读回内容不对：%+v", got)
	}
	// 无记录 ⇒ ErrNotFound（不得返回零值 + nil）。
	if _, err := sn.Load(ctx, "unknown"); !oerrors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("无记录必须 ErrNotFound，实得 %v", err)
	}
}

// TestSnapshotCommitterComposition 快照面与内核的 Committer 机制真的能拼
// 起来（提交器的回滚语义在落库形态上成立）。
func TestSnapshotCommitterComposition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	store := s.Snapshots()
	commit := checkpoint.NewCommitter(checkpoint.NewSeqGate(), store)

	if err := commit.Commit(ctx, checkpoint.Snapshot{RunID: "r2", Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := commit.Commit(ctx, checkpoint.Snapshot{RunID: "r2", Seq: 2}); err != nil {
		t.Fatal(err)
	}
	// 进程内闸挡下陈旧写。
	if err := commit.Commit(ctx, checkpoint.Snapshot{RunID: "r2", Seq: 1}); err == nil {
		t.Fatal("陈旧序号必须被拒")
	}
	// 跨进程的那一半：换一个实例（无进程内闸）写陈旧序号，存储侧仍要拒。
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.Snapshots().SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r2", Seq: 1}); !oerrors.Is(err, checkpoint.ErrStaleSeq) {
		t.Fatalf("存储侧必须拒陈旧写，实得 %v", err)
	}
}

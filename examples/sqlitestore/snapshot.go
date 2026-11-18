// snapshot.go —— 进度快照面（checkpoint.StoreCAS）。
//
// # 判据只有两条，但都是硬的
//
//  1. **严格递增**：只有当该 runID 已持久化的序号**严格小于**本次序号时才
//     写入，否则返回 ErrStaleSeq 且**不得写入**（不得部分写、不得覆盖）。
//     首次写入（无记录）视作已持久化序号为 0。
//  2. **Seq == 0 必须被拒**：序号为 0 的快照不是一次真实提交。此前契约写
//     「Seq == 0 不参与单调判定」，被实现读成了「跳过判定照写」⇒ seq=0
//     反而成了**唯一能覆盖既有快照**的写：恢复点静默回退且不报错。
//
// 落库形态与内存参考实现（examples/reference-runtime 的 snapStore）同一
// 判据；跨进程的那一半由「判据在存储侧」保证——本实现的判定就在事务里，
// 进程内再加一层闸也不会让它漂。
package sqlitestore

import (
	"context"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
)

// SnapshotStore 进度快照面（checkpoint.StoreCAS）。
//
// 两种装配：独立事务（Store.Snapshots()）或事务内（Tx.Snapshots()）。
type SnapshotStore struct {
	exec exec
}

// SaveIfNewer 仅当该 runID 已持久化的序号严格小于 snap.Seq 时才写入。
func (s *SnapshotStore) SaveIfNewer(ctx context.Context, snap checkpoint.Snapshot) error {
	return s.exec.run(ctx, func(tx *txCtx) error {
		return snapSaveIfNewer(tx, snap)
	})
}

// Load 无记录时返回 checkpoint.ErrNotFound（不得返回零值 + nil）。
func (s *SnapshotStore) Load(ctx context.Context, runID string) (checkpoint.Snapshot, error) {
	var out checkpoint.Snapshot
	err := s.exec.run(ctx, func(tx *txCtx) error {
		var err error
		out, err = snapLoad(tx, runID)
		return err
	})
	if err != nil {
		return checkpoint.Snapshot{}, err
	}
	return out, nil
}

// snapSaveIfNewer 判据本体（事务内与独立事务共用这一份）。
func snapSaveIfNewer(tx *txCtx, snap checkpoint.Snapshot) error {
	if snap.Seq == 0 {
		return oerrors.Wrap(checkpoint.ErrStaleSeq,
			"sqlitestore: 序号为 0 的快照不是一次真实提交", "run_id", snap.RunID)
	}
	if prev, ok := tx.st.snapshots[snap.RunID]; ok && prev.Seq >= snap.Seq {
		return oerrors.Wrap(checkpoint.ErrStaleSeq,
			"sqlitestore: 已有不小于本次的快照（恢复点不得回退）",
			"run_id", snap.RunID, "已存 seq", prev.Seq, "本次 seq", snap.Seq)
	}
	rec := &snapshotRecord{RunID: snap.RunID, Snapshot: *cloneSnapshot(snap)}
	return tx.stage(journalRecord{Kind: kindSnapshot, Snapshot: rec})
}

// snapLoad 读快照（无记录 ⇒ ErrNotFound）。
func snapLoad(tx *txCtx, runID string) (checkpoint.Snapshot, error) {
	prev, ok := tx.st.snapshots[runID]
	if !ok {
		return checkpoint.Snapshot{}, oerrors.Wrap(checkpoint.ErrNotFound,
			"sqlitestore: 该 runID 无进度快照", "run_id", runID)
	}
	return *cloneSnapshot(*prev), nil
}

// 编译期断言：本实现满足进度快照的跨进程存取契约。
var _ checkpoint.StoreCAS = (*SnapshotStore)(nil)

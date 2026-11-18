// tx_test.go —— 跨面同一次提交的契约判据（#419 的机器形态）。
//
// 「单一存储覆盖全部状态」若只做到「四个面各自能落库」，那还差最要紧的
// 一条：**跨面的变更必须能落在同一次提交里**。这组用例钉住它。
package sqlitestore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// journalLines 数日志的行数（一行 = 一个事务批次）。
func journalLines(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, journalName))
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))
}

// TestCrossFaceOneCommit 四个面在**同一次提交**里落盘（磁盘上是一行）。
func TestCrossFaceOneCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	before := journalLines(t, dir)
	act := effAct("k-atomic", "res-atomic")
	err = s.Apply(ctx, func(tx *Tx) error {
		// 任务面：认领。
		if _, err := tx.Tasks().Claim(ctx, "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
			return err
		}
		// 副作用面：授予执行权。
		if _, _, _, err := tx.Effects().Acquire(ctx, act, time.Minute); err != nil {
			return err
		}
		// 快照面：推进恢复点。
		if err := tx.Snapshots().SaveIfNewer(ctx, checkpoint.Snapshot{RunID: "r1", Seq: 7}); err != nil {
			return err
		}
		// 事件面：留一条事件行（Outbox）。
		if _, err := tx.Events().Insert(ctx, evtOf("e")); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("跨面事务失败：%v", err)
	}

	// 磁盘上只多了一行 ⇒ 四个面在同一次提交里。
	if got := journalLines(t, dir); got != before+1 {
		t.Fatalf("跨面提交必须只写一行（一个事务批次），实得行数 %d（之前 %d）", got, before)
	}

	// 四个面都看得到这次提交。
	tasks, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateClaimed {
		t.Fatalf("任务面未生效：%+v", tasks)
	}
	snap, err := s.Snapshots().Load(ctx, "r1")
	if err != nil {
		t.Fatalf("快照面未生效：%v", err)
	}
	if snap.Seq != 7 {
		t.Fatalf("快照面未生效：%+v", snap)
	}
	wm, err := s.Events().CommittedWatermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wm != 1 {
		t.Fatalf("事件面未生效：水位线 %d", wm)
	}
	outcome, _, _, err := s.Effects().Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeContended {
		t.Fatalf("副作用面未生效（意图应仍在）：实得 %v", outcome)
	}
}

// TestCrossFaceRollbackDiscardsAll 事务失败 ⇒ 四个面一个都没变。
func TestCrossFaceRollbackDiscardsAll(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	before := journalLines(t, dir)

	boom := oerrors.New("注入的失败")
	err = s.Apply(ctx, func(tx *Tx) error {
		if _, err := tx.Tasks().Claim(ctx, "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
			return err
		}
		if _, err := tx.Events().Insert(ctx, evtOf("e")); err != nil {
			return err
		}
		return boom
	})
	if err == nil {
		t.Fatal("回调返回错误时事务必须失败")
	}
	// 一个字都没落盘。
	if got := journalLines(t, dir); got != before {
		t.Fatalf("失败的事务不得写任何东西：行数 %d（之前 %d）", got, before)
	}
	tasks, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].State != taskboard.StateOpen {
		t.Fatalf("任务面必须回滚：%+v", tasks)
	}
	if _, err := s.Snapshots().Load(ctx, "r1"); !oerrors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("快照面必须回滚：实得 %v", err)
	}
	wm, err := s.Events().CommittedWatermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if wm != 0 {
		t.Fatalf("事件面必须回滚：水位线 %d", wm)
	}
}

// TestCrossFaceSwallowedErrorStillAborts 调用方吞掉某个面的错误时，整批仍
// 作废——「一半已提交」是最贵的失败形态。
func TestCrossFaceSwallowedErrorStillAborts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	before := journalLines(t, dir)

	err = s.Apply(ctx, func(tx *Tx) error {
		// 认领一个不存在的任务 ⇒ 失败；但回调**吞掉**这个错误。
		_, _ = tx.Tasks().Claim(ctx, "missing", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
		// 继续写别的面。
		if _, err := tx.Events().Insert(ctx, evtOf("e")); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		t.Fatal("事务内某个面失败时，整个事务必须失败")
	}
	if got := journalLines(t, dir); got != before {
		t.Fatalf("失败的事务不得写任何东西：行数 %d（之前 %d）", got, before)
	}
}

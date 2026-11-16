// store_test.go —— 文件后端的契约判据 + 崩溃恢复/跨实例用例。
//
// 判据分两层：
//
//   - boardtest.Run：任务板共享契约套件（含 100 并发认领档），证明文件后端
//     满足与 pgstore 同一套语义；
//   - 本文件的手写用例：单机形态特有的重启恢复、残尾截断、跨实例单写者。
package sqlitestore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// TestTaskBoardContract 跑任务板共享契约套件（与 pgstore 同一段判据）。
func TestTaskBoardContract(t *testing.T) {
	boardtest.Run(t, func(t *testing.T, clock *boardtest.FakeClock) boardtest.ClockedBoard {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		s.UseClock(clock.Now)
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func newTestStore(t *testing.T, dir string, clock *boardtest.FakeClock) *Store {
	t.Helper()
	s, err := Open(dir, WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestReopenRecoversStateAndRejectsOldToken 重启后状态、令牌、代次都还在。
func TestReopenRecoversStateAndRejectsOldToken(t *testing.T) {
	ctx := context.Background()
	clock := boardtest.NewFakeClock()
	dir := t.TempDir()

	s1 := newTestStore(t, dir, clock)
	s1.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)
	t1, err := s1.Claim(ctx, "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s1.Claim(ctx, "t2", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Complete(ctx, "t2", t2.Lease.Token); err != nil {
		t.Fatal(err)
	}

	// 第二个实例（模拟重启后的新进程）重放日志。
	s2 := newTestStore(t, dir, clock)
	tasks, err := s2.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].State != taskboard.StateClaimed || tasks[1].State != taskboard.StateDone {
		t.Fatalf("重启后状态不一致：%+v", tasks)
	}
	if tasks[0].Lease.Token != t1.Lease.Token || tasks[0].Lease.Fence != t1.Lease.Fence {
		t.Fatalf("令牌/代次必须持久化：%+v vs %+v", tasks[0].Lease, t1.Lease)
	}
	if _, err := s2.Claim(ctx, "t1", "w-2", taskboard.ClaimRequest{TTL: time.Minute}); !oerrors.Is(err, taskboard.ErrTaken) {
		t.Fatalf("活租约期间不能接管：%v", err)
	}

	// 过期后可接管：代次递增，旧令牌的提交被拒。
	clock.Advance(2 * time.Minute)
	taken, err := s2.Claim(ctx, "t1", "w-2", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if taken.Lease.Fence != t1.Lease.Fence+1 {
		t.Fatalf("接管代次必须递增：%d → %d", t1.Lease.Fence, taken.Lease.Fence)
	}
	if err := s1.Complete(ctx, "t1", t1.Lease.Token); !oerrors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("旧令牌提交应 ErrLeaseLost：%v", err)
	}
	if err := s2.Complete(ctx, "t1", taken.Lease.Token); err != nil {
		t.Fatal(err)
	}

	// 再次重启：完成状态不丢。
	s3 := newTestStore(t, dir, clock)
	tasks, err = s3.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.State != taskboard.StateDone {
			t.Fatalf("重启后任务应全部完成：%+v", task)
		}
	}
}

// TestRenewPersistsUntil 续期推后的到期时刻跨实例可见。
func TestRenewPersistsUntil(t *testing.T) {
	ctx := context.Background()
	clock := boardtest.NewFakeClock()
	dir := t.TempDir()

	s1 := newTestStore(t, dir, clock)
	s1.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	task, err := s1.Claim(ctx, "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second)
	renewed, err := s1.Renew(ctx, "t1", task.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	want := clock.Now().Add(time.Minute)
	if !renewed.Lease.Until.Equal(want) {
		t.Fatalf("续期终点错误：%v vs %v", renewed.Lease.Until, want)
	}
	s2 := newTestStore(t, dir, clock)
	tasks, _ := s2.List(ctx)
	if len(tasks) != 1 || !tasks[0].Lease.Until.Equal(want) {
		t.Fatalf("续期结果必须跨实例可见：%+v", tasks)
	}
	// 过期后续期即资格终止。
	clock.Advance(2 * time.Minute)
	if _, err := s2.Renew(ctx, "t1", task.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !oerrors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("过期续期应 ErrLeaseLost：%v", err)
	}
}

// TestFenceWatermarkSurvivesRelease 释放/完成清租约，但代次水位继续递增。
func TestFenceWatermarkSurvivesRelease(t *testing.T) {
	ctx := context.Background()
	clock := boardtest.NewFakeClock()
	dir := t.TempDir()
	s := newTestStore(t, dir, clock)
	s.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	var fences []uint64
	for range 3 {
		task, err := s.Claim(ctx, "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		fences = append(fences, uint64(task.Lease.Fence))
		if err := s.Release(ctx, "t1", task.Lease.Token); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(fences); i++ {
		if fences[i] <= fences[i-1] {
			t.Fatalf("释放后重新认领的代次必须继续递增：%v", fences)
		}
	}
	// 重启后水位仍在：下一次认领仍大于历史最大代次。
	s2 := newTestStore(t, dir, clock)
	task, err := s2.Claim(ctx, "t1", "w-1", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if uint64(task.Lease.Fence) <= fences[len(fences)-1] {
		t.Fatalf("重启后代次水位丢失：%d vs %v", task.Lease.Fence, fences)
	}
}

// TestTornTailRecovery 崩溃残尾被截断，之后的追加不受污染。
func TestTornTailRecovery(t *testing.T) {
	ctx := context.Background()
	clock := boardtest.NewFakeClock()
	dir := t.TempDir()

	s1 := newTestStore(t, dir, clock)
	s1.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟「写了一半被杀」：末行没有换行，JSON 也不完整。
	path := filepath.Join(dir, journalName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(`{"v":1,"task":{"id":"t2"`)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	s2 := newTestStore(t, dir, clock)
	tasks, err := s2.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("残尾必须被丢弃、已完成记录必须保留：%+v", tasks)
	}
	// 追加必须落在干净的行边界上。
	s2.Seed(taskboard.Task{ID: "t2", State: taskboard.StateOpen})
	s2.Seed(taskboard.Task{ID: "t3", State: taskboard.StateOpen})
	s3 := newTestStore(t, dir, clock)
	tasks, err = s3.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("残尾修复后追加应完整可重放：%+v", tasks)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("日志必须以完整行结尾")
	}
}

// TestTwoInstancesConcurrentClaim 两个 Store 实例同时认领：赢家恰好 1 个。
func TestTwoInstancesConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	clock := boardtest.NewFakeClock()
	dir := t.TempDir()
	a := newTestStore(t, dir, clock)
	b := newTestStore(t, dir, clock)
	a.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	const workers = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		bad     []error
	)
	start := make(chan struct{})
	for i := range workers {
		store := a
		if i%2 == 1 {
			store = b
		}
		wg.Add(1)
		go func(agent int) {
			defer wg.Done()
			<-start
			_, err := store.Claim(ctx, "t1", taskboard.AgentID("w-"+string(rune('a'+agent))), taskboard.ClaimRequest{TTL: time.Minute})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
			case oerrors.Is(err, taskboard.ErrTaken):
			default:
				bad = append(bad, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(bad) > 0 {
		t.Fatalf("非争用类错误（判据无法给出结论）：%v", bad)
	}
	if winners != 1 {
		t.Fatalf("跨实例并发认领赢家应恰好 1 个，得到 %d", winners)
	}
}

// TestClosedStoreFailsLoudly 关闭后的存储显式失败，不静默放行。
func TestClosedStoreFailsLoudly(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background()); err == nil {
		t.Fatal("关闭后 List 应报错")
	}
}

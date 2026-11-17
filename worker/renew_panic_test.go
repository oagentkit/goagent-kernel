// renew_panic_test.go —— 宿主任务板的 panic 隔离在两条续期路径上必须同口径
// （issue #368）。
//
// 批量路径（BatchRenewingBoard.RenewBatch）与并发路径（RenewingBoard.Renew
// 经 async.GoSafe）是同一个宿主接缝的两种形态。宿主任务板 panic 时：
//   - 批量路径必须被内核 recover 并上报，心跳协程不得死亡；
//   - 并发路径由 GoSafe 隔离（本用例即它的对照面）。
//
// 判据：同一块 panic 板连续多轮续期，调用次数持续增长（心跳仍活着），
// 且 panic 不逃出 runDueRenewals（逃出即测试进程崩溃）。
//
// 反向验证（issue #368 要求）：去掉并发路径的 async.GoSafe ⇒
// TestRenewConcurrentPanicIsolated 必须转红（panic 在裸 goroutine 中
// 无法被外层 recover，测试进程直接死亡）。
package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// panicRenewBoard 宿主任务板：Renew 一律 panic（并发路径）。
type panicRenewBoard struct {
	*coord.BoardAdapter
	renewCalls atomic.Int32
}

func (b *panicRenewBoard) Renew(context.Context, taskboard.TaskID, string, taskboard.ClaimRequest) (taskboard.Task, error) {
	b.renewCalls.Add(1)
	panic("宿主任务板 Renew 炸了")
}

// panicBatchBoard 宿主任务板：RenewBatch 一律 panic（批量路径）。
type panicBatchBoard struct {
	*panicRenewBoard
	batchCalls atomic.Int32
}

func (b *panicBatchBoard) RenewBatch(context.Context, []taskboard.RenewRequest) ([]taskboard.Renewal, error) {
	b.batchCalls.Add(1)
	panic("宿主任务板 RenewBatch 炸了")
}

// panicTestLoop 构造一个续期失败阈值为 100 的 Loop（避免多轮后进入封口态）。
func panicTestLoop(board taskboard.RenewingBoard) *Loop {
	return &Loop{board: board, cfg: Config{
		TTL: time.Minute, RenewMargin: time.Minute / 3, RenewTimeout: time.Second,
		RenewFailMax: 100, RenewConcurrency: 4,
	}}
}

// seedClaimedTasks 摆 n 条已认领任务并返回到期集合。
func seedClaimedTasks(t *testing.T, board *coord.BoardAdapter, n int) []*claimed {
	t.Helper()
	tasks := make([]taskboard.Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, taskboard.Task{
			ID:    taskboard.TaskID("t" + string(rune('a'+i))),
			State: taskboard.StateOpen,
		})
	}
	board.Seed(tasks...)
	ctx := context.Background()
	due := make([]*claimed, 0, n)
	for _, task := range tasks {
		task, err := board.Claim(ctx, task.ID, "w-1", taskboard.ClaimRequest{TTL: time.Minute})
		if err != nil {
			t.Fatalf("Claim %s: %v", task.ID, err)
		}
		due = append(due, &claimed{id: task.ID, token: task.Lease.Token})
	}
	return due
}

// mustNotPanic 把 panic 转成测试失败（而不是掀翻整个测试进程）。
func mustNotPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panic 逃出续期路径：宿主任务板不应掀翻心跳协程（recovered: %v）", what, r)
		}
	}()
	fn()
}

// TestRenewBatchPanicIsolated 批量路径：宿主 RenewBatch panic 被隔离，
// 逐条记为一次续期失败，下一轮继续调用（心跳不停摆）。
func TestRenewBatchPanicIsolated(t *testing.T) {
	clock := newTestClock()
	base := &panicRenewBoard{BoardAdapter: newTestBoard(clock)}
	board := &panicBatchBoard{panicRenewBoard: base}
	due := seedClaimedTasks(t, base.BoardAdapter, 3)
	loop := panicTestLoop(board)
	ctx := context.Background()

	for round := 0; round < 3; round++ {
		var outcomes []renewOutcome
		mustNotPanic(t, "renewBatch", func() {
			outcomes = loop.runDueRenewals(ctx, due)
		})
		if len(outcomes) != len(due) {
			t.Fatalf("第 %d 轮：panic 后仍应为每条任务给出一致结论（want %d, got %d）",
				round, len(due), len(outcomes))
		}
		for _, o := range outcomes {
			if o.verdict != renewRetry {
				t.Fatalf("第 %d 轮：宿主 panic 应记一次普通失败（renewRetry），got %v", round, o.verdict)
			}
		}
	}
	if got := board.batchCalls.Load(); got != 3 {
		t.Fatalf("心跳应持续发起批量续期（3 轮应 3 次），got %d —— 批量 panic 掀翻了续期路径", got)
	}
	if got := base.renewCalls.Load(); got != 0 {
		t.Fatalf("批量路径不得退回逐条 Renew，got %d 次", got)
	}
}

// TestRenewConcurrentPanicIsolated 并发路径对照面：宿主 Renew panic 由
// async.GoSafe 隔离，下一轮继续调用。
func TestRenewConcurrentPanicIsolated(t *testing.T) {
	clock := newTestClock()
	board := &panicRenewBoard{BoardAdapter: newTestBoard(clock)}
	due := seedClaimedTasks(t, board.BoardAdapter, 3)
	loop := panicTestLoop(board)
	ctx := context.Background()

	for round := 0; round < 3; round++ {
		mustNotPanic(t, "renewConcurrent", func() {
			loop.runDueRenewals(ctx, due)
		})
	}
	if got, want := board.renewCalls.Load(), int32(3*len(due)); got != want {
		t.Fatalf("宿主 panic 后续期调用应持续增长（want %d, got %d）——心跳停摆", want, got)
	}
}

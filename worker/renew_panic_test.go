// renew_panic_test.go —— 续期路径的 panic 隔离（issue #368）。
//
// 同一个宿主接缝（任务板续期）在 worker 的**两条续期路径**上，panic 隔离
// 口径此前相反：
//
//   - 并发路径（无批量面）：每条走 async.GoSafe ⇒ 宿主 panic 只掀翻那一条；
//   - 批量路径（有批量面）：此前**直接调** ⇒ panic 逃出 renewBatch → 逃出
//     heartbeat → 被 loop.go 的 async.GoSafe 吞掉 ⇒ **心跳协程就此死亡**。
//
// 心跳死亡一次即全部在途租约停止续期 ⇒ 他副本接管 ⇒ 非幂等副作用双份；
// 而失效形态是**绿的**：驱动继续领新任务，没有任何错误返回。
//
// 两条判据：
//   - 宿主任务板 panic 后，续期调用次数必须在观测窗口内**持续增长**；
//   - 两条路径的 panic **归属相同**：都经 async 的同一观测面上报，且上报的
//     panic 值逐字相同（同一接缝 ⇒ 同一归属）。
package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// hostRenewPanic 两条路径注入的**同一个**宿主 panic 值。
//
// 逐字相同 ⇒ 两条路径报告的归属可逐字比对（不是「都报了点什么」）。
const hostRenewPanic = "fault: 宿主续期 panic"

// panicBatchBoard 批量续期面每次都 panic（宿主任务板崩了）。
//
// 实现批量面 ⇒ 驱动走批量路径。
type panicBatchBoard struct {
	*coord.BoardAdapter
	calls atomic.Int64
}

func (b *panicBatchBoard) RenewBatch(context.Context, []taskboard.RenewRequest) ([]taskboard.Renewal, error) {
	b.calls.Add(1)
	panic(hostRenewPanic)
}

func (b *panicBatchBoard) renewCalls() int64 { return b.calls.Load() }

// panicRenewBoard 单条续期面每次都 panic（并发路径的同款接缝）。
//
// 不实现批量面 ⇒ 驱动走并发路径（逐条 async.GoSafe）。
type panicRenewBoard struct {
	*coord.BoardAdapter
	calls atomic.Int64
}

func (b *panicRenewBoard) Renew(context.Context, taskboard.TaskID, string, taskboard.ClaimRequest) (taskboard.Task, error) {
	b.calls.Add(1)
	panic(hostRenewPanic)
}

func (b *panicRenewBoard) renewCalls() int64 { return b.calls.Load() }

// panicRenewFace 两条路径的宿主续期面共用的观测面。
type panicRenewFace interface {
	taskboard.RenewingBoard
	Seed(tasks ...taskboard.Task)
	renewCalls() int64
}

// renewPanicLoop 起一个驱动：一条任务在途，宿主续期面每次都 panic。
func renewPanicLoop(t *testing.T, board panicRenewFace) func() {
	t.Helper()
	clock := newTestClock()
	reg := newMemRegistry(clock.Now)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done() // 在途挂住：任务留在在途集合里，续期才有对象
		return ctx.Err()
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("装配驱动失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("panic 隔离后应优雅退出（实得 %v）", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("等待驱动退出超时")
		}
	}
}

// runPanicRenewPath 跑一条续期路径，返回（观测窗口内的续期调用次数,
// async 观测面收到的 panic 值）。
func runPanicRenewPath(t *testing.T, board panicRenewFace) (int64, any) {
	t.Helper()
	prev := async.GlobalPanicHandler()
	var mu sync.Mutex
	var reported []any
	async.SetPanicHandler(func(_ context.Context, r any, _ []byte) {
		mu.Lock()
		reported = append(reported, r)
		mu.Unlock()
	})
	defer async.SetPanicHandler(prev)

	stop := renewPanicLoop(t, board)
	defer stop()

	// 等首次续期调用。
	deadline := time.Now().Add(5 * time.Second)
	for board.renewCalls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待首次续期调用超时")
		}
		time.Sleep(time.Millisecond)
	}
	first := board.renewCalls()

	// 观测窗口：调用次数必须**持续增长**（心跳还活着）。
	time.Sleep(120 * time.Millisecond)
	got := board.renewCalls()
	if got < first+3 {
		t.Fatalf("宿主 panic 后续期调用次数必须持续增长（%d → %d）——panic 掀翻了心跳协程",
			first, got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) == 0 {
		t.Fatal("被隔离的宿主 panic 必须上报到 async 观测面")
	}
	return got, reported[0]
}

// TestRenewPathPanicIsolationParity 两条续期路径的 panic 隔离口径必须相同。
//
// 表驱动逐条独立报告：批量面与并发面是同一个宿主接缝的两条路径，结论必须
// 一致——都隔离、都不掀翻心跳、都经 async 同一观测面上报。
func TestRenewPathPanicIsolationParity(t *testing.T) {
	cases := []struct {
		name  string
		board panicRenewFace
	}{
		{"批量路径", &panicBatchBoard{BoardAdapter: newTestBoard(newTestClock())}},
		{"并发路径", &panicRenewBoard{BoardAdapter: newTestBoard(newTestClock())}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, reported := runPanicRenewPath(t, tc.board)
			if calls < 2 {
				t.Fatalf("观测窗口内的续期调用次数应 ≥ 2，实得 %d", calls)
			}
			if fmt.Sprint(reported) != hostRenewPanic {
				t.Fatalf("上报的应是宿主那个 panic 值（%q），实得 %v", hostRenewPanic, reported)
			}
		})
	}
}

// TestRenewPathsReportSamePanic 两条路径的 panic **归属**必须相同：
// 同一接缝 ⇒ 同一个 panic 值 ⇒ 两条路径上报的逐字一致。
//
// 这一条比「两边都报了点什么」更强：它要求归因到**同一个**宿主 panic，
// 而不是各自塞一个内部错误了事。
func TestRenewPathsReportSamePanic(t *testing.T) {
	_, batchPanic := runPanicRenewPath(t,
		&panicBatchBoard{BoardAdapter: newTestBoard(newTestClock())})
	_, concurrentPanic := runPanicRenewPath(t,
		&panicRenewBoard{BoardAdapter: newTestBoard(newTestClock())})
	if fmt.Sprint(batchPanic) != fmt.Sprint(concurrentPanic) {
		t.Fatalf("两条路径的 panic 归属必须相同：批量报 %v、并发报 %v", batchPanic, concurrentPanic)
	}
}

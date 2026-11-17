// renew_concurrency_test.go —— 续期的**有界并发**与「到期前续上」两条性质
// （issue #411：把原先那条墙钟预算用例拆成两条负载不敏感的断言）。
//
// # 为什么拆
//
// 原用例（TestLoopHighConcurrencyRenewalBeforeExpiry）把两件事塞进一个墙钟
// 窗口：200 条在途 × 单次续期 100ms × 并发上限 8 ⇒ 续期轮名义 2.5s；然后
// 「跑满 TTL(15s)+1s」并断言 renews >= 200。
//
// 那个预算在**机器慢**时不够：续期轮被拖过窗口 ⇒ 计数不够 ⇒ 红。可红的
// 原因与被测机制无关——机器一忙，判据就变成测机器（实录两次：53/200、
// 120/200）。
//
// 而且它红得**不彻底**：窗口内的两条强断言（lost == 0 / failed == 0）当时
// 仍是绿的——它们量的是「有没有租约在续期前过期」，而 renews >= n 量的是
// 「有没有在固定墙钟窗口内跑完 N 次慢操作」。后者换个更慢的机器就假红。
//
// 拆成两条：
//
//   - 用例 A（规模面）：200 在途，断言「续期是**有界并发**」——并发上限真
//     的被用上（串行全量扫描 ⇒ 峰值 1 ⇒ 红），且每条都至少续过一次；
//   - 用例 B（小尺度面）：断言「租约在到期前被续上」——这条性质只有在余量
//     相对代价足够宽时才稳定，故取小规模 + 宽余量。
//
// # 负载不敏感是怎么来的
//
// 判据不再拿「固定墙钟窗口」当尺子：
//
//   - 用例 A 的尺子是**并发计数**（一个数，与机器快慢无关）与**有界等待**
//     （有上界，不是无限等）；
//   - 用例 B 的尺子是**余量比**（租约余量 800ms vs 单次续期 10ms ⇒ 80 倍
//     裕度），机器慢到几十倍仍绿。
package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// slowRenewBoard 让每次续期花 delay，并记录**在途续期数**、峰值并发、
// 已续期过的任务与首次续期时刻。
//
// 峰值并发是「续期是有界并发、不是串行全量扫描」这条性质的直接证据：
// 它是一个**计数**而不是一个时长，因此与机器快慢无关——原用例假红的根因
// 就是把「墙钟窗口」当尺子。
type slowRenewBoard struct {
	*coord.BoardAdapter
	delay        time.Duration
	renews       atomic.Int64
	failed       atomic.Int64
	inflight     atomic.Int32
	maxInflight  atomic.Int32
	once         renewedOnce
	firstRenewal renewedAt
}

// Renew 一次续期：在途计数 + 峰值 + 去重计数 + 首次续期时刻。
func (b *slowRenewBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	cur := b.inflight.Add(1)
	defer b.inflight.Add(-1)
	b.noteMax(cur)
	b.renews.Add(1)
	b.once.mark(id)
	b.firstRenewal.mark(id, time.Now())
	time.Sleep(b.delay)
	task, err := b.BoardAdapter.Renew(ctx, id, token, req)
	if err != nil {
		b.failed.Add(1)
	}
	return task, err
}

// noteMax 记录在途峰值（只增不减；CAS 循环，不用 Mutex——判据要读它）。
func (b *slowRenewBoard) noteMax(cur int32) {
	for {
		old := b.maxInflight.Load()
		if cur <= old || b.maxInflight.CompareAndSwap(old, cur) {
			return
		}
	}
}

// renewedOnce 记下已被续期过的任务（去重：只数「至少续过一次」）。
//
// 为什么不用 renews 计数当判据：renews 数的是**尝试次数**。一旦某条任务的
// 排期早于别的任务，它可能在别的任务还没轮到之前就续了第二次——于是
// 「renews >= n」可能在**并非每条都被续过**时就成立。判据要数的是覆盖。
type renewedOnce struct {
	mu  sync.Mutex
	ids map[taskboard.TaskID]struct{}
}

func (r *renewedOnce) mark(id taskboard.TaskID) {
	r.mu.Lock()
	if r.ids == nil {
		r.ids = map[taskboard.TaskID]struct{}{}
	}
	r.ids[id] = struct{}{}
	r.mu.Unlock()
}

func (r *renewedOnce) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

// renewedAt 记每条任务的首次续期时刻（用于「续期发生在到期前」的断言）。
type renewedAt struct {
	mu sync.Mutex
	at map[taskboard.TaskID]time.Time
}

func (r *renewedAt) mark(id taskboard.TaskID, at time.Time) {
	r.mu.Lock()
	if r.at == nil {
		r.at = map[taskboard.TaskID]time.Time{}
	}
	if _, ok := r.at[id]; !ok {
		r.at[id] = at
	}
	r.mu.Unlock()
}

// first 读某条任务的首次续期时刻。
func (r *renewedAt) first(id taskboard.TaskID) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.at[id]
	return at, ok
}

// waitRenewed 有界等待「至少续期过 n 条」。
//
// 上界是**判据的一部分**：到点还没续够就是红（机制没把在途租约续完），
// 不是「等多久都行」。上界取 60s：续期轮名义 1.25s，满载机器上放大十几倍
// 仍在其内。
func waitRenewed(t *testing.T, b *slowRenewBoard, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if b.once.count() >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("有界等待 %s 内未把 %d 条在途租约都续期过（实得 %d 条）", within, n, b.once.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLoopRenewalUsesBoundedConcurrency 200 条在途的续期必须走**有界并发**。
//
// 断言四件事：
//
//  1. 每条在途租约都至少续期过一次（覆盖 n 条）——规模面；
//  2. 续期的**峰值并发**落在 [2, RenewConcurrency]：
//     串行全量扫描 ⇒ 峰值 1 ⇒ 红（这是 issue #266 当年那个 bug 的形状）；
//     无界扇出 ⇒ 峰值 > 上限 ⇒ 红（并发不是没有代价的）；
//  3. 没有一次续期因租约已过期被拒（failed == 0）；
//  4. 没有任务失格（lost == 0）。
//
// 尺子是**计数**不是时长：机器快慢不影响它，所以本用例在满载机器上依然绿
// ——原用例正是在这里假红（issue #411）。
//
// 峰值只断言「不小于 2」而不是「恰好等于 8」：峰值是观测值，机器极端满载时
// 调度延迟可能让两个 goroutine 错开，于是峰值偶尔小于上限——那是**观测噪声**
// 而不是机制变了。判机制回归的是下界（串行 ⇒ 1），判无界扇出的是上界。
func TestLoopRenewalUsesBoundedConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("200 在途的续期并发用例在 -short 下跳过")
	}
	const (
		n      = 200
		delay  = 20 * time.Millisecond
		ttl    = 8 * time.Second
		margin = ttl - 100*time.Millisecond
	)
	realBoard, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	realBoard.UseClock(time.Now)
	board := &slowRenewBoard{BoardAdapter: realBoard, delay: delay}
	tasks := make([]taskboard.Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, taskboard.Task{
			ID:    taskboard.TaskID("t" + itoa(i)),
			State: taskboard.StateOpen,
		})
	}
	board.Seed(tasks...)

	reg := newMemRegistry(time.Now)
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	var lost atomic.Int64
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: ttl, PollInterval: time.Millisecond, Concurrency: n,
		HeartbeatInterval: ttl / 3,
		RenewMargin:       margin,
		RenewTimeout:      2 * time.Second,
		RenewConcurrency:  DefaultRenewConcurrency,
		OnLeaseLost:       func(taskboard.TaskID, LeaseLostCause) { lost.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	// 有界等待：等「每条都被续过一次」，不是等一个固定墙钟窗口。
	waitRenewed(t, board, n, 60*time.Second)

	if got := board.once.count(); got != n {
		t.Fatalf("每条在途租约都应至少续期一次（%d/%d）", got, n)
	}
	peak := board.maxInflight.Load()
	if peak < 2 {
		t.Fatalf("续期峰值并发应大于 1（实得 %d）——串行全量扫描会退化成这个形状", peak)
	}
	if peak > int32(DefaultRenewConcurrency) {
		t.Fatalf("续期峰值并发应不超过配置上限 %d（实得 %d）——并发必须是有界的", DefaultRenewConcurrency, peak)
	}
	if got := board.failed.Load(); got != 0 {
		t.Fatalf("续期不得因过期被拒（实得 %d 次失败）", got)
	}
	if got := lost.Load(); got != 0 {
		t.Fatalf("高并发续期不得有任务失格（实得 %d 条 OnLeaseLost）", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅退出应 nil: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待驱动退出超时")
	}
}

// TestLoopRenewalBeforeExpiry 租约必须在**到期前**被续上。
//
// 这条性质只能在小尺度 + 宽余量下稳定断言：余量 800ms vs 单次续期 10ms
// ⇒ 80 倍裕度，机器慢几十倍仍绿。而「续期发生在到期前」这件事由**时刻**
// 断言（首次续期时刻 < 认领时刻 + TTL），不是由「有没有报错」间接推的——
// 后者在「根本没续」与「续晚了」两种情况下都是绿的。
func TestLoopRenewalBeforeExpiry(t *testing.T) {
	const (
		n      = 3
		delay  = 10 * time.Millisecond
		ttl    = time.Second
		margin = 800 * time.Millisecond
	)
	realBoard, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	realBoard.UseClock(time.Now)
	board := &slowRenewBoard{BoardAdapter: realBoard, delay: delay}
	tasks := make([]taskboard.Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, taskboard.Task{
			ID:    taskboard.TaskID("t" + itoa(i)),
			State: taskboard.StateOpen,
		})
	}
	board.Seed(tasks...)

	reg := newMemRegistry(time.Now)
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	var lost atomic.Int64
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: ttl, PollInterval: time.Millisecond, Concurrency: n,
		HeartbeatInterval: ttl / 3,
		RenewMargin:       margin,
		RenewTimeout:      500 * time.Millisecond,
		OnLeaseLost:       func(taskboard.TaskID, LeaseLostCause) { lost.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	claimedAt := time.Now()
	go func() { done <- loop.Run(ctx) }()

	waitRenewed(t, board, n, 10*time.Second)

	// 首次续期必须发生在租约到期之前——逐条看时刻。
	for _, task := range tasks {
		at, ok := board.firstRenewal.first(task.ID)
		if !ok {
			t.Fatalf("任务 %s 没有续期记录", task.ID)
		}
		if !at.Before(claimedAt.Add(ttl)) {
			t.Fatalf("任务 %s 的首次续期发生在到期之后（续期 %s / 到期 %s）",
				task.ID, at.Format(time.StampMilli), claimedAt.Add(ttl).Format(time.StampMilli))
		}
	}
	if got := board.failed.Load(); got != 0 {
		t.Fatalf("续期不得因过期被拒（实得 %d 次失败）", got)
	}
	if got := lost.Load(); got != 0 {
		t.Fatalf("不得有任务失格（实得 %d 条 OnLeaseLost）", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅退出应 nil: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待驱动退出超时")
	}
}

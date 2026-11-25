// renew_schedule_test.go —— 续期调度/批量/并发/超时（issue #266）。
//
// 判据：
//   - 续期按每条任务自己的到期时刻调度（不是每拍全量扫描）；
//   - 批量续期接口存在时，存储往返次数显著少于任务数；
//   - 单条续期超时只影响该任务，不阻断同拍其它任务；
//   - 超时与普通失败分开计数。
//
// 「200 在途的续期压力」这条判据已挪到 renew_concurrency_test.go：它原先把
// 「有界并发」与「到期前续上」两件事塞进一个墙钟窗口，机器一慢就假红
// （issue #411）。拆开后的两条断言与本文件同属续期语义，但尺子不同：
// 一条量并发计数，一条量续期时刻。
package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// TestRenewCountersSeparateTimeout 超时与普通失败分开计数，成功清零。
func TestRenewCountersSeparateTimeout(t *testing.T) {
	c := &claimed{id: "t1"}
	after, retry := time.Second, time.Millisecond

	v := c.renew(context.Background(), 5*time.Millisecond, func(ctx context.Context) (taskboard.Task, error) {
		<-ctx.Done()
		return taskboard.Task{}, ctx.Err()
	}, after, retry, 3)
	if v != renewRetry {
		t.Fatalf("首次超时未到阈值应 renewRetry, got %v", v)
	}
	if c.renewTimeouts != 1 || c.renewFails != 0 {
		t.Fatalf("超时必须单独计数（timeouts=%d fails=%d）", c.renewTimeouts, c.renewFails)
	}

	v = c.renew(context.Background(), 0, func(context.Context) (taskboard.Task, error) {
		return taskboard.Task{}, errors.New("存储抖动")
	}, after, retry, 3)
	if v != renewRetry {
		t.Fatalf("普通失败未到阈值应 renewRetry, got %v", v)
	}
	if c.renewFails != 1 || c.renewTimeouts != 1 {
		t.Fatalf("普通失败不得混入超时计数（timeouts=%d fails=%d）", c.renewTimeouts, c.renewFails)
	}

	if v = c.renew(context.Background(), 0, func(context.Context) (taskboard.Task, error) {
		return taskboard.Task{ID: "t1"}, nil
	}, after, retry, 3); v != renewOK {
		t.Fatalf("成功应 renewOK, got %v", v)
	}
	if c.renewFails != 0 || c.renewTimeouts != 0 {
		t.Fatalf("成功必须清零两个计数（timeouts=%d fails=%d）", c.renewTimeouts, c.renewFails)
	}
}

// TestInflightDueByNextRenew 调度按每条任务自己的 nextRenew，而非全量扫描。
func TestInflightDueByNextRenew(t *testing.T) {
	set := newInflight()
	now := time.Now()
	soon := &claimed{id: "soon"}
	soon.scheduleRenew(50 * time.Millisecond)
	later := &claimed{id: "later"}
	later.scheduleRenew(time.Hour)
	set.add(soon)
	set.add(later)

	if due := set.due(now); len(due) != 0 {
		t.Fatalf("未到点的任务不应续期（实得 %v）", due)
	}
	due := set.due(now.Add(time.Minute))
	if len(due) != 1 || due[0].id != "soon" {
		t.Fatalf("只应续到点的 soon（实得 %v）", due)
	}
	if d := set.nextRenewDelay(now); d <= 0 || d > 100*time.Millisecond {
		t.Fatalf("下一到期点应约 50ms（实得 %v）", d)
	}
}

// batchCountingBoard 记录批量往返与逐条条数的任务板。
type batchCountingBoard struct {
	*coord.BoardAdapter
	batchCalls atomic.Int32
	itemCalls  atomic.Int32
}

func (b *batchCountingBoard) RenewBatch(ctx context.Context, reqs []taskboard.RenewRequest) ([]taskboard.Renewal, error) {
	b.batchCalls.Add(1)
	out := make([]taskboard.Renewal, len(reqs))
	for i, req := range reqs {
		b.itemCalls.Add(1)
		task, err := b.Renew(ctx, req.ID, req.Token, taskboard.ClaimRequest{TTL: req.TTL})
		out[i] = taskboard.Renewal{ID: req.ID, Task: task, Err: err}
	}
	return out, nil
}

// TestRenewBatchRoundTrips 200 条到期租约走 2 次批量往返（每批 100）。
func TestRenewBatchRoundTrips(t *testing.T) {
	clock := newTestClock()
	board := &batchCountingBoard{BoardAdapter: newTestBoard(clock)}
	const n = 200
	tasks := make([]taskboard.Task, 0, n)
	for i := 0; i < n; i++ {
		tasks = append(tasks, taskboard.Task{ID: taskboard.TaskID("t" + string(rune('a'+i%26)) + string(rune('a'+i/26))), State: taskboard.StateOpen})
	}
	board.Seed(tasks...)

	ctx := context.Background()
	due := make([]*claimed, 0, n)
	for _, task := range tasks {
		claimedTask, err := board.Claim(ctx, task.ID, "w-1", taskboard.ClaimRequest{TTL: time.Minute})
		if err != nil {
			t.Fatalf("Claim %s: %v", task.ID, err)
		}
		due = append(due, &claimed{id: task.ID, token: claimedTask.Lease.Token})
	}
	loop := &Loop{board: board, cfg: Config{
		TTL: time.Minute, RenewMargin: time.Minute / 3, RenewTimeout: time.Second,
		RenewFailMax: 3, RenewConcurrency: 8,
	}}
	outcomes := loop.runDueRenewals(ctx, due)
	if len(outcomes) != n {
		t.Fatalf("应产生 %d 条续期结论, got %d", n, len(outcomes))
	}
	for _, o := range outcomes {
		if o.verdict != renewOK {
			t.Fatalf("批量续期应全部成功（%s → %v）", o.cur.id, o.verdict)
		}
	}
	if got := board.batchCalls.Load(); got != 2 {
		t.Fatalf("200 条应按每批 100 分 2 次往返, got %d", got)
	}
	if got := board.itemCalls.Load(); got != n {
		t.Fatalf("逐条续期次数应为 %d, got %d", n, got)
	}
}

// slowOneBoard t1 的续期挂到 ctx 超时；其它任务走正常存储。
type slowOneBoard struct {
	*coord.BoardAdapter
	slowID taskboard.TaskID
	renews syncMap
}

// syncMap 按任务计数（避免引入额外依赖）。
type syncMap struct {
	mu sync.Mutex
	m  map[taskboard.TaskID]int
}

func (s *syncMap) inc(id taskboard.TaskID) {
	s.mu.Lock()
	if s.m == nil {
		s.m = map[taskboard.TaskID]int{}
	}
	s.m[id]++
	s.mu.Unlock()
}

func (s *syncMap) get(id taskboard.TaskID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[id]
}

func (b *slowOneBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	b.renews.inc(id)
	if id == b.slowID {
		<-ctx.Done() // 只认超时：单条续期失败不得拖住其它任务
		return taskboard.Task{}, ctx.Err()
	}
	return b.BoardAdapter.Renew(ctx, id, token, req)
}

// TestRenewTimeoutOnlyAffectsThatTask 一条续期超时只影响它自己。
func TestRenewTimeoutOnlyAffectsThatTask(t *testing.T) {
	clock := newTestClock()
	board := &slowOneBoard{BoardAdapter: newTestBoard(clock), slowID: "t1"}
	board.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
	)
	reg := newMemRegistry(clock.Now)
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	lost := make(chan taskboard.TaskID, 4)
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond, Concurrency: 2,
		RenewMargin: renewEveryTickMargin, RenewTimeout: 20 * time.Millisecond, RenewFailMax: 2,
		OnLeaseLost: func(id taskboard.TaskID, _ LeaseLostCause) { lost <- id },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	// t1 超时到阈值 ⇒ 只有 t1 被取消并留痕；t2 继续被成功续期。
	select {
	case id := <-lost:
		if id != "t1" {
			t.Fatalf("超时留痕必须只打 t1（实得 %s）", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("t1 连续续期超时后必须留痕")
	}
	deadline := time.After(5 * time.Second)
	for board.renews.get("t2") < 2 {
		select {
		case <-deadline:
			t.Fatalf("t2 必须继续被续期（实得 %d 次）", board.renews.get("t2"))
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case id := <-lost:
		t.Fatalf("t2 不得被 t1 的超时波及（误留痕 %s）", id)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, ErrLeaseUnconfirmed) {
		t.Fatalf("单条超时到阈值只应让驱动按 ErrLeaseUnconfirmed 收刀（实得 %v）", err)
	}
}

// itoa 十进制度字符串（测试夹具，避免 fmt 依赖扩散）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

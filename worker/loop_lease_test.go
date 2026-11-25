// loop_lease_test.go —— 心跳/续期两个故障域**互不阻断**的回归用例
// （issue #179）：注册表心跳持续失败不得吞掉同拍的租约续期；租约续期
// 连续失败跨过阈值必须取消在途并留痕；ErrLeaseLost 的每条路径都有
// 可观测信号，不再静默。
//
// 两个故障域分开构造（与 TestLoopLeaseLostCancelsInFlight 同一口径）：
// 心跳坏 → flakyRegistry；续期坏 → flakyBoard。时钟可注入，场景确定性
// 触发，不靠 sleep 撞时序。
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

// flakyRegistry 心跳可注入持续故障的注册表替身（其余方法走 MemRegistry）。
type flakyRegistry struct {
	*MemRegistry
	failHB atomic.Bool
}

func (r *flakyRegistry) Heartbeat(ctx context.Context, id, token string, ttl time.Duration) (time.Time, error) {
	if r.failHB.Load() {
		// 非身份类故障（存储抖动）：不得被当成身份丢失，也不得吞掉
		// 同一拍的租约续期（issue #179 的触发序列）。
		return time.Time{}, errors.New("worker: 注册表心跳存储抖动（替身）")
	}
	return r.MemRegistry.Heartbeat(ctx, id, token, ttl)
}

// flakyBoard 续期可注入持续故障的任务板替身（其余方法走 BoardAdapter）。
type flakyBoard struct {
	*coord.BoardAdapter
	failRenew  atomic.Bool
	renewCalls atomic.Int64 // Renew 被调计数（可观测事实：等拍数的尺子）
}

func (b *flakyBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	b.renewCalls.Add(1)
	if b.failRenew.Load() {
		return taskboard.Task{}, errors.New("worker: 任务板续期存储抖动（替身）")
	}
	return b.BoardAdapter.Renew(ctx, id, token, req)
}

// TestLoopHeartbeatOutageDoesNotSkipRenewal 用例 A：注册表心跳持续报错、
// 任务板正常 ⇒ 租约仍按节拍续期。旧实现里心跳失败整拍 continue、续期
// 一拍不跑，租约静默过期——本用例对旧实现必红。
func TestLoopHeartbeatOutageDoesNotSkipRenewal(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock() // 注册表时钟不动：身份保持有效
	board := newTestBoard(boardClock)
	reg := &flakyRegistry{MemRegistry: newMemRegistry(regClock.Now)}
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		<-ctx.Done() // 在途挂起：给续期留出多拍
		return ctx.Err()
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: 5 * time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	// 等认领落地，记下认领时的租约终点。
	deadline := time.After(5 * time.Second)
	var until0 time.Time
	for until0.IsZero() {
		tasks, _ := board.List(context.Background())
		if tasks[0].State == taskboard.StateClaimed && !tasks[0].Lease.Until.IsZero() {
			until0 = tasks[0].Lease.Until
			break
		}
		select {
		case <-deadline:
			t.Fatal("等待认领超时")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// 注册表持续故障（非身份类）+ 板时钟前移：续期若照常跑，租约终点
	// 应跟随「当前时刻 + TTL」；被心跳失败吞掉的实现里 Until 停在 until0。
	reg.failHB.Store(true)
	boardClock.Advance(10 * time.Second)
	target := boardClock.Now().Add(time.Minute)

	deadline = time.After(5 * time.Second)
	for {
		tasks, _ := board.List(context.Background())
		if tasks[0].Lease.Until.Equal(target) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("心跳持续失败期间租约必须照常续期：认领时 %v，实得 %v（期望 %v）",
				until0, tasks[0].Lease.Until, target)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("心跳抖动不构成身份丢失，应优雅退出（实得 %v）", err)
	}
}

// TestLoopRenewOutageUnconfirmedStopsDriver 用例 B：心跳与续期**两条都
// 失败** ⇒ 连续失败到阈值后，在途 ctx 必须被取消（TTL 内）、驱动停止
// 领取收刀并以 ErrLeaseUnconfirmed 收场、留痕可观测——旧实现里这条
// 路径完全静默。
func TestLoopRenewOutageUnconfirmedStopsDriver(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock()
	board := &flakyBoard{BoardAdapter: newTestBoard(boardClock)}
	reg := &flakyRegistry{MemRegistry: newMemRegistry(regClock.Now)}
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	var (
		mu     sync.Mutex
		causes []LeaseLostCause
	)
	sawCancel := atomic.Bool{}
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		entered <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			sawCancel.Store(true)
		}
		return ctx.Err()
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: 5 * time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond,
		RenewFailMax: 3, // 显式阈值：三拍 = 15ms，远在 TTL=1min 之内
		OnLeaseLost: func(_ taskboard.TaskID, cause LeaseLostCause) {
			mu.Lock()
			causes = append(causes, cause)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待在途执行超时")
	}
	board.failRenew.Store(true)
	reg.failHB.Store(true)

	deadline := time.After(5 * time.Second) // TTL=1min 的零头：取消必须发生在 TTL 内
	for !sawCancel.Load() {
		select {
		case <-deadline:
			t.Fatal("续期连续失败跨阈值后必须取消在途执行——无法确认持有资格就不得继续施加")
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(gate)
	select {
	case err := <-done:
		if !errors.Is(err, ErrLeaseUnconfirmed) {
			t.Fatalf("驱动必须以 ErrLeaseUnconfirmed 收刀（实得 %v）", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待驱动收刀超时")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(causes) == 0 {
		t.Fatal("「无法确认持有资格」必须留痕（OnLeaseLost）——这条路径不许静默")
	}
	for _, c := range causes {
		if c != LeaseLostUnconfirmed {
			t.Fatalf("本场景的留痕必须是 LeaseLostUnconfirmed（实得 %v）", causes)
		}
	}
	// 收场走的是没坏的 Release 面：任务回到可重做，不悬在 claimed。
	tasks, _ := board.List(context.Background())
	if tasks[0].State != taskboard.StateOpen {
		t.Fatalf("收刀后在途任务必须被释放回可重做（实得 %s）", tasks[0].State)
	}
}

// TestLoopLeaseTakenOverNotifiesHook 用例 C（其一）：Renew 报
// ErrLeaseLost（租约过期被接管）⇒ 取消在途 + 留痕 LeaseLostTakenOver。
func TestLoopLeaseTakenOverNotifiesHook(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock() // 注册表时钟不动：续期失败只由任务租约触发
	board := newTestBoard(boardClock)
	reg := newMemRegistry(regClock.Now)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	var (
		mu     sync.Mutex
		causes []LeaseLostCause
	)
	sawCancel := atomic.Bool{}
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	handler := HandlerFunc(func(ctx context.Context, _ taskboard.Task) error {
		entered <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			sawCancel.Store(true)
		}
		return errors.New("worker: 资格检查失败后的收场（替身）")
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: 5 * time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond,
		OnLeaseLost: func(_ taskboard.TaskID, cause LeaseLostCause) {
			mu.Lock()
			causes = append(causes, cause)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待在途执行超时")
	}
	boardClock.Advance(2 * time.Minute) // 租约过期：下一拍 Renew 必报 ErrLeaseLost

	deadline := time.After(5 * time.Second)
	for !sawCancel.Load() {
		select {
		case <-deadline:
			t.Fatal("租约易主后必须取消在途执行")
		case <-time.After(5 * time.Millisecond):
		}
	}
	mu.Lock()
	has := false
	for _, c := range causes {
		if c == LeaseLostTakenOver {
			has = true
		}
	}
	mu.Unlock()
	if !has {
		t.Fatal("ErrLeaseLost 路径必须留痕 LeaseLostTakenOver——不许当成功路径吞掉")
	}
	close(gate)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("本次任务按失败收场后应优雅退出（实得 %v）", err)
	}
}

// TestLoopFinalizeLeaseLostNotifiesHook 用例 C（其二）：收场 Complete 被
// ErrLeaseLost 拒（任务已被接管者拿走）⇒ 驱动如实接受（不重试、不吞）
// 但必须留痕 LeaseLostOnFinalize——「本次结果作废」是宿主必须知道的
// 事实，静默吞掉它下游的非幂等副作用就可能双份。
func TestLoopFinalizeLeaseLostNotifiesHook(t *testing.T) {
	boardClock := newTestClock()
	regClock := newTestClock()
	board := newTestBoard(boardClock)
	reg := newMemRegistry(regClock.Now)
	board.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	var (
		mu     sync.Mutex
		causes []LeaseLostCause
	)
	entered := make(chan struct{}, 1)
	resume := make(chan struct{})
	handler := HandlerFunc(func(context.Context, taskboard.Task) error {
		entered <- struct{}{}
		<-resume // 无视 ctx：模拟「handler 完成得比接管晚」
		return nil
	})
	loop, err := NewLoop(board, reg, handler, Config{
		Agent: "w-1", TTL: time.Minute, PollInterval: 5 * time.Millisecond,
		RenewMargin: renewEveryTickMargin, HeartbeatInterval: 5 * time.Millisecond,
		OnLeaseLost: func(_ taskboard.TaskID, cause LeaseLostCause) {
			mu.Lock()
			causes = append(causes, cause)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等待在途执行超时")
	}
	boardClock.Advance(2 * time.Minute) // 租约过期
	if _, err := board.Claim(context.Background(), "t1", "接管者", taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		t.Fatalf("接管者认领: %v", err)
	}
	close(resume) // handler 带着旧令牌去收场 → Complete 必被 ErrLeaseLost 拒

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		has := false
		for _, c := range causes {
			if c == LeaseLostOnFinalize {
				has = true
			}
		}
		mu.Unlock()
		if has {
			break
		}
		select {
		case <-deadline:
			t.Fatal("收场被 ErrLeaseLost 拒必须留痕 LeaseLostOnFinalize——「结果作废」不是成功路径")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("闸已易主属如实接受，驱动应优雅退出（实得 %v）", err)
	}
	tasks, _ := board.List(context.Background())
	if tasks[0].State != taskboard.StateClaimed || tasks[0].Owner != "接管者" {
		t.Fatalf("任务必须归接管者（实得 %s/%s）——旧持有者不得改写", tasks[0].State, tasks[0].Owner)
	}
}

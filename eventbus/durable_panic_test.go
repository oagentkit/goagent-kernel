// durable_panic_test.go —— 宿主 Store.Insert 的 panic 隔离与锁外调用
// （issue #399）。
//
// 同包另一个宿主接缝 onDrop 已走 subhub.Do 隔离（issue #328），而 Insert 在
// 发布临界区内裸调：宿主 DB 驱动/连接池/序列化 panic 会掀翻调用方，且不走
// drop 留痕。本文件把「panic ⇒ error + 不广播 + drop 留痕 + 令牌不遗留」
// 和「Insert 不持发布锁」两条钉住。
package eventbus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// panicStore 每次 Insert 都 panic（模拟宿主存储炸了）。
type panicStore struct{ calls atomic.Int64 }

func (s *panicStore) Insert(_ context.Context, _ Event) (uint64, error) {
	s.calls.Add(1)
	panic("宿主存储炸了")
}

// publishSafely 包一层 recover：用例要判「panic 没逃出 Publish」，直接调用
// 会让测试进程崩掉、看不出断言意图。
func publishSafely(p *DurablePublisher, ev Event) (out Event, panicked any, err error) {
	defer func() {
		if r := recover(); r != nil {
			panicked = r
		}
	}()
	out, err = p.Publish(context.Background(), ev)
	return
}

// TD7 宿主 Insert panic 不得逃出 Publish：转成 error、不广播、走 drop 留痕。
//
// 反向验证：去掉 insertStore 的 recover ⇒ 本用例在 panicked 处转红。
func TestDurablePublishInsertPanicIsolated(t *testing.T) {
	boom := &panicStore{}
	var drops dropLog
	p := NewDurablePublisher(New(), boom, WithOnDrop(drops.onDrop))
	ch, cancel := p.bus.Subscribe(context.Background())
	defer cancel()

	out, panicked, err := publishSafely(p, Event{SessionID: "s1", Type: EventStepStarted})
	if panicked != nil {
		t.Fatalf("Insert panic 逃出了 Publish: %v", panicked)
	}
	if err == nil {
		t.Fatal("Insert panic 应转成 error（失败路径：零值事件 + error）")
	}
	if !sameEvent(out, Event{}) {
		t.Fatalf("panic 隔离后应返回零值事件, got %+v", out)
	}
	if got := drops.count(); got != 1 {
		t.Fatalf("panic 隔离后应走 drop 留痕 1 次, got %d", got)
	}
	drops.mu.Lock()
	dropped, cause := drops.evs[0], drops.cuses[0]
	drops.mu.Unlock()
	if dropped.SessionID != "s1" || dropped.ID != 0 || dropped.Level != "info" {
		t.Fatalf("drop 应收到归一后的事件（ID 清零、Level 补 info）: %+v", dropped)
	}
	if cause == nil {
		t.Fatal("drop 应收到非 nil 的 cause")
	}

	// 不广播：订阅者一无所获。
	select {
	case ev := <-ch:
		t.Fatalf("Insert panic 不得广播, 订阅者收到 %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	if got := boom.calls.Load(); got != 1 {
		t.Fatalf("Insert 应恰好被调用 1 次, got %d", got)
	}
}

// TD8 panic 隔离后串行令牌不得遗留：同一发布器的下一次 Publish 必须自行返回
// （哪怕仍然 panic）。
//
// 反向验证：Insert 的 recover 放在串行令牌释放之后（或干脆不释放）⇒ 第二次
// Publish 在超时处转红。
func TestDurablePublishInsertPanicDoesNotLeakGate(t *testing.T) {
	boom := &panicStore{}
	p := NewDurablePublisher(New(), boom)

	if _, panicked, _ := publishSafely(p, Event{SessionID: "s1", Type: EventStepStarted}); panicked != nil {
		t.Fatalf("首次 panic 逃出 Publish: %v", panicked)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = publishSafely(p, Event{SessionID: "s1", Type: EventStepFinished})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("第二次 Publish 未在 2s 内返回：panic 后串行令牌/锁未释放")
	}
	if got := boom.calls.Load(); got != 2 {
		t.Fatalf("Insert 应被调用 2 次（第二次也进入临界区）, got %d", got)
	}
}

// probeStore 在 Insert 内探测发布器的 mu 是否被持有。
type probeStore struct {
	probe func() bool // 返回 true = mu 未持有（可获取）
	held  atomic.Bool
}

func (s *probeStore) Insert(_ context.Context, _ Event) (uint64, error) {
	s.held.Store(!s.probe())
	return 1, nil
}

// TD9 宿主 Insert 必须在发布互斥锁**之外**调用（issue #399 的「锁内只留判定」）。
//
// 判据是白盒的：Insert 里 TryLock 发布器的 mu。锁外的实现能拿到（随后解锁），
// 锁内的实现拿不到。反向验证：把 Insert 调用移回 p.mu 临界区 ⇒ 转红。
func TestDurableInsertRunsOutsidePublishMutex(t *testing.T) {
	var p *DurablePublisher
	store := &probeStore{probe: func() bool {
		if p.mu.TryLock() {
			p.mu.Unlock()
			return true
		}
		return false
	}}
	p = NewDurablePublisher(New(), store)

	if _, err := p.Publish(context.Background(), Event{SessionID: "s1", Type: EventStepStarted}); err != nil {
		t.Fatalf("正常发布失败: %v", err)
	}
	if store.held.Load() {
		t.Fatal("Store.Insert 在发布互斥锁内被调用：锁内应只留判定，宿主调用移出锁")
	}
}

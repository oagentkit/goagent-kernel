package eventbus

// 载荷隔离回归（issue #378）：Event.Payload 是 map（引用类型），按值传递
// 只复制 map 头。发布方、返回帧、每个订阅方、落库行必须是各自独立的数据。
//
// 反向验证：去掉 Publish/Broadcast 的 Clone 或 MemoryStore.Insert 的克隆，
// 对应用例转红；去掉逐订阅方克隆时「跨订阅方污染」用例必红。

import (
	"context"
	"sync"
	"testing"
)

// TestEventCloneDeepCopiesReferences 钉住 Clone 的语义：Payload map 与
// StepID 指针都必须换一份，nil 保持 nil。
func TestEventCloneDeepCopiesReferences(t *testing.T) {
	step := uint64(7)
	ev := Event{ID: 1, StepID: &step, Payload: map[string]any{"k": "v0"}}
	cp := ev.Clone()

	*cp.StepID = 9
	cp.Payload["k"] = "v1"
	if *ev.StepID != 7 {
		t.Fatalf("Clone 共享 StepID 指针：原值被改成 %d", *ev.StepID)
	}
	if ev.Payload["k"] != "v0" {
		t.Fatalf("Clone 共享 Payload map：原值被改成 %v", ev.Payload["k"])
	}

	// nil 保持 nil（不是空 map / 新指针）：零值事件克隆后仍可安全比较字段。
	var zero Event
	if got := zero.Clone(); got.Payload != nil || got.StepID != nil {
		t.Fatalf("零值 Clone 应保持 nil 引用字段，got %+v", got)
	}
}

// TestPublishPayloadIsolatedFromCallerAndFrame 顺序形态：发布方复用入参
// map 不得改穿返回帧或订阅帧。
func TestPublishPayloadIsolatedFromCallerAndFrame(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	payload := map[string]any{"k": "v0"}
	frame := b.Publish("s", "", nil, EventStepStarted, "info", payload)

	// 发布后复用同一张 map 写下一条事件（issue 里的实测写法）。
	payload["k"] = "v1"
	if frame.Payload["k"] != "v0" {
		t.Fatalf("返回帧与发布方共享载荷：got %v", frame.Payload["k"])
	}
	sub, ok := waitReceive(ch)
	if !ok {
		t.Fatal("2s 内未收到广播帧")
	}
	if sub.Payload["k"] != "v0" {
		t.Fatalf("订阅帧与发布方共享载荷：got %v", sub.Payload["k"])
	}

	// 返回帧与订阅帧也应是两份独立数据。
	sub.Payload["k"] = "v2"
	if frame.Payload["k"] != "v0" {
		t.Fatalf("返回帧与订阅帧共享载荷：got %v", frame.Payload["k"])
	}
}

// TestPublishPerSubscriberPayloadIndependent 跨订阅方污染：一个订阅方改写
// 自己收到的载荷，不得影响另一个订阅方。
func TestPublishPerSubscriberPayloadIndependent(t *testing.T) {
	b := New()
	ch1, cancel1 := b.Subscribe(context.Background())
	defer cancel1()
	ch2, cancel2 := b.Subscribe(context.Background())
	defer cancel2()

	b.Publish("s", "", nil, EventStepStarted, "info", map[string]any{"k": "v0"})
	e1, ok1 := waitReceive(ch1)
	e2, ok2 := waitReceive(ch2)
	if !ok1 || !ok2 {
		t.Fatal("2s 内两个订阅方都应收到广播帧")
	}
	e1.Payload["k"] = "mutated-by-sub1"
	if e2.Payload["k"] != "v0" {
		t.Fatalf("订阅方之间共享载荷：sub2 读到 %v", e2.Payload["k"])
	}
}

// TestBroadcastPayloadIsolated 宿主直接 Broadcast 时同款：入参事件、每个
// 订阅方各拿独立副本。
func TestBroadcastPayloadIsolated(t *testing.T) {
	b := New()
	ch1, cancel1 := b.Subscribe(context.Background())
	defer cancel1()
	ch2, cancel2 := b.Subscribe(context.Background())
	defer cancel2()

	ev := Event{ID: 1, SessionID: "s", Type: EventStepStarted, Payload: map[string]any{"k": "v0"}}
	b.Broadcast(ev)

	e1, ok1 := waitReceive(ch1)
	e2, ok2 := waitReceive(ch2)
	if !ok1 || !ok2 {
		t.Fatal("2s 内两个订阅方都应收到广播帧")
	}
	e1.Payload["k"] = "mutated-by-sub1"
	if e2.Payload["k"] != "v0" {
		t.Fatalf("Broadcast 的各订阅方共享载荷：sub2 读到 %v", e2.Payload["k"])
	}
	if ev.Payload["k"] != "v0" {
		t.Fatalf("Broadcast 改穿了调用方的入参事件：got %v", ev.Payload["k"])
	}
}

// TestDurablePublishPayloadIsolated 落库行、广播帧、返回帧三向独立：
// 入参复用 / 订阅方改写 / 落库行改写都不得互相改穿。
func TestDurablePublishPayloadIsolated(t *testing.T) {
	b := New()
	store := NewMemoryStore()
	p := NewDurablePublisher(b, store)
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	payload := map[string]any{"k": "v0"}
	frame, err := p.Publish(context.Background(), Event{
		SessionID: "s", Type: EventStepStarted, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, ok := waitReceive(ch)
	if !ok {
		t.Fatal("2s 内未收到广播帧")
	}

	// 1) 发布方复用入参 map。
	payload["k"] = "v1"
	if frame.Payload["k"] != "v0" || sub.Payload["k"] != "v0" {
		t.Fatalf("入参复用改穿了返回帧/广播帧：frame=%v sub=%v", frame.Payload["k"], sub.Payload["k"])
	}

	// 2) 订阅方改写广播帧。
	sub.Payload["k"] = "v2"
	if frame.Payload["k"] != "v0" {
		t.Fatalf("广播帧与返回帧共享载荷：got %v", frame.Payload["k"])
	}

	// 3) 落库行与广播面是两份独立数据（MemoryStore 入口也要拷）。
	store.mu.Lock()
	row := store.evs[0]
	store.mu.Unlock()
	if row.Payload["k"] != "v0" {
		t.Fatalf("落库行被发布方复用改穿：got %v", row.Payload["k"])
	}
	row.Payload["k"] = "v3"
	if frame.Payload["k"] != "v0" || sub.Payload["k"] != "v2" {
		t.Fatalf("改写落库行改穿了广播面：frame=%v sub=%v", frame.Payload["k"], sub.Payload["k"])
	}
}

// TestMemoryStoreInsertCopiesPayload 参考存储自身的入口拷贝：调用方之后
// 复用入参 map 不得改穿落库行（Store 契约不要求实现替调用方拷，但参考实现
// 必须把「落库是独立数据」演示对）。
func TestMemoryStoreInsertCopiesPayload(t *testing.T) {
	store := NewMemoryStore()
	payload := map[string]any{"k": "v0"}
	if _, err := store.Insert(context.Background(), Event{SessionID: "s", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	payload["k"] = "v1"

	store.mu.Lock()
	row := store.evs[0]
	store.mu.Unlock()
	if row.Payload["k"] != "v0" {
		t.Fatalf("MemoryStore 留存入参 map：落库行被改成 %v", row.Payload["k"])
	}
}

// TestPublishPayloadNoRaceWithCallerReuse -race 形态：发布方复用入参 map
// 与订阅方读载荷并发，不得出现数据竞争（无 -race 时本用例只验不死锁）。
func TestPublishPayloadNoRaceWithCallerReuse(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	payload := map[string]any{"k": 0}
	b.Publish("s", "", nil, EventStepStarted, "info", payload)
	got, ok := waitReceive(ch)
	if !ok {
		t.Fatal("2s 内未收到广播帧")
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // 发布方：复用同一张 map（写）
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			payload["k"] = i
		}
	}()
	go func() { // 订阅方：读收到的载荷
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			_ = got.Payload["k"]
		}
	}()
	close(start)
	wg.Wait()
}

// TestHubPayloadPerSubscriberIndependent Hub 直接投递面同样不得跨订阅方
// 共享载荷（两个 SSE 连接是两份独立消费）。
func TestHubPayloadPerSubscriberIndependent(t *testing.T) {
	h := NewHub()
	ch1, stop1 := h.Subscribe("sess-a")
	defer stop1()
	ch2, stop2 := h.Subscribe("sess-a")
	defer stop2()

	h.Publish(Event{ID: 1, SessionID: "sess-a", Payload: map[string]any{"k": "v0"}})
	e1 := <-ch1
	e2 := <-ch2
	e1.Payload["k"] = "mutated-by-sub1"
	if e2.Payload["k"] != "v0" {
		t.Fatalf("Hub 的订阅方之间共享载荷：sub2 读到 %v", e2.Payload["k"])
	}
}

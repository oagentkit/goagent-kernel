package eventbus

// payload_clone_test.go —— 载荷拷贝契约（issue #378）。
//
// 契约：发布方、Publish 返回帧、广播帧、落库行**互不共享同一张 map**。
// 发布方复用 map 写下一条事件是极常见的写法——不拷贝就是顺序污染（订阅方
// 看到下一条的值）加上一次数据竞争（竞争的是留痕载荷，事后无法从症状倒推
// 哪条事件被改过）。

import (
	"context"
	"sync"
	"testing"
	"time"
)

// T-C1 顺序污染：发布后改写原 payload map，订阅方收到的帧与 Publish 返回帧的
// 载荷必须仍是旧值。
func TestPublishCopiesPayloadForSubscriberAndReturn(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	payload := map[string]any{"k": "v0"}
	out := b.Publish("s1", "c1", nil, EventStepStarted, "info", payload)

	// 发布方复用同一张 map 写下一条事件（极常见的写法）。
	payload["k"] = "v1"

	frame, ok := waitReceive(ch)
	if !ok {
		t.Fatal("2s 内未收到广播帧")
	}
	if frame.Payload["k"] != "v0" {
		t.Errorf("订阅方收到的载荷被发布方改写污染：%v", frame.Payload)
	}
	if out.Payload["k"] != "v0" {
		t.Errorf("Publish 返回帧的载荷被发布方改写污染：%v", out.Payload)
	}
}

// T-C2 durable 面同款：返回帧、广播帧、落库行三份数据互不共享。
func TestDurablePublishCopiesPayloadForStoreAndBroadcast(t *testing.T) {
	b := New()
	store := NewMemoryStore()
	p := NewDurablePublisher(b, store)
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	payload := map[string]any{"k": "v0"}
	out, err := p.Publish(context.Background(), Event{
		SessionID: "s1", Type: EventStepStarted, Payload: payload,
	})
	if err != nil {
		t.Fatalf("正常发布不应失败: %v", err)
	}
	payload["k"] = "v1"

	if out.Payload["k"] != "v0" {
		t.Errorf("Publish 返回帧的载荷被发布方改写污染：%v", out.Payload)
	}
	frame, ok := waitReceive(ch)
	if !ok {
		t.Fatal("2s 内未收到广播帧")
	}
	if frame.Payload["k"] != "v0" {
		t.Errorf("广播帧的载荷被发布方改写污染：%v", frame.Payload)
	}
	store.mu.Lock()
	row := store.evs[0]
	store.mu.Unlock()
	if row.Payload["k"] != "v0" {
		t.Errorf("落库行的载荷被发布方改写污染：%v", row.Payload)
	}
}

// T-C3 并发：发布方复用 map 写下一条事件 与 订阅方读载荷并发，必须无 race
// （-race 下运行）。反向验证：去掉 Publish 里的载荷拷贝 ⇒ 本用例报 race。
func TestPublishPayloadConcurrentReuseNoRace(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	const n = 300
	var wg sync.WaitGroup

	// 发布方：复用同一张 map 反复发布（每次发布前改写它）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		payload := map[string]any{"k": "v0"}
		for i := 0; i < n; i++ {
			payload["k"] = i
			b.Publish("s1", "c1", nil, EventStepStarted, "info", payload)
		}
	}()

	// 订阅方：读广播帧的载荷。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				_ = ev.Payload["k"]
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	wg.Wait()
}

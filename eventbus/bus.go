package eventbus

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oekit/concurrency/async"
)

// busBuf 单订阅者缓冲（与上游 progress.Bus 的 64 一致：慢订阅丢帧语义不变）。
const busBuf = 64

// Bus 进程内事件总线（单实例；cluster 多副本 Phase③ 换 NATS 扇出）。
//
// # 载荷隔离（issue #378）
//
// 扇出在本包里自己实现（不再直接委托 progress.Bus）：投递前对**每个订阅方**
// 各 Clone 一次，故一个订阅方原地改写收到的 Payload 不会改穿其它订阅方，
// 也不会改穿发布方的返回帧。其余投递语义与 progress.Bus 逐字一致：64 缓冲、
// 非阻塞发送、满即丢并计数、退订/ctx 取消即关闭 channel。
type Bus struct {
	mu      sync.Mutex
	subs    map[chan Event]struct{}
	seq     atomic.Uint64
	dropped atomic.Int64
}

// New 构造（序号从 0 起；仅测试或全新库使用）。
func New() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

// Broadcast 广播一个已构建事件（发布路径先落库拿 bq_event 自增 ID，再由此广播；
// 该次序的生产收口见 DurablePublisher，issue #160——本方法只做广播面，不落库）。
// SSE after 游标从此只有 bq_event ID 一个单调空间——跨重启、跨副本（PG 序列全局唯一）都成立，
// 取代旧的 NewWithSeq 启动播种方案（bus 内部序号与回放 ID 是两个空间，多副本下必然错位）。
func (b *Bus) Broadcast(e Event) {
	if e.Level == "" {
		e.Level = "info"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		// 每个订阅方一份独立副本：map 按值传递只复制头，直接投同一个 Event
		// 会让所有订阅方与发布方共享一张载荷（跨订阅方污染，issue #378）。
		//
		// len/cap 预判与发送之间没有第二个发送者（发送全在 b.mu 下），
		// 订阅方只会取走元素，故「有空间」在本临界区内稳定成立——满则计数
		// 丢弃，语义与上游 progress.Bus 的非阻塞 select 相同。
		if len(ch) == cap(ch) {
			b.dropped.Add(1)
			continue
		}
		ch <- e.Clone()
	}
}

// Publish 赋 ID/时间戳后广播（未落库的临时序号；生产路径改用 DurablePublisher）。
func (b *Bus) Publish(sessionID, caseID string, stepID *uint64, typ, level string, payload map[string]any) Event {
	e := Event{
		ID:         b.seq.Add(1),
		SessionID:  sessionID,
		CaseID:     caseID,
		StepID:     stepID,
		Type:       typ,
		Level:      level,
		Payload:    payload,
		OccurredAt: time.Now(),
	}
	if e.Level == "" {
		e.Level = "info"
	}
	// 入口侧深拷贝（Payload + StepID）：发布方在 Publish 之后复用同一张 map /
	// 改写 step 指针是常见写法（issue #378），不得改穿返回帧与广播帧。
	e = e.Clone()
	b.Broadcast(e)
	return e
}

// Subscribe 订阅事件流；cancel 释放订阅（SSE 断开/bq_event writer 停止时调用）。
//
// # 契约：退订（cancel 或 ctx 取消）后 channel 会被**关闭**
//
// 消费方必须写 `v, ok := <-ch` 并处理 ok==false。这不是可选风格：关闭的 channel
// 在 select 里**恒可就绪**并反复给出零值，于是 `case v := <-ch:` 不查 ok 的循环
// 会空转——CPU 打满、事件是假的、而且不报错。
//
// 本包曾按上游旧契约（runtime<v0.10.0 的「channel 永不关闭」，oagentkit/runtime#20）
// 承诺「不关」，并以此为由禁止消费方 range。上游 v0.10.0 推翻了那条理由
// （runtime#32）：Publish 与 unsubscribe 同锁互斥，不存在 send-on-closed 窗口，
// 故退订即 close。本仓随之上车——关闭换来的是 `for range ch` 能正常退出、
// 且不会把已缓冲事件挂在无人消费的 channel 上（旧形态每退订一次就漏一条 channel）。
//
// 注意 Hub 的 Subscribe **不**关 channel（见 hub.go 的说明）：两个投递面在这里
// 有意分叉，判据由 hub_test.go 与 eventbus_test.go 各钉一侧。
func (b *Bus) Subscribe(ctx context.Context) (<-chan Event, func()) {
	ch := make(chan Event, busBuf)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	stop := make(chan struct{})
	unsubscribe := func() {
		once.Do(func() {
			close(stop)
			b.mu.Lock()
			delete(b.subs, ch)
			// 持锁 close：与 Broadcast 的持锁发送互斥，不存在 send-on-closed 窗口。
			close(ch)
			b.mu.Unlock()
		})
	}
	async.GoWithContext(ctx, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			unsubscribe()
		case <-stop:
		}
	})
	return ch, unsubscribe
}

// Dropped 慢订阅丢弃计数（可观测）。
func (b *Bus) Dropped() int64 { return b.dropped.Load() }

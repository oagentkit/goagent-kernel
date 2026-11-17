package eventbus

// durable_panic_test.go —— 宿主存储接缝 panic 的隔离（issue #399）。
//
// 契约与 onDrop 同款：宿主接缝的 panic 不得掀翻发布面。隔离后转成 error ⇒
// 走 drop 路径留痕 ⇒ 次序不变量（失败不广播）不变，且发布锁不被遗留。

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// panicStore 固定 panic 的存储接缝（模拟宿主驱动/连接池在写库时炸掉）。
type panicStore struct{ calls int }

func (s *panicStore) Insert(_ context.Context, _ Event) (uint64, error) {
	s.calls++
	panic("存储接缝炸了")
}

// T-P1 宿主存储 panic ⇒ Publish 返回 error 且不 panic；不广播；drop 路径走通。
func TestDurablePublishIsolatesStorePanic(t *testing.T) {
	b := New()
	store := &panicStore{}
	var log dropLog
	p := NewDurablePublisher(b, store, WithOnDrop(log.onDrop))
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	out, err := p.Publish(context.Background(), Event{
		SessionID: "s1", Type: EventStepStarted, Payload: map[string]any{"k": "v"},
	})
	if err == nil {
		t.Fatal("存储接缝 panic 应转成 error 返回（不得 panic 逃出 Publish）")
	}
	if !errors.Is(err, ErrStorePanic) {
		t.Errorf("panic 应隔离成 ErrStorePanic，实得 %v", err)
	}
	if !sameEvent(out, Event{}) {
		t.Errorf("失败路径应返回零值事件，实得 %+v", out)
	}

	// 不广播：失败帧不得出现在任何订阅者面前。
	select {
	case ev := <-ch:
		t.Fatalf("存储接缝 panic 不得广播，订阅者收到 %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	// drop 路径被走通：留痕拿到的是同一个隔离错误。
	if got := log.count(); got != 1 {
		t.Fatalf("onDrop 应留痕 1 次，实得 %d", got)
	}
	log.mu.Lock()
	cause := log.cuses[0]
	log.mu.Unlock()
	if !errors.Is(cause, ErrStorePanic) {
		t.Errorf("onDrop 应收到隔离后的错误，实得 %v", cause)
	}
}

// T-P2 隔离后发布锁未被遗留：后续 Publish 在 N 秒内自行返回（不是死锁）。
func TestDurablePublishAfterIsolatedPanicDoesNotDeadlock(t *testing.T) {
	b := New()
	store := &panicStore{}
	p := NewDurablePublisher(b, store)

	if _, err := p.Publish(context.Background(), Event{SessionID: "s1", Type: EventStepStarted}); err == nil {
		t.Fatal("存储接缝 panic 应转成 error 返回")
	}

	done := make(chan error, 1)
	go func() {
		_, err := p.Publish(context.Background(), Event{SessionID: "s1", Type: EventStepFinished})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("第二次发布同样应拿到隔离后的错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("隔离后发布锁未遗留：后续 Publish 未能自行返回")
	}
}

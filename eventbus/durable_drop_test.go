// durable_drop_test.go —— onDrop 的 panic 隔离与锁外调用（issue #328）。
package eventbus

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// failSessionStore 按事件会话判定失败的 Store：模拟「只有某条事件落库失败」，
// 用于在同一个发布器上验证失败路径不会占住成功路径的发布锁。
type failSessionStore struct{ failSession string }

func (s failSessionStore) Insert(_ context.Context, ev Event) (uint64, error) {
	if ev.SessionID == s.failSession {
		return 0, errors.New("insert 失败（会话 " + ev.SessionID + "）")
	}
	return 1, nil
}

// TD5 onDrop panic 不得逃出 Publish（issue #328）：宿主留痕钩子的异常
// 只能丢掉这次留痕，不能把「已确定失败」的发布改写成进程级事故。
func TestDurablePublishOnDropPanicIsolated(t *testing.T) {
	boom := errors.New("bq_event insert 失败")
	p := NewDurablePublisher(New(), boomStore{err: boom}, WithOnDrop(func(Event, error) {
		panic("probe: 宿主回调 panic")
	}))

	out, err := p.Publish(context.Background(), Event{SessionID: "s1", Type: EventStepStarted})
	if !errors.Is(err, boom) {
		t.Fatalf("panic 隔离后仍应返回 insert 错误, got %v", err)
	}
	if !sameEvent(out, Event{}) {
		t.Fatalf("失败路径应返回零值事件, got %+v", out)
	}
}

// TD6 onDrop 在发布临界区之外（issue #328）：回调挂起期间，另一条 Publish
// 必须能独立完成。若 onDrop 仍在锁内，并发 Publish 会一直等到回调返回——
// 用例以超时判红。
func TestDurablePublishOnDropOutsidePublishLock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	p := NewDurablePublisher(New(), failSessionStore{failSession: "fail"}, WithOnDrop(func(Event, error) {
		close(entered)
		<-release
	}))

	failed := make(chan struct{})
	go func() {
		defer close(failed)
		_, _ = p.Publish(context.Background(), Event{SessionID: "fail", Type: EventStepStarted})
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("onDrop 未被调用")
	}

	okDone := make(chan error, 1)
	go func() {
		_, err := p.Publish(context.Background(), Event{SessionID: "ok", Type: EventStepFinished})
		okDone <- err
	}()
	select {
	case err := <-okDone:
		if err != nil {
			t.Fatalf("并发成功发布不应失败: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("onDrop 挂起期间并发 Publish 被阻塞——回调仍在发布临界区内")
	}

	close(release)
	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("释放回调后失败的 Publish 未返回")
	}
}

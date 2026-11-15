package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// memRegistry 测试夹具：Registry 的进程内实现（真实现见 examples/worker-runtime）。
// 过期判定读可注入时钟——注册资格的判定归注册表侧（契约要求）。
type memRegistry struct {
	mu   sync.Mutex
	now  func() time.Time
	recs map[string]*regRec
}

type regRec struct {
	token     string
	labels    map[string]string
	expiresAt time.Time
}

func newMemRegistry(now func() time.Time) *memRegistry {
	return &memRegistry{now: now, recs: map[string]*regRec{}}
}

func (m *memRegistry) Register(_ context.Context, spec Spec) (Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := spec.Validate(); err != nil {
		return Registration{}, err
	}
	token := fmt.Sprintf("reg-%s-%d-%d", spec.ID, m.now().UnixNano(), suffix.Add(1))
	m.recs[spec.ID] = &regRec{token: token, labels: spec.Labels, expiresAt: m.now().Add(spec.TTL)}
	return Registration{ID: spec.ID, Token: token, ExpiresAt: m.recs[spec.ID].expiresAt}, nil
}

func (m *memRegistry) Heartbeat(_ context.Context, id, token string, ttl time.Duration) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok || r.token != token || !m.now().Before(r.expiresAt) {
		return time.Time{}, errors.Wrap(ErrNotRegistered, "worker: 心跳被拒（替身）", "id", id)
	}
	if ttl <= 0 {
		return time.Time{}, errors.Wrap(ErrInvalidConfig, "worker: 心跳 TTL 必须为正（替身）")
	}
	r.expiresAt = m.now().Add(ttl)
	return r.expiresAt, nil
}

func (m *memRegistry) Deregister(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok || r.token != token {
		return errors.Wrap(ErrNotRegistered, "worker: 注销被拒（替身）", "id", id)
	}
	delete(m.recs, id)
	return nil
}

func (m *memRegistry) List(_ context.Context) ([]Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.recs))
	for id, r := range m.recs {
		out = append(out, Status{ID: id, Labels: r.labels, ExpiresAt: r.expiresAt, Alive: m.now().Before(r.expiresAt)})
	}
	return out, nil
}

// suffix 注册令牌的独立序号（原子——多驱动并发注册时不串号）。
var suffix atomic.Int64

// errRegistryDown 注册表存储抖动的合成错误（**非** ErrNotRegistered：
// 身份不可确认 ≠ 身份已丢失——两种失败的处置完全不同，测试必须分开构造）。
var errRegistryDown = errors.New("worker_test: 注册表存储不可用（替身）")

// errBoardDown 任务板存储抖动的合成错误（非 ErrLeaseLost：资格不可确认
// ≠ 资格已丢失）。
var errBoardDown = errors.New("worker_test: 任务板存储不可用（替身）")

// heartbeatFailingRegistry 注册表替身：Register/Deregister/List 正常，
// Heartbeat 恒报存储抖动（issue #179 用例 A/B 的故障域）。
type heartbeatFailingRegistry struct {
	*memRegistry
	err error
}

func (r *heartbeatFailingRegistry) Heartbeat(context.Context, string, string, time.Duration) (time.Time, error) {
	return time.Time{}, r.err
}

// renewCountingBoard 计数 Renew 调用后委托给真实任务板适配器
// （issue #179 用例 A：租约是否仍按节拍续期）。
type renewCountingBoard struct {
	*coord.BoardAdapter
	renews atomic.Int64
}

func (b *renewCountingBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	b.renews.Add(1)
	return b.BoardAdapter.Renew(ctx, id, token, req)
}

// renewFailingBoard 任务板替身：Renew 恒报存储抖动，其余委托
// （issue #179 用例 B：资格无法确认）。
type renewFailingBoard struct {
	*coord.BoardAdapter
	err error
}

func (b *renewFailingBoard) Renew(context.Context, taskboard.TaskID, string, taskboard.ClaimRequest) (taskboard.Task, error) {
	return taskboard.Task{}, b.err
}

// eventLog OnEvent 回调替身：并发安全收集，按类别等待（事件是契约的
// 一部分——等不到就是失败，不是可选观测）。
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (e *eventLog) record(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *eventLog) snapshot() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.events...)
}

// wait 等到第一件指定类别的事件（5s 超时即 Fatal）。
func (e *eventLog) wait(t *testing.T, kind EventKind) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		for _, ev := range e.snapshot() {
			if ev.Kind == kind {
				return ev
			}
		}
		select {
		case <-deadline:
			t.Fatalf("必须产出 %s 事件（实得 %v）", kind, e.snapshot())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// testClock 测试时钟：过期场景要能确定性触发，不靠 sleep 撞时序。
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *testClock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

package worker

import (
	"sync"
	"time"
)

// 测试夹具：注册表用本包的进程内参考实现（MemRegistry，契约用例覆盖），
// 时钟可注入——过期场景要能确定性触发，不靠 sleep 撞时序。

// testClock 测试时钟。
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *testClock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

// newMemRegistry 测试夹具的构造快捷方式：固定注入测试时钟。
func newMemRegistry(now func() time.Time) *MemRegistry {
	return NewMemRegistry(WithRegistryClock(now))
}

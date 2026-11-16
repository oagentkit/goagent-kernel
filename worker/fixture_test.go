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

// renewEveryTickMargin 让续期在每个心跳拍都到点（TTL=1min 的测试夹具）。
//
// 新调度（issue #266）按「上次成功续期 + TTL − RenewMargin」排期；margin
// 设成 TTL−1ms 即每个心跳拍续一次——只影响调度频率，判据仍是续期语义。
// 不设它的用例按缺省 margin（TTL/3）走，续期会在 2/3 TTL 处发生。
const renewEveryTickMargin = time.Minute - time.Millisecond

// fixtures_test.go —— 故障矩阵共用的最小替身（全部合成数据）。
//
// 时间一律可注入：过期/租约接管/时钟偏移的判据若读真实时间，要么靠 sleep
// 撞时序，要么永不触发（一组空断言全绿）。存储替身只实现契约原语，
// 故障注入层包在它外面。
package faultmatrix

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
)

// fakeClock 可推进的假时钟（并发安全）。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newBoardWithClock 构造内存后端任务板，并把判定时钟注入为假时钟。
//
// 内存后端显式声明 SingleMachine（契约套件与单机嵌入模式的既有形态）；
// standalone/cluster 的跨进程原子性由真实后端负责，矩阵验证的判定逻辑
// 与后端无关（见包注释）。
func newBoardWithClock(t *testing.T, clock *fakeClock) *coord.BoardAdapter {
	t.Helper()
	board, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatalf("装配内存任务板失败: %v", err)
	}
	board.UseClock(clock.Now)
	return board
}

// storageBackend 存储时钟权威的后端：Now 是唯一的到期判定依据。
// 节点本地时钟不参与判定（issue #194 的时钟偏移用例靠它复现）。
type storageBackend struct {
	*coord.MemoryBackend
	clock *fakeClock
}

func newStorageBackend(clock *fakeClock) *storageBackend {
	return &storageBackend{MemoryBackend: coord.NewMemoryBackend(), clock: clock}
}

// Now 实现 coord.ClockSource（存储时钟）。
func (b *storageBackend) Now(context.Context) (time.Time, error) { return b.clock.Now(), nil }

// faultBackend 可注入「数据库整体不可用」的后端。
//
// 四个原语全部显式失败——只让某一个失败会造出「半可用」的假形态：
// 快照读失败但事务成功之类的组合不是数据库故障，而是实现 bug。
type faultBackend struct {
	*coord.MemoryBackend
	down atomic.Bool
}

var errInjectedOutage = errors.New("faultmatrix: 注入数据库故障")

func (b *faultBackend) Snapshot(ctx context.Context, keys ...string) (map[string]coord.Entry, error) {
	if b.down.Load() {
		return nil, errInjectedOutage
	}
	return b.MemoryBackend.Snapshot(ctx, keys...)
}

func (b *faultBackend) Txn(ctx context.Context, asserts []coord.Assert, mutations []coord.Mutation) (bool, error) {
	if b.down.Load() {
		return false, errInjectedOutage
	}
	return b.MemoryBackend.Txn(ctx, asserts, mutations)
}

func (b *faultBackend) ScanSnapshot(ctx context.Context, prefix string) (map[string]coord.Entry, error) {
	if b.down.Load() {
		return nil, errInjectedOutage
	}
	return b.MemoryBackend.ScanSnapshot(ctx, prefix)
}

func (b *faultBackend) Keys(ctx context.Context, prefix string) ([]string, error) {
	if b.down.Load() {
		return nil, errInjectedOutage
	}
	return b.MemoryBackend.Keys(ctx, prefix)
}

// bareBackend 只有四个原语、**不**声明时间权威的后端：
// 装配期必须被拒绝（不得静默退化成节点本地时钟）。
type bareBackend struct {
	inner *coord.MemoryBackend
}

func (b *bareBackend) Snapshot(ctx context.Context, keys ...string) (map[string]coord.Entry, error) {
	return b.inner.Snapshot(ctx, keys...)
}

func (b *bareBackend) Txn(ctx context.Context, asserts []coord.Assert, mutations []coord.Mutation) (bool, error) {
	return b.inner.Txn(ctx, asserts, mutations)
}

func (b *bareBackend) ScanSnapshot(ctx context.Context, prefix string) (map[string]coord.Entry, error) {
	return b.inner.ScanSnapshot(ctx, prefix)
}

func (b *bareBackend) Keys(ctx context.Context, prefix string) ([]string, error) {
	return b.inner.Keys(ctx, prefix)
}

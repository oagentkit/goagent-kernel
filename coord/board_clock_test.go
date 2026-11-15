// board_clock_test.go —— issue #194 回归：租约到期判定只随**存储时钟**。
//
// 节点本地时钟退出判定面：适配器的时间权威次序是「UseClock 注入（套件）
// → ClockSource（存储时钟）→ 声明过仅单机（节点时钟）」，装配期拒绝
// 两头都不给的 Backend。判据与 examples/pgstore「到期由存储时钟算」同源。
package coord

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// clockedStorage 测试用 ClockSource：存储时钟可注入推进（真实后端的形态
// 是存储自己报 clock_timestamp / Lua TIME——这里由测试扮演存储）。
type clockedStorage struct {
	*MemoryBackend

	mu sync.Mutex
	at time.Time
	// err 非空时 Now 原样返回它（模拟存储侧取时失败）。
	err error
}

func newClockedStorage(at time.Time) *clockedStorage {
	return &clockedStorage{MemoryBackend: NewMemoryBackend(), at: at}
}

func (c *clockedStorage) Now(_ context.Context) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at, c.err
}

func (c *clockedStorage) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// bareBackend 只有三个原语：既不实现 ClockSource 也不声明 SingleMachine
// （内嵌接口不提升任何额外方法）——装配期必须拒绝它。
type bareBackend struct{ Backend }

// TestBoardAdapterLeaseJudgedByStorageClock 两个「节点」（两个适配器实例）
// 共享一个存储，存储时钟拨到与进程真实时间差出数年的刻度：
//
//   - Until 由存储时钟算（含节点时钟成分就会落在真实 now 附近，可辨）；
//   - 在持租约对另一节点是 ErrTaken（判定读存储，不读自己进程的时间）；
//   - 存储时钟推过 Until ⇒ 原持有者续期 ErrLeaseLost、另一节点接管成功。
func TestBoardAdapterLeaseJudgedByStorageClock(t *testing.T) {
	storage := newClockedStorage(time.Date(2031, 3, 1, 12, 0, 0, 0, time.UTC))
	bX, err := NewBoardAdapter(storage) // 节点 X
	if err != nil {
		t.Fatal(err)
	}
	bY, err := NewBoardAdapter(storage) // 节点 Y（同一存储，另一进程的替身）
	if err != nil {
		t.Fatal(err)
	}
	bX.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	tkX, err := bX.Claim(t.Context(), "t1", "agent-x", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	wantUntil := time.Date(2031, 3, 1, 12, 1, 0, 0, time.UTC)
	if !tkX.Lease.Until.Equal(wantUntil) {
		t.Fatalf("Until 必须由存储时钟算（要 %v 实得 %v——含节点本地时钟成分）", wantUntil, tkX.Lease.Until)
	}

	// 租约在存储侧未过期：节点 Y 认领 ⇒ ErrTaken。
	if _, err := bY.Claim(t.Context(), "t1", "agent-y", taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, taskboard.ErrTaken) {
		t.Fatalf("在持租约必须 ErrTaken（实得 %v）", err)
	}

	// 存储时钟推过 Until：租约在存储侧已过期。
	storage.advance(2 * time.Minute)

	// 原持有者续期：资格终止（判定同样读存储时钟）。
	if _, err := bX.Renew(t.Context(), "t1", tkX.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("存储时钟判过期后续期必须 ErrLeaseLost（实得 %v）", err)
	}
	// 节点 Y 接管成功。
	tkY, err := bY.Claim(t.Context(), "t1", "agent-y", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("存储时钟判过期后另一节点必须能接管（实得 %v）", err)
	}
	if tkY.Owner != "agent-y" {
		t.Fatalf("接管后持有者必须是 agent-y（实得 %v）", tkY.Owner)
	}
}

// TestBoardAdapterStorageClockErrorFailsLoud 存储取时失败 ⇒ Claim 显式报错：
// 绝不静默退回节点本地时钟把判定做下去（退了就是两套时间真相）。
func TestBoardAdapterStorageClockErrorFailsLoud(t *testing.T) {
	storage := newClockedStorage(time.Date(2031, 3, 1, 12, 0, 0, 0, time.UTC))
	b, err := NewBoardAdapter(storage)
	if err != nil {
		t.Fatal(err)
	}
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	boom := errors.New("存储取时失败")
	storage.mu.Lock()
	storage.err = boom
	storage.mu.Unlock()

	if _, err := b.Claim(t.Context(), "t1", "agent-x", taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, boom) {
		t.Fatalf("存储取时失败必须原样上抛（实得 %v）", err)
	}

	// Renew 同判据：先在时钟健康时把任务认领到手（令牌匹配），再让存储
	// 取时失败——资格检查取不到存储时刻就终止，不拿节点时钟顶上。
	storage.mu.Lock()
	storage.err = nil
	storage.mu.Unlock()
	tk, err := b.Claim(t.Context(), "t1", "agent-x", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	storage.mu.Lock()
	storage.err = boom
	storage.mu.Unlock()
	if _, err := b.Renew(t.Context(), "t1", tk.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, boom) {
		t.Fatalf("Renew 在取时失败时也必须原样上抛（实得 %v）", err)
	}
}

// TestAssemblyRejectsBackendWithoutTimeAuthority 后端既不实现 ClockSource
// 也未声明 SingleMachine ⇒ 装配期拒绝（两个适配器同一判据）。
func TestAssemblyRejectsBackendWithoutTimeAuthority(t *testing.T) {
	bare := bareBackend{NewMemoryBackend()}
	if _, err := NewBoardAdapter(bare); !errors.Is(err, ErrNoTimeAuthority) {
		t.Fatalf("无时间权威的后端必须被拒绝装配（实得 %v）", err)
	}
	if _, err := NewStoreAdapter(bare); !errors.Is(err, ErrNoTimeAuthority) {
		t.Fatalf("StoreAdapter 同判据：无时间权威的后端必须被拒绝装配（实得 %v）", err)
	}
	// 对照组：实现 ClockSource 的后端放行。
	if _, err := NewBoardAdapter(newClockedStorage(time.Now())); err != nil {
		t.Fatalf("ClockSource 后端必须放行（实得 %v）", err)
	}
}

// TestMemoryBackendDeclaresSingleMachine 内存后端显式声明仅单机：装配放行
// 且判定可用（契约套件的注入时钟路径不受影响）。
func TestMemoryBackendDeclaresSingleMachine(t *testing.T) {
	if !NewMemoryBackend().SingleMachine() {
		t.Fatal("内存后端必须声明 SingleMachine（节点本地时钟是它唯一的时间真相）")
	}
	b, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatalf("仅单机声明的后端必须放行装配（实得 %v）", err)
	}
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	if _, err := b.Claim(t.Context(), "t1", "agent-x", taskboard.ClaimRequest{}); err != nil {
		t.Fatalf("声明过仅单机的后端走节点时钟判定，认领必须成功（实得 %v）", err)
	}
}

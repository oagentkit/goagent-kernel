// dbfailover_test.go —— 矩阵行十：数据库主备切换（Standalone）。
//
// 进程内替身把「主库提交 → 备库可见 → 主库故障 → 备库 promote」拆成可判
// 事实：
//   - 切换前已提交的状态（任务/令牌/代次）在备库可见；
//   - 主库故障期间写面显式失败，未提交的写入不得半写；
//   - promote 后旧主库被 fence，旧写入路径不得继续提交（不产生双主）；
//   - 新主库按同一份存储时钟继续判定租约。
//
// 真 PG 主备格（requiresEnv）另行声明；缺环境时本文件的可判事实仍然执行。
package faultmatrix

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// failoverBackend 主备切换的协调后端替身：active 指向当前主库；down 时
// 所有原语显式失败（不伪装成「空结果」）。
type failoverBackend struct {
	mu     sync.Mutex
	active *storageBackend
	down   bool
}

func (f *failoverBackend) view() (*storageBackend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errInjectedOutage
	}
	return f.active, nil
}

func (f *failoverBackend) Snapshot(ctx context.Context, keys ...string) (map[string]coord.Entry, error) {
	b, err := f.view()
	if err != nil {
		return nil, err
	}
	return b.Snapshot(ctx, keys...)
}

func (f *failoverBackend) Txn(ctx context.Context, asserts []coord.Assert, mutations []coord.Mutation) (bool, error) {
	b, err := f.view()
	if err != nil {
		return false, err
	}
	return b.Txn(ctx, asserts, mutations)
}

func (f *failoverBackend) ScanSnapshot(ctx context.Context, prefix string) (map[string]coord.Entry, error) {
	b, err := f.view()
	if err != nil {
		return nil, err
	}
	return b.ScanSnapshot(ctx, prefix)
}

func (f *failoverBackend) Keys(ctx context.Context, prefix string) ([]string, error) {
	b, err := f.view()
	if err != nil {
		return nil, err
	}
	return b.Keys(ctx, prefix)
}

func (f *failoverBackend) Now(ctx context.Context) (time.Time, error) {
	b, err := f.view()
	if err != nil {
		return time.Time{}, err
	}
	return b.Now(ctx)
}

func (f *failoverBackend) fail() { f.mu.Lock(); f.down = true; f.mu.Unlock() }

func (f *failoverBackend) promote(next *storageBackend) {
	f.mu.Lock()
	f.active = next
	f.down = false
	f.mu.Unlock()
}

// errPrimaryFenced 旧主库被隔离：它的写入路径必须显式失败，而不是继续
// 接受旧持有者的提交（双主的第一信号）。
var errPrimaryFenced = errors.New("faultmatrix: 旧主库已被 fence，拒绝写入")

// fencedStorage 可隔离的旧主库视图。
type fencedStorage struct {
	*storageBackend
	fenced atomic.Bool
}

func (f *fencedStorage) check() error {
	if f.fenced.Load() {
		return errPrimaryFenced
	}
	return nil
}

func (f *fencedStorage) Snapshot(ctx context.Context, keys ...string) (map[string]coord.Entry, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	return f.storageBackend.Snapshot(ctx, keys...)
}

func (f *fencedStorage) Txn(ctx context.Context, asserts []coord.Assert, mutations []coord.Mutation) (bool, error) {
	if err := f.check(); err != nil {
		return false, err
	}
	return f.storageBackend.Txn(ctx, asserts, mutations)
}

func (f *fencedStorage) ScanSnapshot(ctx context.Context, prefix string) (map[string]coord.Entry, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	return f.storageBackend.ScanSnapshot(ctx, prefix)
}

func (f *fencedStorage) Keys(ctx context.Context, prefix string) ([]string, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	return f.storageBackend.Keys(ctx, prefix)
}

func (f *fencedStorage) Now(ctx context.Context) (time.Time, error) {
	if err := f.check(); err != nil {
		return time.Time{}, err
	}
	return f.storageBackend.Now(ctx)
}

// cloneStorage 把已提交状态复制到备库（模拟同步复制的可见性）。
// 只复制存活键与值；版本在新库重新铸造——切换后旧版本断言自然失效，
// 调用方只能取新快照重判，不会拿旧主库的版本锚继续提交。
func cloneStorage(t *testing.T, src, dst *coord.MemoryBackend) {
	t.Helper()
	ctx := context.Background()
	keys, err := src.Keys(ctx, "")
	if err != nil {
		t.Fatalf("读主库键集失败: %v", err)
	}
	for _, key := range keys {
		snap, err := src.Snapshot(ctx, key)
		if err != nil {
			t.Fatalf("读主库键 %s 失败: %v", key, err)
		}
		if !snap[key].Exists {
			continue
		}
		if _, err := dst.Txn(ctx, nil, []coord.Mutation{{Key: key, Val: snap[key].Val}}); err != nil {
			t.Fatalf("复制键 %s 到备库失败: %v", key, err)
		}
	}
}

func runDBFailover(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	primary := newStorageBackend(clock)
	oldPrimary := &fencedStorage{storageBackend: primary}
	failover := &failoverBackend{active: primary}

	board, err := coord.NewBoardAdapter(failover)
	if err != nil {
		t.Fatalf("装配主备切换任务板失败: %v", err)
	}
	oldBoard, err := coord.NewBoardAdapter(oldPrimary)
	if err != nil {
		t.Fatalf("装配旧主库任务板失败: %v", err)
	}
	const id taskboard.TaskID = "task-failover"
	const pending taskboard.TaskID = "task-pending"
	const ttl = time.Minute
	board.Seed(
		taskboard.Task{ID: id, State: taskboard.StateOpen},
		taskboard.Task{ID: pending, State: taskboard.StateOpen},
	)
	claimed, err := board.Claim(ctx, id, "worker-a", taskboard.ClaimRequest{TTL: ttl})
	if err != nil {
		t.Fatalf("切换前认领失败: %v", err)
	}

	// 备库复制已提交状态。
	standby := newStorageBackend(clock)
	cloneStorage(t, primary.MemoryBackend, standby.MemoryBackend)

	// 主库故障 + 旧主库被 fence：两条路径都必须显式失败。
	failover.fail()
	oldPrimary.fenced.Store(true)
	if _, err := board.Renew(ctx, id, claimed.Lease.Token, taskboard.ClaimRequest{TTL: ttl}); err == nil {
		t.Fatal("主库故障期续期必须显式失败")
	} else if errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("存储故障不得伪装成租约丢失（实得 %v）", err)
	}
	if _, err := oldBoard.Renew(ctx, id, claimed.Lease.Token, taskboard.ClaimRequest{TTL: ttl}); !errors.Is(err, errPrimaryFenced) {
		t.Fatalf("旧主库必须被 fence 拒绝写入（实得 %v）", err)
	}
	if _, err := board.Claim(ctx, pending, "worker-b", taskboard.ClaimRequest{TTL: ttl}); err == nil {
		t.Fatal("主库故障期认领必须显式失败")
	}

	// promote 备库：已提交状态可见，未提交的写入没有半写。
	failover.promote(standby)
	tasks, err := board.List(ctx)
	if err != nil {
		t.Fatalf("切换后备库读失败: %v", err)
	}
	byID := map[taskboard.TaskID]taskboard.Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	if got, ok := byID[id]; !ok || got.State != taskboard.StateClaimed ||
		got.Owner != "worker-a" || got.Lease.Token != claimed.Lease.Token {
		t.Fatalf("切换前已提交的认领必须在备库逐字可见（实得 %+v）", got)
	}
	if got, ok := byID[pending]; !ok || got.State != taskboard.StateOpen {
		t.Fatalf("故障期未提交的认领不得半写（实得 %+v）", got)
	}

	// 新主库继续同一租约：存储时钟仍是权威，令牌仍有效。
	if _, err := board.Renew(ctx, id, claimed.Lease.Token, taskboard.ClaimRequest{TTL: ttl}); err != nil {
		t.Fatalf("promote 后同一租约必须可续期: %v", err)
	}
	if err := board.Complete(ctx, id, claimed.Lease.Token); err != nil {
		t.Fatalf("promote 后必须能正常收场: %v", err)
	}
	// 旧主库仍被 fence：旧写入路径不得复活。
	if _, err := oldBoard.Renew(ctx, id, claimed.Lease.Token, taskboard.ClaimRequest{TTL: ttl}); !errors.Is(err, errPrimaryFenced) {
		t.Fatalf("旧主库 fence 不得因 promote 解除（实得 %v）", err)
	}
}

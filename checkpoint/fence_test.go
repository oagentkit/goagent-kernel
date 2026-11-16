// fence_test.go —— 带接管代次的条件提交判据（issue #265）。
//
// memFenced 是一份**按契约实现**的内存栅栏存储：代次判定与写入在同一把
// 锁内完成（模拟宿主侧「一条带双条件的 UPDATE」的原子性），并同时实现
// 不带代次的 SaveIfNewer——反向验证靠它：同一份迟到写入，不带代次的
// 路径会放行，带代次的路径必须拒绝。
package checkpoint

import (
	"context"
	"sync"
	"testing"

	"github.com/oekit/pkg/errors"
)

// memFenced 内存栅栏存储：per-run 记录 (快照, 已记录代次)。
type memFenced struct {
	mu    sync.Mutex
	data  map[string]Snapshot
	fence map[string]uint64
	saves int // 全部写入尝试（含被拒的），用于断言「被拒的写没碰到存储」
}

func newMemFenced() *memFenced {
	return &memFenced{data: map[string]Snapshot{}, fence: map[string]uint64{}}
}

// SaveIfNewer 不带代次的普通提交（StoreCAS 契约；缺陷对照路径）。
func (m *memFenced) SaveIfNewer(_ context.Context, s Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	if s.Seq == 0 {
		return ErrStaleSeq
	}
	if cur, ok := m.data[s.RunID]; ok && s.Seq <= cur.Seq {
		return ErrStaleSeq
	}
	m.data[s.RunID] = s.Clone()
	return nil
}

// SaveIfNewerFenced 带代次的条件提交（FencedStoreCAS 契约）：
// 代次判定与写入在同一把锁内（同一次原子操作）。
func (m *memFenced) SaveIfNewerFenced(_ context.Context, s Snapshot, fence uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	if s.Seq == 0 {
		return ErrStaleSeq
	}
	if cur, ok := m.fence[s.RunID]; ok && cur > fence {
		return ErrStaleFence // 陈旧代次的迟到写入：不写入
	}
	if cur, ok := m.data[s.RunID]; ok && s.Seq <= cur.Seq {
		return ErrStaleSeq
	}
	m.fence[s.RunID] = fence // 新代绑定（cur <= fence ⇒ 推进到 fence）
	m.data[s.RunID] = s.Clone()
	return nil
}

func (m *memFenced) Load(_ context.Context, runID string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.data[runID]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return s.Clone(), nil
}

func (m *memFenced) recordedFence(runID string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fence[runID]
}

// TestSaveIfNewerFencedContract 代次判定的四条分支与序号单判定并存。
func TestSaveIfNewerFencedContract(t *testing.T) {
	ctx := context.Background()
	store := newMemFenced()

	// 同一代持有者的分批提交（等于 ⇒ 通过）。
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 1, "running"), 1); err != nil {
		t.Fatalf("首代首次提交应通过：%v", err)
	}
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 2, "running"), 1); err != nil {
		t.Fatalf("同代分批提交应通过：%v", err)
	}
	// 新代接管：首次提交换绑（大于 ⇒ 通过并推进）。
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 3, "running"), 2); err != nil {
		t.Fatalf("新代首次提交应通过并换绑：%v", err)
	}
	if got := store.recordedFence("r1"); got != 2 {
		t.Fatalf("已记录代次应推进到 2，实得 %d", got)
	}

	// 陈旧代次的迟到写入：拒绝且不写入。
	before := store.saves
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 4, "running"), 1); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("陈旧代次的迟到提交必须被拒（ErrStaleFence）：%v", err)
	}
	if store.saves != before+1 {
		t.Fatal("被拒的写入尝试应被记录（但不得落库）")
	}
	if got, _ := store.Load(ctx, "r1"); got.Seq != 3 {
		t.Fatalf("被拒的写入不得落库（快照保持 seq=3），实得 %+v", got)
	}

	// 序号单调判定照旧：同代同序重复 = 陈旧序号。
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 3, "running"), 2); !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("序号不递增必须被拒（ErrStaleSeq）：%v", err)
	}
	// Seq==0 必拒（v0.33.0 契约，栅栏路径同样适用）。
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 0, "running"), 2); !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("Seq==0 必须被拒：%v", err)
	}
}

// TestStaleCommitLandsWithoutFence 反向验证（issue #265 验收第 4 条）：
// **把进度提交改成不带令牌 ⇒ 用例必须红**的判据面。
//
// 同一份陈旧代次的迟到快照（seq 更大、代次更旧），走不带代次的普通
// SaveIfNewer 会被放行（缺陷形态：旧持有者的进度覆盖新持有者的世界）；
// 走 SaveIfNewerFenced 被拒。两条路径的唯一差别就是**代次是否随提交
// 出示**——上游（worker.LeasedProgress）若不再出示凭据，本组断言翻红。
func TestStaleCommitLandsWithoutFence(t *testing.T) {
	ctx := context.Background()
	store := newMemFenced()

	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 1, "running"), 1); err != nil {
		t.Fatal(err)
	}
	// 新代接管并提交。
	if err := store.SaveIfNewerFenced(ctx, mkSnap("r1", 2, "running"), 2); err != nil {
		t.Fatal(err)
	}

	stale := mkSnap("r1", 3, "running") // 旧持有者的迟到快照（序号更大）
	if err := store.SaveIfNewerFenced(ctx, stale, 1); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("带代次的路径必须拒绝陈旧代次：%v", err)
	}
	// 缺陷形态仍在普通路径上：不带代次 ⇒ 放行 ⇒ 恢复点被旧持有者覆盖。
	if err := store.SaveIfNewer(ctx, stale); err != nil {
		t.Fatalf("反向锚点失效：不带代次的普通提交也应被放行（否则本断言不再构成对照）：%v", err)
	}
	if got, _ := store.Load(ctx, "r1"); got.Seq != 3 {
		t.Fatalf("普通路径应把迟到快照落库（缺陷形态实锤）：%+v", got)
	}
}

// TestCommitFenced 提交器的栅栏路径：闸前进、存储判定、失败回滚闸、
// 存储不支持时显式失败。
func TestCommitFenced(t *testing.T) {
	ctx := context.Background()

	t.Run("正常提交", func(t *testing.T) {
		store := newMemFenced()
		gate := NewSeqGate()
		c := NewCommitter(gate, store)
		if err := c.CommitFenced(ctx, mkSnap("r1", 1, "running"), 1); err != nil {
			t.Fatalf("带代次提交应成功：%v", err)
		}
		if got := c.Last("r1"); got != 1 {
			t.Fatalf("闸应前进到 1，实得 %d", got)
		}
	})

	t.Run("陈旧代次被拒且回滚闸", func(t *testing.T) {
		store := newMemFenced()
		gate := NewSeqGate()
		c := NewCommitter(gate, store)
		if err := c.CommitFenced(ctx, mkSnap("r1", 1, "running"), 2); err != nil {
			t.Fatal(err)
		}
		if err := c.CommitFenced(ctx, mkSnap("r1", 2, "running"), 1); !errors.Is(err, ErrStaleFence) {
			t.Fatalf("陈旧代次必须被拒：%v", err)
		}
		if got := c.Last("r1"); got != 1 {
			t.Fatalf("失败提交必须回滚进程内闸（否则该 run 卡死在 2），实得 %d", got)
		}
		if got, _ := store.Load(ctx, "r1"); got.Seq != 1 {
			t.Fatalf("被拒的写入不得落库：%+v", got)
		}
	})

	t.Run("存储不支持栅栏判据时显式失败", func(t *testing.T) {
		c := NewCommitter(NewSeqGate(), newMemCAS())
		err := c.CommitFenced(ctx, mkSnap("r1", 1, "running"), 1)
		if !errors.Is(err, ErrFenceUnsupported) {
			t.Fatalf("降级成不带代次的提交等于把缺口伪装成通过，必须显式失败：%v", err)
		}
	})

	t.Run("未装配存储", func(t *testing.T) {
		c := NewCommitter(NewSeqGate(), nil)
		if err := c.CommitFenced(ctx, mkSnap("r1", 1, "running"), 1); !errors.Is(err, ErrStoreMissing) {
			t.Fatalf("未装配存储必须显式失败：%v", err)
		}
	})
}

// 编译期锚：内存替身同时满足两份存储契约。
var (
	_ StoreCAS       = (*memFenced)(nil)
	_ FencedStoreCAS = (*memFenced)(nil)
)

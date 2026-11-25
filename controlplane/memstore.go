// memstore.go —— Store 的进程内参考实现（issue #189）。
//
// # 边界
//
// MemStore 的原子性来自**进程内互斥锁**：快照一致、版本锚、留痕与状态
// 同事务都只在单进程内成立。多副本（多进程）共享控制面状态时，宿主必须
// 把同一份 Store 契约落到自己的持久化存储上（状态行 + 审计行同一事务）；
// 拿 MemStore 当多副本存储，两个进程各持一份状态，介入可见性会显示为绿，
// 而 A 的强释在 B 的视图里从未发生（AGENTS.md「参考实现是可被误用的
// 资产」的原话场景）。
//
// 它在本仓的用途只有两个：①给 StorePlane 的契约用例当**测试保底**
// （两个 StorePlane 共享同一 MemStore 即「两个副本共享同一存储」的替身）；
// ②给宿主当**形态示范**——照三个 Snapshot/三个 Commit 写持久化版本。
//
// # 保留 MemPlane 的运行时面
//
// SeedRun / SeedEffect / Claim / Complete / Release 仍是参考运行时的最小
// 面（runtime.go）：控制面的介入判据必须对着真实的租约与代次演示。
// 这些方法不属于 Store 契约——宿主的真实运行时是自己的 taskboard.Board /
// sideeffect Runner。
package controlplane

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// MemStore Store 的进程内参考实现。经 NewMemStore 构造；零值可用；
// 并发安全。aud 为 nil 时所有 Commit 返回 ErrNoAuditor（留痕面缺位 ⇒
// 介入一律拒绝——与 MemPlane 的 fail-closed 口径一致）。
type MemStore struct {
	mu sync.Mutex
	// commitMu 串行化全部**写路径**（三个 Commit* 与运行时的 Seed/Claim/
	// Complete/Release/Renew）。留痕 Append 在它之下、m.mu 之外执行：
	// 提交序列仍然单线（版本锚在「校验→留痕→落状态」之间不会被别的写者
	// 推进），但宿主 Auditor 的回查/慢 I/O 不再占住状态锁——Snapshot*
	// 查询面照常可读（issue #327）。
	commitMu sync.Mutex

	aud    Auditor
	ver    Version
	tokens uint64 // 令牌铸造序号（参考实现不承诺随机性）
	// clock 存储时钟（UseClock 注入；nil = time.Now）。租约到期由存储
	// 自己的时刻判定——与 taskboard.ClaimRequest 的「TTL 按存储时钟推算」
	// 同一条口径（issue #335）。
	clock func() time.Time

	tasks   map[taskboard.TaskID]*memTask
	runs    map[RunID]*memRun
	effects map[string]*memEffect
}

// memTask 任务 + 该任务已铸造的最大接管代次（下一次认领 mint gen+1——
// 代次计数是租约状态的投影，不是第二个计数器：它只随认领递增）。
type memTask struct {
	task taskboard.Task
	gen  taskboard.Fence
	run  RunID // 反查 run（TaskView 需要跑 run 级不变量）
}

// memRun 运行状态。
type memRun struct {
	status  string
	gen     uint64 // 运行代次（Terminate 的乐观并发闸；终止/推进即前进）
	taskIDs []taskboard.TaskID
}

// memEffect 副作用键的裁决账（参考实现自己的最小对账面）。
type memEffect struct {
	uncertain bool
	unsafe    bool
	resolved  Verdict // uncertain=false 时是生效的结论
}

// NewMemStore 构造参考实现。aud 可为 nil——留痕面缺位时 Commit 一律
// 拒绝（ErrNoAuditor），这正是契约要演示的 fail-closed 形态。
func NewMemStore(aud Auditor) *MemStore {
	return &MemStore{
		aud:     aud,
		tasks:   make(map[taskboard.TaskID]*memTask),
		runs:    make(map[RunID]*memRun),
		effects: make(map[string]*memEffect),
	}
}

// 编译期契约断言：参考实现必须满足 Store。
var _ Store = (*MemStore)(nil)

// ensureLocked 惰性初始化零值状态（持锁调用）。
func (m *MemStore) ensureLocked() {
	if m.tasks == nil {
		m.tasks = make(map[taskboard.TaskID]*memTask)
	}
	if m.runs == nil {
		m.runs = make(map[RunID]*memRun)
	}
	if m.effects == nil {
		m.effects = make(map[string]*memEffect)
	}
}

// ── 快照 ──

// SnapshotTask 任务 + 所属 run 的全部任务（同一把锁内取，快照一致）。
func (m *MemStore) SnapshotTask(_ context.Context, id taskboard.TaskID) (TaskSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mt, ok := m.tasks[id]
	if !ok {
		return TaskSnapshot{}, errors.Wrap(ErrNotFound, "任务不存在: "+string(id))
	}
	snap := TaskSnapshot{Version: m.ver, RunID: mt.run, Task: cloneTask(mt.task)}
	if run := m.runs[mt.run]; run != nil {
		snap.RunTasks = m.runTasksLocked(run)
	} else {
		// 数据不变量上不会发生（SeedRun 同时建 run 与 task）；真发生了
		// 也至少让单任务视图可见，不把已存在的任务伪装成不存在。
		snap.RunTasks = []taskboard.Task{cloneTask(mt.task)}
	}
	return snap, nil
}

// SnapshotRun 运行 + 全部任务（同一把锁内取，快照一致）。
func (m *MemStore) SnapshotRun(_ context.Context, id RunID) (RunSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return RunSnapshot{}, errors.Wrap(ErrNotFound, "运行不存在: "+string(id))
	}
	return RunSnapshot{
		Version: m.ver,
		RunID:   id,
		Status:  run.status,
		Gen:     run.gen,
		Tasks:   m.runTasksLocked(run),
	}, nil
}

// SnapshotEffect 效果键裁决账。
func (m *MemStore) SnapshotEffect(_ context.Context, key string) (EffectSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	eff, ok := m.effects[key]
	if !ok {
		return EffectSnapshot{}, errors.Wrap(ErrNotFound, "效果键不存在: "+key)
	}
	return EffectSnapshot{
		Version: m.ver,
		Key:     key,
		Effect:  EffectState{Uncertain: eff.uncertain, Unsafe: eff.unsafe, Resolved: eff.resolved},
	}, nil
}

// runTasksLocked 取一个 run 的全部任务快照（按任务 id 排序，确定性输出）。
// 持锁调用。
func (m *MemStore) runTasksLocked(run *memRun) []taskboard.Task {
	out := make([]taskboard.Task, 0, len(run.taskIDs))
	for _, tid := range run.taskIDs {
		if mt := m.tasks[tid]; mt != nil {
			out = append(out, cloneTask(mt.task))
		}
	}
	slices.SortFunc(out, func(a, b taskboard.Task) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

// ── 提交（版本锚 + 留痕同事务）──
//
// 锁次序与原子性（issue #327）：commitMu 串行化写者，m.mu 只护内存状态。
// 每个 Commit 在 m.mu 内校验版本/存在性后**释放** m.mu，再在 m.mu 外调
// 宿主 Auditor.Append，最后回 m.mu 落状态。因为写者已被 commitMu 串行化，
// 校验与落状态之间没有第二个写者能推进版本——「留痕失败 ⇒ 状态一字不动」
// 的原子性仍成立。宿主 Auditor 回查 Snapshot* 或做慢 I/O 都不会持状态锁
// （查询面不经过 commitMu）。

// CommitTask 原子提交任务状态 + 留痕。
func (m *MemStore) CommitTask(ctx context.Context, expect Version, next taskboard.Task, entry AuditEntry) error {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()

	m.mu.Lock()
	if m.ver != expect {
		m.mu.Unlock()
		return errors.Wrap(ErrStoreConflict, "任务状态在快照之后已被另一个副本修改")
	}
	mt, ok := m.tasks[next.ID]
	aud := m.aud
	m.mu.Unlock()
	if !ok {
		return errors.Wrap(ErrNotFound, "任务不存在: "+string(next.ID))
	}
	if aud == nil {
		return ErrNoAuditor
	}
	// 留痕先落（状态锁之外）：失败 ⇒ 状态一字不动。
	if err := appendAudit(ctx, aud, entry); err != nil {
		return err
	}
	m.mu.Lock()
	mt.task = cloneTask(next)
	if next.Lease.Fence > mt.gen {
		mt.gen = next.Lease.Fence
	}
	m.ver++
	m.mu.Unlock()
	return nil
}

// CommitRun 原子提交运行状态/代次 + 留痕。
func (m *MemStore) CommitRun(ctx context.Context, expect Version, id RunID, next RunState, entry AuditEntry) error {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()

	m.mu.Lock()
	if m.ver != expect {
		m.mu.Unlock()
		return errors.Wrap(ErrStoreConflict, "运行状态在快照之后已被另一个副本修改")
	}
	run, ok := m.runs[id]
	aud := m.aud
	m.mu.Unlock()
	if !ok {
		return errors.Wrap(ErrNotFound, "运行不存在: "+string(id))
	}
	if aud == nil {
		return ErrNoAuditor
	}
	if err := appendAudit(ctx, aud, entry); err != nil {
		return err
	}
	m.mu.Lock()
	run.status = next.Status
	run.gen = next.Gen
	if next.TaskIDs != nil {
		run.taskIDs = slices.Clone(next.TaskIDs)
	}
	m.ver++
	m.mu.Unlock()
	return nil
}

// CommitEffect 原子提交裁决账 + 留痕。
func (m *MemStore) CommitEffect(ctx context.Context, expect Version, key string, next EffectState, entry AuditEntry) error {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()

	m.mu.Lock()
	if m.ver != expect {
		m.mu.Unlock()
		return errors.Wrap(ErrStoreConflict, "裁决账在快照之后已被另一个副本修改")
	}
	eff, ok := m.effects[key]
	aud := m.aud
	m.mu.Unlock()
	if !ok {
		return errors.Wrap(ErrNotFound, "效果键不存在: "+key)
	}
	if aud == nil {
		return ErrNoAuditor
	}
	if err := appendAudit(ctx, aud, entry); err != nil {
		return err
	}
	m.mu.Lock()
	eff.uncertain = next.Uncertain
	eff.unsafe = next.Unsafe
	eff.resolved = next.Resolved
	m.ver++
	m.mu.Unlock()
	return nil
}

// cloneTask 任务快照拷贝（Deps 深拷贝——账面切片不外借）。
func cloneTask(t taskboard.Task) taskboard.Task {
	t.Deps = slices.Clone(t.Deps)
	return t
}

// runtime.go —— MemStore 的**最小运行时面**（建场景与认领/提交/归还）。
//
// 控制面的介入判据必须对着运行时的租约与代次演示（强释之后旧令牌提交
// 被拒、下一次认领代次严格递增），没有运行时面就没有可演示的「同一套
// 判据」。这些方法不是 Store / Plane 契约的一部分——它们是「运行时那一侧」
// 的参考实现：宿主的真实运行时是自己的 taskboard.Board / sideeffect
// Runner / 状态存储。
//
// Seed* 先整单校验再落账：部分落账的 run 是半成品状态，视图会把
// 「建了一半」当成「建成了」。
package controlplane

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// SeedRun 运行时侧：建一个运行及其任务（taskID 全局唯一）。
// status 为空取 contract.StatusCreated。重复 run / 重复任务 → ErrAlreadyExists。
// 先整单校验再落账：部分落账的 run 是半成品状态，视图会把「建了一半」
// 当成「建成了」。
func (m *MemStore) SeedRun(id RunID, status string, tasks []taskboard.Task) error {
	if id == "" {
		return errors.Wrap(ErrInvalidRequest, "SeedRun: run id 为空")
	}
	if status == "" {
		status = contract.StatusCreated
	}
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if _, ok := m.runs[id]; ok {
		return errors.Wrap(ErrAlreadyExists, "SeedRun: run 已存在")
	}
	seen := make(map[taskboard.TaskID]bool, len(tasks))
	for _, t := range tasks {
		if t.ID == "" {
			return errors.Wrap(ErrInvalidRequest, "SeedRun: 任务缺 id")
		}
		if seen[t.ID] {
			return errors.Wrap(ErrInvalidRequest, "SeedRun: 同一 run 内任务 id 重复")
		}
		seen[t.ID] = true
		if _, exists := m.tasks[t.ID]; exists {
			return errors.Wrap(ErrAlreadyExists, "SeedRun: 任务已存在")
		}
	}
	run := &memRun{status: status, gen: 1}
	for _, t := range tasks {
		m.tasks[t.ID] = &memTask{task: cloneTask(t), gen: t.Lease.Fence, run: id}
		run.taskIDs = append(run.taskIDs, t.ID)
	}
	m.runs[id] = run
	m.ver++
	return nil
}

// SeedEffect 运行时侧：登记一个处于「结果未知」态的副作用键。
// unsafe 对应 sideeffect 的 RetrySafetyUnsafe（未生效裁决不自动开门）。
func (m *MemStore) SeedEffect(key string, unsafe bool) error {
	if key == "" {
		return errors.Wrap(ErrInvalidRequest, "SeedEffect: 效果键为空")
	}
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if _, ok := m.effects[key]; ok {
		return errors.Wrap(ErrAlreadyExists, "SeedEffect: 效果键已存在")
	}
	m.effects[key] = &memEffect{uncertain: true, unsafe: unsafe}
	m.ver++
	return nil
}

// UseClock 注入存储时钟（租约到期与 Until 推算都按它；nil 忽略）。
//
// 与 taskboard 的「TTL 由存储自己的时钟推算」同口径：跨进程时调用方的
// 时钟不可信，参考实现也必须能演示「到期由存储判定」。
//
// # 接缝契约（issue #369）
//
// @seam: no-reentry no-blocking panic-caller
//
//   - **不得重入**：回调内不得再调用 Claim / Renew 等会读取存储时钟的写路径
//     （会在回调里递归调用自己）。只读回查（SnapshotTask 等）是安全的：
//     实现保证时钟在 m.mu + commitMu **之外**被调用（issue #367）。
//   - **不得阻塞**：回调必须立即返回；它位于认领/续期路径的入口，阻塞会拖住
//     整条写路径。
//   - **panic 由调用方兜**（panic-caller）：内核不 recover 该回调的 panic。
//     时钟失读是装配/编程错误，应当响亮失败；需要隔离的宿主应在回调内 recover。
func (m *MemStore) UseClock(clock func() time.Time) {
	if clock == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = clock
}

// now 存储侧当前时刻（**调用方不得持有 m.mu / commitMu**）。
//
// clock 是宿主注入的回调，持锁调用会让回调回查本包时二次抢锁 ⇒ 永久死锁
// （issue #367）。这里只在短临界区内取出回调指针，调用发生在锁外；写路径
// 在进入 commitMu 之前先取 now，故宿主回调永远看不到内核的锁。
func (m *MemStore) now() time.Time {
	m.mu.Lock()
	clock := m.clock
	m.mu.Unlock()
	if clock != nil {
		return clock()
	}
	return time.Now()
}

// List 运行时侧：全部任务的一致快照（按 ID 排序，确定性输出）。
//
// 它是 taskboard.Board 的读面：参考实现也要能被共享契约套件（boardtest）
// 直接跑，判据才不是「为参考实现单独写一套」。
func (m *MemStore) List(_ context.Context) ([]taskboard.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	out := make([]taskboard.Task, 0, len(m.tasks))
	for _, mt := range m.tasks {
		out = append(out, cloneTask(mt.task))
	}
	slices.SortFunc(out, func(a, b taskboard.Task) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out, nil
}

// Claim 运行时侧：认领（铸造令牌与接管代次，语义对齐 taskboard.Board.Claim
// ——被**活着**持有 ErrTaken / 终态 ErrTerminal / 依赖未完成 ErrBlocked /
// 状态不在词表内 ErrUnknownState）。
//
// 「仍被持有」必须同时看状态与租约存活（taskboard.LeaseHeld，issue #335）：
// 只看 State == claimed 会让持有者崩溃后的任务永久不可接管——与 Board.Claim
// 的「被别人活着持有」契约及三个参考实现方向相反。
func (m *MemStore) Claim(_ context.Context, id taskboard.TaskID, agent taskboard.AgentID, ttl time.Duration) (taskboard.Task, error) {
	// 宿主时钟在双锁之外取值（issue #367）：回调回查 SnapshotTask 不会互锁。
	now := m.now()
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	mt, ok := m.tasks[id]
	if !ok {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrNotFound, "任务不存在: "+string(id))
	}
	// 状态可认领判定统一走 taskboard.State.Claimable（issue #360）：
	// 词表外 → ErrUnknownState，终态 → ErrTerminal。只判 StateDone 会让
	// 一条脏状态行照常领到租约。
	if cerr := mt.task.State.Claimable(); cerr != nil {
		return taskboard.Task{}, errors.Wrap(cerr, "任务状态不可认领",
			"task", string(id), "state", string(mt.task.State))
	}
	switch {
	case taskboard.LeaseHeld(mt.task, now):
		return taskboard.Task{}, errors.Wrap(taskboard.ErrTaken, "任务已被他人活着持有")
	}
	for _, d := range mt.task.Deps {
		dep, ok := m.tasks[d]
		if !ok || !dep.task.State.Terminal() {
			return taskboard.Task{}, errors.Wrap(taskboard.ErrBlocked, "依赖未完成: "+string(d))
		}
	}
	m.tokens++
	mt.gen++
	mt.task.State = taskboard.StateClaimed
	mt.task.Owner = agent
	mt.task.Lease = taskboard.Lease{
		Token: fmt.Sprintf("t-%d", m.tokens),
		Fence: mt.gen,
	}
	if ttl > 0 {
		mt.task.Lease.Until = now.Add(ttl)
	}
	mt.task.Seq++
	m.ver++
	return cloneTask(mt.task), nil
}

// Renew 运行时侧：续期并确认持有资格（taskboard.RenewingBoard 的参考实现）。
//
// 契约与 taskboard.Renew 一致：令牌不匹配 / 租约已过期 ⇒ ErrLeaseLost，
// **不做任何修改**；续期不递增代次、不换令牌。
func (m *MemStore) Renew(_ context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	// 宿主时钟在双锁之外取值（issue #367）：回调回查 SnapshotTask 不会互锁。
	now := m.now()
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	mt, ok := m.tasks[id]
	if !ok {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrNotFound, "任务不存在: "+string(id))
	}
	if mt.task.State != taskboard.StateClaimed || token == "" || token != mt.task.Lease.Token {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, "租约令牌不匹配（租约已易主或被释放过）")
	}
	if !taskboard.LeaseHeld(mt.task, now) {
		// 过期即资格终止：存储不能既承诺「过期随时可接管」又给死者续命。
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, "租约已过期，资格终止（请重新认领）")
	}
	if req.TTL > 0 {
		mt.task.Lease.Until = now.Add(req.TTL)
	} else {
		mt.task.Lease.Until = time.Time{}
	}
	mt.task.Seq++
	m.ver++
	return cloneTask(mt.task), nil
}

// Complete 运行时侧：凭 token 完成（旧令牌 / 非认领态 / **租约已过期**
// → taskboard.ErrLeaseLost）。
//
// 租约有效期与 Renew 同一口径（issue #497）：**资格在到期时刻终止**——租约
// 过期后、尚未被别人接管的窗口里，旧持有者提交终态会让终态内容取决于「谁先
// 提交」而不是「谁有资格提交」。故这里判 taskboard.LeaseHeld，过期即
// ErrLeaseLost 且**不做任何修改**（拒绝后任务仍是原状态，可被接管）。
func (m *MemStore) Complete(_ context.Context, id taskboard.TaskID, token string) error {
	// 宿主时钟在双锁之外取值（issue #367）：回调回查 SnapshotTask 不会互锁。
	now := m.now()
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	mt, ok := m.tasks[id]
	if !ok {
		return errors.Wrap(taskboard.ErrNotFound, "任务不存在: "+string(id))
	}
	if mt.task.State != taskboard.StateClaimed || token == "" || token != mt.task.Lease.Token {
		// 令牌不匹配 / 任务已不在认领态：一律 ErrLeaseLost——
		// 迟到持有者不被告知「任务其实已经易主到什么状态」。
		return errors.Wrap(taskboard.ErrLeaseLost, "令牌不匹配或租约已不在")
	}
	if !taskboard.LeaseHeld(mt.task, now) {
		// 过期即资格终止：存储不能既承诺「过期随时可接管」又让死者提交终态。
		return errors.Wrap(taskboard.ErrLeaseLost, "租约已过期，资格终止（请重新认领）")
	}
	return m.finishLocked(mt, taskboard.StateDone)
}

// Release 运行时侧：凭 token 归还认领（旧令牌 → taskboard.ErrLeaseLost）。
//
// Release **不判租约有效期**：归还是自愿放弃、不写终态，过期持有者归还不
// 产生错误结论（它至多让任务回到 open 让别人来领）——与 taskboard.Board
// 契约同口径（issue #497）。
func (m *MemStore) Release(_ context.Context, id taskboard.TaskID, token string) error {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	mt, ok := m.tasks[id]
	if !ok {
		return errors.Wrap(taskboard.ErrNotFound, "任务不存在: "+string(id))
	}
	if mt.task.State != taskboard.StateClaimed || token == "" || token != mt.task.Lease.Token {
		return errors.Wrap(taskboard.ErrLeaseLost, "令牌不匹配或租约已不在")
	}
	return m.finishLocked(mt, taskboard.StateOpen)
}

// finishLocked 收尾写入（调用方已持双锁并完成凭据判定）。
func (m *MemStore) finishLocked(mt *memTask, to taskboard.State) error {
	mt.task.State = to
	mt.task.Owner = ""
	mt.task.Lease = taskboard.Lease{}
	mt.task.Seq++
	m.ver++
	return nil
}

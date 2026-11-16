// memplane.go —— 控制面的进程内参考实现。
//
// # MemPlane 同时是两面
//
//   - **控制面**：实现 Plane 契约（查询 + 三类介入），演示介入判据怎么落在
//     「与运行时同一份状态」上——留痕与状态变更在同一个临界区里完成，
//     留痕失败即整体失败（没有「先改后补痕迹」的窗口）；
//   - **最小运行时面**：Claim / Complete / Release / Seed* 驱动场景——
//     控制面的介入判据必须对着运行时的租约与代次演示（强释之后旧令牌
//     提交被拒、下一次认领代次严格递增），没有运行时面就没有可演示的
//     「同一套判据」。
//
// 参考实现用单调计数器铸令牌（可读、可断言），**不承诺随机性**：宿主
// 实现存储时按 taskboard 的契约自行铸造（不可预测性是宿主的安全需求，
// 参考实现只演示判据）。进程内存储重启即失效——与 ticket 同一条理由：
// 控制面参考实现的价值在判据演示，不在持久化。
package controlplane

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/tenant"
)

// ErrAlreadyExists 目标已存在（Seed* 的重复登记；介入类动作不会报它——
// 介入对已存在目标的语义是执行，不是登记）。
var ErrAlreadyExists = errors.New("controlplane: 目标已存在")

// MemOption MemPlane 的装配选项。
type MemOption func(*memConfig)

// memConfig MemPlane 的装配期决策（只经 MemOption 显式给，零值 = 全部
// fail-closed）。
type memConfig struct {
	// anonRead 读路径（TaskView/RunView）跳过租户 fail-closed 判定。
	// 缺省 false：读与写同一道闸（issue #197）。
	anonRead bool
}

// AllowAnonymousRead 显式放开读路径的匿名访问（TaskView/RunView 不再做
// tenant.Require）。**缺省不放行**——控制面读路径与写路径共用同一道
// fail-closed 闸（issue #197）：宿主 Authorizer 实现有 bug（对任何请求
// 返回零值 tenant.Context + nil）时，写路径被 tenant.Require 拦下、
// 读路径若不设闸就成了「同样的鉴权钩子、同样的错误，只有读放行」的口子。
//
// 这是给「健康检查式只读」场景的**显式**出口：装配时写明，装配期可见，
// 不许静默缺省开。开了它之后读路径的可归责性由宿主的 Authorizer 独自
// 承担——内核不再有第二道闸。
func AllowAnonymousRead(v bool) MemOption {
	return func(c *memConfig) { c.anonRead = v }
}

// MemPlane 进程内参考实现。经 NewMemPlane 构造；并发安全。
type MemPlane struct {
	aud Auditor

	cfg memConfig

	mu      sync.Mutex
	tasks   map[taskboard.TaskID]*memTask
	runs    map[RunID]*memRun
	effects map[string]*memEffect
	tokens  uint64 // 令牌铸造序号（单调，参考实现不承诺随机性）
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

// memEffect 副作用键的裁决账（MemPlane 自己的最小对账面）。
type memEffect struct {
	uncertain bool
	unsafe    bool
	resolved  Verdict // uncertain=false 时是生效的结论
}

// NewMemPlane 构造参考实现。aud 可为 nil——留痕面缺位时介入一律拒绝
// （ErrNoAuditor），这正是契约要演示的 fail-closed 形态。
// opts 可变参装配选项（见 MemOption；缺省全部 fail-closed）。
func NewMemPlane(aud Auditor, opts ...MemOption) *MemPlane {
	c := memConfig{}
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	return &MemPlane{
		aud:     aud,
		cfg:     c,
		tasks:   make(map[taskboard.TaskID]*memTask),
		runs:    make(map[RunID]*memRun),
		effects: make(map[string]*memEffect),
	}
}

// MemPlane 实现的 Plane 实例化：taskboard 三型（issue #238 的「用本仓
// taskboard 的宿主不必动」路径）。契约是泛型的，本参考实现钉住这一档选型。
var _ Plane[taskboard.TaskID, taskboard.Task, taskboard.Problem] = (*MemPlane)(nil)

// ── 查询 ──

// requireTenant 读/写共用的租户 fail-closed 判定（issue #197）：返回错误
// 即拒绝。anonRead 只放开显式声明过的读路径。
func (m *MemPlane) requireTenant(ctx context.Context) error {
	if m.cfg.anonRead {
		return nil
	}
	_, err := tenant.Require(ctx)
	return err
}

// TaskView 实现 Plane：任务视图 + run 级不变量在该任务上的投影。
//
// 租户 fail-closed 与写路径同一道闸（issue #197）：上下文未注入租户信息
// → tenant.ErrNoContext——宿主 Authorizer 返回零值 Context 时读路径
// 同样 401，不留「只有读放行」的口子。匿名读须装配期显式
// AllowAnonymousRead(true)。
func (m *MemPlane) TaskView(ctx context.Context, id taskboard.TaskID) (TaskView[taskboard.Task, taskboard.Problem], error) {
	if err := m.requireTenant(ctx); err != nil {
		return TaskView[taskboard.Task, taskboard.Problem]{}, err
	}
	m.mu.Lock()
	mt, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return TaskView[taskboard.Task, taskboard.Problem]{}, errors.Wrap(ErrNotFound, "任务不存在: "+string(id))
	}
	tasks := m.runTasksLocked(mt.run)
	m.mu.Unlock()
	// 不变量是 run 级判据（依赖、环都要看全局），投到本任务身上。
	problems := taskboard.Check(tasks)
	own := make([]taskboard.Problem, 0, len(problems))
	for _, p := range problems {
		if p.Task == id {
			own = append(own, p)
		}
	}
	return TaskView[taskboard.Task, taskboard.Problem]{Task: cloneTask(mt.task), Problems: own}, nil
}

// RunView 实现 Plane：运行视图（状态 / 代次 / 全部任务 / 全 run 不变量）。
// 租户 fail-closed 与 TaskView 同一道闸（见上，issue #197）。
func (m *MemPlane) RunView(ctx context.Context, id RunID) (RunView[taskboard.Task, taskboard.Problem], error) {
	if err := m.requireTenant(ctx); err != nil {
		return RunView[taskboard.Task, taskboard.Problem]{}, err
	}
	m.mu.Lock()
	run, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return RunView[taskboard.Task, taskboard.Problem]{}, errors.Wrap(ErrNotFound, "运行不存在: "+string(id))
	}
	tasks := m.runTasksLocked(id)
	m.mu.Unlock()
	return RunView[taskboard.Task, taskboard.Problem]{
		RunID:    id,
		Status:   run.status,
		Gen:      run.gen,
		Tasks:    tasks,
		Problems: taskboard.Check(tasks),
	}, nil
}

// runTasksLocked 取一个 run 的全部任务快照（按任务 id 排序，确定性输出）。
// 持锁调用。
func (m *MemPlane) runTasksLocked(id RunID) []taskboard.Task {
	run := m.runs[id]
	out := make([]taskboard.Task, 0, len(run.taskIDs))
	for _, tid := range run.taskIDs {
		out = append(out, cloneTask(m.tasks[tid].task))
	}
	slices.SortFunc(out, func(a, b taskboard.Task) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// ── 介入 ──

// ForceRelease 实现 Plane：经闸换持有者（契约见 ForceRelease）。
//
// 留痕与状态变更在同一临界区：留痕失败 ⇒ 状态不变 ⇒ 整体失败——
// 不存在「改完了但没留痕」的窗口。
func (m *MemPlane) ForceRelease(ctx context.Context, req ForceRelease[taskboard.TaskID]) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if m.aud == nil {
		return ErrNoAuditor
	}
	if _, err := tenant.Require(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mt, ok := m.tasks[req.TaskID]
	if !ok {
		if err := m.audit(ctx, req.Operator, ActionForceRelease, string(req.TaskID), req.Basis, "任务不存在"); err != nil {
			return err
		}
		return errors.Wrap(ErrNotFound, "任务不存在: "+string(req.TaskID))
	}
	if mt.task.State != taskboard.StateClaimed {
		if err := m.audit(ctx, req.Operator, ActionForceRelease, string(req.TaskID), req.Basis, "任务不在认领态，无可释放的租约"); err != nil {
			return err
		}
		return errors.Wrap(taskboard.ErrLeaseLost,
			"该任务不在认领态：无可强制释放的租约（若这是介入重试，说明首次强释已生效）")
	}
	if err := m.audit(ctx, req.Operator, ActionForceRelease, string(req.TaskID), req.Basis, "已释放；下一次认领铸造的代次将大于被释放的那一代"); err != nil {
		return err
	}
	// 旧令牌随整份租约清零当场作废；代次不在此处递增——代次回答「第几任
	// 持有者」，强释结束当前任期而不开新任期，下一次认领 mint gen+1。
	mt.task.State = taskboard.StateOpen
	mt.task.Owner = ""
	mt.task.Lease = taskboard.Lease{}
	mt.task.Seq++
	return nil
}

// Adjudicate 实现 Plane：裁决未知结果（契约见 Adjudication；sideeffect 同口径）。
func (m *MemPlane) Adjudicate(ctx context.Context, req Adjudication) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if m.aud == nil {
		return ErrNoAuditor
	}
	if _, err := tenant.Require(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	eff, ok := m.effects[req.EffectKey]
	if !ok {
		if err := m.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis, "效果键不存在"); err != nil {
			return err
		}
		return errors.Wrap(ErrNotFound, "效果键不存在: "+req.EffectKey)
	}
	if !eff.uncertain {
		if err := m.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis, "结论已定，未覆盖（先到者为准）"); err != nil {
			return err
		}
		return sideeffect.ErrNotUncertain
	}
	if req.Verdict == VerdictNotApplied && eff.unsafe && !req.ForceRetryable {
		if err := m.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis, "Unsafe 动作凭未生效裁决不自动开门，需显式决策"); err != nil {
			return err
		}
		return sideeffect.ErrUnsafeNotRetryable
	}
	if err := m.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis, "已裁决: "+string(req.Verdict)); err != nil {
		return err
	}
	eff.uncertain = false
	eff.resolved = req.Verdict
	return nil
}

// Terminate 实现 Plane：凭运行代次终止（契约见 Termination）。
func (m *MemPlane) Terminate(ctx context.Context, req Termination) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if m.aud == nil {
		return ErrNoAuditor
	}
	if _, err := tenant.Require(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[req.RunID]
	if !ok {
		if err := m.audit(ctx, req.Operator, ActionTerminate, string(req.RunID), req.Basis, "运行不存在"); err != nil {
			return err
		}
		return errors.Wrap(ErrNotFound, "运行不存在: "+string(req.RunID))
	}
	if run.gen != req.ExpectedGen {
		if err := m.audit(ctx, req.Operator, ActionTerminate, string(req.RunID), req.Basis, "代次已前进，拒绝过期视图"); err != nil {
			return err
		}
		return ErrStaleRun
	}
	if terminalRun(run.status) {
		if err := m.audit(ctx, req.Operator, ActionTerminate, string(req.RunID), req.Basis, "运行已是终态"); err != nil {
			return err
		}
		return errors.Wrap(ErrTerminalRun, "当前状态: "+run.status)
	}
	// 候选变更先行（与 ForceRelease/Adjudicate 同一口径）：留痕失败 ⇒
	// 状态与代次一字不动（issue #180——一次失败的介入不得改变可观测状态，
	// 并发介入者的旧视图必须仍然成立）。留痕成功后才提交变更。
	nextGen := run.gen + 1 // 终止本身是一次代次前进：并发介入者的旧视图从此失效
	if err := m.audit(ctx, req.Operator, ActionTerminate, string(req.RunID), req.Basis, fmt.Sprintf("已终止（第 %d 代）", nextGen)); err != nil {
		return err
	}
	run.status = contract.StatusCancelled
	run.gen = nextGen
	return nil
}

// audit 追加介入留痕。失败向上传播（留痕失败 ⇒ 介入不生效——没有
// 「先改后补痕迹」的窗口）；只应在持有 m.mu 的临界区里调用（与状态变更原子）。
func (m *MemPlane) audit(ctx context.Context, operator string, action Action, target, basis, detail string) error {
	return m.aud.Append(ctx, AuditEntry{
		Operator: operator,
		Action:   action,
		Target:   target,
		Basis:    basis,
		Detail:   detail,
	})
}

// terminalRun 报告运行状态是否终态（与 contract 的终态词表一致）。
func terminalRun(status string) bool {
	switch status {
	case contract.StatusSucceeded, contract.StatusFailed, contract.StatusCancelled, contract.StatusTimedOut:
		return true
	}
	return false
}

// cloneTask 任务快照拷贝（Deps 深拷贝——账面切片不外借）。
func cloneTask(t taskboard.Task) taskboard.Task {
	t.Deps = slices.Clone(t.Deps)
	return t
}

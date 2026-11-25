// storeplane.go —— Plane 的**存储中立实现**（Store 后端；issue #189）。
//
// # 判定逻辑只有这一份
//
// 查询与三类介入的判据全部在本文件：快照读 → 判定 → 带版本锚的原子提交
// （状态 + 留痕同事务）。MemPlane 只是把 Store 固定成 MemStore 的薄壳
// （memplane.go）——参考实现与持久化实现走**同一份状态机**，「语义逐条
// 一致」不是靠两份代码互相校对，而是结构上只有一份。
//
// # 并发介入：乐观并发 + 重试
//
// 两个副本拿着同一份快照同时介入：先提交者推进版本，后到者的提交
// 因 ErrStoreConflict 被拒——StorePlane 在内部重读快照、重判（此时通常
// 会看到「已经不在认领态 / 代次已前进」），而不是把旧视图的结论硬写下去。
// 这正是 MemPlane 的互斥锁在存储中立形态下的对偶：锁换成了版本锚。
//
// # 留痕失败即整体失败
//
// 成功路径的留痕由 Store.Commit* 与状态写在同一次原子操作里：留痕失败
// ⇒ 状态一字不动（issue #180）。被拒路径（目标不存在 / 代次过期 /
// 已裁决）没有状态变更，留痕直接追加；追加失败同样向上传播——「介入
// 返回 error ⇒ 可观测状态未变」这条对成功与失败路径都成立。
package controlplane

import (
	"context"
	"fmt"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// storePlaneCASMaxAttempts 乐观并发重试上限。存储侧持续冲突说明有并发
// 介入者在抢同一目标；穷尽后把 ErrStoreConflict 交给调用方重试整个动作
// （介入动作是幂等的：重读重判后要么执行、要么得到「已生效」的结论）。
const storePlaneCASMaxAttempts = 32

// StorePlane 基于 Store 的 Plane 实现：查询 + 三类介入。
//
// 两个构造器对应两种装配形态：
//
//   - NewStorePlane：任务/运行/效果键全部走 Store（单进程参考实现或与
//     运行时同源的持久化 Store；同源与否由宿主保证）；
//   - NewAuthoritativeStorePlane：强制释放走运行时的 taskboard.Board、
//     裁决走副作用闸（sideeffect.Store）的同一份状态——结构上不构造第二份
//     可写任务/效果状态（issue #268）。
//
// 多个 StorePlane 实例共享同一个 Store 即多副本部署的替身（跨实例可见、
// 代次单调、冲突串行化）；并发安全与否取决于 Store 实现的并发契约。
type StorePlane struct {
	store Store
	aud   Auditor
	cfg   memConfig

	// tasks/effects 非 nil = 权威控制面（issue #268，NewAuthoritativeStorePlane）：
	// 任务快照与强制释放走 tasks（与运行时同一份任务板），裁决走 effects
	// （与副作用闸同一份状态）；不再调用 store 的任务/效果提交面。
	tasks   TaskAuthority
	effects EffectAuthority
}

// NewStorePlane 构造持久化控制面。
//
// store 为 nil ⇒ ErrNoStore（显式失败，不静默退化成进程内状态）；
// aud 为 nil ⇒ 介入一律 ErrNoAuditor（查询面仍可用）。
//
// aud 必须与 Store 的留痕落点是**同一本账**：成功路径的留痕由
// Store.Commit* 与状态同事务写入，被拒路径（目标不存在 / 代次过期 /
// 已裁决）由 StorePlane 直接 Append——两个账会让「谁在什么时候依据什么
// 介入过」在成功与失败两类记录之间分裂。
func NewStorePlane(store Store, aud Auditor, opts ...MemOption) (*StorePlane, error) {
	if store == nil {
		return nil, errors.Wrap(ErrNoStore, "controlplane: 持久化 Plane 缺状态存储（介入无处落地）")
	}
	return newStorePlane(store, aud, newMemConfig(opts)), nil
}

// newStorePlane 免检构造（MemPlane 已持有非 nil 的 MemStore）。
func newStorePlane(store Store, aud Auditor, cfg memConfig) *StorePlane {
	return &StorePlane{store: store, aud: aud, cfg: cfg}
}

// 编译期契约断言。
var _ Plane = (*StorePlane)(nil)

// requireTenant 读/写共用的租户 fail-closed 判定（issue #197）：返回错误
// 即拒绝。anonRead 只放开显式声明过的读路径。
func (p *StorePlane) requireTenant(ctx context.Context) error {
	if p.cfg.anonRead {
		return nil
	}
	_, err := tenant.Require(ctx)
	return err
}

// audit 追加一条介入留痕（只用于**没有状态变更**的路径；成功路径的
// 留痕随 Store.Commit* 同事务落地）。
func (p *StorePlane) audit(ctx context.Context, operator string, action Action, target, basis, detail string) error {
	if p.aud == nil {
		return ErrNoAuditor
	}
	// 与 MemStore 的成功路径共用同一个接缝包装：锁外调用 + panic 隔离，
	// 两个实现对同一宿主方法的暴露面口径一致（issue #327）。
	return appendAudit(ctx, p.aud, AuditEntry{
		Operator: operator,
		Action:   action,
		Target:   target,
		Basis:    basis,
		Detail:   detail,
	})
}

// ── 查询 ──

// TaskView 实现 Plane：任务视图 + run 级不变量在该任务上的投影。
func (p *StorePlane) TaskView(ctx context.Context, id taskboard.TaskID) (TaskView, error) {
	if p.tasks != nil {
		return p.taskViewAuthoritative(ctx, id)
	}
	if err := p.requireTenant(ctx); err != nil {
		return TaskView{}, err
	}
	snap, err := p.store.SnapshotTask(ctx, id)
	if err != nil {
		return TaskView{}, err
	}
	// 不变量是 run 级判据（依赖、环都要看全局），投到本任务身上。
	problems := taskboard.Check(snap.RunTasks)
	own := make([]taskboard.Problem, 0, len(problems))
	for _, problem := range problems {
		if problem.Task == id {
			own = append(own, problem)
		}
	}
	return TaskView{Task: cloneTask(snap.Task), Problems: own}, nil
}

// RunView 实现 Plane：运行视图（状态 / 代次 / 全部任务 / 全 run 不变量）。
func (p *StorePlane) RunView(ctx context.Context, id RunID) (RunView, error) {
	if err := p.requireTenant(ctx); err != nil {
		return RunView{}, err
	}
	snap, err := p.store.SnapshotRun(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	return RunView{
		RunID:    id,
		Status:   snap.Status,
		Gen:      snap.Gen,
		Tasks:    snap.Tasks,
		Problems: taskboard.Check(snap.Tasks),
	}, nil
}

// ── 介入 ──

// ForceRelease 实现 Plane：经闸换持有者（契约见 ForceRelease 请求类型）。
func (p *StorePlane) ForceRelease(ctx context.Context, req ForceRelease) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if p.aud == nil {
		return ErrNoAuditor
	}
	if _, err := tenant.Require(ctx); err != nil {
		return err
	}
	if p.tasks != nil {
		// 权威模式：直接复用运行时任务板的原子状态迁移（issue #268）。
		return p.forceReleaseAuthoritative(ctx, req)
	}
	target := string(req.TaskID)
	for range storePlaneCASMaxAttempts {
		snap, err := p.store.SnapshotTask(ctx, req.TaskID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				if aerr := p.audit(ctx, req.Operator, ActionForceRelease, target, req.Basis, "任务不存在"); aerr != nil {
					return aerr
				}
			}
			return err
		}
		if snap.Task.State != taskboard.StateClaimed {
			if aerr := p.audit(ctx, req.Operator, ActionForceRelease, target, req.Basis,
				"任务不在认领态，无可释放的租约"); aerr != nil {
				return aerr
			}
			return errors.Wrap(taskboard.ErrLeaseLost,
				"该任务不在认领态：无可强制释放的租约（若这是介入重试，说明首次强释已生效）")
		}
		// 旧令牌随整份租约清零当场作废；代次不在此处递增——代次回答
		// 「第几任持有者」，强释结束当前任期而不开新任期，下一次认领
		// mint gen+1。
		next := cloneTask(snap.Task)
		next.State = taskboard.StateOpen
		next.Owner = ""
		next.Lease = taskboard.Lease{}
		next.Seq++
		entry := AuditEntry{
			Operator: req.Operator,
			Action:   ActionForceRelease,
			Target:   target,
			Basis:    req.Basis,
			Detail:   "已释放；下一次认领铸造的代次将大于被释放的那一代",
		}
		err = p.store.CommitTask(ctx, snap.Version, next, entry)
		if errors.Is(err, ErrStoreConflict) {
			continue // 并发副本先提交了：重读重判
		}
		return err
	}
	return errors.Wrap(ErrStoreConflict, "controlplane: 强释在重试上限内未提交（并发介入持续冲突）")
}

// Adjudicate 实现 Plane：裁决未知结果（契约见 Adjudication 请求类型）。
func (p *StorePlane) Adjudicate(ctx context.Context, req Adjudication) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if p.aud == nil {
		return ErrNoAuditor
	}
	if _, err := tenant.Require(ctx); err != nil {
		return err
	}
	if p.tasks != nil || p.effects != nil {
		// 权威模式：裁决落到副作用闸的同一份状态（issue #268）。
		return p.adjudicateAuthoritative(ctx, req)
	}
	for range storePlaneCASMaxAttempts {
		snap, err := p.store.SnapshotEffect(ctx, req.EffectKey)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				if aerr := p.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis, "效果键不存在"); aerr != nil {
					return aerr
				}
			}
			return err
		}
		if !snap.Effect.Uncertain {
			if aerr := p.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis,
				"结论已定，未覆盖（先到者为准）"); aerr != nil {
				return aerr
			}
			return sideeffect.ErrNotUncertain
		}
		if req.Verdict == VerdictNotApplied && snap.Effect.Unsafe && !req.ForceRetryable {
			if aerr := p.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis,
				"Unsafe 动作凭未生效裁决不自动开门，需显式决策"); aerr != nil {
				return aerr
			}
			return sideeffect.ErrUnsafeNotRetryable
		}
		next := snap.Effect
		next.Uncertain = false
		next.Resolved = req.Verdict
		entry := AuditEntry{
			Operator: req.Operator,
			Action:   ActionAdjudicate,
			Target:   req.EffectKey,
			Basis:    req.Basis,
			Detail:   "已裁决: " + string(req.Verdict),
		}
		err = p.store.CommitEffect(ctx, snap.Version, req.EffectKey, next, entry)
		if errors.Is(err, ErrStoreConflict) {
			continue
		}
		return err
	}
	return errors.Wrap(ErrStoreConflict, "controlplane: 裁决在重试上限内未提交（并发介入持续冲突）")
}

// Terminate 实现 Plane：凭运行代次终止（契约见 Termination 请求类型）。
func (p *StorePlane) Terminate(ctx context.Context, req Termination) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if p.aud == nil {
		return ErrNoAuditor
	}
	if _, err := tenant.Require(ctx); err != nil {
		return err
	}
	target := string(req.RunID)
	for range storePlaneCASMaxAttempts {
		snap, err := p.store.SnapshotRun(ctx, req.RunID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				if aerr := p.audit(ctx, req.Operator, ActionTerminate, target, req.Basis, "运行不存在"); aerr != nil {
					return aerr
				}
			}
			return err
		}
		if snap.Gen != req.ExpectedGen {
			if aerr := p.audit(ctx, req.Operator, ActionTerminate, target, req.Basis,
				"代次已前进，拒绝过期视图"); aerr != nil {
				return aerr
			}
			return ErrStaleRun
		}
		switch contract.ClassifyStatus(snap.Status) {
		case contract.StatusTerminal:
			if aerr := p.audit(ctx, req.Operator, ActionTerminate, target, req.Basis, "运行已是终态"); aerr != nil {
				return aerr
			}
			return errors.Wrap(ErrTerminalRun, "当前状态: "+snap.Status)
		case contract.StatusUnknown:
			// 词表外取值不得按「非终态」放行（issue #334）：它可能是宿主
			// 自定义终态，改写它就是覆盖历史事实。与 checkpoint 的
			// statusUnknown 同方向——判不了就阻塞，交宿主显式决定。
			if aerr := p.audit(ctx, req.Operator, ActionTerminate, target, req.Basis,
				"状态不在已知词表内，无法判定是否终态，拒绝终止"); aerr != nil {
				return aerr
			}
			return errors.Wrap(ErrUnknownRunStatus, "当前状态: "+snap.Status)
		}
		// 候选变更先行：留痕失败 ⇒ 状态与代次一字不动（Store.CommitRun
		// 的原子性；issue #180）。留痕成功后才提交变更。
		nextGen := snap.Gen + 1 // 终止本身是一次代次前进：并发介入者的旧视图从此失效
		next := RunState{
			Status:  contract.StatusCancelled,
			Gen:     nextGen,
			TaskIDs: taskIDsOf(snap.Tasks),
		}
		entry := AuditEntry{
			Operator: req.Operator,
			Action:   ActionTerminate,
			Target:   target,
			Basis:    req.Basis,
			Detail:   fmt.Sprintf("已终止（第 %d 代）", nextGen),
		}
		err = p.store.CommitRun(ctx, snap.Version, req.RunID, next, entry)
		if errors.Is(err, ErrStoreConflict) {
			continue
		}
		return err
	}
	return errors.Wrap(ErrStoreConflict, "controlplane: 终止在重试上限内未提交（并发介入持续冲突）")
}

// taskIDsOf 把任务快照的 id 列表取出（RunState.TaskIDs 用）。
func taskIDsOf(tasks []taskboard.Task) []taskboard.TaskID {
	out := make([]taskboard.TaskID, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

// 运行状态的三态归类直接取自 contract.ClassifyStatus（issue #363）。
//
// 本包此前**自建**了一份三态分类器（runStatusClass / runStatusOpen /
// runStatusTerminal / runStatusUnknown + classifyRunStatus），注释写着「终态
// 词表取自 contract——不在本包另立一份」，但它自己列举了已知过程态
//（"" / created / running / waiting_approval）——**立的就是另一份**。
// checkpoint 侧当时有一份逐字同构的副本（statusClass / classifyStatus），
// 两处注释都这么写。
//
// 后果不是当下的错误结论（两份今天完全同构），而是**静默分叉**的入口：
// 同一个状态在两包得到相反结论（checkpoint 当可续跑 vs controlplane 当需
// 终止），而两份边界要一起改才会一致、这件事没有任何机制记录。
//
// 现在「什么算词表外」这条边界只有一份：contract.ClassifyStatus。本包只决定
// **处置方向**（未知 ⇒ 拒绝终止），不决定**分类**。
//
// 三态本身仍不可省（issue #334）：「不在终态词表内」把**词表外**取值也吞进
// 「非终态」，宿主自定义终态（如 "aborted"）会被当成可终止并静默改写。

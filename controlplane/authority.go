// authority.go —— 控制面与运行时/副作用闸的**权威写入面**（issue #268）。
//
// # 它补的是哪一段
//
// 旧形态里控制面的强制释放走 controlplane.Store.CommitTask，Worker 走
// taskboard.Board——两个接口不强制共享同一条权威任务记录。宿主各接一份
// 存储时，运维「强制释放」只改了一个投影：任务板仍认为旧令牌有效，旧
// Worker 继续执行、继续提交；两份状态各显示正常。
//
// 本文件给两条**权威面**，由宿主在装配期注入：
//
//   - TaskAuthority：必须就是 Worker 使用的 taskboard.Board（或其同一事务
//     后端/同一张表的包装）。ForceRelease 直接调用它凭令牌释放，令牌/代次
//     在同一处判定——旧 Worker 的 Renew/Complete 由同一份任务板拒收。
//     authoritative 模式**不再**调用 Store.CommitTask：结构上不存在第二份
//     可写任务状态；
//   - EffectAuthority：必须落到副作用闸（sideeffect.Store.ResolveUncertain）
//     读取的同一份状态，而不是控制面自己维护的效果投影。裁决之后闸看到的
//     就是这份结论。
//
// # 装配期同源校验
//
// 内核不能靠类型猜「两份存储是不是同一张表」。宿主可选实现 TaskSource，
// 让 Store 与 TaskAuthority 各报一个来源标识；两者都实现且不一致 ⇒
// ErrAuthorityMismatch（装配期拒绝）。不实现则跳过这项校验——校验是宿主
// 显式声明的前提，不是内核替它假设。
package controlplane

import (
	"context"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// TaskAuthority 任务状态的唯一权威写入面（issue #268）。
//
// 生产实现必须是运行时同一个 taskboard.Board：强制释放与 Worker 的
// Renew/Complete 在同一处判定令牌与代次。taskboard.Board 本身满足本接口。
type TaskAuthority interface {
	// List 权威任务快照（与运行时同一份）。
	List(ctx context.Context) ([]taskboard.Task, error)
	// Release 凭令牌把认领态原子迁移回 open（等同 taskboard.Board.Release）。
	// 令牌不匹配/已被释放 ⇒ taskboard.ErrLeaseLost。
	Release(ctx context.Context, id taskboard.TaskID, token string) error
}

// EffectAuthority 副作用裁决的权威面（issue #268）。
//
// 实现必须把结论落到**副作用闸读取的同一份状态**（sideeffect.Store.
// ResolveUncertain）；控制面据此观察「同一份」而不是再写一个投影。
type EffectAuthority interface {
	// ResolveUncertain 裁决一个「结果未知」的副作用键。
	//
	// applied=true ⇒ 记为已完成；false ⇒ 确认未生效（Unsafe 动作仍受
	// ForceRetryable 约束）。不处于未知态 ⇒ sideeffect.ErrNotUncertain；
	// Unsafe 且未显式放行 ⇒ sideeffect.ErrUnsafeNotRetryable。
	ResolveUncertain(ctx context.Context, key string, applied, forceRetryable bool, reason string) error
}

// TaskSource 可选的装配期同源声明（issue #268）。
//
// 同一份任务表/同一个任务板必须返回相同的非空来源标识。Store 与
// TaskAuthority 都实现它时，NewAuthoritativeStorePlane 要求两者相等。
type TaskSource interface {
	TaskSourceID() string
}

// 装配/裁决面的失败哨兵。
var (
	// ErrAuthorityMismatch 控制面存储与任务权威面不是同一份（装配期拒绝）。
	ErrAuthorityMismatch = errors.New("controlplane: 控制面存储与任务权威面不同源（装配期拒绝）")
	// ErrNoEffectAuthority 权威控制面未装配副作用权威面：裁决无处落地（fail-closed）。
	ErrNoEffectAuthority = errors.New("controlplane: 未装配副作用权威面（裁决必须落到副作用闸的同一份状态）")
)

// EffectResolver sideeffect.Store 的裁决子集（structural：完整 Store 自动满足）。
type EffectResolver interface {
	ResolveUncertain(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error)
}

// SideEffectAuthority 用 sideeffect.Store 实现 EffectAuthority 的参考适配器。
//
// ActionOf 由宿主提供：把控制面的 EffectKey 映射成 sideeffect.Action
// （Resource / Payload / RetrySafety 是宿主词表，内核不解释）。
type SideEffectAuthority struct {
	Store    EffectResolver
	ActionOf func(key string) (sideeffect.Action, bool)
}

// ResolveUncertain 实现 EffectAuthority。
func (a *SideEffectAuthority) ResolveUncertain(ctx context.Context, key string, applied, forceRetryable bool, reason string) error {
	if a.Store == nil {
		return errors.Wrap(ErrNoEffectAuthority, "controlplane: SideEffectAuthority 缺 sideeffect 存储")
	}
	if a.ActionOf == nil {
		return errors.Wrap(sideeffect.ErrNotUncertain, "controlplane: 未配置 EffectKey → Action 映射")
	}
	act, ok := a.ActionOf(key)
	if !ok {
		return errors.Wrap(ErrNotFound, "效果键未登记: "+key)
	}
	_, _, err := a.Store.ResolveUncertain(ctx, act, sideeffect.Resolution{
		Applied:        applied,
		Reason:         reason,
		ForceRetryable: forceRetryable,
	})
	return err
}

// NewAuthoritativeStorePlane 构造任务/副作用权威统一的控制面（issue #268）。
//
// tasks 必填且必须与运行时同一份 taskboard.Board；effects 缺失时裁决
// fail-closed（ErrNoEffectAuthority）。store 只提供运行状态（SnapshotRun/
// CommitRun 等）——本实现**不调用** store 的任务/效果提交面，结构上不存在
// 第二份可写任务状态或效果投影。
func NewAuthoritativeStorePlane(store Store, aud Auditor, tasks TaskAuthority, effects EffectAuthority, opts ...MemOption) (*StorePlane, error) {
	if store == nil {
		return nil, errors.Wrap(ErrNoStore, "controlplane: 权威控制面缺状态存储（运行状态无处落地）")
	}
	if tasks == nil {
		return nil, errors.Wrap(ErrAuthorityMismatch,
			"controlplane: 权威控制面缺任务权威面——强制释放必须经运行时的同一份任务板")
	}
	if err := checkTaskSource(store, tasks); err != nil {
		return nil, err
	}
	return &StorePlane{
		store:   store,
		aud:     aud,
		cfg:     newMemConfig(opts),
		tasks:   tasks,
		effects: effects,
	}, nil
}

// checkTaskSource 宿主声明了来源标识时校验同源；未声明则跳过。
func checkTaskSource(store Store, tasks TaskAuthority) error {
	ss, ok := store.(TaskSource)
	if !ok {
		return nil
	}
	ts, ok := tasks.(TaskSource)
	if !ok {
		return errors.Wrap(ErrAuthorityMismatch,
			"controlplane: Store 声明了 TaskSourceID 而任务权威面没有——无法判定同源")
	}
	if ss.TaskSourceID() == "" || ss.TaskSourceID() != ts.TaskSourceID() {
		return errors.Wrap(ErrAuthorityMismatch,
			"controlplane: 来源标识不一致（store="+ss.TaskSourceID()+" tasks="+ts.TaskSourceID()+"）")
	}
	return nil
}

// taskViewAuthoritative 任务视图走权威任务板（整板快照 + 不变量投影）。
func (p *StorePlane) taskViewAuthoritative(ctx context.Context, id taskboard.TaskID) (TaskView, error) {
	if err := p.requireTenant(ctx); err != nil {
		return TaskView{}, err
	}
	tasks, err := p.tasks.List(ctx)
	if err != nil {
		return TaskView{}, errors.Wrap(err, "controlplane: 读权威任务快照失败")
	}
	var own *taskboard.Task
	for i := range tasks {
		if tasks[i].ID == id {
			own = &tasks[i]
			break
		}
	}
	if own == nil {
		return TaskView{}, errors.Wrap(ErrNotFound, "任务不存在: "+string(id))
	}
	problems := taskboard.Check(tasks)
	filtered := make([]taskboard.Problem, 0, len(problems))
	for _, problem := range problems {
		if problem.Task == id {
			filtered = append(filtered, problem)
		}
	}
	return TaskView{Task: cloneTask(*own), Problems: filtered}, nil
}

// forceReleaseAuthoritative 经权威任务板强制释放（issue #268）。
//
// 并发语义：与 Worker 的 Complete/接管同时发生时，Board.Release 凭快照里的
// 令牌判定——只有一方能赢；输家得到 taskboard.ErrLeaseLost（可观测），
// 控制面重读重判后给出「已不在认领态」的结论。
func (p *StorePlane) forceReleaseAuthoritative(ctx context.Context, req ForceRelease) error {
	// 留痕面缺位 ⇒ 介入一律拒绝（ErrNoAuditor）要在**任何状态变更之前**：
	// 旧实现把这道检查放在 Release 成功之后——「租约已放、才发现无留痕面」
	// 与「留痕失败但状态已变」是同一家族的事故（介入留痕与状态同事务）。
	if p.aud == nil {
		return ErrNoAuditor
	}
	target := string(req.TaskID)
	for range storePlaneCASMaxAttempts {
		tasks, err := p.tasks.List(ctx)
		if err != nil {
			return errors.Wrap(err, "controlplane: 读权威任务快照失败")
		}
		var cur *taskboard.Task
		for i := range tasks {
			if tasks[i].ID == req.TaskID {
				cur = &tasks[i]
				break
			}
		}
		if cur == nil {
			if aerr := p.audit(ctx, req.Operator, ActionForceRelease, target, req.Basis, "任务不存在"); aerr != nil {
				return aerr
			}
			return errors.Wrap(ErrNotFound, "任务不存在: "+target)
		}
		if cur.State != taskboard.StateClaimed {
			if aerr := p.audit(ctx, req.Operator, ActionForceRelease, target, req.Basis,
				"任务不在认领态，无可释放的租约"); aerr != nil {
				return aerr
			}
			return errors.Wrap(taskboard.ErrLeaseLost,
				"该任务不在认领态：无可强制释放的租约（若这是介入重试，说明首次强释已生效）")
		}
		// 成功路径的留痕条目在动作**之前**构造好：原子路径把它带进同一事务，
		// 两步形态在动作成功后追加。
		entry := AuditEntry{
			Operator: req.Operator,
			Action:   ActionForceRelease,
			Target:   target,
			Basis:    req.Basis,
			Detail:   "经权威任务板强制释放；旧令牌已作废",
		}
		// 原子路径：任务权威实现 ForceReleaseAtomic ⇒ 「校验 + 释放 + 留痕」
		// 收敛进同一事务提交，「已释放但无成功留痕」的窗口闭合。
		// 探测必须在分步 Release **之前**——在 Release 成功之后才走原子，
		// 等于把同一动作施加两遍（第二遍必撞 ErrLeaseLost）。
		if atomic, ok := p.tasks.(ForceReleaseAtomic); ok {
			return atomic.ForceReleaseWithAudit(ctx, req.TaskID, cur.Lease.Token, entry)
		}
		err = p.tasks.Release(ctx, req.TaskID, cur.Lease.Token)
		if errors.Is(err, taskboard.ErrLeaseLost) || errors.Is(err, taskboard.ErrNotFound) {
			continue // 与 Worker 的收场/接管并发：重读重判（只有一方能赢）
		}
		if err != nil {
			return errors.Wrap(err, "controlplane: 权威任务板拒绝强制释放")
		}
		// 两步形态（窗口如实声明）：此刻任务板已裁决（旧令牌当场作废），留痕
		// 随后——留痕失败 ⇒ 租约已释放而缺成功记录，调用方须对账审计平台。
		// 消窗 = 让任务权威实现 ForceReleaseAtomic（见 intervention_atomic.go）。
		return appendAudit(ctx, p.aud, entry)
	}
	return errors.Wrap(ErrStoreConflict, "controlplane: 强释在重试上限内未提交（并发介入持续冲突）")
}

// adjudicateAuthoritative 裁决经副作用闸的同一份状态（issue #268）。
func (p *StorePlane) adjudicateAuthoritative(ctx context.Context, req Adjudication) error {
	if p.effects == nil {
		return errors.Wrap(ErrNoEffectAuthority, "controlplane: 裁决需要副作用权威面")
	}
	applied := req.Verdict == VerdictApplied
	err := p.effects.ResolveUncertain(ctx, req.EffectKey, applied, req.ForceRetryable, req.Basis)
	switch {
	case errors.Is(err, sideeffect.ErrNotUncertain):
		if aerr := p.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis,
			"结论已定，未覆盖（先到者为准）"); aerr != nil {
			return aerr
		}
		return err
	case errors.Is(err, sideeffect.ErrUnsafeNotRetryable):
		if aerr := p.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis,
			"Unsafe 动作凭未生效裁决不自动开门，需显式决策"); aerr != nil {
			return aerr
		}
		return err
	case errors.Is(err, ErrNotFound):
		if aerr := p.audit(ctx, req.Operator, ActionAdjudicate, req.EffectKey, req.Basis, "效果键不存在"); aerr != nil {
			return aerr
		}
		return err
	case err != nil:
		return err
	}
	entry := AuditEntry{
		Operator: req.Operator,
		Action:   ActionAdjudicate,
		Target:   req.EffectKey,
		Basis:    req.Basis,
		Detail:   "已裁决: " + string(req.Verdict) + "（副作用闸同一份状态）",
	}
	// 两步形态（窗口如实声明）：此刻裁决已生效，留痕随后——留痕失败 ⇒
	// 裁决已落而缺成功记录。效果权威的原子接缝（与 ForceReleaseAtomic 同构）
	// 待参考实现就绪后提供，届时在此探测。
	return appendAudit(ctx, p.aud, entry)
}

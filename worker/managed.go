// managed.go —— 受管任务执行器：Claim → SideEffect.Execute → Checkpoint → Complete
// 的标准编排。
//
// # 它补的是哪一段（issue #191）
//
// @@Loop@@ 的语义是「认领 → 调 Handler → 成功 Complete / 失败 Release」——
// 失败即回板，可被重做。这条默认语义作用在**不可幂等**动作上就是重复施加：
// 下单、发信、改远程状态这类动作重跑一次，副作用就双份。
//
// 本执行器把「执行意图与对账」强制接进任务生命周期，四步不能重排、不能漏：
//
//  1. **Claim**：拿租约与令牌（令牌由任务板铸造——任务归属的唯一凭据）；
//  2. **SideEffect.Execute**：走 @@sideeffect.Runner@@ 的既有契约——
//     Acquire 原子登记执行意图 + 栅栏令牌，施加前做持有资格检查，
//     失败按 @@RetrySafety@@ 分流出「可重试」与「结果未知」；
//  3. **Checkpoint**：副作用记账成功后单调提交进度快照（不覆盖更高版本）；
//  4. **Complete**：凭第 1 步的令牌收口。
//
// # 不发明第二套状态机
//
// 本执行器**只做编排**：每个判定都调用既有原语，不复制任何状态。
//
//   - 「这次能不能做」归 @@sideeffect.Store.Acquire@@（已完成 / 结果未知 /
//     执行中 / 资源被占 / 授予）；
//   - 「这次失败能不能重试」归 @@sideeffect.RetrySafety@@ 与 Runner 的四条
//     失败出口（Abandon / MarkUncertain + Release）；
//   - 「进度回退」归 @@checkpoint.Committer@@（SaveIfNewer + 进程内闸）；
//   - 「任务归谁」归 @@taskboard.Board@@（令牌 + 代次，Complete 校验令牌）。
//
// 本文件出现 if/else 的地方都是**处置分流**（这个既有结论该触发哪个
// 既有动作），不是第四份判据。
//
// # UNCERTAIN 在任务板上的落法
//
// taskboard 没有（也不该有）UNCERTAIN 状态：结果未知是 @@sideeffect@@ 的
// 一等结论，任务板只回答「谁负责这个子任务」。本执行器的处置是：
//
//   - **不 Complete**（没做成的不能记成做成了）；
//   - **释放任务板租约**（不占着一把谁都不敢动的锁）；
//   - 该副作用 Key 由 sideeffect 闸继续挡着——任何 worker 重新认领后，
//     Runner.Acquire 返回 OutcomeUncertain ⇒ 不执行、再次释放。于是
//     「任务可被重新认领」与「动作不会被重跑」同时成立，阻塞点在唯一
//     该在的地方（副作用闸），而不是在任务板上再长一个状态。
//
// 对账入口是 @@ManagedExecutor.Resolve@@（转发 Runner.Reconcile，不另造裁决）。
//
// # 与 worker.Loop 的取舍
//
//   - 任务动作**不可幂等**（或宿主不确定）→ 用本执行器。它保证失败/不确定
//     不会被「回板重做」变成重复施加；
//   - 动作可安全重做、或根本不对接外部副作用 → 用 @@worker.Loop@@ 更省
//     （它已经处理注册、心跳、续期、优雅退出）。
//
// 两者不要叠着用：本执行器自己 Claim、自己 Complete，再套 Loop 的 Handler
// 会对同一任务二次收口（第二次必然 ErrLeaseLost）。
//
// # 任务选择不在本执行器里
//
// Execute 收的是**一个任务 id**（调用方选定）。优先级 / 公平 / 配额等挑选
// 判据归 @@scheduling@@——本执行器不内嵌第二份，否则同一件事会有两个答案，
// 而漂移的形态是「调度说该做 A、执行器挑了 B」且两边都显示正常。
package worker

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// EffectRunner 受管执行器依赖的副作用编排面（*sideeffect.Runner 满足）。
//
// 收成接口而不是直接收 *sideeffect.Runner：执行器的用例要能精确构造
// 「Applied / Replayed / Contended / Uncertain」四种结论，而这两种结论的
// 真实构造需要完整的存储替身；接口让编排判据与存储实现解耦（生产构造
// 仍只接受 *sideeffect.Runner 的实现，见 NewManagedExecutor）。
type EffectRunner interface {
	// ExecuteWith 抢闸 → 准备 → 持有资格检查 → 施加 → 记账（语义见
	// sideeffect.Runner.ExecuteWith）。
	ExecuteWith(ctx context.Context, act sideeffect.Action, eff sideeffect.Effect) (sideeffect.Outcome, sideeffect.Result, error)
	// Reconcile 对账裁决结果未知的 Key（语义见 sideeffect.Runner.Reconcile）。
	Reconcile(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error)
}

// Progress 进度快照的提交面（*checkpoint.Committer 满足）。
//
// 只收 Commit 一个方法：单调判定（SaveIfNewer + 进程内闸）在 Committer /
// StoreCAS 里，本执行器不重写。
type Progress interface {
	Commit(ctx context.Context, s checkpoint.Snapshot) error
}

// ManagedConfig 执行器配置（零值不可用；NewManagedExecutor 做装配期校验）。
type ManagedConfig struct {
	// Agent 任务认领者标识（必填）。
	Agent taskboard.AgentID
	// ClaimTTL 认领租约有效期（必填，>0）。
	//
	// 必须覆盖「一次副作用 + 一次快照提交」的时长：本执行器不替宿主续期
	// （长动作的续期责任在副作用执行体，见 sideeffect.Runner 的注释）。
	ClaimTTL time.Duration
	// Progress 进度提交面（可选；nil = 本执行器不提交进度）。
	Progress Progress
}

// ManagedWork 一个受管任务要做的动作（由宿主按任务给出）。
type ManagedWork struct {
	// Action 副作用动作。Key / Resource 留空时由任务 id 派生
	// （key = "task:" + id，resource = id）——同一次任务的重试因此天然
	// 共用同一个幂等键；宿主需要更细的粒度时显式给。
	Action sideeffect.Action
	// Effect 执行体（Prepare / Apply；Apply 必填）。
	Effect sideeffect.Effect
	// Snapshot 成功后构造进度快照（可选；nil = 不记录进度）。
	//
	// 返回 false 表示「这次任务没有进度可记」（不是错误，也不阻塞 Complete）。
	// 配置了 Snapshot 但没有装配 Progress ⇒ 装配期之外的显式错误，不会
	// 静默跳过（静默跳过会让宿主以为进度落了库）。
	Snapshot func(task taskboard.Task, res sideeffect.Result) (checkpoint.Snapshot, bool, error)
}

// ManagedExecutor 受管任务执行器（可组合组件；无共享可变状态）。
type ManagedExecutor struct {
	board    taskboard.RenewingBoard
	effects  EffectRunner
	progress Progress
	agent    taskboard.AgentID
	claimTTL time.Duration
}

// NewManagedExecutor 装配执行器。
func NewManagedExecutor(board taskboard.RenewingBoard, effects EffectRunner, cfg ManagedConfig) (*ManagedExecutor, error) {
	switch {
	case board == nil:
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器缺任务板（Claim/Complete 无处落地）")
	case effects == nil:
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器缺副作用编排面（执行意图无处登记）")
	case cfg.Agent == "":
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器缺 Agent 标识")
	case cfg.ClaimTTL <= 0:
		return nil, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器的认领 TTL 必须为正")
	}
	return &ManagedExecutor{
		board:    board,
		effects:  effects,
		progress: cfg.Progress,
		agent:    cfg.Agent,
		claimTTL: cfg.ClaimTTL,
	}, nil
}

// Execute 受管执行一个任务（阻塞到本次收场）。
//
// 返回的 Outcome 是**副作用闸的结论**（Applied / Replayed / Contended /
// Uncertain）——调用方据此区分「我做完了」「别人做过了」「没轮上我」
// 「上次结果未知」。error 只表示本次调用没能完整走完编排（认领失败、
// 副作用失败、进度失败、收口被拒）；Contended 不带 error（正常判据）。
//
// 各结论的处置：
//
//   - Applied / Replayed → 快照提交（若装配）→ Complete；
//   - Contended → 释放任务板租约（让别的 worker 试），不 Complete；
//   - Uncertain → **不 Complete**、释放租约；副作用闸继续挡着重试；
//   - Complete 被 ErrLeaseLost 拒 → 如实返回（任务已归新持有者），
//     **不重试、不覆盖**——本次副作用已记账，重试会与真正持有者重复。
func (e *ManagedExecutor) Execute(ctx context.Context, id taskboard.TaskID, work ManagedWork) (sideeffect.Outcome, error) {
	if e == nil || e.board == nil || e.effects == nil {
		return sideeffect.OutcomeContended, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器未装配")
	}
	if work.Effect.Apply == nil {
		return sideeffect.OutcomeContended, errors.Wrap(ErrInvalidConfig, "worker: 受管任务缺施加阶段")
	}
	if work.Snapshot != nil && e.progress == nil {
		return sideeffect.OutcomeContended, errors.Wrap(ErrInvalidConfig,
			"worker: 受管任务要求记进度但执行器未装配 Progress（静默跳过会让宿主以为已落库）")
	}

	task, err := e.board.Claim(ctx, id, e.agent, taskboard.ClaimRequest{TTL: e.claimTTL})
	if err != nil {
		return sideeffect.OutcomeContended, errors.Wrap(err, "worker: 受管执行器认领失败", "task", string(id))
	}
	token := task.Lease.Token

	act := work.Action
	if act.Key == "" {
		act.Key = sideeffect.Key("task:" + string(id))
	}
	if act.Resource == "" {
		act.Resource = string(id)
	}

	outcome, res, effErr := e.effects.ExecuteWith(ctx, act, work.Effect)
	switch outcome {
	case sideeffect.OutcomeContended:
		// 没轮上我：归还任务板租约，让别的 worker 去试。副作用闸没有
		// 登记本次尝试，动作没做——释放是安全的。非 nil 的 err 是
		// 准备/施加失败的「可安全重试」出口（Runner 的契约），必须上抛，
		// 不能因为结论是 contended 就吞掉。
		return outcome, e.withRelease(ctx, id, token, effErr)
	case sideeffect.OutcomeUncertain:
		// 结果未知：不记账、不重跑。租约放回去，副作用闸负责挡住下一次执行。
		// 自定义 EffectRunner 若只给结论不给错误，补一条可判别的哨兵——
		// 「未知」必须能被 errors.Is 识别，不能只剩一个枚举值。
		if effErr == nil {
			effErr = errors.Wrap(sideeffect.ErrOutcomeUnknown,
				"worker: 副作用闸判定结果未知但未给出错误")
		}
		return outcome, e.withRelease(ctx, id, token, effErr)
	case sideeffect.OutcomeApplied, sideeffect.OutcomeReplayed:
		if effErr != nil {
			// 编排面保证这两个结论不带错误；带上说明实现违约，显式失败。
			return outcome, e.withRelease(ctx, id, token,
				errors.Wrap(effErr, "worker: 副作用闸返回了带错误的已执行结论"))
		}
	default:
		return outcome, e.withRelease(ctx, id, token,
			errors.Wrap(sideeffect.ErrUnknownOutcome, "worker: 副作用闸返回未知结论"))
	}

	if work.Snapshot != nil {
		snap, ok, serr := work.Snapshot(task, res)
		if serr != nil {
			// 副作用已记账，但进度没落：不 Complete（恢复点会缺这一步），
			// 释放租约让下一次重试——重试时 Acquire 返回 Replayed，
			// 执行体不会再施加一次。
			return outcome, e.withRelease(ctx, id, token,
				errors.Wrap(serr, "worker: 构造进度快照失败", "task", string(id)))
		}
		if ok {
			if cerr := e.progress.Commit(ctx, snap); cerr != nil {
				if errors.Is(cerr, checkpoint.ErrStaleSeq) {
					// 已有更高序号的快照：本次进度是旧的，Committer 已拒绝
					// 覆盖（不覆盖更高版本）。副作用与本任务仍然完成，
					// 继续收口；把「旧进度」当失败会让任务永远停在 claimed。
				} else {
					return outcome, e.withRelease(ctx, id, token,
						errors.Wrap(cerr, "worker: 提交进度快照失败", "task", string(id)))
				}
			}
		}
	}

	// 收口走 WithoutCancel：外层停机时已完成的动作仍要如实记成完成
	// （与 worker.Loop 的终态写入同一条纪律）。
	if cerr := e.board.Complete(context.WithoutCancel(ctx), id, token); cerr != nil {
		if errors.Is(cerr, taskboard.ErrLeaseLost) {
			// 任务已归新持有者：本次结果作废，不重试、不覆盖。
			return outcome, errors.Wrap(cerr,
				"worker: 受管执行器收口被拒（任务已归新持有者，本次结果作废）",
				"task", string(id))
		}
		return outcome, errors.Wrap(cerr, "worker: 受管执行器收口失败", "task", string(id))
	}
	return outcome, nil
}

// Resolve 对账裁决「结果未知」的副作用 Key（转发 Runner.Reconcile）。
//
// 它不碰任务板：裁决的对象是**副作用的事实**（到底做没做），不是任务归属。
// 裁决为已生效后，下一次 Execute 拿到 OutcomeReplayed，跳过执行直接收口。
func (e *ManagedExecutor) Resolve(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	if e == nil || e.effects == nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器未装配")
	}
	return e.effects.Reconcile(ctx, act, res)
}

// release 归还任务板租约（收场走 WithoutCancel）。
//
// ErrLeaseLost 不是故障：租约已易主，没有可归还的东西（也不该放掉
// 新持有者的闸）。其余错误如实上抛。
func (e *ManagedExecutor) release(ctx context.Context, id taskboard.TaskID, token string) error {
	err := e.board.Release(context.WithoutCancel(ctx), id, token)
	if err == nil || errors.Is(err, taskboard.ErrLeaseLost) {
		return nil
	}
	return errors.Wrap(err, "worker: 受管执行器释放任务租约失败", "task", string(id))
}

// withRelease 把「释放失败」与主错误合成一条链（两者都要保留：调用方既要
// 判主因，也要知道租约没还回去）。
func (e *ManagedExecutor) withRelease(ctx context.Context, id taskboard.TaskID, token string, primary error) error {
	rerr := e.release(ctx, id, token)
	if primary == nil {
		return rerr
	}
	if rerr == nil {
		return primary
	}
	return errors.Append(primary, rerr)
}

// 编译期断言：*sideeffect.Runner 满足执行器依赖的编排面。
var _ EffectRunner = (*sideeffect.Runner)(nil)

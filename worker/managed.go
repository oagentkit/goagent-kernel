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
//  1. **Claim**：拿租约与令牌（令牌由任务板铸造——任务归属的唯一凭据），
//     认领即开**租约会话**（@@LeaseSession@@：执行期间续期、关键步之前
//     确认所有权、易主封口在途执行）；
//  2. **SideEffect.Execute**：走 @@sideeffect.Runner@@ 的既有契约——
//     Acquire 原子登记执行意图 + 栅栏令牌，施加前做持有资格检查，
//     失败按 @@RetrySafety@@ 分流出「可重试」与「结果未知」；
//  3. **Checkpoint**：副作用记账成功后单调提交进度快照（不覆盖更高版本）；
//     进度面支持栅栏判据时带接管代次条件提交（存储侧拒绝陈旧代次的
//     迟到写入，见 @@LeasedProgress@@ / checkpoint.FencedStoreCAS）；
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

// LeasedProgress 带**持有凭据**的进度提交面（*checkpoint.Committer 满足，
// 经 CommitFenced）。
//
// 进度实现了它时，执行器把本次认领的接管代次（@@LeaseSession.Fence@@）
// 一并交给存储：陈旧代次的迟到提交由**存储侧**拒绝（check-then-act 的
// 窗口靠这个关，调用前的资格确认关不住它，见 checkpoint.FencedStoreCAS）。
// 未实现时退回普通 Commit——资格确认仍在（提交前 AssertOwned），但
// 「确认通过之后、写入落地之前」的窗口只剩 TTL 量级的兜底，宿主要严格
// 一致的状态提交必须装配支持栅栏判据的存储。
type LeasedProgress interface {
	// CommitFenced 带接管代次的条件提交（语义见 checkpoint.FencedStoreCAS）。
	CommitFenced(ctx context.Context, s checkpoint.Snapshot, fence uint64) error
}

// ManagedConfig 执行器配置（零值不可用；NewManagedExecutor 做装配期校验）。
type ManagedConfig struct {
	// Agent 任务认领者标识（必填）。
	Agent taskboard.AgentID
	// ClaimTTL 认领租约有效期（必填，>0）。
	//
	// # ClaimTTL 覆盖什么、不覆盖什么（issue #265）
	//
	// **覆盖**：两次续期之间的资格容忍窗。本执行器为每次 Execute 开
	// 租约会话（见 @@LeaseSession@@），执行期间按 RenewInterval 节拍续期
	// ——TTL 不再是「整个任务必须做完」的截止线，长任务不会只因超时
	// 就被接管。
	//
	// **不覆盖**：
	//   - **续期断档**：节拍短于 TTL 是资格的前提（节拍 ≥ TTL 会被装配
	//     期拒绝）。存储抖动或进程暂停跨满 TTL ⇒ 任务可被接管；接管后
	//     旧持有者的迟到提交由存储端条件提交（@@LeasedProgress@@）与
	//     Complete 的令牌校验拒绝——TTL 是资格的容忍窗，不是免死金牌；
	//   - **副作用闸的租约**（sideeffect.Runner 的 TTL 另配）与**下游
	//     栅栏**（sideeffect.FencedSink）——两份独立判据面，本执行器的
	//     续期不替它们续；
	//   - **进度归属**：租约只答「任务归谁」。谁有权写这个 run 的恢复点，
	//     由进度存储的代次判定答（checkpoint.FencedStoreCAS）。
	ClaimTTL time.Duration
	// RenewInterval 租约续期节拍（可选；零值 = ClaimTTL/3）。
	//
	// 必须短于 ClaimTTL（装配期拒绝）。调度策略的正式化（按到期时刻动态
	// 安排、抖动、退避）是 #266 的车道；本字段是换装点。
	RenewInterval time.Duration
	// Progress 进度提交面（可选；nil = 本执行器不提交进度）。
	//
	// 实现 @@LeasedProgress@@（如 *checkpoint.Committer，其存储实现
	// checkpoint.FencedStoreCAS 时）即获得存储端的迟到提交拒绝；
	// 只有 Commit 时提交前仍有资格确认，但 check-then-act 窗口没有
	// 存储侧判据兜底。
	Progress Progress
}

// ManagedWork 一个受管任务要做的动作（由宿主按任务给出）。
type ManagedWork struct {
	// Action 副作用动作。Key / Resource 留空时由任务 id 派生（默认键的
	// 作用域口径见 Scope）：零作用域时 key = "task:" + id、resource = id
	// （旧口径逐字不变）；有作用域时按 @@sideeffect.ActionKeyBuilder@@
	// 全段转义拼接。同一次任务的重试因此天然共用同一个幂等键；宿主需要
	// 更细的粒度时显式给（拼装走 ActionKeyBuilder，不要各自拼字符串——
	// 拼法分散就是口径漂移的开始）。
	Action sideeffect.Action
	// Scope 动作身份的**作用域段**（可选；宿主词汇——运行 id、环境名、
	// 任务板未覆盖的命名空间维度……本仓不解释段的内容，见
	// sideeffect.ActionKeyBuilder 的词汇归属说明）。
	//
	// 默认键的作用域段自动并入两项：
	//   - 任务板的租户（@@Task.Tenant@@ 非空时）——与任务板的作用域口径
	//     对齐：coord 的任务板分租户，副作用闸不分就会跨租户互相重放
	//     （issue #261）；
	//   - 本字段给的段（按给出顺序拼接）。
	//
	// 稳定性：同一逻辑动作的所有重试必须给同一组段——Attempt / 重试
	// 计数 / 时间戳不得入段（入段即每次重试一个新动作，幂等闸形同虚设）。
	Scope []string
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
	board      taskboard.RenewingBoard
	effects    EffectRunner
	progress   Progress
	agent      taskboard.AgentID
	claimTTL   time.Duration
	renewEvery time.Duration
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
	case cfg.RenewInterval < 0 || (cfg.RenewInterval > 0 && cfg.RenewInterval >= cfg.ClaimTTL):
		return nil, errors.Wrap(ErrInvalidConfig,
			"worker: 受管执行器的续期节拍必须短于认领 TTL（节拍是资格容忍窗里的巡逻频率，不是补票）",
			"ttl", cfg.ClaimTTL.String(), "renewInterval", cfg.RenewInterval.String())
	}
	return &ManagedExecutor{
		board:      board,
		effects:    effects,
		progress:   cfg.Progress,
		agent:      cfg.Agent,
		claimTTL:   cfg.ClaimTTL,
		renewEvery: cfg.RenewInterval,
	}, nil
}

// Execute 受管执行一个任务（阻塞到本次收场）。
//
// 认领成功即开租约会话（@@LeaseSession@@）：执行期间按节拍续期，施加段
// 跑在会话 ctx 上（租约易主 ⇒ 在途取消），进度与收口前各做一次资格确认。
//
// 返回的 Outcome 是**副作用闸的结论**（Applied / Replayed / Contended /
// Uncertain）——调用方据此区分「我做完了」「别人做过了」「没轮上我」
// 「上次结果未知」。error 只表示本次调用没能完整走完编排（认领失败、
// 副作用失败、进度失败、资格被拒、收口被拒）；Contended 不带 error
// （正常判据）。
//
// 各结论的处置：
//
//   - Applied / Replayed → 资格确认 → 快照提交（若装配）→ 资格确认 → Complete；
//   - Contended → 释放任务板租约（让别的 worker 试），不 Complete；
//   - Uncertain → **不 Complete**、释放租约；副作用闸继续挡着重试；
//   - 资格确认或 Complete 被 ErrLeaseLost 拒 → 如实返回（任务已归新
//     持有者），**不重试、不覆盖**——本次副作用已记账，重试会与真正
//     持有者重复；进度提交被 ErrStaleFence 拒同理（本 run 已归新代）。
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

	// 租约会话：执行期间的持有资格有人管（续期 + 确认 + 易主封口）。
	// Close 只停续期协程，不归还租约——归还/收口按下面的处置分流。
	session, serr := NewLeaseSession(ctx, e.board, task, LeaseSessionConfig{TTL: e.claimTTL, RenewInterval: e.renewEvery})
	if serr != nil {
		return sideeffect.OutcomeContended, e.withRelease(ctx, id, token, serr)
	}
	defer func() { _ = session.Close(context.WithoutCancel(ctx)) }()

	act := work.Action
	if act.Key == "" || act.Resource == "" {
		// 默认身份的作用域段：任务板的租户（任务板分租户，副作用闸必须
		// 同口径，否则跨租户同名任务互相重放）+ 宿主给的作用域段（词汇
		// 归宿主，本仓只提供拼装机制）。拼装走
		// sideeffect.NewActionKeyBuilderFor 这一条入口（issue #275）：
		// 租户排第一段、Scope 随后，拼法是纯函数——重试同一逻辑动作恒得
		// 同一个键（Attempt 不进键）。
		b := sideeffect.NewActionKeyBuilderFor(task.Tenant, work.Scope...)
		if act.Key == "" {
			act.Key = b.Key("task:" + string(id))
		}
		if act.Resource == "" {
			act.Resource = b.Resource(string(id))
		}
		// 身份字段随默认键一并落到 Action：持久化面按 (Tenant, Key)
		// 判定，不靠解析键字符串猜租户。
		if act.Tenant == "" {
			act.Tenant = task.Tenant
		}
		if len(act.Scope) == 0 {
			act.Scope = append([]string(nil), work.Scope...)
		}
	}

	// 施加段跑在会话 ctx 上：租约易主时在途执行被取消（施加之前封口，
	// 与 worker.Loop 的在途取消同一纪律）。
	outcome, res, effErr := e.effects.ExecuteWith(session.Context(), act, work.Effect)
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
		// 提交前确认所有权：不是心跳，是资格判据。确认失败就不提交——
		// 存储端条件提交（LeasedProgress）也会拒，但调用前的确认把迟到
		// 写入挡在更早的地方（不惊动存储）。
		if aerr := session.AssertOwned(ctx); aerr != nil {
			if errors.Is(aerr, taskboard.ErrLeaseLost) {
				return outcome, e.withRelease(ctx, id, token,
					errors.Wrap(aerr, "worker: 进度提交前确认所有权被拒（任务已归新持有者，本次结果作废）", "task", string(id)))
			}
			return outcome, e.withRelease(ctx, id, token,
				errors.Wrap(aerr, "worker: 进度提交前确认所有权失败（资格答不出来，不写状态）", "task", string(id)))
		}
		snap, ok, serr := work.Snapshot(task, res)
		if serr != nil {
			// 副作用已记账，但进度没落：不 Complete（恢复点会缺这一步），
			// 释放租约让下一次重试——重试时 Acquire 返回 Replayed，
			// 执行体不会再施加一次。
			return outcome, e.withRelease(ctx, id, token,
				errors.Wrap(serr, "worker: 构造进度快照失败", "task", string(id)))
		}
		if ok {
			var cerr error
			if lp, oklp := e.progress.(LeasedProgress); oklp {
				// 存储端条件提交：出示本次认领的接管代次。check-then-act
				// 的窗口（确认通过之后、写入落地之前租约易主）只有存储侧
				// 的代次判定关得住（checkpoint.FencedStoreCAS）。
				cerr = lp.CommitFenced(ctx, snap, session.Fence())
			} else {
				cerr = e.progress.Commit(ctx, snap)
			}
			if cerr != nil {
				switch {
				case errors.Is(cerr, checkpoint.ErrStaleSeq):
					// 已有更高序号的快照：本次进度是旧的，Committer 已拒绝
					// 覆盖（不覆盖更高版本）。副作用与本任务仍然完成，
					// 继续收口；把「旧进度」当失败会让任务永远停在 claimed。
				case errors.Is(cerr, checkpoint.ErrStaleFence):
					// 本 run 已归新持有者的代次：本次进度不落库，本次收场
					// 也必然被拒（令牌已易主）——按失格处置如实返回。
					return outcome, e.withRelease(ctx, id, token,
						errors.Wrap(cerr, "worker: 进度提交被拒（本 run 已归新持有者的代次，本次结果作废）", "task", string(id)))
				default:
					return outcome, e.withRelease(ctx, id, token,
						errors.Wrap(cerr, "worker: 提交进度快照失败", "task", string(id)))
				}
			}
		}
	}

	// 收口前确认所有权（收口与其资格确认同走 WithoutCancel：外层停机时
	// 已完成的动作仍要如实记成完成——cancelled ctx 上的 Renew 会立即失败，
	// 把「能收口」误判成「失格」；与 worker.Loop 的终态写入同一条纪律）。
	// 非失格的确认失败（存储抖动）不拦收口：Complete 本身凭令牌由存储侧
	// 判定，是最安全的收口形态——确认不到也轮不到进程内猜。
	if aerr := session.AssertOwned(context.WithoutCancel(ctx)); aerr != nil && errors.Is(aerr, taskboard.ErrLeaseLost) {
		return outcome, errors.Wrap(aerr,
			"worker: 受管执行器收口前确认所有权被拒（任务已归新持有者，本次结果作废）",
			"task", string(id))
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

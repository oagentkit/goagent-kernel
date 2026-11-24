// Package invocation —— 工具调用账本（Tool Invocation Journal）的跨进程契约。
//
// 生命周期: Experimental（v0.75.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 它补的是哪一段
//
// sideeffect 封的是「同一个逻辑动作**至多施加一次**」：判据是幂等键（Key）。
// 但它有一条前提——**调用方记得住那个键**。
//
// 这条前提在进程崩溃之后不成立：
//
//  1. 模型返回工具调用 call-A（编排侧铸造 callID）；
//  2. 副作用施加成功（sideeffect 闸已记账）；
//  3. 进程在「结果回填 + 快照提交」之前崩溃；
//  4. 恢复时历史里没有 call-A ⇒ 模型**重新生成**一次调用（call-B）；
//  5. call-B 是新的幂等键 ⇒ 闸认不出这是同一次动作 ⇒ 同一业务动作再施加一遍。
//
// ⇒ 缺的不是「幂等」，是**调用身份在进程死亡之后仍然存在**。本包就是那段机制：
// 在**执行外部工具之前**，把这次调用的不可变身份（callID + 工具名 + 参数）落到
// 持久账本上；恢复时按账本续跑，**不再问模型要那次调用**。
//
// # 与 sideeffect 的分工（两个机制，不是一套）
//
//	账本（invocation）    判「这次调用是谁、参数是什么、走到哪一步了」
//	副作用闸（sideeffect）判「这个逻辑动作是不是已经施加过」
//
// 两者合成才闭合崩溃窗口：账本保住**身份**（不重新生成 ⇒ 键不变），闸保住
// **至多一次**（键不变 ⇒ 重放取回既有结果，不重复施加）。
//
// 只有闸 ⇒ 键随模型重新生成而变 ⇒ 重复施加；只有账本 ⇒ 知道要做什么，但
// 不知道远端做没做 ⇒ 盲目重做。
//
// # 与 trajectory 的分工（两根轴，不是一根）
//
// trajectory 判的是「这一轮的流水对不对得上」（**配对轴**：调用有没有配上
// 结果、有没有重复结果）；本包判的是「这次调用走到哪一步了」（**执行轴**）。
// 两根轴都消费 callID，但结论不冲突：trajectory 说「这一轮的事件流自相矛盾」，
// 本包说「这次调用的进度」。同一处 callID 在两根轴上得到不同结论是**正常的**
// （事件流完整 ≠ 账目已回填）——但**同一根轴**上的两个结论冲突就是分叉，
// 必须有人负责。
//
// # 三条判据
//
//  1. **身份不可变**：一经落账，工具名与参数摘要不得改（改了 = 另一个动作）。
//  2. **状态机**：planned → approved → executing → applied → result_recorded，
//     另有 rejected（审批拒绝，副作用确定未施加）与 uncertain（结果未知）。
//  3. **未完成是合法形态**：executing 的条目是**合法的未完成调用**——恢复时
//     据此续跑或对账，**不是**「历史损坏」。
//
// # 两张判定词表（登记在 policy 的 word_tables 段）
//
// 状态词表（7 值）与恢复处置词表（4 值）都登记在 architecture-policy.yaml 的
// word_tables 段 ⇒ 消费方（宿主）必须**引用**本包的常量，而不是手抄字面量——
// 内核新增一个取值，宿主那份手抄清单不会跟着变，且没有任何判据会红。
//
// # 边界：内核给契约，宿主给存储
//
// 本包**不实现存储**。宿主实现 Store 接口（PostgreSQL / SQLite / etcd 皆可），
// 内核只保证：
//
//   - 判据是确定的（同一输入必得同一结论，不依赖进程内状态）；
//   - 状态转移只有一条路径（不存在「executing 与 applied 同时成立」的中间态）；
//   - 判据失败时的**失败形态是变红**（拒绝并报明确原因），不是静默放行。
//
// 宿主实现时的硬要求：**Advance 必须在存储侧原子地校验「当前状态允许该转移」**
// （一次 CAS / 一条带条件的 UPDATE），不能拆成「读 → 判 → 写」三步——那三步
// 之间正是本包要封的那个窗口（两个执行者同时推进同一条账目）。
//
// # 不做什么
//
// 本包不提供：重试策略（宿主定）、对账裁决（sideeffect 的面）、审批闸本身
// （宿主注入）、补偿/回滚（副作用不可逆时无回滚可言）。
package invocation

import "slices"

// State 账目状态（判定词表，本包自有命名）。
//
// 取值域封闭：读不懂的状态不是「还没初始化」，是**未知状态**——内核拒绝判读
// 它（宿主实现若遇到词表外取值，须显式报错而不是当默认值放行）。
type State string

const (
	// StatePlanned 身份已落账，尚未取得执行权（还没开始）。
	StatePlanned State = "planned"
	// StateApproved 已获批准（审批闸放行），可以执行。
	StateApproved State = "approved"
	// StateExecuting 已取得执行权，副作用正在施加。
	//
	// ★ 这是**崩溃窗口的合法形态**：进程死在这一步时，远端到底做没做**不知道**，
	//   但账目本身是合法的（不是历史损坏）⇒ 恢复时据它对账，而不是判失败。
	StateExecuting State = "executing"
	// StateApplied 副作用已施加且已记账（结果在 Entry.Result）。
	StateApplied State = "applied"
	// StateUncertain 结果未知：可能已生效也可能没有 ⇒ 须先对账裁决，禁止盲目重试。
	StateUncertain State = "uncertain"
	// StateRejected 审批拒绝：副作用**确定未施加**（终态）。
	StateRejected State = "rejected"
	// StateResultRecorded 结果已回填进会话历史（终态）。
	StateResultRecorded State = "result_recorded"
)

// transitions 状态词表与合法转移的**同一张表**（单一事实源）。
//
// 形态说明：
//
//   - 表的**键**就是词表全集（每个状态都有一行；终态那行是空表＝走不动了）；
//   - 表的**值**是从该状态允许转移到的下一批状态；
//   - planned → executing：无审批要求的工具直接进执行（有审批的先过 approved）；
//   - approved → rejected：审批可以在执行前撤回（拒绝 ⇒ 副作用确定未施加）；
//   - executing → uncertain：施加过程中失联（不知道远端做没做）；
//   - uncertain → applied：对账裁决「确实已生效」；
//   - uncertain → executing：对账裁决「未生效」后重做——**必须先有裁决**，
//     盲重试由 sideeffect 闸挡（未知态下 Acquire 转 uncertain，不授予执行权）。
//
// ★ 为什么词表与转移规则是**一张表**：分两张就要同步维护两张清单——
//
//	新增一个状态时忘了加进词表 ⇒ 那个取值的消费侧从此没人管（枚举出口里
//	没有它、宿主看不到它）；忘了加进转移规则 ⇒ 那个状态走不动却看着合法。
//	一张表 ⇒ 两件事同时发生或同时不发生。
var transitions = map[State][]State{
	StatePlanned:        {StateApproved, StateExecuting, StateRejected},
	StateApproved:       {StateExecuting, StateRejected},
	StateExecuting:      {StateApplied, StateUncertain},
	StateApplied:        {StateResultRecorded},
	StateUncertain:      {StateApplied, StateExecuting},
	StateRejected:       nil,
	StateResultRecorded: nil,
}

// AllStates 返回账目状态词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染状态选择器 / 校验落库值，不必手抄常量清单——手抄清单必然漂移：
// 内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红。
// 返回的是副本：调用方改写它不影响判定面。
func AllStates() []State {
	out := make([]State, 0, len(transitions))
	for s := range transitions {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// Known 判该状态是否在词表内。
//
// 读不懂的状态不是「还没初始化」，是**未知**：fail-closed（不判已知）。
func Known(s State) bool {
	_, ok := transitions[s]
	return ok
}

// CanTransition 判状态转移是否合法（纯函数）。
//
// 词表外的取值恒为 false——读不懂的状态不是「还没初始化」，是未知（fail-closed）。
func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Terminal 判该状态是否**终态**（走不动了）。
//
// 判据取自转移表本身：**没有出边**的状态即终态（rejected / result_recorded）。
// 词表外的取值恒判 false——读不懂的状态不许冒充终态（那是「放行」的形状）。
func Terminal(s State) bool {
	next, ok := transitions[s]
	if !ok {
		return false
	}
	return len(next) == 0
}

// Predecessors 返回能合法转移到 to 的**全部前驱状态**（排序后的新切片）。
//
// ★ 这条出口是给**存储实现**用的，不是给业务逻辑用的：宿主要让「推进」在
//
//	存储侧原子化，最省事且最不会走样的形态是「一条带条件的 UPDATE：
//	WHERE state IN (前驱集)」——与 sideeffect 的「一条带条件的 upsert 承担
//	比较与写入」同款（读-判-写不可分割）。
//
//	前驱集若由宿主自己反推转移表，就是**第二份真相**：内核改了转移表，宿主
//	那份反推不会跟着变，且没有任何判据会红（转移表是包私有，宿主**拿不到**
//	它 ⇒ 反推只能是重写一遍）。
//
// 词表外的目标状态恒返回空集（没有前驱）⇒ 存储侧的「条件 UPDATE」自然
// 匹配不到任何行 ⇒ 失败形态是「没有行被更新」（⇒ 宿主报
// ErrIllegalTransition），不会误放行。
func Predecessors(to State) []State {
	out := make([]State, 0, len(transitions))
	for from, nexts := range transitions {
		for _, s := range nexts {
			if s == to {
				out = append(out, from)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// RecoveryAction 恢复时对一条账目的处置（判定词表，本包自有命名）。
type RecoveryAction string

const (
	// ActionExecute 执行（还没开始，或已批准未执行）。
	ActionExecute RecoveryAction = "execute"
	// ActionReconcile 对账（执行中崩溃 / 结果未知 ⇒ 远端做没做不知道）。
	ActionReconcile RecoveryAction = "reconcile"
	// ActionRecord 回填结果（已施加且结果在账上，只差写回会话历史）。
	ActionRecord RecoveryAction = "record"
	// ActionNone 无需动作（终态）。
	ActionNone RecoveryAction = "none"
)

// recoveryActionAll 恢复处置词表（4 值）——判定与枚举的**同一张表**。
var recoveryActionAll = map[RecoveryAction]bool{
	ActionExecute:   true,
	ActionReconcile: true,
	ActionRecord:    true,
	ActionNone:      true,
}

// AllRecoveryActions 返回恢复处置词表的全集（排序后的**新切片**）。
func AllRecoveryActions() []RecoveryAction {
	out := make([]RecoveryAction, 0, len(recoveryActionAll))
	for a := range recoveryActionAll {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// KnownRecoveryAction 判该处置是否在词表内。
func KnownRecoveryAction(a RecoveryAction) bool {
	return recoveryActionAll[a]
}

// Recovery 给一条账目判定恢复处置（纯判据）。
//
// ★ 这条判据是本包存在的理由：它把「恢复时该怎么办」写成**可机判的结论**，
//
//	而不是让每个宿主各自推理一遍（各推理一遍 = 各判一套 = 漂移）。
func Recovery(e Entry) RecoveryAction {
	switch e.State {
	case StatePlanned, StateApproved:
		return ActionExecute
	case StateExecuting, StateUncertain:
		return ActionReconcile
	case StateApplied:
		return ActionRecord
	case StateRejected, StateResultRecorded:
		return ActionNone
	}
	// 词表外 ⇒ 显式对账（不猜）：读不懂的状态不许当「已完成」放行。
	return ActionReconcile
}

// Unfinished 判该状态是否**未完成**（恢复时要处理的）。
//
// ★ applied 也在此列：副作用已施加，但结果还没回填进会话历史 ⇒ 恢复时
//
//	要做的是「回填」，不是「重做」。把它当完成 ⇒ 历史缺结果 ⇒ 会话报废。
//
// 判据取自 Recovery：恢复时**还有动作**的状态就是未完成。
func Unfinished(s State) bool {
	return Recovery(Entry{State: s}) != ActionNone
}

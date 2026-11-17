// Package statemachine 环节状态机（PHASE1_PLAN §6.2）：合法转移表硬编码 + 全覆盖单测。
//
// 生命周期: Stable（v0.33.0 定级）——只允许 additive 变更（新增导出符号 /
// 新增方法且给零值默认实现 / 新增错误值）；改签名、改语义、改默认值、
// 删导出符号都算 breaking，判定规则与变更流程见 docs/api-compat.md，
// 逐包理由见 api-lifecycle.yaml。
//
// 所有状态迁移走条件 UPDATE（repo 层），影响行数=0 即竞态失败——standalone/cluster 同一写法。
package statemachine

import (
	"fmt"
	"strings"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
)

// StepStatus 环节状态。
type StepStatus string

// 环节状态常量：**全部是 contract 同值常量的别名**（issue #354/#357）。
//
// 状态词表只有一个定义处（contract）；本包不再写第二份字面量。无类型别名
// 保持批次二十八的性质：同一取值既可作 StepStatus 用（转移表/判定），也可作
// string 直传 repo.TransitionStep 等以字符串参数调用的位点。
//
// 两包共有的 6 个取值（created/running/waiting_approval/succeeded/failed/
// cancelled）与会话词表同源；环节独有的 6 个取值也由 contract 单点定义，
// 使终态判据只需一份词表就能覆盖两根轴。
//
// Deprecated: 状态取值的词表主人是 contract（便于两包共用同一份终态判据）。
// 这些常量保留为别名仅为兼容既有调用方；新代码请直接使用 contract.StatusX。
const (
	StatusCreated         = contract.StatusCreated
	StatusRunning         = contract.StatusRunning
	StatusWaitingApproval = contract.StatusWaitingApproval
	StatusApproved        = contract.StatusApproved
	StatusRejected        = contract.StatusRejected
	StatusExpired         = contract.StatusExpired
	StatusSucceeded       = contract.StatusSucceeded
	StatusFailed          = contract.StatusFailed
	// StatusInterrupted 中断占位态（spec 2026-09-21-conversation-uplift P1-3）：环节因
	// 用户取消/会话超时/进程中断而中止——区别于失败（执行了但失败）。占位报告写明
	// 「被中断、无产出或不完整」，下一轮对话历史如实携带，LLM 自行决定重做或跳过。
	StatusInterrupted = contract.StatusInterrupted
	StatusRolledBack  = contract.StatusRolledBack
	StatusSkipped     = contract.StatusSkipped
	StatusCancelled   = contract.StatusCancelled
)

// StepType 环节类别；plan/execute 必经审批态，readonly 裁剪审批（AGENTS 治理闸门）。
type StepType string

const (
	TypeReadonly StepType = "readonly"
	TypePlan     StepType = "plan"
	TypeApproval StepType = "approval"
	TypeExecute  StepType = "execute"
	TypeVerify   StepType = "verify"
)

// normalized 写法归一：状态值可能来自库 / 配置 / 外部来源，大小写与空白
// 是常见漂移。与 StepType.normalized 同形——同包两个枚举必须用同一套
// 「写法差异算不算同一个值」的约定（issue #373）。
func (s StepStatus) normalized() StepStatus {
	return StepStatus(strings.ToLower(strings.TrimSpace(string(s))))
}

// Valid 报告状态是否在本状态机词表内（归一化后判定；词表 = 全部状态常量）。
//
// 词表外的值代表「内核读不懂这个状态」，与「状态合法但迁移非法」是两种
// 不同的现场：前者排障应查数据来源/版本，后者查迁移代码。MustTransition
// 据此分成两条可判定的错误（issue #325）。
//
// 大小写/空白变体与规范形态同结论（issue #373）：它们是同一个状态，不是
// 「读不懂」——误诊会把宿主导向错误的数据来源排查。
func (s StepStatus) Valid() bool {
	switch s.normalized() {
	case StatusCreated, StatusRunning, StatusWaitingApproval, StatusApproved,
		StatusRejected, StatusExpired, StatusSucceeded, StatusFailed,
		StatusInterrupted, StatusRolledBack, StatusSkipped, StatusCancelled:
		return true
	default:
		return false
	}
}

// transitions **自行推进**的合法转移表：无出边 = 终态（issue #374）。
//
// **终态不在这张表里另列清单**：IsTerminal 直接读本表派生，给某个状态加/删
// 出边时终态结论自动跟随。
//
// failed 不在本表里列出边：failed → rolled_back 是**补偿迁移**（由裁决面/
// 恢复面显式驱动，不是状态机自行前进），登记在 compensations。故 failed
// 仍是终态，且与 contract.IsTerminalStatus 的结论一致（issue #354）。
var transitions = map[StepStatus][]StepStatus{
	StatusCreated:         {StatusRunning, StatusSkipped},
	StatusRunning:         {StatusSucceeded, StatusFailed, StatusInterrupted, StatusWaitingApproval},
	StatusWaitingApproval: {StatusApproved, StatusRejected, StatusExpired, StatusCancelled},
	StatusApproved:        {StatusRunning},
	// rejected/expired/succeeded/failed/interrupted/skipped/rolled_back/cancelled 无自行出边（终态）
}

// compensations **显式补偿**迁移：只允许从终态出发，且不改变「终态不自行
// 前进」的判定。目前唯一一条是失败环节回滚（failed → rolled_back）。
var compensations = map[StepStatus][]StepStatus{
	StatusFailed: {StatusRolledBack},
}

// CanTransition 判定 from→to 是否合法（两边先归一：写法变体与规范形态
// 走同一张转移表，issue #373）。
//
// 两段判定：① 自行推进走 transitions；② 补偿迁移只允许从**终态**出发，
// 且必须显式登记在 compensations（IsTerminal 的生产调用点——终态定义若漂了，
// 补偿迁移的准入会立刻跟着变）。
func CanTransition(from, to StepStatus) bool {
	f, t := from.normalized(), to.normalized()
	for _, next := range transitions[f] {
		if next == t {
			return true
		}
	}
	if !IsTerminal(f) {
		return false
	}
	for _, next := range compensations[f] {
		if next == t {
			return true
		}
	}
	return false
}

// 转移错误哨兵（宿主按 errors.Is 分流排障方向）。
var (
	// ErrIllegalTransition 两个状态都在词表内，但转移不在合法转移表里。
	ErrIllegalTransition = errors.New("statemachine: 非法状态转移")
	// ErrUnknownStatus 状态值不在本状态机词表内（读不懂，不是迁移写错）。
	//
	// 与 ErrIllegalTransition 分开：旧实现把两者压成同一条「非法状态转移」，
	// 宿主据此去查迁移代码，而真因往往是写库口径错/版本不一致/数据被截断。
	// 同仓 checkpoint.classifyStatus 对同一问题已做三态（open/terminal/unknown），
	// 本包口径向它对齐。
	ErrUnknownStatus = errors.New("statemachine: 状态不在词表内")
)

// MustTransition 非法转移返回错误（repo 条件 UPDATE 前的快速失败；DB 侧仍有条件守卫兜底）。
//
// # 错误分三类（issue #325）
//
//   - 正常迁移 → nil；
//   - from / to 有任一词表外 → 包装 ErrUnknownStatus，文案说明「不认识这个
//     状态」，不含「非法状态转移」；
//   - 两者都在词表内但不在转移表 → 包装 ErrIllegalTransition。
//
// 判定方向不变（词表外仍然拒绝），变的只是诊断分类：把「读不懂」说成
// 「写错了」会让宿主照着错方向查。
func MustTransition(from, to StepStatus) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("%w: %s → %s", ErrUnknownStatus, from, to)
	}
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, from, to)
}

// normalized 写法归一：类别取值来自计划文件/配置（字符串），大小写与空白
// 是常见漂移，不应改变治理语义。
func (t StepType) normalized() StepType {
	return StepType(strings.ToLower(strings.TrimSpace(string(t))))
}

// Valid 报告该类别是否在内核词表内（归一化后判定）。
//
// 为什么必须有：RequiresApproval 对词表外取值走保守侧（要求审批），宿主
// 还需要一个手段区分「这类别本就免审」与「内核不认识它」。此前 StepType
// 没有 Valid()，宿主只能看到 false，连「词表里没有它」都查不到。
// 口径向 backend.Isolation.Valid() 对齐：越界值不得被当成最弱档。
func (t StepType) Valid() bool {
	switch t.normalized() {
	case TypeReadonly, TypePlan, TypeApproval, TypeExecute, TypeVerify:
		return true
	default:
		return false
	}
}

// RequiresApproval 该环节类别是否必须经过审批态。
//
// # 口径（issue #322）
//
//   - 规范类别按治理语义判定：plan / execute 必经审批，readonly / approval /
//     verify 免审；
//   - 大小写与空白先归一（"PLAN" / "Execute" / " plan " 与规范形态同结论）——
//     旧实现用精确相等，模型或配置里的大小写变体会静默落进「免审」；
//   - **词表外一律返回 true**：这是审批类判定面，沉默必须落在最严一侧。
//     旧实现在这里返回 false，一条 "remediate" / "" / 拼写错误就能把治理闸
//     静默拆掉，而宿主没有任何信号（同包 CanTransition 对词表外是 fail-closed，
//     两处口径本来就该一致）。
func RequiresApproval(t StepType) bool {
	switch t.normalized() {
	case TypePlan, TypeExecute:
		return true
	case TypeReadonly, TypeApproval, TypeVerify:
		return false
	default:
		return true // 内核不认识这个类别：保守要求审批，不得静默放行
	}
}

// IsTerminal 是否终态（环节侧的类型化入口）。
//
// # 判据：转移表派生（issue #374）
//
// 本函数是**本状态机**的终态判据，直接读 transitions：无**自行推进**出边 =
// 终态。failed 的出边（→ rolled_back）是补偿迁移，登记在 compensations，
// 不在 transitions 里，所以 failed 仍是终态——与 contract.IsTerminalStatus
// 的结论一致（issue #354；两包对 12 个环节状态逐条一致由用例钉住）。
//
// 写法变体先归一（与 Valid / CanTransition 同口径）。词表外的值不是终态：
// 「读不懂」不能被当成「不可再变」（timed_out 等会话独有状态在环节词表外，
// 本函数返回 false；会话侧判据 contract.IsTerminalStatus 仍认它）。
//
// 反向验证（#374 常驻用例）：给某个终态临时加一条 transitions 出边，本函数
// 必须立即跟随；#354 的跨包用例则保证此时 contract 侧也会红——两处必须同改。
func IsTerminal(s StepStatus) bool {
	n := s.normalized()
	if !n.Valid() {
		return false
	}
	return len(transitions[n]) == 0
}

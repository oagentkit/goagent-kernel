// Package statemachine 环节状态机（PHASE1_PLAN §6.2）：合法转移表硬编码 + 全覆盖单测。
//
// 生命周期: Stable（v0.33.0 定级）——只允许 additive 变更（新增导出符号 /
// 新增方法且给零值默认实现 / 新增错误值）；改签名、改语义、改默认值、
// 删导出符号都算 breaking，判定规则与变更流程见 docs/api-compat.md，
// 逐包理由见 api-lifecycle.yaml。
//
// 所有状态迁移走条件 UPDATE（repo 层），影响行数=0 即竞态失败——standalone/cluster 同一写法。
//
// # 词表与枚举出口
//
// 环节状态词表（12 值）与环节类别词表（5 值）在 enum.go（**单一事实源**）：
// AllStepStatuses / AllStepTypes 给出全集，Valid 给出判定面。
//
// 与 contract 同名的 6 个取值是 contract 的**别名**（issue #357），本包
// 独有的 6 个取值**不**与它合并——会话终态与环节终态是**两个概念**
// （见 IsStepTerminal 的注释）。
package statemachine

import (
	"fmt"
	"strings"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
)

// StepStatus 环节状态。
type StepStatus string

// 无类型常量（批次二十八）：同一取值既可作 StepStatus 用（转移表/判定），也可作
// string 直传 repo.TransitionStep 等以字符串参数调用的位点——单一事实源不设两份。
//
// # 与 contract 的重叠取值是**别名**（issue #357）
//
// 与 contract 同名的 6 个取值（created / running / waiting_approval /
// succeeded / failed / cancelled）此前**各写一份字面量**——两张部分重叠
// 的表看起来像同一套词表，实际各有各的边界，且零跨包断言：改掉其中一份
// 不会有任何测试变红。
//
// 现按 #333 已验证的「一处定义 + 另一处别名」样板处理：**statemachine 别名
// contract**（contract 不依赖 statemachine，无环）。两侧都是无类型 string
// 常量，故 const X = contract.X 合法且**保持无类型**——「同一取值既可作
// StepStatus 用也可作 string 直传」的承诺不破。
//
// 本包**独有**的取值（approved / rejected / expired / interrupted /
// rolled_back / skipped）与 contract 独有的 timed_out **不合并**：会话终态
// 与环节终态是两个概念，各有各的词表（见 IsStepTerminal 的注释），合成
// 一张大表会把两个语义不同的概念混成一个词表。
const (
	// 与 contract 同名的 6 个取值是**别名**（issue #357）：见上方说明。
	StatusCreated         = contract.StatusCreated
	StatusRunning         = contract.StatusRunning
	StatusWaitingApproval = contract.StatusWaitingApproval
	StatusSucceeded       = contract.StatusSucceeded
	StatusFailed          = contract.StatusFailed
	StatusCancelled       = contract.StatusCancelled
	// 以下是本包**独有**的环节取值（会话侧没有对应项）。
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusExpired  = "expired"
	// StatusInterrupted 中断占位态（spec 2026-09-21-conversation-uplift P1-3）：环节因
	// 用户取消/会话超时/进程中断而中止——区别于失败（执行了但失败）。占位报告写明
	// 「被中断、无产出或不完整」，下一轮对话历史如实携带，LLM 自行决定重做或跳过。
	StatusInterrupted = "interrupted"
	StatusRolledBack  = "rolled_back"
	StatusSkipped     = "skipped"
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

// Valid 报告状态是否在本状态机词表内（**归一化后**判定）。
//
// 词表外的值代表「内核读不懂这个状态」，与「状态合法但迁移非法」是两种
// 不同的现场：前者排障应查数据来源/版本，后者查迁移代码。MustTransition
// 据此分成两条可判定的错误（issue #325）。
//
// # 口径与 StepType.Valid 对齐（issue #373）
//
// 同一个词的**写法差异**（大写 / 首尾空白）不该改变它在词表内的结论。
// 此前本函数精确查表，于是 "RUNNING" 被判成「读不懂」——而它就是 running
// 的写法变体；宿主照着「读不懂 ⇒ 查数据来源/版本」的方向去查，方向就错了
// （把「读不懂」说成「写错了」会让宿主照错方向查，反过来同样成立）。
func (s StepStatus) Valid() bool {
	return stepStatusAll[s.normalized()]
}

// transitions 合法转移表。
var transitions = map[StepStatus][]StepStatus{
	StatusCreated:         {StatusRunning, StatusSkipped},
	StatusRunning:         {StatusSucceeded, StatusFailed, StatusInterrupted, StatusWaitingApproval},
	StatusWaitingApproval: {StatusApproved, StatusRejected, StatusExpired, StatusCancelled},
	StatusApproved:        {StatusRunning},
	StatusFailed:          {StatusRolledBack},
	// rejected/expired/succeeded/interrupted/skipped/rolled_back/cancelled 为终态
}

// CanTransition 判定 from→to 是否合法。
func CanTransition(from, to StepStatus) bool {
	for _, next := range transitions[from] {
		if next == to {
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
	//
	// # 大小写 / 空白变体**不算**词表外（issue #373）
	//
	// 判定面按**归一后**的形态查表（Valid 与 MustTransition 都先归一），所以
	// "RUNNING" / " running " 归一到 running，**不报本哨兵**——它们是同一个
	// 词的写法差异，不是「读不懂」。只有**归一后仍不在词表内**的值才报
	// ErrUnknownStatus。
	//
	// 误诊方向相反同样有害：把「写错了（查迁移代码）」说成「读不懂（查数据
	// 来源/版本）」会让宿主照着错方向查——所以归一必须发生在判定**之前**，
	// 而不是靠报错文案去解释。
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
	// 先归一再判定：写法变体不该改变迁移结论，也不该被误报成「读不懂」
	// （issue #373）。归一后的形态才是转移表里的键。
	fn, tn := from.normalized(), to.normalized()
	if !fn.Valid() || !tn.Valid() {
		return fmt.Errorf("%w: %s → %s", ErrUnknownStatus, from, to)
	}
	if CanTransition(fn, tn) {
		return nil
	}
	return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, from, to)
}

// normalized 写法归一：类别取值来自计划文件/配置（字符串），大小写与空白
// 是常见漂移，不应改变治理语义。
func (t StepType) normalized() StepType {
	return StepType(strings.ToLower(strings.TrimSpace(string(t))))
}

// normalized 写法归一：环节状态常由外部来源读入（库 / 配置 / 模型输出），
// 大小写与空白是常见漂移，不应改变语义（与 StepType 同一口径，
// issue #373）。
func (s StepStatus) normalized() StepStatus {
	return StepStatus(strings.ToLower(strings.TrimSpace(string(s))))
}

// Valid 报告该类别是否在内核词表内（归一化后判定）。
//
// 为什么必须有：RequiresApproval 对词表外取值走保守侧（要求审批），宿主
// 还需要一个手段区分「这类别本就免审」与「内核不认识它」。此前 StepType
// 没有 Valid()，宿主只能看到 false，连「词表里没有它」都查不到。
// 口径向 backend.Isolation.Valid() 对齐：越界值不得被当成最弱档。
func (t StepType) Valid() bool {
	return stepTypeAll[t.normalized()]
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

// IsStepTerminal 报告该环节状态是否为**终态**（无出边 = 终态）。
//
// # 终态集**从转移表派生**（issue #374）
//
// 终态 = transitions 表里**没有出边**的状态。此前本函数硬编码 7 个终态，
// 而它的注释写的是「词表以转移表为准」——代码并不读那张表：给某个终态加
// 一条出边，两处结论相反且**没有任何判据会红**。现在终态集由转移表派生，
// 注释即成真：转移表改了，终态判定跟着改。
//
// 派生出来的事实：failed **不是**终态（它还有出边 → rolled_back，回滚是
// 失败后的正常去向）；rejected / expired / succeeded / interrupted /
// skipped / rolled_back / cancelled 无出边，故是终态。degraded 不在词表内。
//
// # 与 contract.IsSessionTerminal 是**两个概念**（issue #354）
//
// 本函数判的是**环节**终态（一个环节还能不能迁移）；contract 判的是**会话**
// 终态（一轮会话会不会再自行前进）。两张词表互不覆盖，且 failed 的归属
// **相反**：会话侧 failed 是终态，环节侧不是。
//
// ⇒ 不存在一个同时满足两边的词表，宿主**不要**把某一侧的取值灌进另一侧的
// 判定面。
func IsStepTerminal(s StepStatus) bool {
	n := s.normalized()
	if !stepStatusAll[n] {
		return false
	}
	return len(transitions[n]) == 0
}

// IsTerminal 报告该环节状态是否为终态。
//
// **已更名，见 IsStepTerminal**（issue #354）：本名在 #354 之前与
// contract.IsTerminalStatus 同名，而两者判的是两个不同概念、结论对 12 个
// 环节取值里有 6 个相反。保留本名只为兼容既有调用点，语义与
// IsStepTerminal **逐字相同**。
func IsTerminal(s StepStatus) bool {
	return IsStepTerminal(s)
}

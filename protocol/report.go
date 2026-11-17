package protocol

// 统一报告协议（PHASE1_PLAN §4.1）——类型即协议。
// Validate 的白名单语义与被替代的 kyaiops-filter.js 完全对齐：
// 执行类动作缺 requires_approval → 拦截；snippet 超长 → warn 放行（证据保真）；
// snippet 质量启发式判据与 check_consistency.py F 节同源。
// 本包只依赖标准库，保持引擎内零外部耦合。
//
// 按职责拆分：report_types.go 类型契约 · report_repair.go 宽容解析
// report_validate.go 白名单 · report_render.go Markdown 渲染。

import (
	"strings"

	"github.com/oekit/pkg/errors"
)

// SnippetMax evidence.snippet 长度上限；超长仅 warn 不截断（证据保真）。
const SnippetMax = 200

// recommendation.action 白名单（粗粒度枚举，hook/校验用）。
const (
	ActionKillAndRestart = "kill_and_restart"
	ActionTune           = "tune"
	ActionNone           = "none"
)

// 编目声明风险档的合法区间（curated_risk）。此前只写在字段注释里而不校验，
// 99 或 -1 会原样放行，宿主拿它当「引擎基线下限」时得不到任何信号。
// 0 不参与判定：字段带 omitempty，0 与「未声明」不可区分。
const (
	RiskTierMin = 1
	RiskTierMax = 4
)

// 决策与置信枚举。
const (
	DecisionPending  = "pending_approval"
	DecisionApproved = "approved"
	DecisionRejected = "rejected"

	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// scope 取值（判定词表：scope）。
//
// scope 是**路由信号**（out = 转出/转派）：模型写 "OUT" / "Out" / " out " 说的是
// 同一件事，解析面归一、谓词面认写法变体（issue #371）。
//
// 此前本包九张判定词表里**只有 scope 没有导出常量**——宿主只能手抄 "in"/"out"，
// 内核改一次词值，宿主编译期不报错、运行期静默失效（issue #382）。现在与其余
// 八张表口径一致：取值一律有常量，词表本体（scopeAll）也引用它。
const (
	// ScopeIn 在范围内。
	ScopeIn = "in"
	// ScopeOut 超出范围（转出/转派；此时 redirect 必填）。
	ScopeOut = "out"
)

// 严重度枚举（批次一百三十七，终报结构化补缺）：info|low|medium|high|critical。
// 与 confidence 相互独立——confidence 说「结论可信到什么程度」，severity 说「问题本身
// 有多重」。专家可不填（空=未评估，渲染与聚合按缺席处理）；聚合面取子报告最高值
// （只升不降），dispute 钳制与 confidence 同钳 ≤medium。
const (
	SeverityInfo     = "info"
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

var severityAll = map[string]bool{
	SeverityInfo: true, SeverityLow: true, SeverityMedium: true,
	SeverityHigh: true, SeverityCritical: true,
}

// severityRank 严重度序（越大越重；空串=0 未评估）。聚合取 max、dispute 钳制都用它。
//
// # 未知值与未评估必须分档（issue #324）
//
// 空串 = 未评估（rank 0）；非空但不在词表 = 读不懂（SeverityRankUnknown）。
// 两者同档会让「填了但看不懂」在聚合里静默消失，方向恰好是「看起来更轻」。
// 读不懂按高于 critical 处理：无法判断严重度时，聚合不得把它降级成不参与比较。
//
// 大小写与空白在读取时归一（SeverityRankOf 内部 ToLower+TrimSpace）：
// 「Critical」与「critical」是同一个词，不是两个严重度。
//
// # 为什么不做成导出的 map
//
// 导出的 map 可被任何导入方就地改写，而 `protocol.SeverityRank["critical"] = 0`
// 会让 MaxSeverity 把最高危静默降级成「未评估」——**无编译错误、无运行错误、
// 无日志**，只在若干天后表现为「报告的严重度排序不对」，极难回溯到这一行。
//
// 这不是假想的风险：netguard/policy.go 对拦截名单守住了同一条（"默认名单刻意
// 不做成可写变量"），而本包此前是**全仓唯一的例外**——一次全仓扫描显示，除哨兵
// error 外，本仓导出变量中只有 SeverityRank 一个是可变容器。
//
// 读取一律走 SeverityRankOf；需要遍历的内部代码直接读本表。
var severityRank = map[string]int{
	SeverityInfo: 1, SeverityLow: 2, SeverityMedium: 3, SeverityHigh: 4, SeverityCritical: 5,
}

// SeverityRankUnassessed 未评估（空串）的序。
const SeverityRankUnassessed = 0

// SeverityRankUnknown 非空但不在词表内的取值序（读不懂）。
//
// 高于任何已知档：聚合取 max 时，读不懂的取值不会被静默降级成「未评估」。
// 它不是 severity 的合法取值，不应出现在 AllSeverities() 里；校验面仍以
// warn 留痕（severity 是增强面，不因它拦整份报告）。
const SeverityRankUnknown = 6

// normalizeSeverityValue severity 的写法归一：ToLower + TrimSpace。
// 归一后命中词表才返回规范形态；仍词表外则保持原值（不臆断成某个档）。
//
// 实现走 canonicalSeverity（谓词面同一份口径），不再自写一遍 ToLower——
// 归一面与谓词面各写一遍必然漂移（issue #371）。
func normalizeSeverityValue(s string) string { return canonicalSeverity(s) }

// SeverityRankOf 严重度序取值。
//
//   - 空串（含纯空白）= SeverityRankUnassessed（未评估）；
//   - 词表内的取值（大小写/空白变体先归一）= 1..5；
//   - 非空但词表外 = SeverityRankUnknown（读不懂，按高于 critical 处理）。
//
// 大小写与空白不改变语义：「Critical」/「CRITICAL」/「 critical 」都是
// critical，不得落进「未评估」——那会让聚合方向看起来更轻。
func SeverityRankOf(s string) int {
	n := strings.ToLower(strings.TrimSpace(s))
	if n == "" {
		return SeverityRankUnassessed
	}
	if rank, ok := severityRank[n]; ok {
		return rank
	}
	return SeverityRankUnknown
}

// IsSeverityValid 严重度白名单判定（空合法=未评估；大小写/空白变体先归一）。
//
// 归一走 canonicalEnumValue——与其余五个谓词同一份实现（issue #371）。
func IsSeverityValid(s string) bool {
	n := canonicalEnumValue(s)
	return n == "" || severityAll[n]
}

// MaxSeverity 取两者较重者。
//
// 已知取值返回规范小写形态；词表外取值按 SeverityRankUnknown 参与比较
// （读不懂不会被「未评估」吞掉，也不会被更轻的已知档盖过）。
func MaxSeverity(a, b string) string {
	if SeverityRankOf(b) > SeverityRankOf(a) {
		return normalizeSeverityValue(b)
	}
	return normalizeSeverityValue(a)
}

var (
	// ErrNotReport 输出不是统一报告 JSON（调用方降级读 markdown 主体，不阻塞）。
	ErrNotReport = errors.New("protocol: 输出不是统一报告 JSON")
	// ErrViolation schema 白名单违规（必须拦截，违规项经 errors.Join 附带）。
	ErrViolation = errors.New("protocol: 报告未通过 schema 白名单校验")
)

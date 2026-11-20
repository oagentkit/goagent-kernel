// Package retryplan 重试处置决策：把「该不该重试」与「重试会不会重复施加」
//
// 生命周期: Stable（v0.33.0 定级）——只允许 additive 变更（新增导出符号 /
// 新增方法且给零值默认实现 / 新增错误值）；改签名、改语义、改默认值、
// 删导出符号都算 breaking，判定规则与变更流程见 docs/api-compat.md，
// 逐包理由见 api-lifecycle.yaml。
//
// 两问合成**一条可执行**的结论。
//
// # 它补的是哪一段
//
// 上游 `guardx/toolfail` 判的是**处置层语义**：这次失败该不该重试、能不能
// 原样重发、要不要人工。它对「超时类」给出的 Guidance 是：
//
//	「可以重试，但先确认上一次调用是否已经生效，避免重复施加副作用。」
//
// 这句话**只说清了要做什么，没说清怎么做到**。「确认上一次是否已生效」
// 需要一个机制——而那个机制本仓已经有了：`sideeffect` 的幂等键
// （同一 Key 重放 → 取回既有结果，而不是重复施加）。
//
// 两者此前是**并列的独立机制**，接线的活儿全推给宿主。后果不是「少个功能」，
// 而是**每个宿主各写一遍组合逻辑**，而组合逻辑里恰好藏着一个必须全局一致的判定：
//
//	「这次重试，到底是原样重发，还是走幂等键重放？」
//
// 这与 `guardset` 面对的是同一类问题（守卫怎么装配），故本包与它同族：
// **组合逻辑归内核，判定归各自机制**。
//
// # 三问与一条结论
//
// 本包把三个问题合成一条决策：
//
//	① 这次失败值得重试吗？          （toolfail 判）
//	② 原样重发安全吗？              （toolfail 判）
//	③ 若不安全，重试该走什么路径？  （sideeffect 判）
//
// 结论是 `Decision`，它同时告诉宿主**做什么**与**为什么**。
//
// # 关键取舍一：「可重试但不可原样重发」必须落到具体路径
//
// 这是最容易漏的一档。`toolfail` 把它标成「值得重试但不确定能不能重发」，
// 若接线只把它翻译成「重试」（不带路径），宿主面对超时类失败时
// 仍会原样重发——于是「先确认是否已生效」这句提醒被丢掉了，
// 副作用落在这一步。
//
// 故 `Decision` 在这一档给出的是 **`PathReplayed`**：
// **重试必须走幂等键**，由 `sideeffect` 保证「至多一次」。
// 没有幂等键可用时（`Options.WithoutIdempotency`），决策降级为
// `PathNone`（不重试）——而不是退化成原样重发。
//
// # 关键取舍二：没有幂等键就不许「猜着重试」
//
// 「值得重试但不确定能不能重发」+「没有幂等键」= **不能重试**。
// 此时正确的处置是如实失败交上层降级/人工，而不是赌一次。
// 赌的代价是一次重复副作用——正是本包存在的理由要防的那件事。
//
// # 关键取舍三：失败类别的判定归上游，本包不重新分类
//
// 本包**不重新判定**失败是什么类别（那是 `toolfail` 的事，判定口径必须
// 跨宿主一致）。它只把既有判定**组合**成决策。
//
// 分类器可注入（`Options.Classifier`，issue #240）：缺省实现
// `DefaultClassifier` 就是 `toolfail` 的既有口径（行为零变化）；宿主自有
// 错误体系注入自己的 `Classifier` 即可，**组合判据不变**——注入换的是分类
// 输入，不是「无幂等键不赌重试」这类内核取舍。
//
// # 与 sideeffect 的关系
//
// 本包**不执行**重试，也不持有存储。它只产出「该走哪条路径」的决策；
// 执行是宿主的接缝：`PathReplayed` 对应 `sideeffect.Runner.Execute` 用
// **同一个幂等键**再跑一次。
//
// 本包对 `sideeffect` **零依赖**——它只说「该走幂等键路径」，
// 不关心幂等键怎么实现、存储怎么落。那是 `sideeffect` 的事。
// 两者是**契约上的衔接**，不是代码上的耦合。
package retryplan

import (
	"time"

	"github.com/oagentkit/guardx/toolfail"
)

// Path 重试路径。
type Path int

const (
	// PathNone 不重试。失败是终态，或重试没有意义/没有安全路径。
	PathNone Path = iota
	// PathDirect 原样重发。判定为「原样重发安全」（幂等或确认未受理）。
	PathDirect
	// PathReplayed 走幂等键重放。判定为「值得重试但原样重发不安全」，
	// 且幂等键可用——由幂等机制保证「至多一次」。
	PathReplayed
)

// pathNoneName 档位名「不重试」。
//
// **本包自起名字**（不是引用别家的常量）：这一串拼写与 roundguard 的额度类别
// "none"（未因额度叫停）只是同形，概念不同——本包说的是**重试路径**，不是额度
// 判定；字面量只允许出现在常量定义处。
const pathNoneName = "none"

func (p Path) String() string {
	switch p {
	case PathNone:
		return pathNoneName
	case PathDirect:
		return "direct"
	case PathReplayed:
		return "replayed"
	default:
		return "unknown"
	}
}

// Decision 一条重试处置决策。
type Decision struct {
	// Path 该走哪条路径（见 Path 的三个取值）。
	Path Path
	// Retryable 该失败本身是否值得重试（toolfail 的判定）。
	//
	// 与 Path 分开：PathNone + Retryable=true 是可能的
	// （值得重试，但没有安全的重试路径——见 §关键取舍二）。
	// 压成一个字段会把这两件事混成「不可重试」。
	Retryable bool
	// Backoff 是否建议退避后重试（限流类：立刻重试会放大拥塞）。
	Backoff bool
	// RetryAfter 服务端给的等待时长（0 = 无此信息）。
	RetryAfter time.Duration
	// NeedsOperator 是否需要人工介入（改凭证 / 放权限）。
	NeedsOperator bool
	// Reason 面向人的判定理由（宿主可原样进日志）。
	//
	// 与 `toolfail.Guidance()` 分开：Guidance 是给**模型**看的
	// （要可执行、不含现场描述），Reason 是给**人**看的
	// （要说清「为什么不能重试」这类结论）。
	Reason string
}

// Classification 一次失败的分类结论（重试决策的全部输入）。
//
// 这是内核与分类器之间的**唯一约定**：宿主自有分类器只需填这张表，
// 不必 import 或伪造 toolfail 的类别。Class 是机读名，只进 Decision.Reason
// （人读理由），**不参与判定**——判定只看四个布尔位与等待时长。
type Classification struct {
	// Class 失败类别名（机读；注入分类器可用自己的词面）。
	Class string
	// Retryable 这次失败值得重试吗。
	Retryable bool
	// SafeToResubmit 原样重发安全吗（不产生重复副作用）。
	//
	// 与 Retryable 正交：可重试但重发不安全是最危险的一档，必须走幂等键。
	SafeToResubmit bool
	// Backoff 是否建议退避后重试（限流类）。
	Backoff bool
	// RetryAfter 服务端建议的等待时长（0 = 无此信息）。
	RetryAfter time.Duration
	// NeedsOperator 是否需要人工介入（改凭证 / 放权限）。
	NeedsOperator bool
}

// Classifier 失败分类器：把一次失败映射成决策输入（issue #240）。
//
// 与 sideeffect.Action.RetrySafety 同层——都是**宿主对该次调用的表态**：
// 内核不重新判定失败性质，只把表态组合成决策。缺省实现是
// DefaultClassifier（现有 toolfail 口径），注入即替换分类，不绕过
// 「无幂等键不赌重试」的组合判据。
type Classifier func(err error) Classification

// DefaultClassifier 缺省分类器：toolfail.Classify + PolicyOf + 逐次幂等表态。
//
// 与 issue #240 之前的 Decide 内部逻辑逐字等价（含「宿主逐次 Idempotency
// 表态优先于类别缺省」这条顺序）；显式导出供注入分类器做兜底/组合。
func DefaultClassifier(err error) Classification {
	cls := toolfail.Classify(err)
	policy := toolfail.PolicyOf(cls)

	// 取逐次表态（宿主对该次调用的幂等性声明），它优先于类别缺省。
	safeToResubmit := policy.SafeToResubmit
	if f, ok := toolfail.As(err); ok {
		safeToResubmit = f.SafeToResubmit()
	}

	return Classification{
		Class:          string(cls),
		Retryable:      policy.Retryable,
		SafeToResubmit: safeToResubmit,
		Backoff:        policy.Backoff,
		RetryAfter:     retryAfterOf(err),
		NeedsOperator:  policy.NeedsOperator,
	}
}

// Options 决策的输入条件。
type Options struct {
	// WithoutIdempotency 声明本次调用**没有**幂等键可用。
	//
	// 缺省（false）表示有幂等键可用——此时「可重试但不可原样重发」
	// 会被判为 PathReplayed。
	//
	// 置 true 时，该档降级为 PathNone：值得重试，但没有安全的重试路径
	// （见 §关键取舍二：宁可如实失败，不赌一次重复副作用）。
	WithoutIdempotency bool

	// Classifier 失败分类器注入（issue #240；nil = DefaultClassifier）。
	//
	// 零值保持既有行为：不注入时分类完全走 toolfail 的既有口径。
	// 注入后分类由宿主负责，本包只做组合判定——「无幂等键不赌重试」
	// 等组合纪律仍然生效（见 TestInjectedClassifierDoesNotBypassIdempotencyGate）。
	Classifier Classifier
}

// Decide 依据一次失败与当前条件，产出重试处置决策。
//
// err 为 nil 时返回 PathNone（没有失败可处置）。
//
// 分类由 Options.Classifier 注入；nil 时走 DefaultClassifier（toolfail 的
// 既有口径，行为与注入接缝引入前逐字一致）。判定只消费 Classification 的
// 四维与等待时长，不重新分类失败。
func Decide(err error, opts Options) Decision {
	if err == nil {
		return Decision{Path: PathNone, Reason: "无失败，无需处置"}
	}

	classifier := opts.Classifier
	if classifier == nil {
		classifier = DefaultClassifier
	}
	cls := classifier(err)

	d := Decision{
		Retryable:     cls.Retryable,
		Backoff:       cls.Backoff,
		RetryAfter:    cls.RetryAfter,
		NeedsOperator: cls.NeedsOperator,
	}

	switch {
	case !cls.Retryable:
		// 不可重试：终态，换处置路径（改参数 / 找人工 / 收口）。
		d.Path = PathNone
		d.Reason = "该失败不可重试：" + cls.Class
	case cls.SafeToResubmit:
		// 可重试且原样重发安全：直接重发。
		d.Path = PathDirect
		d.Reason = "可重试，且原样重发安全"
	case opts.WithoutIdempotency:
		// 可重试但原样重发不安全，且没有幂等键兜底 → 不能重试。
		// 宁可如实失败交上层降级/人工，不赌一次重复副作用。
		d.Path = PathNone
		d.Reason = "值得重试，但原样重发不安全且无幂等键兜底，不能重试"
	default:
		// 可重试但原样重发不安全，且幂等键可用 → 走幂等键重放。
		d.Path = PathReplayed
		d.Reason = "值得重试，但原样重发不安全；须走幂等键重放以保证至多一次"
	}

	return d
}

// retryAfterOf 取服务端给的等待时长（若有）。
func retryAfterOf(err error) time.Duration {
	f, ok := toolfail.As(err)
	if !ok || f == nil {
		return 0
	}
	return f.RetryAfter
}

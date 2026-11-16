// classifier.go —— 失败分类接缝（issue #240）：Decide 不再硬编码
// toolfail 分类器，宿主可注入自有分类器；缺省实现保留（toolfail）。
//
// # 为什么是接缝而不是换实现
//
// 本包的定位是**组合逻辑**（三问 → Decision）：「该不该重试」的**分类**
// 归上游判据。此前上游只认 `toolfail` 一家——宿主自有错误体系（自己的
// 词表/处置位）接不进来，只能把自家错误先翻译成 toolfail 词表再喂进来，
// 翻译层就是第二份分类逻辑。抽成 Classifier 之后：分类随宿主口径，
// 路径合成（Retryable/SafeToResubmit/幂等键 → Path 三档）仍是本包唯一一份。
//
// 注入形态与 sideeffect.Action.RetrySafety 同层：**输入结构上的可选字段**
// （Options.Classifier），不是包级全局——同一次构建里两条调用链可以各带
// 各的分类器，互不干扰。
package retryplan

import "github.com/oagentkit/guardx/toolfail"

// Verdict 分类器对一次失败的处置判定（toolfail 判定的接缝形态）。
//
// 字段与 toolfail.Policy 同义；Class 是**机读类别名**（进 Decision.Reason，
// 宿主词表，缺省分类器给 toolfail.Class 的字符串值）。
type Verdict struct {
	// Class 失败类别（机读；进 Decision.Reason 供宿主分流）。
	Class string
	// Retryable 原样重试是否有意义。
	Retryable bool
	// Backoff 是否建议退避后重试（限流类：立刻重试会放大拥塞）。
	Backoff bool
	// SafeToResubmit 该类失败默认能否原样重发（不产生重复副作用）。
	// 保守侧取值：只有能证明服务端未受理才置真（toolfail 同一条纪律）。
	SafeToResubmit bool
	// NeedsOperator 是否需要人工介入（改凭证 / 放权限）。
	NeedsOperator bool
}

// Classifier 失败分类接缝（issue #240）：把一次失败归入处置类别并给出
// 判定结论。
//
// 实现纪律（与 toolfail 同一条保守方向）：认不出的失败按 **不可重试**
// 处理（Retryable=false）——「查不到就不可重试」的单向失败，策略查询
// 不该是第二个不确定来源。
type Classifier interface {
	// Classify 把一次失败归入处置类别。err 非 nil（Decide 已拦 nil）。
	Classify(err error) Verdict
}

// toolfailClassifier 缺省分类器：toolfail.Classify + PolicyOf（issue #240
// 的「保留默认实现」）。经 [DefaultClassifier] 取用。
type toolfailClassifier struct{}

// Classify 实现 Classifier：判定完全基于 toolfail 的既有结论
// （Classify + PolicyOf），本包不重新分类失败。
func (toolfailClassifier) Classify(err error) Verdict {
	cls := toolfail.Classify(err)
	policy := toolfail.PolicyOf(cls)
	return Verdict{
		Class:          string(cls),
		Retryable:      policy.Retryable,
		Backoff:        policy.Backoff,
		SafeToResubmit: policy.SafeToResubmit,
		NeedsOperator:  policy.NeedsOperator,
	}
}

// DefaultClassifier 缺省分类器：toolfail 的既有判定（Classify + PolicyOf）。
//
// 单独导出是因为宿主分类器的典型形态是「先自有词表、认不出回落缺省」——
// 没有这个名字，回落就得自己再调一遍 toolfail 并把 Policy 映射回 Verdict
// （第二份翻译）。Options.Classifier 为 nil 时 Decide 内部也用它。
func DefaultClassifier() Classifier { return toolfailClassifier{} }

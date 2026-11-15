package finalize

// 失败臂排队重试裁决（P1-2）：宿主 FinalizeTaskError 失败段的排队语义收口，
// 下沉为纯函数——迁移执行与结算载荷留宿主。

// RetryFacts 失败臂裁决的注入事实（宿主给出，本包不读库）。
type RetryFacts struct {
	// RetryEnabled 失败路径排队重试开关（缺省关 = 保守取向：排队明示结算，
	// 不让坏场景无限重跑）。
	RetryEnabled bool
	// HasQueue 是否仍有排队条目。
	HasQueue bool
	// BudgetOK 排队预算未尽（续跑预算闸的判定结果，判据在宿主）。
	BudgetOK bool
}

// RetryDecision 失败臂裁决结论。
type RetryDecision int

const (
	// RetryAbandon 落 failed + 排队明示结算（不悬挂）——零值缺省。
	RetryAbandon RetryDecision = iota
	// RetryRequeue 回 created 自动以排队输入重开一轮（条件迁移由宿主执行，
	// 零行回落到放弃——迁移零行 = 他路已接手，不得再开轮）。
	RetryRequeue
	// RetryBudgetExhausted 预算尽：按预算收敛（报告与 needs_review 载荷
	// 留宿主回调）。
	RetryBudgetExhausted
)

// String 裁决的人类可读形态（留痕与测试失败信息用）。
func (d RetryDecision) String() string {
	switch d {
	case RetryRequeue:
		return "requeue"
	case RetryBudgetExhausted:
		return "budget_exhausted"
	default:
		return "abandon"
	}
}

// AdjudicateRetry 失败路径排队重试裁决：
//
//	RetryEnabled 且队列非空才进预算判定——预算未尽回 created 续跑、
//	预算尽按预算收敛；其余（缺省关 / 无排队）落 failed 并明示结算排队。
//
// 缺省关是保守取向：排队明示结算（queue_abandoned 事件）不悬挂，
// 坏场景不因自动续跑无限重跑。
func AdjudicateRetry(f RetryFacts) RetryDecision {
	if !f.RetryEnabled || !f.HasQueue {
		return RetryAbandon
	}
	if !f.BudgetOK {
		return RetryBudgetExhausted
	}
	return RetryRequeue
}

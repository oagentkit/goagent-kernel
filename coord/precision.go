// precision.go —— 租约「判定时刻—提交」窗口的可查事实（issue #271）。
//
// # 它补的是哪一段
//
// issue #194 已经要求「到期由存储时钟算」，装配期也拒绝拿不出时间权威的
// 后端。但那一条只管**时间来源**，不管**读取时刻与提交时刻是否重合**：
// 普通路径是「读存储时刻 → 快照 → 判定 → 条件事务」，两次后端交互之间
// 存在窗口。窗口内租约可能到期，而本副本仍按旧快照把续期提交成功。
//
// 本文件把这条窗口从「实现细节」变成两个可查事实：
//
//   - @@LeasePrecision@@：装配后即可查询的承诺——后端是否支持原子判定、
//     判定—提交窗口的上界、时间来源；
//   - @@LeaseVerdict@@：每次续期判定后的观测——实际窗口多长、是否在窗口内
//     越过到期时刻、是否超出预算。
//
// 原子后端（实现 @@AtomicDecision@@）窗口恒为 0；非原子后端走误差预算
// 降级路径，必须显式标记（Degraded），并允许宿主持有「这不是严格保证」
// 的口径。预算数值与来源写在 @@DefaultLeaseErrorBudget@@ 与 @@TimeSource@@。
package coord

import (
	"context"
	"fmt"
	"time"

	"github.com/oekit/pkg/errors"
)

// DefaultLeaseErrorBudget 非原子后端的**承诺误差预算**（issue #271）。
//
// 数值：250ms；来源：存储时钟（ClockSource.Now 与条件事务之间的一次
// 往返）。这个数不是测量值，而是本仓对「判定读到的时刻与提交时刻最多
// 差多少」的承诺上界：同地域、同一存储服务的条件事务往返通常在此量级内。
// 后端可以用 LeaseBudget 声明自己的真实上界；声明值 <= 0 视作未声明。
//
// 语义边界（重要）：预算**不改变**判定逻辑，也不阻止过期持有者提交——
// 它只把「可能发生的窗口」变成可解释、可观测的上界。需要严格保证的宿主
// 必须让后端实现 AtomicDecision（窗口 0），或消费 LeaseVerdict 自行处置
// 超预算事实。
const DefaultLeaseErrorBudget = 250 * time.Millisecond

// TimeSource 租约判定使用的时间权威来源（issue #271 要求写明三选一）。
type TimeSource string

const (
	// TimeSourceStorageClock 存储时钟（ClockSource；原子判定事务内时刻
	// 也属于这一档）。
	TimeSourceStorageClock TimeSource = "storage_clock"
	// TimeSourceMonotonicSequence 存储侧单调序列（etcd revision 一类；
	// 当前适配器未使用，保留词表以免宿主自造字符串）。
	TimeSourceMonotonicSequence TimeSource = "monotonic_sequence"
	// TimeSourceLocalClock 节点本地时钟（仅 SingleMachine 声明下成立）。
	TimeSourceLocalClock TimeSource = "local_clock"
)

// LeasePrecision 租约判定的承诺精度（装配后即可查询的事实，不是散文）。
type LeasePrecision struct {
	// Atomic 判定时刻与提交是否在同一存储事务内。
	Atomic bool
	// Degraded 是否走误差预算降级路径（Atomic=false 时恒为 true）。
	Degraded bool
	// Window 判定—提交窗口的承诺上界：原子后端为 0，非原子后端等于 Budget。
	Window time.Duration
	// Budget 后端声明或本仓缺省的误差预算（原子后端也保留该声明，便于观测）。
	Budget time.Duration
	// Source 时间权威来源。
	Source TimeSource
}

// leasePrecisionOf 由后端能力推导承诺精度（唯一实现，Board/Store 共用）。
func leasePrecisionOf(b Backend) LeasePrecision {
	source := TimeSourceLocalClock
	if _, ok := b.(ClockSource); ok {
		source = TimeSourceStorageClock
	}
	budget := DefaultLeaseErrorBudget
	if lb, ok := b.(LeaseBudget); ok {
		if v := lb.LeaseErrorBudget(); v > 0 {
			budget = v
		}
	}
	if _, ok := b.(AtomicDecision); ok {
		return LeasePrecision{
			Atomic:   true,
			Degraded: false,
			Window:   0,
			Budget:   budget,
			Source:   source,
		}
	}
	return LeasePrecision{
		Atomic:   false,
		Degraded: true,
		Window:   budget,
		Budget:   budget,
		Source:   source,
	}
}

// LeaseVerdict 一次续期判定的窗口观测（issue #271）。
//
// 原子路径下 DecidedAt == CommittedAt、Window == 0；非原子路径下
// CommittedAt 是提交后重读的存储时刻，Window 是两次读之间的实际差。
// 其余字段是把「窗口内发生了什么」问清楚的机器可判事实。
type LeaseVerdict struct {
	// Atomic 本次判定是否走了原子路径。
	Atomic bool
	// Observed 非原子路径是否成功观测到提交后的存储时刻
	// （读不到时 Window/BudgetExceeded 不可解释，只能标为未观测）。
	Observed bool
	// Source 时间权威来源。
	Source TimeSource
	// Budget 本次判定适用的误差预算。
	Budget time.Duration
	// DecidedAt 判定读到的存储时刻（原子路径 = 事务内时刻）。
	DecidedAt time.Time
	// CommittedAt 提交后的存储时刻（原子路径 = DecidedAt）。
	CommittedAt time.Time
	// Window 判定—提交实际窗口（非负；原子路径为 0）。
	Window time.Duration
	// ExpiredInWindow 本次判定所依据的租约到期时刻落在窗口内
	// （即提交时租约已经到期，判定依据的是过期快照）。
	ExpiredInWindow bool
	// BudgetExceeded 实际窗口超过承诺预算。
	BudgetExceeded bool
}

// Warning 返回可解释告警；无异常时返回空串。
//
// 非原子路径的异常必须能被宿主直接写进日志/审计，而不是只留下
// 「续期成功了」这一条看不出风险的事实。
func (v LeaseVerdict) Warning() string {
	if v.Atomic {
		return ""
	}
	switch {
	case !v.Observed:
		return fmt.Sprintf("coord: 非原子租约判定（来源 %s）提交后无法读取存储时钟，误差预算 %s 不可观测——本次判定不得作为严格保证", v.Source, v.Budget)
	case v.ExpiredInWindow && v.BudgetExceeded:
		return fmt.Sprintf("coord: 租约在判定—提交窗口内过期（窗口 %s，预算 %s，来源 %s）且窗口超出预算——后端时间上界已不可信，宿主不得把本次续期当作严格保证", v.Window, v.Budget, v.Source)
	case v.ExpiredInWindow:
		return fmt.Sprintf("coord: 租约在判定—提交窗口内过期（窗口 %s，预算 %s，来源 %s）——非原子路径按误差预算降级，宿主可接受该风险但不得视为严格保证", v.Window, v.Budget, v.Source)
	case v.BudgetExceeded:
		return fmt.Sprintf("coord: 判定—提交窗口 %s 超出预算 %s（来源 %s）——后端原子性/延迟异常，本次判定的时间上界不再可信", v.Window, v.Budget, v.Source)
	default:
		return ""
	}
}

// leasePlan 一次续期判定：返回结果、判定所依据的旧到期时刻、断言与写入。
//
// expiresAt 用于事后判断「窗口内是否越过到期」；零值 = 不限期（无到期可越）。
type leasePlan[T any] func(now time.Time, snap map[string]Entry) (result T, expiresAt time.Time, asserts []Assert, mutations []Mutation, err error)

// renewWithVerdict 续期判定的统一执行体：有 AtomicDecision 走原子事务，
// 否则走「读时刻→快照→判定→提交→再读时刻观测」的降级路径。
//
// 两条路径的重判循环共用 casMaxAttempts：断言失败取新快照重判。
func renewWithVerdict[T any](
	ctx context.Context,
	b Backend,
	injected func() time.Time,
	keys []string,
	plan leasePlan[T],
) (T, LeaseVerdict, error) {
	precision := leasePrecisionOf(b)

	if ad, ok := b.(AtomicDecision); ok {
		rejected := LeaseVerdict{
			Atomic: true,
			Source: precision.Source,
			Budget: precision.Budget,
		}
		for range casMaxAttempts {
			var (
				result  T
				at      time.Time
				planned bool
			)
			applied, committedAt, err := ad.DecideTx(ctx, func(now time.Time, read func(keys ...string) (map[string]Entry, error)) ([]Assert, []Mutation, error) {
				at = now
				snap, rerr := read(keys...)
				if rerr != nil {
					return nil, nil, rerr
				}
				r, _, asserts, mutations, perr := plan(now, snap)
				if perr != nil {
					return nil, nil, perr
				}
				result = r
				planned = true
				return asserts, mutations, nil
			})
			if err != nil {
				var zero T
				rejected.DecidedAt = at
				return zero, rejected, errors.Wrap(err, "coord: 原子判定事务失败")
			}
			if applied {
				return result, LeaseVerdict{
					Atomic:      true,
					Observed:    true,
					Source:      precision.Source,
					Budget:      precision.Budget,
					DecidedAt:   at,
					CommittedAt: committedAt,
					Window:      0,
				}, nil
			}
			if planned {
				continue // 断言失败：取新快照重判（原子事务内重新取时）
			}
		}
		var zero T
		return zero, rejected, errors.Wrap(ErrTxnConflict,
			"coord: 原子判定重试上限内未提交（高争用或后端原子性可疑）")
	}

	for range casMaxAttempts {
		decidedAt, err := resolveNow(b, injected, ctx)
		if err != nil {
			var zero T
			return zero, LeaseVerdict{}, errors.Wrap(err, "coord: 取存储时钟失败")
		}
		snap, err := b.Snapshot(ctx, keys...)
		if err != nil {
			var zero T
			return zero, LeaseVerdict{}, errors.Wrap(err, "coord: 快照读失败")
		}
		result, expiresAt, asserts, mutations, err := plan(decidedAt, snap)
		if err != nil {
			var zero T
			return zero, LeaseVerdict{}, err
		}
		applied, err := b.Txn(ctx, asserts, mutations)
		if err != nil {
			var zero T
			return zero, LeaseVerdict{}, errors.Wrap(err, "coord: 条件事务失败")
		}
		if !applied {
			continue // 断言失败：取新快照重判
		}
		verdict := LeaseVerdict{
			Atomic:    false,
			Source:    precision.Source,
			Budget:    precision.Budget,
			DecidedAt: decidedAt,
		}
		committedAt, oerr := resolveNow(b, injected, ctx)
		if oerr != nil {
			return result, verdict, nil // 提交已成立；观测失败只让窗口不可解释
		}
		verdict.Observed = true
		verdict.CommittedAt = committedAt
		verdict.Window = committedAt.Sub(decidedAt)
		if verdict.Window < 0 {
			verdict.Window = 0
		}
		verdict.ExpiredInWindow = !expiresAt.IsZero() && !committedAt.Before(expiresAt)
		verdict.BudgetExceeded = verdict.Window > precision.Budget
		return result, verdict, nil
	}
	var zero T
	return zero, LeaseVerdict{}, errors.Wrap(ErrTxnConflict,
		"coord: 续期重试上限内未提交（高争用或后端原子性可疑）")
}

// LeasePrecision 报告本适配器的租约判定精度承诺（issue #271）。
//
// 装配后调用一次即可：Atomic=true ⇒ 判定与提交不重合（窗口 0）；
// Atomic=false ⇒ 走误差预算降级路径，Window 是承诺上界（Budget），
// 每次判定后的实际窗口用 RenewWithVerdict 观测。
func (a *BoardAdapter) LeasePrecision() LeasePrecision {
	return leasePrecisionOf(a.b)
}

// LeasePrecision 报告本存储适配器的占用/意图续期精度承诺（issue #271）。
func (s *StoreAdapter) LeasePrecision() LeasePrecision {
	return leasePrecisionOf(s.b)
}

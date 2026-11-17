// effect.go —— 动作的执行体契约（Prepare / Apply 两段）。
//
// 从 runner.go 拆出的理由：执行体是**宿主写的回调**，编排器是**内核写的
// 编排**——两者的读者不同（宿主读执行体契约，内核读编排顺序），而执行体
// 声明带接缝契约注释（issue #370 形态 B），放在同一文件里会把编排器的判据
// 顺序淹没在回调契约中。按职责分文件，不按行数分（与 worker/loop.go ↔
// worker/execute.go 同款切法）。
package sideeffect

import (
	"context"
)

// Effect 动作的执行体。
//
// 拆成「准备」与「施加」两段，是为了在**两者之间**插入持有资格检查：
// 准备阶段可能很慢（调外部接口、算结果），而闸的有效期可能在这段时间里
// 到期被别人接管。此时若不加检查就进入施加阶段，就是**两份闸同时开着**
// ——本次动作与新持有者做同一件事，副作用施加两次。
//
// # Apply 为什么必须拿到 Lease
//
// Lease 里带着**栅栏令牌**（Fence）。它不是给你看的，是给你**传下去**的：
//
//   - 下游按令牌拒收陈旧写入 → 过期持有者的迟到写被下游挡下（见 FencedSink）；
//   - 下游按令牌/幂等键去重 → 重试不会重复施加（此时才敢声明
//     Action.RetrySafety = RetrySafetySafe）。
//
// 不给令牌，下游就无从区分「这次写」与「那次过期持有者的写」——
// 栅栏就只剩上游那半截，形同虚设。
//
// # 两段的失败语义不同
//
//   - Prepare 失败 → 动作没开始，**未施加任何副作用**（可安全重试）；
//   - Apply 失败 → 副作用**可能已生效**，是否生效取决于远端
//     （见 Action.RetrySafety）。
type Effect struct {
	// Prepare 准备阶段：算结果、构造请求，但**不施加副作用**。
	// 可为 nil（不需要准备阶段）。
	//
	// # 接缝契约（issue #370 形态 B）
	//
	// @seam: no-reentry no-blocking panic-isolated
	//
	// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
	// 内核侧：准备阶段在**不持任何内核锁**处被调用（Runner 无锁）。
	// no-blocking：宿主实现内不得无界等待——准备可能耗时超过 TTL，随后
	// **施加前的持有资格检查**会拒（ErrLeaseLost ⇒ 本次动作干净放弃）。
	// panic-isolated：**宿主侧兑现**——内核不隔离本回调的 panic（本包无
	// recover），宿主实现须自行保证不 panic。
	Prepare func(ctx context.Context) (any, error)
	// Apply 施加阶段：真正对外部世界动手。prepared 是 Prepare 的产出，
	// lease 是本次持有（含栅栏令牌）——把它传给下游。
	//
	// # 接缝契约（issue #370 形态 B）
	//
	// @seam: no-reentry no-blocking panic-isolated
	//
	// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
	// 内核侧：施加阶段在**不持任何内核锁**处被调用（Runner 无锁）。
	// no-blocking：宿主实现内不得无界等待——内核只在**施加前**检查一次资格，
	// 施加期间闸到期即被别人接管 ⇒ 两边同时做同一动作；长动作应由宿主在
	// 执行器内部续期（Store.Renew）并把 lease.Fence 传给下游。
	// panic-isolated：**宿主侧兑现**——内核不隔离本回调的 panic（本包无
	// recover），宿主实现须自行保证不 panic。
	Apply func(ctx context.Context, prepared any, lease Lease) (Result, error)
}

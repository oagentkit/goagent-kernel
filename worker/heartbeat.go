// heartbeat.go —— 注册心跳与在途租约续期（issue #179/#196 共用的单拍循环）。
//
// # 两个心跳、一拍执行、互不阻断
//
// 续期先做、注册心跳后做：身份的可用性不是任务正确性的前置条件——
// 注册表抖动不得吞掉同一拍的租约续期（issue #179 的触发序列）。
//
// # 并发面：按任务独立续期，失败逐个处置
//
// 在途集合是 `map[TaskID]*claimed`（见 loop.go 的 inflight）。每一拍对
// **快照里的每一条**任务租约各做一次 Renew（Renew = 资格检查 + 推后到期）：
//
//   - 成功 → 该任务的连续失败计数清零；
//   - ErrLeaseLost → 只取消**该任务**的在途 ctx（其它任务不受影响）；
//   - 其它错误 → 只累加该任务的计数；连续失败达到阈值（ErrLeaseUnconfirmed）
//     时按既有契约停止领取并取消该任务——答不出「谁在持」就不发起新的副作用。
//     停止领取会取消领取闸（claimCtx），已投递的在途任务走排水路径收场。
//
// 计数住在 claimed 上、只由本协程读写：任务移出集合后无人再碰，故无需
// 额外锁；在途集合本身由 inflight.mu 护住。
package worker

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// heartbeat 跑一拍一轮的心跳/续期循环，直到外层 ctx 取消、stopping 关闭
// 或注册身份丢失。done 在退出时关闭（Run 用它建立「心跳已退出」的先行发生）。
func (l *Loop) heartbeat(
	ctx context.Context,
	claimCancel context.CancelFunc,
	reg Registration,
	set *inflight,
	stopping <-chan struct{},
	lost, unconfirmed *bool,
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(l.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopping:
			return
		case <-ticker.C:
		}

		// 任务租约续期：快照在本拍内固定，逐条独立处置（一条失败不阻断
		// 同拍的其它任务——与「两个故障域互不阻断」同一条纪律）。
		for _, cur := range set.snapshot() {
			cur.renewLocked(func() {
				_, rErr := l.board.Renew(ctx, cur.id, cur.token, taskboard.ClaimRequest{TTL: l.cfg.TTL})
				switch {
				case rErr == nil:
					cur.renewFails = 0
				case errors.Is(rErr, taskboard.ErrLeaseLost):
					// 资格没了：取消**这一条**在途执行（施加之前封口），并留痕。
					cur.renewFails = 0
					l.notifyLeaseLost(cur.id, LeaseLostTakenOver)
					cur.cancel()
				default:
					// 其它续期错误：下一拍重试（阈值内租约尚未到期，仍有出路）；
					// 连续失败跨过阈值 = 无法确认持有资格——与新持有者重复施加
					// 的是同一种危险，封口并停止领取。
					cur.renewFails++
					if cur.renewFails >= l.cfg.RenewFailMax && ctx.Err() == nil {
						*unconfirmed = true
						l.notifyLeaseLost(cur.id, LeaseLostUnconfirmed)
						claimCancel() // 停止领取：答不出「谁在持」就不发起新的副作用
						cur.cancel()  // 在途封口
					}
				}
			})
		}

		// 注册心跳：身份的资格检查。非身份类失败（含 ctx 取消的抖动）
		// 不判丢失——把存储抖动当成身份丢失，驱动会在任务板正常时自杀。
		// 失败也不回退上面已做过的续期（两个故障域互不阻断，issue #179）。
		if _, hbErr := l.registry.Heartbeat(ctx, reg.ID, reg.Token, l.cfg.TTL); hbErr != nil {
			if errors.Is(hbErr, ErrNotRegistered) {
				*lost = true
				claimCancel()
				return
			}
			if ctx.Err() != nil {
				return
			}
			// 存储抖动：下一拍重试（只影响身份，不影响上面的续期）。
		}
	}
}

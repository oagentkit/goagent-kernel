package reconcile

import "time"

// 宽限窗与三阈值（bianque recovery.go:104-176 的常量原样下沉；宿主经
// Config 调档，0=取这里的缺省）。
//
// 三阈值量的是三件不同的事，**不许互相顶替**：
//   - Stall：本副本会话「多久没事件才算死」——活跃编排即使在长步骤期间也有
//     进度事件在持续产生，这是死亡的**唯一**证据（本副本无心跳可借）；
//   - DeadOwner：他副本无心跳行时的回落静默——是 Stall 的 3 倍，给慢步骤与
//     网络抖动留余量；活跃副本会持续打事件，不会误伤；
//   - LeaseTTL：他副本心跳租约——**主动续约**面，让存活副本名下的会话不再被
//     按静默阈值误收（长步骤期间事件可能静默数分钟，心跳不会）。
const (
	// DefaultStall 事件静默阈值（缺省 10m）：running 会话超过该时长无任何事件
	// 即判定编排已死。
	DefaultStall = 10 * time.Minute
	// DefaultDeadOwner 他副本死主回落阈值（缺省 30m，Stall 的 3 倍）。
	DefaultDeadOwner = 30 * time.Minute
	// DefaultLeaseTTL 心跳租约缺省 TTL（缺省 60s）：打点间隔 15s 的容忍 3 拍丢失。
	DefaultLeaseTTL = 60 * time.Second
	// GraceWindow 决策/续约宽限窗（30s）：判定时间在该窗口内视为「活流程可能
	// 仍在处理」，不介入；租约判定里它是「租约刚过期的重启重打窗口」。
	GraceWindow = 30 * time.Second
)

// LeaseState 心跳租约判定结论（仅对 owner=他副本的会话有意义）。
type LeaseState int

const (
	// LeaseNoRow 无心跳行（存量版本副本 / 心跳面读失败降级 / 心跳判定已关闭）：
	// **回落事件静默**——以更宽的 DeadOwner 阈值近似存活，不误伤活跃副本。
	LeaseNoRow LeaseState = iota
	// LeaseLive 心跳新鲜，或租约刚过期仍在重打宽限窗内：**免检**（活编排
	// 名下不等事件静默——长步骤的静默是常态，不是死亡证据）。
	LeaseLive
	// LeaseExpired 租约过期（含宽限窗）：**不等事件静默立即回收**。
	LeaseExpired
)

// OrphanAtBoot 启动锚点判据：最后事件早于「启动锚点 − 额外宽限」即孤儿。
//
// 语义（与另两路的差异见包头）：进程启动即替代同 instanceID 的上一具尸体，
// 此刻名下会话定义性地是孤儿——**无须等事件静默**。反向的免检条件同样在
// 判据里：boot 后仍有事件产生 = 本进程或同标识存活进程在推进，天然免检。
//
// 总函数（零值/关闭态返回 false，不 panic）：boot 零值 = 没有启动事实、
// bootGrace < 0 = 显式关闭——两者都不得把「无法判定」写成「是孤儿」。
// 边界：last 恰等于 cutoff **不算**孤儿（Before 严格小于），宽限窗的语义是
// 「往回收敛的方向再多留一格」。
func OrphanAtBoot(last, boot time.Time, bootGrace time.Duration) bool {
	if boot.IsZero() || bootGrace < 0 {
		return false
	}
	return last.Before(boot.Add(-bootGrace))
}

// JudgeLease 心跳租约判据（仅他副本名下；本副本走 Stall 静默，见包头）。
//
//   - beats 为 nil、lease <= 0（心跳判定关闭）或 owner 无心跳行 → LeaseNoRow
//     （回落事件静默）；
//   - now − beat < lease + GraceWindow → LeaseLive：心跳新鲜，或租约刚过期但
//     仍在「重启副本重打」宽限窗内——活编排名下**免检**；
//   - 否则 LeaseExpired：租约过期，不等事件静默立即回收。
//
// 宽限窗必须加在租约上：副本重启后要花一拍（15s）才重建心跳，没有这 30s，
// 每次重启都会被他副本按「租约过期」误收一波。
func JudgeLease(beats map[string]time.Time, owner string, now time.Time, lease, grace time.Duration) LeaseState {
	if beats == nil || lease <= 0 {
		return LeaseNoRow
	}
	beat, ok := beats[owner]
	if !ok {
		return LeaseNoRow
	}
	if now.Sub(beat) < lease+grace {
		return LeaseLive
	}
	return LeaseExpired
}

// Stalled 事件静默判据：now − last >= threshold 即静默超阈（判定编排已死）。
//
// 边界是**含等号**：恰等于阈值就算超（宿主原实现 `now.Sub(last) < threshold
// 才跳过` 的反面）。threshold <= 0 视为立即超阈——那是「关闭静默窗」的危险
// 方向，Config 不提供该入口（Stall/DeadOwner 只接受正值覆盖），此处置写明
// 是为了判据本身是总函数，不是给宿主开的口子。
func Stalled(last, now time.Time, threshold time.Duration) bool {
	return !now.Before(last.Add(threshold))
}

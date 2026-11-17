// quota_contract.go —— 配额原语的**契约面**（宿主接地面）。
//
// # 为什么与参考实现分开成文件
//
// quota_store.go 承载的是**判定与实现**（MemQuotaStore：原子预占、凭令牌归还、
// 回收协议、账本形态）；本文件承载的是**宿主接地面**——两个接口
// （QuotaStore 存储中立契约、TaskLeaseOracle 任务租约判定面）与两处装配项。
//
// 拆分的理由不是行数好看，而是**两类东西的纪律不同**：契约面写的是「宿主实现
// 必须怎样」，实现面写的是「内核自己怎么判」。混在一份文件里，宿主读契约要在
// 实现细节里翻，宿主改实现又会顺手改掉契约。
//
// # 三条接缝约束的共同前提
//
// 本文件里的接缝（QuotaStore / TaskLeaseOracle / WithQuotaClock 的时钟）在
// **内核侧**都有一条共同前提：**回调在存储锁外被调用**（issue #366）——
// 见 MemQuotaStore.now（锁内取值、解锁后调用）与 Reclaim（先解锁再问判定面）。
// 这条前提是 no-reentry 能成立的基础：宿主回调若在锁内被调用，「回调里回查
// 本包」就是永久死锁（sync.Mutex 不可重入）。
package scheduling

import (
	"context"
	"time"
)

// TaskLeaseOracle 任务租约存活判定（回收判据依赖任务租约闭环，#265）。
//
// 实现由宿主提供（通常包装 taskboard.Board 的租约/续期状态）；返回
// alive=true 表示任务仍被**活着**持有，此时不得回收配额。
//
// 接缝契约（宿主注入的判定面；一条接缝覆盖其全部方法）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：判定面内不得回查本包（不得再读写配额账本）。内核侧兑现「调用点
// 不持锁」：Reclaim **先释放存储锁再问判定面**（issue #366）——判定面回查
// 配额账本不会自锁。
// no-blocking：判定是同步的，且它挡在回收路径上：慢判定面拖住整轮扫描
// （SweepExpired 逐条走同一判据），须自行有界。
// panic-isolated：**宿主侧兑现**——本包不做 recover：判定面 panic 会穿到
// Reclaim/SweepExpired 的调用方（宿主实现须自行保证不 panic）。
type TaskLeaseOracle interface {
	TaskLeaseAlive(ctx context.Context, taskID string) (bool, error)
}

// QuotaStore 多副本共享的配额原语。
//
// 实现方必须保证：
//
//   - Reserve / ReserveLease **原子**：余额判定与扣减不可分割（并发预留
//     只能有一个赢家越过上限；余额不足时**一个额度都不占**）；
//   - Release **原子且凭 Token**：同一租约至多归还一次；未知/已归还的
//     令牌报 ErrOverRelease（账不平必须显式，不静默吞）；
//   - Reclaim **凭 Token + 归属 + 有效期**：预占未到期
//     （ErrLeaseNotExpired）、关联任务租约仍有效（ErrLeaseStillHeld）、
//     或无判定面（ErrNoLeaseOracle）时**不回收**；成功回收后迟到的
//     Release 必须被 ErrOverRelease 拒收且不改账；
//   - SweepExpired 逐条走同一 Reclaim 判据，返回成功回收的条数；
//   - 上限与用量由**存储侧**持有（副本不各自记一份）。
//
// 接缝契约（宿主实现的跨进程存储；一条接缝覆盖其全部方法）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：实现内不得回查本包（不得再走本包的配额接口）。内核侧兑现「调用点
// 不持锁」：本包调用存储方法的路径不持任何内核锁；进程内参考实现的存储时钟在
// **锁外**取值（issue #366/#317）。
// no-blocking：存储调用是同步的，跨副本实现会做 I/O（DB 条件 UPDATE / Redis
// Lua）——须自行有界（不得无界等待）；本包不替它设期限。
// panic-isolated：**宿主侧兑现**——本包不对存储调用做 recover：实现 panic 会穿
// 到调用方（宿主实现须自行保证不 panic）。
type QuotaStore interface {
	// Reserve 预占 n 个额度（等价 ReserveLease 的无归属/无期限形态）。
	//
	// n <= 0 或 resource 为空 ⇒ ErrInvalidConfig；余额不足 ⇒
	// ErrQuotaExhausted（整单拒绝，不做部分占用）。
	Reserve(ctx context.Context, resource string, n int64) (QuotaLease, error)
	// ReserveLease 带归属与有效期的预占（推荐入口；issue #267）。
	ReserveLease(ctx context.Context, req QuotaRequest) (QuotaLease, error)
	// Release 凭租约全额归还。未知/重复的令牌 ⇒ ErrOverRelease。
	Release(ctx context.Context, lease QuotaLease) error
	// Reclaim 回收悬挂预占（凭 Token；契约见类型级说明）。
	Reclaim(ctx context.Context, lease QuotaLease) error
	// SweepExpired 扫描全部到期预占并逐条按 Reclaim 判据回收，
	// 返回实际回收条数（租约仍有效/未到期/已被归还的不计）。
	SweepExpired(ctx context.Context) (int, error)
}

// QuotaStoreOption MemQuotaStore 的装配项。
type QuotaStoreOption func(*MemQuotaStore)

// WithQuotaClock 注入存储时钟（到期判定按它；缺省 time.Now）。
//
// # 契约：回调在**存储锁外**调用（issue #366）
//
// 回调**不得回查本包**（读一次用量是极自然的写法，而 sync.Mutex 不可重入
// ⇒ 永久死锁），也**不得阻塞**（它是到期判定的同步路径）。内核侧三条调用
// 路径都在锁外取时钟值。
//
// 接缝契约（宿主注入的时钟回调）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：时钟回调内不得回查本包（不得再读用量/账本）。内核侧兑现「调用点
// 不持锁」：now() 在**锁内取函数值、解锁后再调用**（issue #366）——三条调用
// 路径（ReserveLease / Reclaim / SweepExpired）都在锁外取值。
// no-blocking：时钟在到期判定的**同步路径**上被调用，须廉价返回、不得无界等待。
// panic-isolated：**宿主侧兑现**——本包不做 recover：时钟回调 panic 会穿到
// 预占/回收/扫描的调用方（宿主实现须自行保证不 panic）。
func WithQuotaClock(clock func() time.Time) QuotaStoreOption {
	return func(m *MemQuotaStore) {
		if clock != nil {
			m.clock = clock
		}
	}
}

// WithQuotaLeaseOracle 注入任务租约判定面（Reclaim 的归属判据）。
func WithQuotaLeaseOracle(o TaskLeaseOracle) QuotaStoreOption {
	return func(m *MemQuotaStore) {
		if o != nil {
			m.oracle = o
		}
	}
}

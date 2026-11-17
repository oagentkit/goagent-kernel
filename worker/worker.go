// Package worker Worker Runtime 的**生命周期契约与驱动**：注册 → 心跳 →
// 领取 → 执行 → 续期/恢复 → 优雅退出。
//
// 生命周期: Experimental（v0.35.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 它补的是哪一段（issue #106）
//
// @delegate@ 是委托的契约面（能不能交 / 交给谁 / 回来算不算完成），
// @taskboard@ 是认领的契约面（谁负责哪个子任务、怎么把认领还回去）——
// 但「执行这些子任务的 worker 自己怎么活」此前没有机制面：各宿主自己写
// 注册、心跳、领取循环、退出清理，各写一套必然各漏一样（漏心跳 ⇒ 别人
// 以为它死了；漏续期 ⇒ 长任务必被接管；漏注销 ⇒ 注册表里全是幽灵）。
//
// 本包把生命周期收成两样：
//
//  1. @@Registry@@：worker 注册/心跳/注销的**存储中立契约**（与
//     taskboard.Board 同一范式：语义归本包，落库归宿主）；
//  2. @@Loop@@：把六段生命周期串起来的**驱动**——领取走 taskboard.Board，
//     续期走 taskboard.RenewingBoard（issue #87 的能力，心跳不是摆设：
//     它续的就是那条契约的租约），退出走排水语义。
//
// # 心跳与续期是两个心跳（都接上了既有契约）
//
//   - **worker 心跳**（@@Registry.Heartbeat@@）：「这个 worker 还活着」——
//     注册表按它判离线，是资格检查不是纯计时（令牌不匹配即拒绝）；
//   - **任务租约续期**（@@taskboard.RenewingBoard.Renew@@）：「这次认领
//     还有资格」——它是 issue #87 定的语义：校验令牌仍是最新代 + 推后
//     到期时刻，**同一次原子操作**。租约易主（@@ErrLeaseLost@@）时驱动
//     **取消在途执行的 ctx**：资格没了，施加副作用前必须收手——这正是
//     sideeffect「危险窗口要在施加之前封口」的同一条纪律在 worker 侧的落法。
//
// # 并发池（issue #196）
//
// Config.Concurrency 缺省 1 = 串行驱动（领一个、跑完、再领下一个），既有
// 宿主的语义逐字不变。显式 >1 时领取与执行解耦：领取环单点认领，执行体在
// 有界池里并行，每任务独立 token / cancel / 连续续期失败计数；慢任务不再
// 占住整个副本的领取能力。停机仍是「停止领取 + 在途逐个收场（WithoutCancel
// 落库）」，排水上界由 handler 对 ctx 取消的响应决定（与串行同一条契约）。
//
// # 恢复（Recover）判资格不判时钟
//
// 崩溃恢复 = 接管前任留下的过期任务。@@Recover@@ 对每个在认领态的任务
// 直接尝试 @@Claim@@：租约仍活的被 @@ErrTaken@@ 拒（跳过），已过期的被
// 存储侧判过资格、授予新代——**过期判定归存储侧**（与 taskboard 的
// 判据归存储同一条纪律），本包不自建第二套时钟判据。
//
// # 边界（契约 + 进程内参考实现；跨进程落库归宿主）
//
// 本包含契约、驱动与一份**进程内参考实现**（MemRegistry，memory.go）：
// 后者的原子性来自进程内互斥锁，只给契约用例与宿主当形态示范——多副本
// 共享注册表的跨进程实现（Redis/PG/etcd）归宿主，本仓零数据库依赖。
// 端到端演示仍在 examples/worker-runtime（Worker 生命周期要靠真实的任务板
// 与注册表才能验证）。
package worker

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"
)

// ErrInvalidConfig Loop/Spec 的配置非法（空 ID、非正 TTL、心跳间隔不短于
// 注册有效期等）。
//
// 装配期就红：心跳间隔长于有效期 = 注册必然周期性过期，症状是
// 「同事 sees 幽灵/失踪交替」，而配置里每个数字看起来都合法。
var ErrInvalidConfig = errors.New("worker: 配置非法")

// ErrNotRegistered 注册已失效：令牌不匹配、注册被顶替、或有效期已过。
//
// 心跳拿到它 = 这个 worker 的身份已经没了（凭据由注册表铸造，丢了就是
// 丢了）。驱动收到它即停止领取新任务、收刀退出——身份没了还继续领任务，
// 账都记不到它头上。
var ErrNotRegistered = errors.New("worker: 注册已失效（令牌不匹配或有效期已过）")

// ErrLeaseUnconfirmed 在途任务的租约连续续期失败跨过一个 TTL：驱动无法
// 确认自己仍是持有者，已取消在途执行并停止领取收刀（issue #179）。
//
// 与 @@ErrNotRegistered@@ 分列：那是身份没了，这是任务资格答不出来——
// 两者对宿主的处置不同（前者查注册表/顶替，后者查任务板/存储健康）。
// 驱动报它即已收刀：续不上还继续开工，施加的每个副作用都可能是
// 与新持有者的双份。
var ErrLeaseUnconfirmed = errors.New("worker: 任务租约无法确认持有资格（连续续期失败跨过 TTL）")

// errBoardPanic 宿主任务板续期调用 panic（已由内核隔离，issue #368）。
//
// 不导出：它只是逐条续期失败的判据来源（记一次普通失败），宿主可见的信号
// 是续期失败与租约失去资格，不是一个内部 panic 值。
var errBoardPanic = errors.New("worker: 宿主任务板续期 panic（已隔离）")

// Spec 注册声明：worker 是谁、要多久的心跳周期。
type Spec struct {
	// ID worker 标识（宿主词表；空串非法）。允许重复注册：后到者顶替
	// 先到者并铸新令牌（崩溃重启是正常路径，先到者的心跳从此被拒
	// ——它由此得知自己已被顶替，而不是靠幽灵状态猜）。
	ID string
	// Labels 原样携带的标签（能力声明、分组等；语义由宿主解释）。
	Labels map[string]string
	// TTL 注册有效期（>0 必填）：两次心跳之间隔多久算离线。
	// 这与任务的租约 TTL 是两回事——worker 活着不等于某个任务还在做。
	TTL time.Duration
}

// Validate 装配期自检（违者报 ErrInvalidConfig）。
func (s Spec) Validate() error {
	switch {
	case s.ID == "":
		return errors.Wrap(ErrInvalidConfig, "worker: 注册声明缺 ID（留痕无从定位）")
	case s.TTL <= 0:
		return errors.Wrap(ErrInvalidConfig, "worker: 注册 TTL 必须为正——零值等于「一次心跳都不跳也永远在线」")
	}
	return nil
}

// Registration 一次注册的凭证（由注册表铸造；心跳与注销的唯一凭据）。
//
// 与 taskboard「令牌由存储铸造」同一条纪律：调用方自带的凭据不是凭据
// ——凭据铸造在注册表的原子操作里，调用方拿到的只是它。
type Registration struct {
	// ID worker 标识。
	ID string
	// Token 注册表铸造的凭据。
	Token string
	// ExpiresAt 当前有效期终点（存储时钟口径；心跳成功后前移）。
	ExpiresAt time.Time
}

// Status 注册表里一条 worker 记录的快照（List 的产出）。
//
// Alive 由**注册表用自己的时钟**判（判据归存储侧，不归调用方——
// 跨进程时钟对不上，调用方拿 time.Now 比对 ExpiresAt 是自建第二套判据）。
type Status struct {
	// ID worker 标识。
	ID string
	// Labels 注册时声明的标签（快照）。
	Labels map[string]string
	// ExpiresAt 有效期终点（存储时钟口径）。
	ExpiresAt time.Time
	// Alive 注册表判定：有效期未过（在线）。已过期的记录是否被清除
	// 由实现决定（清了就不在 List 里；不清则 Alive=false 显式可见）。
	Alive bool
}

// Registry worker 注册表的存储中立契约。
//
// 实现方必须保证：
//
//   - Register / Heartbeat / Deregister 的**资格校验与写入**是一次原子操作
//     （心跳推后有效期与校验令牌不可分——拆两步就是「假心跳」：
//     顶替者已在位，原持有者的心跳延长的却是别人的注册）；
//   - 心跳凭 Token 认人，不匹配报 @@ErrNotRegistered@@（不静默成功）；
//   - ExpiresAt 由**存储自己的时钟**推算（调用方时钟与存储时钟不可假设一致）。
type Registry interface {
	// Register 注册一个 worker（同 ID 顶替旧注册并铸新令牌）。
	Register(ctx context.Context, spec Spec) (Registration, error)
	// Heartbeat 心跳 = 资格检查 + 延长有效期（凭 Token；TTL 语义与
	// Register 一致：正数按存储时钟推算，<=0 报错——「不设限的注册」
	// 会让幽灵永远在线）。
	Heartbeat(ctx context.Context, id, token string, ttl time.Duration) (time.Time, error)
	// Deregister 注销（凭 Token；优雅退出收尾）。
	Deregister(ctx context.Context, id, token string) error
	// List 全量快照（判据归存储侧：Alive 用存储时钟判）。
	List(ctx context.Context) ([]Status, error)
}

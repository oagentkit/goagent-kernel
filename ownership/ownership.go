// Package ownership 跨区域任务所有权权威的**契约面**（issue #273）：
// 一个任务域当前归哪个区域、所有权是第几代、提交路径凭什么把旧持有者拒掉。
//
// 生命周期: Experimental（v0.43.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 它补的是哪一段
//
// @@handoff@@ 的 Epoch 管的是**交接单**的代次（同一 Key 重新挂出 = 新一代），
// @@coord@@ 的时间权威管的是**租约到期**由存储算（不信任节点本地时钟）。
// 两者都不回答「网络分区后，哪个区域有权提交这个任务域」：旧区域可以拿着
// 一个从未失效的 owner 身份继续提交，而新区域也认为自己是负责人——两个
// 执行体同时写同一份外部状态，报告里却只有两份「各自成功」。
//
// # 给什么、不给什么
//
// **给**：
//
//   - 契约：@@Authority@@（权威管理面）/ @@Fencer@@（提交路径唯一接缝）/
//     @@Grant@@ / @@Commit@@ / @@Transfer@@；
//   - 配置事实：@@RegionMap@@——任务域到权威区域的映射，可校验、可查、
//     枚举顺序确定（不是散落在注释里的约定）；
//   - 纯判据：@@Judge@@——当前授权与一次提交声明逐项对比（宿主实现可直接
//     复用，判定逻辑只有一份）；
//   - 两阶段转移：@@BeginTransfer@@（提案，**不改变**当前权威）→
//     @@ConfirmTransfer@@（目标区域凭回执令牌确认，换代）；
//   - 参考实现：@@MemoryAuthority@@（共享权威，单进程参考；跨进程落库归
//     宿主）与 @@Local@@（单区域退化：本区域即权威，转移显式拒绝）。
//
// **不给**：
//
//   - 共享权威的持久化、事务边界、选主与共识——那是宿主资产（与
//     taskboard「不给存储实现」同一条分工）；
//   - 第二个租约：本包**没有** TTL / 到期 / 续期。所有权不是租约，不随
//     时间自动失效；任务租约仍归 @@taskboard.Lease@@ / @@handoff.Ticket.TTL@@。
//     把两套时限混在一起，只会得到「所有权过期但任务租约还在」的双真相；
//   - 多自治域之间的最终一致合流：issue #273 的顺序纪律是**先**做
//     「多集群共享一个权威任务域」（L1+L2 齐备），**再**做多自治域 Handoff。
//
// # 三个概念
//
//   - @@Domain@@ 任务域：所有权的作用域（开放键，内核不解释业务语义）；
//   - @@Region@@ 区域：权威的持有者（开放键，内核不解释部署拓扑）；
//   - @@Epoch@@ 所有权代次：同一任务域上每次**确认的**转移严格 +1，
//     旧代一律不得提交（防 ABA：代次不复用、不跳号）。
//
// 不变量：**每个任务域在任一时刻恰好一个权威区域**。确认前是旧区域，
// 确认后是新区域，不存在「两个都算」的中间态。一个区域可以同时对多个
// 任务域有权威——约束在**域**上，不在区域上。
//
// # 两阶段转移（Epoch + 确认协议）
//
//  1. @@BeginTransfer@@：当前权威区域凭**当前授权的令牌**提出转移。
//     权威记录「候选代次 = 当前代次 + 1」与一个**回执令牌**，但当前授权
//     （Owner / Mapping）保持不变——提案不是授权。
//  2. @@ConfirmTransfer@@：目标区域**凭回执令牌**确认。成功后候选代次
//     成为当前代次、新区域成为唯一权威，权威铸造新的授权令牌。
//
// 缺确认不可用：确认前目标区域拿候选代次提交 ⇒ @@ErrEpochUnconfirmed@@；
// 拿当前代次提交 ⇒ @@ErrNotOwner@@。旧区域在确认后拿旧代次提交 ⇒
// @@ErrStaleEpoch@@。提案可被同持有者的新提案取代（旧回执
// ⇒ @@ErrNoTransfer@@）；确认回执重放幂等（返回同一授权，不推进代次）。
//
// # 提交路径怎么用
//
// 提交落点构造 @@Commit@@（通常直接 @@Grant.Commit@@）并调用
// @@Fencer.Fence@@。**任何**错误都必须拒绝提交，不得降级为放行
// （fail-closed——权威不可达时「放行」等于分区期间双主）。判据次序：
// 代次回退（ErrStaleEpoch）→ 代次超前（ErrEpochUnconfirmed）→ 区域
// （ErrNotOwner）→ 令牌（ErrTokenMismatch）。
//
// 重要：Fence 是**判据**不是事务。判据与提交必须落在权威的同一次原子操作
// 里（参考实现用一把锁；宿主接共享存储时把 Judge 放进同一次条件写/CAS），
// 否则「判定通过」与「提交生效」之间仍有窗口——与 coord 的判定—提交窗口
// （issue #271）是同一类事实，不能靠调用顺序掩盖。
//
// # 与 taskboard / handoff 的关系（同一条纪律，不造第二份租约）
//
//   - Epoch 是 @@taskboard.Fence@@ / @@handoff.Epoch@@ 在**区域维度**的
//     投影：同一条「代次严格递增 + 旧代凭令牌被拒」纪律，作用域从任务/
//     交接单提升到任务域；
//   - 令牌由权威**铸造**，调用方不自行生成（taskboard v0.33.0 的教训：
//     调用方能造令牌，「凭令牌认人」就名存实亡）；
//   - 本包不新增时限面：@@Grant.Token@@ 是所有权能力凭据，不是租约令牌。
//
// # 分区语义与部署形态
//
// 分区期间旧区域仍持旧代次，它唯一能做的事是「提交」——而提交路径的
// Fence 要么报旧代次（权威已换代），要么因为权威不可达而失败（调用方按
// 拒绝处理）。两种落点都不会让它成为第二权威。
//
//   - **单区域**：@@NewLocal@@——本区域即权威，任意任务域 epoch 1；
//     转移显式报 @@ErrSingleRegion@@。没有共享权威依赖，行为与「没有跨域
//     所有权」逐条一致，但提交路径的判据形状不变：将来换成共享权威不用
//     改调用方；
//   - **多区域**：宿主实现 @@Authority@@ 落在共享权威存储（同一份可写
//     真相——与 controlplane 的权威统一同一条纪律），或用
//     @@MemoryAuthority@@ 跑契约用例。参考实现不承诺跨进程原子性；
//   - **顺序纪律**：先共享权威任务域，再多自治域 Handoff。本包只给机制面，
//     不替宿主选拓扑。
package ownership

import (
	"context"

	"github.com/oekit/pkg/errors"
)

// Region 区域标识（开放键：内核不解释部署拓扑，只把非空值当作权威持有者）。
type Region string

// Domain 任务域标识（开放键：内核不解释业务语义，只把它当作所有权作用域）。
type Domain string

// Epoch 所有权代次：同一任务域上每次**确认的**转移严格 +1。
//
// 它是防 ABA 的判据，也是下游栅栏的代次来源：旧代提交一律拒绝；
// 代次不复用、不跳号（候选代次恒为当前代次 + 1）。
type Epoch uint64

// TransferID 一次转移提案的标识（权威铸造）。
type TransferID string

// Grant 一次所有权授权（权威铸造的能力凭据）。
//
// 它不是租约：没有到期时刻，也不会随时间自动失效。任务租约仍由
// taskboard.Lease / handoff.Ticket.TTL 管（见包文档「不造第二份租约」）。
type Grant struct {
	// Domain 任务域。
	Domain Domain
	// Region 权威区域。
	Region Region
	// Epoch 所有权代次（同一域上严格递增）。
	Epoch Epoch
	// Token 授权令牌（权威铸造；与 Epoch 同一次转移/授予铸造）。
	//
	// 提交路径凭它认人，不凭「区域名」——区域名是公开事实，令牌不是。
	Token string
}

// Commit 把授权投影成提交声明（两边的结构必须同型——新加一列时编译期就红，
// 不会出现「新字段忘了投影」的静默漏判；调用方也不用手抄令牌与代次）。
func (g Grant) Commit() Commit {
	return Commit(g)
}

// Commit 一次提交携带的所有权声明（提交落点用它调用 @@Fencer.Fence@@）。
type Commit struct {
	// Domain 提交所属任务域。
	Domain Domain
	// Region 提交方声称的权威区域。
	Region Region
	// Epoch 提交方依据的所有权代次。
	Epoch Epoch
	// Token 提交方持有的授权令牌。
	Token string
}

// Transfer 两阶段转移的提案（第一阶段产物；**不是**授权）。
//
// 提案存在期间当前权威不变；只有 @@ConfirmTransfer@@ 成功后才换代。
type Transfer struct {
	// ID 提案标识（确认时定位提案）。
	ID TransferID
	// Domain 任务域。
	Domain Domain
	// From 发起转移的当前权威区域。
	From Region
	// To 目标区域（确认后成为新权威）。
	To Region
	// Candidate 候选代次（= 发起时的当前代次 + 1；确认前不可提交）。
	Candidate Epoch
	// Token 确认回执令牌（权威铸造，只应交付给目标区域）。
	//
	// 它与授权令牌不是一回事：确认成功后权威会**另铸**新的授权令牌。
	Token string
}

// TransferRequest 发起转移的请求（第一阶段）。
type TransferRequest struct {
	// Domain 任务域。
	Domain Domain
	// From 发起方声称的当前权威区域。
	From Region
	// Token From 当前授权的令牌（凭令牌认人，不凭区域名）。
	Token string
	// To 目标区域（不得为空，也不得等于当前权威区域）。
	To Region
}

// ConfirmRequest 确认转移的请求（第二阶段回执）。
type ConfirmRequest struct {
	// ID 提案标识。
	ID TransferID
	// Token @@Transfer.Token@@ 的原样值（回执凭据）。
	Token string
	// By 确认方（必须是提案的目标区域）。
	By Region
}

// Fencer 提交路径的所有权栅栏（**可插拔接缝**）。
//
// 单区域部署用 @@Local@@；多区域部署接宿主实现的共享权威（或用
// @@MemoryAuthority@@ 跑契约）。提交落点只依赖本接口，不依赖具体实现。
//
// 实现方必须保证（与 taskboard.Board 的实现义务同构）：
//
//   - 判定与提交在**同一次原子操作**里完成，或显式声明判定—提交窗口；
//   - 任何错误都不得被解释成放行（fail-closed）；
//   - 代次只增不减、令牌由权威铸造（调用方不自行生成）。
type Fencer interface {
	// Fence 校验一次提交的所有权声明；nil = 放行，任何错误 = 拒绝。
	Fence(ctx context.Context, c Commit) error
}

// Authority 所有权权威的管理面 + 提交栅栏。
//
// 实现方必须保证：
//
//   - @@Owner@@ / @@Mapping@@ 返回**一致快照**（不是边改边读的流）；
//   - @@BeginTransfer@@ 不改变当前授权；候选代次恒为当前代次 + 1；
//   - @@ConfirmTransfer@@ 的成功是一次原子换代（代次提升 + 区域切换 +
//     令牌重铸不可分割）；失败不得留下半代状态；
//   - 确认回执对**最近一次已确认提案**幂等；未知/被取代的提案一律
//     @@ErrNoTransfer@@，不得凭令牌猜测复活；
//   - @@Fence@@ 的判定逻辑复用 @@Judge@@（第二份判据必然漂移）。
type Authority interface {
	Fencer
	// Owner 返回任务域当前的权威授权；未知任务域 ⇒ ErrUnknownDomain。
	Owner(ctx context.Context, domain Domain) (Grant, error)
	// Mapping 返回当前区域权威映射的一致快照（可查事实）。
	Mapping(ctx context.Context) (RegionMap, error)
	// BeginTransfer 发起转移（第一阶段）：当前权威不变，返回带候选代次
	// 与回执令牌的提案。
	BeginTransfer(ctx context.Context, req TransferRequest) (Transfer, error)
	// ConfirmTransfer 确认转移（第二阶段）：目标区域凭回执令牌确认，
	// 成功后换代并返回新授权。
	ConfirmTransfer(ctx context.Context, req ConfirmRequest) (Grant, error)
}

// 错误哨兵（宿主实现必须复用，不要各造一套：判定方按 errors.Is 认人）。
var (
	// ErrUnknownDomain 任务域没有权威映射（未配置、拼错，或已移除）。
	ErrUnknownDomain = errors.New("ownership: 任务域没有权威映射")
	// ErrNotOwner 出示的区域不是该任务域的当前权威区域（代次相同）。
	ErrNotOwner = errors.New("ownership: 区域不是该任务域的权威区域")
	// ErrStaleEpoch 出示的代次低于当前代次（旧代提交 / 回退）。
	ErrStaleEpoch = errors.New("ownership: 所有权代次已回退（旧代不得提交）")
	// ErrEpochUnconfirmed 出示的代次高于当前代次（候选代次尚未确认）。
	ErrEpochUnconfirmed = errors.New("ownership: 所有权代次尚未确认（候选代次不得提交）")
	// ErrTokenMismatch 授权令牌不匹配（凭令牌认人，不凭区域名）。
	ErrTokenMismatch = errors.New("ownership: 所有权令牌不匹配")
	// ErrNoTransfer 转移提案不存在或已被新提案取代。
	ErrNoTransfer = errors.New("ownership: 转移提案不存在或已被取代")
	// ErrNotTarget 确认方不是提案的目标区域。
	ErrNotTarget = errors.New("ownership: 确认方不是转移的目标区域")
	// ErrBadTransfer 转移请求自身不合法（空目标 / 目标等于当前权威区域）。
	ErrBadTransfer = errors.New("ownership: 转移请求不合法")
	// ErrSingleRegion 单区域本地权威不支持跨区域转移。
	ErrSingleRegion = errors.New("ownership: 单区域权威不支持跨区域转移")
	// ErrBadMapping 区域权威映射不合法（空映射 / 空任务域 / 空区域）。
	ErrBadMapping = errors.New("ownership: 区域权威映射不合法")
)

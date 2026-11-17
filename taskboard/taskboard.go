// Package taskboard 多 agent 协调面的**存储中立契约 + 一致性判据**。
//
// # 它补的是哪一段
//
// @dispatch@ 只回答「能不能派」。派出去之后的三件事——谁负责哪个子任务、
// 它依赖谁、怎么把认领还回去——此前**没有任何机制面**：各宿主自己拿一张
// 表或一个 map 兜着，于是「同一个子任务被两个 agent 同时认领」这种错在
// 报告里读起来完全正常（两条分支都干完了活，各自都以为自己是负责人）。
//
// # 本仓给什么、不给什么
//
// **给**：① 存储中立的接口（@@Board@@）；② 快照的**不变量判据**（@@Check@@，
// 纯函数，宿主可以随时拿当前快照跑一遍）；③ 并发正确性的**可跑断言**
// （@@ConcurrentClaim@@，判据本身而不是测试风格）；④ 可选的**批量领取面**
// （@@ReadyBoard@@.ClaimReady，issue #189：存储侧一次领取至多 n 个就绪
// 任务，单条 Claim 保持默认与语义不变）。
//
// **不给**：存储实现。表结构、事务边界、持久化形态是宿主资产——本仓给一份
// 参考实现，宿主就会照着改它，最后两份真相并存。这与 @checkpoint.StoreCAS@
// 的分工完全同构：契约与判据归内核，落库归宿主。
//
// # 为什么「认领」必须是原子的
//
// 认领是**读-判-写**：读当前状态、判依赖是否完成、写 owner。三段分开做，
// 两个 agent 会读到同一个「open」，各自判定自己可以认领，然后先后写入——
// 两个赢家。这与 @@commitgate@@ 的「两条并行分支都赢了」是同一种失效，
// 而这里的代价更直接：同一份工作被做两遍，或者两份互相覆盖的结果被当成一份。
//
// 故契约要求 @@Board.Claim@@ **由存储实现保证原子性**，本包只判结果
// （并发跑 N 次，成功者必须恰好 1 个）。
//
// # 释放认领凭令牌，不凭内容
//
// 与 @@commitgate@@ 的教训同源：用「任务 id + agent 名」认人判的是
// 「内容相同」，不是「同一次占用」。同名 agent（并行分支常同名）或
// 「失败 → 释放 → 重试」这条推荐路径都会撞上，于是输家放掉赢家的闸、
// 上一轮的迟到释放放掉新一轮的闸。故 @Lease@ 里的 token 由存储**铸造**，
// @@Release@@ / @@Complete@@ 只认 token。
//
// # 令牌必须由存储铸造（v0.33.0 起）
//
// 此前 @Claim@ 收的是调用方自带的 @Lease@（含 token）——契约写着
// 「token 由存储铸造」，实现却让调用方造 token。两者对不上时，**实现赢**：
// 调用方能造出任意 token，于是「输家的迟到释放放掉赢家的闸」这条失效形态
// 又回来了，而报告里一切正常。
//
// 故 @Claim@ 改收 @ClaimRequest@（只说「要多长的租约」），token 由存储铸造
// 并随返回的 @@Task@ 给出。契约与实现从此一致，且不可能被调用方绕过。
//
// # 续期与接管代次（v0.33.0 起，详见 renew.go）
//
// 长任务没有续期手段就必然被接管；续期走 @@RenewingBoard.Renew@@——
// 它是**资格检查**不是心跳：校验令牌仍是最新代，同时把到期时刻推后。
// 接管代次 @@Fence@@ 与令牌**同源**（同一次认领的同一原子操作铸造），
// 同一任务上严格递增；旧持有者迟到的提交凭「令牌不匹配 ⇒ @@ErrLeaseLost@@」
// 必然被拒，且不波及新持有者。
//
// # 多租户（issue #195）
//
// 任务身份是 @@(Tenant, ID)@@：同一 ID 在不同租户下是两条互不相干的任务。
// @@Task.Tenant@@ 是开放键（内核不解释语义）；**空租户 = 默认/单租户命名
// 空间**，此时身份退化为 ID，旧行为逐字不变。
//
// 支持租户的实现必须同时满足以下条款（单租户实现保持空租户即可，不受影响）：
//
//   - **键由实现按租户作用域生成**，调用方不把租户拼进 TaskID——调用方
//     自由拼键时，租户段与 id 段的分界就成了第二个约定，撞键是必然；
//   - 租户上下文经 ctx 传递（见 tenant 包的传递契约）——它仍是机制间的
//     唯一通道：不另设全局变量、不把租户挂到 Board 结构体上；
//   - 作用域内 @@List@@ 不含他租户任务；@@Claim@@ / @@Release@@ /
//     @@Complete@@ / @@Renew@@ 对他租户任务一律 @@ErrNotFound@@（不可见 =
//     不存在，不泄露存在性）——跨租户领取与收场必须被拒；
//   - 实现声明「本板必须带租户上下文」时，缺租户 fail-closed
//     （@@tenant.ErrNoContext@@），与写路径入口同一口径；空租户与缺租户
//     不是一回事：前者是默认命名空间，后者是拒绝。
//
// @@Check@@ 接受混合租户快照：按 (Tenant, ID) 判重，依赖只在同租户内解析
// （跨租户依赖按不存在处理——租户之间本来就不可见）。详见 check.go。
package taskboard

import (
	"context"
	"slices"
	"time"

	"github.com/oekit/pkg/errors"
)

// TaskID 子任务标识。
type TaskID string

// AgentID 执行者标识。
//
// **允许重复**（并行分支常同名）——这正是「凭 token 不凭内容」的理由。
type AgentID string

// State 任务状态。
type State string

const (
	// StateOpen 待认领。
	StateOpen State = "open"
	// StateClaimed 已被认领（owner + lease 必须有值）。
	StateClaimed State = "claimed"
	// StateDone 已完成（终态）。
	StateDone State = "done"
	// StateFailed 已失败（**非终态**——允许重新认领：失败的工作通常要重做）。
	StateFailed State = "failed"
)

// Terminal 报告该状态是否**不可再被认领**。
//
// 只有 done 是终态：failed 的任务必须能重做，否则一次失败就把子任务永久钉死，
// 而宿主的补救路径（重新派一个人）会静默失效。
//
// **failed 在词表上非终态，这是刻意的**（见 StateFailed 的常量注释，issue #375）：
// 常量注释曾写「终态」，与本函数相反；两处现在逐字对齐。体检面同样按本函数
// 判：failed + 残留 owner 不报 CodeTerminalClaimed，failed 也不算「依赖已完成」。
func (s State) Terminal() bool { return s == StateDone }

// Valid 报告状态是否在内核词表内。
//
// # 为什么必须导出（issue #323）
//
// 词表外状态代表「内核读不懂这个状态」：体检面（Check）会报
// CodeUnknownState，但写路径此前只判 Terminal()（未知状态不是终态）⇒
// 同一个状态「体检说非法、Claim 说可领」。宿主需要能自己区分
// 「状态合法且非终态」与「状态不在词表内」——Valid() 就是这个判据。
//
// Terminal() 对词表外状态仍返回 false（不能把「读不懂」说成「终态」），
// 所以写路径必须显式消费 Valid()，不能靠 Terminal() 反推。
func (s State) Valid() bool {
	switch s {
	case StateOpen, StateClaimed, StateDone, StateFailed:
		return true
	}
	return false
}

// Lease 一次认领的凭证。
type Lease struct {
	// Token 由存储**铸造**的唯一令牌（释放与完成的唯一凭据）。
	//
	// 它**不由调用方提供**——见 ClaimRequest 的注释：调用方能造令牌，
	// 「凭令牌认人」就名存实亡。
	Token string
	// Fence 接管代次（栅栏令牌）：与 Token **同一次原子操作**铸造，同一任务上
	// 每次认领成功严格递增；续期**不**递增（续期不是接管）。
	//
	// 判据与下游栅栏的用法见 Fence 与 RenewingBoard 的契约说明（renew.go）。
	Fence Fence
	// Until 租约到期时刻（零值 = 不限期，由宿主自担）。
	//
	// 到期后其他 agent 可以接管：进程崩掉时没有它会留下一把永久占用。
	Until time.Time
}

// LeaseHeld 报告任务当前是否仍被**活持有**（issue #335 的单点判据）。
//
// 判定 = 认领态 **且** 租约未过期（Until 零值 = 不限期，按在持判——当成
// 已过期会让「两个执行者同时认领」变成「两个赢家」）。
//
// 为什么收敛成导出函数：此前 coord / examples 的两个参考实现 / controlplane
// 各写一份等价判据，而 controlplane.MemStore.Claim **压根没判过期**——
// 持有者进程崩掉后任务被永久占用，与 Board.Claim 的「被别人**活着**持有」
// 契约方向相反。存储实现的 Claim 必须调用本函数，不得各写一份（第二份
// 判据必然漂移）。
func LeaseHeld(t Task, now time.Time) bool {
	if t.State != StateClaimed {
		return false
	}
	if t.Lease.Until.IsZero() {
		return true
	}
	return t.Lease.Until.After(now)
}

// IsZero 报告租约是否为全零值（没有残留任何一列）。
//
// 判定收敛在此处而不是散落各处（issue #220）：快照体检要判「非认领态是否
// 残留租约」，漏一列（Until）就让「体检绿」与运行期行为分叉——今后再加
// 字段时，这里是一处改、处处跟的单点。
func (l Lease) IsZero() bool {
	return l.Token == "" && l.Fence == 0 && l.Until.IsZero()
}

// ClaimRequest 认领请求：调用方给出**想要多长的租约**，不给出令牌。
//
// # 为什么请求里没有令牌
//
// 令牌必须由**存储**铸造。调用方自带令牌时，「释放/完成只认 token」
// 这条契约就名存实亡：调用方能造出任意 token，于是
// 「同名 agent 的输家迟到释放放掉赢家的闸」这条失效形态又回来了
// （commitgate 同源教训：凭令牌释放、不认内容）。
//
// # 为什么给的是「多长」而不是「到什么时候」
//
// 到期时刻归存储算：调用方的时钟与存储的时钟可能对不上，跨进程时更不能
// 假设一致。给 TTL，存储按**自己的**当前时刻推算，两边不会各算一套。
type ClaimRequest struct {
	// TTL 本次认领的租约有效期。
	//
	// 正数 ⇒ 存储按「自己的当前时刻 + TTL」算出到期时刻。
	// 零值 ⇒ 不限期（到期时刻为零值，由宿主自担）——持有着崩了会留下一把
	// 永久占用，故宿主通常应给一个正的 TTL。
	TTL time.Duration
}

// Task 一个子任务。
//
// **不是可安全复制的值类型**：Deps 是切片，`t2 := t1` 只复制切片头，改写
// t2.Deps 的元素会改穿 t1（也不能用 == 比较——含切片的 struct 在 Go 里
// 不可比较）。跨边界交出前必须 Clone（等价于深拷贝 Deps）；Board 实现
// 返回快照时的拷贝义务写进了 Board 契约（issue #379）。
type Task struct {
	// Tenant 租户标识（开放键：内核不解释语义，只把非空值当作作用域）。
	//
	// 任务身份是 (Tenant, ID)：同一 ID 在不同租户下是两条互不相干的任务。
	// 空值 = 默认/单租户命名空间（旧行为：身份退化为 ID，存储键布局不变）。
	// 租户段的键由实现生成，调用方**不要**把租户拼进 ID（见包文档「多租户」）。
	Tenant string `json:",omitempty"`
	// ID 任务标识（在租户作用域内唯一）。
	ID TaskID
	// Title 展示用标题。
	Title string
	// Deps 依赖的任务 id（全部完成才可认领）。
	Deps []TaskID
	// State 当前状态。
	State State
	// Owner 当前认领者（仅 StateClaimed 时有值）。
	Owner AgentID
	// Lease 当前租约（仅 StateClaimed 时有值）。
	Lease Lease
	// Seq 单调递增序号（宿主用它做 CAS 与留痕；本包只要求它随变更增大）。
	Seq uint64
}

// Clone 返回任务的深拷贝：Deps 换一份底层数组，其余字段值语义直接复制。
//
// 用途是跨边界交出（Board 实现返回 List/Claim 快照、宿主把任务传出自己的
// 存储层）：仓库内三个实现各自手写了同款 `slices.Clone(t.Deps)`/等价的
// 逐条 append，这里给出**单一口径**，契约不再依赖每个实现各自记得写
// （issue #379）。
func (t Task) Clone() Task {
	t.Deps = slices.Clone(t.Deps)
	return t
}

// Board 协调面的存储中立契约。
//
// 实现方必须保证：
//
//   - @@Claim@@ **原子**：读-判-写不可分割（并发认领同一任务只能有一个赢家）；
//   - @@Release@@ / @@Complete@@ **只认 token**，且 token 不匹配时报
//     @@ErrLeaseLost@@（不许静默成功——静默成功会让输家以为自己的释放生效了）；
//   - @@List@@ 返回**一致快照**（不是边改边读的流）；
//   - @@List@@ / @@Claim@@ 返回的 @@Task@@ **不得与实现内部共享 Deps 的
//     底层数组**（与 checkpoint.Snapshot.Clone 同款拷贝义务）：Task 含
//     切片字段，按值返回只复制切片头，宿主照契约字面实现时改写返回值元素
//     会改穿存储内部的任务拓扑。实现可用 @@Task.Clone()@@ 收口这一义务。
//
// 支持多租户的实现还必须满足租户条款（见包文档「多租户」）：键按 ctx 的
// 租户作用域生成；他租户任务不可见、不可领、不可收场（一律 ErrNotFound）；
// 声明「必须带租户」时缺租户 fail-closed（tenant.ErrNoContext）。空租户与
// 缺租户不是一回事：前者是默认命名空间（旧布局），后者是拒绝。
type Board interface {
	// List 返回当前全部任务的快照（顺序不要求，判据会排序）。
	List(ctx context.Context) ([]Task, error)
	// Claim 认领一个任务。
	//
	// **令牌由存储铸造**并随返回的 Task 一起给出（Task.Lease.Token 与
	// Task.Lease.Fence 同一次原子操作铸造，Fence 在同一任务上严格递增）；
	// 调用方只给「要多长的租约」（ClaimRequest），不给令牌。
	//
	// 以下情形必须报错而不是「尽力而为」：不存在（ErrNotFound）/
	// 被别人活着持有（ErrTaken）/ 依赖未完成（ErrBlocked）/ 已是终态（ErrTerminal）。
	Claim(ctx context.Context, id TaskID, agent AgentID, req ClaimRequest) (Task, error)
	// Release 归还认领（凭 token）。
	Release(ctx context.Context, id TaskID, token string) error
	// Complete 标记完成（凭 token）。
	Complete(ctx context.Context, id TaskID, token string) error
}

// 错误哨兵（宿主实现必须复用，不要各造一套：判定方按 As/Is 认人）。
var (
	// ErrNotFound 任务不存在。
	ErrNotFound = errors.New("taskboard: 任务不存在")
	// ErrTaken 任务被他人活着持有。
	ErrTaken = errors.New("taskboard: 任务已被他人认领")
	// ErrBlocked 依赖未完成。
	ErrBlocked = errors.New("taskboard: 依赖未完成")
	// ErrTerminal 任务已是终态。
	ErrTerminal = errors.New("taskboard: 任务已是终态")
	// ErrUnknownState 任务状态不在内核词表内（读不懂 ⇒ 写路径拒绝，不认领）。
	//
	// 与 Check 的 CodeUnknownState 同一事实、同一方向：体检说「这状态非法」，
	// 认领也必须拒绝——否则一条脏状态行会让副作用照常施加。
	ErrUnknownState = errors.New("taskboard: 任务状态不在词表内")
	// ErrLeaseLost 令牌不匹配（租约已易主或被释放过）。
	//
	// 旧持有者迟到的一切动作——Release / Complete / Renew——都用它拒收：
	// 接管必然铸造新令牌，令牌不匹配即说明你不是现任持有者。
	ErrLeaseLost = errors.New("taskboard: 租约已易主")
)

// Problem 一条快照不变量违反。
type Problem struct {
	// Tenant 相关任务的租户（空 = 默认/单租户命名空间）。
	Tenant string `json:",omitempty"`
	// Task 相关任务 id。
	Task TaskID
	// Code 机器可判的类别（宿主按它分流与留痕）。
	Code string
	// Text 人读的说明。
	Text string
}

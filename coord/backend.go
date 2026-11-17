// backend.go —— 协调后端的最小原语契约（适配形态约定的代码面）。
//
// # 为什么是这四个原语，而不是更多
//
// 把 taskboard.Board 与 sideeffect.Store 的判定逻辑推到底之后，
// 后端真正要提供的只有四件事：
//
//  1. **快照读**：一次取多键的一致视图（判定所需的全部状态，同一瞬间）；
//  2. **条件事务**：全部断言成立才提交全部写入，否则一个都不执行
//     （「一次原子操作」的通用形态——Redis 的 Lua、etcd 的 txn、
//     PG 的事务条件更新都是它的实例）;
//  3. **前缀扫描**：一次原子操作取回前缀下**全部键与值**（issue #182——
//     全量视图类操作的读面。「列键 + 逐键快照」两步拼接不是一致快照，
//     两步之间新增/删除的任务既不出现在结果里也不报错）；
//  4. **前缀列键**：只列键名（宿主自己的枚举需要；需要「键+值」同刻视图
//     一律走 ScanSnapshot，不要 Keys+Snapshot 两步拼）。
//
// 少一个：没有快照读就得逐键读，一致性窗口从「一次」变成「N 次」；
// 没有条件事务就没有原子提交，三判合一必然退化成三步三写；
// 没有前缀扫描，全量列举就得「列键 + 快照」两步拼——那是一致性窗口
// 被「两次调用」重新打开（#182 的缺陷本体）。
// 多一个：锁、watch、租约原语都不需要——那些语义已经由适配器在原语之上
// 组合实现，后端多给只会引入第二份真相。
//
// # 版本（Version）是断言的锚点
//
// 条件事务的断言形态刻意取「版本相等」而不是「值相等」：版本由后端在
// **每次成功写入时 +1**（单调不回退），等值断言等价于「自我上次快照后
// 这个键没被动过」。值相等断言在 ABA 场景（改走又改回）会误判「没动过」，
// 版本断言不会——这与 commitgate「凭令牌认占用」是同一条纪律：
// 用变更的次数而不是变更后的内容认变更。
package coord

import (
	"context"
	"time"
)

// Version 键版本：每次成功写入 +1，单调不回退，由后端保证。
//
// 「成功写入」**含删除**（issue #181）：Del 同样推进版本——版本是断言的
// 锚点，随条目一起删掉就是把它拆了。实现可保留墓碑条目（Exists=false +
// 版本），也可用全局修订号/单调 CAS 序号做锚点；但下面这条**没有实现
// 自由**：删除后同名键重建，拿删除前（或重建前任何历史）版本做的断言
// 必须失败——「写 v → 删 → 重建 → 旧版本 CAS 必失败」由
// @@VersionNotReused@@ 判据钉住，宿主换 Redis/etcd/PG 跑同一套。
type Version uint64

// VerMissing 从未写过的键的版本（0；新键首写后从 1 起）。
//
// 只表示「从未写过」：被删除过的键携带墓碑版本（> VerMissing）且
// Exists=false——对它断言 VerMissing 不成立。这是刻意为之（ABA 防线）：
// 「快照时没有、提交时之前被人写过又删了」正是断言要拦住的窗口。
const VerMissing Version = 0

// Entry 一次快照读到的键状态。
type Entry struct {
	// Key 键名。
	Key string
	// Ver 当前版本（从未写过的键为 VerMissing；被删除过的键为墓碑
	// 版本，随每次写入与删除推进、不回退不复用）。
	Ver Version
	// Val 当前值（键不存在时为 nil）。后端不得复用底层字节——
	// 返回的切片必须是快照那一刻的独立拷贝。
	Val []byte
	// Exists 键是否存在（墓碑键 Exists=false，但 Ver 仍可读）。
	Exists bool
}

// Assert 条件事务的断言：键的当前版本必须等于 Ver。
//
// Ver = VerMissing 即断言「键从未写过」（防「快照时没有、提交时被别人
// 建了」的窗口）；要断言「此刻不存在」，断言快照里读到的那个版本即可
// （墓碑键快照返回 Exists=false + 墓碑版本，原样列进断言就是「自我上次
// 快照后这个键没被动过」）。断言的键与被写的键**必须同批提交**：断言
// 只护自己列出的键，没列出的键在快照与提交之间怎么变都不管——适配器要
// 把判定读过的键全部列进断言，少列一个就是少护一扇窗。
type Assert struct {
	Key string
	Ver Version
}

// Mutation 条件事务的写入：Set（Del=false）或删除（Del=true）。
type Mutation struct {
	Key string
	Val []byte
	Del bool
}

// ClockSource 权威时间原语（issue #194）：租约/占用的「到期时刻」由**存储
// 自己的时钟**算——调用方的时钟与存储的时钟可能对不上，跨进程时更不能
// 假设一致。这是 examples/pgstore 的硬纪律（「到期由存储时钟算」）在
// Backend 这个更通用的接缝上的同一落法：多副本下两个节点时钟差 30 秒，
// 用节点时钟判「租约已过期」就是让第二个人在第一个人还在跑时认领成功。
//
// 实现形态示例：Redis → Lua 脚本内 TIME；PostgreSQL → 事务内
// clock_timestamp()（examples/pgstore 即此形态）；etcd → 以 revision 或
// 专门的存储侧时间键为锚。拿不出权威时间的后端见 SingleMachine。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：resolveNow 在**不持适配器锁**的位置调用本接口（nowAt 取
// 回调在锁内、调用在锁外）。
// no-blocking：宿主实现内不得无界等待——它在认领 / 续期的重判循环里被
// 同步调用（每次重判都重取时刻），阻塞会拖住整条判定路径。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（resolveNow
// 直接调 Now，无 recover），宿主实现须自行保证不 panic。
type ClockSource interface {
	// Now 存储侧当前时刻。
	Now(ctx context.Context) (time.Time, error)
}

// SingleMachine 仅单机声明：拿不出权威时间的后端在**装配期**显式声明
// 「只在单进程内使用」，适配器才允许用节点本地时钟判到期——单进程内只有
// 一个时钟，不存在跨进程不一致。不声明也不实现 ClockSource 的后端
// **拒绝装配**（@@ErrNoTimeAuthority@@），不静默退化成节点时钟。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：本接口只在**装配期**被调用（hasTimeAuthority），不持内核
// 锁、不在热路径上。
// no-blocking：宿主实现内不得无界等待——装配期同步调用，阻塞即拖住装配。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（装配期调用，
// panic 会掀翻装配），宿主实现须自行保证不 panic。
type SingleMachine interface {
	// SingleMachine 报告后端是否只在单进程内使用。
	SingleMachine() bool
}

// hasTimeAuthority 装配期检查（issue #194 判据）：后端要么实现 ClockSource
// （到期由存储时钟算），要么显式声明 SingleMachine。两者皆无 ⇒ 拒绝装配。
func hasTimeAuthority(b Backend) bool {
	if _, ok := b.(ClockSource); ok {
		return true
	}
	sm, ok := b.(SingleMachine)
	return ok && sm.SingleMachine()
}

// resolveNow 判定用当前时刻的时间权威次序（issue #194）：
//
//  1. UseClock 注入——boardtest/sidetest 契约套件的过期场景专用，**生产
//     路径不得注入本地时钟**（租约到期是跨进程共享的事实，时间真相在存储）；
//  2. ClockSource——存储时钟（到期由存储算，与 examples/pgstore 同一口径）；
//  3. 仅单机声明——节点时钟（装配期已显式声明，不是静默退化）。
//
// 只在装配期检查（hasTimeAuthority）通过后才可达：步骤 3 的 time.Now 是
// 「声明过仅单机」的那条路，不是缺省。
func resolveNow(b Backend, injected func() time.Time, ctx context.Context) (time.Time, error) {
	if injected != nil {
		return injected(), nil
	}
	if cs, ok := b.(ClockSource); ok {
		return cs.Now(ctx)
	}
	return time.Now(), nil
}

// AtomicDecision 后端的**原子判定**接缝（issue #271，可选能力）。
//
// 普通 Backend 的判定路径是「先读存储时刻 → 快照 → 判定 → 条件事务」：
// 读时刻与提交是两次后端交互，中间有时间窗口。窗口内租约可能已经到期，
// 而本副本仍按旧快照把续期提交成功——这不是「两个真相」，是「承诺的
// 时间上界说不清」。
//
// 实现本接口 = 承诺下面这件事在**一次存储事务内**完成：
//
//	取存储时刻 now → 交给 decide 读快照并给出断言/写入 → 条件提交
//
// decide 收到的 now 就是该事务的提交时刻（实现必须取事务内时间，
// 例如 PostgreSQL 的 clock_timestamp()、Redis Lua 的 TIME），read 返回的
// 快照必须属于同一事务。断言失败返回 applied=false（正常结论，什么都没写）；
// error 只表示基础设施故障。
//
// 实现了本接口的后端，租约判定与提交的窗口为 0（见 LeasePrecision）；
// 未实现的后端走误差预算降级路径（见 DefaultLeaseErrorBudget）。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：内核交给宿主的 decide 闭包在**宿主事务内**运行 ⇒ 闭包内
// 不得回查本包的 Backend（不得在事务内再发一次后端交互）。内核侧兑现：
// 闭包只调宿主给的 read 与内核自己的 plan，不回查 Backend；宿主侧要求：
// read 提供的快照必须属于同一事务。
// no-blocking：decide 闭包在宿主事务内同步运行 ⇒ 闭包内不得无界等待
// （不得做 I/O / 等锁 / 等其它回调）。内核侧兑现：plan 是纯判定。
// panic-isolated：**宿主侧兑现**——闭包内的 panic 会逃进宿主的事务实现，
// 内核不设 recover；宿主必须自行隔离，否则事务半途 panic、闸状态不明。
type AtomicDecision interface {
	// DecideTx 在一次事务内取存储时刻、读快照、执行判定并条件提交。
	DecideTx(ctx context.Context, decide func(now time.Time, read func(keys ...string) (map[string]Entry, error)) (asserts []Assert, mutations []Mutation, err error)) (applied bool, at time.Time, err error)
}

// LeaseBudget 后端可声明的**判定—提交窗口预算**（issue #271，可选能力）。
//
// 未实现时适配器按 DefaultLeaseErrorBudget 承诺。实现本接口的后端应当
// 按自己的真实事务往返（含存储时钟读取）声明上界；返回值 <= 0 视作未声明。
// 只能声明、不能验证——实际窗口由 LeaseVerdict 在每次判定后观测。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：本接口是**只读声明**，在装配期与判定前被读取，不持内核锁。
// no-blocking：宿主实现内不得无界等待——读取是纯读数，判定路径同步等它。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic，宿主实现须
// 自行保证不 panic。
type LeaseBudget interface {
	LeaseErrorBudget() time.Duration
}

// Backend 协调后端的最小原语契约。
//
// 实现方必须保证（这四条是「接上协调契约」的全部硬要求）：
//
//   - Txn **原子**：全部 Assert 成立时执行全部 Mutation 并把被写键的版本
//     各 +1；任一断言不成立时**一个 Mutation 都不执行**（返回 false）；
//   - Snapshot **一致**：返回的是同一瞬间的视图（后端自己的锁/事务/脚本内）；
//   - ScanSnapshot **一致且原子**：键集与值来自**同一次原子操作**（issue #182
//     ——「列键 + 快照」两步之间新增/删除的键既不进结果也不报错，那是
//     跨两个时刻的拼接，不是快照）；
//   - Version **单调**：同一键的版本随每次成功写入严格 +1，不回退、不复用
//     ——写入含 Del（删除同样推进版本，见 Version）。
//
// 典型实现形态见包文档的「形态约定」节。跨进程原子性由实现方的存储保证
// ——本包的内存后端只在单进程内成立，不得上生产。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁），
// 也不得在一次调用内自锁（如连接池耗尽时等待自己）。内核侧兑现：适配器
// 在**不持适配器锁**的位置调用本接口（casLoop 只用断言护窗口）。
// no-blocking：宿主实现内不得无界等待——每次重判都是一次后端往返，内核
// 不设超时，阻塞会拖住整条判定路径。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（coord 包内
// 无 recover），宿主实现须自行保证不 panic。
type Backend interface {
	// Snapshot 一致快照读：返回这些键的当前状态。
	// 同一个键重复出现按一次计；不存在的键返回 Exists=false 的条目。
	Snapshot(ctx context.Context, keys ...string) (map[string]Entry, error)
	// Txn 一次原子操作：全部 Assert 成立才执行全部 Mutation。
	//
	// 返回 applied=false 表示有断言不成立（什么都没发生）——这是**正常结论**
	// 不是错误：CAS 循环的调用方取新快照重判即可。error 只表示基础设施故障。
	Txn(ctx context.Context, asserts []Assert, mutations []Mutation) (applied bool, err error)
	// ScanSnapshot 前缀扫描：一次原子操作取回前缀下全部**存活键**的键与值
	// （issue #182，全量视图类操作的读面——BoardAdapter.List 用它取「同刻
	// 全集」）。墓碑键（已删除）不返回——与 Keys 的可见性同一口径。
	//
	// 「原子」= 键集与值出自同一瞬间（后端自己的锁/事务/脚本内），调用方
	// 拿到的 map 就是那一个时刻的完整视图，不存在「先看到的键在取值时已被删」
	// 的拼接缝。每次成功写入（含删除）推进版本的同一条纪律在这里同样成立：
	// 扫描结果里每个条目的 Ver 都是扫描那一刻的版本。
	ScanSnapshot(ctx context.Context, prefix string) (map[string]Entry, error)
	// Keys 列出前缀下的全部键名。
	//
	// 注意（issue #182 后的口径修正）：本方法**只供宿主自己枚举键名**。
	// 「键名 + 值」的同刻视图必须走 ScanSnapshot——先 Keys 再 Snapshot 是
	// 两次独立调用，两次之间键集可以增删，拼出来的既不是全量也不是任一时刻
	// 的视图。适配器内不再这样拼（List 已收口到 ScanSnapshot）。
	Keys(ctx context.Context, prefix string) ([]string, error)
}

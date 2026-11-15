// backend.go —— 协调后端的最小原语契约（适配形态约定的代码面）。
//
// # 为什么是这三个原语，而不是更多
//
// 把 taskboard.Board 与 sideeffect.Store 的判定逻辑推到底之后，
// 后端真正要提供的只有三件事：
//
//  1. **快照读**：一次取多键的一致视图（判定所需的全部状态，同一瞬间）；
//  2. **条件事务**：全部断言成立才提交全部写入，否则一个都不执行
//     （「一次原子操作」的通用形态——Redis 的 Lua、etcd 的 txn、
//     PG 的事务条件更新都是它的实例）;
//  3. **前缀列键**：按前缀枚举（List 类操作需要，快照语义由实现保证）。
//
// 少一个：没有快照读就得逐键读，一致性窗口从「一次」变成「N 次」；
// 没有条件事务就没有原子提交，三判合一必然退化成三步三写。
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
//
// **删除推进版本**（issue #181）：Del 也是一次成功写入——条目转为墓碑
// （Exists=false、版本 +1 并保留、值清空），不是把版本一起删掉。版本若随
// 条目消失，同名键重建后从 VerMissing 重新 +1，旧代快照的断言 k@1 会在
// 「删 + 重建」后重新成立（ABA：旧代判定读上一代的值，提交写新一代的键）。
// 删除**从未存在**的键是空操作：没有代可灭，不创建墓碑、不消耗版本。
//
// 墓碑**不得随意 GC**：回收墓碑等于把该键的版本还给 VerMissing，同名键
// 重建后版本从 1 重来，ABA 窗口重新打开。宿主的 GC 许可条件是「能证明
// 没有任何在途快照的断言仍引用 ≤ 该墓碑版本的旧代」——实践上等于
// 「全局静默期（无在途 CAS 循环）才可回收」；最稳妥的形态是**不 GC**
// （本仓 MemoryBackend 与 examples/pgstore 的参考实现都不 GC）。
package coord

import "context"

// Version 键版本：每次成功写入 +1，单调不回退，由后端保证。
type Version uint64

// VerMissing 键**从未存在过**时的版本（0；新键首写后从 1 起）。
//
// 删除过的键不是 VerMissing：删除推进版本（墓碑保留版本），同名键重建从
// 墓碑版本之后继续——这是「版本不复用」在删除路径上的落法（issue #181）。
const VerMissing Version = 0

// Entry 一次快照读到的键状态。
type Entry struct {
	// Key 键名。
	Key string
	// Ver 当前版本（键从未存在过时为 VerMissing）。删除过的键**不是**
	// VerMissing：删除推进版本，条目处于墓碑态（Exists=false），Ver 为
	// 墓碑版本——同名键重建从它之后继续。
	Ver Version
	// Val 当前值（键不存在时为 nil）。后端不得复用底层字节——
	// 返回的切片必须是快照那一刻的独立拷贝。
	Val []byte
	// Exists 键是否存在。
	Exists bool
}

// Assert 条件事务的断言：键的当前版本必须等于 Ver。
//
// Ver = VerMissing 即断言「键从未存在过」（防「快照时没有、提交时被别人
// 建了」的窗口）。**墓碑上这条断言不成立**：删除过的键当前版本是墓碑版本，
// 不是 VerMissing——放行会让重建的键复用旧版本。断言的键与被写的键
// **必须同批提交**：断言只护自己列出的键，没列出的键在快照与提交之间
// 怎么变都不管——适配器要把判定读过的键全部列进断言，少列一个就是少护
// 一扇窗。
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

// Backend 协调后端的最小原语契约。
//
// 实现方必须保证（这三条是「接上协调契约」的全部硬要求）：
//
//   - Txn **原子**：全部 Assert 成立时执行全部 Mutation 并把被写键的版本
//     各 +1；任一断言不成立时**一个 Mutation 都不执行**（返回 false）；
//   - Snapshot **一致**：返回的是同一瞬间的视图（后端自己的锁/事务/脚本内）；
//   - Version **单调**：同一键的版本随每次成功写入严格 +1，不回退、不复用。
//     Del 也是成功写入（版本 +1、条目转墓碑：Exists=false、版本保留、
//     值清空）；Del 从未存在的键是空操作（不创建墓碑、不消耗版本）。
//     墓碑的 GC 许可条件见包文档的 Version 条款——默认不 GC。
//
// 实现方接上协调契约后，用 @@BackendContract@@ 跑一遍契约套件（判据里包含
// 「写 → 删 → 重建 → 旧版本 CAS 必须失败」的 ABA 负向用例）。
//
// 典型实现形态见包文档的「形态约定」节。跨进程原子性由实现方的存储保证
// ——本包的内存后端只在单进程内成立，不得上生产。
type Backend interface {
	// Snapshot 一致快照读：返回这些键的当前状态。
	// 同一个键重复出现按一次计；不存在的键返回 Exists=false 的条目。
	Snapshot(ctx context.Context, keys ...string) (map[string]Entry, error)
	// Txn 一次原子操作：全部 Assert 成立才执行全部 Mutation。
	//
	// 返回 applied=false 表示有断言不成立（什么都没发生）——这是**正常结论**
	// 不是错误：CAS 循环的调用方取新快照重判即可。error 只表示基础设施故障。
	Txn(ctx context.Context, asserts []Assert, mutations []Mutation) (applied bool, err error)
	// Keys 列出前缀下的全部键名（快照语义：与 Snapshot 同一一致性面）。
	Keys(ctx context.Context, prefix string) ([]string, error)
}

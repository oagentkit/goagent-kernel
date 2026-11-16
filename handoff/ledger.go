// ledger.go —— 交接单的**持久化 Store 面**（Save / Load / Delete）。
//
// 内存参考实现在 memory.go；语义实现在 desk.go。
//
// # 为什么要把「语义」与「持久化」分成两层
//
// 语义的 @@Store@@（Offer / Take / Reply / Cancel / Snapshot）回答「交接怎么走」；
// 本文件的 @@Ledger@@ 回答「记录落在哪」。两者分开的理由与 taskboard 的分工
// 同构：跨进程落库形态是宿主资产（表结构、事务边界、时钟域），内核不能替它
// 选；但交接的原子性、幂等键、回执寻址与代次又必须只有一份判据。故本包给
//
//   - Ledger：**持久化契约**——Save 的条件写、Load、Delete 与回执索引；
//   - MemoryLedger：进程内参考实现，只证契约可满足，不对跨进程做承诺；
//   - @@Desk@@：**语义实现**，只依赖 Ledger——两个 Desk 共用一个 Ledger
//     就是两个进程客户端的形状（用例以此模拟跨实例交接）。
//
// # Ownership Epoch：代次防 ABA
//
// 每个 Key 有一条**所有权代次水位**（Record.Epoch）。它在同一 Key 上严格递增，
// 且**跨终态与 Delete 都不复用**：Delete 只删当前记录，水位留在 Ledger 里。
// 于是「删掉重来」不会让一个旧代持有者重新变成合法持有者（ABA），迟到的
// Save / TakeAt / Reply 凭代次或令牌被拒。
//
// 代次是**存储侧铸造**的：Record.Epoch 为零值的 Save 由 Ledger 取
// 「水位 + 1」；调用方自带的更大代次不被接受（否则客户端可以把代次跳到
// 未来，让后面所有合法代次都变成「旧代」）。
//
// # Receipt addressing：回执按地址留存
//
// 回执送达的是 Ticket.ReplyTo 声明的地址，不是「谁交的回给谁」。Ledger 在
// Save 一条已收口记录时把回执按地址索引下来（Receipts(addr)），记录被
// Delete 后回执仍在——源方崩溃重启后仍能按自己的地址取回执。
//
// # 条件写（CAS）与并发
//
// Record.Revision 是记录版本，由 Ledger 铸造。更新一条**已存在**记录时，
// 调用方必须带回 Load 时看到的 Revision；版本不一致 ⇒ @@ErrConflict@@
// （并发写者已经改过，调用方重新 Load 后再判一次）。这不是可「盲重试」的
// 错误：Desk 必须重读并重新执行语义判定（例如输家要在新状态下读到 accepted
// 才能返回 @@ErrTaken@@，而不是把一切冲突都当输家）。
package handoff

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"
)

// Epoch 所有权代次（同一 Key 上严格递增；跨终态与删除不复用）。
//
// 它是防 ABA 的**单一真相**：@@Ticket.Generation@@ 是这个代次在交接单上的
// 投影，@Handle.Fence@ 是它在领取凭证上的投影——不是第二个计数器。
type Epoch uint64

// AddressedReceipt 一条已投递到回执地址的回执。
//
// 地址取 Ticket.ReplyTo 的**原样值**：本包不解析地址，只按相等索引。
type AddressedReceipt struct {
	// Key 交接单的幂等键。
	Key Key
	// Generation 回执所属代次（= 收口时刻的 Ownership Epoch）。
	Generation uint64
	// Address 回执地址（Ticket.ReplyTo 的原样值）。
	Address string
	// From 交接发起方。
	From Agent
	// To 实际领取方。
	To Agent
	// Receipt 回执内容。
	Receipt Receipt
	// ClosedAt 收口时刻（存储时钟口径）。
	ClosedAt time.Time
}

// Record 一条交接单的持久记录。
//
// 这是 Ledger 的存储单元，不是语义面：宿主实现 Ledger 时照它落库/序列化；
// 语义面仍只看 @@Ticket@@ / @@Receipt@@。
type Record struct {
	// Ticket 当前代的交接单。
	Ticket Ticket
	// Epoch 所有权代次（持久化真相；Ticket.Generation 是它的投影）。
	Epoch Epoch
	// Revision 记录版本（CAS；由 Ledger 铸造，调用方不自行递增）。
	Revision uint64
	// Token 领取令牌（accepted 时由 Ledger 铸造；非 accepted 必须为空）。
	Token string
	// Receipt 本次收口的回执（closed 时有值；Ledger 按 Address 索引留存）。
	Receipt *AddressedReceipt
}

// Ledger 交接单的持久化 Store 面。
//
// 实现方必须保证：
//
//   - @@Save@@ 是**一次原子操作**：版本比对、代次水位推进、令牌铸造与
//     回执索引不可分割（拆开就有「两位写者各写一半」的窗口）；
//   - @@Save@@ 只接受「同代更新」或「终态之上开新代」两种形态，旧代一律
//     @@ErrStaleEpoch@@（不以「看起来更小」为由静默接受）；
//   - @@Load@@ 无记录返回 @@ErrUnknownKey@@，不得返回 (零值, nil)；
//   - @@Delete@@ 只删当前记录，**保留代次与版本水位**（否则 ABA 封不住）；
//   - @@Receipts@@ 只返回发给该地址的回执（回执寻址的取件半边）。
type Ledger interface {
	// Save 写入一条记录并返回**存储定型后**的记录（代次 / 版本 / 令牌可能被铸造）。
	//
	// 形态一：新建（无当前记录）。Record.Epoch 必须为 0（由 Ledger 铸
	// 「水位 + 1」），Revision 必须为 0，Ticket.State 必须是 offered。
	//
	// 形态二：同代更新。Record.Epoch 等于当前记录的 Epoch，Record.Revision
	// 等于当前记录的 Revision；不匹配 ⇒ ErrConflict（并发写者已改）。
	//
	// 形态三：终态之上开新代。Record.Epoch 等于当前 Epoch + 1，Revision 为 0，
	// 当前记录必须是终态，新单必须是 offered。
	//
	// 其余形态（旧代、跨代跳跃、对非终态开新代、Revision 不匹配）分别报
	// ErrStaleEpoch / ErrConflict，**都不得写入**。
	Save(ctx context.Context, rec Record) (Record, error)
	// Load 读取当前记录；无记录返回 ErrUnknownKey。
	Load(ctx context.Context, key Key) (Record, error)
	// Delete 删除当前记录（保留代次与版本水位，防 ABA）。
	//
	// 这是保留/清理入口，不是交接动作：删除不会让旧代重新合法。
	Delete(ctx context.Context, key Key) error
	// List 返回全部当前记录的一致快照（顺序不要求，Desk 会排序）。
	List(ctx context.Context) ([]Record, error)
	// Receipts 返回发往 addr 的全部回执（按 Key、代次升序，确定性）。
	Receipts(ctx context.Context, addr string) ([]AddressedReceipt, error)
	// Epoch 返回该 Key 的当前代次水位（从未出现过的 Key 返回 0）。
	//
	// 它让调用方在记录被 Delete 之后仍能知道「下一代从几开始」；
	// 新建记录时传 Epoch=0 也可让 Ledger 自行铸造。
	Epoch(ctx context.Context, key Key) (Epoch, error)
}

// 持久化面的错误哨兵。
var (
	// ErrConflict 记录已被并发写者修改（Revision 不匹配）。
	//
	// 处置是**重新 Load 并按最新状态重新判定**，不是盲目重试同一次写入——
	// 并发 Take 的输家必须在最新状态下读到 accepted 才能返回 ErrTaken。
	ErrConflict = errors.New("handoff: 交接单已被并发修改（重新读取后再判）")
	// ErrStaleEpoch 所有权代次陈旧：旧代不得覆盖/领取新代。
	//
	// 它是防 ABA 的判据：Delete 之后重新挂出必须开更大的代次；迟到的旧代
	// 写入与旧代领取一律用本哨兵拒收。
	ErrStaleEpoch = errors.New("handoff: 所有权代次已陈旧（旧代不得覆盖新代）")
)

// ClockedLedger 可选能力：持久化实现能报**自己的**当前时刻。
//
// 截止时刻与过期判定必须按存储的时钟推算（跨进程不假设时钟一致）——Desk
// 在装配到这种 Ledger 上时读它；没有该能力时退回 Desk 的注入时钟（单进程
// 参考实现即满足，跨进程宿主应实现它）。
type ClockedLedger interface {
	Ledger
	Now(ctx context.Context) (time.Time, error)
}

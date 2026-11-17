// fence.go —— 进度提交的**接管代次判据**（issue #265）。
//
// # 它补的是哪一段
//
// StoreCAS 的序号单调判定挡得住「恢复点回退」，挡不住「过期持有者的迟到
// 提交」：旧持有者 A 的租约过期、任务易主给 B 之后，A 的快照若带着更大的
// 序号，SaveIfNewer 照样放行——「旧 Worker 提交了进度、新 Worker 才拥有
// 任务」的状态错位由此发生。单调判据判的是**快照之间**的关系，判不出
// **提交者还有没有资格**。
//
// # 判据归存储，不归进程
//
// 「提交前确认所有权」是 check-then-act：确认通过之后、写入落地之前，
// 租约仍可能易主——那个窗口只有**存储侧**判据关得住。与 sideeffect 的
// FencedSink（下游拒收陈旧写入）同一形态、同一条纪律：提交方出示本次
// 认领的接管代次（Fence），存储侧把「代次判定」与「写入」放进**同一次
// 原子操作**。
//
// 判定口径与 sideeffect.FencedSink 完全一致：
//
//   - 出示的代次 **小于** 已记录代次 ⇒ 拒（陈旧代次的迟到写入）；
//   - **等于** ⇒ 通过（同一代持有者的多次提交，如分批落库）；
//   - **大于** ⇒ 通过，并把已记录代次推进到它（接管后的首次提交把本 run
//     的进度身份绑定到新代——从此旧代的迟到写入全部被拒）。
//
// # 残余窗口（如实写明，不假装封死）
//
// 新持有者接管之后、它的**首次**带代次提交落地之前，旧代的写入仍会命中
// 「等于」分支放行——绑定发生在新持有者的首次提交，不在接管那一刻。编排
// 面（worker.ManagedExecutor）用「提交前确认所有权」把这道窗口压到次
// TTL 量级：旧持有者的资格确认（Renew）在租约过期后**必然**被拒，迟到
// 提交通常到不了存储。跨存储的完全封死需要两个存储共享一个事务边界，
// 那是宿主的接线资产，不是契约能替它承诺的。
package checkpoint

import (
	"context"

	"github.com/oekit/pkg/errors"
)

// ErrStaleFence 进度提交的接管代次已落后：本 run 的进度已归更新的持有
// 代次，迟到的写入被拒。
//
// 与 sideeffect.ErrFenceStale 同一判据、同一句话：**「你的写入被拒因为
// 代次陈旧」是同一件事**，不该让调用方分别判两套错误词表。哨兵分属两包
// 是因为两包各自定义自己的栅栏类型（与 Fence/Lease 各自成词的道理相同）。
var ErrStaleFence = errors.New("checkpoint: 接管代次已落后（本 run 已归新持有者的代次）")

// ErrFenceUnsupported 存储不具备栅栏判据：调用方显式要求带代次的条件
// 提交，存储没有相应能力。
//
// 这是显式失败，不是静默降级——降级成不带代次的提交等于把「过期持有者
// 可提交进度」的缺口伪装成通过（issue #265 要封的就是它）。
var ErrFenceUnsupported = errors.New("checkpoint: 存储不支持栅栏判据（未实现 FencedStoreCAS）")

// FencedStoreCAS 带**接管代次判定**的跨进程条件提交边界。
//
// 在 StoreCAS 的全部契约（序号单调、Seq==0 必拒、Load 语义）之上增加
// 提交资格判定。宿主实现它时，「代次判定」与「写入」必须在**同一次原子
// 操作**里完成——拆成「读代次 → 判 → 写」三步，两个进程可以同时读到
// 同一个旧值，栅栏就形同虚设（与 FencedSink 的原子性要求同一条）。
//
// 典型实现形态：进度记录多存一列 fence，一条带双条件的 UPDATE
// （WHERE seq < :seq AND fence <= :fence）。
//
// 接缝契约（宿主实现本接口；内核的 Committer 在提交路径上**同步**调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：**宿主侧兑现**。Committer 的存储往返在 SeqGate 的锁**之外**
// （闸只保护「取哪个序号」，调用发生在闸外）——回查不会自锁：宿主实现不得
// 在方法内回调本包的其它接缝（不得回调 Committer / SeqGate）——内核正同步
// 等着它返回，回查即互相等待。
// no-blocking：**宿主侧兑现**。SaveIfNewerFenced 可能要走网络，「不阻塞」在
// 此处的准确含义是「**有界**」——必须承接 ctx 的取消/超时并在期限内返回，
// 不得无限等待、不得在持存储内部锁时等待别的回调。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本接口的 panic（本包无
// recover，Committer 直接调存储），宿主实现须自行保证不 panic 或在实现内部
// recover 成 error；否则 panic 会掀翻提交方。
type FencedStoreCAS interface {
	// SaveIfNewerFenced 在 SaveIfNewer 的全部契约之上增加代次判定：
	//
	//  1. 该 run 已记录代次 > fence ⇒ ErrStaleFence，**不得写入**
	//     （陈旧代次的迟到写入）；
	//  2. 已记录代次 == fence ⇒ 通过（同一代持有者的多次提交）；
	//  3. 无记录或已记录代次 < fence ⇒ 通过，并把已记录代次推进为
	//     fence（新代绑定：接管后的首次提交换绑本 run 的进度身份）；
	//  4. 序号单调判定照旧（ErrStaleSeq）。
	//
	// 返回 ErrStaleFence 时**必须没有发生任何写入**（不得部分写）。
	SaveIfNewerFenced(ctx context.Context, s Snapshot, fence uint64) error
}

// CommitFenced 提交一次**带接管代次**的进度快照，返回 nil 表示已成功落库。
//
// 语义 = Commit 的全部顺序（进程内闸 → 存储侧判定 → 失败回滚闸）+
// 存储侧的代次判定（见 FencedStoreCAS）。调用方出示的 fence 是本次认领
// 铸造的接管代次（taskboard.Task.Lease.Fence）——存储侧据此拒绝陈旧
// 代次的迟到提交。
//
// store 未实现 FencedStoreCAS ⇒ ErrFenceUnsupported（显式失败，不降级）。
func (c *Committer) CommitFenced(ctx context.Context, s Snapshot, fence uint64) error {
	if c.store == nil {
		return ErrStoreMissing
	}
	fs, ok := c.store.(FencedStoreCAS)
	if !ok {
		return errors.Wrap(ErrFenceUnsupported, "checkpoint: 本次提交要求栅栏判据（带代次的条件提交）", "runID", s.RunID)
	}

	if c.gate != nil {
		if err := c.gate.Accept(s); err != nil {
			return err
		}
	}

	err := fs.SaveIfNewerFenced(ctx, s, fence)
	if err != nil && c.gate != nil {
		c.gate.Release(s.RunID, s.Seq)
	}
	return err
}

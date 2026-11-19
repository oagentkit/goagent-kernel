// apply.go —— 跨面 Apply：四个面的变更收进**同一次提交**。
//
// # 为什么要有它
//
// 四个面各自提交 ⇒ 「任务做完了但没留痕」「留痕了但没做完」两类半截状态
// 结构性地可能。Apply 把四个面的变更收进**同一次提交** ⇒ 跨面不会出现
// 「一半已提交」。
//
// # ⚠ 嵌套事务会死锁（表现为 ctx 超时，不是干净报错）
//
// 写句柄 MaxOpenConns(1)；exec.run 只在 e.tx == nil 时才 BeginTx；Store.exec()
// **永远造 tx-less 的 exec** ⇒ **各面各开事务 ⇒ 第二个面等第一个面的连接
// ⇒ 永久等 ⇒ 挂到 ctx 超时**。
//
// ⇒ 正确形态：**Apply 开一个事务，把同一个 tx 贯穿各面**（tx.Tasks() 等访问器
// 造的面都挂在这一条 tx 上）。
// ⇒ **禁止**在 Apply 的事务内调任何走 s.exec() / s.exec().run(...) 的路径
// （那是嵌套事务 ⇒ 挂死，不是干净报错）。
//
// # 失败即作废整批（即使调用方吞掉了内层的错误）
//
// 内层任一面失败都会被记进**共享失败槽**（exec.failed）：调用方吞掉错误继续
// 写别的面时，Apply 仍会发现「本批已失败」⇒ 整批回滚。缺这条，「任务做完了
// 但没留痕」与「留痕了但没做完」两类半截状态就会重现——这正是本包存在的理由。
//
// 失败槽是**记录点**，不是判定点：判定只有一处（下面的提交闸）。两处都判 ⇒
// 同一个判断有两个主人 ⇒ 改坏一处测试看不出区别。
//
// # 事务内的读一律走 tx
//
// 事务内的读走 exec.reader 的路由（tx != nil ⇒ 用 tx）：读-判-写原子性靠这条
// 保证；绕开 tx 用 db 直查会与占着唯一连接的写事务抢连接 ⇒ 挂到 ctx 超时。
//
// # 时钟
//
// 批内新建的面实例拿不到「调用方注入到某个面实例上的面级时钟」（面级 clock
// 是注入到**那个面实例**上的）⇒ 批内按各面 clockSource() 解析（面级 →
// Store 级 → nil）。
//
// ⚠ **面级时钟注入不跨 Apply 边界**：s.Board().UseClock(f) 注入的时钟在
// tx.Tasks() 造的新面实例上取不到（那是个新实例）。要批内也用注入时钟，
// 在 Store 级注入（Store.UseClock）——这是本任务明确记录的取舍，不是漏了。
package sqliteadapter

import (
	"context"
	"database/sql"

	"github.com/oekit/pkg/errors"
)

// Tx 一次事务的四个面（共享同一份状态与同一次提交）。
//
// ⚠ 四个面共用**同一个** tx ⇒ 同一次提交；不是一个面一个事务（那会死锁，
// 见包头「嵌套事务会死锁」）。
type Tx struct {
	// tx 本批的事务句柄（四个面共用同一个）。
	tx *sql.Tx
	// store 归属的 Store（面实例的时钟来源与只读句柄的来源）。
	store *Store
	// failed 本批第一次失败（共享失败槽；nil ⇒ 该面不在批次里）。
	failed *error
}

// exec 造一个事务内的执行位置，并挂上本批的失败槽。
//
// 时钟传 nil：批内按各面 clockSource() 解析（面级 → Store 级 → nil），
// 详见包头「时钟」。
func (tx *Tx) exec() exec {
	e := tx.store.execIn(tx.tx, nil)
	e.failed = tx.failed
	return e
}

// Tasks 任务面（事务内形态）。
//
// 事务内的写走本批的 tx（不再自开事务）；读走 exec.reader 的路由（tx != nil
// ⇒ 用 tx ⇒ 读-判-写原子）。
func (tx *Tx) Tasks() *BoardStore {
	return &BoardStore{exec: tx.exec(), store: tx.store}
}

// Effects 副作用面（事务内形态）。
func (tx *Tx) Effects() *EffectStore {
	return &EffectStore{exec: tx.exec(), store: tx.store}
}

// Snapshots 进度快照面（事务内形态）。
//
// ⚠ 与门面形态的关键差别：SaveIfNewer 在门面形态下**自开事务**，在批内形态下
// 复用本批的 tx（⇒ 快照与四面的变更同生共死）。
func (tx *Tx) Snapshots() *SnapStore {
	return &SnapStore{exec: tx.exec()}
}

// Events 事件面（事务内形态）。
//
// ⚠ 批内请用 Insert：它经 exec.run ⇒ 复用本批的 tx，且失败会记进失败槽。
// PublishInTx 需要调用方自己拿 *sql.Tx（批外用法），批内用 Insert 等价。
func (tx *Tx) Events() *EventStore {
	return &EventStore{exec: tx.exec()}
}

// Apply 在**一个事务**里提交四个面的变更。
//
// 语义：
//   - 回调返回错误 ⇒ 整批丢弃，该错误**原样**传出（不被包装）；
//   - 回调返回 nil，但批内任一面失败过 ⇒ 整批仍丢弃，并报出那条失败
//     （失败被调用方吞掉时，返回 nil 等于谎报「整批提交了」）；
//   - 回调返回 nil 且批内无失败 ⇒ 四个面的变更作为**一次提交**落库。
//
// ⚠ **禁止**在回调里调任何走 s.exec() / s.exec().run(...) 的路径（如
// s.Board().Claim）：那是嵌套事务 ⇒ 外层 tx 占着唯一连接、内层永远等 ⇒
// 挂到 ctx 超时（不是干净报错）。批内一律用 tx.Tasks() 等事务内形态。
//
// ⚠ Apply 不可重入（回调里再 Apply ⇒ 同样的嵌套事务死锁）。
func (s *Store) Apply(ctx context.Context, fn func(tx *Tx) error) error {
	if fn == nil {
		return errors.New("sqliteadapter: Apply 的回调不得为 nil（空回调会提交一个空批）")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "sqliteadapter: 开事务失败")
	}
	// 回滚兜底：回调 panic 时事务必须被释放——写句柄只有一条连接，泄漏连接
	// = 后续所有写挂死。提交成功后这句返回 sql.ErrTxDone（忽略即可）。
	defer func() { _ = tx.Rollback() }()

	// 失败槽：四个面共享**同一份** ⇒ 任一面失败，其余面都看得见。
	var failed error
	inner := &Tx{tx: tx, store: s, failed: &failed}

	cbErr := fn(inner)
	switch {
	case cbErr != nil:
		// 回调自己的错误：原样传出（调用方按自己的哨兵分流）。
		_ = tx.Rollback()
		return cbErr
	case failed != nil:
		// 内层失败被调用方吞掉了 ⇒ 整批仍必须作废，并把失败报出来。
		_ = tx.Rollback()
		return errors.Wrap(failed, "sqliteadapter: 本批有一面失败，整批作废")
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "sqliteadapter: 提交事务失败")
	}
	return nil
}

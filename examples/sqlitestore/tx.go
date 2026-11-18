// tx.go —— 事务内的四个面（跨面同一次提交）。
//
// # 为什么需要它
//
// 四个面各自都能独立提交，并不能证明「单一存储覆盖全部状态」——那还差
// 最要紧的一条：**跨面的变更必须能落在同一次提交里**。
//
//   - 任务板说「这一步完成了」，事件面要同时留下一条事件行（Outbox）：
//     分两次提交 ⇒ 崩在中间 ⇒ 「事情做完了但没留痕」或「留痕了但没做完」；
//   - 副作用闸说「这个动作已生效」，快照面要同时推进进度：分两次提交 ⇒
//     恢复点与事实对不上，重启后重复施加。
//
// Apply 提供的正是这个：**一次事务、四个面、一次落盘**。事务内的变更整批
// 写进日志的**同一行**（journal.go 的 v2 格式）⇒ 磁盘层面也不会出现
// 「一半已提交」。
package sqlitestore

import (
	"context"
	"time"
)

// txCtx 一次事务的上下文：读已提交状态 + 暂存待写记录。
type txCtx struct {
	st     *state
	writes []journalRecord
	// now 本次事务的时刻（进锁前取一次 ⇒ 同一事务里所有判据看同一个时刻）。
	now time.Time
	// failed 本事务内第一次失败（一旦发生，整批作废）。
	//
	// 为什么要有它：事务内某个面判失败时，调用方**可能吞掉那个错误**继续
	// 写别的面 ⇒ 就会出现「一半已提交」。失败一旦发生就作废整批，是唯一
	// 能杜绝这种半截事务的形态（失败形态是变红，不是留半截状态）。
	//
	// ⚠️ 它是**记录点**，不是判定点：判定只有一处（db.transact 的提交闸）。
	// 两处都判 ⇒ 同一个判断有两个主人 ⇒ 改坏一处测试看不出区别。
	failed error
}

// stage 暂存一条记录并**立即**应用到内存状态。
//
// 「立即应用」是事务语义：同一事务里后续的判定读得到前一步的改动。落盘
// 仍是**整批一次**（transact 末尾）——失败则整批丢弃，状态随之回到事务
// 开始时的样子。
func (t *txCtx) stage(rec journalRecord) error {
	if err := applyRecord(t.st, rec); err != nil {
		t.fail(err)
		return err
	}
	t.writes = append(t.writes, rec)
	return nil
}

// fail 记下本次事务的失败（只记第一次：第一次才是根因）。
//
// 它**不返回**错误：返回会让调用点写成 `return t.fail(err)`，而那处返回的
// 是「已记录」这个事实、不是根因——读代码的人会以为返回的是新错误。
func (t *txCtx) fail(err error) {
	if err == nil {
		return
	}
	if t.failed == nil {
		t.failed = err
	}
}

// exec 一次操作的执行位置：独立事务，或当前事务内。
//
// 两种位置共用同一套判据函数（effectAcquire / taskClaim / …）⇒ 事务内
// 走的判据与独立提交**逐字相同**，不会因为「换了个入口」而漂。
type exec struct {
	db *db
	tx *txCtx
}

// run 在执行位置里跑一次操作。
func (e exec) run(ctx context.Context, fn func(tx *txCtx) error) error {
	if e.tx != nil {
		// 事务内：失败要被记进事务（调用方吞掉错误也要作废整批）。
		if err := fn(e.tx); err != nil {
			e.tx.fail(err)
			return err
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.db.transact(fn)
}

// now 取本次操作的时刻。
//
// 独立事务：锁外取存储时钟（issue #317：注入的回调不得在持锁时被调用）。
// 事务内：读事务开始时捕获的时刻（Apply 在进锁前取一次）⇒ 同一个事务里
// 所有判据看的是同一个时刻，不会出现「事务中途时钟跳了」。
func (e exec) now() time.Time {
	if e.tx != nil {
		return e.tx.now
	}
	return e.db.clock()
}

// Tx 一次事务的四个面（共享同一份状态与同一次提交）。
type Tx struct {
	ctx *txCtx
}

// Tasks 任务面（taskboard.Board / RenewingBoard 的事务内形态）。
func (tx *Tx) Tasks() *TaskStore {
	return &TaskStore{exec: exec{tx: tx.ctx}}
}

// Effects 副作用面（sideeffect.Store 的事务内形态）。
func (tx *Tx) Effects() *EffectStore {
	return &EffectStore{exec: exec{tx: tx.ctx}}
}

// Snapshots 进度快照面（checkpoint.StoreCAS 的事务内形态）。
func (tx *Tx) Snapshots() *SnapshotStore {
	return &SnapshotStore{exec: exec{tx: tx.ctx}}
}

// Events 事件面（eventbus.Store / TailStore 的事务内形态）。
func (tx *Tx) Events() *EventStore {
	return &EventStore{exec: exec{tx: tx.ctx}}
}

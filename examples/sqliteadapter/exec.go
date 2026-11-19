// exec.go —— 执行位置抽象：独立事务，或当前事务内。
//
// 判据单一来源的前提：两种执行位置共用同一套判据函数 ⇒ 事务内走的判据与
// 独立提交**逐字相同**，不会因为「换了个入口」而漂。这是跨面 Apply 的前提。

package sqliteadapter

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// exec 一次操作的执行位置：独立事务，或当前事务内。
//
// 两种位置共用同一套判据函数 ⇒ 事务内走的判据与独立提交**逐字相同**，
// 不会因为「换了个入口」而漂。这是跨面 Apply 的前提。
type exec struct {
	db *sql.DB
	tx *sql.Tx
	// clock 注入的时钟（契约套件硬要求）；nil ⇒ 用存储时钟。
	clock func() time.Time
}

// run 在执行位置里跑一次操作。
func (e exec) run(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if e.tx != nil {
		// 事务内：调用方拿到的是同一个 tx，不再开事务、不再提交。
		return fn(e.tx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqliteadapter: 开事务失败: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqliteadapter: 提交事务失败: %w", err)
	}
	return nil
}

// now 取本次操作的时刻。
//
// 注入时钟优先（套件硬要求）；否则读**存储时钟** unixepoch('subsec')。
//
// 两条纪律：
//   - 不用进程本地 time.Now——租约与执行意图是跨进程共享的事实，各进程用
//     自己的时钟写入/判定到期时刻时，±10s 的漂移对 15s 的 TTL 意味着
//     「A 写入的租约在 B 看来从一开始就是过期的」。
//   - 取时刻在**拿到写锁之后**——BEGIN IMMEDIATE 已持写锁，此刻取的才是
//     「真正开始干活」的时刻（unixepoch('subsec') 不是 BEGIN 冻结值，
//     实测同事务内两次取时刻相差 202ms）。
//
// 精度：SQLite 内建时钟是**毫秒级**（实测最小非零增量 0.9999ms），
// 比 PG 的 clock_timestamp（微秒）粗。租约 TTL 建议 ≥ 1s。
//
// tx 必须显式传：nil tx 且**无注入时钟** ⇒ 返回显式错误（fail loud），
// 不空指针 panic。
func (e exec) now(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	if c := e.clockOverride(); c != nil {
		return c(), nil
	}
	// 为什么不让 now 自己去 e.db 取时钟：
	//   - exec.run 的回调拿到的 tx 与 exec 值里的 tx **不是同一个**（run 期间
	//     exec.tx 仍为 nil）⇒ 去 e.db 取走的不是那条事务连接。
	//   - 违反设计稿「取时刻在拿到写锁之后」；单连接池下更是直接与占着
	//     唯一连接的写事务抢连接 ⇒ 挂到 ctx 超时。
	// ⇒ nil tx 是**调用方漏传**的编程错误，显式报错让人一眼看出。
	if tx == nil {
		return time.Time{}, fmt.Errorf("sqliteadapter: 读存储时钟必须传事务句柄：tx 为 nil（调用方漏传 tx）")
	}
	var ms int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(unixepoch('subsec') * 1000 AS INTEGER)`).Scan(&ms); err != nil {
		return time.Time{}, fmt.Errorf("sqliteadapter: 读存储时钟失败: %w", err)
	}
	return time.UnixMilli(ms).UTC(), nil
}

// clockOverride 返回注入的时钟；nil ⇒ 读存储时钟。
func (e exec) clockOverride() func() time.Time { return e.clock }

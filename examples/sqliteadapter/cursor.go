// cursor.go —— eventbus.Tailer 游标持久化（eventbus.CursorStore）的 SQLite 参考实现。
//
// # 为什么需要它
//
// eventbus.Tailer 的游标默认是 Run 的局部状态，停机即失；只有装配 CursorStore
// 才能从上次停点续随（eventbus.CursorStore 契约：Load 失败停摆、Save 失败不停摆、
// 未命中回退显式起点 / MaxID）。仓内此前只有测试替身满足这条接缝，生产宿主接
// SQLite 时没有参考落点——本实现补上这一环。
//
// # 语义
//
//   - 一行一个消费者（consumer key）：同一键的游标只有一条，Load 未命中 =
//     found=false（首次部署）；读失败返回 error，Tailer 会停摆重试，绝不
//     静默回退 MaxID（回退即跳过停机窗口）。
//   - **游标单调不减**：SaveCursor 用带条件的 upsert 吸收回退写（陈旧副本的
//     迟到保存不会把游标拉回去）；非法回退不报错、不落库。
//   - 保存是独立 UPSERT，不与事件发布同事务：游标不是业务状态，Tailer 在
//     投递之后保存——崩溃窗口只会造成重启后重读（订阅侧游标幂等吸收），
//     不会跳读。
//   - 同一 consumer key 只应由一个尾随者写（多副本各用各的键）；同键并发写
//     的语义是「最后落库的最大值获胜」，不承诺无重复投递。
//
// # 事务边界
//
// 写走写句柄的一个事务（独立路径）；读（LoadCursor）走**只读句柄**——写句柄
// 只有一条连接，事务占用它期间从写句柄取连接会永久等待（实测：挂到 ctx 超时）。
package sqliteadapter

import (
	"context"
	"database/sql"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TailCursorStore eventbus.CursorStore 的 SQLite 参考实现：绑定一个 consumer
// key（如「副本名 / 消费组名」），游标落在 gak_tail_cursor。
type TailCursorStore struct {
	// exec 执行位置：db 是写句柄，read 是只读句柄（独立读路径走它）。
	exec exec
	// consumer 消费者键（与 PG 侧同款：一行一个消费者）。
	consumer string
}

// 编译期断言：本实现满足事件面的游标持久化接缝。
var _ eventbus.CursorStore = (*TailCursorStore)(nil)

// TailCursors 游标面（consumer key 不得为空）。
//
// 空 key 是装配期编程错误：所有尾随者共用一条游标 ⇒ 后跑的推过对方整段窗口
// ⇒ 订阅侧静默漏帧。fail fast 在构造点（与 PG 侧 NewTailCursorStore、
// eventbus.NewTailer 同口径）——不留到运行时。
func (s *Store) TailCursors(consumer string) *TailCursorStore {
	if consumer == "" {
		panic("sqliteadapter: TailCursorStore 需要 consumer key（空键会让所有尾随者共享一条游标）")
	}
	return &TailCursorStore{exec: s.execBase(), consumer: consumer}
}

// LoadCursor 读上次保存的游标（0, false, nil = 从未存过；error = 读失败，
// Tailer 会停摆重试，不得静默回退 MaxID）。
func (s *TailCursorStore) LoadCursor(ctx context.Context) (uint64, bool, error) {
	q, err := s.exec.reader()
	if err != nil {
		return 0, false, err
	}
	var n int64
	err = q.QueryRowContext(ctx,
		`SELECT cursor_pos FROM gak_tail_cursor WHERE consumer = ?`, s.consumer).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.Wrap(err, "sqliteadapter: 读尾随游标失败", "consumer", s.consumer)
	}
	if n < 0 {
		return 0, false, errors.New("sqliteadapter: 尾随游标为负（数据损坏，consumer=" + s.consumer + "）")
	}
	return uint64(n), true, nil
}

// SaveCursor 保存游标（UPSERT；单调不减：回退写被条件 upsert 吸收，不落库）。
//
// 失败返回 error，调用方（Tailer）只 warn 不停摆——游标落后只会导致重启后
// 重读（订阅侧幂等吸收），方向安全。
func (s *TailCursorStore) SaveCursor(ctx context.Context, cursor uint64) error {
	if cursor > uint64(1<<63-1) {
		// 游标列是 INTEGER（有符号 64 位）：高位被截断成负数 ⇒ 读出来的游标
		// 倒退 ⇒ 尾随从停摆点回退 ⇒ 重放整段（比漏帧更难排查）。
		return errors.New("sqliteadapter: 尾随游标超出 INTEGER 范围（consumer=" + s.consumer + "）")
	}
	return s.exec.run(ctx, func(tx *sql.Tx) error {
		// 条件 upsert：只有「本次 > 已存」才更新 ⇒ 回退写与等值写都是无操作
		// （影响行数 0），游标单调不减由**存储侧**保证，不靠进程内比较。
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO gak_tail_cursor (consumer, cursor_pos) VALUES (?, ?)
			 ON CONFLICT (consumer) DO UPDATE SET cursor_pos = excluded.cursor_pos
			 WHERE gak_tail_cursor.cursor_pos < excluded.cursor_pos`,
			s.consumer, int64(cursor)); err != nil {
			return errors.Wrap(err, "sqliteadapter: 保存尾随游标失败", "consumer", s.consumer)
		}
		return nil
	})
}

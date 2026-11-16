// cursor.go —— eventbus.Tailer 游标持久化（eventbus.CursorStore）的 PostgreSQL
// 参考实现（issue #264 的「崩溃点重启续读」）。
//
// # 为什么需要它
//
// eventbus.Tailer 的游标默认是 Run 的局部状态，停机即失；只有装配
// CursorStore 才能从上次停点续随（eventbus.CursorStore 契约：Load 失败停摆、
// Save 失败不停摆、未命中回退显式起点 / MaxID）。仓内此前只有测试替身
// （eventbus 的 memCursorStore）满足这条接缝，生产宿主接 PostgreSQL 时没有
// 参考落点——本实现补上这一环。
//
// # 语义
//
//   - 一行一个消费者（consumer key）：同一键的游标只有一条，Load 未命中 =
//     found=false（首次部署）；读失败返回 error，Tailer 会停摆重试，绝不
//     静默回退 MaxID（回退即跳过停机窗口）。
//   - **游标单调不减**：SaveCursor 用 GREATEST 吸收回退写（陈旧副本的迟到
//     保存不会把游标拉回去）；非法回退不报错、不落库。
//   - 保存是独立 UPSERT，不与事件发布同事务：游标不是业务状态，Tailer 在
//     投递之后保存——崩溃窗口只会造成重启后重读（订阅侧游标幂等吸收），
//     不会跳读。
//   - 同一 consumer key 只应由一个尾随者写（多副本各用各的键）；同键并发写
//     的语义是「最后落库的最大值获胜」，不承诺无重复投递。
package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TailCursorStore eventbus.CursorStore 的 PostgreSQL 实现：绑定一个 consumer
// key（如「副本名 / 消费组名」），游标落在 pgref_tail_cursor。
type TailCursorStore struct {
	pool     *pgxpool.Pool
	consumer string
}

// NewTailCursorStore 构造游标存储。pool 不得为 nil，consumer 不得为空——
// 两者都是装配期编程错误，fail fast 在构造点（与 eventbus.NewTailer 同口径）。
func NewTailCursorStore(pool *pgxpool.Pool, consumer string) *TailCursorStore {
	if pool == nil {
		panic("pgstore: TailCursorStore 需要连接池（nil pool 的游标无从读写）")
	}
	if consumer == "" {
		panic("pgstore: TailCursorStore 需要 consumer key（空键会让所有尾随者共享一条游标）")
	}
	return &TailCursorStore{pool: pool, consumer: consumer}
}

// 编译期断言：本实现满足 eventbus 的游标持久化接缝。
var _ eventbus.CursorStore = (*TailCursorStore)(nil)

// LoadCursor 读上次保存的游标（0, false, nil = 从未存过；error = 读失败，
// Tailer 会停摆重试，不得静默回退 MaxID）。
func (s *TailCursorStore) LoadCursor(ctx context.Context) (uint64, bool, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT cursor_pos FROM pgref_tail_cursor WHERE consumer = $1`,
		s.consumer).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.Wrap(err, "pgstore: 读尾随游标失败", "consumer", s.consumer)
	}
	if n < 0 {
		return 0, false, errors.New("pgstore: 尾随游标为负（数据损坏，consumer=" + s.consumer + "）")
	}
	return uint64(n), true, nil
}

// SaveCursor 保存游标（UPSERT；单调不减：回退写被 GREATEST 吸收，不落库）。
//
// 失败返回 error，调用方（Tailer）只 warn 不停摆——游标落后只会导致重启后
// 重读（订阅侧幂等吸收），方向安全。
func (s *TailCursorStore) SaveCursor(ctx context.Context, cursor uint64) error {
	if cursor > uint64(1<<63-1) {
		return errors.New("pgstore: 尾随游标超出 BIGINT 范围（consumer=" + s.consumer + "）")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO pgref_tail_cursor (consumer, cursor_pos) VALUES ($1, $2)
		ON CONFLICT (consumer) DO UPDATE
		  SET cursor_pos = GREATEST(pgref_tail_cursor.cursor_pos, EXCLUDED.cursor_pos),
		      updated_at = clock_timestamp()
		  WHERE pgref_tail_cursor.cursor_pos < EXCLUDED.cursor_pos`,
		s.consumer, int64(cursor))
	if err != nil {
		return errors.Wrap(err, "pgstore: 保存尾随游标失败", "consumer", s.consumer)
	}
	return nil
}

// event.go —— 事件面（eventbus.Store + TailStore + WatermarkTailStore）。
//
// # 游标从哪来：已提交水位线
//
// 事件 ID **不是**自增序列，而是「已提交水位线 + 1」：水位线只随**提交**
// 推进，事务回滚既不推它也不烧号（下一个事务重新从同一点派生）。
//
// ⇒ 铸造序 == 提交序 ⇒ **序列空洞结构性地不存在** ⇒ TailStore 的「无空洞
// 推进」硬要求由构造满足，而不是靠「读的时候小心别跳过」这种约定。
//
// 这也正是 eventbus 契约点名的**唯一被承诺支持的生产形态：Outbox**（事件
// 行与业务状态同事务提交）。本包用 Tx/Apply 提供这个形态：事件行与任务
// 状态、副作用闸、进度快照落在**同一行**日志里。
//
// # 「自增/序列 ID 直读」显式不支持
//
// BIGSERIAL + WHERE id > cursor 那种读法：乱序提交让迟到的小 ID 被游标
// 永久越过（丢帧），回滚烧号让空洞永不回填（尾随永久停摆）。本实现不
// 提供那种形态，也不假装提供。
//
// # 载荷的往返口径（与 examples/pgstore 的 JSONB 同款）
//
// 载荷是 map[string]any，经 JSON 往返后**数字统一为 float64**（"计数": 3
// 读回来是 3.0）。宿主若需要整数保真，走字符串或宿主自己的序列化。这是
// JSON 载荷存储的既有性质，不是本实现的取舍——两个参考实现口径一致。
package sqlitestore

import (
	"context"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// EventStore 事件面（eventbus.Store + TailStore + WatermarkTailStore）。
//
// 实现了 WatermarkTailStore 即**声明**本存储是 Outbox 形态（事件行与业务
// 状态同事务提交、水位线随提交推进）。
type EventStore struct {
	exec exec
}

// Insert 持久化一条事件并返回权威游标 ID。
//
// 传入事件的 ev.ID 是输入侧遗留值，**一律忽略**（游标由存储铸造）。
func (e *EventStore) Insert(ctx context.Context, ev eventbus.Event) (uint64, error) {
	var id uint64
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		id, err = eventInsert(tx, ev)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// MaxID 当前权威游标空间的**无空洞边界**（空表 = 0）。
//
// Outbox 形态下它就是已提交水位线（与 CommittedWatermark 同值同源）——
// 返回更大的值（如裸 max(id)）会让初始化本身跳过在途小 ID。
func (e *EventStore) MaxID(ctx context.Context) (uint64, error) {
	var out uint64
	err := e.exec.run(ctx, func(tx *txCtx) error {
		out = tx.st.watermark
		return nil
	})
	if err != nil {
		return 0, err
	}
	return out, nil
}

// ListAfter 列出 id > cursor 的事件（升序，单拍上限）。
//
// 返回段从 cursor+1 起**连续可见**：已提交的事件 ID 无空洞（见文件头），
// 故不会返回跳号段。
func (e *EventStore) ListAfter(ctx context.Context, cursor uint64, limit int) ([]eventbus.Event, error) {
	out := []eventbus.Event{}
	err := e.exec.run(ctx, func(tx *txCtx) error {
		out = eventListAfter(tx, cursor, limit)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CommittedWatermark 已提交水位线（≤ 此值的游标 ID 已全部提交且可见）。
func (e *EventStore) CommittedWatermark(ctx context.Context) (uint64, error) {
	var out uint64
	err := e.exec.run(ctx, func(tx *txCtx) error {
		out = tx.st.watermark
		return nil
	})
	if err != nil {
		return 0, err
	}
	return out, nil
}

// eventInsert 落库一条事件并铸造游标（事务内与独立事务共用这一份）。
func eventInsert(tx *txCtx, ev eventbus.Event) (uint64, error) {
	row := ev.Clone()
	// 游标由存储铸造：输入侧 ID 一律忽略（防御陈旧游标流入）。
	row.ID = tx.st.watermark + 1
	rec := &eventRecord{ID: row.ID, Event: row}
	if err := tx.stage(journalRecord{Kind: kindEvent, Event: rec}); err != nil {
		return 0, err
	}
	return row.ID, nil
}

// eventListAfter 列出 id > cursor 的事件（升序）。
func eventListAfter(tx *txCtx, cursor uint64, limit int) []eventbus.Event {
	out := make([]eventbus.Event, 0)
	if limit <= 0 {
		return out
	}
	for _, rec := range tx.st.events {
		if rec.ID <= cursor {
			continue
		}
		out = append(out, rec.Event.Clone())
		if len(out) >= limit {
			break
		}
	}
	return out
}

// 编译期断言：本实现满足事件面的三张接缝。
var (
	_ eventbus.Store              = (*EventStore)(nil)
	_ eventbus.TailStore          = (*EventStore)(nil)
	_ eventbus.WatermarkTailStore = (*EventStore)(nil)
)

// backend.go —— coord.Backend 的 PostgreSQL 参考实现（issue #181）。
//
// # 它是哪一层
//
// coord 包把「协调契约 → 后端原语」的判定逻辑收成 BoardAdapter / StoreAdapter
// 一份实现，宿主的存储只需实现三个原语（快照读 / 条件事务 / 前缀列键）并让
// 它们真的原子。本文件就是**PostgreSQL 怎么落这三个原语**的参考：用
// coord.BackendContract 契约套件在真实数据库上原样跑通，宿主照抄即可。
//
// # 版本与墓碑
//
// 版本跨删除存活：删除不删行，ver + 1 且 deleted = TRUE（墓碑），同名键
// 重建从墓碑之后继续——「写 → 删 → 重建 → 旧版本 CAS」因此必然失败（ABA
// 封死，issue #181）。删除从未存在的键是空操作（不建墓碑、不消耗版本）。
// 本实现**不 GC 墓碑**：回收墓碑等于把版本还给 VerMissing；GC 许可条件见
// coord.Backend 文档的 Version 条款。
//
// # 原子性：咨询锁而不是行锁
//
// 条件事务要把「读断言 → 写」整段对同一批键串行化。行锁（SELECT … FOR
// UPDATE）只锁**已存在**的行，而断言/写入都可能落在不存在的键上——「断言
// 这个键不存在」正是最需要护的窗口。故这里对事务**涉及键**取事务级咨询锁
// （pg_advisory_xact_lock，键的哈希值升序拿锁防死锁）：锁与行是否存在无关，
// 提交/回滚自动释放；哈希碰撞只让无关键多等一次，不影响正确性。
// 前提是**所有写者都走本实现**（直接改 pgref_coord_kv 的旁路写者不受锁保护）。
package pgstore

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/coord"
)

// BackendStore coord.Backend 的 PostgreSQL 参考实现。
type BackendStore struct{ pool *pgxpool.Pool }

// NewBackendStore 构造协调后端原语面存储。
func NewBackendStore(pool *pgxpool.Pool) *BackendStore {
	return &BackendStore{pool: pool}
}

// Snapshot 一致快照读（单语句：同一瞬间的视图）。
//
// 墓碑返回 Exists=false + 墓碑版本（不是 VerMissing）——版本是断言的锚点，
// 必须跨删除存活（见文件头）。
func (b *BackendStore) Snapshot(ctx context.Context, keys ...string) (map[string]coord.Entry, error) {
	out := make(map[string]coord.Entry, len(keys))
	uniq := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, dup := out[k]; dup {
			continue // 同一个键重复出现按一次计（契约）
		}
		out[k] = coord.Entry{Key: k, Ver: coord.VerMissing}
		uniq = append(uniq, k)
	}
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := b.pool.Query(ctx,
		`SELECT key, ver, deleted, val FROM pgref_coord_kv WHERE key = ANY($1::text[])`, uniq)
	if err != nil {
		return nil, errors.Wrap(err, "pgstore: coord 快照读失败")
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key     string
			ver     int64
			deleted bool
			val     []byte
		)
		if err := rows.Scan(&key, &ver, &deleted, &val); err != nil {
			return nil, errors.Wrap(err, "pgstore: 解析 coord 快照行失败")
		}
		e := coord.Entry{Key: key, Ver: coord.Version(ver)}
		if !deleted {
			e.Exists = true
			// 独立拷贝：后端不得复用底层字节（契约）。
			e.Val = append([]byte(nil), val...)
		}
		out[key] = e
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "pgstore: coord 快照读失败")
	}
	return out, nil
}

// Txn 条件事务：全部断言成立才执行全部写入（一个事务内完成）。
//
// 返回 applied=false 是**正常结论**（断言不成立，什么都没发生，事务空提交
// 释放咨询锁）；error 只表示基础设施故障。
func (b *BackendStore) Txn(ctx context.Context, asserts []coord.Assert, mutations []coord.Mutation) (bool, error) {
	keys := involvedKeys(asserts, mutations)
	return withinTx(ctx, b.pool, func(tx pgx.Tx) (bool, error) {
		if err := lockCoordKeys(ctx, tx, keys); err != nil {
			return false, err
		}
		state, err := readCoordState(ctx, tx, keys)
		if err != nil {
			return false, err
		}
		for _, a := range asserts {
			cur := coord.VerMissing
			if st, ok := state[a.Key]; ok {
				cur = coord.Version(st.ver)
			}
			if cur != a.Ver {
				return false, nil // 一个写入都不执行
			}
		}
		for _, mu := range mutations {
			if err := applyCoordMutation(ctx, tx, state, mu); err != nil {
				return false, err
			}
		}
		return true, nil
	})
}

// Keys 前缀列键（墓碑不是「存在的键」：只列活键，按键名排序）。
func (b *BackendStore) Keys(ctx context.Context, prefix string) ([]string, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT key FROM pgref_coord_kv WHERE starts_with(key, $1) AND NOT deleted ORDER BY key`, prefix)
	if err != nil {
		return nil, errors.Wrap(err, "pgstore: coord 前缀列键失败")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, errors.Wrap(err, "pgstore: 解析 coord 列键结果失败")
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "pgstore: coord 前缀列键失败")
	}
	return out, nil
}

// ── 事务内部件 ────────────────────────────────────────────────────────────

// coordRow 事务内读到的一行（墓碑 = deleted）。
type coordRow struct {
	ver     int64
	deleted bool
}

// involvedKeys 事务涉及的键：断言 ∪ 写入，去重并按**键名**排序
// （锁顺序由哈希值定，排序只为结果确定性）。
func involvedKeys(asserts []coord.Assert, mutations []coord.Mutation) []string {
	seen := make(map[string]bool, len(asserts)+len(mutations))
	out := make([]string, 0, len(asserts)+len(mutations))
	for _, a := range asserts {
		if !seen[a.Key] {
			seen[a.Key] = true
			out = append(out, a.Key)
		}
	}
	for _, m := range mutations {
		if !seen[m.Key] {
			seen[m.Key] = true
			out = append(out, m.Key)
		}
	}
	sort.Strings(out)
	return out
}

// lockCoordKeys 取事务级咨询锁（顺序 = 键哈希值升序：两个事务以相反顺序
// 拿锁会死锁）。锁在提交/回滚时由 PostgreSQL 自动释放。
//
// 为什么不是 SELECT … FOR UPDATE：行锁锁不住不存在的行，而「断言/创建
// 不存在的键」正是最需要护住的窗口（见文件头）。
func lockCoordKeys(ctx context.Context, tx pgx.Tx, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT hashtext(k)::bigint FROM unnest($1::text[]) AS k`, keys)
	if err != nil {
		return errors.Wrap(err, "pgstore: coord 计算键锁失败")
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return errors.Wrap(err, "pgstore: 解析 coord 键锁失败")
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "pgstore: coord 计算键锁失败")
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, id); err != nil {
			return errors.Wrap(err, "pgstore: coord 取键锁失败")
		}
	}
	return nil
}

// readCoordState 读事务涉及键的当前状态（缺失键不在 map 里，视作 VerMissing）。
func readCoordState(ctx context.Context, tx pgx.Tx, keys []string) (map[string]coordRow, error) {
	state := make(map[string]coordRow, len(keys))
	if len(keys) == 0 {
		return state, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT key, ver, deleted FROM pgref_coord_kv WHERE key = ANY($1::text[])`, keys)
	if err != nil {
		return nil, errors.Wrap(err, "pgstore: coord 读键状态失败")
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key string
			st  coordRow
		)
		if err := rows.Scan(&key, &st.ver, &st.deleted); err != nil {
			return nil, errors.Wrap(err, "pgstore: 解析 coord 键状态失败")
		}
		state[key] = st
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "pgstore: coord 读键状态失败")
	}
	return state, nil
}

// applyCoordMutation 落一条写入，并把结果写回事务内状态（同键的后续写入
// 在更新后的版本上继续）。调用方已持有全部涉及键的咨询锁。
func applyCoordMutation(ctx context.Context, tx pgx.Tx, state map[string]coordRow, mu coord.Mutation) error {
	st, ok := state[mu.Key]
	if mu.Del {
		// 删除也是一次成功写入：活条目转墓碑并推进版本。墓碑与从未存在的键
		// 都没有代可灭 ⇒ 空操作（不创建墓碑、不消耗版本）。
		if !ok || st.deleted {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pgref_coord_kv SET ver = ver + 1, val = NULL, deleted = TRUE WHERE key = $1`,
			mu.Key); err != nil {
			return errors.Wrap(err, "pgstore: coord 删除写回失败", "key", mu.Key)
		}
		state[mu.Key] = coordRow{ver: st.ver + 1, deleted: true}
		return nil
	}
	if !ok {
		// 首次写入：版本从 1 起；重建（墓碑之后）走下面的 UPDATE 分支，版本
		// 继续递增——不复用旧版本。
		if _, err := tx.Exec(ctx,
			`INSERT INTO pgref_coord_kv (key, ver, val, deleted) VALUES ($1, 1, $2, FALSE)`,
			mu.Key, mu.Val); err != nil {
			return errors.Wrap(err, "pgstore: coord 首写失败", "key", mu.Key)
		}
		state[mu.Key] = coordRow{ver: 1}
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE pgref_coord_kv SET ver = ver + 1, val = $2, deleted = FALSE WHERE key = $1`,
		mu.Key, mu.Val); err != nil {
		return errors.Wrap(err, "pgstore: coord 写入失败", "key", mu.Key)
	}
	state[mu.Key] = coordRow{ver: st.ver + 1}
	return nil
}

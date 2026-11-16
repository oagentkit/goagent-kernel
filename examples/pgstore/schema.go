// schema.go —— 连接池构造与建表。
//
// 表结构与判据的对应关系（每张表管一件事，见 doc.go）：
//
//   - pgref_effect_action   Key 级状态（做过没有 / 结果未知 / 执行意图 / 对账占用）
//   - pgref_effect_resource 资源级状态（这个资源谁占着 / 发第几代令牌）
//   - pgref_downstream      下游资源（已见最大令牌 + 参考业务数据，FencedSink 的落点）
//   - pgref_board_task      任务认领面（taskboard.Board）
//   - pgref_snapshot        进度快照（checkpoint.StoreCAS）
//   - pgref_event_watermark 已提交水位线（单行；事件 ID 分配的串行化点，#264）
//   - pgref_event           事件行（与业务状态同事务提交的落点，#264）
//   - pgref_tail_cursor     尾随游标持久化（eventbus.CursorStore，#264 重启续读）
package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaDDL 一套参考表（幂等：全部 CREATE TABLE IF NOT EXISTS）。
//
// 刻意放在 Go 侧而不是 init.sql：契约套件与冒烟入口都从这条路径建表，
// 单一来源，宿主抄它时不会抄到一份没人维护的 SQL 文件。
const schemaDDL = `
CREATE TABLE IF NOT EXISTS pgref_effect_action (
    key             TEXT PRIMARY KEY,
    done            BOOLEAN    NOT NULL DEFAULT FALSE,
    result          BYTEA,
    uncertain       BOOLEAN    NOT NULL DEFAULT FALSE,
    reason          TEXT       NOT NULL DEFAULT '',
    intent_resource TEXT,
    intent_fence    BIGINT,
    intent_holder   TEXT,
    intent_expires  TIMESTAMPTZ,
    recon_holder    TEXT,
    recon_expires   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS pgref_effect_resource (
    resource TEXT PRIMARY KEY,
    fence    BIGINT      NOT NULL DEFAULT 0,
    holder   TEXT,
    until    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS pgref_downstream (
    resource   TEXT PRIMARY KEY,
    seen_fence BIGINT NOT NULL,
    value      TEXT   NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS pgref_board_task (
    id     TEXT PRIMARY KEY,
    title  TEXT        NOT NULL DEFAULT '',
    deps   TEXT[]      NOT NULL DEFAULT '{}',
    state  TEXT        NOT NULL,
    owner  TEXT,
    token  TEXT,
    fence  BIGINT      NOT NULL DEFAULT 0,
    until  TIMESTAMPTZ,
    seq    BIGINT      NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pgref_snapshot (
    run_id TEXT PRIMARY KEY,
    seq    BIGINT NOT NULL,
    status TEXT   NOT NULL DEFAULT '',
    done   TEXT[] NOT NULL DEFAULT '{}',
    fields TEXT   NOT NULL DEFAULT ''
);

-- 事件面（issue #264）：Outbox 形态的两张表。水位行是 ID 分配的串行化点
-- （UPDATE … RETURNING 拿号并持行锁到事务提交），committed 就是「已提交
-- 水位线」——事件行与它的推进同事务落库，可观测态下恒有
-- committed == max(pgref_event.id) 且行集恰为 1..committed（无洞无孤儿）。
CREATE TABLE IF NOT EXISTS pgref_event_watermark (
    id        SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    committed BIGINT NOT NULL DEFAULT 0
);

INSERT INTO pgref_event_watermark (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS pgref_event (
    id          BIGINT PRIMARY KEY,
    session_id  TEXT        NOT NULL DEFAULT '',
    case_id     TEXT        NOT NULL DEFAULT '',
    step_id     BIGINT,
    type        TEXT        NOT NULL DEFAULT '',
    level       TEXT        NOT NULL DEFAULT 'info',
    payload     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 尾随游标（eventbus.CursorStore，#264「崩溃点重启续读」）：一行一个消费者。
-- cursor_pos 单调不减由 SaveCursor 的 UPSERT 保证；updated_at 仅作对账留痕。
CREATE TABLE IF NOT EXISTS pgref_tail_cursor (
    consumer   TEXT PRIMARY KEY,
    cursor_pos BIGINT      NOT NULL DEFAULT 0 CHECK (cursor_pos >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
`

// EnsureSchema 在 schema 里建好全部参考表（幂等，可重复执行）。
//
// schema 由调用方先建好（或用 NewSchema 一并创建）；本函数只建表。
// search_path 用 SET LOCAL 在本事务内指定——否则 DDL 落到调用方连接的
// 缺省 schema（public）里，读侧的 search_path 指到的却是一套空 schema。
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if !validSchemaName(schema) {
		return fmt.Errorf("pgstore: 非法的 schema 名 %q（须匹配 ^[a-z_][a-z0-9_]*$）", schema)
	}
	return runTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema); err != nil {
			return fmt.Errorf("pgstore: 指定 search_path 失败（schema %s）: %w", schema, err)
		}
		if _, err := tx.Exec(ctx, schemaDDL); err != nil {
			return fmt.Errorf("pgstore: 建表失败（schema %s）: %w", schema, err)
		}
		// 动作身份三列（ErrActionConflict）**只在这里定义一次**：CREATE TABLE
		// 不带它们，新老表统一由下面三条幂等 ALTER 加上——两处各写一份列
		// 定义就会漂（改一处漏一处，老库与新库的表结构分叉）。
		// IF NOT EXISTS 对新表是无操作，对已部署的旧表是迁移。
		const identityDDL = `
ALTER TABLE pgref_effect_action ADD COLUMN IF NOT EXISTS id_resource TEXT;
ALTER TABLE pgref_effect_action ADD COLUMN IF NOT EXISTS id_safety SMALLINT;
ALTER TABLE pgref_effect_action ADD COLUMN IF NOT EXISTS id_payload_hash TEXT;`
		if _, err := tx.Exec(ctx, identityDDL); err != nil {
			return fmt.Errorf("pgstore: 动作身份列迁移失败（schema %s）: %w", schema, err)
		}
		return nil
	})
}

// validSchemaName schema 名必须是裸标识符（本包把 schema 名拼进 DDL 文本，
// 不走参数绑定——PostgreSQL 不支持 identifier 参数化，故用白名单防注入）。
func validSchemaName(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, r := range s {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9' && i > 0)
		if !ok {
			return false
		}
	}
	return true
}

// NewSchema 建一个随机命名的 schema，返回名字。
//
// 契约套件的 factory 每个子测试调一次，构造一份**全新**实现——在共享的
// 数据库里，「全新」的落法是独立 schema：表状态互不可见，t.Cleanup 时整块删除。
func NewSchema(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pgstore: 生成 schema 名失败: %w", err)
	}
	name := "pgref_t_" + hex.EncodeToString(b[:])
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		return "", fmt.Errorf("pgstore: 建 schema 失败: %w", err)
	}
	return name, nil
}

// DropSchema 删除一个 schema（含其中的表）。
func DropSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if !validSchemaName(schema) {
		return fmt.Errorf("pgstore: 非法的 schema 名 %q", schema)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		return fmt.Errorf("pgstore: 删 schema 失败: %w", err)
	}
	return nil
}

// searchPath 把连接池的默认 search_path 指到 schema：后续全部 SQL 不用写前缀。
func searchPath(poolCfg *pgxpool.Config, schema string) {
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
}

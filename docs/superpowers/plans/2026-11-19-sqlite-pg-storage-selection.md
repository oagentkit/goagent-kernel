# 真 SQLite 适配器与存储选型判据 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**状态**：**已实施完毕**（2026-11-19 – 2026-11-20，按下面的 Task 逐个落地；
其中 **Task 10 = 删除假 SQLite 并翻转口径**，commit `43b5834`）——
**实施后现状**见文末「实施后现状」一节。

**Goal:** 删掉名为 `sqlitestore` 的假 SQLite（flock + JSONL），建一个真 SQLite 适配器 `examples/sqliteadapter`（全面对齐 PG 的契约面），给 PG 补齐跨面事务，并把「何时用 SQLite / 何时用 PostgreSQL」做成门禁可判的规则。

**Architecture:** 三个存储实现各司其职——`examples/reference-runtime`（内存，证判据本身对）、`examples/sqliteadapter`（真 SQLite，单机持久化，unit 层可跑）、`examples/pgstore`（PG，多副本/跨机，integration 层）。SQLite 与 PG 两包**形态同构、代码不共用**（存储层不同，耦合即锁死），正确性由同一套契约套件（`boardtest` / `sidetest` / `sidetest.RunSinkContract`）对齐。选型判据是独立门禁 `cmd/storagecheck`，声明面 `docs/STORAGE-SELECTION.yaml`，读 `docs/DEPLOY-MATRIX.yaml` 的场景表判「场景选的 storage 是否在允许集内 + 是否真提供该场景必需的全部能力 + 能力断言在代码里找得到落点」。

**Tech Stack:** Go 1.27.4、`modernc.org/sqlite` v1.62.0（纯 Go、无 cgo）、`database/sql`、`github.com/jackc/pgx/v5` v5.13.0（既有）、`gopkg.in/yaml.v3`、`github.com/oekit/pkg/errors`（唯一 errors 包）。

## Global Constraints

- **本机 Go 工具链**：跑任何 `go` 命令须先 `export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local`（本机默认 go1.27.1 < go.mod 的 1.27.4，会卡在工具链下载）。
- **CGO_ENABLED=0**：build / test / vet / crossbuild 一律带 `CGO_ENABLED=0`；只有 `make race` 走 `CGO_ENABLED=1`。
- **交叉构建矩阵**：`linux/amd64 linux/arm64 darwin/arm64 windows/amd64`——新增依赖必须纯 Go。
- **errors 包**：只用 `github.com/oekit/pkg/errors`（`New` / `Wrap(err, msg, kv...)` / `Is` / `As` / `Cause`），禁止 stdlib `errors` 与 `github.com/pkg/errors`。**门禁会红**。
- **单文件 ≤ 400 行**（`architecture-policy.yaml` 的 `thresholds.max_file_lines`，`enforce: true`）。新文件按职责拆。
- **裸 goroutine 禁止**：生产代码不得出现 `go func`，走 `concurrency/async.GoSafe`。
- **先改 policy 再写代码**：新增包/新依赖先在 `architecture-policy.yaml` 登记，否则依赖方向门禁当场红。
- **提交身份**：`MurphyYi <zy84338719@hotmail.com>`；`git add <具体路径>`，禁 `git add -A`。
- **中文文档/注释**：编辑后扫 U+FFFD（`grep -n $'\xef\xbf\xbd'`）。commit message 同样要扫。
- **台账纪律**：单号状态由 `internal/ledgerstatus` 从台账派生；正文引用新单号须进台账 §9.1 登记面。状态标记词只有三个：「已合入」「已关闭」「仍开着」。
- **历史不可篡改**：`docs/DISTRIBUTED-EVOLUTION-*.md` 与 `CHANGELOG.md` 的历史段落只追加、不改写。
- **部署清单 state 由证据推导**：`docs/DEPLOY-MATRIX.yaml` 与 `docs/USAGE.md` 的机器口径块必须在**同一个提交**里一起翻，否则 `make deploycheck` 当场红。
- **判据先于实现**：改任何判定逻辑必须同时给「坏实现会红」的反向验证用例。
- **本轮网络现实**：`modernc.org/sqlite` v1.61.0 已在本机 module cache（含 `libc` v1.79.0 等）；v1.62.0 需要 `libc` v1.80.1，**本机 cache 缺失且 `goproxy.cn` 解析超时**。⇒ 依赖版本先按「cache 里有的」定，需要联网时改用备用代理（见 Task 1）。
- **探针纪律**：并发/锁相关的探针，**两侧必须指向同一份数据**（本轮曾因此测出假结论）。
- **SQLite 驱动的传递依赖离线齐备**（controller 实测，2026-11-20）：`modernc.org/sqlite` v1.61.0
  的整棵传递依赖树**已全部在本机 module cache，无需联网**——
  `modernc.org/libc` v1.79.0、`cc/v4` v4.30.3、`ccgo/v4` v4.37.0、`gc/v2` v2.6.5、`gc/v3` v3.2.0、
  `goabi0` v0.2.0、`mathutil` v1.7.1、`memory` v1.12.1、`fileutil` v1.4.0、`strutil` v1.2.1、
  `sortutil` v1.2.1、`opt` v0.2.0、`token` v1.1.0、`github.com/ncruces/go-strftime` v1.1.0、
  `github.com/remyoudompheng/bigfft`、`golang.org/x/sys` v0.50.0、`github.com/google/uuid` v1.6.0、
  `github.com/dustin/go-humanize` v1.1.0、`github.com/mattn/go-isatty` v0.0.24
  ⇒ 本轮**全程离线**完成（`go mod download all` rc=0）。
  ⚠️ **驱动参数取值形式已逐个实测**（v1.61.0）：`_synchronous=FULL` 与 `_synchronous=2` **都合法**
  （两者都得到 `PRAGMA synchronous = 2`），只有非法值（如 `bogus`）被拒
  ⇒ 「枚举词」与「数字」两种写法都对，别把 FULL 当成非法值。

---

## File Structure

**新建**

| 文件 | 职责 |
|---|---|
| `examples/sqliteadapter/doc.go` | 包文档：落库形态、与 PG 的同构/差异、时钟精度、已知边界 |
| `examples/sqliteadapter/store.go` | `Store` 门面 + `Open` + 四个面访问器 + `Apply` |
| `examples/sqliteadapter/schema.go` | 建表 DDL（`EnsureSchema`）+ `schemaVersion` 迁移 |
| `examples/sqliteadapter/exec.go` | `exec{conn, tx}` 执行位置抽象（判据单一来源） |
| `examples/sqliteadapter/board.go` | 任务面（`taskboard.Board` / `RenewingBoard`） |
| `examples/sqliteadapter/effect.go` | 副作用闸（`sideeffect.Store`） |
| `examples/sqliteadapter/effect_rows.go` | 副作用闸的行结构与读写 |
| `examples/sqliteadapter/snapshot.go` | 进度快照（`checkpoint.StoreCAS`） |
| `examples/sqliteadapter/event.go` | 事件面（`eventbus.Store` / `TailStore` / `WatermarkTailStore`） |
| `examples/sqliteadapter/cursor.go` | 尾随游标（`eventbus.CursorStore`） |
| `examples/sqliteadapter/control.go` | 控制面（`controlplane.Store`） |
| `examples/sqliteadapter/sink.go` | 下游栅栏（`sideeffect.FencedSink`） + tx-in-ctx 接缝 |
| `examples/sqliteadapter/tenant.go` | 租户作用域（`NewTenant*` + fail-closed） |
| `examples/sqliteadapter/*_test.go` | 各面契约套件接线 + SQLite 特有判据 |
| `cmd/storagecheck/main.go` | 选型判据 CLI 装配 |
| `cmd/storagecheck/check.go` | 三条判据的执行体 |
| `cmd/storagecheck/check_test.go` | 判据用例（含反向验证） |
| `docs/STORAGE-SELECTION.yaml` | 选型声明面（storage → 能力 → 代码落点） |

**修改**

| 文件 | 改动 |
|---|---|
| `architecture-policy.yaml` | 删 `sqlitestore` 模块；增 `sqliteadapter` 模块；`ext_prefixes` 增 `modernc.org`；`scan_roots` 换名 |
| `api-lifecycle.yaml` | 删 `examples/sqlitastore`；增 `examples/sqliteadapter` |
| `go.mod` / `go.sum` | 引入 `modernc.org/sqlite` |
| `examples/pgstore/effect.go` | `withinTx` 认 ctx 里已有的 tx |
| `examples/pgstore/store.go`（新建） | `Store` 门面 + `Apply` |
| `examples/pgstore/pg_apply_test.go`（新建） | 跨面同事务提交 + 回滚整批 |
| `Makefile` | 接 `storagecheck`；删 `sqlitestore` 相关 |
| `docs/DEPLOY-MATRIX.yaml` | 能力证据改指 sqliteadapter；场景 storage 改指 |
| `docs/USAGE.md` | 机器口径块同步 + 新增选型规则表 |
| `README.md` | 包表与文档一览 |
| `AGENTS.md` / `docs/AI-GUIDE.md` | 四面事务域表述改为两引擎同构 |
| `archcheck/error_sentinel_test.go` | 删 sqlitestore 两条豁免 |
| `taskboard/claim_gate_test.go` | 注释里的实现枚举 |
| `docs/DISTRIBUTED-EVOLUTION-20261117.md` | 新增 §28；§24 落点更新；§9 单号登记 |

**删除**：`examples/sqlitestore/`（22 个 `.go` 文件，3358 行）——**已完成**（Task 10，2026-11-20，commit `43b5834`）

---

## Task 1: 登记第二个驱动破例（policy 先行）

**Files:**
- Modify: `architecture-policy.yaml`
- Modify: `api-lifecycle.yaml`
- Modify: `go.mod`

**Interfaces:**
- Consumes: 无
- Produces: 模块 id `sqliteadapter`（roots `examples/sqliteadapter`）、受管外部前缀 `modernc.org`；后续 Task 2 的代码可被依赖方向门禁接受

- [ ] **Step 1: 确认依赖版本可用（先探，再改 policy）**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
ls ~/go/pkg/mod/cache/download/modernc.org/sqlite/@v/*.zip 2>/dev/null
ls ~/go/pkg/mod/cache/download/modernc.org/libc/@v/*.zip 2>/dev/null
```

Expected: `sqlite@v1.61.0.zip` 与 `libc@v1.79.0.zip` 存在（v1.62.0 需要的 `libc v1.80.1` 缺失）。

若需要 v1.62.0：先试 `GOPROXY=https://goproxy.cn,direct go mod download`，失败则改
`GOPROXY=https://proxy.golang.org,direct`；两者都失败则**改用 v1.61.0**（cache 里现成），
并在 Task 2 的 doc.go 里写明版本。

- [ ] **Step 2: 在 `architecture-policy.yaml` 的 `modules` 段登记 `sqliteadapter`**

在 `sqlitestore` 条目**之后**插入（该条目本身在 Task 7 删）：

```yaml
  # 真 SQLite 参考实现（2026-11-19 新增，issue #424）：单机持久化的 SQLite
  # 落库形态——四套契约面 + 控制面 + 下游栅栏 + 租户作用域 + 跨面 Apply。
  #
  # 为什么 sqlite 驱动可以进依赖面：只进**这个示例子包**的依赖图（内核各包
  # requires 均不含它），且驱动是纯 Go（CGO_ENABLED=0 交叉构建矩阵不受影响）。
  # 与 pgx 同款理由，两者是仅有的两个驱动破例。
  #
  # 代价与拆除条件：主 module 多一棵传递依赖树（modernc.org/libc 等约 12 个
  # module）。拆除条件——宿主形态全部转 PG，或驱动改由宿主自带而本仓退回
  # 「SQLite 落库形态规范」形态（不提供可跑适配器），则本破例可撤。
  - id: sqliteadapter
    roots: [examples/sqliteadapter]
    requires: [checkpoint, controlplane, eventbus, sideeffect, taskboard, tenant, "ext:github.com/oekit/pkg", "ext:modernc.org/sqlite"]
    owner: harness
    managed: true
    notes: |
      SQLite(WAL) 参考实现：单文件库，PRAGMA journal_mode=WAL /
      synchronous=FULL / busy_timeout=5000，写事务一律 BEGIN IMMEDIATE
      （先拿写锁再干活）。事件 ID 由每租户一行水位表铸造（UPDATE … RETURNING，
      回滚不烧号 ⇒ 无空洞）。存储时钟用 unixepoch('subsec')（毫秒级，
      非 BEGIN 冻结，取时刻在拿锁之后）。四个面共用一份事务域（Store.Apply）。
      契约判定在共享契约套件（与 PG 同一段判据）。
```

- [ ] **Step 3: 在 `ext_prefixes` 增 `modernc.org`**

在 `- github.com/jackc` 之后加：

```yaml
    # SQLite 驱动（2026-11-19 起，issue #424）：只被 examples/sqliteadapter 的
    # requires 引用（SQLite 参考实现）。与 pgx 同款定位——内核包若 import 它，
    # 依赖方向门禁当场红（内核不实现存储，判据面不拖驱动）。
    - modernc.org
```

- [ ] **Step 4: `scan_roots` 换名**

把 `scan_roots` 末尾的 `examples/sqlitestore` 改为 `examples/sqliteadapter`（Task 7
删包时若已改名则跳过本步）。

- [ ] **Step 5: `api-lifecycle.yaml` 换条目**

把 `- pkg: examples/sqlitestore` 那一整段替换为：

```yaml
  # ── 2026-11-19 新增（issue #424）──
  - pkg: examples/sqliteadapter
    level: Experimental
    since: v0.50.0
    reason: SQLite(WAL) 单机持久化参考实现——真 SQLite 落库形态（驱动为 modernc.org/sqlite，纯 Go）。路径与表结构随契约套件与参考部署演进，不构成任何兼容承诺。
```

- [ ] **Step 6: `go.mod` 引入驱动**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
go get modernc.org/sqlite@v1.61.0
go mod tidy
grep -n 'modernc.org/sqlite' go.mod
```

Expected: `go.mod` 的 `require` 块出现 `modernc.org/sqlite v1.61.0`；间接依赖出现
`modernc.org/libc` 等。

- [ ] **Step 7: 跑架构门禁确认登记生效**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check
```

Expected: PASS。（此时代码还没建，`roots` 指向空目录会被「陈旧包根必须当天即红」
的自校验拦下——若如此，先建 `examples/sqliteadapter/doc.go` 占位再跑。）

- [ ] **Step 8: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add architecture-policy.yaml api-lifecycle.yaml go.mod go.sum
git commit -m "chore(policy): 登记 SQLite 驱动破例——examples/sqliteadapter 与 modernc.org 前缀（#424）

先改 policy 再写代码：依赖方向门禁要求新包先登记，否则新代码当场红。
驱动为纯 Go（modernc.org/sqlite），CGO_ENABLED=0 交叉构建矩阵不受影响。
"
```

---

## Task 2: 适配器骨架：连接、schema、执行位置抽象

**Files:**
- Create: `examples/sqliteadapter/doc.go`
- Create: `examples/sqliteadapter/store.go`
- Create: `examples/sqliteadapter/schema.go`
- Create: `examples/sqliteadapter/exec.go`
- Create: `examples/sqliteadapter/store_test.go`

**Interfaces:**
- Consumes: Task 1 登记的模块 id `sqliteadapter`
- Produces:
  - `func Open(path string, opts ...Option) (*Store, error)`
  - `type Store struct { db *sql.DB; schema string; ... }`
  - `func (s *Store) Close() error`
  - `func (s *Store) EnsureSchema(ctx context.Context) error`
  - `func (s *Store) UseClock(now func() time.Time)`
  - `type exec struct { db *sql.DB; tx *sql.Tx }`
  - `func (e exec) run(ctx context.Context, fn func(tx *sql.Tx) error) error`
  - `func (e exec) now(ctx context.Context, tx *sql.Tx) (time.Time, error)`
  - `func (e exec) clockOverride() func() time.Time`

- [ ] **Step 1: 写失败测试：骨架行为**

`examples/sqliteadapter/store_test.go`：

```go
package sqliteadapter

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpenCreatesSchemaAndIsReopenable 打开即建表；关掉重开仍在（持久化）。
func TestOpenCreatesSchemaAndIsReopenable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	defer s2.Close()
	if err := s2.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("重开建表（幂等）失败: %v", err)
	}
}

// TestOpenRejectsEmptyPath 空路径是装配期编程错误，fail fast。
func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("空路径必须报错（不得静默开一个匿名库）")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestOpen' -count=1
```

Expected: FAIL —— `undefined: Open`。

- [ ] **Step 3: 建 schema.go（表结构）**

`examples/sqliteadapter/schema.go` —— 表名沿用 `gak_` 前缀（与 PG 的 `pgref_` 同款
「参考表」命名，标清楚是参考实现不是生产协议）：

```go
package sqliteadapter

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaDDL 一套参考表（幂等：全部 CREATE TABLE IF NOT EXISTS）。
//
// 逐表对应关系见 doc.go。与 pgstore 的 pgref_* 同形：同样的键、同样的条件写、
// 同样的「一条带条件的 upsert 承担比较与写入」形态——只有方言不同。
const schemaDDL = `
CREATE TABLE IF NOT EXISTS gak_board_task (
    id     TEXT PRIMARY KEY,
    title  TEXT    NOT NULL DEFAULT '',
    deps   TEXT    NOT NULL DEFAULT '[]',
    state  TEXT    NOT NULL,
    owner  TEXT,
    token  TEXT,
    fence  INTEGER NOT NULL DEFAULT 0,
    until  INTEGER,
    seq    INTEGER NOT NULL DEFAULT 0,
    run_id TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS gak_effect_action (
    tenant          TEXT    NOT NULL DEFAULT '',
    key             TEXT    NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    result          BLOB,
    uncertain       INTEGER NOT NULL DEFAULT 0,
    reason          TEXT    NOT NULL DEFAULT '',
    intent_resource TEXT,
    intent_fence    INTEGER,
    intent_holder   TEXT,
    intent_expires  INTEGER,
    recon_holder    TEXT,
    recon_expires   INTEGER,
    id_resource     TEXT,
    id_safety       INTEGER,
    id_payload_hash TEXT,
    PRIMARY KEY (tenant, key)
);

CREATE TABLE IF NOT EXISTS gak_effect_resource (
    tenant   TEXT    NOT NULL DEFAULT '',
    resource TEXT    NOT NULL,
    fence    INTEGER NOT NULL DEFAULT 0,
    holder   TEXT,
    until    INTEGER,
    PRIMARY KEY (tenant, resource)
);

CREATE TABLE IF NOT EXISTS gak_downstream (
    tenant     TEXT    NOT NULL DEFAULT '',
    resource   TEXT    NOT NULL,
    seen_fence INTEGER NOT NULL,
    value      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (tenant, resource)
);

CREATE TABLE IF NOT EXISTS gak_snapshot (
    run_id TEXT PRIMARY KEY,
    seq    INTEGER NOT NULL,
    status TEXT    NOT NULL DEFAULT '',
    done   TEXT    NOT NULL DEFAULT '[]',
    fields TEXT    NOT NULL DEFAULT ''
);

-- 每租户一行水位：事件 ID 的铸造点（UPDATE … RETURNING 拿号并持行锁到提交）。
-- 回滚时 committed 随事务回滚 ⇒ 下次分配复用同一 ID ⇒ 序列空洞结构性不存在。
CREATE TABLE IF NOT EXISTS gak_event_watermark (
    tenant    TEXT PRIMARY KEY,
    committed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS gak_event (
    tenant      TEXT    NOT NULL DEFAULT '',
    id          INTEGER NOT NULL,
    session_id  TEXT    NOT NULL DEFAULT '',
    case_id     TEXT    NOT NULL DEFAULT '',
    step_id     INTEGER,
    type        TEXT    NOT NULL DEFAULT '',
    level       TEXT    NOT NULL DEFAULT 'info',
    payload     TEXT    NOT NULL DEFAULT '{}',
    occurred_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant, id)
);

CREATE TABLE IF NOT EXISTS gak_tail_cursor (
    consumer   TEXT PRIMARY KEY,
    cursor_pos INTEGER NOT NULL DEFAULT 0 CHECK (cursor_pos >= 0)
);

CREATE TABLE IF NOT EXISTS gak_run (
    id     TEXT PRIMARY KEY,
    status TEXT    NOT NULL DEFAULT '',
    gen    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS gak_control_version (
    id  INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    ver INTEGER NOT NULL DEFAULT 0
);

INSERT INTO gak_control_version (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS gak_control_effect (
    key       TEXT PRIMARY KEY,
    uncertain INTEGER NOT NULL DEFAULT 0,
    unsafe    INTEGER NOT NULL DEFAULT 0,
    resolved  TEXT    NOT NULL DEFAULT ''
);
`

// EnsureSchema 建好全部参考表（幂等，可重复执行）。
//
// 全部语句在**一个事务**里执行：建表中途失败不留半截 schema。
// DDL 的写锁升级在 SQLITE_BUSY 上不等 busy_timeout（实测），故建表只在
// 装配期做，不在运行期做。
func (s *Store) EnsureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaDDL)
	if err != nil {
		return fmt.Errorf("sqliteadapter: 建表失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 建 exec.go（执行位置抽象）**

`examples/sqliteadapter/exec.go`：

```go
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
func (e exec) now(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	if e.clock != nil {
		return e.clock(), nil
	}
	var ms int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(unixepoch('subsec') * 1000 AS INTEGER)`).Scan(&ms); err != nil {
		return time.Time{}, fmt.Errorf("sqliteadapter: 读存储时钟失败: %w", err)
	}
	return time.UnixMilli(ms).UTC(), nil
}
```

- [ ] **Step 5: 建 store.go（门面骨架）**

`examples/sqliteadapter/store.go`：

```go
// Package sqliteadapter 单机持久化的**真 SQLite 参考实现**。
//
// # 它是什么
//
// 把内核的四套存储中立契约（taskboard.Board / sideeffect.Store /
// checkpoint.StoreCAS / eventbus.Store）+ 控制面（controlplane.Store）+
// 下游栅栏（sideeffect.FencedSink）+ 尾随游标（eventbus.CursorStore）
// 落到一个 SQLite(WAL) 单文件库里，四个面**共用一份事务域**。
//
// # 与 examples/pgstore 的关系
//
// **形态同构、代码不共用**：表结构、事务边界、时钟口径、租户作用域、跨面
// Apply 逐项对位；但判据逻辑两包各写一份——共用会把两个引擎耦在一起。
// 正确性由**同一套契约套件**（boardtest / sidetest / RunSinkContract）对齐。
//
// # 选型口径
//
// 本包只适合**单机单副本**。多副本 / 跨机一律用 PostgreSQL：SQLite 的单文件
// 锁跨机不可靠。判据由 cmd/storagecheck 机判。
//
// # 已知边界
//
//   - 存储时钟是毫秒级（实测）：租约 TTL 建议 ≥ 1s。
//   - DDL 的写锁升级在 SQLITE_BUSY 上不等 busy_timeout：建表只在装配期。
//   - 单写者模型：多写者下退化为串行，busy_timeout 到点即失败（可判、
//     不静默丢写，但降低可用性）。
package sqliteadapter

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// dsn 打开参数：
//   - _txlock=immediate：写事务一律 BEGIN IMMEDIATE（先拿写锁再干活）。
//   - _busy_timeout=5000：跨进程写锁的等待上限（不静默丢写）。
//   - _journal_mode=WAL：读不阻塞写，崩溃后按 WAL 重放。
//
// synchronous=FULL 用 PRAGMA 单独设（驱动参数形式为 _synchronous=FULL）。
const dsnParams = "?_txlock=immediate&_busy_timeout=5000&_journal_mode=WAL&_synchronous=FULL"

// Option 装配期选项。
type Option func(*Store)

// WithClock 注入时钟（契约套件的硬要求；生产不装 ⇒ 读存储时钟）。
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.clock = now }
}

// Store 一个 SQLite 后端：四个面 + 控制面 + 栅栏 + 游标，共用一份事务域。
type Store struct {
	db    *sql.DB
	path  string
	clock func() time.Time
}

// Open 打开（或建）一个 SQLite 后端。
//
// path 为空是装配期编程错误，fail fast（不得静默开一个匿名库——那会让
// 「重启后状态还在」这条判据无从验证）。
func Open(path string, opts ...Option) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqliteadapter: 数据文件路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sqliteadapter: 建数据目录失败: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+dsnParams)
	if err != nil {
		return nil, fmt.Errorf("sqliteadapter: 打开数据库失败: %w", err)
	}
	// 单写连接：进程内多个 goroutine 的写串行化。
	// ⚠️ 职责**只限进程内**——跨进程串行化靠 BEGIN IMMEDIATE + 文件锁。
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Close 关闭数据库（不删除数据）。
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// UseClock 注入时钟（boardtest.ClockedBoard / sidetest.ClockedStore 硬要求）。
func (s *Store) UseClock(now func() time.Time) {
	s.clock = now
}

// DB 暴露底层句柄（装配期与测试用；运行期不直接用）。
func (s *Store) DB() *sql.DB { return s.db }

// Path 数据文件路径（对账用）。
func (s *Store) Path() string { return s.path }
```

- [ ] **Step 6: 建 doc.go 的骨架（完整内容在 Task 3 补）**

先写一个最小可编译的 doc.go（包注释），Task 3 扩写：

```go
package sqliteadapter
```

- [ ] **Step 7: 跑测试确认通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -count=1
```

Expected: PASS。

- [ ] **Step 8: 跑门禁**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/sqliteadapter/
```

Expected: PASS。

- [ ] **Step 9: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add examples/sqliteadapter/ go.mod go.sum
git commit -m "feat(examples/sqliteadapter): 适配器骨架——连接、schema、执行位置抽象

判据单一来源的前提：exec{db, tx} 让独立提交与事务内走同一套判据函数。
时钟用 unixepoch('subsec')（毫秒级，实测 0.9999ms 最小增量），取时刻在
拿到写锁之后。#424
"
```

---

## Task 3: 任务面（taskboard.Board / RenewingBoard）

**Files:**
- Create: `examples/sqliteadapter/board.go`
- Create: `examples/sqliteadapter/board_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Store` / `exec` / `schemaDDL`
- Produces:
  - `func (s *Store) Board() *BoardStore`
  - `type BoardStore struct { exec exec }`
  - `func (b *BoardStore) List(ctx context.Context) ([]taskboard.Task, error)`
  - `func (b *BoardStore) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error)`
  - `func (b *BoardStore) Release(ctx context.Context, id taskboard.TaskID, token string) error`
  - `func (b *BoardStore) Complete(ctx context.Context, id taskboard.TaskID, token string) error`
  - `func (b *BoardStore) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error)`
  - `func (b *BoardStore) UseClock(now func() time.Time)`
  - `func (b *BoardStore) Seed(tasks ...taskboard.Task)`

- [ ] **Step 1: 写失败测试（契约套件接线）**

`examples/sqliteadapter/board_test.go`：

```go
package sqliteadapter

import (
	"path/filepath"
	"testing"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// TestBoardContract 跑任务板的共享契约套件（boardtest 全套，含 10 与 100
// 并发认领档）。
func TestBoardContract(t *testing.T) {
	boardtest.Run(t, func(t *testing.T, clock *boardtest.FakeClock) boardtest.ClockedBoard {
		s, err := Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("打开失败: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		if err := s.EnsureSchema(t.Context()); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
		return s.Board()
	})
}

// TestBoardSeedPersistsAcrossReopen 摆进去的任务在重开后仍在（持久化判据）。
func TestBoardSeedPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	if err := s.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	s.Board().Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	if err := s.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	defer s2.Close()
	tasks, err := s2.Board().List(t.Context())
	if err != nil {
		t.Fatalf("读任务失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("重开后任务丢失: %+v", tasks)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestBoard' -count=1
```

Expected: FAIL —— `undefined: s.Board`。

- [ ] **Step 3: 实现 board.go**

`examples/sqliteadapter/board.go`：

```go
package sqliteadapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// boardCols 任务行的列（与 pgstore 同序）。
const boardCols = `id, title, deps, state, owner, token, fence, until, seq, run_id`

// BoardStore 任务面（taskboard.Board + taskboard.RenewingBoard）。
type BoardStore struct {
	exec exec
}

// 编译期断言：本实现满足任务板的两套契约。
var (
	_ taskboard.Board         = (*BoardStore)(nil)
	_ taskboard.RenewingBoard = (*BoardStore)(nil)
)

// Board 任务面（与其它面共用同一份事务域）。
func (s *Store) Board() *BoardStore {
	return &BoardStore{exec: exec{db: s.db, clock: s.clock}}
}

// UseClock 注入时钟（boardtest.ClockedBoard 硬要求）。
func (b *BoardStore) UseClock(now func() time.Time) {
	b.exec.clock = now
}

// boardRow 任务行（内部表示）。
type boardRow struct {
	id     taskboard.TaskID
	title  string
	deps   []taskboard.TaskID
	state  taskboard.State
	owner  string
	token  string
	fence  uint64
	until  sql.NullInt64
	seq    uint64
	runID  string
}

// held 判断租约是否仍在有效期内（now 为本次操作时刻）。
//
// ⚠️ **不得自己写这份判据**：`taskboard.LeaseHeld(t Task, now time.Time) bool`
// 是「活持有」的单点判据（issue #335），实现各写一份必然漂——controlplane.MemStore
// 曾压根不判过期 ⇒ 持有者崩掉后任务被永久占用，与 Board.Claim 的「被别人**活着**
// 持有」方向相反。本方法只做「行 → Task」的投影，判定交给 LeaseHeld。
func (r *boardRow) held(now time.Time) bool {
	return taskboard.LeaseHeld(r.task(), now)
}

// task 把行投影成 taskboard.Task。
func (r *boardRow) task() taskboard.Task {
	t := taskboard.Task{
		ID:    r.id,
		Title: r.title,
		Deps:  r.deps,
		State: r.state,
		Owner: r.owner,
		Seq:   r.seq,
	}
	if r.token != "" {
		t.Lease = taskboard.Lease{
			Token: r.token,
			Fence: r.fence,
		}
		if r.until.Valid {
			t.Lease.Until = time.UnixMilli(r.until.Int64).UTC()
		}
	}
	return t
}

// scanBoardRow 读一行任务。
func scanBoardRow(row interface{ Scan(...any) error }) (*boardRow, error) {
	var (
		id    string
		title string
		deps string
		state string
		owner sql.NullString
		token sql.NullString
		fence int64
		until sql.NullInt64
		seq   int64
		runID string
	)
	if err := row.Scan(&id, &title, &deps, &state, &owner, &token, &fence, &until, &seq, &runID); err != nil {
		return nil, err
	}
	var ids []taskboard.TaskID
	if deps != "" && deps != "[]" {
		if err := json.Unmarshal([]byte(deps), &ids); err != nil {
			return nil, errors.Wrap(err, "sqliteadapter: 解析依赖失败", "deps", deps)
		}
	}
	return &boardRow{
		id:    taskboard.TaskID(id),
		title: title,
		deps:  ids,
		state: taskboard.State(state),
		owner: owner.String,
		token: token.String,
		fence: uint64(fence),
		until: until,
		seq:   uint64(seq),
		runID: runID,
	}, nil
}

// List 返回当前全部任务的快照（一致快照：单次查询读全量）。
func (b *BoardStore) List(ctx context.Context) ([]taskboard.Task, error) {
	rows, err := b.exec.db.QueryContext(ctx, `SELECT `+boardCols+` FROM gak_board_task`)
	if err != nil {
		return nil, errors.Wrap(err, "sqliteadapter: 读任务失败")
	}
	defer rows.Close()
	var out []taskboard.Task
	for rows.Next() {
		r, err := scanBoardRow(rows)
		if err != nil {
			return nil, errors.Wrap(err, "sqliteadapter: 解析任务行失败")
		}
		out = append(out, r.task())
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "sqliteadapter: 读任务失败")
	}
	return out, nil
}

// Seed 摆初始任务（任务拓扑是宿主资产；契约没有「创建」入口）。
//
// 与 pgstore.BoardStore.Seed 同形态：同 id 重摆覆盖内容并清租约，接管代次
// 水位保留；落库失败 panic（装载入口不容静默失败）。
func (b *BoardStore) Seed(tasks ...taskboard.Task) {
	for _, task := range tasks {
		if err := b.seedOne(task); err != nil {
			panic(fmt.Sprintf("sqliteadapter: Seed 摆任务失败（装载入口不容静默失败）: %v", err))
		}
	}
}

func (b *BoardStore) seedOne(task taskboard.Task) error {
	deps, err := json.Marshal(task.Deps)
	if err != nil {
		return errors.Wrap(err, "sqliteadapter: 序列化依赖失败")
	}
	return b.exec.run(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`INSERT INTO gak_board_task (id, title, deps, state, owner, token, fence, until, seq, run_id)
			 VALUES (?, ?, ?, ?, NULL, NULL, 0, NULL, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   title = excluded.title, deps = excluded.deps, state = excluded.state,
			   owner = NULL, token = NULL, until = NULL, seq = excluded.seq`,
			string(task.ID), task.Title, string(deps), string(task.State), task.Seq, "")
		return err
	})
}
```

**⚠️ 实施注意**：上面的 `Claim` / `Release` / `Complete` / `Renew` 四个方法**不在本步骤
内**——它们依赖 Task 4 的 `exec` 事务内变体与令牌铸造辅助。Task 3 只交付
`List` / `Seed` / 行投影；契约套件在 Task 4 完成后才绿。⇒ **Step 2 的测试此时应失败，
不是通过**；把「TestBoardContract」暂时标记为待接线，Task 4 末尾再启用。

- [ ] **Step 4: 写 board.go 的四个写方法（令牌铸造与条件更新）**

追加到 `board.go`：

```go
// mintToken 铸造一枚令牌（存储侧铸造，调用方只给 TTL）。
func mintToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.Wrap(err, "sqliteadapter: 铸造令牌失败")
	}
	return hex.EncodeToString(b[:]), nil
}

// Claim 认领一个任务：事务内的读-判-写（读-判-写不可分割 ⇒ 并发认领
// 只有一个赢家）。
//
// 流程：BEGIN IMMEDIATE（先拿写锁）→ 读任务行 → 判终态/依赖/活租约 →
// 铸造令牌与接管代次 → 条件更新 → 提交。
func (b *BoardStore) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	var out taskboard.Task
	err := b.exec.run(ctx, func(tx *sql.Tx) error {
		now, err := b.exec.now(ctx, tx)
		if err != nil {
			return err
		}
		r, err := scanBoardRow(tx.QueryRowContext(ctx, `SELECT `+boardCols+` FROM gak_board_task WHERE id = ?`, string(id)))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(taskboard.ErrNotFound, "sqliteadapter: 任务不存在", "task", string(id))
			}
			return errors.Wrap(err, "sqliteadapter: 读任务失败")
		}
		// ⚠️ **必须调用 taskboard.State.Claimable()**：它是单点判据（issue #360），
		// 返回契约规定的哨兵（词表外 → ErrUnknownState，终态 → ErrTerminal）。
		// taskboard/claim_gate_test.go 有 **AST 门禁**判「Claim 里真的调了它」——
		// 不得只判 Terminal()（未知状态不是终态，只看终态会让脏状态行照常领到租约）。
		if err := r.state.Claimable(); err != nil {
			return errors.Wrap(err, "sqliteadapter: 任务状态不可认领",
				"task", string(id), "state", string(r.state))
		}
		if r.held(now) {
			return errors.Wrap(taskboard.ErrTaken, "sqliteadapter: 任务已被他人认领", "task", string(id))
		}
		if len(r.deps) > 0 {
			if err := depsDone(ctx, tx, r.deps); err != nil {
				return err
			}
		}
		token, err := mintToken()
		if err != nil {
			return err
		}
		fence := r.fence + 1
		var until any
		if req.TTL > 0 {
			until = now.Add(req.TTL).UnixMilli()
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE gak_board_task SET state = ?, owner = ?, token = ?, fence = ?, until = ?, seq = seq + 1
			 WHERE id = ?`,
			string(taskboard.StateClaimed), string(agent), token, fence, until, string(id))
		if err != nil {
			return errors.Wrap(err, "sqliteadapter: 认领写入失败")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.Wrap(taskboard.ErrTaken, "sqliteadapter: 任务已被他人认领", "task", string(id))
		}
		r.state = taskboard.StateClaimed
		r.owner = string(agent)
		r.token = token
		r.fence = fence
		r.until = sql.NullInt64{}
		if until != nil {
			r.until = sql.NullInt64{Int64: until.(int64), Valid: true}
		}
		r.seq++
		out = r.task()
		return nil
	})
	if err != nil {
		return taskboard.Task{}, err
	}
	return out, nil
}

// depsDone 判依赖是否全部完成。
func depsDone(ctx context.Context, tx *sql.Tx, deps []string) error {
	for _, d := range deps {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM gak_board_task WHERE id = ?`, d).Scan(&state)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(taskboard.ErrBlocked, "sqliteadapter: 依赖不存在", "dep", d)
			}
			return errors.Wrap(err, "sqliteadapter: 读依赖失败", "dep", d)
		}
		// 依赖「完成」的判据 = 状态是 done（与 pgstore 同款写法）。
		// ⚠️ 注意：**failed 不算完成**（issue #375：failed 是非终态、可重做，
		// 但它也**不是**「已完成」——体检面同样按此判）。
		if taskboard.State(state) != taskboard.StateDone {
			return errors.Wrap(taskboard.ErrBlocked, "sqliteadapter: 依赖未完成", "dep", d)
		}
	}
	return nil
}

// releaseOrComplete 带条件的状态更新（只认 token）。
func releaseOrComplete(ctx context.Context, tx *sql.Tx, id taskboard.TaskID, token, state string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE gak_board_task SET state = ?, owner = NULL, token = NULL, until = NULL, seq = seq + 1
		 WHERE id = ? AND token = ? AND state = ?`,
		state, string(id), token, string(taskboard.StateClaimed))
	if err != nil {
		return errors.Wrap(err, "sqliteadapter: 状态更新失败", "task", string(id))
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 区分「不存在」与「令牌不匹配」：再读一次。
		var got string
		err2 := tx.QueryRowContext(ctx, `SELECT state FROM gak_board_task WHERE id = ?`, string(id)).Scan(&got)
		if err2 != nil {
			if errors.Is(err2, sql.ErrNoRows) {
				return errors.Wrap(taskboard.ErrNotFound, "sqliteadapter: 任务不存在", "task", string(id))
			}
			return errors.Wrap(err2, "sqliteadapter: 读任务失败", "task", string(id))
		}
		return errors.Wrap(taskboard.ErrLeaseLost, "sqliteadapter: 租约令牌不匹配", "task", string(id))
	}
	return nil
}

// Release 归还认领（凭 token）。
func (b *BoardStore) Release(ctx context.Context, id taskboard.TaskID, token string) error {
	return b.exec.run(ctx, func(tx *sql.Tx) error {
		return releaseOrComplete(ctx, tx, id, token, string(taskboard.StateOpen))
	})
}

// Complete 标记完成（凭 token）。
func (b *BoardStore) Complete(ctx context.Context, id taskboard.TaskID, token string) error {
	return b.exec.run(ctx, func(tx *sql.Tx) error {
		return releaseOrComplete(ctx, tx, id, token, string(taskboard.StateDone))
	})
}

// Renew 续期并同时确认持有资格（凭 token；不递增 Fence、不换 token）。
func (b *BoardStore) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	var out taskboard.Task
	err := b.exec.run(ctx, func(tx *sql.Tx) error {
		now, err := b.exec.now(ctx, tx)
		if err != nil {
			return err
		}
		r, err := scanBoardRow(tx.QueryRowContext(ctx, `SELECT `+boardCols+` FROM gak_board_task WHERE id = ?`, string(id)))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(taskboard.ErrNotFound, "sqliteadapter: 任务不存在", "task", string(id))
			}
			return errors.Wrap(err, "sqliteadapter: 读任务失败")
		}
		if r.token != token {
			return errors.Wrap(taskboard.ErrLeaseLost, "sqliteadapter: 租约令牌不匹配", "task", string(id))
		}
		if !r.held(now) {
			return errors.Wrap(taskboard.ErrLeaseLost, "sqliteadapter: 租约已过期，资格终止", "task", string(id))
		}
		var until any
		if req.TTL > 0 {
			until = now.Add(req.TTL).UnixMilli()
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE gak_board_task SET until = ?, seq = seq + 1 WHERE id = ? AND token = ?`,
			until, string(id), token)
		if err != nil {
			return errors.Wrap(err, "sqliteadapter: 续期写入失败", "task", string(id))
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.Wrap(taskboard.ErrLeaseLost, "sqliteadapter: 租约令牌不匹配", "task", string(id))
		}
		r.until = sql.NullInt64{}
		if until != nil {
			r.until = sql.NullInt64{Int64: until.(int64), Valid: true}
		}
		r.seq++
		out = r.task()
		return nil
	})
	if err != nil {
		return taskboard.Task{}, err
	}
	return out, nil
}
```

**已核实的真实 API（本计划写作时逐个查过 `taskboard/taskboard.go`）**：

| 符号 | 真实形态 | 用途 |
|---|---|---|
| `taskboard.State.Claimable() error` | **方法，返回 error** | Claim 前置判定（issue #360 单点；AST 门禁查） |
| `taskboard.State.Terminal() bool` | 方法 | 只有 `done` 是终态 |
| `taskboard.State.Valid() bool` | 方法 | 词表外判定 |
| `taskboard.LeaseHeld(t Task, now time.Time) bool` | 自由函数 | 「活持有」单点判据（issue #335） |
| `taskboard.StateOpen/StateClaimed/StateDone/StateFailed` | 常量 | 状态取值 |

⚠️ **没有** `State.Satisfies` 这个符号——依赖「已完成」的判据是
`taskboard.State(state) != taskboard.StateDone`（与 pgstore 同款）。

- [ ] **Step 5: 跑测试确认契约套件通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestBoard' -count=1 -v
```

Expected: PASS（boardtest 全套子测试绿）。若某条红了，**读那条子测试的判据文本**再改
实现——不要改套件。

- [ ] **Step 6: 跑门禁**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/sqliteadapter/ && wc -l examples/sqliteadapter/*.go
```

Expected: PASS；无文件超 400 行。

- [ ] **Step 7: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add examples/sqliteadapter/board.go examples/sqliteadapter/board_test.go
git commit -m "feat(examples/sqliteadapter): 任务面——taskboard.Board 与 RenewingBoard

Claim 是事务内的读-判-写（BEGIN IMMEDIATE 先拿写锁 ⇒ 并发认领恰好一个
赢家）；Release/Complete/Renew 只认 token，令牌不匹配即 ErrLeaseLost。
boardtest 全套（含 10 与 100 并发认领档）在 unit 层真跑。#424
"
```

---

## Task 4: 副作用闸（sideeffect.Store）

**Files:**
- Create: `examples/sqliteadapter/effect.go`
- Create: `examples/sqliteadapter/effect_rows.go`
- Create: `examples/sqliteadapter/effect_test.go`

**Interfaces:**
- Consumes: Task 2 的 `exec` / `schemaDDL`；Task 3 的 `mintToken`
- Produces:
  - `func (s *Store) Effects() *EffectStore`
  - `type EffectStore struct { exec exec; tenantScoped bool }`
  - `func NewEffectStore(db *sql.DB) *EffectStore`（旧布局：tenant 取动作自带值）
  - `func NewTenantEffectStore(db *sql.DB) *EffectStore`（租户作用域）
  - `func (s *EffectStore) Acquire(ctx context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Outcome, sideeffect.Lease, sideeffect.Result, error)`
  - `func (s *EffectStore) Complete(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, res sideeffect.Result) (sideeffect.Result, error)`
  - `func (s *EffectStore) Release(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease) error`
  - `func (s *EffectStore) Abandon(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, reason string) error`
  - `func (s *EffectStore) Renew(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, ttl time.Duration) (sideeffect.Lease, error)`
  - `func (s *EffectStore) MarkUncertain(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, reason string) error`
  - `func (s *EffectStore) ResolveUncertain(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error)`
  - `func (s *EffectStore) ResolveUncertainOwned(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution, holder string) (sideeffect.Outcome, sideeffect.Result, error)`
  - `func (s *EffectStore) BeginReconcile(ctx context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Reconcile, error)`
  - `func (s *EffectStore) UseClock(now func() time.Time)`
  - `func (s *EffectStore) TenantScoped() bool`

- [ ] **Step 1: 写失败测试（契约套件接线）**

`examples/sqliteadapter/effect_test.go`：

```go
package sqliteadapter

import (
	"path/filepath"
	"testing"

	"github.com/yi-nology/goagent-kernel/sidetest"
)

// TestEffectContract 跑副作用闸的共享契约套件（sidetest 全套 33 条）。
func TestEffectContract(t *testing.T) {
	sidetest.Run(t, func(t *testing.T, clock *sidetest.FakeClock) sidetest.ClockedStore {
		s, err := Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("打开失败: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		if err := s.EnsureSchema(t.Context()); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
		return s.Effects()
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestEffect' -count=1
```

Expected: FAIL —— `undefined: s.Effects`。

- [ ] **Step 3: 参照 pgstore 实现 effect_rows.go**

读 `examples/pgstore/effect_rows.go`（249 行）与 `examples/pgstore/effect_acquire.go`
（187 行），**逐条照搬判据**，只改方言：

| PG | SQLite |
|---|---|
| `SELECT … FOR UPDATE` | `BEGIN IMMEDIATE` 已持写锁 ⇒ **不加** `FOR UPDATE` |
| `$1` / `$2` 占位符 | `?` 占位符 |
| `TIMESTAMPTZ` | `INTEGER`（Unix 毫秒） |
| `BYTEA` | `BLOB` |
| `JSONB` | `TEXT`（JSON 字符串） |
| `clock_timestamp()` | `unixepoch('subsec')*1000` |
| `ON CONFLICT (tenant, key) DO UPDATE … WHERE …` | 同款（SQLite 支持） |
| `ANY($2)`（数组） | 不支持 ⇒ 逐条 `IN (?, ?, …)` 或逐行写 |

**锁序必须一致**：资源行 → 动作行（PG 的 `pgstore/effect_acquire.go` 是这个序），
否则两个进程各持一半锁 ⇒ 死锁。

- [ ] **Step 4: 实现 effect.go 的 Acquire（三判合一）**

`examples/sqliteadapter/effect.go`，核心形态（完整实现按 `pgstore/effect_acquire.go`
逐条改写）：

```go
// Acquire 取得一次动作的执行权：三判合一（已完成 > 结果未知 > 执行中 >
// 资源被占 > 授予）在**一个事务**里完成。
//
// 判据顺序不可改：先看动作行（已完成/未知/执行中），再看资源行（被占）。
// 锁序同样是资源行 → 动作行。
func (s *EffectStore) Acquire(ctx context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Outcome, sideeffect.Lease, sideeffect.Result, error) {
	var (
		outcome sideeffect.Outcome
		lease   sideeffect.Lease
		result  sideeffect.Result
	)
	err := s.exec.run(ctx, func(tx *sql.Tx) error {
		tenantID, err := resolveTenant(ctx, s.tenantScoped, "")
		if err != nil {
			return err
		}
		now, err := s.exec.now(ctx, tx)
		if err != nil {
			return err
		}
		// 1) 锁资源行 → 2) 锁动作行（与 PG 同序）。
		// …（按 pgstore/effect_acquire.go 的判据逐条实现）
		return nil
	})
	if err != nil {
		return sideeffect.Outcome{}, sideeffect.Lease{}, sideeffect.Result{}, err
	}
	return outcome, lease, result, nil
}
```

- [ ] **Step 5: 实现其余方法（Complete/Release/Abandon/Renew/MarkUncertain/ResolveUncertain/BeginReconcile）**

逐条参照 `pgstore/effect_lease.go`（215 行）与 `pgstore/effect_reconcile.go`
（159 行）改写。**三项联查**（令牌 + 持有者 + 资源一致）必须逐字保留——少一项即
契约破坏。

- [ ] **Step 6: 跑测试确认套件通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestEffect' -count=1 -v
```

Expected: PASS（33 条子测试绿）。

- [ ] **Step 7: 跑门禁**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/sqliteadapter/
wc -l examples/sqliteadapter/*.go
```

Expected: PASS；无文件超 400 行（`effect.go` 若超限就拆 `effect_lease.go`）。

- [ ] **Step 8: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add examples/sqliteadapter/effect.go examples/sqliteadapter/effect_rows.go examples/sqliteadapter/effect_test.go
git commit -m "feat(examples/sqliteadapter): 副作用闸——sideeffect.Store

Acquire 三判合一在一个事务里（资源行 → 动作行的锁序与 PG 同序）；
Complete/Release/Abandon/Renew/MarkUncertain 三项联查（令牌 + 持有者 +
资源一致），缺一即拒。sidetest 全套 33 条在 unit 层真跑。#424
"
```

---

## Task 5: 进度快照与事件面

**Files:**
- Create: `examples/sqliteadapter/snapshot.go`
- Create: `examples/sqliteadapter/event.go`
- Create: `examples/sqliteadapter/cursor.go`
- Create: `examples/sqliteadapter/snapshot_test.go`
- Create: `examples/sqliteadapter/event_test.go`

**Interfaces:**
- Consumes: Task 2 的 `exec` / `schemaDDL`
- Produces:
  - `func (s *Store) Snapshots() *SnapStore`
  - `type SnapStore struct { exec exec }`
  - `func (s *SnapStore) SaveIfNewer(ctx context.Context, snap checkpoint.Snapshot) error`
  - `func (s *SnapStore) Load(ctx context.Context, runID string) (checkpoint.Snapshot, error)`
  - `func (s *Store) Events() *EventStore`
  - `type EventStore struct { exec exec; tenantScoped bool }`
  - `func (s *EventStore) Insert(ctx context.Context, ev eventbus.Event) (uint64, error)`
  - `func (s *EventStore) PublishInTx(ctx context.Context, tx *sql.Tx, ev eventbus.Event) (uint64, error)`
  - `func (s *EventStore) MaxID(ctx context.Context) (uint64, error)`
  - `func (s *EventStore) CommittedWatermark(ctx context.Context) (uint64, error)`
  - `func (s *EventStore) ListAfter(ctx context.Context, cursor uint64, limit int) ([]eventbus.Event, error)`
  - `func (s *Store) TailCursors(consumer string) *TailCursorStore`
  - `type TailCursorStore struct { exec exec; consumer string }`
  - `func (s *TailCursorStore) LoadCursor(ctx context.Context) (uint64, bool, error)`
  - `func (s *TailCursorStore) SaveCursor(ctx context.Context, cursor uint64) error`

- [ ] **Step 1: 写失败测试（快照 CAS + 事件无空洞 + 水位线上界）**

`examples/sqliteadapter/snapshot_test.go`：

```go
package sqliteadapter

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/checkpoint"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return s
}

// TestSnapshotSaveIfNewerMonotonic 序号更小的快照不得覆盖更大的（单调）。
func TestSnapshotSaveIfNewerMonotonic(t *testing.T) {
	s := newTestStore(t)
	if err := s.Snapshots().SaveIfNewer(t.Context(), checkpoint.Snapshot{
		RunID: "r1", Seq: 5, Status: "running",
	}); err != nil {
		t.Fatalf("存快照失败: %v", err)
	}
	if err := s.Snapshots().SaveIfNewer(t.Context(), checkpoint.Snapshot{
		RunID: "r1", Seq: 3, Status: "stale",
	}); err == nil {
		t.Fatal("更旧的快照必须被拒（不得回退恢复点）")
	}
	got, err := s.Snapshots().Load(t.Context(), "r1")
	if err != nil {
		t.Fatalf("读快照失败: %v", err)
	}
	if got.Seq != 5 {
		t.Fatalf("旧快照覆盖了新快照: seq=%d", got.Seq)
	}
}
```

`examples/sqliteadapter/event_test.go`：

```go
package sqliteadapter

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestEventIDsContiguousNoGap 事件 ID 连续（铸造序 == 提交序）。
func TestEventIDsContiguousNoGap(t *testing.T) {
	s := newTestStore(t)
	evs := s.Events()
	for i := uint64(1); i <= 5; i++ {
		got, err := evs.Insert(t.Context(), eventbus.Event{SessionID: "s", Type: "t"})
		if err != nil {
			t.Fatalf("插入事件失败: %v", err)
		}
		if got != i {
			t.Fatalf("事件 ID 不连续: 期望 %d 实得 %d", i, got)
		}
	}
}

// TestEventWatermarkNotEventTableMax 已提交水位线与事件表 MAX(id) 不是同一个数
// ——水位线读水位行，事件表读事件行，两者上界关系是「水位线 ≤ MAX(id)」。
//
// 这条判据是本轮实测挖出来的：水位线在事件行插入前就已推进。
func TestEventWatermarkNotEventTableMax(t *testing.T) {
	s := newTestStore(t)
	evs := s.Events()
	wm, err := evs.CommittedWatermark(t.Context())
	if err != nil {
		t.Fatalf("读水位线失败: %v", err)
	}
	var maxID int
	if err := s.DB().QueryRow(`SELECT COALESCE(MAX(id),0) FROM gak_event`).Scan(&maxID); err != nil {
		t.Fatalf("读事件表最大值失败: %v", err)
	}
	if int(wm) != maxID {
		t.Fatalf("本用例的前置条件不成立：水位线=%d 事件表最大=%d（空库时应相等）", wm, maxID)
	}
	// 插入一条后：水位线与最大 ID 同步推进。
	if _, err := evs.Insert(t.Context(), eventbus.Event{SessionID: "s", Type: "t"}); err != nil {
		t.Fatalf("插入事件失败: %v", err)
	}
	wm2, _ := evs.CommittedWatermark(t.Context())
	var max2 int
	s.DB().QueryRow(`SELECT COALESCE(MAX(id),0) FROM gak_event`).Scan(&max2)
	if int(wm2) != max2 {
		t.Fatalf("水位线(%d) 必须等于事件表最大 ID(%d)——两者同源", wm2, max2)
	}
}

// TestEventRollbackDoesNotBurnID 回滚不烧号：回滚后拿到的号与回滚前相同。
func TestEventRollbackDoesNotBurnID(t *testing.T) {
	s := newTestStore(t)
	evs := s.Events()
	first, err := evs.Insert(t.Context(), eventbus.Event{SessionID: "s", Type: "t"})
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	// 在事务内铸造后回滚。
	tx, err := s.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("开事务失败: %v", err)
	}
	var burned uint64
	if err := evs.PublishInTx(t.Context(), tx, eventbus.Event{SessionID: "s", Type: "t"}); err == nil {
		_ = tx.Rollback()
		t.Fatal("本用例要求 PublishInTx 在事务内成功后再回滚")
	} else {
		_ = tx.Rollback()
		_ = burned
	}
	next, err := evs.Insert(t.Context(), eventbus.Event{SessionID: "s", Type: "t"})
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	if next != first+1 {
		t.Fatalf("回滚烧了号: first=%d next=%d（回滚后应复用）", first, next)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestSnapshot|TestEvent' -count=1
```

Expected: FAIL —— `undefined: s.Snapshots`。

- [ ] **Step 3: 实现 snapshot.go**

参照 `pgstore/snapshot.go`（109 行）改写。核心：带条件 upsert
`WHERE gak_snapshot.seq < excluded.seq`，影响行数 0 即判陈旧（**已实测该形态可用**：
陈旧写 rows=0）。

- [ ] **Step 4: 实现 event.go（水位线铸造 + 无空洞）**

参照 `pgstore/event.go`（269 行）改写。**关键差异**：

| | PG | SQLite |
|---|---|---|
| 铸造 | `UPDATE pgref_event_watermark SET committed = committed + 1 … RETURNING committed` | `UPDATE gak_event_watermark SET committed = committed + 1 … RETURNING committed` |
| 时钟 | `clock_timestamp()` | `unixepoch('subsec')*1000` |
| JSONB | `JSONB` 列 + pgx 自动编码 | `TEXT` 列 + `json.Marshal` |
| 事件插入 | `INSERT INTO pgref_event (id, …)` | `INSERT INTO gak_event (id, …)` |

`CommittedWatermark` 读**水位线行**（不是事件表 MAX）——这是本轮实测确认的关键。

- [ ] **Step 5: 实现 cursor.go**

参照 `pgstore/cursor.go`（159 行）改写。游标键单调不减由 UPSERT 保证
（`WHERE excluded.cursor_pos > gak_tail_cursor.cursor_pos`）。

- [ ] **Step 6: 跑测试确认通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -count=1 -v
```

Expected: PASS。

- [ ] **Step 7: 跑门禁 + Commit**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/sqliteadapter/
git add examples/sqliteadapter/
git commit -m "feat(examples/sqliteadapter): 进度快照与事件面

快照 CAS 用带条件 upsert（陈旧写 rows=0，已实测）；事件 ID 在每租户一行
水位表上铸造（UPDATE … RETURNING），回滚随事务回滚 ⇒ 不烧号、无空洞。
CommittedWatermark 读水位行而非事件表 MAX——实测确认两者不是同一个数。#424
"
```

---

## Task 6: 租户作用域与跨面 Apply

**Files:**
- Create: `examples/sqliteadapter/tenant.go`
- Create: `examples/sqliteadapter/apply.go`
- Create: `examples/sqliteadapter/apply_test.go`
- Create: `examples/sqliteadapter/tenant_test.go`

**Interfaces:**
- Consumes: Task 3–5 的四个面
- Produces:
  - `func resolveTenant(ctx context.Context, scoped bool, fallback string) (string, error)`
  - `func (s *Store) Apply(ctx context.Context, fn func(tx *Tx) error) error`
  - `type Tx struct { tx *sql.Tx; store *Store }`
  - `func (tx *Tx) Tasks() *BoardStore`
  - `func (tx *Tx) Effects() *EffectStore`
  - `func (tx *Tx) Snapshots() *SnapStore`
  - `func (tx *Tx) Events() *EventStore`
  - `func NewTenantEffectStore(db *sql.DB) *EffectStore`
  - `func NewTenantEventStore(db *sql.DB) *EventStore`

- [ ] **Step 1: 写失败测试（跨面原子性 + 失败整批作废 + 租户 fail-closed）**

`examples/sqliteadapter/apply_test.go`：

```go
package sqliteadapter

import (
	"errors"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestApplyCommitsAllFacesAtomically 四个面的变更落进同一次提交。
func TestApplyCommitsAllFacesAtomically(t *testing.T) {
	s := newTestStore(t)
	s.Board().Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	err := s.Apply(t.Context(), func(tx *Tx) error {
		if err := tx.Tasks().Complete(t.Context(), "t1", "no-such-token"); err == nil {
			return errors.New("令牌不匹配的完成必须被拒")
		}
		// 故意吞掉上面的错误继续写：整批仍必须作废。
		return nil
	})
	if err != nil {
		t.Fatalf("Apply 本身不应报错（错误由内层吞掉）: %v", err)
	}

	tasks, err := s.Board().List(t.Context())
	if err != nil {
		t.Fatalf("读任务失败: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("任务数异常: %d", len(tasks))
	}
	// 关键：失败被吞掉后，事务仍必须回滚 ⇒ 任务状态未变（仍是 open）。
	if tasks[0].State != taskboard.StateOpen {
		t.Fatalf("失败被吞掉后状态仍被提交（半截事务）: state=%s", tasks[0].State)
	}
}

// TestApplyRollsBackEverythingOnError 回调返回错误 ⇒ 整批丢弃。
func TestApplyRollsBackEverythingOnError(t *testing.T) {
	s := newTestStore(t)
	s.Board().Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	sentinel := errors.New("故意失败")
	err := s.Apply(t.Context(), func(tx *Tx) error {
		if _, err := tx.Events().Insert(t.Context(), eventbus.Event{SessionID: "s", Type: "t"}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Apply 必须原样传出回调的错误: %v", err)
	}
	wm, err := s.Events().CommittedWatermark(t.Context())
	if err != nil {
		t.Fatalf("读水位线失败: %v", err)
	}
	if wm != 0 {
		t.Fatalf("回滚后水位线必须为 0（事件行不得留下）: %d", wm)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestApply' -count=1
```

Expected: FAIL —— `undefined: s.Apply`。

- [ ] **Step 3: 实现 apply.go**

`examples/sqliteadapter/apply.go`：

```go
package sqliteadapter

import (
	"context"
	"database/sql"
	"fmt"
)

// Tx 一次事务的四个面（共享同一份状态与同一次提交）。
type Tx struct {
	tx    *sql.Tx
	store *Store
}

// Tasks 任务面（事务内形态）。
func (tx *Tx) Tasks() *BoardStore {
	return &BoardStore{exec: exec{db: tx.store.db, tx: tx.tx, clock: tx.store.clock}}
}

// Effects 副作用面（事务内形态）。
func (tx *Tx) Effects() *EffectStore {
	return &EffectStore{exec: exec{db: tx.store.db, tx: tx.tx, clock: tx.store.clock}}
}

// Snapshots 进度快照面（事务内形态）。
func (tx *Tx) Snapshots() *SnapStore {
	return &SnapStore{exec: exec{db: tx.store.db, tx: tx.tx, clock: tx.store.clock}}
}

// Events 事件面（事务内形态）。
func (tx *Tx) Events() *EventStore {
	return &EventStore{exec: exec{db: tx.store.db, tx: tx.tx, clock: tx.store.clock}}
}

// Apply 在**一个事务**里提交四个面的变更。
//
// 回调返回错误 ⇒ 整批丢弃；返回 nil ⇒ 四个面的变更作为**一次提交**落库 ⇒
// 跨面不会出现「一半已提交」。
//
// ⚠️ **失败即作废整批**（即使调用方吞掉了内层的错误）：内层任一面操作
// 失败都会记下「本批已失败」⇒ 事务最终回滚。缺这条，「任务做完了但没留痕」
// 与「留痕了但没做完」两类半截状态就会重现——这正是本包存在的理由。
//
// 实现：内层失败通过 txCtx.failed 记录；Apply 在回调返回后检查它，
// 若已失败则**回滚并返回该错误**（即使回调本身返回 nil）。
func (s *Store) Apply(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqliteadapter: 开事务失败: %w", err)
	}
	inner := &Tx{tx: tx, store: s}

	// failed 记录本批第一次失败：调用方吞掉错误也要作废整批。
	failed := error(nil)
	guarded := func(fn func(tx *Tx) error) error {
		if err := fn(inner); err != nil {
			if failed == nil {
				failed = err
			}
			return err
		}
		return nil
	}
	_ = guarded

	cbErr := fn(inner)
	if cbErr == nil {
		cbErr = failed
	}
	if cbErr != nil {
		_ = tx.Rollback()
		return cbErr
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqliteadapter: 提交事务失败: %w", err)
	}
	return nil
}
```

**⚠️ 实施注意**：上面的 `guarded` 包装**没有真正拦住内层错误**（内层方法各自
返回错误，`Apply` 无法知道它们是否被吞掉）。**正确实现要求每个面的事务内变体
在失败时把错误记进一个共享的失败槽**。落法：给 `exec` 加一个
`failed *error` 字段，`exec.run` 在事务内执行时若 `fn` 返回错误且
`e.failed != nil` 且 `*e.failed == nil` 则记下它。这样任何一面失败都被记录，
无论调用方是否吞掉。**这一步必须改成这个形态**，否则「失败即作废整批」是假的。

- [ ] **Step 4: 给 exec 加失败槽（真正的失败即作废）**

改 `examples/sqliteadapter/exec.go`：

```go
type exec struct {
	db *sql.DB
	tx *sql.Tx
	clock func() time.Time
	// failed 本批第一次失败（仅事务内形态使用）。
	//
	// 为什么要有它：事务内某个面判失败时，调用方**可能吞掉那个错误**继续
	// 写别的面 ⇒ 就会出现「一半已提交」。失败一旦发生就作废整批，是唯一
	// 能杜绝这种半截事务的形态（失败形态是变红，不是留半截状态）。
	//
	// ⚠️ 它是**记录点**，不是判定点：判定只有一处（Apply 的提交闸）。
	// 两处都判 ⇒ 同一个判断有两个主人 ⇒ 改坏一处测试看不出区别。
	failed *error
}
```

并在 `exec.run` 的事务内分支里：

```go
	if e.tx != nil {
		if err := fn(e.tx); err != nil {
			if e.failed != nil && *e.failed == nil {
				*e.failed = err
			}
			return err
		}
		return nil
	}
```

同时 `Apply` 里创建 `inner` 时把 `failed` 槽传下去：

```go
	var failed error
	inner := &Tx{tx: tx, store: s}
	inner.failed = &failed
```

并让 `Tx` 的四个访问器把 `failed: tx.failed` 传进 `exec`。

- [ ] **Step 5: 实现 tenant.go**

参照 `pgstore/tenant.go`（54 行）与 `pgstore/sink.go` 的 `resolveTenant` 改写：

```go
package sqliteadapter

import (
	"context"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/tenant"
)

// resolveTenant 取本次调用的租户。
//
// scoped=false ⇒ 旧布局：tenant 取动作/事件自带值（空 = 单租户命名空间）。
// scoped=true  ⇒ 租户作用域：每次调用从 ctx 取租户（tenant.Require），
// 缺租户 ⇒ tenant.ErrNoContext（写入被拒，fail-closed）。
//
// ⚠️ **真实签名（已查 tenant/tenant.go）**：
//	func Require(ctx context.Context, required ...string) (Context, error)
// 它返回 **`tenant.Context`（结构体）不是 string**，且**带必填参数**——
// `required` 是「本次调用必须声明的租户名」，缺任一即 ErrTenantMismatch。
// 取租户 ID 用 `tc.Tenant`（**已核实**：tenant.Context 的字段是 `Tenant`，
// 不是 `ID`——见 tenant/tenant.go:80-82）。
func resolveTenant(ctx context.Context, scoped bool, fallback string) (string, error) {
	if !scoped {
		return fallback, nil
	}
	tc, err := tenant.Require(ctx)
	if err != nil {
		return "", err
	}
	return tc.Tenant, nil
}
```

**已核实**：`tenant.Require(ctx context.Context, required ...string) (Context, error)`——
返回 `tenant.Context` 结构体（**不是 string**），且带必填参数 `required`
（本次调用必须声明的租户名，缺任一即 `ErrTenantMismatch`）。缺租户的
fail-closed 语义不得弱化。

- [ ] **Step 6: 跑测试确认通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -count=1 -v
```

Expected: PASS（含 `TestApplyCommitsAllFacesAtomically` 与
`TestApplyRollsBackEverythingOnError`）。

- [ ] **Step 7: 跑门禁 + Commit**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/sqliteadapter/
git add examples/sqliteadapter/
git commit -m "feat(examples/sqliteadapter): 租户作用域与跨面 Apply

Apply 把四个面的变更收进同一次提交。失败即作废整批：内层任一面失败都会被
记进共享失败槽，即使调用方吞掉错误也回滚——否则「一半已提交」重现。
租户作用域从 ctx 取租户，缺租户 fail-closed。#424
"
```

---

## Task 7: 控制面与下游栅栏（对齐 PG 全貌）

**Files:**
- Create: `examples/sqliteadapter/control.go`
- Create: `examples/sqliteadapter/sink.go`
- Create: `examples/sqliteadapter/control_test.go`
- Create: `examples/sqliteadapter/sink_test.go`

**Interfaces:**
- Consumes: Task 2–6
- Produces:
  - `func (s *Store) Control(aud controlplane.Auditor) *ControlStore`
  - `type ControlStore struct { exec exec; aud controlplane.Auditor; src string }`
  - `func (c *ControlStore) SnapshotTask(ctx context.Context, id taskboard.TaskID) (controlplane.TaskSnapshot, error)`
  - `func (c *ControlStore) SnapshotRun(ctx context.Context, id controlplane.RunID) (controlplane.RunSnapshot, error)`
  - `func (c *ControlStore) SnapshotEffect(ctx context.Context, key string) (controlplane.EffectSnapshot, error)`
  - `func (c *ControlStore) CommitTask(ctx context.Context, expect controlplane.Version, next taskboard.Task, entry controlplane.AuditEntry) error`
  - `func (c *ControlStore) CommitRun(ctx context.Context, expect controlplane.Version, id controlplane.RunID, next controlplane.RunState, entry controlplane.AuditEntry) error`
  - `func (c *ControlStore) CommitEffect(ctx context.Context, expect controlplane.Version, key string, next controlplane.EffectState, entry controlplane.AuditEntry) error`
  - `func (s *Store) Downstream() *Downstream`
  - `type Downstream struct { exec exec; tenantScoped bool }`
  - `func (d *Downstream) WriteFenced(ctx context.Context, resource string, presented sideeffect.Fence, write func(ctx context.Context) error) error`
  - `func (d *Downstream) SeenFence(ctx context.Context, resource string) (sideeffect.Fence, error)`
  - `func ExecWithin(ctx context.Context, sql string, args ...any) (int64, error)`
  - `func WithTx(ctx context.Context, tx *sql.Tx) context.Context`
  - `func TxFromContext(ctx context.Context) (*sql.Tx, bool)`

- [ ] **Step 1: 写失败测试（栅栏套件 + 控制面版本锚）**

`examples/sqliteadapter/sink_test.go`：

```go
package sqliteadapter

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/sidetest"
)

// TestSinkContract 跑下游栅栏的共享契约套件（6 条）。
func TestSinkContract(t *testing.T) {
	sidetest.RunSinkContract(t, func(t *testing.T) sidetest.SinkProbe {
		s := newTestStore(t)
		return s.Downstream()
	})
}
```

`examples/sqliteadapter/control_test.go`：

```go
package sqliteadapter

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/controlplane"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestControlStoreVersionAnchor 控制面的版本锚：快照后目标被改过 ⇒ 提交必失败。
func TestControlStoreVersionAnchor(t *testing.T) {
	s := newTestStore(t)
	c := s.Control(controlplane.MemAuditor{}) // MemAuditor 零值可用（无 NewMemAuditor）

	snap, err := c.SnapshotTask(t.Context(), "t1")
	if err == nil {
		t.Fatalf("不存在的任务必须报 ErrNotFound（实得 %+v）", snap)
	}

	// 摆一个任务再取快照。
	s.Board().Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	snap, err = c.SnapshotTask(t.Context(), "t1")
	if err != nil {
		t.Fatalf("取快照失败: %v", err)
	}
	if err := c.CommitTask(t.Context(), snap.Version, taskboard.Task{ID: "t1", State: taskboard.StateDone},
		controlplane.AuditEntry{Action: "commit"}); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	// 用旧版本再提交 ⇒ 必须冲突。
	err = c.CommitTask(t.Context(), snap.Version, taskboard.Task{ID: "t1", State: taskboard.StateFailed},
		controlplane.AuditEntry{Action: "commit"})
	if err == nil {
		t.Fatal("陈旧版本必须报 ErrStoreConflict")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -run 'TestSinkContract|TestControlStore' -count=1
```

Expected: FAIL —— `undefined: s.Downstream`。

- [ ] **Step 3: 实现 sink.go（栅栏 + tx-in-ctx 接缝）**

参照 `pgstore/sink.go`（209 行）改写。**核心形态**（已实测该 upsert 可用：陈旧写
rows=0、同代 rows=1）：

```go
	// 一条带条件的 upsert 完成比较与预占：
	//   新资源直接以 presented 建行（首次写入恒通过）；
	//   既有资源仅在 seen_fence <= presented 时更新（小于即陈旧）。
	res, err := tx.ExecContext(ctx,
		`INSERT INTO gak_downstream (tenant, resource, seen_fence, value) VALUES (?, ?, ?, '')
		 ON CONFLICT (tenant, resource) DO UPDATE SET seen_fence = excluded.seen_fence
		 WHERE gak_downstream.seen_fence <= excluded.seen_fence`,
		tenantID, resource, int64(presented))
	if err != nil {
		return errors.Wrap(err, "sqliteadapter: 栅栏预占失败", "resource", resource)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.Wrap(sideeffect.ErrFenceStale,
			"sqliteadapter: 栅栏令牌已不是最新代（陈旧写入被拒）", "resource", resource)
	}
```

tx-in-ctx 接缝（`TxFromContext` / `WithTx` / `ExecWithin`）照 `pgstore/sink.go`
同款改写（pgx.Tx → sql.Tx）。

- [ ] **Step 4: 实现 control.go（控制面）**

参照 `pgstore/control.go`（393 行）改写。**注意**：

- 一致快照：PG 用 `REPEATABLE READ` 只读事务；SQLite **没有**只读快照事务的等价物
  ⇒ 用 `BEGIN`（deferred）读事务即可：SQLite 的读事务在首次读时取快照，后续读同一快照。
- 版本锚行锁：PG 用 `SELECT … FOR UPDATE`；SQLite 靠 `BEGIN IMMEDIATE` 已持的写锁。
- 三个 `Commit*` 的「校验版本 → 留痕 → 写状态 → 推进版本」必须同事务。
- 留痕失败 ⇒ 状态一字不动（与 PG 同款）。
- **`controlplane.NewMemAuditor` 的真实构造名必须先查**（`controlplane/audit.go` 里是
  `MemAuditor`；构造名可能是 `NewMemAuditor` 或零值可用）——按真实 API 改写。

- [ ] **Step 5: 跑测试确认通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./examples/sqliteadapter/ -count=1 -v
```

Expected: PASS。

- [ ] **Step 6: 跑门禁 + Commit**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/sqliteadapter/
wc -l examples/sqliteadapter/*.go
git add examples/sqliteadapter/
git commit -m "feat(examples/sqliteadapter): 控制面与下游栅栏

controlplane.Store：三个 Snapshot* 走一致快照读事务；三个 Commit* 在版本
锚上「校验 → 留痕 → 写状态 → 推进版本」同事务，留痕失败则状态一字不动。
FencedSink：一条带条件 upsert 原子预占，写入体走同一事务（不得部分写）。
栅栏套件 6 条真跑。#424
"
```

---

## Task 8: PG 侧补齐跨面事务

**Files:**
- Modify: `examples/pgstore/effect.go`（`withinTx` 与 tx 助手）
- Create: `examples/pgstore/store.go`
- Create: `examples/pgstore/pg_apply_test.go`

**Interfaces:**
- Consumes: Task 4–7 建好的四套契约面
- Produces:
  - `func (s *Store) Apply(ctx context.Context, fn func(ctx context.Context) error) error`
  - `type Store struct { pool *pgxpool.Pool }`
  - `func NewStore(pool *pgxpool.Pool) *Store`
  - `func (s *Store) Board() *BoardStore`
  - `func (s *Store) Effects() *EffectStore`
  - `func (s *Store) Snapshots() *SnapStore`
  - `func (s *Store) Events() *EventStore`
  - `withinTx` 认 ctx 里已有的 tx（既有 `pgstore.WithTx` / `TxFromContext` 已有接缝）

- [ ] **Step 1: 写失败测试（跨面同事务 + 回滚整批）**

`examples/pgstore/pg_apply_test.go`：

```go
package pgstore

import (
	"context"
	"os"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestApplyCommitsAllFacesAtomically 跨面变更落在同一次提交里。
//
// 这条判据是 #419 在 PG 侧的补齐：四个面此前各自 Begin/Commit ⇒ 跨面
// 原子提交拿不到。
func TestApplyCommitsAllFacesAtomically(t *testing.T) {
	dsn := os.Getenv("GAK_PGREF_DSN")
	if dsn == "" {
		t.Skip("无 PG DSN（GAK_PGREF_DSN 未设）——环境依赖入口的显式跳过")
	}
	pool, schema := freshPool(t, dsn)
	s := NewStore(pool)
	s.Board().Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})

	err := s.Apply(context.Background(), func(ctx context.Context) error {
		if _, err := s.Events().Insert(ctx, eventbus.Event{SessionID: "s", Type: "t"}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	wm, err := s.Events().CommittedWatermark(ctx())
	if err != nil {
		t.Fatalf("读水位线失败: %v", err)
	}
	if wm != 1 {
		t.Fatalf("事件未落进 Apply 的事务: watermark=%d", wm)
	}
	_ = schema
}
```

**⚠️ 实施注意**：上面的 `freshPool` / `ctx()` 是**占位写法**，实施时必须照
`examples/pgstore/pg_test.go` 里的**真实** fixture 写（本仓已有建 schema 的辅助：
`pgstore.NewSchema` + `EnsureSchema` + `t.Cleanup(DropSchema)`）。**照抄既有 fixture，
不要新造**。

- [ ] **Step 2: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
GAK_PGREF_DSN=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable \
  CGO_ENABLED=0 go test ./examples/pgstore/ -run 'TestApply' -count=1
```

Expected: FAIL —— `undefined: NewStore`（无 DSN 时 Skip，Skip 是环境入口的显式跳过）。

- [ ] **Step 3: 让 withinTx 认 ctx 里已有的 tx**

读 `examples/pgstore/effect.go` 里 `withinTx` 的真实实现，改成：

```go
// runTx 事务骨架：fn 报错即整体回滚（不落任何一行），成功才提交。
//
// **本轮改动**：ctx 里若已有 tx（pgstore.WithTx 塞进去的），**复用它**——
// 不新开事务、不提交（提交权在最外层 Apply）。否则照旧自己开一个。
//
// 这一处改动让四个面全部支持跨面原子提交：Apply 开一个 tx 塞进 ctx，
// 各面方法经 withinTx/withinTx2/withinTx3 → runTx 拿到同一个 tx ⇒
// 跨面变更落进同一次提交。
//
// ⚠️ **改的是 runTx，不是 withinTx**：withinTx/withinTx2/withinTx3 是
// **泛型变体族**（按返回值个数分，见 effect.go:140-180），三者的骨架都是
// runTx ⇒ 只改 runTx 一处，三个变体全部受益。
func runTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	if tx, ok := TxFromContext(ctx); ok {
		return fn(tx)
	}
	// …（原有开事务 / 回滚 / 提交逻辑照旧）
}
```

**已核实**（`examples/pgstore/effect.go:124-180`）：`runTx(ctx, pool, fn)` 是
真正开事务的那个；`withinTx` / `withinTx2` / `withinTx3` 是**泛型变体族**（按返回值
个数分），三者的骨架都是 runTx ⇒ **只改 runTx 一处，三个变体全部受益**。

- [ ] **Step 4: 实现 store.go（Store 门面 + Apply）**

`examples/pgstore/store.go`：

```go
package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 聚合 PG 参考实现的四个面，供跨面原子提交用。
//
// 四个面共用**同一个 pool** ⇒ 同一个数据库、同一个 schema ⇒ 跨面事务
// 有落点。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 构造门面。pool 不得为 nil（装配期编程错误 fail fast）。
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("pgstore: Store 需要连接池（nil pool 无从读写）")
	}
	return &Store{pool: pool}
}

// Board 任务面。
func (s *Store) Board() *BoardStore { return NewBoardStore(s.pool) }

// Effects 副作用面。
func (s *Store) Effects() *EffectStore { return NewEffectStore(s.pool) }

// Snapshots 进度快照面。
func (s *Store) Snapshots() *SnapStore { return NewSnapStore(s.pool) }

// Events 事件面。
func (s *Store) Events() *EventStore { return NewEventStore(s.pool) }

// Apply 在**一个事务**里提交四个面的变更。
//
// 回调返回错误 ⇒ 整批丢弃；返回 nil ⇒ 四个面的变更作为**一次提交**落库。
//
// 实现：开一个 pgx.Tx 塞进 ctx（pgstore.WithTx），各面方法经 withinTx
// 认到它 ⇒ 复用同一个 tx ⇒ 跨面变更落进同一次提交。
//
// ⚠️ **失败即作废整批**：内层任一面操作失败都要记下「本批已失败」⇒
// 事务最终回滚（即使调用方吞掉了那个错误）。
func (s *Store) Apply(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	inner := WithTx(ctx, tx)

	var failed error
	cbErr := fn(inner)
	if cbErr == nil {
		cbErr = failed
	}
	if cbErr != nil {
		_ = tx.Rollback(ctx)
		return cbErr
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

var _ = pgx.Tx(nil)
```

**⚠️ 实施注意**：上面的 `failed` 槽**没有接上内层失败**——与 Task 6 Step 4 同一个坑。
必须让 `withinTx` 在 ctx 带 tx 的分支里把内层错误记进共享失败槽（放进 ctx 或
包级结构），否则「失败即作废整批」是假的。**落法**：在 `Apply` 里造一个
`failSlot` 指针塞进 ctx（自定义 key），`withinTx` 读到它就在失败时记录。

- [ ] **Step 5: 跑测试确认通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
GAK_PGREF_DSN=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable \
  CGO_ENABLED=0 go test ./examples/pgstore/ -count=1 -timeout 15m
```

Expected: PASS（无 DSN 时该包整体 Skip，属既有纪律）。

- [ ] **Step 6: 跑门禁 + Commit**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && CGO_ENABLED=0 go vet ./examples/pgstore/
git add examples/pgstore/
git commit -m "feat(examples/pgstore): 补齐跨面事务——Store.Apply

withinTx 认 ctx 里已有的 tx（不新开事务、不提交）⇒ 一处改动让四个面都能
落进同一次提交，不必给 30 个方法各写 InTx 变体。失败即作废整批：内层任一
面失败都记进共享失败槽，即使调用方吞掉错误也回滚。#424
"
```

---

## Task 9: 选型判据门禁（cmd/storagecheck）

**Files:**
- Create: `docs/STORAGE-SELECTION.yaml`
- Create: `cmd/storagecheck/main.go`
- Create: `cmd/storagecheck/check.go`
- Create: `cmd/storagecheck/check_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: Task 1–8 落地的两个适配器与 `docs/DEPLOY-MATRIX.yaml`
- Produces:
  - `make storagecheck` 目标
  - `cmd/storagecheck` 的三条判据（场景 storage 在允许集内 / storage 提供场景必需的全部能力 / 能力断言在代码里找得到落点）

- [ ] **Step 1: 建声明面 docs/STORAGE-SELECTION.yaml**

```yaml
# 存储选型声明面（issue #424）
#
# 这份文件回答一个问题：**「什么时候用 SQLite、什么时候用 PostgreSQL」**。
# 它是机器可查的声明面——cmd/storagecheck 逐条判定：
#
#   1. 场景声明的 storage 必须在本文件的允许集内；
#   2. 该 storage 必须**实际提供**场景 required 的全部能力；
#   3. 「storage 提供某能力」的断言必须能在代码里找到对应落点
#      （path#Symbol 可解析，与 deploycheck 的 contract 证据同款判据）——
#      否则「SQLite 提供租户隔离」就成了一句没人验证的话。
#
# 选型口径（三条）：
#   - 需要跨进程 / 跨机共享写（多副本、多机、K8s）⇒ PostgreSQL；
#   - 需要重启后恢复且不引入外部服务进程 ⇒ SQLite；
#   - 只要进程内判据正确、不要持久化 ⇒ memory。
#
# 边界写死：**单机多进程一律判 PostgreSQL**——SQLite 的单写者模型在多写者下
# 退化为串行，busy_timeout 到点即失败（可判、不静默丢写，但降低可用性）。
#
# 附加提示：SQLite 侧存储时钟是毫秒级（实测），租约 TTL 建议 ≥ 1s。
version: 1

storages:
  - id: examples/reference-runtime
    title: 内存参考实现（进程内，不持久化）
    kind: memory
    capabilities:
      - id: idempotency-key-scope
        evidence:
          - kind: contract
            ref: sideeffect/key_scope.go#ActionKeyBuilder

  - id: examples/sqliteadapter
    title: 真 SQLite（WAL 单文件，单机单副本）
    kind: sqlite
    capabilities:
      - id: taskboard-persistence
        evidence:
          - kind: contract
            ref: examples/sqliteadapter/board.go#BoardStore
      - id: startup-recovery
        evidence:
          - kind: contract
            ref: examples/sqliteadapter/store.go#Open
      - id: single-store-full-state
        evidence:
          - kind: contract
            ref: examples/sqliteadapter/apply.go#Store
      - id: progress-reconciliation
        evidence:
          - kind: contract
            ref: examples/sqliteadapter/snapshot.go#SnapStore

  - id: examples/pgstore
    title: PostgreSQL（共享库，多副本 / 跨机）
    kind: postgresql
    capabilities:
      - id: taskboard-persistence
        evidence:
          - kind: contract
            ref: examples/pgstore/board.go#BoardStore
      - id: multi-process-claim
        evidence:
          - kind: contract
            ref: examples/pgstore/board.go#BoardStore
      - id: unified-lease
        evidence:
          - kind: contract
            ref: taskboard/renew.go#RenewingBoard
      - id: database-clock
        evidence:
          - kind: contract
            ref: examples/pgstore/effect.go#EffectStore
      - id: cross-process-token
        evidence:
          - kind: contract
            ref: examples/pgstore/board.go#BoardStore
      - id: tenant-scoped-storage
        evidence:
          - kind: contract
            ref: examples/pgstore/effect.go#NewTenantEffectStore
      - id: reliable-events
        evidence:
          - kind: contract
            ref: examples/pgstore/event.go#EventStore
      - id: quota-reclaim
        evidence:
          - kind: contract
            ref: scheduling/quota_store.go#QuotaStore
      - id: job-idempotency
        evidence:
          - kind: contract
            ref: examples/containerbackend/k8sjob.go#K8sJob
      - id: network-egress-enforcement
        evidence:
          - kind: contract
            ref: examples/containerbackend/k8sjobegress.go#K8sEgressProbe
      - id: authority-uniqueness
        evidence:
          - kind: contract
            ref: examples/pgstore/control.go#ControlStore
      - id: ownership-epoch
        evidence:
          - kind: contract
            ref: ownership/ownership.go#Authority
      - id: partition-write-fencing
        evidence:
          - kind: contract
            ref: ownership/ownership.go#Fencer
      - id: reliable-handoff
        evidence:
          - kind: contract
            ref: handoff/desk.go#Desk
      - id: progress-reconciliation
        evidence:
          - kind: contract
            ref: examples/pgstore/snapshot.go#SnapStore
      - id: single-store-full-state
        evidence:
          - kind: contract
            ref: examples/pgstore/store.go#Store
      - id: startup-recovery
        evidence:
          - kind: contract
            ref: examples/pgstore/schema.go#EnsureSchema
```

**⚠️ 实施注意**：上表的 capability id **必须与 `docs/DEPLOY-MATRIX.yaml` 里的 id
逐字一致**（判据 2 会按 id 比对）。实施时先读 DEPLOY-MATRIX.yaml 的 capabilities 段，
把**每一个** capability 分配给真正提供它的 storage，再据此写本文件。

- [ ] **Step 2: 写失败测试（判据 + 反向验证）**

`cmd/storagecheck/check_test.go`：

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRoot 造一个最小的仓根（含清单与声明面）。
func writeRoot(t *testing.T, matrix string, selection string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "DEPLOY-MATRIX.yaml"), matrix)
	mustWrite(t, filepath.Join(root, "docs", "STORAGE-SELECTION.yaml"), selection)
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const okMatrix = `version: 1
capabilities:
  - id: taskboard-persistence
    title: 任务板持久化
    state: available
    evidence:
      - kind: contract
        ref: taskboard/taskboard.go#Board
scenarios:
  - id: single-host-persistent
    title: 单机持久化
    storage: examples/sqliteadapter
    worker: single-node
    required: [taskboard-persistence]
    readiness: available
`

const okSelection = `version: 1
storages:
  - id: examples/sqliteadapter
    title: 真 SQLite
    kind: sqlite
    capabilities:
      - id: taskboard-persistence
        evidence:
          - kind: contract
            ref: taskboard/taskboard.go#Board
`

// TestCheckPasses 正常形态不报违反。
func TestCheckPasses(t *testing.T) {
	root := writeRoot(t, okMatrix, okSelection)
	probs, notes, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("正常形态不该报违反: %v", probs)
	}
}

// TestCheckRejectsStorageOutsideAllowedSet 场景声明了不在允许集内的 storage ⇒ 红。
func TestCheckRejectsStorageOutsideAllowedSet(t *testing.T) {
	m := strings.Replace(okMatrix, "storage: examples/sqliteadapter", "storage: examples/unknown", 1)
	root := writeRoot(t, m, okSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) == 0 {
		t.Fatal("声明面外的 storage 必须报违反")
	}
}

// TestCheckRejectsMissingCapability storage 不提供场景必需的能力 ⇒ 红。
func TestCheckRejectsMissingCapability(t *testing.T) {
	m := strings.Replace(okMatrix, "required: [taskboard-persistence]", "required: [tenant-scoped-storage]", 1)
	s := strings.Replace(okSelection, "      - id: taskboard-persistence", "      - id: other-capability", 1)
	root := writeRoot(t, m, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) == 0 {
		t.Fatal("能力不齐必须报违反")
	}
}

// TestCheckRejectsUnresolvableEvidence 能力断言指向不存在的符号 ⇒ 红。
func TestCheckRejectsUnresolvableEvidence(t *testing.T) {
	s := strings.Replace(okSelection, "ref: taskboard/taskboard.go#Board", "ref: taskboard/nope.go#Nope", 1)
	root := writeRoot(t, okMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if len(probs) == 0 {
		t.Fatal("落点找不到必须报违反")
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./cmd/storagecheck/ -count=1
```

Expected: FAIL —— `undefined: check`。

- [ ] **Step 4: 实现 check.go（三条判据）**

`cmd/storagecheck/check.go`：

```go
// check.go —— 存储选型判据（issue #424）。
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	matrixPath   = "docs/DEPLOY-MATRIX.yaml"
	selectionPath = "docs/STORAGE-SELECTION.yaml"
)

// Matrix 部署矩阵（只取本判据要用的字段）。
type Matrix struct {
	Version      int          `yaml:"version"`
	Capabilities []Capability `yaml:"capabilities"`
	Scenarios    []Scenario   `yaml:"scenarios"`
}

type Capability struct {
	ID       string     `yaml:"id"`
	Title    string     `yaml:"title"`
	State    string     `yaml:"state"`
	Evidence []Evidence `yaml:"evidence"`
}

type Evidence struct {
	Kind string `yaml:"kind"`
	Ref  string `yaml:"ref"`
}

type Scenario struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Storage   string   `yaml:"storage"`
	Worker    string   `yaml:"worker"`
	Required  []string `yaml:"required"`
	Readiness string   `yaml:"readiness"`
}

// Selection 选型声明面。
type Selection struct {
	Version  int       `yaml:"version"`
	Storages []Storage `yaml:"storages"`
}

type Storage struct {
	ID           string             `yaml:"id"`
	Title        string             `yaml:"title"`
	Kind         string             `yaml:"kind"`
	Capabilities []StorageCapability `yaml:"capabilities"`
}

type StorageCapability struct {
	ID       string     `yaml:"id"`
	Evidence []Evidence `yaml:"evidence"`
}

// check 三条判据的聚合。
//
// 返回 (违反, 说明, 错误)。说明不阻断但必须打出来——派生状态的理由不给人
// 看，就等于让人自己重推一遍。
func check(root string) ([]string, []string, error) {
	m, err := loadMatrix(root)
	if err != nil {
		return nil, nil, err
	}
	sel, err := loadSelection(root)
	if err != nil {
		return nil, nil, err
	}

	known := map[string]bool{}
	for _, c := range m.Capabilities {
		known[c.ID] = true
	}
	storages := map[string]Storage{}
	for _, s := range sel.Storages {
		storages[s.ID] = s
	}

	var probs []string
	var notes []string

	for _, sc := range m.Scenarios {
		where := fmt.Sprintf("%s: 场景 %s", matrixPath, sc.ID)
		st, ok := storages[sc.Storage]
		if !ok {
			probs = append(probs, fmt.Sprintf("%s: storage %q 不在选型声明面的允许集内", where, sc.Storage))
			continue
		}
		// 判据 2：storage 必须提供场景必需的全部能力。
		provided := map[string]bool{}
		for _, c := range st.Capabilities {
			provided[c.ID] = true
		}
		for _, req := range sc.Required {
			if !known[req] {
				probs = append(probs, fmt.Sprintf("%s: 必需能力 %q 不在部署矩阵的能力表里", where, req))
				continue
			}
			if !provided[req] {
				probs = append(probs, fmt.Sprintf("%s: storage %q 不提供必需能力 %q", where, sc.Storage, req))
			}
		}
	}

	// 判据 3：能力断言必须在代码里找得到落点。
	for _, st := range sel.Storages {
		for _, c := range st.Capabilities {
			if !known[c.ID] {
				probs = append(probs, fmt.Sprintf("%s: storage %s 声明的能力 %q 不在部署矩阵的能力表里",
					selectionPath, st.ID, c.ID))
			}
			for _, ev := range c.Evidence {
				if ev.Kind != "contract" {
					continue
				}
				ok, err := contractExists(root, ev.Ref)
				if err != nil {
					return nil, nil, err
				}
				if !ok {
					probs = append(probs, fmt.Sprintf("%s: storage %s 的能力 %q 的落点 %q 在代码里找不到",
						selectionPath, st.ID, c.ID, ev.Ref))
				}
			}
		}
	}
	return probs, notes, nil
}

// contractExists 判 path#Symbol 是否真的声明在文件里（go/ast）。
func contractExists(root, ref string) (bool, error) {
	path, sym, ok := strings.Cut(ref, "#")
	if !ok {
		return false, fmt.Errorf("落点 %q 缺 #Symbol 形态", ref)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(root, path), nil, 0)
	if err != nil {
		return false, nil // 文件不存在或解析失败 ⇒ 落点不存在
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == sym {
				return true, nil
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if ok && ts.Name.Name == sym {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func loadMatrix(root string) (*Matrix, error) {
	b, err := os.ReadFile(filepath.Join(root, matrixPath))
	if err != nil {
		return nil, fmt.Errorf("读 %s 失败: %w", matrixPath, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var m Matrix
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", matrixPath, err)
	}
	return &m, nil
}

func loadSelection(root string) (*Selection, error) {
	b, err := os.ReadFile(filepath.Join(root, selectionPath))
	if err != nil {
		return nil, fmt.Errorf("读 %s 失败: %w", selectionPath, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Selection
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", selectionPath, err)
	}
	return &s, nil
}
```

- [ ] **Step 5: 实现 main.go（CLI 装配）**

`cmd/storagecheck/main.go`：

```go
// storagecheck —— 存储选型判据（issue #424）。
//
// 判「场景选的存储对不对」：在允许集内 + 提供该场景必需的全部能力 +
// 能力断言在代码里找得到落点。
//
// 与 cmd/deploymatrix 分开：deploymatrix 判「能力齐不齐」（红在缺能力），
// 本判据红在「选错引擎」——两件事的失败形态不同，混在一起会让「哪个门禁
// 在管我」变得不可追。
package main

import (
	"fmt"
	"os"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	probs, notes, err := check(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storagecheck: %v\n", err)
		os.Exit(1)
	}
	for _, n := range notes {
		fmt.Printf("storagecheck: 说明 %s\n", n)
	}
	if len(probs) > 0 {
		for _, p := range probs {
			fmt.Fprintf(os.Stderr, "storagecheck: %s\n", p)
		}
		os.Exit(1)
	}
	fmt.Println("storagecheck ✓（场景选型在允许集内 + 能力齐备 + 落点可解析）")
}
```

- [ ] **Step 6: 跑测试确认通过**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
CGO_ENABLED=0 go test ./cmd/storagecheck/ -count=1 -v
```

Expected: PASS（四条用例，含三条反向验证）。

- [ ] **Step 7: 接进 Makefile**

在 `check:` 的依赖链里加 `storagecheck`，并加目标：

```make
storagecheck: ## 存储选型判据（场景选的 storage 在允许集内 + 能力齐备 + 落点可解析）
	@$(EXPORT) $(GOENV) go run ./cmd/storagecheck .
```

同时把 `.PHONY` 行补上 `storagecheck`。

**注意**：`gates` 层里也要接（与 `deploycheck` / `ledgercheck` 同级）。先读
Makefile 的 `gates:` 目标，把 `storagecheck` 加进去——**只在 `check` 里而不在
`gates` 里会重演「deploycheck / releasecheck 从来没在 CI 上跑过」的教训**。

- [ ] **Step 8: 跑门禁确认接线生效**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make storagecheck
```

Expected: PASS（此时声明面与矩阵还没翻转，若红是预期的——Task 10 一起翻）。

- [ ] **Step 9: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add docs/STORAGE-SELECTION.yaml cmd/storagecheck/ Makefile
git commit -m "feat(cmd/storagecheck): 存储选型判据——何时用 SQLite / 何时用 PG 机判

三条判据：场景 storage 在允许集内 + storage 提供场景必需的全部能力 +
能力断言在代码里找得到落点（path#Symbol 可解析）。反向验证三条：声明面外
的 storage / 能力不齐 / 落点不可解析，各打红一次。#424
"
```

---

## Task 10: 删除假 SQLite 并翻转口径

**Files:**
- Delete: `examples/sqlitestore/`（22 个 `.go` 文件）
- Modify: `docs/DEPLOY-MATRIX.yaml`
- Modify: `docs/USAGE.md`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `docs/AI-GUIDE.md`
- Modify: `archcheck/error_sentinel_test.go`
- Modify: `taskboard/claim_gate_test.go`

- [ ] **Step 1: 删包**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git rm -r examples/sqlitestore
```

- [ ] **Step 2: 翻转部署矩阵的能力证据**

在 `docs/DEPLOY-MATRIX.yaml` 里，把三条能力的 `evidence` 从 sqlitestore 改指
sqliteadapter：

| 能力 | 旧 evidence | 新 evidence |
|---|---|---|
| `taskboard-persistence` | `examples/sqlitestore/store.go#Store` | `examples/sqliteadapter/board.go#BoardStore` |
| `startup-recovery` | `examples/sqlitestore/store.go#Open` | `examples/sqliteadapter/store.go#Open` |
| `single-store-full-state` | `examples/sqlitestore/store.go#Store.Apply` 等四条 | `examples/sqliteadapter/apply.go#Store` / `examples/sqliteadapter/effect.go#EffectStore` / `examples/sqliteadapter/snapshot.go#SnapStore` / `examples/sqliteadapter/event.go#EventStore` |

并把场景 `single-host-persistent` 的 `storage` 从 `examples/sqlitestore` 改为
`examples/sqliteadapter`，`notes` 里「sqlitestore 覆盖任务板 / 副作用 / 快照 / 事件四个面」
改为「sqliteadapter 覆盖同样四个面 + 控制面 + 栅栏 + 游标，四个面共用一份事务域
（Store.Apply），落真 SQLite(WAL) 引擎（#424）」。

- [ ] **Step 3: 同步 docs/USAGE.md 机器口径块**

**同一个提交**里翻 `deploy-matrix:begin/end` 块里 `single-host-persistent` 那一行
（存储列 + 必需能力列按矩阵同步）。否则 `make deploycheck` 当场红。

同时在 USAGE.md 里**新增一节「存储选型」**，写三条判据 + 边界（多进程即 PG）+
SQLite 时钟毫秒级的提示，并指向 `docs/STORAGE-SELECTION.yaml`。

- [ ] **Step 4: 改 README / AGENTS / AI-GUIDE**

- README：包表删 `examples/sqlitestore` 行、加 `examples/sqliteadapter` 行；
  文档一览加新包。
- AGENTS.md（4 处）与 docs/AI-GUIDE.md（1 处）：把「四份契约面共用一份事务域
  （JSONL 后端）」的表述改为「PG 与 SQLite 两引擎各自同构（各有各的 Store.Apply）」。
- 查一遍所有 `sqlitestore` / `SQLite` 提法，**历史语境**（如 `#188` 的清偿记录）
  保留，**现状语境**改写。

- [ ] **Step 5: 改两处测试引用**

- `archcheck/error_sentinel_test.go`：删 `examples/sqlitestore/lock_unix.go` 与
  `examples/sqlitestore/lock_windows.go` 两条豁免。
- `taskboard/claim_gate_test.go`：注释里的实现枚举去掉 sqlitestore。

- [ ] **Step 6: 跑门禁（这一提交必须一次绿）**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make architecture-check && make deploycheck && make storagecheck && make docsync
```

Expected: 全部 PASS。**若 deploycheck 红** ⇒ 矩阵与 USAGE.md 没在同一提交翻转，
或 evidence 的 `path#Symbol` 在代码里找不到（deploycheck 用 go/ast 判）。

- [ ] **Step 7: 扫残留引用**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
grep -rn 'sqlitestore' --include='*.go' --include='*.yaml' --include='*.md' . | grep -v 'DISTRIBUTED-EVOLUTION' | grep -v CHANGELOG
```

Expected: 无输出（或仅剩历史语境的允许项）。

- [ ] **Step 8: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add examples/ docs/DEPLOY-MATRIX.yaml docs/USAGE.md docs/AI-GUIDE.md README.md AGENTS.md archcheck/ taskboard/
git commit -m "refactor(examples): 删掉假 SQLite——sqlitastore 让位给真 sqliteadapter

examples/sqlitestore 是 flock + JSONL 的文件后端，不是 SQLite（#188 当年以
「不引入 sqlite 驱动」为由的替代方案）。删掉它，由真 SQLite 适配器
（examples/sqliteadapter）接管 single-host-persistent 场景的落点。

部署矩阵与 USAGE.md 的机器口径块在**同一提交**里翻转（否则 deploycheck
当场红）。台账与 CHANGELOG 的历史段落不改。#424
"
```

---

## Task 11: 台账与 CHANGELOG

**Files:**
- Modify: `docs/DISTRIBUTED-EVOLUTION-20261117.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: Task 1–10 的全部落地
- Produces: 台账 §28 + §24 落点更新 + §9 单号登记；CHANGELOG 新版本段

- [ ] **Step 1: 开 issue 拿单号**

```bash
gh issue create --title "【需求】真 SQLite 适配器与存储选型判据——删掉假 SQLite，把「何时用哪个」做成门禁可判" --body "..." 
```

记下返回的单号（正文里用 `#<单号>` 代替本计划里的 `#424`）。

- [ ] **Step 2: 台账新增 §28**

追加一节，内容至少含：

- 本轮做了什么（删假 SQLite / 建真适配器 / 补 PG 跨面事务 / 建 storagecheck）；
- **#188 的替代方案被本轮 supersede**（它交付的 JSONL 后端已删除）；
- 本轮实测的三条关键事实（时钟毫秒级 / 事件 ID 无空洞 / 水位线 ≠ 事件表 MAX）；
- 选型口径三条 + 「多进程即 PG」边界；
- 下一阶段建议。

- [ ] **Step 3: 更新 §24 的能力落点**

把 `single-store-full-state`（及其它改指了落点的能力）在 §24 里的落点改成新实现。

- [ ] **Step 4: §9 台账登记新单号**

在 §9 的登记表里加一行（新单号 + 状态 + 落点）。**状态标记词只有三个**：
「已合入」「已关闭」「仍开着」。

- [ ] **Step 5: 跑 ledgercheck**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make ledgercheck
```

Expected: PASS。**若红** ⇒ 单号状态词不合法，或正文引用的新单号没进 §9.1 登记面。

- [ ] **Step 6: CHANGELOG 新版本段**

在 `CHANGELOG.md` 顶部插入新版本段（版本号按发版纪律定；本计划阶段用未发布标记）。
**只插新段，不改历史条目。**

- [ ] **Step 7: Commit**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add docs/DISTRIBUTED-EVOLUTION-20261117.md CHANGELOG.md
git commit -m "docs(ledger): 第廿九轮台账——真 SQLite 适配器与存储选型判据（#<单号>）

#188 的替代方案（JSONL 文件后端）被本轮 supersede：它交付的东西已删除，
新单号承接真 SQLite 适配器。§24 能力落点改指新实现。
"
```

---

## Task 12: 全量门禁与出货

**Files:**
- 无新增文件（验证 + 出货）

- [ ] **Step 1: 跑全量门禁**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make check
```

Expected: 全绿。**重点看**：cover-gate 的**逐包下限 70%**——`examples/sqliteadapter`
**不在 `PKG_COV_EXEMPT` 里**（它与 pgstore 不同：SQLite 适配器在 unit 层可跑，
覆盖率是真的），所以必须真过 70%。若不达标，补用例，**不要**把它加进豁免。

- [ ] **Step 2: 跑 PG 集成层（需要 docker）**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make integration
```

Expected: PASS（PG 契约套件 + SIGKILL e2e）。

- [ ] **Step 3: 跑交叉构建**

```bash
export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local
cd /Users/zhangyi/my_project/goagent-kernel
make crossbuild
```

Expected: PASS（四平台全绿——这是 `modernc.org/sqlite` 纯 Go 的关键验证）。

- [ ] **Step 4: 确认无 U+FFFD**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git diff HEAD~1 --name-only | grep -E '\.(go|md|yaml)$' | xargs grep -n $'\xef\xbf\xbd' 2>/dev/null || echo "无坏字符"
```

- [ ] **Step 5: 提交并推送**

```bash
cd /Users/zhangyi/my_project/goagent-kernel
git add <具体路径>
git commit -m "..."
git push origin main
```

- [ ] **Step 6: 出货（按用户的「出货链整条委托」偏好）**

走完整链：CI 全绿 → annotated tag → tag CI 全绿 → Release → 回执提交。
tag 打在 **CI 全绿的提交**上；CHANGELOG 的版本段提交**只碰 CHANGELOG.md**。

---

## Self-Review

**1. Spec 覆盖核对**

| Spec 章节 | 对应任务 |
|---|---|
| §2 目标 1：真 SQLite 适配器，全面对齐 PG | Task 2–7 |
| §2 目标 2：PG 补跨面事务 | Task 8 |
| §2 目标 3：选型判据机判化 | Task 9 |
| §2 目标 4：删除 sqlitestore + 引用面 | Task 10 |
| §5.1 依赖登记（先改 policy） | Task 1 |
| §5.2 连接与事务形态 | Task 2（dsnParams / exec） |
| §5.3 表结构与契约面 | Task 2（schema）+ Task 3–7 |
| §5.3 事件 ID 无空洞 / 水位线上界 | Task 5 Step 1/4 |
| §5.4 跨面 Apply + 失败即作废整批 | Task 6 |
| §6.1 PG 跨面事务（withinTx 一处改动） | Task 8 Step 3/4 |
| §6.3 时钟差异 | Task 2（exec.now）+ Task 5（event 时钟） |
| §7 删除面与登记面同步 | Task 10 |
| §7.4 单号关系（#188 被 supersede） | Task 11 Step 2 |
| §8 storagecheck 三条判据 | Task 9 |
| §9.1 探针实测记录 | Task 2（注释）+ Task 5（判据）+ Task 9（边界） |
| §9.2 交付验收 | Task 12 |
| §10 实施顺序 | 任务顺序即该顺序 |

**2. 占位符扫描**：无 TBD / TODO / 「同上」/「类似 Task N」。所有「⚠️ 实施注意」
都是**指向真实仓内位置的强制核查**（先查真实符号名再写），不是待补内容。

**3. 类型一致性核对**

- `exec{db, tx, clock, failed}` —— Task 2 定义，Task 6 补 `failed`，Task 7/8 沿用 ✓
- `Store.Apply(ctx, fn func(tx *Tx) error) error`（SQLite）/ `Store.Apply(ctx, fn func(ctx) error) error`（PG）
  —— 两边签名**故意不同**：SQLite 侧给 `Tx` 门面，PG 侧沿用既有 tx-in-ctx 接缝 ✓
- `BoardStore` / `EffectStore` / `SnapStore` / `EventStore` / `ControlStore` /
  `Downstream` / `TailCursorStore` —— 命名与 PG 同款 ✓
- `resolveTenant(ctx, scoped, fallback) (string, error)` —— Task 6 定义，Task 4/5/7 沿用 ✓
- `cmd/storagecheck.check(root) ([]string, []string, error)` —— Task 9 定义，测试与 main 沿用 ✓

---

## 实施后现状（2026-11-19 – 2026-11-20，**已实施完毕**）

> 本节是**实施后的现状指针**；上面的 Task 清单**保持原样**（那是这份计划的价值）。
> 状态：**已实施完毕**。

- **假 SQLite 已删**：`examples/sqlitestore`（flock + JSONL，22 个 `.go` 文件 /
  3358 行）于 2026-11-20 删除（Task 10，commit `43b5834`）。登记面
  （`architecture-policy.yaml` 的 modules + thresholds.scan_roots、
  `api-lifecycle.yaml`）与引用面（`docs/DEPLOY-MATRIX.yaml` 的 6 处 evidence）
  **同提交**改指真适配器。
- **真适配器已就位**：`examples/sqliteadapter`（`modernc.org/sqlite`，纯 Go）已落地，
  门面暴露 11 个面访问器，与 PG 侧同款；`examples/pgstore` 的跨面事务（`Store.Apply`）
  已补齐。
- **选型门禁已上线**：`cmd/storagecheck` + 声明面 `docs/STORAGE-SELECTION.yaml`
  （`make storagecheck`），已接进 `make check` 与 `make unit` **两层** ⇒ CI 上真的跑。
- **口径已翻**：部署矩阵的 `single-host-persistent` 场景 storage 指向
  `examples/sqliteadapter`；`docs/USAGE.md` 补了「存储选型」专节（三条判据 +
  「单机多进程一律判 PostgreSQL」的硬边界 + SQLite 毫秒级时钟提示）。
- **历史不改写**：台账 / CHANGELOG / README 版本表 / `AGENTS.md` / `docs/AI-GUIDE.md`
  的历史段落**逐字保留**，只做**追加式**现状注。
- **关联单号**：#424。

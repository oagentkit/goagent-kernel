# 真 SQLite 适配器与存储选型判据（设计）

日期：2026-11-19
状态：设计已确认（brainstorming 四节逐节确认），待实施
关联台账：docs/DISTRIBUTED-EVOLUTION-20261117.md（新增 §28）

## 0. 一句话

删掉名为 `sqlitestore` 的假 SQLite（flock + JSONL + fsync），建一个**真 SQLite 适配器**
（`examples/sqliteadapter`，modernc.org/sqlite 驱动），把 PG 侧的跨面事务补齐，
并把「什么时候用 SQLite、什么时候用 PostgreSQL」做成**门禁可判**的规则。

## 1. 问题陈述

现状三份存储实现：

| 包 | 实际是什么 | 部署形态 |
|---|---|---|
| `examples/reference-runtime` | 内存存储（`package main`） | 单进程、不持久化 |
| `examples/pgstore` | **真** PostgreSQL（6270 行） | 共享库、多副本 |
| `examples/sqlitestore` | **不是 SQLite**：flock + JSONL 追加 + fsync（3358 行） | 单机、单写者 |

`examples/sqlitestore` 的包名与实现不符：`doc.go` 自述「为什么这里没有 SQLite 驱动」
——issue #188 要 SQLite(WAL) 适配器，但因「内核包不得为参考实现引入第三方依赖」被拒，
改以「契约级设计 + 零依赖文件后端」清偿（#188 关闭于 PR #249），SQLite 的 DDL / WAL /
事务边界只以**文档章节**形式留给宿主抄写。

后果：部署矩阵里 `single-host-persistent`（单机持久化）场景指向的 `examples/sqlitestore`
**不是 SQLite**。台账 §2 行「评审另有一处纠正点也坐实」早就记下这一事实。因此
「什么时候用 SQLite、什么时候用 PostgreSQL」在仓里目前是**散文**，且散文描述的那个
SQLite 在仓内**不存在**。

本设计让判据站得住：先让「SQLite」这三个字在仓里指一个真东西，再把选型规则做成
机器可判。

## 2. 目标与非目标

### 目标

1. `examples/sqliteadapter`：真 SQLite 适配器，契约面**全面对齐 PG**（四套契约面 +
   `controlplane.Store` + `sideeffect.FencedSink` + `eventbus.CursorStore` + 租户作用域 +
   跨面 `Apply`）。
2. `examples/pgstore` 补齐**跨面事务**（`Store.Apply`），使四个面可落进同一次提交。
3. 「何时用 SQLite / 何时用 PG」成为**门禁可判**的规则（`cmd/storagecheck`）。
4. 删除 `examples/sqlitestore` 及其登记面 / 引用面。

### 非目标（防范围蔓延）

- **不提供** JSONL → SQLite 的数据迁移工具。`sqlitestore` 是 Experimental、无兼容承诺，
  宿主自行决定重装或自行写迁移。
- **不提供** SQLite 的多副本 / 跨机形态。SQLite 单文件锁跨机不可靠，选型规则把多副本
  直接判给 PG。
- **不**给 `examples/sqliteadapter` 做 `cmd/` 冒烟二进制（PG 有 `cmd/pgsmoke`；
  SQLite 侧由契约套件 + 文档示例覆盖）。
- **不**让两个适配器共用判据代码（会把两个引擎耦在一起；契约套件是共同判据来源）。

## 3. 总体形态

三份存储实现重新分工：

| 包 | 角色 | 部署形态 | 契约覆盖 |
|---|---|---|---|
| `examples/reference-runtime` | 内存参考：证明判据本身对 | 单进程、不持久化 | 三套契约 |
| `examples/sqliteadapter`（新） | **真 SQLite**：单机持久化 | 单机、单副本 | 全部对齐 PG |
| `examples/pgstore` | **PG**：多副本 / 跨机 | 共享库 | 全部 |

`examples/sqliteadapter` 与 `examples/pgstore` **同构**：表结构、事务边界、时钟口径、
租户作用域、跨面 `Apply` **逐项对位**（每一项都能指出对方对应的落点）。

「对位」说的是**形态对应**，不是**代码共用**：判据逻辑在两包各写一份（存储层不同，
共用会把两个引擎耦在一起）。两者的正确性由**同一套契约套件**
（`boardtest` / `sidetest` / `RunSinkContract`）对齐——这正是共享套件存在的理由。

## 4. 选型判据（何时 SQLite / 何时 PG）

判据是三条**可机判**的事实，不是散文：

1. **需要跨进程 / 跨机共享写**（多副本、多机、K8s）⇒ 需要数据库级并发控制 ⇒ **PG**。
2. **需要重启后恢复且不引入外部服务进程**⇒ **SQLite**（单文件、WAL、无守护进程）。
3. **只要进程内判据正确，不要持久化**⇒ **memory**。

**边界情形写死**：单机多进程两者技术上都可行，但本仓口径取「**多进程即 PG**」——
SQLite 的单写者模型在多写者下退化为串行，且 `busy_timeout` 到点即失败（可判、
不静默丢写，但会降低可用性）。这条口径进判据，不留给读者猜。

**SQLite 场景附加提示**：SQLite 侧存储时钟精度为**毫秒**（见 §6.3），租约 TTL 建议 ≥ 1s。

## 5. `examples/sqliteadapter` 设计

### 5.1 依赖与登记（先改 policy 再写代码）

- 驱动：`modernc.org/sqlite`（**纯 Go，无 cgo**）⇒ `CGO_ENABLED=0` 交叉构建矩阵
  （`linux/amd64 linux/arm64 darwin/arm64 windows/amd64`）不受影响，与 `pgx/v5` 同款理由。
- `architecture-policy.yaml`：
  - `dependency_direction.ext_prefixes` 增 `modernc.org`；
  - `modules` 段增 `sqliteadapter` 条目，`requires` 只列
    `checkpoint / controlplane / eventbus / sideeffect / taskboard / tenant / oekit/pkg`
    + `ext:github.com/oekit/*` + `ext:modernc.org/sqlite`，**多引一个机制包即红**
    （与 `pgrefstore` 同款约束）；
  - `thresholds.scan_roots` 增 `examples/sqliteadapter`；
  - 删 `sqlitestore` 条目与 `examples/sqlitestore` scan_root。
- `api-lifecycle.yaml`：删 `examples/sqlitestore`，增 `examples/sqliteadapter`
  （Experimental，`since` 写实际发版版本）。

**破例登记必须写清**：为什么进依赖面（纯 Go、单机场景零外部服务）、代价（主 module
多一棵传递依赖树，`modernc.org/libc` 等约 12–15 个 module）、**拆除条件**（宿主形态
全部转 PG，或驱动改由宿主自带而本仓退回「落库形态规范」形态 ⇒ 本破例可撤）。

### 5.2 连接与事务形态

- 单文件数据库；`PRAGMA journal_mode=WAL` / `synchronous=FULL` / `busy_timeout=5000`。
- **写连接单条**（`SetMaxOpenConns(1)`），读连接另开（WAL 允许读写并发）。
  ⚠️ 它的职责**只限进程内**（同一进程内多个 goroutine 的写串行化）；**跨进程**串行化
  由下面的 `BEGIN IMMEDIATE` + 文件锁负责。两者不是同一件事，也不要用前者替代后者。
- 所有写事务以 **`BEGIN IMMEDIATE`** 开局（先拿写锁再干活，避免 defer 到 COMMIT 才
  升级锁导致半途失败）。这与 PG 的 `SELECT … FOR UPDATE` 行锁是**同一条纪律的两种落法**：
  读-判-写钉在同一次原子操作里。
- 驱动参数：`_txlock=immediate` + `_busy_timeout=5000` + `_journal_mode=WAL`。

**实测结论（本轮真机探针，见 §9.1）**：

- 跨连接（模拟跨进程）60 次并发写 ⇒ 计数 **60**、零错误 ⇒ `BEGIN IMMEDIATE` +
  `busy_timeout` 在**跨连接**下真串行化，**不依赖** `MaxOpenConns(1)`（后者只在单进程内
  有效）。两者职责不同：前者管跨进程，后者管进程内。
- 持锁事务未提交时，第二个连接写入被拒：`database is locked (5) (SQLITE_BUSY)`。
  **拿不到写锁就显式报错，不静默丢写**——与本仓既有纪律一致。
- ⚠️ DDL（`CREATE TABLE`）等需要写锁升级的操作在 `SQLITE_BUSY` 上**不等 busy_timeout
  直接抛**。适配器不在运行期做 DDL（建表只在 `EnsureSchema` 装配期），故不触发此路径。

### 5.3 表结构与契约面（全部对齐 PG）

| 面 | 契约 | 落库形态 |
|---|---|---|
| 任务板 | `taskboard.Board` / `RenewingBoard` | `board_task` 表；`Claim` 在 `BEGIN IMMEDIATE` 内读-判-写并铸造 token 与 `fence`；`Release`/`Complete` 只认 token，`RowsAffected==0` 再读一次区分 `ErrNotFound` / `ErrLeaseLost` |
| 副作用闸 | `sideeffect.Store` | `effect` / `effect_resource` 两级表（Key 级 + 资源级），与 `pgstore/effect_rows.go` 同形 |
| 进度快照 | `checkpoint.StoreCAS` | `snapshot` 表；带条件 upsert（`WHERE seq < excluded.seq` ⇒ 影响行数 0 即判陈旧） |
| 事件面 | `eventbus.Store` / `TailStore` / `WatermarkTailStore` / `CursorStore` | `event` 表 + `event_watermark` **每租户一行**水位（与租户作用域一致）+ `tail_cursor` 表 |
| 控制面 | `controlplane.Store` | **复用任务板那张表**（任务状态；两侧都不另设 `control_task` 表）+ `run` 表 + `control_effect` 表 + `control_version` 单行版本锚；三个 `Commit*` 在版本锚上「校验 → 留痕 → 写状态 → 推进版本」同事务 |
| 下游栅栏 | `sideeffect.FencedSink` | `downstream_fence` 表；带条件 upsert（`WHERE seen_fence <= presented`），**先原子预占再执行写入体** |

**事件 ID 的铸造与水位线（本轮实测挖出的关键点）**：

- 事件 ID 在 **`event_watermark` 每租户一行**上铸造（`UPDATE … SET last_id = last_id + 1 …
  RETURNING last_id`），**不是** `AUTOINCREMENT`。⇒ 回滚时 `last_id` 随事务一起回滚，
  **号被复用** ⇒ 序列空洞结构性不存在（实测：`first=1`、回滚烧掉 `2`、`next=2`）。
- `CommittedWatermark` 读**水位线行**；`ListAfter` 读**事件表**。两者**不是同一个数**：
  已提交水位线 ≤ 事件表 `MAX(id)`（实测：水位线 2 而事件表 MAX(id) 0，因为 ID 在水位线
  行上铸造而事件行尚未插入）。⇒ 实现与文档都必须写死这条上界关系，**不得**把
  「事件表 MAX(id)」当水位线。

**租户作用域**：与 PG 同形——`NewTenant*` 装配从 ctx 取租户，缺租户 ⇒
`tenant.ErrNoContext`（fail-closed），声明与 ctx 不一致 ⇒ `ErrTenantMismatch`。

### 5.4 跨面 `Apply`

`Store.Apply(ctx, fn func(tx *Tx) error) error`：`Tx` 上挂五个面的**事务内变体**
（`Tasks()` / `Effects()` / `Snapshots()` / `Events()` / `Downstream()`），一次事务一次
提交 ⇒ 跨面不会出现「一半已提交」。

**判据代码单一来源**：事务内的判据与独立提交**逐字相同**，不写两份。实现形态是
「判据函数 + 可切换的执行位置」（`sqlitestore` 当年用 `exec{db, tx}` 达成，
本包用同一形状：`exec{conn, tx}`）。

**失败即作废整批**：任一面操作返回错误 ⇒ 记失败 ⇒ 即使调用方吞掉错误继续写，
事务**仍回滚**（`sqlitestore` 当年的 `txCtx.failed` 语义）。缺这条，
「一半已提交」就会重现。

### 5.5 测试

在 **unit 层**真跑（不需 docker）：`boardtest` 全套 + `sidetest` 全套 +
`RunSinkContract` + 事件面 / 游标面判据。这是 SQLite 适配器相对 PG 的实打实优势，
也是它能占住「单机持久化」场景落点的关键。

## 6. `examples/pgstore` 跨面事务补齐

### 6.1 改造量（比预想小）

`sink.go` 已有「tx 塞进 ctx」的接缝（`TxFromContext` / `ExecWithin`），
各面方法统一走 `withinTx(ctx, pool, fn)` 助手。⇒ 补法是：

- **`withinTx` 认 ctx 里已有的 tx**：ctx 带 tx ⇒ 复用（不新开事务）；否则自己开一个
  并在返回前提交。**一处改动，四个面全部受益**——不必给 30 个方法各写 `...InTx` 变体。
- **新增 `Store` 门面**：聚合 pool + 四个面 + `Apply(ctx, fn func(ctx) error) error`——
  开一个 `pgx.Tx` 塞进 ctx，`fn` 返回错误 ⇒ 回滚整批，成功 ⇒ 提交。四个面在 `fn`
  内的任何写入自动落进同一事务。
- **失败即作废整批**（同 §5.4）。
- **既有 `PublishInTx` 保留**：它是「宿主在自己业务事务里发事件」的出口，语义不变。
  新 `Apply` 是「四个面一次提交」的出口。两者可共用同一 tx——这正是宿主想要的
  （控制面提交 + 事件发布同事务）。

### 6.2 时钟

PG 侧读 `clock_timestamp`（事务内实时，**非** BEGIN 冻结），且**必须在取得行锁之后**
取时刻（`CURRENT_TIMESTAMP` 固定在 BEGIN 时刻，会把等锁时长算进租约寿命）。本轮不改
PG 时钟口径。

### 6.3 SQLite 与 PG 的时钟差异（实测，须写进文档）

| | PG | SQLite |
|---|---|---|
| 时钟源 | `clock_timestamp` | `unixepoch('subsec')` |
| 事务内是否冻结 | 否 | **否**（实测同事务内两次取时刻相差 202 ms） |
| 精度 | 微秒（PG 原生） | **毫秒**（实测最小非零增量 0.9999 ms） |

- `unixepoch('subsec') * 1e6` 的尾数恒为 `.000` ⇒ 乘 1e6 只是把毫秒放大成「整数微秒」，
  **不产生新精度**。⇒ SQLite 侧按**毫秒**实现，精度差异写进文档与选型规则，
  不假装没有。
- 两边纪律一致（都在拿到锁后取时刻），但精度不同 ⇒ 选型规则里给 SQLite 场景加
  「租约 TTL 建议 ≥ 1s」。
- `UseClock` 注入接缝同样要有（`boardtest.ClockedBoard` / `sidetest.ClockedStore` 硬要求）。

## 7. 删除面与登记面同步

全仓 **33 个文件**提及 `sqlitestore`（包内 24 个 + 包外 9 个）。

### 7.1 删除

- `examples/sqlitestore/**`（22 个 `.go` 文件，3358 行）——**全部删除**。

### 7.2 改写

| 位置 | 处置 |
|---|---|
| `architecture-policy.yaml` | 删 `sqlitestore` 模块条目；`scan_roots` 去掉 `examples/sqlitestore`；新增 `sqliteadapter` 条目 + `ext_prefixes` 加 `modernc.org` |
| `api-lifecycle.yaml` | 删 `examples/sqlitestore`；新增 `examples/sqliteadapter` |
| `docs/DEPLOY-MATRIX.yaml` | `taskboard-persistence` / `startup-recovery` / `single-store-full-state` 三条能力的 `evidence` 改指 sqliteadapter；`single-host-persistent` 场景的 `storage` 改指 `examples/sqliteadapter` |
| `docs/USAGE.md` | 机器口径块（`deploy-matrix:begin/end`）同步——**必须与 YAML 同一提交翻转**，否则 `deploycheck` 当场红 |
| `README.md` | 包表删 sqlitestore 行、加 sqliteadapter 行；文档一览同步 |
| `AGENTS.md`（4 处）/ `docs/AI-GUIDE.md`（1 处） | 删「四份契约面共用一份事务域（JSONL 后端）」的表述，改为 PG + SQLite 两引擎各自同构 |
| `archcheck/error_sentinel_test.go` | 删 `examples/sqlitestore/lock_unix.go` / `lock_windows.go` 两条豁免 |
| `taskboard/claim_gate_test.go` | 注释里的四实现枚举去掉 sqlitestore |

### 7.3 只追加、不改历史

- `docs/DISTRIBUTED-EVOLUTION-20261117.md`：**历史段落不改**（历史记录不可篡改）；
  新增 §28 记本轮；§24 能力表的 `single-store-full-state` 落点改指新实现。
- `CHANGELOG.md`：历史条目不改；新增版本段记删除与新增。

### 7.4 单号关系（必须显式记）

`#188`（原要 SQLite 适配器）虽已关闭，但它交付的产物（JSONL 后端）**本轮被删除**
⇒ 须在 §28 写明「#188 的替代方案被本轮 supersede，真 SQLite 适配器由本轮单号承接」，
否则「已关闭单号对应的东西没了」就是账实差异。

## 8. `cmd/storagecheck`（选型判据执行体）

### 8.1 声明面

新增 `docs/STORAGE-SELECTION.yaml`：每个存储声明它**实际提供**的能力集合，能力→落点的
映射**从代码派生**，不手写。

### 8.2 判据三条

1. 场景的 `storage` 必须在声明面允许集内（否则红）；
2. 该 storage 必须提供场景 `required` 的**全部**能力（否则红）；
3. 声明面里「storage 提供某能力」的断言，**必须能在代码里找到对应落点**
   （`path#Symbol` 可解析，与 `deploycheck` 的 `contract` 证据同款判据）——
   否则「SQLite 提供租户隔离」就成了一句没人验证的话。

### 8.3 接线

`storagecheck` 接进 `make check` 依赖链 + `gates` 层（与 `deploycheck` / `ledgercheck`
同级）。**不**接进 `unit` 层（只读文件 + 判一致性，无编译依赖）。

### 8.4 为什么独立成包而不并入 `cmd/deploymatrix`

`deploymatrix` 判「能力齐不齐」，`storagecheck` 判「**存储选型对不对**」，两件事的
失败形态不同（前者红在缺能力，后者红在选错引擎）；混在一起会让「哪个门禁在管我」
变得不可追。代价是多一个 cmd 包（须在 `architecture-policy.yaml` 登记）。

## 9. 验收判据

### 9.1 本轮真机探针结论（已完成，实测记录）

用 `modernc.org/sqlite v1.61.0`（本机 module cache 已有）实测：

| 探针 | 结果 |
|---|---|
| `unixepoch('subsec')` 最小非零增量 | 0.9999 ms ⇒ 引擎时钟为毫秒级 |
| `unixepoch('subsec')*1e6` 尾数 | 恒 `.000` ⇒ 不产生新精度 |
| 引擎 vs Go 时钟偏差 | −0.738 ms（同机同钟，取样间隙所致） |
| 同一事务内两次取时刻 | 相差 201–202 ms ⇒ 事务内**不冻结** |
| 栅栏 upsert `ON CONFLICT … WHERE seen<=excluded.seen` | 陈旧写 `rows=0`、同代 `rows=1` ✅ |
| 快照 CAS upsert `WHERE seq<excluded.seq` | 陈旧 `rows=0` ✅ |
| 水位线 `UPDATE … RETURNING` | `last_id` 正常递增 ✅ |
| 跨连接 60 次并发写（模拟跨进程） | 计数 60、零错误 ✅ |
| 持锁时第二个连接写入 | `database is locked (5) (SQLITE_BUSY)` ✅ |
| 事件 ID 回滚后重铸 | `first=1`、回滚烧 2、`next=2` ⇒ 无空洞 ✅ |

**探针自身的教训（须写进实施计划的检查表）**：并发判据的探针**必须让两侧指向同一份
数据**。本轮第一次探针让第二个连接指向了另一个库文件（`t2.db`），测出「写锁未生效」
的假结论；修正为同库后结论反转。

### 9.2 交付验收

- `make check` 全绿（含新 `storagecheck`、架构门禁、覆盖率逐包 70%、docsync 一致性）；
- SQLite 适配器在 **unit 层**真跑 `boardtest` 全套 + `sidetest` 全套 + `RunSinkContract`
  + 事件 / 游标面判据；
- PG 侧 `Apply` 新增用例：跨面同事务提交（任务完成 + 事件写入同事务）+ 回滚整批
  （注入失败 ⇒ 四面状态全未变）；
- **反向验证**：`storagecheck` 注入三类违规（多副本场景的 storage 改成 sqlite、
  能力断言指向不存在的符号、storage 不在允许集内）各打红一次。

## 10. 实施顺序（先改 policy 再写代码）

1. `architecture-policy.yaml`：删 sqlitestore、增 sqliteadapter + `ext_prefixes`；
   `api-lifecycle.yaml` 同步。
2. `go.mod`：引入 `modernc.org/sqlite`（主 module，第二个驱动破例）。
3. 建 `examples/sqliteadapter`：schema → 判据核心（单一来源）→ 四个面 → 扩展面
   → 跨面 `Apply` → 租户作用域 → 单元层契约套件。
4. 改 `examples/pgstore`：`withinTx` 认 ctx tx + `Store.Apply` + 回滚整批 + 用例。
5. 建 `cmd/storagecheck` + `docs/STORAGE-SELECTION.yaml` + 接线 + 反向验证。
6. 删 `examples/sqlitestore`，改写 §7.2 清单。
7. 同步 `docs/DEPLOY-MATRIX.yaml` + `docs/USAGE.md`（**同一提交**）+ `README.md`
   + `AGENTS.md` + `docs/AI-GUIDE.md`。
8. 台账 §28 + §24 落点更新 + §9 单号登记；`CHANGELOG.md` 新版本段。
9. 全量 `make check` + 反向验证 + 出货链。

## 11. 已知边界

- SQLite 侧存储时钟精度为毫秒（§6.3），租约 TTL 建议 ≥ 1s。
- SQLite 单文件锁跨机不可靠 ⇒ 多副本 / 跨机一律 PG（选型规则判）。
- SQLite 的 DDL 写锁升级路径不等 `busy_timeout`；适配器不在运行期做 DDL。
- 两个适配器判据逻辑不共用代码 ⇒ 同一契约的两次实现可能漂 ⇒ 靠契约套件对齐
  （这正是共享套件存在的理由），不靠代码复用。
- 本轮不提供 JSONL → SQLite 迁移工具（§2 非目标）。

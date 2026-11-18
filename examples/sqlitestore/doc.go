// Package sqlitestore Standalone 部署形态的**单机持久化参考实现**：
// 任务板（taskboard.Board + RenewingBoard）、副作用闸（sideeffect.Store）、
// 进度快照（checkpoint.StoreCAS）、事件面（eventbus.Store + TailStore +
// WatermarkTailStore）**四个面共用一份事务域**——同一把锁、同一个日志、
// 一次提交。
//
// 生命周期: Experimental（v0.39.0 定级）——参考实现，随契约套件与参考部署
// 演进，不承诺任何兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级
// 规则见 docs/api-compat.md。
//
// # 依赖纪律：为什么这里没有 SQLite 驱动（先读这段）
//
// issue #188 要的是 SQLite(WAL) 适配器。但本仓是**单一 Go module**（examples
// 不是独立 module，`go test ./...` 从根跑），而主干依赖面有一条硬纪律：
// 内核包不得为参考实现引入第三方依赖。go.mod 里唯一为示例破例的驱动是
// pgx/v5（examples/pgstore），它在 architecture-policy.yaml 里登记为
// 「只进 pgrefstore 的依赖图」并有注释说明。
//
// 引入 modernc.org/sqlite（纯 Go，无 cgo）在技术上可行，但它会新增一个
// **数 MB 的传递依赖树**到主 module，且 sqlite 的版本会随工具链/安全发布
// 漂移——为了一个示例性适配器付这份维护成本，不满足「依赖面窄」的判据。
// 故本包采用任务允许的替代方案：
//
//   - **契约级设计**：SQLite 的落库形态（DDL / WAL 参数 / 事务边界 /
//     单连接写串行化 / 时钟口径）完整写在下面的「SQLite 落库形态」一节，
//     宿主在自己的 module 里加驱动后可以逐条照抄；
//   - **可运行的最小文件后端**：`Store` 用 **flock 串行化 + JSONL 追加 +
//     fsync** 实现同一套语义。它不是 SQLite，但它是**同一份契约的另一种
//     落法**——boardtest 与 sidetest 全套判据在它身上真跑，证明这些语义
//     在本机单写者形态下可满足。
//
// 明确不承诺：本包不是多副本共享协调库。数据目录放 NFS / 对象存储让多台
// 机器共同写，等于让「单写者」前提失效——多副本协调一律用 PG（与
// examples/pgstore 的分工）。
//
// # 四个面与一份事务域（issue #419）
//
// 四个面不是四份数据，而是**同一份事务日志重放出来的四个投影**：
//
//	面            契约                        入口
//	任务板        taskboard.Board/Renewing   Store.Claim / Complete / …
//	副作用闸      sideeffect.Store            Store.Effects().Acquire / …
//	进度快照      checkpoint.StoreCAS         Store.Snapshots().SaveIfNewer / …
//	事件面        eventbus.Store/TailStore    Store.Events().Insert / ListAfter / …
//
// 它们共用一把进程内 mutex + 一把跨进程 flock、一个日志文件、一次 fsync。
// 更要紧的是：**跨面的变更能落在同一次提交里**（`Store.Apply`）——任务
// 状态、副作用闸、进度快照、事件行写在日志的**同一行**里（journal.go 的
// v2 批次格式）。⇒ 跨面不会出现「一半已提交」：要么整批可见，要么整批
// 不可见。
//
// 事件 ID 取自**已提交水位线**（铸造序 == 提交序，回滚不烧号）⇒ 序列空洞
// 结构性地不存在 ⇒ TailStore 的「无空洞推进」硬要求由构造满足，而不是靠
// 「读的时候小心别跳过」这种约定。
//
// # SQLite 落库形态（宿主抄这一节）
//
// 打开数据库后先执行（顺序有意义）：
//
//	PRAGMA journal_mode = WAL;      -- 读不阻塞写，崩溃后按 WAL 重放
//	PRAGMA synchronous = FULL;      -- 提交即落盘（单机可恢复的前提）
//	PRAGMA busy_timeout = 5000;     -- 跨进程写锁的等待上限（不静默丢写）
//
// 连接池只有**一个**写连接（database/sql 的 db.SetMaxOpenConns(1)；读可以
// 另开只读连接池，WAL 允许读写并发）。所有写事务用 `BEGIN IMMEDIATE` 开局
// ——它是「先拿写锁再干活」，避免 defer 到 COMMIT 才升级锁导致的
// SQLITE_BUSY 半途失败。四个面各自的表（宿主按自己的字段裁剪）：
//
//	CREATE TABLE IF NOT EXISTS board_task (
//	    id         TEXT PRIMARY KEY,
//	    title      TEXT    NOT NULL DEFAULT '',
//	    deps       TEXT    NOT NULL DEFAULT '[]',  -- JSON 数组
//	    state      TEXT    NOT NULL,
//	    owner      TEXT,
//	    token      TEXT,
//	    fence      INTEGER NOT NULL DEFAULT 0,     -- 接管代次水位（释放不清）
//	    until_unix INTEGER,                        -- 毫秒；NULL = 不限期
//	    seq        INTEGER NOT NULL DEFAULT 0
//	);
//
// 判据落库与 pgstore 的 board.go 同形：
//
//   - Claim：`BEGIN IMMEDIATE` → SELECT 任务行与依赖行 → 判终态/依赖/活租约
//     → 铸令牌与 fence（`fence = fence + 1`）→ UPDATE → COMMIT。读-判-写
//     在同一个写事务里，跨进程赢家恰好一个（boardtest.ConcurrentClaim 判）。
//   - Release / Complete：`UPDATE ... WHERE id=? AND token=? AND state='claimed'`，
//     RowsAffected==0 ⇒ 再读一次区分 ErrNotFound / ErrLeaseLost；只认令牌。
//   - Renew：同一事务内校验 token + 未过期，再写 until_unix；过期即资格终止
//     （ErrLeaseLost），不建第二套时钟判据。
//   - 到期时刻用**数据库时钟**（`unixepoch('subsec')*1000` 或 Go 侧传入的
//     存储时钟），不用进程本地 time.Now——跨进程不假设时钟一致。
//
// 副作用闸的两级状态（Key 级 / 资源级）见 pgstore 的 effect*.go；进度快照
// 的单调写见 checkpoint/cas.go；事件面的水位线见 eventbus/tail.go。三者的
// 判据都不在本包重写——本包只提供**落库形态**。
//
// 本包文件后端的对应关系：
//
//	SQLite WAL 帧 + fsync        ⇔ tasks.jsonl 的一行 + file.Sync()
//	BEGIN IMMEDIATE / 写锁       ⇔ flock(LOCK_EX) 包住「重放 → 判 → 追加」
//	崩溃后 WAL 重放              ⇔ 打开/写入前重放 JSONL，截断未换行的残尾
//	db.SetMaxOpenConns(1)        ⇔ 进程内 mutex（跨进程靠 flock）
//	PRAGMA busy_timeout          ⇔ lockTimeout（拿不到锁显式报错，不丢写）
//	一个事务 = 一行              ⇔ journal.go 的 v2 批次（跨面原子性）
//
// # 已知边界（宿主照抄时要一起带上）
//
//   - **重放是 O(n)**：每次事务都从头重放整个日志。参考实现够用，生产宿主
//     用 SQLite 就是 O(1) 索引查找——这不是语义差别，是量级差别。
//   - **载荷的数字会变成 float64**：事件载荷经 JSON 往返后数字统一为
//     float64（"计数": 3 读回来是 3.0）。与 examples/pgstore 的 JSONB 同款；
//     需要整数保真就走字符串或宿主自己的序列化。
//   - **日志文件名没改**：文件里现在装的不止任务（四个面的批次），但名字
//     仍是 tasks.jsonl——改名会让宿主已有数据目录看起来是空的（比报错更难
//     查）。
//   - **写入粒度是整个事务**：一个批次一行 ⇒ 文件会随事务数线性增长，
//     且没有日志压缩。生产宿主用 SQLite 的表结构。
//
// # 它是什么、不是什么
//
// 是：单机 + 可恢复形态的**契约级参考**（重启后四个面的状态都在；半截写
// 被丢弃；并发认领只有一个赢家；跨面变更同生共死）。不是：SQLite 本身的
// 绑定、查询优化、多副本协调。宿主的生产实现应照「SQLite 落库形态」一节
// 写，而不是把本包的 JSONL 文件格式当成生产存储协议。
package sqlitestore

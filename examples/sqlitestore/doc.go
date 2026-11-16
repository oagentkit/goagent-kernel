// Package sqlitestore Standalone 部署形态的**单机持久化参考实现**：
// taskboard.Board + taskboard.RenewingBoard 的「单写者 + WAL 语义 + 崩溃重放」
// 形态，与 examples/pgstore 同构（同一批共享契约套件判它）。
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
//     落法**——boardtest 全套判据在它身上真跑，证明这些语义在本机单写者
//     形态下可满足。
//
// 明确不承诺：本包不是多副本共享协调库。数据目录放 NFS / 对象存储让多台
// 机器共同写，等于让「单写者」前提失效——多副本协调一律用 PG（与
// examples/pgstore 的分工）。
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
// SQLITE_BUSY 半途失败。表：
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
// 本包文件后端的对应关系：
//
//	SQLite WAL 帧 + fsync        ⇔ tasks.jsonl 的一行 + file.Sync()
//	BEGIN IMMEDIATE / 写锁       ⇔ flock(LOCK_EX) 包住「重放 → 判 → 追加」
//	崩溃后 WAL 重放              ⇔ 打开/写入前重放 JSONL，截断未换行的残尾
//	db.SetMaxOpenConns(1)        ⇔ 进程内 mutex（跨进程靠 flock）
//	PRAGMA busy_timeout          ⇔ lockTimeout（拿不到锁显式报错，不丢写）
//
// # 范围：只承担 taskboard 的两面
//
// 本包实现 taskboard.Board + taskboard.RenewingBoard（与 pgstore 的 board.go
// 同一范围）。sideeffect.Store / checkpoint.StoreCAS / eventbus.TailStore 各有
// 自己的契约与判据套件（sidetest / checkpoint 用例 / boardtest），不在本示例
// 的落库范围里——一个示例同时塞四套契约会变成「哪段判据归哪把锁」都读不清的
// 大文件，而每套契约的原子性要求并不相同。宿主接线时按契约分别实现（或直接
// 用 pgstore 那一套跨进程形态）。
//
// # 它是什么、不是什么
//
// 是：单机 + 可恢复形态的**契约级参考**（重启后任务状态、令牌、代次都在；
// 半截写被丢弃；并发认领只有一个赢家）。不是：SQLite 本身的绑定、查询
// 优化、多副本协调。宿主的生产实现应照「SQLite 落库形态」一节写，而不是
// 把本包的 JSONL 文件格式当成生产存储协议。
package sqlitestore

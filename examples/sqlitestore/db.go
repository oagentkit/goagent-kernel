// db.go —— 共享事务域：一把锁、一个日志、一次提交。
//
// 四个面（任务板 / 副作用闸 / 进度快照 / 事件行）共用这一份 db：进程内
// mutex（单写者）+ 跨进程 flock（见 lock_*.go），写日志只有一条 append
// 路径。⇒ 「同一把锁、同一个日志」不是约定，是**只有一条写路径**这件事
// 的直接后果。
//
// 判定读的是**重放后的最新状态**，不依赖进程内缓存——重启后第一件事就是
// 重放，崩溃不会让「谁占着任务」这件事出现两个答案。
package sqlitestore

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	oerrors "github.com/oekit/pkg/errors"
)

// 文件名（目录 = 一个独立存储实例；测试每个子测试一个临时目录）。
//
// ⚠️ 文件名没改，但**内容已经不止任务**了：v2 格式下一个文件里装的是四个
// 面的事务批次（见 journal.go）。保留旧名是为了让宿主已有的数据目录直接
// 打开——改名会让旧目录看起来是空的（比报错更难查）。
const (
	journalName = "tasks.jsonl"
	lockName    = "tasks.lock"
)

// lockTimeout 等文件锁的上限。
//
// 等价于 SQLite 的 busy_timeout：拿不到写锁就**显式失败**，而不是继续写
// （丢写是把「单写者」承诺变成装饰的最短路径）。
const lockTimeout = 5 * time.Second

// db 四个面共享的事务域。
type db struct {
	dir      string
	journal  string
	lockPath string

	// mu 进程内单写者（跨进程靠 flock）。
	mu sync.Mutex
	// clockMu 单独护住存储时钟：注入的回调可能被并发替换。
	clockMu sync.RWMutex
	now     func() time.Time
	f       *os.File
}

// openDB 打开（或创建）一个单机存储目录。
func openDB(dir string) (*db, error) {
	if dir == "" {
		return nil, oerrors.New("sqlitestore: 数据目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, oerrors.Wrap(err, "sqlitestore: 建数据目录失败", "dir", dir)
	}
	path := filepath.Join(dir, journalName)
	// 不用 O_APPEND：Windows 上 O_APPEND 打开的文件句柄没有 FILE_WRITE_DATA，
	// f.Truncate 会报 Access is denied（残尾恢复必用）。写者由 flock 串行化，
	// 每次追加前显式 Seek 到末尾即可，语义与 O_APPEND 相同。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, oerrors.Wrap(err, "sqlitestore: 打开日志失败", "path", path)
	}
	return &db{
		dir:      dir,
		journal:  path,
		lockPath: filepath.Join(dir, lockName),
		now:      time.Now,
		f:        f,
	}, nil
}

// clock 取存储时钟。**回调在锁外调用**（issue #317：注入的时钟可能重入
// 本包，持锁调用即死锁）。
func (d *db) clock() time.Time {
	d.clockMu.RLock()
	now := d.now
	d.clockMu.RUnlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

// setClock 注入存储时钟。
func (d *db) setClock(now func() time.Time) {
	if now == nil {
		return
	}
	d.clockMu.Lock()
	d.now = now
	d.clockMu.Unlock()
}

// transact 执行一次写事务：加锁 → 重放 → 判定 → 追加 → fsync。
//
// fn 返回错误时**不写任何东西**（判定失败不留半截状态）。fn 暂存的全部
// 记录（可能跨四个面）作为**一个批次**落盘 ⇒ 跨面要么全成、要么全不成。
func (d *db) transact(fn func(tx *txCtx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return oerrors.New("sqlitestore: 存储已关闭")
	}
	unlock, err := acquireLock(d.lockPath)
	if err != nil {
		return err
	}
	defer unlock()

	state, err := loadState(d.journal)
	if err != nil {
		return err
	}
	// 重放时末行若是残尾，先截掉再追加（否则追加会接在半截行后面）。
	if err := repairTornTailLocked(d.f, d.journal); err != nil {
		return err
	}
	tx := &txCtx{st: state}
	if err := fn(tx); err != nil {
		tx.fail(err)
	}
	// **提交闸：唯一判定点**——本次事务有任何失败 ⇒ 整批作废（不留半截
	// 状态）。判定只有这一处：放两处就等于同一个判断有两个主人（改坏一处，
	// 测试看不出区别 ⇒ 那条判据事实上没有承重夹具）。
	if tx.failed != nil {
		return tx.failed
	}
	if len(tx.writes) == 0 {
		return nil
	}
	return appendBatchLocked(d.f, d.journal, tx.writes)
}

// close 关闭日志文件（不删除数据）。
func (d *db) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	if err != nil {
		return oerrors.Wrap(err, "sqlitestore: 关闭日志失败")
	}
	return nil
}

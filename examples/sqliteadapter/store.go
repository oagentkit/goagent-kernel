// store.go —— 门面骨架：开库、装配期选项、生命周期。
//
// 门面只管三件事：开库、装选项、关库。四个契约面（任务板 / 副作用闸 / 进度
// 快照 / 事件面）与跨面 Apply 由后续任务在本骨架上落地——它们共用同一份
// exec 执行位置抽象（exec.go），因此「独立提交」与「事务内」两条路径走的是
// 同一套判据函数。

package sqliteadapter

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// dsnParams 打开参数：
//   - _txlock=immediate：写事务一律 BEGIN IMMEDIATE（先拿写锁再干活）。
//   - _busy_timeout=5000：跨进程写锁的等待上限（不静默丢写）。
//   - _journal_mode=WAL：读不阻塞写，崩溃后按 WAL 重放。
//   - _synchronous=FULL：synchronous=FULL（驱动**同时接受枚举词与数字**：
//     0 OFF / 1 NORMAL / 2 FULL / 3 EXTRA——写 FULL 与写 2 等价
//     （实测两者都是 PRAGMA synchronous=2）；只有**非法值**才报错，
//     如 _synchronous=bogus 会显式报 invalid _synchronous）。
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

// exec 造一个**独立事务**的执行位置（判据单一来源的入口）。
//
// 事务内的执行位置由调用方在拿到 tx 后另造（exec{db, tx}）——两者共用同一套
// 判据函数，只是「谁负责提交」不同。
func (s *Store) exec() exec { return exec{db: s.db, clock: s.clock} }

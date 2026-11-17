// store.go —— taskboard.Board + RenewingBoard 的文件后端实现。
//
// 每个公开方法都是一次「加锁 → 重放 → 判 → 追加 → fsync」的事务：
//
//   - 进程内用 mutex（单写者），跨进程用 flock（见 lock_*.go）；
//   - 判定读的是**重放后的最新状态**，不依赖进程内缓存——重启后第一件事
//     就是重放，崩溃不会让「谁占着任务」这件事出现两个答案；
//   - 令牌随机 128 位（跨进程不复用），接管代次是独立水位（释放/完成不清）。
package sqlitestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// 文件名（目录 = 一个独立存储实例；测试每个子测试一个临时目录）。
const (
	journalName = "tasks.jsonl"
	lockName    = "tasks.lock"
)

// lockTimeout 等文件锁的上限。
//
// 等价于 SQLite 的 busy_timeout：拿不到写锁就**显式失败**，而不是继续写
// （丢写是把「单写者」承诺变成装饰的最短路径）。
const lockTimeout = 5 * time.Second

// Option 装配项。
type Option func(*Store)

// WithClock 注入存储时钟（契约套件的过期场景要确定性推进）。
func WithClock(clock func() time.Time) Option {
	return func(s *Store) {
		if clock != nil {
			s.now = clock
		}
	}
}

// Store 单机文件后端（taskboard.Board + taskboard.RenewingBoard）。
//
// ⚠️ 单写者形态：一个目录同一时刻只有一个写事务（flock）；不要把它放到
// NFS/共享盘上当多副本协调库（见包文档「明确不承诺」）。
type Store struct {
	dir      string
	journal  string
	lockPath string

	mu  sync.Mutex
	now func() time.Time
	f   *os.File
}

// Open 打开（或创建）一个单机存储目录。
//
// 打开时会先拿一次写锁做崩溃恢复：截掉未换行的残尾。目录可重复打开
// （多个 Store 实例 = 多个客户端）；每个事务重新拿锁并重放最新日志。
func Open(dir string, opts ...Option) (*Store, error) {
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
	s := &Store{
		dir:      dir,
		journal:  path,
		lockPath: filepath.Join(dir, lockName),
		now:      time.Now,
		f:        f,
	}
	for _, o := range opts {
		o(s)
	}
	// 启动恢复：在写锁内截掉崩溃残尾（不允许在无锁状态下改文件）。
	if err := s.transact(func(map[taskboard.TaskID]journalRecord) ([]journalRecord, error) {
		return nil, nil
	}); err != nil {
		_ = f.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭日志文件（不删除数据）。
func (s *Store) Close() error {
	if s == nil || s.f == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.f.Close()
	s.f = nil
	if err != nil {
		return oerrors.Wrap(err, "sqlitestore: 关闭日志失败")
	}
	return nil
}

// UseClock 注入存储时钟（boardtest.ClockedBoard）。
func (s *Store) UseClock(now func() time.Time) {
	if now == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// transact 执行一次写事务：加锁 → 重放 → fn 判定并产出要追加的记录 → 逐条
// 追加并 fsync。fn 返回错误时**不写任何东西**（判定失败不留半截状态）。
func (s *Store) transact(fn func(map[taskboard.TaskID]journalRecord) ([]journalRecord, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return oerrors.New("sqlitestore: 存储已关闭")
	}
	unlock, err := acquireLock(s.lockPath)
	if err != nil {
		return err
	}
	defer unlock()

	state, err := loadJournalLocked(s.journal)
	if err != nil {
		return err
	}
	// 重放时末行若是残尾，先截掉再追加（否则追加会接在半截行后面）。
	if err := repairTornTailLocked(s.f, s.journal); err != nil {
		return err
	}
	writes, err := fn(state)
	if err != nil {
		return err
	}
	for _, rec := range writes {
		if err := appendLocked(s.f, s.journal, rec); err != nil {
			return err
		}
	}
	return nil
}

// Seed 摆初始任务（任务拓扑是宿主资产；契约没有「创建」入口）。
//
// 与 pgstore.BoardStore.Seed 同形态：同 id 重摆覆盖内容并清租约，接管代次
// 水位保留；落库失败 panic（装载入口不容静默失败）。
func (s *Store) Seed(tasks ...taskboard.Task) {
	err := s.transact(func(state map[taskboard.TaskID]journalRecord) ([]journalRecord, error) {
		out := make([]journalRecord, 0, len(tasks))
		for _, t := range tasks {
			if t.ID == "" {
				continue
			}
			prev := state[t.ID]
			fresh := journalRecord{
				Task:  cloneTask(t),
				Fence: prev.Fence,
			}
			fresh.Task.Owner = ""
			fresh.Task.Lease = taskboard.Lease{}
			fresh.Task.Seq = prev.Task.Seq + 1
			out = append(out, fresh)
		}
		return out, nil
	})
	if err != nil {
		panic(fmt.Sprintf("sqlitestore: Seed 摆任务失败（装载入口不容静默失败）: %v", err))
	}
}

// List 返回一致快照（一次重放；按 id 升序）。
func (s *Store) List(_ context.Context) ([]taskboard.Task, error) {
	var out []taskboard.Task
	err := s.transact(func(state map[taskboard.TaskID]journalRecord) ([]journalRecord, error) {
		out = make([]taskboard.Task, 0, len(state))
		for _, rec := range state {
			out = append(out, cloneTask(rec.Task))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Claim 认领任务（读-判-写在同一个写事务里；令牌与代次由存储铸造）。
func (s *Store) Claim(_ context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	var out taskboard.Task
	err := s.transact(func(state map[taskboard.TaskID]journalRecord) ([]journalRecord, error) {
		rec, ok := state[id]
		if !ok {
			return nil, oerrors.Wrap(taskboard.ErrNotFound, "sqlitestore: 任务不存在", "task", string(id))
		}
		now := s.now()
		// 状态可认领判定统一走 taskboard.State.Claimable（issue #360）：
		// 词表外 → ErrUnknownState，终态 → ErrTerminal。
		if cerr := rec.Task.State.Claimable(); cerr != nil {
			return nil, oerrors.Wrap(cerr, "sqlitestore: 任务状态不可认领",
				"task", string(id), "state", string(rec.Task.State))
		}
		for _, dep := range rec.Task.Deps {
			depRec, ok := state[dep]
			if !ok || depRec.Task.State != taskboard.StateDone {
				return nil, oerrors.Wrap(taskboard.ErrBlocked, "sqlitestore: 依赖未完成",
					"task", string(id), "dep", string(dep))
			}
		}
		if held(rec, now) {
			return nil, oerrors.Wrap(taskboard.ErrTaken, "sqlitestore: 任务已被他人认领", "task", string(id))
		}
		token, err := mintToken()
		if err != nil {
			return nil, err
		}
		next := rec
		next.Fence = rec.Fence + 1
		next.Task.State = taskboard.StateClaimed
		next.Task.Owner = agent
		next.Task.Lease = taskboard.Lease{
			Token: token,
			Fence: taskboard.Fence(next.Fence),
		}
		if req.TTL > 0 {
			next.Task.Lease.Until = now.Add(req.TTL)
		}
		next.Task.Seq = rec.Task.Seq + 1
		out = cloneTask(next.Task)
		return []journalRecord{next}, nil
	})
	if err != nil {
		return taskboard.Task{}, err
	}
	return out, nil
}

// Release 归还认领（凭令牌；非持有者一律被拒）。
func (s *Store) Release(_ context.Context, id taskboard.TaskID, token string) error {
	return s.finish(id, token, taskboard.StateOpen)
}

// Complete 标记完成（凭令牌；终态不带 owner/租约）。
func (s *Store) Complete(_ context.Context, id taskboard.TaskID, token string) error {
	return s.finish(id, token, taskboard.StateDone)
}

// finish 释放/完成共用的条件写：只认「当前认领态 + 令牌一致」。
func (s *Store) finish(id taskboard.TaskID, token string, state taskboard.State) error {
	return s.transact(func(all map[taskboard.TaskID]journalRecord) ([]journalRecord, error) {
		rec, ok := all[id]
		if !ok {
			return nil, oerrors.Wrap(taskboard.ErrNotFound, "sqlitestore: 任务不存在", "task", string(id))
		}
		if rec.Task.State != taskboard.StateClaimed || rec.Task.Lease.Token != token {
			return nil, oerrors.Wrap(taskboard.ErrLeaseLost, "sqlitestore: 租约令牌不匹配",
				"task", string(id), "state", string(rec.Task.State))
		}
		next := rec
		next.Task.State = state
		next.Task.Owner = ""
		next.Task.Lease = taskboard.Lease{} // 代次水位在 rec.Fence 里，不清
		next.Task.Seq = rec.Task.Seq + 1
		return []journalRecord{next}, nil
	})
}

// Renew 续期并确认持有资格（令牌仍是最新代且租约未过期）。
func (s *Store) Renew(_ context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	var out taskboard.Task
	err := s.transact(func(all map[taskboard.TaskID]journalRecord) ([]journalRecord, error) {
		rec, ok := all[id]
		if !ok {
			return nil, oerrors.Wrap(taskboard.ErrNotFound, "sqlitestore: 任务不存在", "task", string(id))
		}
		now := s.now()
		if rec.Task.State != taskboard.StateClaimed || rec.Task.Lease.Token != token {
			return nil, oerrors.Wrap(taskboard.ErrLeaseLost, "sqlitestore: 租约令牌不匹配", "task", string(id))
		}
		if !taskboard.LeaseHeld(rec.Task, now) {
			return nil, oerrors.Wrap(taskboard.ErrLeaseLost,
				"sqlitestore: 租约已过期，资格终止（请重新认领）", "task", string(id))
		}
		next := rec
		if req.TTL > 0 {
			next.Task.Lease.Until = now.Add(req.TTL)
		} else {
			next.Task.Lease.Until = time.Time{}
		}
		next.Task.Seq = rec.Task.Seq + 1
		out = cloneTask(next.Task)
		return []journalRecord{next}, nil
	})
	if err != nil {
		return taskboard.Task{}, err
	}
	return out, nil
}

// mintToken 铸一枚随机令牌（跨进程不复用不重合 ⇒ ABA 封死）。
func mintToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", oerrors.Wrap(err, "sqlitestore: 铸造令牌失败")
	}
	return "tok-" + hex.EncodeToString(b[:]), nil
}

// 编译期断言：本实现满足任务板的全部契约面。
var (
	_ taskboard.Board         = (*Store)(nil)
	_ taskboard.RenewingBoard = (*Store)(nil)
)

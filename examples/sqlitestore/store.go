// store.go —— 存储实例入口 + 任务面（taskboard.Board + RenewingBoard）。
//
// 每个公开方法都是一次「加锁 → 重放 → 判 → 追加 → fsync」的事务：
//
//   - 进程内用 mutex（单写者），跨进程用 flock（见 lock_*.go）；
//   - 判定读的是**重放后的最新状态**，不依赖进程内缓存——重启后第一件事
//     就是重放，崩溃不会让「谁占着任务」这件事出现两个答案；
//   - 令牌随机 128 位（跨进程不复用），接管代次是独立水位（释放/完成不清）。
//
// 任务面之外，Store 还给出另外三个面（Effects / Snapshots / Events）——
// 它们**共享同一份事务域**（同一把锁、同一个日志），并可用 Apply 把四个面
// 的变更放进**同一次提交**。
package sqlitestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// Option 装配项。
type Option func(*Store)

// WithClock 注入存储时钟（契约套件的过期场景要确定性推进）。
func WithClock(clock func() time.Time) Option {
	return func(s *Store) {
		s.UseClock(clock)
	}
}

// Store 单机文件后端：四个面共享一份事务域。
//
// ⚠️ 单写者形态：一个目录同一时刻只有一个写事务（flock）；不要把它放到
// NFS/共享盘上当多副本协调库（见包文档「明确不承诺」）。
type Store struct {
	*TaskStore
}

// Open 打开（或创建）一个单机存储目录。
//
// 打开时会先拿一次写锁做崩溃恢复：截掉未换行的残尾。目录可重复打开
// （多个 Store 实例 = 多个客户端）；每个事务重新拿锁并重放最新日志。
func Open(dir string, opts ...Option) (*Store, error) {
	d, err := openDB(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{TaskStore: &TaskStore{exec: exec{db: d}}}
	for _, o := range opts {
		o(s)
	}
	// 启动恢复：在写锁内截掉崩溃残尾（不允许在无锁状态下改文件）。
	if err := d.transact(func(tx *txCtx) error { return nil }); err != nil {
		_ = d.close()
		return nil, err
	}
	return s, nil
}

// Close 关闭日志文件（不删除数据）。
func (s *Store) Close() error {
	return s.exec.db.close()
}

// Effects 副作用面（与任务面共享同一份事务域）。
func (s *Store) Effects() *EffectStore { return &EffectStore{exec: s.exec} }

// Snapshots 进度快照面（与任务面共享同一份事务域）。
func (s *Store) Snapshots() *SnapshotStore { return &SnapshotStore{exec: s.exec} }

// Events 事件面（与任务面共享同一份事务域）。
func (s *Store) Events() *EventStore { return &EventStore{exec: s.exec} }

// Apply 在**一个事务**里提交四个面的变更。
//
// 回调返回错误 ⇒ 整批丢弃（四面都没变）；返回 nil ⇒ 四个面的变更作为
// **一行**落盘（journal.go 的 v2 批次）⇒ 跨面不会出现「一半已提交」。
//
// 这是「单一存储覆盖全部状态」的机器形态：任务状态、副作用闸、进度快照、
// 事件行（Outbox）在同一次提交里落盘。
func (s *Store) Apply(ctx context.Context, fn func(tx *Tx) error) error {
	now := s.exec.now()
	return s.exec.run(ctx, func(c *txCtx) error {
		c.now = now
		return fn(&Tx{ctx: c})
	})
}

// Seed 摆初始任务（任务拓扑是宿主资产；契约没有「创建」入口）。
//
// 与 pgstore.BoardStore.Seed 同形态：同 id 重摆覆盖内容并清租约，接管代次
// 水位保留；落库失败 panic（装载入口不容静默失败）。
func (s *Store) Seed(tasks ...taskboard.Task) {
	if err := s.TaskStore.Seed(tasks...); err != nil {
		panic(fmt.Sprintf("sqlitestore: Seed 摆任务失败（装载入口不容静默失败）: %v", err))
	}
}

// TaskStore 任务面（taskboard.Board + taskboard.RenewingBoard）。
//
// 两种装配：独立事务（Store 持有）或事务内（Tx.Tasks() 取得）。判据是
// 同一套函数（taskClaim / taskFinish / taskRenew）⇒ 两个入口不漂。
type TaskStore struct {
	exec exec
}

// UseClock 注入存储时钟（boardtest.ClockedBoard）。
func (t *TaskStore) UseClock(now func() time.Time) {
	if t.exec.db != nil {
		t.exec.db.setClock(now)
	}
}

// Seed 摆初始任务（事务内形态返回错误；装载入口由 Store.Seed 兜 panic）。
func (t *TaskStore) Seed(tasks ...taskboard.Task) error {
	return t.exec.run(context.Background(), func(tx *txCtx) error {
		for _, task := range tasks {
			if task.ID == "" {
				continue
			}
			// 同 id 重摆覆盖内容并清租约；接管代次水位保留（不清）。
			var prevSeq, prevFence uint64
			if prev, ok := tx.st.tasks[task.ID]; ok {
				prevSeq = prev.Task.Seq
				prevFence = prev.Fence
			}
			fresh := taskboard.Task{
				ID:    task.ID,
				Title: task.Title,
				Deps:  append([]taskboard.TaskID(nil), task.Deps...),
				State: task.State,
			}
			fresh.Seq = prevSeq + 1
			rec := journalRecord{Kind: kindTask, Task: &fresh, Fence: prevFence}
			if err := tx.stage(rec); err != nil {
				return err
			}
		}
		return nil
	})
}

// List 返回一致快照（一次重放；按 id 升序）。
func (t *TaskStore) List(ctx context.Context) ([]taskboard.Task, error) {
	var out []taskboard.Task
	err := t.exec.run(ctx, func(tx *txCtx) error {
		out = taskList(tx)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Claim 认领任务（读-判-写在同一个写事务里；令牌与代次由存储铸造）。
//
// 「取任务 + 判状态可认领」这两步留在**方法体内**：它是本实现的唯一认领
// 入口（独立事务与事务内两个装配都走这里），判据因此只有一个主人。依赖与
// 活租约的判定、铸令牌与写入交给 taskClaim（同一事务内）。
func (t *TaskStore) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	now := t.exec.now()
	var out taskboard.Task
	err := t.exec.run(ctx, func(tx *txCtx) error {
		rec, ok := tx.st.tasks[id]
		if !ok {
			return oerrors.Wrap(taskboard.ErrNotFound, "sqlitestore: 任务不存在", "task", string(id))
		}
		// 状态可认领判定统一走 taskboard.State.Claimable（issue #360）：
		// 词表外 → ErrUnknownState，终态 → ErrTerminal。
		if cerr := rec.Task.State.Claimable(); cerr != nil {
			return oerrors.Wrap(cerr, "sqlitestore: 任务状态不可认领",
				"task", string(id), "state", string(rec.Task.State))
		}
		var err error
		out, err = taskClaim(tx, rec, id, agent, req, now)
		return err
	})
	if err != nil {
		return taskboard.Task{}, err
	}
	return out, nil
}

// Release 归还认领（凭令牌；非持有者一律被拒）。
func (t *TaskStore) Release(ctx context.Context, id taskboard.TaskID, token string) error {
	return t.exec.run(ctx, func(tx *txCtx) error {
		return taskFinish(tx, id, token, taskboard.StateOpen)
	})
}

// Complete 标记完成（凭令牌；终态不带 owner/租约）。
func (t *TaskStore) Complete(ctx context.Context, id taskboard.TaskID, token string) error {
	return t.exec.run(ctx, func(tx *txCtx) error {
		return taskFinish(tx, id, token, taskboard.StateDone)
	})
}

// Renew 续期并确认持有资格（令牌仍是最新代且租约未过期）。
func (t *TaskStore) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	now := t.exec.now()
	var out taskboard.Task
	err := t.exec.run(ctx, func(tx *txCtx) error {
		var err error
		out, err = taskRenew(tx, id, token, req, now)
		return err
	})
	if err != nil {
		return taskboard.Task{}, err
	}
	return out, nil
}

// taskList 任务快照（按 id 升序）。
func taskList(tx *txCtx) []taskboard.Task {
	out := make([]taskboard.Task, 0, len(tx.st.tasks))
	for _, rec := range tx.st.tasks {
		out = append(out, cloneTask(rec.Task))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// taskClaim 认领任务的其余判定与写入（依赖 / 活租约 / 铸令牌 / 落盘）。
//
// 前置条件：调用方已取到 rec 并判过「状态可认领」（TaskStore.Claim 的方法
// 体里做的——那是唯一入口，判据只有一个主人）。
func taskClaim(tx *txCtx, rec *taskEntry, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest, now time.Time) (taskboard.Task, error) {
	for _, dep := range rec.Task.Deps {
		depRec, ok := tx.st.tasks[dep]
		if !ok || depRec.Task.State != taskboard.StateDone {
			return taskboard.Task{}, oerrors.Wrap(taskboard.ErrBlocked, "sqlitestore: 依赖未完成",
				"task", string(id), "dep", string(dep))
		}
	}
	if taskboard.LeaseHeld(rec.Task, now) {
		return taskboard.Task{}, oerrors.Wrap(taskboard.ErrTaken, "sqlitestore: 任务已被他人认领", "task", string(id))
	}
	token, err := mintToken()
	if err != nil {
		return taskboard.Task{}, err
	}
	next := cloneTask(rec.Task)
	nextFence := rec.Fence + 1
	next.State = taskboard.StateClaimed
	next.Owner = agent
	next.Lease = taskboard.Lease{
		Token: token,
		Fence: taskboard.Fence(nextFence),
	}
	if req.TTL > 0 {
		next.Lease.Until = now.Add(req.TTL)
	}
	next.Seq = rec.Task.Seq + 1
	if err := tx.stage(journalRecord{Kind: kindTask, Task: &next, Fence: nextFence}); err != nil {
		return taskboard.Task{}, err
	}
	return cloneTask(next), nil
}

// taskFinish 释放/完成共用的条件写：只认「当前认领态 + 令牌一致」。
func taskFinish(tx *txCtx, id taskboard.TaskID, token string, state taskboard.State) error {
	rec, ok := tx.st.tasks[id]
	if !ok {
		return oerrors.Wrap(taskboard.ErrNotFound, "sqlitestore: 任务不存在", "task", string(id))
	}
	if rec.Task.State != taskboard.StateClaimed || rec.Task.Lease.Token != token {
		return oerrors.Wrap(taskboard.ErrLeaseLost, "sqlitestore: 租约令牌不匹配",
			"task", string(id), "state", string(rec.Task.State))
	}
	next := cloneTask(rec.Task)
	next.State = state
	next.Owner = ""
	next.Lease = taskboard.Lease{} // 代次水位在 rec.Fence 里，不清
	next.Seq = rec.Task.Seq + 1
	return tx.stage(journalRecord{Kind: kindTask, Task: &next, Fence: rec.Fence})
}

// taskRenew 续期并确认持有资格（令牌仍是最新代且租约未过期）。
func taskRenew(tx *txCtx, id taskboard.TaskID, token string, req taskboard.ClaimRequest, now time.Time) (taskboard.Task, error) {
	rec, ok := tx.st.tasks[id]
	if !ok {
		return taskboard.Task{}, oerrors.Wrap(taskboard.ErrNotFound, "sqlitestore: 任务不存在", "task", string(id))
	}
	if rec.Task.State != taskboard.StateClaimed || rec.Task.Lease.Token != token {
		return taskboard.Task{}, oerrors.Wrap(taskboard.ErrLeaseLost, "sqlitestore: 租约令牌不匹配",
			"task", string(id), "state", string(rec.Task.State))
	}
	if !taskboard.LeaseHeld(rec.Task, now) {
		return taskboard.Task{}, oerrors.Wrap(taskboard.ErrLeaseLost,
			"sqlitestore: 租约已过期，资格终止（请重新认领）", "task", string(id))
	}
	next := cloneTask(rec.Task)
	if req.TTL > 0 {
		next.Lease.Until = now.Add(req.TTL)
	} else {
		next.Lease.Until = time.Time{}
	}
	next.Seq = rec.Task.Seq + 1
	if err := tx.stage(journalRecord{Kind: kindTask, Task: &next, Fence: rec.Fence}); err != nil {
		return taskboard.Task{}, err
	}
	return cloneTask(next), nil
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
	_ taskboard.Board         = (*TaskStore)(nil)
	_ taskboard.RenewingBoard = (*TaskStore)(nil)
)

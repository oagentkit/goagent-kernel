// journal.go —— 事务日志的读写与崩溃恢复（文件后端的「WAL 帧」）。
//
// # 一行 = 一个事务批次（v2 格式）
//
// v2 把「一次事务里的全部记录」写成**一行**：跨面的变更（任务状态 + 副作用
// 闸 + 进度快照 + 事件行）在磁盘上也是**同一条记录**——要么整批可见，要么
// 整批不可见。这正是「同一把锁、同一个日志、一次提交」的机器形态：跨面
// 不会出现「一半已提交」。
//
// v1 格式（一行 = 一条任务记录）**仍可读**：旧目录不必迁移，重放时按行形状
// 与版本号判别（v1 行没有 records 字段）。旧记录读进来后与新记录走同一条
// 应用路径（applyRecord）⇒ 只有一份「记录怎么变成状态」的算法。
//
// 残尾规则不变：只有**以换行结尾**的行才算已提交——崩溃留下的半截行在打开
// / 下一次写入前被截掉（与 SQLite WAL 的「完整帧才重放」同一条口径）。
package sqlitestore

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

const (
	// journalVersion 当前日志格式版本（v2：一行 = 一个事务批次）。
	journalVersion = 2
	// legacyJournalVersion v1 格式版本（一行 = 一条任务记录），仍可读。
	legacyJournalVersion = 1
)

// 记录类型标签（一个批次里可以混装多个面的记录）。
const (
	kindTask     = "task"
	kindEffect   = "effect"
	kindSnapshot = "snapshot"
	kindEvent    = "event"
)

// journalRecord 一条记录（批次内某个面的一次变更）。
type journalRecord struct {
	Kind string `json:"k"`
	// 任务面：整条任务 + 独立的接管代次水位。
	Task  *taskboard.Task `json:"task,omitempty"`
	Fence uint64          `json:"fence,omitempty"`
	// 副作用面：Key 级与资源级（两级可同时涉及）。
	Effect *effectRecord `json:"effect,omitempty"`
	// 进度快照面。
	Snapshot *snapshotRecord `json:"snapshot,omitempty"`
	// 事件面：游标由存储铸造。
	Event *eventRecord `json:"event,omitempty"`
}

// effectRecord 副作用面的一次变更。
//
// Act 与 Res 都是**新值**（不是增量）：Key 级与资源级在同一条记录里一起
// 变 ⇒ 「一次原子操作里同时决定」在落库形态上成立。
type effectRecord struct {
	// Tenant 租户（身份第二维；空串 = 未声明的单租户命名空间）。
	Tenant string `json:"tenant,omitempty"`
	// Key 幂等键。
	Key string    `json:"key"`
	Act *actEntry `json:"act,omitempty"`
	Res *resEntry `json:"res,omitempty"`
	// ResKey 非空 ⇒ 资源闸这条记录涉及（Res 是它的新值）。
	ResKey string `json:"res_key,omitempty"`
}

// snapshotRecord 进度快照的一次落库。
type snapshotRecord struct {
	RunID    string              `json:"run_id"`
	Snapshot checkpoint.Snapshot `json:"snapshot"`
}

// eventRecord 事件行的一次落库。
type eventRecord struct {
	ID    uint64         `json:"id"`
	Event eventbus.Event `json:"event"`
}

// journalBatch 一个事务批次（v2 的一行）。
type journalBatch struct {
	Version int             `json:"v"`
	Records []journalRecord `json:"records"`
}

// loadState 重放日志（调用方必须持有文件锁）。
//
// 返回重放后的已提交状态。无法解析的**末行**视为崩溃残尾丢弃；中间行损坏
// 说明日志被外部改写，必须响亮失败——静默跳过会让状态凭空回退。
func loadState(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newState(), nil
		}
		return nil, oerrors.Wrap(err, "sqlitestore: 读日志失败", "path", path)
	}
	st := newState()
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe struct {
			Version int             `json:"v"`
			Records json.RawMessage `json:"records"`
			Task    *taskboard.Task `json:"task"`
			Fence   uint64          `json:"fence"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			if i == len(lines)-1 {
				// 末行没有换行 ⇒ 上次写入在 fsync 前被打断：这是 WAL 的
				// 「不完整帧」，丢弃即可（repairTornTailLocked 随后会截掉它）。
				break
			}
			return nil, oerrors.Wrap(err, "sqlitestore: 日志中间行损坏（拒绝静默跳过）",
				"path", path, "line", i+1)
		}
		switch probe.Version {
		case journalVersion:
			if probe.Records == nil {
				return nil, oerrors.Wrap(oerrors.New("sqlitestore: 日志批次缺记录数组"),
					"sqlitestore: v2 行必须是事务批次", "path", path, "line", i+1)
			}
			var batch journalBatch
			if err := json.Unmarshal(line, &batch); err != nil {
				return nil, oerrors.Wrap(err, "sqlitestore: 解析事务批次失败",
					"path", path, "line", i+1)
			}
			for _, rec := range batch.Records {
				if err := applyRecord(st, rec); err != nil {
					return nil, oerrors.Wrap(err, "sqlitestore: 应用日志记录失败",
						"path", path, "line", i+1)
				}
			}
		case legacyJournalVersion:
			// v1：一行 = 一条任务记录。读进来后与新记录走同一条应用路径。
			if probe.Task == nil {
				return nil, oerrors.Wrap(oerrors.New("sqlitestore: 日志行缺任务记录"),
					"sqlitestore: v1 行必须含任务记录", "path", path, "line", i+1)
			}
			rec := journalRecord{Kind: kindTask, Task: probe.Task, Fence: probe.Fence}
			if err := applyRecord(st, rec); err != nil {
				return nil, oerrors.Wrap(err, "sqlitestore: 应用日志记录失败",
					"path", path, "line", i+1)
			}
		default:
			return nil, oerrors.Wrap(oerrors.New("sqlitestore: 日志格式版本不支持"),
				"sqlitestore: 日志由另一个格式版本写入",
				"path", path, "line", i+1, "version", probe.Version)
		}
	}
	return st, nil
}

// repairTornTailLocked 截掉最后一个完整行之后的残尾（调用方必须持有文件锁）。
//
// 只在「文件不以换行结尾」时动手：以换行结尾但末行 JSON 损坏属于真损坏，
// 由 loadState 报错，而不是被悄悄截掉。
func repairTornTailLocked(f *os.File, path string) error {
	info, err := f.Stat()
	if err != nil {
		return oerrors.Wrap(err, "sqlitestore: 读日志状态失败", "path", path)
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return oerrors.Wrap(err, "sqlitestore: 读日志末字节失败", "path", path)
	}
	if last[0] == '\n' {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return oerrors.Wrap(err, "sqlitestore: 读日志失败", "path", path)
	}
	cut := bytes.LastIndexByte(data, '\n') + 1
	if err := f.Truncate(int64(cut)); err != nil {
		return oerrors.Wrap(err, "sqlitestore: 截断残尾失败", "path", path)
	}
	return nil
}

// appendBatchLocked 追加一个事务批次并 fsync（调用方必须持有文件锁）。
//
// 一批 = 一行 ⇒ 跨面原子性在磁盘上成立：要么这一行完整可解析（全部面都
// 生效），要么这行不存在（全部面都没变）。
func appendBatchLocked(f *os.File, path string, recs []journalRecord) error {
	if err := repairTornTailLocked(f, path); err != nil {
		return err
	}
	// 显式定位到末尾（文件不是 O_APPEND 打开的：Windows 上 O_APPEND 句柄
	// 不能 Truncate）。写者已被 flock 串行化，这里不会与别的写者交错。
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return oerrors.Wrap(err, "sqlitestore: 定位日志末尾失败", "path", path)
	}
	batch := journalBatch{Version: journalVersion, Records: recs}
	line, err := json.Marshal(batch)
	if err != nil {
		return oerrors.Wrap(err, "sqlitestore: 事务批次序列化失败", "记录数", len(recs))
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return oerrors.Wrap(err, "sqlitestore: 追加日志失败", "path", path)
	}
	if err := f.Sync(); err != nil {
		return oerrors.Wrap(err, "sqlitestore: 日志 fsync 失败", "path", path)
	}
	return nil
}

// cloneTask 深拷贝任务（依赖切片不共享底层数组）。
func cloneTask(t taskboard.Task) taskboard.Task {
	out := t
	out.Deps = append([]taskboard.TaskID(nil), t.Deps...)
	return out
}

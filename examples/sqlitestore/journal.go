// journal.go —— JSONL 追加日志的读写与崩溃恢复（文件后端的「WAL 帧」）。
//
// 一条记录 = 一行完整 JSON + '\n'。追加后 fsync；重放时只有**以换行结尾**
// 的行才算已提交——崩溃留下的半截行在打开/下一次写入前被截掉。这与 SQLite
// WAL 的「完整帧才重放」是同一条口径（详见 doc.go 的映射表）。
package sqlitestore

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// journalVersion 记录格式版本（将来改格式时用它拒绝旧解析器静默误读）。
const journalVersion = 1

// journalRecord 一条任务记录。
//
// Fence 是**独立水位**而不是从 Task.Lease.Fence 读：Release / Complete 会把
// 租约整列清空（taskboard.Check 要求非认领态不得残留租约），但接管代次必须
// 继续递增——否则任务释放后重新认领会从 1 重来，旧持有者的迟到写入又能通过
// 「代次更大」的检查。pgstore 把 fence 放在独立列，本文件后端对齐同一形态。
type journalRecord struct {
	Version int            `json:"v"`
	Task    taskboard.Task `json:"task"`
	Fence   uint64         `json:"fence"`
}

// loadJournalLocked 重放日志（调用方必须持有文件锁）。
//
// 返回值按任务 id 索引，后写覆盖先写（每次变更都追加整条记录，最后一条
// 即当前状态）。无法解析的**末行**视为崩溃残尾丢弃；中间行损坏说明日志
// 被外部改写，必须响亮失败——静默跳过会让状态凭空回退。
func loadJournalLocked(path string) (map[taskboard.TaskID]journalRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[taskboard.TaskID]journalRecord{}, nil
		}
		return nil, oerrors.Wrap(err, "sqlitestore: 读日志失败", "path", path)
	}
	out := make(map[taskboard.TaskID]journalRecord)
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			if i == len(lines)-1 {
				// 末行没有换行 ⇒ 上次写入在 fsync 前被打断：这是 WAL 的
				// 「不完整帧」，丢弃即可（repairTornTail 随后会截掉它）。
				break
			}
			return nil, oerrors.Wrap(err, "sqlitestore: 日志中间行损坏（拒绝静默跳过）",
				"path", path, "line", i+1)
		}
		if rec.Version != journalVersion {
			return nil, oerrors.Wrap(oerrors.New("sqlitestore: 日志格式版本不支持"),
				"sqlitestore: 日志由另一个格式版本写入", "path", path, "line", i+1, "version", rec.Version)
		}
		if rec.Task.ID == "" {
			return nil, oerrors.Wrap(oerrors.New("sqlitestore: 日志记录缺任务 id"),
				"sqlitestore: 记录无法定位到任务", "path", path, "line", i+1)
		}
		out[rec.Task.ID] = rec
	}
	return out, nil
}

// repairTornTailLocked 截掉最后一个完整行之后的残尾（调用方必须持有文件锁）。
//
// 只在「文件不以换行结尾」时动手：以换行结尾但末行 JSON 损坏属于真损坏，
// 由 loadJournalLocked 报错，而不是被悄悄截掉。
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

// appendLocked 追加一条完整记录并 fsync（调用方必须持有文件锁）。
func appendLocked(f *os.File, path string, rec journalRecord) error {
	if err := repairTornTailLocked(f, path); err != nil {
		return err
	}
	// 显式定位到末尾（文件不是 O_APPEND 打开的：Windows 上 O_APPEND 句柄
	// 不能 Truncate）。写者已被 flock 串行化，这里不会与别的写者交错。
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return oerrors.Wrap(err, "sqlitestore: 定位日志末尾失败", "path", path)
	}
	rec.Version = journalVersion
	line, err := json.Marshal(rec)
	if err != nil {
		return oerrors.Wrap(err, "sqlitestore: 记录序列化失败", "task", string(rec.Task.ID))
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

// held 该记录是否仍被活持有（判据单源：taskboard.LeaseHeld）。
func held(rec journalRecord, now time.Time) bool {
	return taskboard.LeaseHeld(rec.Task, now)
}

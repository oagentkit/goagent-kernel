// lock_windows.go —— 跨进程写锁（独占创建锁文件）。
//
// Windows 没有与 flock 等价的、随句柄关闭自动释放的 advisory lock
// （CloseHandle 会释放、但标准库不暴露 LockFileEx）；这里用 O_EXCL 独占
// 创建锁文件实现同一语义：
//
//   - 拿不到 ⇒ 另一个写者持有，轮询到 lockTimeout；
//   - 进程崩溃留下的锁文件在超过 staleLock 后视为陈旧并接管（与 flock
//     的自动释放对齐——否则一次崩溃就把存储永久锁死）。
//
// 这是**示例性**实现：生产宿主应直接用 SQLite（它自带跨进程写锁与
// busy_timeout），不必抄这个 Windows 兜底。
//
//go:build windows

package sqlitestore

import (
	"os"
	"time"

	oerrors "github.com/oekit/pkg/errors"
)

// staleLock 陈旧锁文件的接管阈值。必须显著大于 lockTimeout（一次写事务
// 的时长）：短了会把正在写的进程的锁抢走，两个写者同时写。
const staleLock = 4 * lockTimeout

// acquireLock 取得写锁（独占创建锁文件；陈旧锁可接管）。
func acquireLock(path string) (func(), error) {
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, oerrors.Wrap(err, "sqlitestore: 创建锁文件失败", "path", path)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLock {
			// 崩溃遗留：接管（移除后立即重试，不算一次等待失败）。
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, oerrors.Wrap(oerrors.New("sqlitestore: 等待写锁超时（不丢写）"), "sqlitestore: 另一个写者持有写锁", "path", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

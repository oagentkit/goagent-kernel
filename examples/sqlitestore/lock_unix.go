// lock_unix.go —— 跨进程写锁（flock）。
//
// 与 SQLite 的写锁同一条语义：同一时刻只有一个写事务。flock 在进程崩溃时
// 由内核释放（不会留下永久锁文件），这正是「单机可恢复」需要的行为。
//
//go:build unix

package sqlitestore

import (
	"os"
	"syscall"
	"time"

	oerrors "github.com/oekit/pkg/errors"
)

// acquireLock 取得文件的独占锁（非阻塞轮询到 lockTimeout）。
func acquireLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, oerrors.Wrap(err, "sqlitestore: 打开锁文件失败", "path", path)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = f.Close()
			return nil, oerrors.Wrap(err, "sqlitestore: 取得写锁失败", "path", path)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, oerrors.Wrap(oerrors.New("sqlitestore: 等待写锁超时（不丢写）"), "sqlitestore: 另一个写者持有写锁", "path", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

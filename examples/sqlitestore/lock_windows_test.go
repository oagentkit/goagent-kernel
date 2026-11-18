// lock_windows_test.go —— Windows 侧的 errno 绑定判据。
//
//go:build windows

package sqlitestore

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// 判据在 Windows 上必须绑的是 Windows 自己的错误码：绑成别的 errno ⇒
// 「被占用」永远判不出来 ⇒ 正常并发又被误判成故障（这正是本轮修的缺陷）。
func TestWindowsBusyErrnosBindRealWindowsCodes(t *testing.T) {
	if windowsBusyErrnos.accessDenied != syscall.ERROR_ACCESS_DENIED {
		t.Fatalf("accessDenied 绑错：得到 %v，期望 %v", windowsBusyErrnos.accessDenied, syscall.ERROR_ACCESS_DENIED)
	}
	if windowsBusyErrnos.sharingViolation != errnoSharingViolation {
		t.Fatalf("sharingViolation 绑错：得到 %v，期望 %v", windowsBusyErrnos.sharingViolation, errnoSharingViolation)
	}
	// 再确认真实的 Windows 错误码能被判据认出来（不是只比对了常量）。
	err := &os.PathError{Op: "open", Path: "tasks.lock", Err: syscall.ERROR_ACCESS_DENIED}
	if !classifyLockBusy(err, windowsBusyErrnos) {
		t.Fatal("Windows 的 ACCESS_DENIED 应判为被占用")
	}
}

// Windows 的锁协议是「锁文件存在即被占用」⇒ 释放必须把文件删掉：残留 ⇒
// 下一个写者要等陈旧阈值（staleLock）才能接管，一次释放失败把存储卡住。
func TestLockFileRemovedAfterTransaction(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, boardtest.NewFakeClock())
	s.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	if _, err := os.Stat(filepath.Join(dir, lockName)); !os.IsNotExist(err) {
		t.Fatalf("事务结束后锁文件应已删除，得到 %v", err)
	}
}

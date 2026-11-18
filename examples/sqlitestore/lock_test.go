package sqlitestore

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// Windows 的两个「被占用」错误码（这里只作**注入值**用：判定逻辑与平台
// 无关，平台文件绑的是 syscall 自己的常量；这组数值让平台无关的用例能在
// 任何机器上把同一组判据跑一遍）。
const (
	winErrAccessDenied     syscall.Errno = 5
	winErrSharingViolation syscall.Errno = 32
)

func TestClassifyLockBusy(t *testing.T) {
	busy := busyErrnos{accessDenied: winErrAccessDenied, sharingViolation: winErrSharingViolation}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"锁文件已存在（独占创建最常见的失败）", os.ErrExist, true},
		{"持有者还开着句柄", &os.PathError{Op: "open", Path: "tasks.lock", Err: winErrSharingViolation}, true},
		{"上一位持有者正在释放（delete-pending 窗口）", &os.PathError{Op: "open", Path: "tasks.lock", Err: winErrAccessDenied}, true},
		{"真故障：路径不存在", &os.PathError{Op: "open", Path: "tasks.lock", Err: syscall.ENOENT}, false},
		{"真故障：与占用无关的错误", oerrors.New("boom"), false},
		{"nil 不是被占用", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLockBusy(tc.err, busy); got != tc.want {
				t.Fatalf("classifyLockBusy(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// 平台没登记的 errno 不参与判定：否则会把无关错误误判成「被占用」，
// 等待路径就会一直空转到超时（比直接报错更坏）。
func TestClassifyLockBusyIgnoresUnregisteredErrnos(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "tasks.lock", Err: winErrAccessDenied}
	if classifyLockBusy(err, busyErrnos{}) {
		t.Fatal("未登记的 errno 不应判为被占用")
	}
}

// 事务结束后锁必须能被下一个写者**立刻**拿到：拿不到说明上一次没释放
// 干净（unix 是 flock 没解锁、Windows 是锁文件没删掉）⇒ 后续写者空转到
// 超时，一次释放失败把整台存储卡住。
func TestLockReusableAfterTransaction(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, boardtest.NewFakeClock())
	s.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	s.Seed(taskboard.Task{ID: "t2", State: taskboard.StateOpen})
}

// 释放锁的重试：删得掉 ⇒ 返回 true；文件本来就不在 ⇒ 同样算已释放（幂等，
// 否则重复释放会把它读成失败）。
func TestRemoveWithRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !removeWithRetry(path, time.Second) {
		t.Fatal("应删得掉")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("文件应已删除，得到 %v", err)
	}
	if !removeWithRetry(path, time.Second) {
		t.Fatal("文件不存在时应视为已释放")
	}
}

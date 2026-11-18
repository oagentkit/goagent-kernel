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
	"syscall"
	"time"

	oerrors "github.com/oekit/pkg/errors"
)

// staleLock 陈旧锁文件的接管阈值。必须显著大于 lockTimeout（一次写事务
// 的时长）：短了会把正在写的进程的锁抢走，两个写者同时写。
const staleLock = 4 * lockTimeout

// releaseRetryBudget 释放锁时的退避重试预算。
//
// 删除锁文件同样会撞上 Windows 的瞬时占用（扫描程序开着文件 ⇒ DeleteFile
// 被拒）：删不掉 ⇒ 锁文件残留 ⇒ 后续写者要等 staleLock（20s）才能接管，
// 一次瞬时故障把存储卡住 20s ⇒ 这里退避重试，而不是一次失败就放弃
// （预算用尽仍删不掉，残留由陈旧锁接管路径兜底）。
const releaseRetryBudget = time.Second

// errnoSharingViolation 是 Windows 的 ERROR_SHARING_VIOLATION（32）。
//
// Go 的 syscall 只导出少数 Windows 错误码（ERROR_ACCESS_DENIED 在内，
// ERROR_SHARING_VIOLATION 不在）⇒ 按 Windows SDK 的错误码显式声明，
// 并由 Windows 侧用例断言它真的能被判据认出来（绑错 errno ⇒ 判据永远不
// 命中 ⇒ 正常并发又被误判成故障）。
const errnoSharingViolation = syscall.Errno(32)

// windowsBusyErrnos 是 Windows 上「锁正被占用」的 errno 语义。
//
// 「被占用」在 Windows 上有三种表达，只认一种就会把正常并发误判成故障
// （v0.46.0 的 Windows CI 就是这样红的：20 路并发认领里有 1 路独占创建拿到
// ERROR_ACCESS_DENIED，整条用例判「非争用类错误」失败）：
//
//   - ERROR_FILE_EXISTS：锁文件在（最常见，os.IsExist 认它）；
//   - ERROR_SHARING_VIOLATION：持有者还开着句柄；
//   - ERROR_ACCESS_DENIED：上一位持有者正在释放（NTFS 的 delete-pending
//     窗口）或文件被扫描程序短暂占用——文件仍在目录里，语义上仍是「被占用」。
var windowsBusyErrnos = busyErrnos{
	accessDenied:     syscall.ERROR_ACCESS_DENIED,
	sharingViolation: errnoSharingViolation,
}

// acquireLock 取得写锁（独占创建锁文件；陈旧锁可接管）。
func acquireLock(path string) (func(), error) {
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { releaseLock(path) }, nil
		}
		if !classifyLockBusy(err, windowsBusyErrnos) {
			return nil, oerrors.Wrap(err, "sqlitestore: 创建锁文件失败", "path", path)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLock {
			// 崩溃遗留：接管（移除后立即重试，不算一次等待失败）。
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			// last_error 必须带上：超时的真实原因可能是「别人拿着锁」，也可能
			// 是独占创建一直被拒（权限/路径问题）——只报「别人持有锁」是错误
			// 指路，排查的人会被引到错的方向。
			return nil, oerrors.Wrap(oerrors.New("sqlitestore: 等待写锁超时（不丢写）"),
				"sqlitestore: 另一个写者持有写锁", "path", path, "last_error", err.Error())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// releaseLock 释放锁（删除锁文件；撞上瞬时占用则退避重试）。
//
// 预算用尽仍删不掉 ⇒ 锁文件残留，由 staleLock 的接管路径兜底（下一个写者
// 按陈旧锁接管）。此处不抛错：释放发生在事务提交之后，抛错会让「已提交」
// 被读成「失败」——那比残留锁更难查。
func releaseLock(path string) {
	_ = removeWithRetry(path, releaseRetryBudget)
}

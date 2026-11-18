// lock.go —— 跨进程写锁的平台无关部分（判定结构 + 释放重试）。
//
// 平台差异只有 errno 的取值与「怎么拿锁」；判定结构本身与平台无关 ⇒ 放在
// 无平台约束的文件里：**判据能在任何机器上被真跑**（Windows 专属的 errno
// 只在 Windows 上出现，但判据不必等到 Windows CI 才验）。
//
// 为什么要有这个文件：Windows 上「独占创建锁文件」失败有三种「被占用」的
// 表达（见 lock_windows.go），只认一种就会把正常并发误判成故障——错指路比
// 不指路更坏（v0.46.0 的 Windows CI 就是这样红的：20 路并发认领里有 1 路
// 独占创建拿到 ERROR_ACCESS_DENIED，整条用例判「非争用类错误」失败）。
package sqlitestore

import (
	"os"
	"syscall"
	"time"

	oerrors "github.com/oekit/pkg/errors"
)

// busyErrnos 描述某个平台上「锁正被别的写者占用」的 errno 语义。
//
// 零值表示该平台不认这个 errno（判定时跳过）⇒ 平台文件只声明自己有的，
// 不会把别的平台的错误码混进来。
type busyErrnos struct {
	// accessDenied：Windows 在「上一位持有者正在释放锁文件」（NTFS 的
	// delete-pending 窗口）或「文件被扫描程序短暂占用」时，用
	// ERROR_ACCESS_DENIED 而不是 ERROR_FILE_EXISTS 表达「文件在」。
	accessDenied syscall.Errno
	// sharingViolation：持有者还开着句柄时，独占创建被系统拒绝。
	sharingViolation syscall.Errno
}

// classifyLockBusy 判断 err 是否表示「锁正被别的写者占用」而不是真故障。
//
// 判据只问「是不是占用」这一件事，不做语义推断：占用与否由**平台 errno**
// 表达，判定结构由本文件统一 ⇒ 各平台不会各自漂移。
func classifyLockBusy(err error, busy busyErrnos) bool {
	if err == nil {
		return false
	}
	if os.IsExist(err) {
		return true
	}
	if busy.sharingViolation != 0 && oerrors.Is(err, busy.sharingViolation) {
		return true
	}
	if busy.accessDenied != 0 && oerrors.Is(err, busy.accessDenied) {
		return true
	}
	return false
}

// removeWithRetry 删除文件，撞上瞬时占用时退避重试到 budget。
//
// Windows 上删除也会被瞬时占用打断（扫描程序开着文件 ⇒ DeleteFile 被拒）：
// 删不掉 ⇒ 锁文件残留 ⇒ 后续写者要等陈旧阈值（staleLock）才能接管，一次
// 瞬时故障把存储卡住 20s ⇒ 这里退避重试，而不是删不掉就吞掉。
//
// 返回是否已删除：false = 预算用尽仍删不掉（残留由陈旧锁接管路径兜底）。
func removeWithRetry(path string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

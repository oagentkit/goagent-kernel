// lease.go —— 在途租约资格的**可观测面**（issue #179）：三条失去资格的
// 路径各给一个可判别的信号，宿主据此计数/留痕/告警。从 loop.go 拆出
// （结构阈值按判据的一侧切）：驱动循环与它的租约事件词表分开，两边各自
// 可读。
//
// # 为什么这三条不许静默
//
// 「闸已易主、本次结果作废」是事实不是成功路径。静默吞掉它们，下游的
// 非幂等副作用就可能双份——租约易主 ⇒ 旧执行体本该被封口，而它是否
// 真被封口、收场有没有被拒，宿主必须有信号可看（@@Config.OnLeaseLost@@）。
package worker

import (
	"github.com/oekit/concurrency/async"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// LeaseLostCause 在途租约失去资格的原因（@@Config.OnLeaseLost@@ 的判别面）。
type LeaseLostCause int

const (
	// LeaseLostTakenOver 续期报 @@taskboard.ErrLeaseLost@@：租约已易主
	//（新持有者在位），在途执行的 ctx 随之取消。
	LeaseLostTakenOver LeaseLostCause = iota
	// LeaseLostUnconfirmed 连续续期失败跨过阈值：无法确认持有资格——
	// 取消在途 ctx 并停止领取（施加之前封口；本次连「谁在持」都答不出）。
	LeaseLostUnconfirmed
	// LeaseLostOnFinalize 收场动作（Complete/Release）被
	// @@taskboard.ErrLeaseLost@@ 拒：任务已归新持有者，本次结果作废。
	LeaseLostOnFinalize
)

func (c LeaseLostCause) String() string {
	switch c {
	case LeaseLostTakenOver:
		return "taken_over"
	case LeaseLostUnconfirmed:
		return "unconfirmed"
	case LeaseLostOnFinalize:
		return "on_finalize"
	default:
		return "unknown"
	}
}

// notifyLeaseLost 租约失去资格的可观测信号（nil 钩子静默跳过）。
//
// panic 隔离与 invokeHandler（execute.go，issue #196）同款：宿主留痕钩子
// 的异常不得掀翻心跳协程——一次 panic 逃出 heartbeat 就是全部在途租约
// 停止续期、他副本接管、非幂等副作用双份（issue #326）。上报走同一个
// async.ReportPanic 观测面，宿主注入的 panic handler 不会漏接。
func (l *Loop) notifyLeaseLost(id taskboard.TaskID, cause LeaseLostCause) {
	if l.cfg.OnLeaseLost == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			async.ReportPanic(r)
		}
	}()
	l.cfg.OnLeaseLost(id, cause)
}

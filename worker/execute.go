// execute.go —— 单个在途任务的执行与收场（串行路径与并发池共用）。
//
// 从 loop.go 拆出的理由与 lease.go 同款（结构阈值按判据的一侧切）：
// 领取/心跳是「怎么找到活」，本文件是「一条活怎么跑完、怎么落库」。
package worker

import (
	"context"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// invokeHandler 调用执行体并把 panic 收编成一次普通失败（issue #196）：
// panic 不杀进程、不逃过收场（失败 ⇒ Release 回可重做），同池其它任务
// 继续推进。上报走 async.ReportPanic——与 GoSafe 同一个观测面，宿主
// 注入的 panic handler 不会漏接。
func (l *Loop) invokeHandler(ctx context.Context, task taskboard.Task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			async.ReportPanic(r)
			err = errors.Wrap(errors.New("worker: 执行体 panic"),
				"执行体 panic 已隔离，本任务按失败收场", "task", string(task.ID))
		}
	}()
	return l.handler.Execute(ctx, task)
}

// executeOne 执行一个在途任务：登记给心跳协程 → 跑 handler → 收场。
//
// 收场动作走 WithoutCancel：外层停机时在途任务的 Complete/Release 仍要落地
// （任务板的状态不能因为停机而悬在 claimed）。收场被 ErrLeaseLost 拒
// = 闸已易主——任务归新持有者，如实接受（不重试、不吞），但经
// OnLeaseLost 留痕：本次结果作废是宿主必须知道的事实，不是成功路径
// （issue #179：静默吞掉它，下游非幂等副作用就可能双份）。
func (l *Loop) executeOne(ctx context.Context, set *inflight, task taskboard.Task) error {
	execCtx, cancel := context.WithCancel(ctx)
	c := &claimed{id: task.ID, token: task.Lease.Token, cancel: cancel}
	// 首次续期按「TTL − 安全余量」调度（issue #266）：拿到租约后不必每个
	// 心跳拍都续，提前 margin 续一次即可；之后每次成功续期再重排。
	c.scheduleRenew(l.renewAfter())
	set.add(c)
	// 兜底：handler 经 invokeHandler 已收编 panic，但收场路径上的意外
	// panic 也不得让心跳永远续一条已经没人执行的租约。
	defer func() {
		c.beginFinalize()
		set.remove(task.ID, c)
		cancel()
	}()

	execErr := l.invokeHandler(execCtx, task)

	// 先标记收场并摘出集合再收场：与旧实现的 `current = nil`（收场动作
	// 之前）同序。beginFinalize 与心跳续期互斥——Complete 成功之后不得
	// 再有本任务的 Renew 报出假 ErrLeaseLost。任务可能在同一窗口被重新
	// 认领，remove 只删自己的那条（见 inflight.remove）。
	c.beginFinalize()
	set.remove(task.ID, c)
	cancel()

	finalCtx := context.WithoutCancel(ctx)
	if execErr == nil {
		if err := l.board.Complete(finalCtx, task.ID, c.token); err != nil {
			if !errors.Is(err, taskboard.ErrLeaseLost) {
				return errors.Wrap(err, "worker: 完成任务失败", "task", string(task.ID))
			}
			l.notifyLeaseLost(task.ID, LeaseLostOnFinalize)
		}
	} else {
		if err := l.board.Release(finalCtx, task.ID, c.token); err != nil {
			if !errors.Is(err, taskboard.ErrLeaseLost) {
				return errors.Wrap(err, "worker: 释放任务失败", "task", string(task.ID))
			}
			l.notifyLeaseLost(task.ID, LeaseLostOnFinalize)
		}
	}
	return nil
}

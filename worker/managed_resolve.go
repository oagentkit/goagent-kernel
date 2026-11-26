// managed_resolve.go —— 受管执行器的对账与释放面。
//
// Resolve 是宿主对「结果未知」副作用做人工/自动裁决的入口（转交
// sideeffect.Runner.Reconcile）；release / withRelease 是任务板租约的
// 收场面——ErrLeaseLost 不算故障（租约已易主，不该动新持有者的闸），
// 释放失败与主错误链式合并，两个信息都不丢。
package worker

import (
	"context"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

func (e *ManagedExecutor) Resolve(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	if e == nil || e.effects == nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, errors.Wrap(ErrInvalidConfig, "worker: 受管执行器未装配")
	}
	return e.effects.Reconcile(ctx, act, res)
}

// release 归还任务板租约（收场走 WithoutCancel）。
//
// ErrLeaseLost 不是故障：租约已易主，没有可归还的东西（也不该放掉
// 新持有者的闸）。其余错误如实上抛。
func (e *ManagedExecutor) release(ctx context.Context, id taskboard.TaskID, token string) error {
	err := e.board.Release(context.WithoutCancel(ctx), id, token)
	if err == nil || errors.Is(err, taskboard.ErrLeaseLost) {
		return nil
	}
	return errors.Wrap(err, "worker: 受管执行器释放任务租约失败", "task", string(id))
}

// withRelease 把「释放失败」与主错误合成一条链（两者都要保留：调用方既要
// 判主因，也要知道租约没还回去）。
func (e *ManagedExecutor) withRelease(ctx context.Context, id taskboard.TaskID, token string, primary error) error {
	rerr := e.release(ctx, id, token)
	if primary == nil {
		return rerr
	}
	if rerr == nil {
		return primary
	}
	return errors.Append(primary, rerr)
}

// 编译期断言：*sideeffect.Runner 满足执行器依赖的编排面。
var _ EffectRunner = (*sideeffect.Runner)(nil)

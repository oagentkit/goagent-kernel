// pool.go —— 有界并发池（issue #196，Config.Concurrency > 1 的路径）。
//
// # 形态：领取环单点、执行体有界并行
//
// 领取仍然只有一处（claimNext → Claim）：并发认领会互相竞争同一个候选，
// 把「谁先领」变成随机重试；单点领取 + 并行执行拿到同样的吞吐，且
// Pick 钩子不需要是并发安全的（既有契约不变）。
//
// slots 是并发上限：领取前先占槽，认领到任务后把执行投给 async.GoSafe；
// 没有候选就立刻还槽并按 PollInterval 再试。因此慢任务只占执行槽，
// **不占领取环**。
//
// # 排水（Drain）
//
// 停机 = 停止领取 + 等全部在途收场：
//
//   - 外层 ctx 取消 ⇒ 主循环退出，执行体经派生 ctx 收到取消信号；
//     收场动作（Complete/Release）在 executeOne 里走 WithoutCancel，
//     落库不受停机影响；
//   - 收场何时完成由 handler 对 ctx 的响应决定：handler 无视取消等于
//     无限期排水——这是既有 Loop 契约，不是并发池新增的语义。
//
// # 失败处置
//
//   - 执行体 panic → invokeHandler 收编为一次失败（Release），只影响该任务；
//   - 收场（Complete/Release）返回基础设施错误 → 与串行路径同义：停止领取、
//     取消其它在途（它们按失败收场）、排水后把第一个错误交给 Run；
//   - claimNext 的基础设施错误 → 同上（不再领取，等在途收场后返回）。
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/concurrency/async"
)

// runClaimsPool 领取-执行主循环的并发形态（claimCtx 取消即停止领取）。
//
// 返回 nil = 外层 ctx 取消且全部在途已收场；其余错误 = 基础设施故障
// （领取失败或收场失败），此时已取消其余在途并等它们收场。
func (l *Loop) runClaimsPool(ctx context.Context, set *inflight) error {
	slots := make(chan struct{}, l.cfg.Concurrency)
	errCh := make(chan error, l.cfg.Concurrency) // 每个在途执行至多一条错误

	// execCtx 是全部在途执行的父 ctx：外层取消会传递；基础设施故障时由
	// stopExec 主动取消其它在途（它们按失败收场，不把任务悬在 claimed）。
	execCtx, stopExec := context.WithCancel(ctx)
	defer stopExec()

	var (
		wg       sync.WaitGroup
		firstErr error
	)
	stop := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
		stopExec()
	}

	for ctx.Err() == nil {
		// 先占槽再认领：没有槽就不领取（上限是真上限，不是执行时才排队）。
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return l.drainPool(&wg, errCh, firstErr)
		case err := <-errCh:
			stop(err)
			return l.drainPool(&wg, errCh, firstErr)
		}

		task, ok, err := l.claimNext(ctx)
		if err != nil {
			<-slots
			stop(err)
			return l.drainPool(&wg, errCh, firstErr)
		}
		if !ok {
			<-slots
			// 轮询节拍：ctx 取消/执行失败立即醒来（不等满一个间隔）。
			select {
			case <-ctx.Done():
				return l.drainPool(&wg, errCh, firstErr)
			case err := <-errCh:
				stop(err)
				return l.drainPool(&wg, errCh, firstErr)
			case <-time.After(l.cfg.PollInterval):
			}
			continue
		}

		wg.Add(1)
		// 走 async.GoSafe 而非裸 go func（本仓门禁：panic 无法被外层
		// recover）。执行体自身的 panic 已在 invokeHandler 内收编；这里
		// 兜住执行/收场路径上的意外 panic，至少保证槽与 WaitGroup 归还。
		async.GoSafe(func() {
			defer wg.Done()
			defer func() { <-slots }()
			if e := l.executeOne(execCtx, set, task); e != nil {
				select {
				case errCh <- e:
				default: // 已有一条致命错误在路上：首错为足以收刀
				}
			}
		})

		// 投递后立刻看一眼执行侧是否已经报错（小池里错误通常很快出现）。
		select {
		case err := <-errCh:
			stop(err)
			return l.drainPool(&wg, errCh, firstErr)
		default:
		}
	}
	return l.drainPool(&wg, errCh, firstErr)
}

// drainPool 等在途全部收场，并取回第一条（若尚未取到）执行侧错误。
//
// 注意：**不取消 execCtx**——调用方已决定是外层取消（ctx 派生取消已传递）
// 还是基础设施故障（调用方先 stopExec）。排水完成之前不得返回，否则
// 任务会悬在 claimed（收场动作还没落库）。
func (l *Loop) drainPool(wg *sync.WaitGroup, errCh <-chan error, firstErr error) error {
	wg.Wait()
	if firstErr == nil {
		select {
		case firstErr = <-errCh:
		default:
		}
	}
	return firstErr
}

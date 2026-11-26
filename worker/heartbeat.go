// heartbeat.go —— 注册心跳与在途租约续期（issue #179/#196/#266 共用的循环）。
//
// # 两个心跳、互不阻断
//
// 注册心跳走独立节拍（HeartbeatInterval）；任务租约续期按**每条任务自己的
// 到期时刻**调度，两者互不阻断：身份的可用性不是任务正确性的前置条件
// （issue #179 的触发序列）。
//
// # 续期调度（issue #266）
//
// 旧实现每拍逐条串行 Renew 全量在途任务：200 个任务 × 100ms 一轮 = 20s，
// TTL 15s 时后段任务在轮到它之前就已过期。现在：
//
//   - **按到期时刻调度**：每条 claimed 有 nextRenew（本地时钟）= 上次成功
//     续期 + (TTL − RenewMargin)。心跳只续「到点」的任务，并用定时器等到
//     最近一个到期点；新增在途经 inflight.wake 立即重排；
//   - **批量优先**：任务板实现 taskboard.BatchRenewingBoard 时，一次往返
//     续一批（每批至多 renewBatchLimit 条）；
//   - **有界并发**：没有批量面时按 RenewConcurrency 并发逐条续期，单条慢
//     不拖住同拍其它任务；
//   - **单条超时**：每条/每批续期有独立的 RenewTimeout；超时只算该任务
//     一次失败，且与普通失败**分开计数**（renewTimeouts / renewFails）；
//   - 连续失败（含超时）达到 RenewFailMax ⇒ 停止领取并封口该任务
//     （ErrLeaseUnconfirmed 的既有语义）。
//
// # 锁纪律（issue #326）
//
// 续期判定与计数在 claimed.mu 内（与本任务的收场互斥）；所有宿主可见动作
// ——OnLeaseLost 通知、取消在途 ctx、停止领取——都在锁外、由心跳协程顺序
// 执行。宿主回调回查本包不会二次抢锁死锁，回调 panic 也不会掀翻心跳。
//
// # panic 隔离（issue #368）
//
// 同一宿主接缝（任务板续期）的两条路径必须同口径：并发路径每条
// async.GoSafe（逐条隔离），批量路径经 renewBatchCall recover +
// async.ReportPanic（整批隔离）。宿主 RenewBatch/Renew 的 panic 都不得
// 逃出心跳协程——逃出一次就是全部在途租约失保、他副本接管、非幂等
// 副作用双份。
//
// 计数住在 claimed 上、只由心跳协程（及其派生的续期 goroutine）在 mu 下
// 读写；在途集合本身由 inflight.mu 护住。
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// heartbeat 跑心跳/续期循环，直到外层 ctx 取消、stopping 关闭或注册身份
// 丢失。done 在退出时关闭（Run 用它建立「心跳已退出」的先行发生）。
func (l *Loop) heartbeat(
	ctx context.Context,
	claimCancel context.CancelFunc,
	reg Registration,
	set *inflight,
	stopping <-chan struct{},
	lost, unconfirmed *bool,
	done chan<- struct{},
) {
	defer close(done)
	regTicker := time.NewTicker(l.cfg.HeartbeatInterval)
	defer regTicker.Stop()
	renewTimer := time.NewTimer(set.nextRenewDelay(time.Now()))
	defer renewTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopping:
			return
		case <-set.wake:
			resetTimer(renewTimer, set.nextRenewDelay(time.Now()))
		case <-renewTimer.C:
			now := time.Now()
			if due := set.due(now); len(due) > 0 {
				for _, o := range l.runDueRenewals(ctx, due) {
					l.applyRenewOutcome(ctx, claimCancel, o, unconfirmed)
				}
			}
			resetTimer(renewTimer, set.nextRenewDelay(time.Now()))
		case <-regTicker.C:
			// 注册心跳：身份的资格检查。非身份类失败（含 ctx 取消的抖动）
			// 不判丢失——把存储抖动当成身份丢失，驱动会在任务板正常时自杀。
			if l.registrationBeat(ctx, claimCancel, reg, lost) {
				return
			}
		}
	}
}

// registrationBeat 一次注册心跳。返回 true = 心跳协程应退出
// （身份丢失或外层 ctx 已取消）。
func (l *Loop) registrationBeat(ctx context.Context, claimCancel context.CancelFunc, reg Registration, lost *bool) bool {
	if _, hbErr := l.registry.Heartbeat(ctx, reg.ID, reg.Token, l.cfg.TTL); hbErr != nil {
		if errors.Is(hbErr, ErrNotRegistered) {
			*lost = true
			claimCancel()
			return true
		}
		return ctx.Err() != nil
	}
	return ctx.Err() != nil
}

// renewOutcome 一条任务的续期结论（由心跳协程在锁外统一处置）。
type renewOutcome struct {
	cur     *claimed
	verdict renewVerdict
}

// runDueRenewals 续期本拍到点的任务：优先批量面，否则有界并发逐条。
func (l *Loop) runDueRenewals(ctx context.Context, due []*claimed) []renewOutcome {
	if batch, ok := l.board.(taskboard.BatchRenewingBoard); ok {
		var out []renewOutcome
		for start := 0; start < len(due); start += renewBatchLimit {
			end := min(start+renewBatchLimit, len(due))
			out = append(out, l.renewBatch(ctx, batch, due[start:end])...)
		}
		return out
	}
	return l.renewConcurrent(ctx, due)
}

// renewConcurrent 有界并发逐条续期（无批量面时的路径）。每条独立超时，
// 一条慢/失败不阻断同拍其它任务；结果汇总后由心跳协程顺序处置。
func (l *Loop) renewConcurrent(ctx context.Context, due []*claimed) []renewOutcome {
	sem := make(chan struct{}, l.cfg.RenewConcurrency)
	out := make(chan renewOutcome, len(due))
	var wg sync.WaitGroup
	for _, cur := range due {
		wg.Add(1)
		c := cur
		async.GoSafe(func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			verdict := c.renew(ctx, l.cfg.RenewTimeout, func(rctx context.Context) (taskboard.Task, error) {
				return l.board.Renew(rctx, c.id, c.token, taskboard.ClaimRequest{TTL: l.cfg.TTL})
			}, l.renewAfter(), l.renewRetryAfter(), l.cfg.RenewFailMax)
			out <- renewOutcome{cur: c, verdict: verdict}
		})
	}
	wg.Wait()
	close(out)
	res := make([]renewOutcome, 0, len(due))
	for o := range out {
		res = append(res, o)
	}
	return res
}

// renewBatch 一次往返续一批。为保证「续期与本任务收场互斥」，批内各任务的
// mu 在调用期间保持持有（不跨任务加锁顺序问题：没有第二处同时持多把）。
// 批量成功时逐条按结果记判；整批基础设施失败时按整批失败（逐条结果不可信）。
func (l *Loop) renewBatch(ctx context.Context, batch taskboard.BatchRenewingBoard, due []*claimed) []renewOutcome {
	reqs := make([]taskboard.RenewRequest, 0, len(due))
	held := make([]*claimed, 0, len(due))
	for _, cur := range due {
		cur.mu.Lock()
		if cur.finalizing {
			cur.mu.Unlock()
			continue
		}
		held = append(held, cur)
		reqs = append(reqs, taskboard.RenewRequest{ID: cur.id, Token: cur.token, TTL: l.cfg.TTL})
	}
	if len(reqs) == 0 {
		return nil
	}
	defer func() {
		for _, cur := range held {
			cur.mu.Unlock()
		}
	}()

	rctx := ctx
	cancel := func() {}
	if l.cfg.RenewTimeout > 0 {
		rctx, cancel = context.WithTimeout(ctx, l.cfg.RenewTimeout)
	}
	results, err := l.renewBatchCall(rctx, batch, reqs)
	timedOut := rctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded)
	cancel()

	outcomes := make([]renewOutcome, 0, len(held))
	for i, cur := range held {
		perErr := err
		if perErr == nil {
			switch {
			case i >= len(results):
				perErr = errors.New("worker: 批量续期返回条数不足（逐条结果不可信）")
			case results[i].ID != cur.id:
				perErr = errors.New("worker: 批量续期结果与请求不同序/不同 ID（逐条结果不可信）")
			default:
				perErr = results[i].Err
			}
		}
		verdict := cur.recordRenewLocked(perErr, timedOut, ctx.Err() != nil,
			l.renewAfter(), l.renewRetryAfter(), l.cfg.RenewFailMax)
		outcomes = append(outcomes, renewOutcome{cur: cur, verdict: verdict})
	}
	return outcomes
}

// renewBatchCall 调宿主批量续期面并隔离 panic（issue #368）。
//
// 批量路径此前直接调 batch.RenewBatch：宿主 panic 会逃出 renewBatch →
// 逃出 heartbeat → 被 loop.go 的 GoSafe 吞掉，心跳协程就此死亡、全部在途
// 租约停止续期。这里与并发路径（每条独立 async.GoSafe）同口径隔离：
// recover + async.ReportPanic，并把整批按基础设施失败处理——逐条结果不可信，
// 由 renewBatch 对每条记一次续期失败（renewFails），心跳下一拍照常重试。
//
// 反向验证（issue #368 要求）：把本函数体改回直接调用 batch.RenewBatch ⇒
// TestRenewBatchPanicIsolated 必须转红（panic 逃出续期路径）。
func (l *Loop) renewBatchCall(
	ctx context.Context,
	batch taskboard.BatchRenewingBoard,
	reqs []taskboard.RenewRequest,
) (results []taskboard.Renewal, err error) {
	defer func() {
		if r := recover(); r != nil {
			async.ReportPanic(r)
			results, err = nil, errors.New("worker: 宿主任务板 RenewBatch panic（已隔离；整批按失败处理）")
		}
	}()
	return batch.RenewBatch(ctx, reqs)
}

// renewOne 续期一条在途租约并执行锁外动作（单条路径；issue #326 的用例
// 也直接调它钉「通知在锁外」）。
func (l *Loop) renewOne(ctx context.Context, claimCancel context.CancelFunc, cur *claimed, unconfirmed *bool) {
	verdict := cur.renew(ctx, l.cfg.RenewTimeout, func(rctx context.Context) (taskboard.Task, error) {
		return l.board.Renew(rctx, cur.id, cur.token, taskboard.ClaimRequest{TTL: l.cfg.TTL})
	}, l.renewAfter(), l.renewRetryAfter(), l.cfg.RenewFailMax)
	l.applyRenewOutcome(ctx, claimCancel, renewOutcome{cur: cur, verdict: verdict}, unconfirmed)
}

// applyRenewOutcome 把续期结论落到宿主可见动作上（心跳协程、锁外执行）。
func (l *Loop) applyRenewOutcome(ctx context.Context, claimCancel context.CancelFunc, o renewOutcome, unconfirmed *bool) {
	switch o.verdict {
	case renewTakenOver:
		l.notifyLeaseLost(o.cur.id, LeaseLostTakenOver)
		o.cur.cancel()
	case renewUnconfirmed:
		if ctx.Err() != nil {
			return // 外层已在停机：不把续期失败升级成「资格无法确认」
		}
		*unconfirmed = true
		l.notifyLeaseLost(o.cur.id, LeaseLostUnconfirmed)
		claimCancel()  // 停止领取：答不出「谁在持」就不发起新的副作用
		o.cur.cancel() // 在途封口
	}
}

// renewAfter 成功续期后的下次间隔 = TTL − 安全余量（直构测试兜底 1s）。
func (l *Loop) renewAfter() time.Duration {
	if d := l.cfg.TTL - l.cfg.RenewMargin; d > 0 {
		return d
	}
	return time.Second
}

// renewRetryAfter 失败后的重试间隔（心跳节拍；直构测试兜底 1s）。
func (l *Loop) renewRetryAfter() time.Duration {
	if l.cfg.HeartbeatInterval > 0 {
		return l.cfg.HeartbeatInterval
	}
	return time.Second
}

// resetTimer 安全重置定时器（先停并排空已触发的信号）。
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

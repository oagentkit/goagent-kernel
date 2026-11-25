// board_renew.go —— Board 适配器的续期入口（含判定—提交窗口事实，
// issue #271）。从 board.go 拆出：结构阈值（400 行）按判据的一侧切，
// 续期（资格检查）与认领/收尾入口各自可读。
package coord

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// Renew 续期并确认持有资格（taskboard.RenewingBoard）：令牌不匹配或已过期
// 都报 ErrLeaseLost 且不做任何修改——续期不是心跳，是资格检查。
//
// 需要每次判定的窗口事实（是否在窗口内过期、是否超预算）时用
// RenewWithVerdict；本入口保持 taskboard.RenewingBoard 的原签名。
func (a *BoardAdapter) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	task, _, err := a.RenewWithVerdict(ctx, id, token, req)
	return task, err
}

// RenewWithVerdict 同 Renew，并返回判定—提交窗口的观测事实（issue #271）。
//
// 后端实现 AtomicDecision 时，取存储时刻与提交在同一事务内（窗口 0，
// 窗口内过期必被拒绝）；否则走误差预算降级路径——续期仍可能按旧快照
// 提交，但 verdict 会给出实际窗口、旧到期时刻是否落在窗口内、是否超预算，
// Warning 可直接写审计/日志。
func (a *BoardAdapter) RenewWithVerdict(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, LeaseVerdict, error) {
	scope, serr := a.scope(ctx)
	if serr != nil {
		return taskboard.Task{}, LeaseVerdict{}, serr
	}
	tk := taskKey(scope, id)
	a.mu.Lock()
	injected := a.now
	a.mu.Unlock()
	return renewWithVerdict(ctx, a.b, injected, []string{tk},
		func(now time.Time, snap map[string]Entry) (taskboard.Task, time.Time, []Assert, []Mutation, error) {
			cur, err := requireClaimed(snap[tk], scope, id, token)
			if err != nil {
				return taskboard.Task{}, time.Time{}, nil, nil, err
			}
			oldUntil := cur.Lease.Until
			// 判定时刻取存储时钟（issue #194）：资格是跨进程共享的事实，
			// 「过期与否」不能由续期请求所在节点的本地时钟说了算。
			if !taskboard.LeaseHeld(cur, now) {
				return taskboard.Task{}, oldUntil, nil, nil, errors.Wrap(taskboard.ErrLeaseLost,
					"租约已过期，资格终止（请重新认领）", "task", string(id))
			}
			if req.TTL > 0 {
				cur.Lease.Until = now.Add(req.TTL)
			} else {
				cur.Lease.Until = time.Time{} // TTL=0 = 不限期（与 Claim 同一语义）
			}
			cur.Tenant = scope
			cur.Seq++
			muts, err := setTaskMutation(scope, cur)
			if err != nil {
				return taskboard.Task{}, oldUntil, nil, nil, err
			}
			return cur, oldUntil, assertsFrom(snap), muts, nil
		})
}

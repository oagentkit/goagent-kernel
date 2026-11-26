// store_lease.go —— Store 适配器的**持有期收尾**侧（Complete / Release /
// Abandon / Renew / MarkUncertain）。从 store.go 拆出：结构阈值（400 行）
// 按判据的一侧切——施加侧（Acquire，判定最重）单独在 store.go，
// 这五个入口共享同一性质：**凭凭据收尾**（令牌/栅栏对得上才动状态，
// 收尾不重判授予），与裁决侧（store_reconcile.go，凭下游事实不凭持有权）
// 又是两回事。三个文件三份性质，各自可读。
package coord

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// Complete 标记完成并留存结果（幂等：先到者的结果不被后到者覆盖；
// 三项联查通过才接受）。
func (s *StoreAdapter) Complete(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, res sideeffect.Result) (sideeffect.Result, error) {
	ak, rk := actKeyPrefix+string(act.Key), resKeyPrefix+act.Resource
	return casLoop(ctx, s.b, []string{ak, rk},
		func(snap map[string]Entry) (sideeffect.Result, []Assert, []Mutation, error) {
			a, err := decodeAct(snap[ak])
			if err != nil {
				return sideeffect.Result{}, nil, nil, err
			}
			// 身份不可变：先于「已完成 → 重放」（把旧身份的结果交给新身份
			// 的调用方就是答非所问）。
			if err := identityErr(a, act); err != nil {
				return sideeffect.Result{}, nil, nil, err
			}
			if a.Done {
				return sideeffect.Result{Payload: a.Result}, nil, nil, nil // 幂等
			}
			rr, err := decodeRes(snap[rk])
			if err != nil {
				return sideeffect.Result{}, nil, nil, err
			}
			if err := holdOwnerErr(act, lease, rr); err != nil {
				return sideeffect.Result{}, nil, nil, err
			}
			if err := intentOwnerErr(act, lease, a); err != nil {
				return sideeffect.Result{}, nil, nil, err
			}
			a.Done = true
			a.Result = res.Payload
			a.Intent = nil // 完成后意图一并撤销（这次尝试已有结论）
			rr.Holder = "" // 资源闸释放（否则后来者被「资源正忙」挡住）
			am, merr := actMutation(act.Key, a)
			if merr != nil {
				return sideeffect.Result{}, nil, nil, merr
			}
			rm, rerr := resMutation(act.Resource, rr)
			if rerr != nil {
				return sideeffect.Result{}, nil, nil, rerr
			}
			return sideeffect.Result{Payload: a.Result}, assertsFrom(snap), []Mutation{am, rm}, nil
		})
}

// Release 释放资源持有，**不动** Key 级的执行意图——意图没被裁决之前，
// 这个 Key 不得再被执行（「施加成功但进程死在记账之前」的封口）。
func (s *StoreAdapter) Release(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease) error {
	rk := resKeyPrefix + act.Resource
	_, err := casLoop(ctx, s.b, []string{rk},
		func(snap map[string]Entry) (struct{}, []Assert, []Mutation, error) {
			rr, err := decodeRes(snap[rk])
			if err != nil {
				return struct{}{}, nil, nil, err
			}
			if err := holdOwnerErr(act, lease, rr); err != nil {
				return struct{}{}, nil, nil, err
			}
			rr.Holder = ""
			rm, err := resMutation(act.Resource, rr)
			if err != nil {
				return struct{}{}, nil, nil, err
			}
			return struct{}{}, assertsFrom(snap), []Mutation{rm}, nil
		})
	return err
}

// Abandon 显式放弃本次尝试：撤销执行意图 + 释放资源持有（资源闸只在
// 仍是自己的时才放——易主了就不碰，那是新持有者的闸）。
func (s *StoreAdapter) Abandon(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, _ string) error {
	ak, rk := actKeyPrefix+string(act.Key), resKeyPrefix+act.Resource
	_, err := casLoop(ctx, s.b, []string{ak, rk},
		func(snap map[string]Entry) (struct{}, []Assert, []Mutation, error) {
			a, err := decodeAct(snap[ak])
			if err != nil {
				return struct{}{}, nil, nil, err
			}
			if err := intentOwnerErr(act, lease, a); err != nil {
				return struct{}{}, nil, nil, err
			}
			a.Intent = nil
			rr, err := decodeRes(snap[rk])
			if err != nil {
				return struct{}{}, nil, nil, err
			}
			am, merr := actMutation(act.Key, a)
			if merr != nil {
				return struct{}{}, nil, nil, merr
			}
			muts := []Mutation{am}
			if holdOwnerErr(act, lease, rr) == nil {
				rr.Holder = ""
				rm, rerr := resMutation(act.Resource, rr)
				if rerr != nil {
					return struct{}{}, nil, nil, rerr
				}
				muts = append(muts, rm)
			}
			return struct{}{}, assertsFrom(snap), muts, nil
		})
	return err
}

// Renew 续期并确认持有资格：闸已易主、尝试已失效**或租约已过期**则拒绝
// 续期（假续期延长的是别人的闸——重复施加的入口）。
func (s *StoreAdapter) Renew(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, ttl time.Duration) (sideeffect.Lease, error) {
	renewed, _, err := s.RenewWithVerdict(ctx, act, lease, ttl)
	return renewed, err
}

// RenewWithVerdict 同 Renew，并返回判定—提交窗口的观测事实（issue #271）。
// 原子后端（AtomicDecision）窗口 0；非原子后端按误差预算降级并留下
// 窗口内过期/超预算的可解释事实。
func (s *StoreAdapter) RenewWithVerdict(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, ttl time.Duration) (sideeffect.Lease, LeaseVerdict, error) {
	ak, rk := actKeyPrefix+string(act.Key), resKeyPrefix+act.Resource
	s.mu.Lock()
	injected := s.now
	s.mu.Unlock()
	return renewWithVerdict(ctx, s.b, injected, []string{ak, rk},
		func(now time.Time, snap map[string]Entry) (sideeffect.Lease, time.Time, []Assert, []Mutation, error) {
			a, err := decodeAct(snap[ak])
			if err != nil {
				return sideeffect.Lease{}, time.Time{}, nil, nil, err
			}
			rr, err := decodeRes(snap[rk])
			if err != nil {
				return sideeffect.Lease{}, time.Time{}, nil, nil, err
			}
			if err := holdOwnerErr(act, lease, rr); err != nil {
				return sideeffect.Lease{}, time.Time{}, nil, nil, err
			}
			if err := intentOwnerErr(act, lease, a); err != nil {
				return sideeffect.Lease{}, time.Time{}, nil, nil, err
			}
			// 判定时刻取存储时钟（issue #194，时间权威次序见 resolveNow）。
			oldUntil := rr.Until
			// 过期即资格终止（Store.Renew 契约）：租约已过期而无人接管时，
			// 旧持有者不得复活它——否则过期那次尝试绕开「先对账」直接继续
			// 推进。判定在写入之前，被拒的续期不留任何状态。
			if !rr.Until.After(now) {
				return sideeffect.Lease{}, oldUntil, nil, nil, errors.Wrap(sideeffect.ErrFenceStale,
					"租约已过期且无人接管——到期即资格终止（要继续做请重新 Acquire）",
					"key", string(act.Key), "resource", act.Resource)
			}
			rr.Until = now.Add(ttl)
			a.Intent.Expires = now.Add(ttl)
			am, merr := actMutation(act.Key, a)
			if merr != nil {
				return sideeffect.Lease{}, oldUntil, nil, nil, merr
			}
			rm, rerr := resMutation(act.Resource, rr)
			if rerr != nil {
				return sideeffect.Lease{}, oldUntil, nil, nil, rerr
			}
			return sideeffect.Lease{Resource: act.Resource, Fence: lease.Fence, Holder: lease.Holder, TTL: ttl},
				oldUntil, assertsFrom(snap), []Mutation{am, rm}, nil
		})
}

// MarkUncertain 把该 Key 标记为「结果未知」（按键，与资源租约无关）。
// 标记与「撤销意图」在同一事务里：事务失败 = 什么都没发生，
// 意图原样留在存储里挡着后续 Acquire（契约硬要求第 5 条）。
// 标记成功后闸**不**自动释放——调用方显式 Release 只放资源。
func (s *StoreAdapter) MarkUncertain(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, reason string) error {
	ak, rk := actKeyPrefix+string(act.Key), resKeyPrefix+act.Resource
	_, err := casLoop(ctx, s.b, []string{ak, rk},
		func(snap map[string]Entry) (struct{}, []Assert, []Mutation, error) {
			a, err := decodeAct(snap[ak])
			if err != nil {
				return struct{}{}, nil, nil, err
			}
			rr, err := decodeRes(snap[rk])
			if err != nil {
				return struct{}{}, nil, nil, err
			}
			if err := holdOwnerErr(act, lease, rr); err != nil {
				return struct{}{}, nil, nil, err
			}
			if err := intentOwnerErr(act, lease, a); err != nil {
				return struct{}{}, nil, nil, err
			}
			a.Uncertain = true
			a.Reason = reason
			a.Intent = nil
			am, merr := actMutation(act.Key, a)
			if merr != nil {
				return struct{}{}, nil, nil, merr
			}
			return struct{}{}, assertsFrom(snap), []Mutation{am}, nil
		})
	return err
}

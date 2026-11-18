// effect_store.go —— 副作用面的对外接口（sideeffect.Store + OwnedResolveStore）。
//
// 本文件只是**装配**：判定在 effect.go，事务在 tx.go。两种装配：
//
//   - 独立事务（Store.Effects()）：每次调用自己开一个事务；
//   - 事务内（Tx.Effects()）：与其它面的变更落在同一次提交里。
//
// 两个入口调用**同一套判据函数** ⇒ 不会出现「换个入口语义就变」。
package sqlitestore

import (
	"context"
	"time"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// EffectStore 副作用面（sideeffect.Store + sideeffect.OwnedResolveStore）。
type EffectStore struct {
	exec exec
}

// UseClock 注入存储时钟（sidetest.ClockedStore 的接缝要求）。
func (e *EffectStore) UseClock(now func() time.Time) {
	if e.exec.db != nil {
		e.exec.db.setClock(now)
	}
}

// Acquire 一次原子判定，返回「能否执行」与「若能执行则用哪一代令牌」。
func (e *EffectStore) Acquire(ctx context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Outcome, sideeffect.Lease, sideeffect.Result, error) {
	now := e.exec.now()
	var outcome sideeffect.Outcome
	var lease sideeffect.Lease
	var result sideeffect.Result
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		outcome, lease, result, err = effectAcquire(tx, act, ttl, now)
		return err
	})
	if err != nil {
		return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
	}
	return outcome, lease, result, nil
}

// Complete 标记该键的动作已完成并记录结果（幂等：先到者为准）。
func (e *EffectStore) Complete(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, res sideeffect.Result) (sideeffect.Result, error) {
	var out sideeffect.Result
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		out, err = effectComplete(tx, act, lease, res)
		return err
	})
	if err != nil {
		return sideeffect.Result{}, err
	}
	return out, nil
}

// Release 释放**资源持有**（闸），**不动**该键的执行意图。
func (e *EffectStore) Release(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease) error {
	return e.exec.run(ctx, func(tx *txCtx) error {
		return effectRelease(tx, act, lease)
	})
}

// Abandon 显式放弃本次尝试：撤销执行意图，并释放资源持有。
func (e *EffectStore) Abandon(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, reason string) error {
	return e.exec.run(ctx, func(tx *txCtx) error {
		return effectAbandon(tx, act, lease, reason)
	})
}

// Renew 续期并**同时确认持有资格**。
func (e *EffectStore) Renew(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, ttl time.Duration) (sideeffect.Lease, error) {
	now := e.exec.now()
	var out sideeffect.Lease
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		out, err = effectRenew(tx, act, lease, ttl, now)
		return err
	})
	if err != nil {
		return sideeffect.Lease{}, err
	}
	return out, nil
}

// MarkUncertain 把该键标记为「结果未知」。
func (e *EffectStore) MarkUncertain(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, reason string) error {
	return e.exec.run(ctx, func(tx *txCtx) error {
		return effectMarkUncertain(tx, act, lease, reason)
	})
}

// ResolveUncertain 裁决「结果未知」的键（先到者为准，不校验对账凭据）。
func (e *EffectStore) ResolveUncertain(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	now := e.exec.now()
	var outcome sideeffect.Outcome
	var result sideeffect.Result
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		outcome, result, err = effectResolve(tx, act, res, "", now)
		return err
	})
	if err != nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, err
	}
	return outcome, result, nil
}

// ResolveUncertainOwned 带对账占用凭据的裁决：holder 不是当前有效对账
// 认领的持有者 → ErrReconcileLost（不改任何状态）。
//
// 实现了它 = 声明本存储支持跨进程长对账（sidetest 对缺失**硬失败**）。
func (e *EffectStore) ResolveUncertainOwned(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution, holder string) (sideeffect.Outcome, sideeffect.Result, error) {
	if holder == "" {
		return sideeffect.OutcomeContended, sideeffect.Result{}, sideeffect.ErrReconcileLost
	}
	now := e.exec.now()
	var outcome sideeffect.Outcome
	var result sideeffect.Result
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		outcome, result, err = effectResolve(tx, act, res, holder, now)
		return err
	})
	if err != nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, err
	}
	return outcome, result, nil
}

// BeginReconcile 认领对账权：同一键同一时刻至多一个对账者。
func (e *EffectStore) BeginReconcile(ctx context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Reconcile, error) {
	now := e.exec.now()
	var out sideeffect.Reconcile
	err := e.exec.run(ctx, func(tx *txCtx) error {
		var err error
		out, err = effectBeginReconcile(tx, act, ttl, now)
		return err
	})
	if err != nil {
		return sideeffect.Reconcile{}, err
	}
	return out, nil
}

// 编译期断言：本实现满足副作用闸的完整契约面（含带凭据的裁决入口）。
var (
	_ sideeffect.Store             = (*EffectStore)(nil)
	_ sideeffect.OwnedResolveStore = (*EffectStore)(nil)
)

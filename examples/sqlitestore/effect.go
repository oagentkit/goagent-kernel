// effect.go —— 副作用面（sideeffect.Store）的判定内核。
//
// # 这份内核钉住的三条判据
//
//  1. **三项联查**：Renew / Complete / MarkUncertain / Abandon / Release
//     都必须校验「令牌仍是最新代 + 持有者身份与授予时铸造的一致 + 租约与
//     动作的资源一致」。只比栅栏令牌是不够的：释放闸之后令牌**没有变**
//     （栅栏只在接管时递增），于是「释放后的租约」能拿同一枚令牌继续续期、
//     继续记账；令牌还会经下游流转（FencedSink），见过的组件拿着令牌就能
//     冒充持有者——持有者身份是授予时**存储侧铸造**的第二份凭据。
//  2. **执行意图**：Acquire 授予执行权时原子登记「本次尝试还在」；租约过期
//     且未裁决时，同一 Key 转入需对账态，而不是重新授予执行权。
//  3. **对账占用**：BeginReconcile 同一时刻至多一个对账者，占用过期可重入；
//     RetrySafetyUnsafe 的动作裁决「未生效」不解除阻塞（显式决策才放行）。
//
// # 与内存参考实现的关系
//
// 判据逐条对齐 examples/reference-runtime（已过 sidetest 全套）——本文件是
// 它的**落库形态**：状态从日志重放，变更以「Key 级 + 资源级同一记录」的
// 形式落盘（一次原子操作面在存储侧成立）。
//
// # 判定顺序是契约里最要紧的一行
//
// 已完成 > 结果未知 > 执行中 > 资源被占 > 授予。顺序写反的代价是重复施加：
// 把「结果未知」排到「资源被占」之后，调用方会拿到 contended（以为「晚点
// 重试就行」），重试于是发生。
package sqlitestore

import (
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// effectAcquire 一次原子判定（授予 / 重放 / 争用 / 未知）。
func effectAcquire(tx *txCtx, act sideeffect.Action, ttl time.Duration, now time.Time) (sideeffect.Outcome, sideeffect.Lease, sideeffect.Result, error) {
	cur := tx.st.actCopy(actKeyOf(act))
	curRes := tx.st.resCopy(resKeyOf(act))
	stage := func() error {
		return stageEffect(tx, act, cur, curRes)
	}

	// ── 1) 动作身份：优先于一切状态判定 ──
	if err := effectIdentityErr(cur, act); err != nil {
		return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
	}
	if !cur.IDSet {
		// 首次登记：身份一经登记即永久事实 ⇒ 本次调用即使不授予执行权也
		// 要落盘（否则「首次登记」在重启后失效，换身份的调用方会被当成
		// 首次——那是重复施加的入口重新打开）。
		cur.IDSet = true
		cur.IDResource = act.Resource
		cur.IDSafety = act.RetrySafety
		cur.IDPayloadHash = sideeffect.HashPayload(act.Payload)
	}

	// ── 2) 已完成 → 重放（优先于占用：动作做完了，后来者该拿结果）──
	if cur.Done {
		if err := stage(); err != nil {
			return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
		}
		return sideeffect.OutcomeReplayed, sideeffect.Lease{}, cur.Result, nil
	}

	// ── 3) 结果未知 → 硬阻塞（对账中归入未知态一族）──
	if cur.Uncertain {
		if err := stage(); err != nil {
			return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
		}
		return sideeffect.OutcomeUncertain, sideeffect.Lease{}, sideeffect.Result{}, nil
	}
	if cur.Recon != nil {
		if now.Before(cur.Recon.Expires) {
			// 对账中：Acquire 必须继续拒绝授予执行权（对账中 ≠ 已裁决）。
			if err := stage(); err != nil {
				return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
			}
			return sideeffect.OutcomeUncertain, sideeffect.Lease{}, sideeffect.Result{}, nil
		}
		// 过期占用清掉（可重入：对账者死亡不永久占住这个 Key）。
		cur.Recon = nil
	}

	// ── 4) 执行中：这次尝试还没裁决 ⇒ 不得重新授予执行权 ──
	if cur.Intent != nil {
		if now.Before(cur.Intent.Expires) {
			if err := stage(); err != nil {
				return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
			}
			return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, nil
		}
		// 租约过期 ⇒ 这次尝试的结果未知（可能已施加、账没记上）。
		effectDropIntent(cur, curRes, act)
		if act.RetrySafety != sideeffect.RetrySafetySafe {
			cur.Uncertain = true
			cur.Reason = "上一次尝试的租约已过期，结果未知"
			if err := stage(); err != nil {
				return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
			}
			return sideeffect.OutcomeUncertain, sideeffect.Lease{}, sideeffect.Result{}, nil
		}
		// 声明了可安全重试（下游按幂等键去重）⇒ 意图可清，继续走第 5 条。
	}

	// ── 5) 资源被占 ──
	if curRes.Holder != "" && curRes.Until.After(now) {
		if err := stage(); err != nil {
			return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
		}
		return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, nil
	}

	// ── 6) 授予 ──
	curRes.Fence++
	holder := sideeffect.MintHolder()
	curRes.Holder = holder
	curRes.Until = now.Add(ttl)
	// 意图、令牌、持有者身份在同一次原子操作里登记：分两次写，中间死掉
	// 就等于没写（意图悬空 ⇒ 重复施加；意图没写 ⇒ 入口重新打开）。
	cur.Intent = &intentEntry{
		Resource: act.Resource,
		Fence:    curRes.Fence,
		Holder:   holder,
		Expires:  now.Add(ttl),
	}
	if err := stage(); err != nil {
		return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
	}
	return sideeffect.OutcomeApplied,
		sideeffect.Lease{Resource: act.Resource, Fence: curRes.Fence, Holder: holder, TTL: ttl},
		sideeffect.Result{}, nil
}

// effectComplete 标记该键的动作已完成并留存结果。**先到者为准**。
func effectComplete(tx *txCtx, act sideeffect.Action, lease sideeffect.Lease, res sideeffect.Result) (sideeffect.Result, error) {
	cur := tx.st.actCopy(actKeyOf(act))
	curRes := tx.st.resCopy(resKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return sideeffect.Result{}, err
	}
	// 幂等：先到者的结果不被后到者覆盖。
	if cur.Done {
		return cur.Result, nil
	}
	if err := effectHoldOwnerErr(act, lease, curRes); err != nil {
		return sideeffect.Result{}, err
	}
	if err := effectIntentOwnerErr(act, lease, cur); err != nil {
		return sideeffect.Result{}, err
	}
	cur.Done = true
	cur.Result = sideeffect.Result{Payload: append([]byte(nil), res.Payload...)}
	cur.Intent = nil
	// 动作已完成 ⇒ 资源闸释放（否则后来者会被「资源正忙」挡住）。
	curRes.Holder = ""
	if err := stageEffect(tx, act, cur, curRes); err != nil {
		return sideeffect.Result{}, err
	}
	return cur.Result, nil
}

// effectRelease 释放**资源持有**（闸），**不动**该键的执行意图。
//
// 意图没被裁决之前，这个键不得再被执行——否则「施加成功但进程死在记账
// 之前」会被下一次 Acquire 变成重复施加。
func effectRelease(tx *txCtx, act sideeffect.Action, lease sideeffect.Lease) error {
	cur := tx.st.actCopy(actKeyOf(act))
	curRes := tx.st.resCopy(resKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return err
	}
	if err := effectHoldOwnerErr(act, lease, curRes); err != nil {
		return err
	}
	curRes.Holder = ""
	return stageEffect(tx, act, nil, curRes)
}

// effectAbandon 显式放弃本次尝试：撤销执行意图，并释放资源持有。
func effectAbandon(tx *txCtx, act sideeffect.Action, lease sideeffect.Lease, _ string) error {
	cur := tx.st.actCopy(actKeyOf(act))
	curRes := tx.st.resCopy(resKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return err
	}
	if err := effectIntentOwnerErr(act, lease, cur); err != nil {
		return err
	}
	cur.Intent = nil
	// 资源闸只在令牌仍是最新代时才释放（闸已易主就别碰它——那是新持有者
	// 的闸）。
	if err := effectHoldOwnerErr(act, lease, curRes); err == nil {
		curRes.Holder = ""
	}
	return stageEffect(tx, act, cur, curRes)
}

// effectRenew 续期并**同时确认持有资格**：闸已易主、尝试已失效**或租约已
// 过期**，一律拒绝续期（假续期是重复施加的入口）。
func effectRenew(tx *txCtx, act sideeffect.Action, lease sideeffect.Lease, ttl time.Duration, now time.Time) (sideeffect.Lease, error) {
	cur := tx.st.actCopy(actKeyOf(act))
	curRes := tx.st.resCopy(resKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return sideeffect.Lease{}, err
	}
	if err := effectHoldOwnerErr(act, lease, curRes); err != nil {
		return sideeffect.Lease{}, err
	}
	if err := effectIntentOwnerErr(act, lease, cur); err != nil {
		return sideeffect.Lease{}, err
	}
	// 过期即资格终止（与 taskboard.Renew 同一条口径）：租约已过期而无人
	// 接管时，旧持有者不得复活它——否则那次尝试绕开「先对账」继续推进。
	if !curRes.Until.After(now) {
		return sideeffect.Lease{}, oerrors.Wrap(sideeffect.ErrFenceStale,
			"sqlitestore: 租约已过期且无人接管——到期即资格终止（要继续做请重新 Acquire）",
			"key", string(act.Key), "resource", act.Resource)
	}
	curRes.Until = now.Add(ttl)
	cur.Intent.Expires = now.Add(ttl)
	if err := stageEffect(tx, act, cur, curRes); err != nil {
		return sideeffect.Lease{}, err
	}
	return sideeffect.Lease{Resource: act.Resource, Fence: lease.Fence, Holder: lease.Holder, TTL: ttl}, nil
}

// effectMarkUncertain 把该键标记为「结果未知」（按键，与资源租约无关）。
//
// 标记与「撤销意图」在**同一次提交**里：写失败时存储什么都没变 ⇒ 意图原样
// 留在存储里，后续 Acquire 仍被挡住（契约硬要求第 5 条）。
func effectMarkUncertain(tx *txCtx, act sideeffect.Action, lease sideeffect.Lease, reason string) error {
	cur := tx.st.actCopy(actKeyOf(act))
	curRes := tx.st.resCopy(resKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return err
	}
	if err := effectHoldOwnerErr(act, lease, curRes); err != nil {
		return err
	}
	if err := effectIntentOwnerErr(act, lease, cur); err != nil {
		return err
	}
	cur.Uncertain = true
	cur.Reason = reason
	cur.Intent = nil
	// 闸不自动释放（调用方还要显式调 Release 只放资源）。
	return stageEffect(tx, act, cur, nil)
}

// effectResolve 裁决「结果未知」的键。
//
// requiredHolder 非空时先校验对账占用凭据（已完成的重放无害，先于凭据
// 校验——读既有事实不问资格）。
func effectResolve(tx *txCtx, act sideeffect.Action, res sideeffect.Resolution, requiredHolder string, now time.Time) (sideeffect.Outcome, sideeffect.Result, error) {
	cur := tx.st.actCopy(actKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, err
	}
	if cur.Done {
		return sideeffect.OutcomeReplayed, cur.Result, nil
	}
	if requiredHolder != "" {
		if cur.Recon == nil || !now.Before(cur.Recon.Expires) || cur.Recon.Holder != requiredHolder {
			return sideeffect.OutcomeContended, sideeffect.Result{}, oerrors.Wrap(sideeffect.ErrReconcileLost,
				"sqlitestore: 出示的对账认领不是当前有效的一份（已过期或已被接管）",
				"key", string(act.Key))
		}
	}
	if !cur.Uncertain {
		return sideeffect.OutcomeContended, sideeffect.Result{}, sideeffect.ErrNotUncertain
	}
	// 裁决即结论：执行意图与对账占用一并撤销（这次尝试已有结论）。
	cur.Intent = nil
	cur.Recon = nil
	if res.Applied {
		cur.Done = true
		cur.Uncertain = false
		cur.Result = sideeffect.Result{Payload: append([]byte(nil), res.Result.Payload...)}
		if err := stageEffect(tx, act, cur, nil); err != nil {
			return sideeffect.OutcomeContended, sideeffect.Result{}, err
		}
		return sideeffect.OutcomeReplayed, cur.Result, nil
	}
	if act.RetrySafety == sideeffect.RetrySafetyUnsafe && !res.ForceRetryable {
		// 「不可重试」的动作，一句来自对账的「未生效」不足以重新开门：
		// 那句结论来自宿主的一次查询，查询可能出错，查错的代价是重复
		// 施加。未知标记**原样保留**（这个键仍被硬阻塞），放行须显式签字。
		if err := stageEffect(tx, act, cur, nil); err != nil {
			return sideeffect.OutcomeContended, sideeffect.Result{}, err
		}
		return sideeffect.OutcomeUncertain, sideeffect.Result{}, sideeffect.ErrUnsafeNotRetryable
	}
	cur.Uncertain = false
	if err := stageEffect(tx, act, cur, nil); err != nil {
		return sideeffect.OutcomeContended, sideeffect.Result{}, err
	}
	return sideeffect.OutcomeApplied, sideeffect.Result{}, nil
}

// effectBeginReconcile 认领对账权：同一键同一时刻至多一个对账者。
//
// 占用过期可重新认领（可重入）——对账者死亡不永久占住这个键。认领期间该
// 键仍是未知态（Acquire 继续拒绝），裁决走 ResolveUncertain。
func effectBeginReconcile(tx *txCtx, act sideeffect.Action, ttl time.Duration, now time.Time) (sideeffect.Reconcile, error) {
	if ttl <= 0 {
		return sideeffect.Reconcile{}, oerrors.Wrap(sideeffect.ErrInvalidTTL,
			"sqlitestore: 对账认领时长必须为正（零/负 TTL 造出出生即过期的认领）",
			"key", string(act.Key))
	}
	cur := tx.st.actCopy(actKeyOf(act))
	if err := effectIdentityErr(cur, act); err != nil {
		return sideeffect.Reconcile{}, err
	}
	if cur.Done || !cur.Uncertain {
		return sideeffect.Reconcile{}, sideeffect.ErrNotUncertain
	}
	if cur.Recon != nil && now.Before(cur.Recon.Expires) {
		return sideeffect.Reconcile{}, sideeffect.ErrReconcileTaken
	}
	holder := sideeffect.MintHolder()
	cur.Recon = &reconEntry{Holder: holder, Expires: now.Add(ttl)}
	if err := stageEffect(tx, act, cur, nil); err != nil {
		return sideeffect.Reconcile{}, err
	}
	return sideeffect.Reconcile{Holder: holder, ExpiresAt: now.Add(ttl)}, nil
}

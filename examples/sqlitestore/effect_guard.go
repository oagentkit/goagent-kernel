// effect_guard.go —— 副作用面的**判据辅助**（三项联查与暂存）。
//
// 单独成文件不是为了好看：effect.go 已经顶到「单文件 400 行」的结构阈值，
// 而这些辅助判据是**独立可读的一组**（它们是「谁有资格写」的全部口径）。
//
// # 为什么每条判据只有一个判据点
//
// 三项联查在两个入口（资源闸 / 执行意图）各查一遍，但**判的是同一件事**：
// 「这份凭据是不是本次授予的那份」。两份各写一遍的话，改一处忘另一处 ⇒
// 同一个错误有两个主人 ⇒ 改坏了一处，测试**看不出**区别（另一处还在判），
// 于是那条判据事实上没有承重夹具。
//
// 故下面三个 helper 各自是**唯一判据点**：资源闸与执行意图都调它。
package sqlitestore

import (
	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// effectIdentityErr 动作身份与首次登记不一致时报冲突。
//
// 判定**优先于**已完成重放：把旧身份的结果交给新身份的调用方就是答非所问。
// 身份三元组 = 资源 / 载荷摘要 / 重试安全性（见 ErrActionConflict）。
func effectIdentityErr(cur *actEntry, act sideeffect.Action) error {
	if !cur.IDSet {
		return nil
	}
	if cur.IDResource != act.Resource || cur.IDSafety != act.RetrySafety ||
		cur.IDPayloadHash != sideeffect.HashPayload(act.Payload) {
		return oerrors.Wrap(sideeffect.ErrActionConflict,
			"sqlitestore: 同 Key 的动作身份与首次登记不一致（这不是重试，是另一个动作）",
			"key", string(act.Key),
			"登记 resource", cur.IDResource, "本次 resource", act.Resource,
			"登记 safety", cur.IDSafety, "本次 safety", act.RetrySafety)
	}
	return nil
}

// effectHolderErr 三项联查的第二项：持有者身份必须与授予时铸造的那份一致。
//
// holder = 存储侧记着的持有者身份；presented = 调用方出示的那份。
// 返回 nil 表示通过。
//
// 令牌会经下游流转（FencedSink），见过的组件拿着令牌来冒充持有者时，
// 被这道防线拦下——只比令牌拦不住这个形态。
func effectHolderErr(holder, presented, key, resource string) error {
	if holder == presented {
		return nil
	}
	return oerrors.Wrap(sideeffect.ErrNotHolder,
		"sqlitestore: 凭据被拒：持有者身份不匹配（令牌不是本次授予铸造的那份）",
		"key", key, "resource", resource)
}

// effectFenceErr 三项联查的第一项：令牌仍是最新代，且本次持有仍在。
//
// stored = 存储侧记着的当前代次；presented = 出示的那一代；
// alive = 持有是否仍在（闸未释放 / 意图未撤销）。
//
// 过期即资格终止（与 taskboard.Renew 同一口径）：租约已过期而无人接管时，
// 旧持有者不得复活它——否则那次尝试绕开「先对账」直接继续推进。
func effectFenceErr(stored, presented sideeffect.Fence, alive bool, key, resource string) error {
	if alive && presented != 0 && stored == presented {
		return nil
	}
	return oerrors.Wrap(sideeffect.ErrFenceStale,
		"sqlitestore: 凭据被拒：本次持有已失效（令牌不是最新代或资源已释放）",
		"key", key, "resource", resource)
}

// effectResourceErr 三项联查的第三项：租约的资源必须与动作的资源一致。
//
// A 资源的租约被拿去做 B 资源的操作时，两个 Resource 各说各话——不拦下的
// 话，后面按 act.Resource 去查记录、按 lease.Fence 去比令牌，两份真相拼出
// 一个错误的「合规」。
func effectResourceErr(act sideeffect.Action, lease sideeffect.Lease) error {
	if lease.Resource == act.Resource {
		return nil
	}
	return oerrors.Wrap(sideeffect.ErrResourceMismatch,
		"sqlitestore: 凭据被拒：租约与动作的资源不一致",
		"act_resource", act.Resource, "lease_resource", lease.Resource)
}

// effectHoldOwnerErr 资源闸侧的三项联查。
func effectHoldOwnerErr(act sideeffect.Action, lease sideeffect.Lease, rr *resEntry) error {
	if err := effectResourceErr(act, lease); err != nil {
		return err
	}
	if err := effectFenceErr(rr.Fence, lease.Fence, rr.Holder != "", string(act.Key), act.Resource); err != nil {
		return err
	}
	return effectHolderErr(rr.Holder, lease.Holder, string(act.Key), act.Resource)
}

// effectIntentOwnerErr 执行意图侧的三项联查（本次尝试是否仍归这份凭据所有）。
func effectIntentOwnerErr(act sideeffect.Action, lease sideeffect.Lease, cur *actEntry) error {
	if err := effectResourceErr(act, lease); err != nil {
		return err
	}
	var stored sideeffect.Fence
	if cur.Intent != nil {
		stored = cur.Intent.Fence
	}
	if err := effectFenceErr(stored, lease.Fence, cur.Intent != nil, string(act.Key), act.Resource); err != nil {
		return err
	}
	return effectHolderErr(cur.Intent.Holder, lease.Holder, string(act.Key), act.Resource)
}

// effectDropIntent 撤销一条执行意图，并把它自己的资源闸一并作废。
//
// 作废资源闸的理由：意图与资源持有是同一次尝试的两个投影，同刻到期。
// 意图过期时那份持有也过期了，但**记录还在**——不清掉的话，原持有者拿着
// 同一枚令牌仍能 Renew/Complete（栅栏只在接管时递增，没人接管时令牌没变）。
func effectDropIntent(cur *actEntry, rr *resEntry, act sideeffect.Action) {
	it := cur.Intent
	cur.Intent = nil
	if it == nil || it.Resource == "" || it.Resource != act.Resource {
		return
	}
	if rr.Holder == it.Holder && rr.Fence == it.Fence {
		rr.Holder = ""
	}
}

// stageEffect 暂存副作用面的一次变更（Key 级与资源级可只涉及其中一级）。
//
// 两者在**同一条记录**里 ⇒ 「一次原子操作里同时决定」在落库形态上成立。
func stageEffect(tx *txCtx, act sideeffect.Action, cur *actEntry, curRes *resEntry) error {
	if cur == nil && curRes == nil {
		return nil
	}
	rec := &effectRecord{Tenant: act.Tenant, Key: string(act.Key)}
	if cur != nil {
		rec.Act = cur
	}
	if curRes != nil {
		rec.Res = curRes
		rec.ResKey = act.Resource
	}
	return tx.stage(journalRecord{Kind: kindEffect, Effect: rec})
}

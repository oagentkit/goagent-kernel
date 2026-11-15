package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/eventbus"
)

// RecoverBootOrphans 启动孤儿对账（一次性扫描；宿主在进程启动时调一次）。
//
// 语义与免检条件见包头「三路判定的语义差异」与 OrphanAtBoot：owner=本副本/
// 空 + running + 最后事件早于启动锚点（减 BootGrace）⇒ 定义性孤儿，立即收，
// 无须等 Stall 事件静默。他副本名下的会话**不在本路**（归 RecoverInterrupted
// 的租约/静默判定管）。
//
// 两个关闭态：BootGrace < 0（conf 显式关闭）与 Boot 零值（无启动事实，防
// 零值锚点误收全量）——都直接返回，不碰存储。
//
// 返回 (收敛数, 聚合错误)：单会话的读事实失败/收敛失败**不中止整轮扫描**
// （一个会话的问题不该让其余孤儿滞留），错误经 errors.Append 聚合带出，
// 由宿主留痕；ListRunning 失败则整轮无从扫起，立即返回。
func (r *Reconciler) RecoverBootOrphans(ctx context.Context) (int, error) {
	if r.cfg.BootGrace < 0 {
		return 0, nil // conf 显式关闭：回落常规恢复循环（事件静默路径）
	}
	if r.cfg.Boot.IsZero() {
		return 0, nil // 无启动锚点（零值构造宿主）：防零锚点误收全量
	}
	// 扫描面提示收窄到本实例（含无主行，见 Store 契约）；判定仍按 Owner 复核。
	sessions, err := r.store.ListRunning(ctx, r.cfg.InstanceID)
	if err != nil {
		return 0, errors.Wrap(err, "列 running 会话失败")
	}
	var errs error
	n := 0
	for i := range sessions {
		s := sessions[i]
		if s.Owner != "" && s.Owner != r.cfg.InstanceID {
			continue // 他副本名下：归 RecoverInterrupted 心跳/静默判定管
		}
		last, err := r.store.LastActivity(ctx, s.ID)
		if err != nil {
			// 读不到事实 = 不可判：跳过不收（宁可留给常规路兜底，不误收）。
			errs = errors.Append(errs, errors.Wrap(err, "读最后事件时刻失败", "session", s.ID))
			continue
		}
		if last.IsZero() {
			last = s.CreatedAt // 无事件（首个事件落库前即中断）：以创建时间锚定
		}
		if !OrphanAtBoot(last, r.cfg.Boot, r.cfg.BootGrace) {
			continue // 锚点后仍有事件 = 本进程/同标识存活进程在推进，免检
		}
		cause := Cause{
			By: ByBootReconcile,
			Reason: fmt.Sprintf("编排中断（发版重启孤儿，启动对账回收；最后事件 %s，本进程启动 %s）",
				last.Format(time.RFC3339), r.cfg.Boot.Format(time.RFC3339)),
		}
		ok, err := r.reclaim(ctx, s, cause)
		if err != nil {
			errs = errors.Append(errs, err)
			continue
		}
		if ok {
			n++
		}
	}
	return n, errs
}

// RecoverInterrupted running 搁浅回收（周期扫描；宿主按自己的节奏循环调）。
//
// 语义取 deny by default：**不做步骤级重放**（重放会产生重复环节与二次审批，
// 执行段可能有副作用），收敛为 failed 并写明恢复路径。判定分两路（见包头）：
//
//   - owner=本副本/空：事件静默（Stall）——本副本不适用心跳免检，自副本走
//     静默恰是重启孤儿的安全网；
//   - owner=他副本：先看心跳租约（JudgeLease）——租约过期立即回收、心跳新鲜
//     免检、无心跳行回落事件静默（DeadOwner 阈值）。
//
// 心跳面读失败**降级不停车**：全体走事件静默判定（恢复不因观测面抖动停摆），
// 错误随返回值带出留痕。LeaseTTL < 0（conf 关闭心跳面）则根本不读 ListBeats。
func (r *Reconciler) RecoverInterrupted(ctx context.Context) (int, error) {
	sessions, err := r.store.ListRunning(ctx, "")
	if err != nil {
		return 0, errors.Wrap(err, "列 running 会话失败")
	}
	var beats map[string]time.Time
	var errs error
	if r.lease > 0 {
		beats, err = r.store.ListBeats(ctx)
		if err != nil {
			errs = errors.Append(errs, errors.Wrap(err, "读心跳租约表失败（降级为事件静默判定）"))
			beats = nil
		}
	}
	now := r.now()
	n := 0
	for i := range sessions {
		s := sessions[i]
		mine := s.Owner == "" || s.Owner == r.cfg.InstanceID
		threshold := r.stall
		if !mine {
			threshold = r.deadOwner
		}
		// 心跳租约判定（仅他副本）：Live=免检；Expired=跳过静默直接回收；
		// NoRow=回落下面的事件静默。
		leaseDead := false
		if !mine && beats != nil {
			switch JudgeLease(beats, s.Owner, now, r.lease, GraceWindow) {
			case LeaseLive:
				continue // 心跳新鲜或租约刚过期（重启重打窗口内）：活编排名下免检
			case LeaseExpired:
				leaseDead = true
			case LeaseNoRow:
				// 无心跳行（存量副本/心跳面降级）：回落事件静默。
			}
		}
		if !leaseDead {
			last, err := r.store.LastActivity(ctx, s.ID)
			if err != nil {
				errs = errors.Append(errs, errors.Wrap(err, "读最后事件时刻失败", "session", s.ID))
				continue
			}
			if last.IsZero() {
				last = s.CreatedAt
			}
			if !Stalled(last, now, threshold) {
				continue // 未超阈：活的
			}
		}
		cause := Cause{By: ByRecovery, Reason: fmt.Sprintf(
			"编排中断（进程重启或副本切换，事件静默超过 %s）", threshold)}
		if leaseDead {
			cause.By = ByDeadLease
			cause.Reason = fmt.Sprintf("编排中断（Owner 副本心跳丢失超过 %s）", r.lease+GraceWindow)
		} else if !mine {
			cause.By = ByDeadOwner
		}
		ok, err := r.reclaim(ctx, s, cause)
		if err != nil {
			errs = errors.Append(errs, err)
			continue
		}
		if ok {
			n++
		}
	}
	return n, errs
}

// reclaim 收敛体（三路共用，动作序固定）：
//
//	ConvergeCAS（failed 终态 + 恢复说明报告 + 排队结算 + 悬挂环节收口，原子）
//	  → CloseBracket（turn 括号关闭，closed_by = Cause.By）
//	  → Pub(session_finished)
//
// CAS 返回 false（他方已处理）或 error 时**后续动作一律不发生**——收尾动作
// 只属于赢下条件迁移的那一次，双写轮括号/事件比不写更伤审计。
func (r *Reconciler) reclaim(ctx context.Context, s Session, cause Cause) (bool, error) {
	ok, err := r.store.ConvergeCAS(ctx, s.ID, contract.StatusRunning, contract.StatusFailed, cause)
	if err != nil {
		return false, errors.Wrap(err, "条件收敛失败", "session", s.ID)
	}
	if !ok {
		return false, nil // 他方已处理（条件迁移零行）
	}
	r.store.CloseBracket(ctx, s.ID, cause.By)
	r.pub(s.ID, s.CaseID, nil, eventbus.EventSessionFinished, "error", map[string]any{
		"error": "编排中断恢复：会话按失败收敛（by=" + cause.By + "）",
		"by":    cause.By,
	})
	return true, nil
}

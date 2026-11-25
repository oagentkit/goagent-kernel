// claimready.go —— taskboard.ReadyBoard 的适配器实现（issue #189）。
//
// # 判定仍只有一份
//
// 批量领取不另写认领状态机：候选挑选按一份 List 快照做，每一笔认领都走
// BoardAdapter.Claim（原子读-判-写、令牌与代次铸造、过期接管、依赖判定
// 全部复用）。批量只是「一次快照挑多个候选 + 逐个提交」，输在竞争的那一笔
// 跳过、继续下一笔——与单条 Claim 的竞争性拒绝同一条处置。
//
// # 为什么仍读全量快照
//
// 通用 Backend 原语没有「就绪索引」这一面（四个原语是快照/条件事务/
// 前缀扫描/列键），所以参考实现只能从全量视图挑候选。批量化的收益是
// **把每拍的认领次数从 1 提到 n**（列表调用频率随之下降 n 倍）；真正
// 按索引领取（列表量不随任务总量放大）要由宿主后端自己实现 ReadyBoard
// ——契约在 taskboard，适配器只是可用的参考形态。
package coord

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// 编译期契约断言：适配器同时满足单条认领与批量领取两面。
var _ taskboard.ReadyBoard = (*BoardAdapter)(nil)

// 单次批量领取的候选上限保护：n 由调用方给，契约不设上限；这里只防御
// 极端值导致的巨大预分配（候选本身仍以快照为准）。
const claimReadyPreallocCap = 64

// ClaimReady 实现 taskboard.ReadyBoard：一次快照挑出就绪候选，按任务 id
// 序逐个认领，直到领满 n 个或候选耗尽。
//
// 竞争性拒绝（ErrTaken / ErrBlocked / ErrTerminal / ErrNotFound）跳过并
// 继续下一笔；其它错误中止，**已领到的任务仍随返回值交给调用方**（错误
// 只说明本轮没取满）。
func (a *BoardAdapter) ClaimReady(ctx context.Context, agent taskboard.AgentID, n int, req taskboard.ClaimRequest) ([]taskboard.Task, error) {
	if n <= 0 {
		return nil, errors.Wrap(taskboard.ErrInvalidBatch,
			"coord: 批量领取数量必须为正", "n", n)
	}
	tasks, err := a.List(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "coord: ClaimReady 取任务快照失败")
	}
	now, err := a.nowAt(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "coord: ClaimReady 取存储时钟失败")
	}
	cands := readyCandidates(tasks, now, n)
	out := make([]taskboard.Task, 0, min(n, len(cands)))
	for _, cand := range cands {
		claimed, cerr := a.Claim(ctx, cand.ID, agent, req)
		if cerr != nil {
			switch {
			case errors.Is(cerr, taskboard.ErrTaken),
				errors.Is(cerr, taskboard.ErrBlocked),
				errors.Is(cerr, taskboard.ErrTerminal),
				errors.Is(cerr, taskboard.ErrNotFound):
				// 竞争性拒绝：这一笔没轮上，下一笔继续（不是批量失败）。
				continue
			default:
				return out, errors.Wrap(cerr, "coord: 批量认领中止", "task", string(cand.ID))
			}
		}
		out = append(out, claimed)
		if len(out) == n {
			break
		}
	}
	return out, nil
}

// readyCandidates 从一致快照里挑就绪候选（按任务 id 排序，确定性）。
//
// 就绪 = 状态 open / failed（可重做），或 claimed 但租约已过期（可接管）；
// 且依赖全部完成。被活着持有的任务不返回——除非它的租约已过期，此时
// Claim 会按接管语义铸新令牌新代次。limit 只用于预分配上限，不裁剪结果
// （候选耗尽前可能因竞争跳过若干笔，需要多备候选）。
func readyCandidates(tasks []taskboard.Task, now time.Time, limit int) []taskboard.Task {
	done := make(map[taskboard.TaskID]bool, len(tasks))
	for _, t := range tasks {
		done[t.ID] = t.State.Terminal()
	}
	out := make([]taskboard.Task, 0, min(limit, len(tasks), claimReadyPreallocCap))
	for _, t := range tasks {
		switch t.State {
		case taskboard.StateOpen, taskboard.StateFailed:
			// 可认领（含失败重做）。
		case taskboard.StateClaimed:
			if taskboard.LeaseHeld(t, now) {
				continue // 活着持有：不是候选
			}
		default:
			continue // 终态或未知状态：不碰
		}
		ready := true
		for _, d := range t.Deps {
			if !done[d] {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(x, y taskboard.Task) int {
		return strings.Compare(string(x.ID), string(y.ID))
	})
	return out
}

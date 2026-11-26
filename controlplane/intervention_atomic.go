// intervention_atomic.go —— 权威介入的**事务原子**接缝（P0-A，任务侧）。
//
// # 补的缺口
//
// forceReleaseAuthoritative 的默认形态是两步：先 p.tasks.Release（租约当场
// 作废），成功后 appendAudit（审计随后落账）。两步之间死掉或留痕失败 ⇒
// 「租约已释放、方法返回错误、本次介入缺成功审计」——违背本仓「介入留痕
// 与状态同事务」的既定纪律（非权威路径由 Store.Commit* 原子兑现，唯独
// 权威路径此前没有这个接缝）。
//
// # 契约
//
// 实现方（宿主存储）必须把「校验并释放 + 写审计行」收敛进**同一事务**：
//
//   - 审计写失败 ⇒ 释放一并回滚，状态一字不动（与 Store.Commit* 同一条
//     纪律：「介入返回 error ⇒ 可观测状态未变」）；
//   - 令牌不匹配 ⇒ taskboard.ErrLeaseLost，事务内不产生审计行（拒绝不是
//     介入，不留痕——与非权威路径「被拒路径留痕直接追加」区分）；
//   - entry.At 为零值时由实现侧补自己的当前时刻（与 AuditEntry 契约一致：
//     介入方的时钟不可信）；
//   - 审计行落在**控制面自己的存储**里（同事务可写），不经外部审计平台——
//     外部平台经 Outbox 异步消费，不承担事务一致性职责。
//
// StorePlane 探测到 TaskAuthority 同时实现本接口即走原子路径；未实现则
// 保留两步形态（窗口如实写在 authority.go 的调用点上），不静默假装原子。
package controlplane

import (
	"context"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// ForceReleaseAtomic 权威任务板可选的原子强释接缝。
//
// 生产实现用运行时同一个 taskboard.Board 背后的存储，在同一事务里执行
// 「凭令牌强制释放 + 追加审计行」。参考实现见 examples/pgstore 的
// BoardStore.ForceReleaseWithAudit。
type ForceReleaseAtomic interface {
	TaskAuthority
	// ForceReleaseWithAudit 在同一事务里：校验令牌并强制释放任务 + 追加
	// 审计行。任一失败 ⇒ 两者都不生效（状态一字不动）。
	ForceReleaseWithAudit(ctx context.Context, id taskboard.TaskID, token string, entry AuditEntry) error
}

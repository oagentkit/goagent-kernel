// ready.go —— 批量领取的可选契约（issue #189）。
//
// # 它补的是哪一段
//
// Board.Claim 一次只认领一个任务；worker.Loop 的缺省路径是「每拍 List 全量
// → 本地挑一个 → Claim」。单副本时这不是问题，多副本时列表量随队伍线性
// 放大：N 个 worker 每 200ms 各拉一次全量任务，即使一个都没得领。
//
// ReadyBoard 把「挑就绪候选」这一步挪到存储侧：存储按自己的就绪索引
// （open/failed + 依赖完成 + 未被活着占用）一次领取 n 个，调用方不再需要
// 全量 List。它是**可选**接口——不实现它的 Board 保持原有单条 Claim 路径，
// 语义与默认行为一字不改（本仓的默认路径零回归纪律）。
//
// # 与 Board.Claim 的关系
//
// 批量领取不是第二套状态机：每一笔认领仍然走 Claim 的全部判据（原子
// 读-判-写、令牌由存储铸造、代次严格递增、过期租约可接管、依赖完成才
// 可认领）。实现可以在一次事务里提交整批，也可以逐笔调用 Claim；契约只
// 判结果：
//
//   - 返回的每个任务都是**本次**认领成功的（令牌与代次由存储铸造）；
//   - 同一批里任务 id 不重复；
//   - 并发调用之间同一任务至多归一个调用方（原子性仍由存储保证）；
//   - 数量至多 n；就绪任务不足 n 时返回实际领到的，不报错（「没那么多活」
//     是正常结论，不是故障）。
//
// # 单条 Claim 保持默认
//
// 本接口不改 Board，也不强制实现。worker.Loop 的缺省领取路径仍走
// List + Claim；要启用批量领取的宿主把 Board 构造成 ReadyBoard 并自行
// 接线（或在 Loop 之外驱动）。这样「未实现批量领取」的存储不会被新契约
// 拖红，已实现的存储可以在自己的量级上做索引优化。
package taskboard

import (
	"context"

	"github.com/oekit/pkg/errors"
)

// ErrInvalidBatch 批量领取的数量非法（n <= 0）。
//
// 零或负数的批量请求无从解释：零个任务等于什么都没做，负数不是数量。
// 装配/调用方错误要显式拒绝，不静默返回空列表（那会让「配置写错」看起来
// 像「暂时没有活」）。
var ErrInvalidBatch = errors.New("taskboard: 批量领取数量必须为正")

// ReadyBoard 可选的批量领取面（存储侧按就绪索引领取）。
//
// 实现方必须保证（与 Board.Claim 同一套判据，不另造一套）：
//
//   - 返回的任务全部是本次认领成功的，令牌与代次由存储铸造；
//   - 同一批内任务 id 不重复；并发调用之间同一任务至多一个赢家；
//   - 只返回**就绪**任务：状态为 open 或 failed（可重做），依赖全部完成；
//     被活着持有的任务不得返回；过期租约可以按 Claim 的接管语义领取
//     （返回的令牌是新铸的，旧持有者的迟到提交凭 ErrLeaseLost 被拒）；
//   - 数量至多 n；就绪任务不足时返回实际领到的（可以为空），不报
//     ErrBlocked / ErrTaken——那些是「这一笔没轮上」，不是批量领取失败。
//
// 单个候选的竞争性拒绝（ErrTaken / ErrBlocked / ErrTerminal / ErrNotFound）
// 会被实现跳过并继续尝试下一个候选；基础设施故障则应中止并返回错误。
// **已返回的任务仍然归调用方**（调用方必须按持有者收场），错误只表示
// 本轮批量没有取满 n 个。
//
// 接缝契约（宿主实现本接口；调用方在批量领取路径上**同步**调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：**宿主侧兑现**。内核的调用点**不持任何内核锁**：宿主实现不得
// 在 ClaimReady 内回调本包的其它接缝——内核正同步等着它返回，回查即互相
// 等待。
// no-blocking：**宿主侧兑现**。ClaimReady 落在任务板的持久化路径上，被批量
// 领取路径**同步**等待：「不阻塞」在此处的准确含义是「**有界**」——必须
// 承接 ctx 的取消/超时并在期限内返回，不得无限等待、不得在持存储内部锁时
// 等待别的回调。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本接口的 panic（本包无
// recover），宿主实现须自行保证不 panic 或在实现内部 recover 成 error。
type ReadyBoard interface {
	Board
	// ClaimReady 批量领取至多 n 个就绪任务给 agent。
	//
	// n <= 0 ⇒ ErrInvalidBatch。req 的 TTL 语义与 Board.Claim 完全一致
	// （正数按存储时钟推算，零值不限期由宿主自担）。
	ClaimReady(ctx context.Context, agent AgentID, n int, req ClaimRequest) ([]Task, error)
}

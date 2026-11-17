// managed_ports.go —— 受管执行器的**宿主侧接缝面**（三张窄契约）。
//
// 从 managed.go 拆出的理由：这三张窄契约是**宿主实现、内核调用**的接缝，
// 各自带接缝契约注释（issue #370 形态 B）；与编排逻辑（执行四步与处置
// 分流）放同一文件，编排的判据顺序会被回调契约淹没。按职责分文件，不按
// 行数分（与 worker/loop.go ↔ worker/execute.go 同款切法）。
package worker

import (
	"context"

	"github.com/yi-nology/goagent-kernel/checkpoint"
	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// EffectRunner 受管执行器依赖的副作用编排面（*sideeffect.Runner 满足）。
//
// 收成接口而不是直接收 *sideeffect.Runner：执行器的用例要能精确构造
// 「Applied / Replayed / Contended / Uncertain」四种结论，而这两种结论的
// 真实构造需要完整的存储替身；接口让编排判据与存储实现解耦（生产构造
// 仍只接受 *sideeffect.Runner 的实现，见 NewManagedExecutor）。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：执行器在**不持内核锁**的位置调用 ExecuteWith / Reconcile。
// no-blocking：宿主实现内不得无界等待——调用跑在租约会话的 ctx 上（租约
// 易主即取消），内核不设执行超时。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（managed.go
// 无 recover），宿主实现须自行保证不 panic。
type EffectRunner interface {
	// ExecuteWith 抢闸 → 准备 → 持有资格检查 → 施加 → 记账（语义见
	// sideeffect.Runner.ExecuteWith）。
	ExecuteWith(ctx context.Context, act sideeffect.Action, eff sideeffect.Effect) (sideeffect.Outcome, sideeffect.Result, error)
	// Reconcile 对账裁决结果未知的 Key（语义见 sideeffect.Runner.Reconcile）。
	Reconcile(ctx context.Context, act sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error)
}

// Progress 进度快照的提交面（*checkpoint.Committer 满足）。
//
// 只收 Commit 一个方法：单调判定（SaveIfNewer + 进程内闸）在 Committer /
// StoreCAS 里，本执行器不重写。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：进度提交在**不持内核锁**的位置被调用（提交前已做资格确认
// AssertOwned）。
// no-blocking：宿主实现内不得无界等待——提交跑在会话 ctx 上，阻塞会推迟
// 收口；收口前的资格确认被拒即本次结果作废。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（managed.go
// 无 recover），宿主实现须自行保证不 panic。
type Progress interface {
	Commit(ctx context.Context, s checkpoint.Snapshot) error
}

// LeasedProgress 带**持有凭据**的进度提交面（*checkpoint.Committer 满足，
// 经 CommitFenced）。
//
// 进度实现了它时，执行器把本次认领的接管代次（@@LeaseSession.Fence@@）
// 一并交给存储：陈旧代次的迟到提交由**存储侧**拒绝（check-then-act 的
// 窗口靠这个关，调用前的资格确认关不住它，见 checkpoint.FencedStoreCAS）。
// 未实现时退回普通 Commit——资格确认仍在（提交前 AssertOwned），但
// 「确认通过之后、写入落地之前」的窗口只剩 TTL 量级的兜底，宿主要严格
// 一致的状态提交必须装配支持栅栏判据的存储。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：带代次的条件提交在**不持内核锁**的位置被调用。
// no-blocking：宿主实现内不得无界等待——提交跑在会话 ctx 上，阻塞会推迟
// 收口；陈旧代次被拒（ErrStaleFence）同样按失格处置如实返回。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（managed.go
// 无 recover），宿主实现须自行保证不 panic。
type LeasedProgress interface {
	// CommitFenced 带接管代次的条件提交（语义见 checkpoint.FencedStoreCAS）。
	CommitFenced(ctx context.Context, s checkpoint.Snapshot, fence uint64) error
}

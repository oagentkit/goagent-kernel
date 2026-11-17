// audit.go —— 介入留痕契约与进程内参考实现。
//
// 留痕是介入的硬前提，不是事后增强：没有留痕的介入无法回答「谁在什么时候
// 依据什么改了运行状态」，出问题时与「存储被绕过直接改」无法区分。
// 故契约层面 Auditor 缺位 ⇒ 介入一律拒绝（ErrNoAuditor），实现层面
// MemAuditor 对不完整的留痕拒收——两层各挡一个方向。
package controlplane

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"
)

// Action 介入/查询动作种类（留痕的 Action 字段；httpapi 鉴权钩子的判定对象）。
//
// # 两套口径（issue #372）
//
//   - **查询动作**：ActionQuery —— 不改变任何状态，**不经留痕**；
//   - **介入动作**：会改变状态的动作（强制释放 / 裁决 / 终止），词表见
//     AllInterventionActions()。
//
// 留痕面只接受**介入动作**词表内的值：词表外的动作名一律拒收
// （ErrInvalidAudit）——留痕是审计的唯一凭据，「谁做了什么」那一列出现
// 内核不认的值时，没有任何判据会红；而 ActionQuery 被留痕面拒收不是
// 意外，是「查询不进留痕账」这条口径的必然结果（契约文本与实现此前
// 说的不是一回事，本注释把它说清）。
type Action string

const (
	// ActionQuery 查询（运行状态视图 / 不变量视图）。**查询动作不经留痕**：
	// 它不改变任何状态，留痕面只接受 AllInterventionActions() 内的值。
	ActionQuery Action = "query"
	// ActionForceRelease 强制释放。
	ActionForceRelease Action = "force_release"
	// ActionAdjudicate 裁决未知结果。
	ActionAdjudicate Action = "adjudicate"
	// ActionTerminate 终止运行。
	ActionTerminate Action = "terminate"
)

// AuditEntry 一条人工介入留痕：谁、什么时候、做了什么、对谁、依据什么、结果如何。
type AuditEntry struct {
	// At 留痕时刻。由**留痕实现侧**补（实现收到零值时取自己的当前时刻）——
	// 介入方的时钟不可信，留痕时刻以实现侧为准。
	At time.Time
	// Operator 谁在介入（必填）。
	Operator string
	// Action 动作种类（必填）。
	//
	// 两套口径：**查询动作（ActionQuery）不经留痕**；留痕面只接受
	// AllInterventionActions() 内的值——词表外的动作名同样被拒
	// （ErrInvalidAudit）。
	Action Action
	// Target 目标（任务 id / 效果键 / run id，必填）。
	Target string
	// Basis 依据什么结论（必填）。
	Basis string
	// Detail 结果摘要（可空：成功 / 被拒原因）。
	Detail string
}

// ErrInvalidAudit 留痕不完整（谁 / 动作 / 目标 / 依据缺一不可）。
// MemAuditor 对不完整的留痕拒收；宿主实现应复用同一判据。
var ErrInvalidAudit = errors.New("controlplane: 留痕不完整——谁/动作/目标/依据缺一不可")

// ErrAuditorPanic 留痕实现 panic 被内核隔离（issue #327）：状态一字不动，
// 错误上抛给介入方。宿主留痕面（DB 落盘/审计服务）的异常不得把一次
// 「尚未提交」的介入变成进程级事故，也不得留下半条状态。
var ErrAuditorPanic = errors.New("controlplane: 留痕实现 panic（已隔离，状态未变）")

// appendAudit 调宿主留痕面并隔离 panic（issue #327 的统一接缝）。
//
// 调用点一律在状态锁之外：MemStore 在 commitMu（写者串行）下、m.mu 外调用；
// StorePlane 的拒绝路径本来就无锁。宿主 Auditor 可以安全回查 Snapshot*
// （查询面不经 commitMu），但**不得回调 Commit***——那会重入 commitMu。
func appendAudit(ctx context.Context, aud Auditor, e AuditEntry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Wrap(ErrAuditorPanic, "留痕实现 panic")
		}
	}()
	return aud.Append(ctx, e)
}

// Auditor 留痕契约：**只增不改**（append-only）。
//
// 实现方必须保证：
//
//   - Append 成功后条目永不修改、永不删除（留痕可被追加，不可被改写——
//     可改写的留痕不是留痕）；
//   - 收到零值 At 时补实现侧当前时刻；
//   - 对缺 Operator / Action / Target / Basis 的条目拒绝（ErrInvalidAudit），
//     不静默补空；
//   - **词表外的动作同样拒绝**（ErrInvalidAudit）：留痕面只接受
//     AllInterventionActions() 内的值（查询动作 ActionQuery 不经留痕，
//     同样被拒）。
//
// # 调用面（issue #327）
//
// Append 在**状态锁之外**被调用：MemStore 在 commitMu（写者串行）下、m.mu 外
// 调用；StorePlane 的拒绝路径本来就无锁。宿主实现因此可以安全回查 MemStore
// 的 Snapshot*（查询面不经 commitMu），但有两条硬约束：
//
//   - **不得回调 Commit***：会重入 commitMu（同 goroutine 死锁）；
//   - **不得阻塞**：提交序列串行等待它（查询面不受影响，但写者会排队）。
//
// 实现 panic 由内核隔离为上抛的 ErrAuditorPanic，状态一字不动。
//
// 持久化形态归宿主（表 / 文件 / 只读副本皆可）；MemAuditor 是进程内参考实现。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回调本包的**写面**（不得回调 Commit*）——成功
// 路径在 commitMu 内调本接口，重入即同 goroutine 死锁；回查 Snapshot* 是
// 安全的（查询面不经 commitMu）。
// no-blocking：宿主实现内不得无界等待——appendAudit 在 commitMu 内被调用
// （写者串行），阻塞会让全部提交者排队。
// panic-isolated：**内核侧兑现**——appendAudit 用 recover 隔离为上抛的
// ErrAuditorPanic，状态一字不动（issue #327）。
type Auditor interface {
	// Append 追加一条介入留痕。
	Append(ctx context.Context, e AuditEntry) error
	// Entries 按目标取留痕（查询面）。target 为空取全部；按追加序返回。
	Entries(ctx context.Context, target string) ([]AuditEntry, error)
}

// MemAuditor 进程内参考留痕面。零值可用，并发安全。
type MemAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// Append 追加留痕（先判完整性与动作词表，再补时刻，再落账）。
func (a *MemAuditor) Append(_ context.Context, e AuditEntry) error {
	if e.Operator == "" || e.Target == "" || e.Basis == "" || !IsKnownInterventionAction(e.Action) {
		return ErrInvalidAudit
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

// Entries 按目标取留痕（target 空 = 全部；按追加序）。
// 返回的是拷贝：调用方改写条目不影响留痕面（只增不改的可观测面同样只读）。
func (a *MemAuditor) Entries(_ context.Context, target string) ([]AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, 0, len(a.entries))
	for _, e := range a.entries {
		if target == "" || e.Target == target {
			out = append(out, e)
		}
	}
	return out, nil
}

// interventionActions 介入动作的词表（**单一事实源**：判定与枚举都读它）。
//
// 与 Action* 常量集只差一条：ActionQuery 是**查询**动作，不改变状态，
// 不进留痕账（issue #372）。此前这份词表是私有的、且与契约文本指向的
// 「Action* 常量」不是同一份——照契约实现的宿主会多收一类、少拒一类。
var interventionActions = []Action{ActionForceRelease, ActionAdjudicate, ActionTerminate}

// AllInterventionActions 返回**介入动作**词表的全集（排序后的**新切片**）。
//
// 宿主据此校验自己写的留痕面，不必手抄常量清单——手抄的清单必然漂移：
// 内核新增一个介入动作，宿主那份不会跟着变，且没有任何判据会红。
// 返回的是**副本**：调用方改写它不影响判定面。
func AllInterventionActions() []Action {
	out := make([]Action, len(interventionActions))
	copy(out, interventionActions)
	slices.Sort(out)
	return out
}

// IsKnownInterventionAction 报告动作是否在介入动作词表内。
//
// 留痕面（MemAuditor.Append）与宿主的留痕实现都走这一个判据：词表外的
// 动作名进得了留痕账，「谁做了什么」那一列就出现了内核不认的值，而
// 没有任何判据会红。
func IsKnownInterventionAction(a Action) bool {
	return slices.Contains(interventionActions, a)
}

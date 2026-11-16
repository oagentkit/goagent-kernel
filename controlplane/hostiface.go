// hostiface.go —— 宿主自有类型接入的窄契约族（issue #238）。
//
// [Plane] 是 taskboard 原生的便利契约：任务 ID 是 taskboard.TaskID、视图里是
// taskboard.Task / taskboard.Problem、租户上下文是 tenant.Context。宿主若已有
// 自己的任务 ID / 运行 ID / 快照 / 租户类型，不该为了接入控制面先造一份
// taskboard 副本——本文件给出与 taskboard **解耦**的窄契约：
//
//   - ID 用类型参数表达（任何 comparable 类型，string / 整型 / 自定义结构体均可）；
//   - 任务快照与不变量条目的类型由宿主决定（TaskViewOf[T, P] / RunViewOf）；
//   - 租户上下文不进方法签名（它经 context.Context 传递，载体由宿主决定）。
//
// # 零实现纪律
//
// 本文件**只有声明**，不提供任何适配器或默认实现：接入由宿主完成。
// 参考实现 MemPlane 仍实现 taskboard 原生的 [Plane]，供演练与测试使用。
// 窄契约与原生契约是并列的两条接缝，不是「旧接口的包装层」——包一层
// 适配器会重新引入它本想拆掉的那份类型耦合。
//
// # 与原生契约的分工
//
//	宿主已有 taskboard.Board / tenant.Context   → 实现 Plane（任务板与租户面已就位）
//	宿主自有任务模型 / 租户体系                  → 实现 HostPlane + AuthorizerOf
//
// 两条契约的**判定纪律相同**（介入走运行时同一套令牌 / 代次 / 栅栏，留痕
// 缺位即拒绝，介入者与依据必填）；窄契约只换类型，不换判据。
package controlplane

import (
	"context"
	"net/http"
)

// TaskViewOf 任务视图的宿主类型版本：任务快照 T 与不变量条目 P 由宿主决定。
type TaskViewOf[T any, P any] struct {
	// Task 任务快照（与运行时读到的同一份）。
	Task T
	// Problems 该任务身上的不变量违反（空 = 干净）。
	Problems []P
}

// RunViewOf 运行视图的宿主类型版本。
type RunViewOf[RID comparable, T any, P any] struct {
	// RunID 运行标识（宿主的类型）。
	RunID RID
	// Status 运行状态（词表归宿主；与 contract 对齐与否由宿主决定）。
	Status string
	// Gen 运行代次：Terminate 的乐观并发判据（ExpectedGen 必须等于它）。
	Gen uint64
	// Tasks 该 run 的全部任务快照（实现应按任务 id 排序，确定性输出）。
	Tasks []T
	// Problems 全 run 的不变量视图（宿主自己的判据产出）。
	Problems []P
}

// ForceReleaseOf 强制释放任务认领（目标 ID 用宿主的类型）。
//
// 契约与 [ForceRelease] 相同：不是绕过闸，是经闸换持有者（旧令牌当场作废、
// 下一次认领铸造严格更大的代次）。
type ForceReleaseOf[ID comparable] struct {
	// TaskID 要释放的任务（宿主类型）。
	TaskID ID
	// Operator 谁在介入（留痕必填）。
	Operator string
	// Basis 依据什么结论（留痕必填，宿主词表）。
	Basis string
}

// Validate 留痕完整性判定（目标按宿主 ID 类型的零值判空）。
func (r ForceReleaseOf[ID]) Validate() error {
	var zero ID
	return validateInterventionTarget(r.TaskID != zero, r.Operator, r.Basis)
}

// TerminationOf 终止运行（运行 ID 用宿主的类型）。
//
// 契约与 [Termination] 相同：终止凭运行代次（ExpectedGen 必须等于当前
// RunView.Gen），终止本身是一次代次前进。
type TerminationOf[RID comparable] struct {
	// RunID 要终止的运行（宿主类型）。
	RunID RID
	// ExpectedGen 介入方视图里的运行代次（来自 RunViewOf.Gen）。
	ExpectedGen uint64
	// Operator 谁在介入（留痕必填）。
	Operator string
	// Basis 依据什么结论（留痕必填）。
	Basis string
}

// Validate 留痕完整性判定（目标按宿主 ID 类型的零值判空）。
func (r TerminationOf[RID]) Validate() error {
	var zero RID
	return validateInterventionTarget(r.RunID != zero, r.Operator, r.Basis)
}

// TaskQuerier 任务视图查询的窄契约：宿主自有任务 ID 与快照 / 问题类型。
type TaskQuerier[ID comparable, T any, P any] interface {
	TaskView(ctx context.Context, id ID) (TaskViewOf[T, P], error)
}

// RunQuerier 运行视图查询的窄契约。
type RunQuerier[RID comparable, T any, P any] interface {
	RunView(ctx context.Context, id RID) (RunViewOf[RID, T, P], error)
}

// Releaser 强制释放的窄契约（目标 ID 用宿主的类型）。
type Releaser[ID comparable] interface {
	ForceRelease(ctx context.Context, req ForceReleaseOf[ID]) error
}

// Adjudicator 裁决未知结果的窄契约（效果键是 sideeffect 的字符串契约，
// 与任务 / 租户类型无关，故可直接复用原生 [Adjudication]）。
type Adjudicator interface {
	Adjudicate(ctx context.Context, req Adjudication) error
}

// Terminator 终止运行的窄契约（运行 ID 用宿主的类型）。
type Terminator[RID comparable] interface {
	Terminate(ctx context.Context, req TerminationOf[RID]) error
}

// HostPlane 控制面的宿主类型契约：查询 + 三类介入，类型全部由宿主决定。
//
// 类型参数：TID 任务标识 / RID 运行标识（comparable），T 任务快照，
// P 不变量条目。
//
// 租户上下文不在签名里：机制间经 context.Context 传递（tenant.WithContext
// 是内核的参考载体）。宿主自有租户类型在自己的实现里从 ctx 取用并判定；
// 鉴权钩子的宿主类型版本见 [AuthorizerOf]。
type HostPlane[TID comparable, RID comparable, T any, P any] interface {
	TaskQuerier[TID, T, P]
	RunQuerier[RID, T, P]
	Releaser[TID]
	Adjudicator
	Terminator[RID]
}

// AuthorizerOf 鉴权钩子的宿主类型版本：宿主返回自己的租户 / 权限上下文 C，
// 如何把它注入请求 context 由宿主的装配层决定（controlplane 不解释 C）。
//
// 使用本包 [Handler] 的宿主实现 [Authorizer]（返回 tenant.Context）；
// 租户体系不是 tenant 的宿主、或自建 HTTP 适配面的宿主实现本接口。
type AuthorizerOf[C any] interface {
	Authorize(r *http.Request, act Action, target string) (C, error)
}

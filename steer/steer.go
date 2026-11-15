// Package steer 运行中接管（steer）：用户在一轮编排**运行中**注入新输入时的
// 协作中止与保产续跑——五件套缺一不可（issue #168，spec
// 2026-09-13-interrupt-insert §5）：
//
//  1. **协作中止信号**（ErrSteered + Signal/IsSteered）：带 Cause 的 ctx 取消，
//     与用户取消（finalize.ErrUserCancelled）/ 进程停机（裸 ctx 取消）可区分；
//  2. **保产摘要**（BuildSummary）：已完成环节机械拼接，**不调 LLM**——收尾在
//     脱离 ctx 里跑，须快且不引入新失败面；读取失败走降级文案，不中断收尾；
//  3. **不落终态回可认领态**：接管不是失败也不是取消，回 created 由
//     Dispatcher 认领开新一轮（与自动续跑同一管线）；
//  4. **队列感知**（Decide）：有排队条目才回 created，否则按失败收敛——
//     接管条目已被本轮消费时回 created 会开一个无人认领的空轮；
//  5. **条件迁移单协程执行**（Run）：迁移只由本函数执行，无双跑窗口；
//     并发投喂时条件迁移恰有一个赢家，输家零行静默。
//
// 生命周期: Experimental（v0.38.0 定级）——本包仍会随宿主接入打磨
// （文案头与结算回调的接缝形状），不承诺兼容；定级理由见 api-lifecycle.yaml，
// 分级规则见 docs/api-compat.md。
//
// # 判据只依赖可注入事实（零领域词）
//
// 摘要构建的输入（环节清单）、队列非空与否、条件迁移结果——三样全部经
// Store 注入，本包不认识任何领域字段；文案头（Spec.Header / Spec.FailReport）
// 由宿主注入，内核只出机械正文。
//
// # 划界（同词不同域，互指不重复）
//
//   - vs finalize（issue #167）：finalize 管 Cause→终态的**路由**（本包的
//     ErrSteered 是它接管臂的输入——**steer 产出 Cause、finalize 消费**，
//     依赖方向 finalize → steer 单向，不许环）；本包管接管臂的**载荷与续跑
//     语义**（摘要、回 created、队列续跑、空队列失败收敛）。两边包头互指。
//   - vs handoff：handoff 改写**负责人字段**（From→To 的交接单+回执+代次，
//     跨进程）；本包是**同一会话内**用户对自有轮次的抢占——无所有权转移、
//     无交接单。
//   - vs taskboard 的「接管」：那是租约到期的**被动重跑**；本包是用户
//     **主动抢占**运行中的轮并摘要续跑。
//   - vs drain：drain 是停机排水（进程要死了）；本包是用户要改指令
//     （进程活得好好的）。
//   - vs pending：pending 是等待用户答复的挂起载体；本包**不挂起**——
//     当场中止并重开轮。
package steer

import (
	"context"

	"github.com/oekit/pkg/errors"
)

// ErrSteered 用户运行中注入新输入（接管）的协作中止 Cause。
//
// 与用户取消同走 WithCancelCause 中止、但收尾语义不同：不落 cancelled 终态，
// 而是写环节摘要后回 created 立即续跑（队列已空则按失败收敛，见 Decide）。
// 分流判定在 finalize.Route（它消费本哨兵，单向依赖不许环）。
var ErrSteered = errors.New("会话被用户新输入接管")

// Signal 返回携带接管 Cause 的协作中止 ctx 与只发接管信号的 cancel。
//
// 宿主在用户注入新输入时调用 cancel()——签名收**无参** func：Cause 固定为
// ErrSteered，不给「发错 Cause」留口子。分流判定见 IsSteered 与
// finalize.Route。
func Signal(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	return ctx, func() { cancel(ErrSteered) }
}

// IsSteered ctx 的 Cause 是否为接管信号（errors.Is，可判包装链）。
//
// 只回答「是不是接管」；与用户取消/停机的三分路由在 finalize.Route。
func IsSteered(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrSteered)
}

// Decision 队列感知的回迁决策。
type Decision int

const (
	// DecisionFailConverge 队列已空（接管条目已被本轮消费）：按失败收敛，
	// **不开空轮**。零值即此——保守侧缺省，漏传队列事实时不开新轮。
	DecisionFailConverge Decision = iota
	// DecisionRequeue 队列非空：回 created 由 Dispatcher 认领开新一轮。
	DecisionRequeue
)

// String 决策的人类可读形态（留痕与测试失败信息用）。
func (d Decision) String() string {
	if d == DecisionRequeue {
		return "requeue"
	}
	return "fail_converge"
}

// Decide 队列感知裁决：有排队条目才回 created，否则按失败收敛不开空轮。
//
// 这是纯函数——「队列非空与否」是宿主注入的事实（Store.HasQueue），
// 本包不自己读库。
func Decide(hasQueue bool) Decision {
	if hasQueue {
		return DecisionRequeue
	}
	return DecisionFailConverge
}

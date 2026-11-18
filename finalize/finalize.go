// Package finalize 收尾漏斗：一轮编排中止时「该落哪个终态 / 该不落终态」的
// 唯一收口——Cause→终态四分路由 + 多起点条件迁移 + detached 写纪律 +
// 零行回读补发「不谎报」（issue #167，移植自宿主 FinalizeTaskError 路由表
// 与 queue 的双起点收敛段）。
//
// 生命周期: Experimental（v0.38.0 定级）——本包仍会随首个宿主接入打磨
// （报告文案 / needs_review / 排队结算等宿主回调的接缝形状），不承诺兼容；
// 定级理由见 api-lifecycle.yaml，分级规则见 docs/api-compat.md。
//
// # 为什么是编排内核级
//
// contract 已下发 Status 词表（created / running / waiting_approval /
// succeeded / failed / cancelled / timed_out）——**词表在内核，「怎么安全地
// 迁过去」的算法也该在内核**。路由决策（Cause→目标态）与并发裁决
// （CAS 零行=他路已接手 / detached 写 / 0 行回读补发不谎报）不依赖任何
// 领域字段，是 contract 词表的消费算法，宿主无关性由 contract 的存在
// 本身证明。gorm/model 不下沉：条件迁移、回读现终态、发事件全走 Store
// 注入接口，本包只含路由与并发裁决算法。
//
// # 三条并发纪律（包级不变量，不是调用方自觉）
//
//  1. **终态写必须走脱离 ctx**（Detached）：编排 ctx 已取消时存储直查会
//     直接中止 ⇒ 终态写不进去、会话滞留 running 而事件谎报 succeeded。
//     Converge 内部一律派生脱离 ctx；宿主自己的收尾写同样要走 Detached。
//  2. **条件迁移零行 = 他路已接手**：不再写任何状态（不覆盖他路的终态）。
//     零行处置二选一见 ZeroRow：静默（他路已发事件）或回读补发（不谎报）。
//  3. **多起点收敛 + 0 行回读补发**：超时可能打在编排段（running），也可能
//     打在等待审批段（waiting_approval）——只认 running 起点的终态写零行
//     ⇒ 会话永不入终态。故 Spec.From 收全部起点、由存储一次原子条件迁移；
//     零行时回读库内现终态补发事件（Raced=true），**按库内事实报，不按
//     意图报**。
//
// # 划界（与既有同域包互指、不重复）
//
//   - vs contract：契约面（词表与接缝），不含算法——本包是它的消费算法。
//   - vs statemachine：纯谓词只回答「这次迁移合不合法」，不回答「该迁去哪」
//     ——本包是路由（该迁去哪）+ 并发写纪律。
//   - vs commitgate：环节（step）级并发终态**独占**提交闸（防两条分支都
//     提交）；本包是会话级 **cause 路由**与多起点收敛（防「写不进去却谎报」）。
//   - vs checkpoint：崩溃续跑（定案不接）；本包是运行期收尾，方向不交叉。
//   - vs drain：drain 只管停机臂的在途轮收敛且明写「不写终态」——「停机
//     不落终态」这条 case 的归属地正是本路由表（drain 收敛完 in-flight 后，
//     把「接下来落不落」交给本包）。
//   - vs steer：**接管臂（ErrSteered）的载荷与续跑语义在 steer**（摘要构建、
//     回 created、队列续跑、空队列失败收敛），本包只管路由到那一臂。
//     依赖方向 **finalize → deadline → steer**（cause 判定单源在 deadline，
//     它消费 steer.ErrSteered；本包只消费 deadline.Outcome），单向不许环；
//     包头互指：finalize 管「往哪收」，steer 管「怎么收与怎么续」。
//
// # 留宿主的回调
//
// 报告文案、needs_review 标记、排队结算、事件载荷的组装留宿主：本包只给
// Route（往哪收）+ Converge（怎么安全收）+ AdjudicateRetry（失败臂排队
// 裁决）。
package finalize

import (
	"context"

	"github.com/oekit/logx"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/deadline"
)

// log 收尾留痕（logx.Delegating 懒解析宿主后端）。
var log = logx.Delegating("finalize")

// ErrUserCancelled 用户经取消 API 触发的取消 Cause（WithCancelCause 携带）：
// 与进程停机的裸 ctx 取消**可区分**——取消落 cancelled 终态，停机不落终态
// 留给恢复循环。
//
// 身份来自 contract.ErrUserCancelled（issue #355）：本变量是**别名**而不是
// 第二份 errors.New——deadline 与 finalize 的收尾路由必须指向同一个值，
// 否则同一 ctx 在一侧判 cancelled、另一侧判停机。
var ErrUserCancelled = contract.ErrUserCancelled

// ErrSessionDeadline 会话级墙钟超时的 Cause（WithTimeoutCause 携带）：
// 收尾按 timed_out 终态强制收敛，且判定**必须先于停机分支**（停机不落
// 终态，而超时恰恰要落终态——次序反了该收敛的会话会被留给恢复循环）。
//
// 裸 deadline（未带本 Cause）归停机臂：宿主设置会话超时必须写
// context.WithTimeoutCause(parent, d, ErrSessionDeadline)，否则超时会被
// 误判成停机、会话滞留 running。
//
// 身份来自 contract.ErrSessionDeadline（issue #333）：本变量是**别名**而不是
// 第二份 errors.New。deadline 包装的 ctx 与收尾侧判定必须指向同一个值，
// 否则 errors.Is 互不认领——同一 ctx 上 deadline.Route 判 timed_out、
// finalize.Route 判 shutdown（Status() 为空 = 会话永久滞留 running）。
var ErrSessionDeadline = contract.ErrSessionDeadline

// Action 路由结论（Route 的输出 = 该收去哪条臂）。
type Action int

const (
	// ActionFailed 其余臂：落 failed 终态 + 排队重试裁决（AdjudicateRetry）。
	// 零值即此——「没有特殊 Cause」的缺省结局是失败。
	ActionFailed Action = iota
	// ActionTakeover 接管臂：**不落终态回 created**——载荷（环节摘要、
	// 回迁、队列续跑、空队列失败收敛）由 steer.Run 承接，本包只路由到这一臂。
	ActionTakeover
	// ActionCancelled 用户取消：落 cancelled 终态。
	ActionCancelled
	// ActionTimedOut 会话超时：落 timed_out 终态（**先于停机分支**判定）。
	ActionTimedOut
	// ActionShutdown 进程停机：**不落终态**——running / waiting_approval
	// 留给恢复循环按搁浅语义收敛；也不写失败报告（写了就是谎报）。
	ActionShutdown
)

// 路由臂的机读名（String() 的取值）。
//
// 与 contract 状态词表**同形**的三档（cancelled / timed_out / failed）
// 直接引用 contract 的常量：Action.Status() 把这三档映射到同一批词，
// 两处各写一份拼写就是「同一取值两份真相」——判定词表登记门禁
// （harness-word-table-handcopy）会当场报红。
// takeover / shutdown 是路由臂自有词，不在 contract 词表里。
const (
	actionNameTakeover  = "takeover"
	actionNameCancelled = contract.StatusCancelled
	actionNameTimedOut  = contract.StatusTimedOut
	actionNameShutdown  = "shutdown"
	actionNameFailed    = contract.StatusFailed
)

// String 路由臂的人类可读形态（留痕与测试失败信息用）。
func (a Action) String() string {
	switch a {
	case ActionTakeover:
		return actionNameTakeover
	case ActionCancelled:
		return actionNameCancelled
	case ActionTimedOut:
		return actionNameTimedOut
	case ActionShutdown:
		return actionNameShutdown
	default:
		return actionNameFailed
	}
}

// Status 该臂的收敛状态词（contract 词表）。
//
// ActionShutdown 返回空——「不落终态」在词表上没有目标态，宿主不得把
// 空串映射成任何 Status。
func (a Action) Status() string {
	switch a {
	case ActionTakeover:
		return contract.StatusCreated
	case ActionCancelled:
		return contract.StatusCancelled
	case ActionTimedOut:
		return contract.StatusTimedOut
	case ActionFailed:
		return contract.StatusFailed
	default:
		return ""
	}
}

// Route Cause→终态路由（纯函数；判据单源在 deadline.Route，本函数只做
// Outcome→Action 投影，issue #355）：
//
//	接管（不落终态回 created）/ 用户取消（cancelled 终态）/
//	会话超时（timed_out 终态，**先于**停机分支）/ 进程停机（不落终态留给
//	恢复循环）/ 其余 failed。
//
// **路由表与臂序不在这里第二遍**：接管/取消/超时/停机的 cause 判定与
// 「timed_out 先于停机」的次序纪律都在 deadline.Route 唯一一份；本包若再写
// 一遍，两处会无声分叉（#355 的实测形态：接管的同一 ctx，deadline 判
// shutdown、finalize 判 takeover）。userCancel 是宿主自有取消哨兵的透传口。
//
// 接管臂只判 Cause 不判队列：「有排队条目才回 created、否则按失败收敛」
// 是 steer.Decide 的队列感知（归接管臂载荷，见包头划界）。
func Route(ctx context.Context, userCancel ...error) Action {
	switch deadline.Route(ctx, userCancel...) {
	case deadline.OutcomeTakeover:
		return ActionTakeover
	case deadline.OutcomeCancelled:
		return ActionCancelled
	case deadline.OutcomeTimedOut:
		return ActionTimedOut
	case deadline.OutcomeShutdown:
		return ActionShutdown
	default:
		// OutcomeNone：没有取消类 cause；收尾漏斗的缺省结局是 failed。
		return ActionFailed
	}
}

// Detached 派生脱离 ctx：切断取消传播、保留父 ctx 的值。
//
// **包级不变量**：终态 / 报告 / 事件的存储写一律走本函数派生的 ctx——
// 编排 ctx 已被取消（用户取消 / 接管 / 停机都先取消编排 ctx），存储直查
// 会直接中止，终态写不进去 ⇒ 会话滞留 running 而事件谎报。
//
// 预算（超时上限）留宿主：需要时 context.WithTimeout(Detached(ctx), d)。
func Detached(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

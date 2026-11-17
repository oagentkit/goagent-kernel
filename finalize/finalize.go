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
//     依赖方向 **finalize → steer**（消费 steer.ErrSteered），单向不许环；
//     两边包头互指：finalize 管「往哪收」，steer 管「怎么收与怎么续」。
//   - vs deadline：**收尾路由的唯一收口在本包**——deadline 的 Route 是本表的
//     投影（issue #355：两张路由表曾各写一遍且判据**已分叉**，接管的 ctx 在
//     两包得到相反结论，而两包零交叉断言）。deadline 独有的是**会话墙钟
//     机制**（锚点三态 / shared 锚点 / 轮 ctx 装饰），与本包的「往哪收」
//     不是一根轴。
//
// # 留宿主的回调
//
// 报告文案、needs_review 标记、排队结算、事件载荷的组装留宿主：本包只给
// Route（往哪收）+ Converge（怎么安全收）+ AdjudicateRetry（失败臂排队
// 裁决）。
package finalize

import (
	"context"
	"slices"

	"github.com/oekit/logx"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/steer"
)

// log 收尾留痕（logx.Delegating 懒解析宿主后端）。
var log = logx.Delegating("finalize")

// ErrUserCancelled 用户经取消 API 触发的取消 Cause（WithCancelCause 携带）：
// 与进程停机的裸 ctx 取消**可区分**——取消落 cancelled 终态，停机不落终态
// 留给恢复循环。
var ErrUserCancelled = errors.New("会话被用户取消")

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

// allActions 路由臂全集（判定面与枚举面同源：新增一臂只改这一处）。
var allActions = []Action{
	ActionFailed,
	ActionTakeover,
	ActionCancelled,
	ActionTimedOut,
	ActionShutdown,
}

// AllActions 返回收尾路由臂的全集（**新副本**：调用方改不动本包的事实）。
//
// 取值域要有出口：文档面（README 职责列要列全的词表）与宿主的状态选择器都要
// 拿它做**集合比对**才有意义——手抄一份常量只能比对「已知的几个臂」，真源加了
// 第五个臂时抄写方不会知道自己漏了（issue #384）。
func AllActions() []Action {
	out := make([]Action, len(allActions))
	copy(out, allActions)
	return out
}

// AllRouteStatuses 返回路由各臂落下的**终态词**（非空取值的全集，新副本）。
//
// 「不落终态」的臂（停机）不在其中——它在词表上没有目标态，宿主不得把空串
// 映射成任何 Status。取值从 allActions 派生（Action.Status），**不另抄清单**：
// 抄一份就会与 Action.Status 分叉（而分叉是绿的：两边各自「看起来对」）。
func AllRouteStatuses() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(allActions))
	for _, a := range allActions {
		s := a.Status()
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// String 路由臂的人类可读形态（留痕与测试失败信息用）。
func (a Action) String() string {
	switch a {
	case ActionTakeover:
		return "takeover"
	case ActionCancelled:
		return "cancelled"
	case ActionTimedOut:
		return "timed_out"
	case ActionShutdown:
		return "shutdown"
	default:
		return "failed"
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

// Route Cause→终态四分路由（纯函数；移植自宿主 FinalizeTaskError 的路由表）：
//
//	接管（不落终态回 created）/ 用户取消（cancelled 终态）/ 会话超时
//	（timed_out 终态，**先于**停机分支）/ 进程停机（不落终态留给恢复循环）/
//	其余 failed。
//
// 判定走 errors.Is（可判包装链）；Cause 由宿主经 WithCancelCause 注入
// （可注入哨兵形态——contract 未下发 Cause 哨兵，故用户取消/超时的哨兵
// 在本包、接管哨兵在 steer，本包消费）。
//
// 接管臂只判 Cause 不判队列：「有排队条目才回 created、否则按失败收敛」
// 是 steer.Decide 的队列感知（归接管臂载荷，见包头划界）。
//
// # 本函数是收尾路由的**唯一收口**（issue #355）
//
// 此前同一份收尾事实有两张路由表（本包与 deadline），判据**已分叉**：同一个
// 接管的 ctx 在两包得到相反结论（deadline 判 shutdown / finalize 判 takeover），
// 而两包零交叉断言——失效形态是绿的（两边都返回正常值，只是结论相反）。
// 现在路由表只有这一份：别的包要判「这次编排往哪收」都调这里
// （deadline.Route 是本表的投影），交叉断言见本包的用例。
func Route(ctx context.Context) Action { return RouteWith(ctx) }

// RouteWith 与 Route 同形，但额外接受宿主注入的「用户取消」哨兵。
//
// # 为什么要有注入位
//
// 「用户取消」这件事**归宿主**（内核发不出「用户点了取消」这个事实），而接管
// （steer.ErrSteered）、超时（ErrSessionDeadline）、停机（ctx 取消）三个哨兵是
// 内核的。收尾路由表因此留一个注入位：注入的哨兵与缺省的 ErrUserCancelled
// 同等对待（命中任一即判 cancelled）。
//
// 不传时行为与 Route **逐字相同**：只认 ErrUserCancelled。
//
// # 判定顺序是路由表的语义本体
//
// timeout 臂必须先于停机臂（ctx.Err() != nil）：停机不落终态、超时要落终态——
// 次序反了，该收敛的会话被留给恢复循环，永久滞留 running。
func RouteWith(ctx context.Context, userCancel ...error) Action {
	switch {
	case errors.Is(context.Cause(ctx), steer.ErrSteered):
		return ActionTakeover
	case isUserCancel(context.Cause(ctx), userCancel):
		return ActionCancelled
	case errors.Is(context.Cause(ctx), ErrSessionDeadline):
		// 必须先于 ctx.Err() != nil（停机）分支：超时的 ctx 同样有 ctx.Err()，
		// 但停机不落终态、超时要落终态——次序是路由表的语义本体。
		return ActionTimedOut
	case ctx.Err() != nil:
		return ActionShutdown
	default:
		return ActionFailed
	}
}

// isUserCancel 判定 cause 是否是「用户取消」（缺省哨兵或宿主注入的哨兵）。
//
// 判定走 errors.Is：宿主把哨兵包一层再发也判得中（包装链可判）。
func isUserCancel(cause error, sentinels []error) bool {
	if errors.Is(cause, ErrUserCancelled) {
		return true
	}
	for _, s := range sentinels {
		if s != nil && errors.Is(cause, s) {
			return true
		}
	}
	return false
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

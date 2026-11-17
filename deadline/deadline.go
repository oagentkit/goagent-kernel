// Package deadline 会话级墙钟时限：shared 锚点跨续跑轮 + timed_out deny-by-default
// （issue #170，下沉自宿主 internal/engine/scheduler/handle.go 的轮内墙钟与锚点写入）。
//
// 生命周期: Experimental（v0.38.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 它回答什么
//
// 会话往往跨多轮（自动续跑/排队续跑）：**总时限不能因续跑轮重置**。每次续跑
// 都新开一段时限的话，会话可以无限续跑下去。故：
//
//   - **锚点只写一次**：首轮认领成功后写 deadline_at（FirstClaimAt + Timeout）；
//   - **续跑轮沿用同一锚点**（shared 模式）：轮内 ctx 只拿锚点的**剩余**时长；
//   - **轮内 ctx 携带专属 cause**（ErrSessionDeadline）：与用户取消、进程停机、
//     接管各占一档（收尾路由归 finalize，见下）；
//   - **过期锚点立即收敛**：认领排队窗口把余量耗完的续跑轮，认领迁移成功后
//     立即按 timed_out 收敛，**不进编排**（进去了也只是白跑一段）。
//
// 时限是**会话生命周期**的轴：进程活得好好的也会到点。
//
// # 收尾路由的判定面在 finalize（issue #355）
//
// 收尾路由（cause → 该落哪个终态）此前有**两张表**：本包的 Route 与 finalize
// 的 Route。两张表已**分叉**——同一个接管的 ctx 在两包得到**相反**结论（本包
// 判 shutdown「不落终态」、finalize 判 takeover「落 created」），接管场景下该
// 收敛的会话被永久留在 running；而两包零交叉断言，失效形态是绿的（两边都
// 返回正常值，只是结论相反）。
//
// 现在**收尾路由的唯一收口是 finalize.Route/RouteWith**，本包的 Route 只是
// 它的投影（Outcome ← Action 一一映射）。下列纪律改由 finalize 的路由表承担
// （其用例钉住，交叉断言也在那里）：
//
//	原因值                      结论        落什么
//	steer.ErrSteered            takeover    回 created（不落终态）
//	ErrSessionDeadline          timed_out   终态（deny-by-default：超时的产出不可信）
//	用户取消哨兵                cancelled   终态
//	其余 ctx 已取消（进程停机） 不落终态    留给恢复循环/启动对账
//
// **timed_out 臂必须先于「ctx.Err() != nil（进程停机）」臂。** 停机臂的条件是
// 「ctx 已取消」，而墙钟到点的 ctx 同样已取消（DeadlineExceeded）——次序写反，
// 每次会话超时都会被判成停机、**不落终态**，会话永久滞留 running。
//
// 父 ctx 取消时子 ctx 的 Cause 传播父原因，故「用户取消/停机」不会被墙钟
// 装饰吃掉——各档的前提是各有自己的 cause（用户取消请用
// context.WithCancelCause 表达，裸 cancel 与停机在 Cause 上不可分）。
//
// # 与同域包的划界（各占一根轴，互不改判据）
//
//   - **vs budget**：budget 是资源/窗口压力面（token/上下文水位）；本包是
//     墙钟时限（时间轴、跨轮锚点）。一根轴都不沾——不碰 budget 的判据，
//     也不把时限塞进 budget。
//   - **vs drain**：drain 是**进程**生命周期的在途轮收敛（停机排水）；本包是
//     **会话**生命周期的总时限。drain.ErrDeadline 与本包 ErrSessionDeadline
//     是两个 Cause，收尾路由归 finalize（它认全部 cause）。
//   - **vs finalize**：**收尾路由的唯一收口在 finalize**（issue #355）——本包的
//     Route 是它的投影（同一组 ctx 两包必须得到同一结论，交叉断言在那边）；
//     本包独有的是**会话墙钟机制**（锚点三态 / shared 锚点 / 轮 ctx 装饰），
//     与 finalize 的「往哪收」不是一根轴。
//   - **vs watchdog**：监视不变量，不做时限强制。
//   - **vs contract**：contract 给了 StatusTimedOut 词表（本包的 Outcome.Status
//     缺省消费它），没有时限机制——本包补机制不改词表；宿主自有状态词表经
//     StatusOf/StatusWith 注入（issue #237），缺省路径逐字不变。
package deadline

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
)

// ErrSessionDeadline 会话级墙钟超时的原因值（轮内 ctx 经 WithTimeoutCause 携带）。
//
// 识别一律走 errors.Is（可被宿主的包装链包裹）；与 drain.ErrDeadline
// （停机排水超时）是**两个** Cause、两根轴，不要互相代入。
//
// 身份来自 contract.ErrSessionDeadline（issue #333）：本变量是**别名**而不是
// 第二份 errors.New——收尾侧 finalize.Route 用同一个值做判定，errors.Is
// 一条链打通；各写一份同名同文案的哨兵会让墙钟超时被判成停机。
var ErrSessionDeadline = contract.ErrSessionDeadline

// ErrNoStore 未注入锚点持久化接缝就要读/写锚点。
//
// fail-closed 而不是静默跳过：shared 模式下漏写锚点 = 续跑轮各自新开一段时限，
// 「总时限不被续跑轮刷新」这条契约**静默失效**，且没有任何信号。
var ErrNoStore = errors.New("deadline: 未注入锚点持久化接缝（Store），无法读写会话墙钟锚点")

// Config 宿主注入的时限配置（宿主策略，语义与宿主 conf 旋钮一致）。
type Config struct {
	// Timeout 会话级墙钟上限；**0=关闭**（直通：不装饰 ctx、不写锚点）。
	Timeout time.Duration
	// Shared shared 模式：续跑轮沿用首轮写入的锚点（总时限不因续跑轮重置）。
	// false=每轮各自开一段 Timeout（锚点不参与）。
	Shared bool
}

// Anchor 会话墙钟锚点（首轮认领成功后写一次，续跑轮读）。
type Anchor struct {
	// FirstClaimAt 首次认领成功时刻（写入时生成，只写一次）。
	FirstClaimAt time.Time
	// DeadlineAt 绝对截止时刻 = FirstClaimAt + Timeout。续跑轮沿用它，
	// **绝不刷新**——刷新等于把总时限重新计时。
	DeadlineAt time.Time
}

// State 锚点三态（判定纯函数的返回值）。
type State int

const (
	// StateUnwritten 未写：没有锚点（首轮），或锚点是零值（存层没写全）。
	StateUnwritten State = iota
	// StateLive 有效：还有余量，轮内按剩余时长装饰 ctx。
	StateLive
	// StateExpired 已过期：余量耗尽，认领迁移后立即 timed_out 收敛、不进编排。
	StateExpired
)

// String 机读名（进日志与测试断言）。
func (s State) String() string {
	switch s {
	case StateUnwritten:
		return "unwritten"
	case StateLive:
		return "live"
	case StateExpired:
		return "expired"
	}
	return "unknown"
}

// State 判定锚点三态（纯函数；now 为当前时刻）。
//
// nil 接收者与零值 DeadlineAt 都判 **unwritten**——零值时刻是「没写过」的
// 存层形态，不是「1970 年就过期了」：判成 expired 会让续跑轮一进来就收敛。
func (a *Anchor) State(now time.Time) State {
	if a == nil || a.DeadlineAt.IsZero() {
		return StateUnwritten
	}
	if now.Before(a.DeadlineAt) {
		return StateLive
	}
	return StateExpired
}

// Remaining 锚点剩余时长（未写/已过期均为 0 或负值；判据看 State）。
func (a *Anchor) Remaining(now time.Time) time.Duration {
	if a == nil {
		return 0
	}
	return a.DeadlineAt.Sub(now)
}

// Constraint 本轮时限形态（Begin 的返回值，issue #318）。
//
// # 为什么不是 bool
//
// 「本轮 expired 没有」只有两态，而本轮至少有三态：**关闭直通**（Timeout<=0，
// 本轮无限跑、ctx 不带 Deadline）、**受约束**（装饰了 Deadline）、**已过期**
// （不装饰，认领后立即收敛）。前两者用 bool 表达时返回值完全相同，调用方无法
// 从返回值判断本轮有没有墙钟保护——只能去猜 ctx.Deadline()，而父 ctx 自带的
// Deadline 会污染这个信号。
//
// 这里复用包内已有的三态判定面（Anchor.State）的决策结果，只把「锚点三态」
// 投影成「本轮形态」：StateUnwritten/Live → ConstraintActive，StateExpired →
// ConstraintExpired，Timeout<=0 → ConstraintOff。
type Constraint int

const (
	// ConstraintOff 本轮**不受**时限约束（Timeout<=0 的关闭直通）：ctx 原样返回。
	ConstraintOff Constraint = iota
	// ConstraintActive 本轮受约束：ctx 带本包装饰的 Deadline（新开或锚点剩余）。
	ConstraintActive
	// ConstraintExpired 本轮已过期：不装饰，宿主认领迁移后立即按 timed_out 收敛。
	ConstraintExpired
)

// String 机读名（进日志与测试断言）。
func (c Constraint) String() string {
	switch c {
	case ConstraintOff:
		return "off"
	case ConstraintActive:
		return "active"
	case ConstraintExpired:
		return "expired"
	}
	return "unknown"
}

// Constrained 本轮是否有墙钟保护（受约束或已过期都算——已过期只是余量为 0）。
func (c Constraint) Constrained() bool {
	return c == ConstraintActive || c == ConstraintExpired
}

// Expired 本轮是否已过期（仅 ConstraintExpired）。
func (c Constraint) Expired() bool { return c == ConstraintExpired }

// WithCause 按剩余时长装饰轮 ctx，cause 携带专属 ErrSessionDeadline。
//
// remaining <= 0 得到一个**立即过期**的 ctx（cause 仍是 ErrSessionDeadline）。
// 正常路径不该走到这里：Begin 对过期锚点返回 expired=true 而不装饰，
// 这里保留该行为是为了让「已知剩余时长」的调用方少一层分支。
func WithCause(ctx context.Context, remaining time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, remaining, ErrSessionDeadline)
}

// Begin 为本轮装饰墙钟 ctx：返回 (轮 ctx, 释放函数, 本轮时限形态)。
//
// now 为当前时刻（宿主传 time.Now()；判定与余量都以它为准）。
//
// 四种输入与返回值（issue #318：返回值必须能把前两者分开）：
//
//   - **0=关闭直通**（Timeout<=0）：原样返回 ctx，ConstraintOff——
//     本轮**不受**约束，ctx 不带 Deadline；
//   - **shared 且锚点有效**：沿用同一锚点的剩余时长，ConstraintActive——
//     总时限不因续跑轮重置；
//   - **shared 且锚点已过期**：不装饰，ConstraintExpired。宿主在**认领迁移
//     成功后**按 OutcomeTimedOut 收敛（不进编排），见包头次序纪律；
//   - **首轮（锚点未写）/ 非 shared**：新开一段 Timeout，ConstraintActive。
//
// 调用方判断「本轮是否受墙钟保护」一律看第三个返回值，**不要**用
// ctx.Deadline() 推断：父 ctx 自带的 Deadline 会被误读成本包的约束。
//
// release 非 nil 时必须 defer（释放定时器）；ConstraintExpired 与
// ConstraintOff 时 release 是空操作。
func (c Config) Begin(ctx context.Context, a *Anchor, now time.Time) (context.Context, context.CancelFunc, Constraint) {
	if c.Timeout <= 0 {
		return ctx, func() {}, ConstraintOff // 0=关闭直通
	}
	if c.Shared {
		switch a.State(now) {
		case StateExpired:
			// 认领排队窗口已耗完余量：本轮不进编排，宿主认领后立即收敛。
			return ctx, func() {}, ConstraintExpired
		case StateLive:
			rctx, cancel := WithCause(ctx, a.Remaining(now))
			return rctx, cancel, ConstraintActive
		}
		// StateUnwritten（首轮）→ 落到下面新开一段 Timeout，锚点在认领成功后写。
	}
	rctx, cancel := WithCause(ctx, c.Timeout)
	return rctx, cancel, ConstraintActive
}

// anchorAt 生成新锚点（只在首轮认领成功后调用）。
func (c Config) anchorAt(now time.Time) Anchor {
	return Anchor{FirstClaimAt: now, DeadlineAt: now.Add(c.Timeout)}
}

// Store 锚点持久化接缝（注入）：首轮写一次、续跑轮读。
//
// 内核不关心存哪（DB 列 / 内存表 / 文件）、也不关心键怎么来——宿主按会话
// 构造实例。实现**并发安全**（收尾与恢复路径可能同时读）。
//
// # 接缝契约（宿主实现；内核在 Restore / Persist 里同步调用它）
//
// @seam: no-reentry no-blocking panic-isolated
//
//   - no-reentry：宿主实现内**不得回查本包**（不得调 Restore / Persist）——
//     那会递归回本接口（Restore → LoadAnchor → Restore），无限递归。内核侧
//     的兑现面：本包在调用点**不持有任何互斥锁**，故回查不会自锁；剩下
//     不可回查的是本接口自身的递归。
//   - no-blocking：宿主实现必须**有界**——两个方法都收 ctx，实现必须承接
//     ctx 的取消 / 超时并在期限内返回，不得无限等待、不得在持存储内部锁时
//     等别的回调。Restore 的读失败是**原样上抛**的（不静默当「未写」），
//     所以一次挂住的读会把整轮的剩余时长装饰拖住；Persist 挂在首轮认领
//     成功之后的路径上，同样会拖住本轮。
//   - panic-isolated：**宿主侧兑现**——本包**没有 recover 边界**：宿主实现
//     的 panic 会沿 Restore / Persist 外溢到调用栈。宿主实现须自行保证
//     不 panic。
type Store interface {
	// LoadAnchor 读锚点；**未写必须返回 (nil, nil)**。
	// 返回零值锚点会被判成「已写但过期」，续跑轮会当场 timed_out。
	LoadAnchor(ctx context.Context) (*Anchor, error)
	// SaveAnchor 写锚点。只在该写时被 Persist 调用一次（首轮认领成功后）。
	SaveAnchor(ctx context.Context, a Anchor) error
}

// Restore 从注入的 Store 读锚点（未写 → (nil, nil)）。
//
// 读失败**原样上抛**，不静默当「未写」：读不出锚点就装饰不了本轮剩余时长，
// 静默回落会给续跑轮新开一段 Timeout，把总时限静默重置。
func Restore(ctx context.Context, s Store) (*Anchor, error) {
	if s == nil {
		return nil, ErrNoStore
	}
	return s.LoadAnchor(ctx)
}

// Persist 首轮认领成功后的锚点落位：**只在该写时写一次**，返回实际写入的锚点。
//
//	非 shared / 0=关闭        → 不写（锚点不参与）
//	existing 已有锚点（续跑轮）→ 不写（沿用，绝不刷新——总时限不因续跑轮重置）
//	existing 已过期            → 不写（该轮走立即收敛分支，轮不到这里）
//	其余（首轮认领成功）       → 写 now+Timeout
//
// Store 为 nil 且**确实需要写**时返回 ErrNoStore（fail-closed，见该哨兵注释）。
func Persist(ctx context.Context, s Store, c Config, existing *Anchor, now time.Time) (*Anchor, error) {
	if c.Timeout <= 0 || !c.Shared {
		return nil, nil
	}
	if existing != nil && !existing.DeadlineAt.IsZero() {
		return nil, nil // 续跑轮沿用，绝不刷新
	}
	if s == nil {
		return nil, ErrNoStore
	}
	a := c.anchorAt(now)
	if err := s.SaveAnchor(ctx, a); err != nil {
		return nil, err
	}
	return &a, nil
}

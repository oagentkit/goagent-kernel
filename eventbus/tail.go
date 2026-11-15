// tail.go —— event-tail 跨副本补灌面（issue #166：SSE 单路桥接的 DB→Hub 读侧半边）。
//
// # 为什么要有这个面
//
// Hub 是进程内内存结构：本副本发布的事件经 pub 直投零延迟到达本副本订阅者，
// 但他副本发布的事件只在权威存储里，本地 Hub 永远收不到；pub 直投之前也还有
// 一个窗口缺口。任何「持久化事件 + 进程内 Hub 实时广播 + 游标回放」三件套的
// 宿主都缺这半边——写侧已由 DurablePublisher（#160）收编，**读侧此前各宿主
// 各写一份**（bianque background.go 的 event-tail 段），各判一套必然漂移。
//
// 本包把读侧四步收进包内：初始化游标 → 轮询 → 回灌 → 推进。判据与宿主无关：
// append-only id 游标、初始化取 MaxID、读失败游标不动（幂等补读）、回灌经
// Hub.Publish。存储形状只有两个方法（TailStore），包零 DB 依赖。
//
// # scope:replica——每副本一条，**不套 leader 门**（AGENTS.md 门控教训的实弹对象）
//
// Hub 是进程内内存结构，每个副本的 SSE 连接只看得见**自己进程**的 Hub。若给
// 本循环套上 leader 门，非 leader 副本的事件尾随会整段停摆——那些副本的 SSE
// 连接从此收不到任何他副本事件，且**不报错、不告警**，只表现为「某些副本的
// 页面不动了」。leader 门适用于「全局只该做一次」的作业（清扫、外发游标）；
// 本循环是**每副本自己的补灌桥**，跑的次数 = 副本数才是对的。
//
// # 与既有两面的划界（三层各一层，互不替代）
//
//   - 写侧 = DurablePublisher（#160）：落库拿单调游标→广播、落库失败不广播。
//     本面是它的**对偶读侧**，同包互补、不重复；
//   - 连接侧 = oekit/pkg 的 SSE 游标回放应答器（#483）：单连接 after-cursor
//     窗口应答，是 HTTP 层；本面是**副本级 存储→Hub 补灌桥**，在 Hub 层，
//     不碰 HTTP；
//   - 权威顺序与可靠面始终是存储游标，本面只是实时面的补达。
//
// # 重复帧不归本面去重（与 #160「ID 由谁赋值」同一分工纪律）
//
// 本副本实时帧由 pub 直投（零延迟），本面稍后又会从存储读回同一条事件——
// 同一帧到达订阅者两次是**设计内**的。Tailer 不负责去重：去重归订阅侧游标
// （`ev.ID <= after` 幂等吸收，SSE 连接既有语义）。本面唯一要做的是把
// **存储铸造的权威 ID 原样带上**，让订阅侧的幂等判定有据可依。
//
// # Relay 交互（如实声明）
//
// 回灌经 Publish（与宿主原实现同款）：装配了 Relay 的宿主，本面读回的帧会
// 经 Relay 再外发一次——Relay 契约原文已写明「不得经 Relay 再次外发同一条
// 事件（回流即环，外发的去重是实现的责任）」，该去重归 Relay 实现，本面不
// 替它兜底。只用进程内 Hub 的宿主（缺省形态）无此问题。
package eventbus

import (
	"context"
	"time"

	"github.com/oekit/logx"
)

// log event-tail 留痕。Delegating 懒解析：宿主 SetDefault 之前也能用，之后自动跟随。
var log = logx.Delegating("eventbus")

// 尾随缺省参数（与宿主原实现同参：每秒一拍、单拍 1000 条）。
const (
	// DefaultTailInterval 缺省尾随间隔。他副本事件的补达上界 ≈ 本间隔。
	DefaultTailInterval = time.Second
	// DefaultTailLimit 缺省单拍批量上限（超出部分下一拍续读，游标单调推进）。
	DefaultTailLimit = 1000
)

// TailStore 尾随读侧存储接缝（宿主实现；issue #166，形态与 durable.go 的
// Store 同风格：判定留包内、状态归存储、包零 DB 依赖）。
//
// 契约三条：
//   - **游标空间 append-only 且单调**：ListAfter 只回 id > cursor 的行，
//     且**必须按 ID 升序**——游标推进建立在这个序上（乱序返回会让游标
//     回退或跳号，两者都不在本包的观测面内）；
//   - **行→Event 的转换归实现**：ID 取存储铸造的权威游标，Payload 已是
//     解析好的载荷（与 DurablePublisher 的 Insert 入参转换对称——那边是
//     发布前构造，这边是读出后还原）。本包**不改写**回读到的字段（权威行
//     不是本包的产物，改写即篡改）；
//   - **ctx 超时/取消由实现自判**：读失败返回 error 且**不得返回部分
//     结果**——本包收到 error 即游标不动，返回了一半会让「失败」与
//     「成功读到前半段」不可分。
type TailStore interface {
	// MaxID 当前权威游标空间的最大 ID（空表 = 0）。只在初始化时调用一次。
	MaxID(ctx context.Context) (uint64, error)
	// ListAfter 列出 id > cursor 的事件（limit 为单拍上限，升序）。
	ListAfter(ctx context.Context, cursor uint64, limit int) ([]Event, error)
}

// Tailer 跨副本事件尾随器：每副本一条，把权威存储里 id > 游标 的新事件
// 回灌本进程 Hub（scope:replica，见包头「不套 leader 门」一节）。
//
// 零值不可用，用 NewTailer 构造。Run 同步阻塞到 ctx 取消（不另起 goroutine
// ——起 goroutine 归宿主的后台装配面，本包不碰 `harness-no-bare-goroutine` 的
// 判定面），故宿主在自己的后台任务里调它。
type Tailer struct {
	store TailStore
	hub   *Hub

	interval time.Duration
	limit    int
	// startCursor 显式起始游标（WithTailStartCursor 设定）；useMaxID=false
	// 时跳过 MaxID 初始化，直接从该值起随。缺省 useMaxID=true。
	startCursor uint64
	useMaxID    bool
}

// TailerOption 尾随器可选项。
type TailerOption func(*Tailer)

// WithTailInterval 覆盖尾随间隔（d <= 0 归缺省 DefaultTailInterval）。
func WithTailInterval(d time.Duration) TailerOption {
	return func(t *Tailer) {
		if d > 0 {
			t.interval = d
		}
	}
}

// WithTailBatchLimit 覆盖单拍批量上限（n <= 0 归缺省 DefaultTailLimit）。
func WithTailBatchLimit(n int) TailerOption {
	return func(t *Tailer) {
		if n > 0 {
			t.limit = n
		}
	}
}

// WithTailStartCursor 显式起始游标：**跳过 MaxID 初始化**，直接从给定值起随。
//
// 缺省（不给本选项）走 MaxID 初始化——从当前尾部起随、不回放存量，那是
// 补灌面的常态。显式给游标用于宿主已知起点的形态（如从上次停机点续随）；
// 给 0 即从头回放，回放量由宿主自己负责。
func WithTailStartCursor(id uint64) TailerOption {
	return func(t *Tailer) {
		t.startCursor = id
		t.useMaxID = false
	}
}

// NewTailer 构造：store 与 hub 均必填。
//
// nil 即 panic——没有存储的「尾随」与没有 Hub 的「补灌」都不是半成品而是
// 空转，fail fast 在装配点，不留到运行时（与 NewDurablePublisher 同口径）。
func NewTailer(store TailStore, hub *Hub, opts ...TailerOption) *Tailer {
	if store == nil {
		panic("eventbus: Tailer 需要 TailStore（nil store 的尾随无从读起）")
	}
	if hub == nil {
		panic("eventbus: Tailer 需要 Hub（nil hub 的补灌无处可投）")
	}
	t := &Tailer{
		store:    store,
		hub:      hub,
		interval: DefaultTailInterval,
		limit:    DefaultTailLimit,
		useMaxID: true,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Run 尾随循环：每 interval 一拍，直到 ctx 取消（同步返回）。
//
// 四步语义（与宿主原实现逐条同款）：
//
//  1. **初始化取 MaxID**：首拍只读游标、不读存量——本面是**补灌**不是
//     回放（存量窗口回放归连接侧 after-cursor 应答），且初始化失败下一拍
//     重试，不推进任何状态；
//  2. **读 id > 游标 的新事件回灌**，逐条 Publish 后推进游标；
//  3. **读失败游标不动**：瞬时存储抖动下，下一拍用同一个游标重读
//     （append-only ⇒ 幂等补读，不丢不重——重的部分由订阅侧游标吸收）；
//  4. **ctx 取消即退出**：select ctx.Done 是唯一退出式（`for range` 永不
//     返回的教训见 Hub 包注释），ticker 随 defer 停止，无泄漏。
func (t *Tailer) Run(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	cursor := t.startCursor
	inited := !t.useMaxID
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !inited {
				max, err := t.store.MaxID(ctx)
				if err != nil {
					// 初始化失败：游标未知，绝不能拿 0 去 ListAfter（那会
					// 把全量存量当新事件灌进来）。下一拍重试。
					log.CtxWarn(ctx, "event-tail 游标初始化失败（下拍重试）", logx.ErrKey, err)
					continue
				}
				cursor, inited = max, true
				continue // 初始化拍不读存量
			}
			evs, err := t.store.ListAfter(ctx, cursor, t.limit)
			if err != nil {
				// 瞬时抖动：游标不动，下一拍补读（append-only 幂等）。
				log.CtxWarn(ctx, "event-tail 读取失败（游标不动，下拍补读）", logx.ErrKey, err)
				continue
			}
			for i := range evs {
				t.hub.Publish(evs[i])
				cursor = evs[i].ID
			}
		}
	}
}

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
// append-only id 游标、初始化取 MaxID（或经 CursorStore 续随上次停点）、读失败
// 游标不动（幂等补读）、回灌经 Hub.Publish。存储形状只有两个方法（TailStore），
// 包零 DB 依赖；游标持久化（CursorStore）与游标空洞告警（WithTailOnGap）是
// 可选面（issue #183）。
//
// # 「读失败游标不动 ⇒ 不丢」的完整前提（issue #183，必须写死）
//
// 「读失败游标不动 + append-only ⇒ 幂等补读，不丢不重」只在**「ID 序 ==
// 提交可见序」**时成立。数据库自增/序列形态下有个经典缺口：A 事务拿到
// id=10 尚未提交，B 事务拿到 id=11 先提交；读侧在 READ COMMITTED 下看到
// 11 并把游标推到 11——A 提交后 id=10 **永远读不到**，且是静默丢帧。这条
// 缺口不在调用方能修：它必须由 TailStore 实现按契约排除（见 TailStore 的
// 可见序硬要求），Tailer 侧的 WithTailOnGap 只是最后的哨兵，不是兜底。
// #264 收口：收口前仓内没有任何生产级实现满足这条硬要求（满足它的只有
// 测试替身），本版把被承诺支持的生产形态收窄为**只有 Outbox**、自增直读
// 显式声明不支持——契约文字与实现声明自此必须一致（见 TailStore 契约与
// WatermarkTailStore）。
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
	// cursorStore 游标持久化接缝（WithTailCursorStore 设定，可选）。
	cursorStore CursorStore
	// onGap 游标空洞哨兵（WithTailOnGap 设定，可选）：收到「跳号」事件段时
	// 调用（from..to = 被跳过的 ID 闭区间）。缺省走包内 warn 日志。
	onGap func(from, to uint64)
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
//
// 同时装配了 CursorStore 时，**已持久化的游标优先**：LoadCursor 命中
// （found=true）就从持久化值起随；未命中（found=false，首次部署）才回退
// 到本选项的显式起点，再回退到 MaxID。
func WithTailStartCursor(id uint64) TailerOption {
	return func(t *Tailer) {
		t.startCursor = id
		t.useMaxID = false
	}
}

// WithTailCursorStore 装配游标持久化接缝（issue #183）。
//
// 装配后初始化顺序：LoadCursor 命中 ⇒ 从持久化游标起随（重启续随，停机
// 窗口内的事件仍被补灌）；未命中 ⇒ 回退显式起点 / MaxID（首次部署）；
// Load 失败 ⇒ 停摆重试，**绝不静默回退 MaxID**（那会跳过停机窗口）。
// 游标每前进一拍即 SaveCursor；保存失败只 warn 不停摆（游标落后只会
// 重读，订阅侧幂等吸收）。
func WithTailCursorStore(s CursorStore) TailerOption {
	return func(t *Tailer) {
		if s != nil {
			t.cursorStore = s
		}
	}
}

// WithTailOnGap 注入游标空洞哨兵（issue #183）：Tailer 观测到跳号——
// 返回段的首条 ID 大于游标+1，或段内相邻两条不连续——即回调
// fn(from, to)（from..to = 被跳过的 ID 闭区间）。缺省走包内 warn 日志。
//
// 哨兵响 = TailStore 违反「无空洞推进」契约（乱序提交漏防或行被删），
// 被跳的 ID 大概率**永久丢失**（游标已越过）——宿主该把它接告警/指标，
// 不是当正常路径。回调同步调用、在尾随拍内：不得阻塞、不得回调 Publish。
//
// 接缝契约（宿主注入的哨兵回调；内核在尾随拍里**同步**调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：**宿主侧兑现**。回调在尾随拍内被同步调用：不得回调本包的其它
// 接缝（不得经 Hub.Publish 外发、不得回调 Tailer 的方法）——内核正同步等着
// 它返回，回查即与正在进行的拍互相等待。
// no-blocking：**宿主侧兑现**。回调在尾随拍内被同步等待，不得无界等待
// （不得做长耗时 I/O、不得等其它回调）——它卡住，整拍就卡住。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本回调的 panic（reportGap
// 直接调它，本包无 recover），宿主实现须自行保证不 panic 或在实现内部
// recover 成 error；否则 panic 会掀翻宿主那个跑尾随的后台任务。
func WithTailOnGap(fn func(from, to uint64)) TailerOption {
	return func(t *Tailer) {
		if fn != nil {
			t.onGap = fn
		}
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
// 五步语义：
//
//  1. **初始化游标**：装配了 CursorStore ⇒ 先 LoadCursor——命中即从上次
//     停点起随（#183 重启续随）；未命中回退显式起点 / MaxID；Load 失败
//     停摆重试，绝不静默回退 MaxID（那会跳过停机窗口）。未装配 CursorStore
//     ⇒ 取 MaxID（首拍只读游标、不读存量——本面是**补灌**不是回放，存量
//     窗口回放归连接侧 after-cursor 应答）；初始化失败下一拍重试，不推进
//     任何状态。初始化完成的游标立即落盘（#183）——不落盘，重启会重走
//     MaxID，而停机窗口里游标空间已前进，重走正好把窗口内的事件跳过；
//  2. **读 id > 游标 的新事件回灌**，逐条 Publish 后推进游标；
//  3. **空洞即哨兵响**（#183）：段内出现跳号（首条 > 游标+1 或相邻不连续）
//     时告警——TailStore 违反「无空洞推进」契约，被跳的 ID 大概率永久丢失。
//     事件段照常投递（停摆只会让缺口更大），但失败必须响，不静默；
//  4. **读失败游标不动**：瞬时存储抖动下，下一拍用同一个游标重读
//     （append-only + 无空洞推进契约 ⇒ 幂等补读，不丢不重——重的部分由
//     订阅侧游标吸收）；
//  5. **ctx 取消即退出**：select ctx.Done 是唯一退出式（`for range` 永不
//     返回的教训见 Hub 包注释），ticker 随 defer 停止，无泄漏。
//
// 游标前进后经 CursorStore.SaveCursor 落盘（每拍至多一次）；保存失败只
// warn——游标落后只会导致重启后重读（订阅侧幂等吸收），停摆反而拆了桥。
func (t *Tailer) Run(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	cursor := t.startCursor
	inited := !t.useMaxID
	if t.cursorStore != nil {
		inited = false // 持久化游标面接管初始化（命中/未命中/失败三态在拍内判）
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !inited {
				if t.cursorStore != nil {
					cur, found, err := t.cursorStore.LoadCursor(ctx)
					switch {
					case err != nil:
						// 持久化游标读失败：停摆重试。绝不静默回退 MaxID——
						// 回退就把停机窗口内的事件整个跳过了（宁可停摆不可跳读）。
						log.CtxWarn(ctx, "event-tail 持久化游标读取失败（下拍重试，不回退 MaxID）", logx.ErrKey, err)
						continue
					case found:
						cursor, inited = cur, true
						continue
					default:
						// 未存过游标（首次部署）：回退显式起点 / MaxID 初始化。
					}
				}
				if !t.useMaxID {
					// 显式起点优先（WithTailStartCursor）：CursorStore 未命中时
					// 回退的是这个起点而非 MaxID——否则起点到当前尾部的存量被
					// 整个跳过。初始游标立即落盘。
					inited = true
					if serr := t.cursorStore.SaveCursor(ctx, cursor); serr != nil {
						log.CtxWarn(ctx, "event-tail 初始游标保存失败（重启后将重走初始化）", logx.ErrKey, serr)
					}
					continue // 初始化拍不读存量
				}
				max, err := t.store.MaxID(ctx)
				if err != nil {
					// 初始化失败：游标未知，绝不能拿 0 去 ListAfter（那会
					// 把全量存量当新事件灌进来）。下一拍重试。
					log.CtxWarn(ctx, "event-tail 游标初始化失败（下拍重试）", logx.ErrKey, err)
					continue
				}
				cursor, inited = max, true
				// 初始化的游标立即落盘（#183）：不落盘，重启会重走 MaxID——
				// 而停机窗口里游标空间已前进，回退初始化正好把窗口内的事件跳过。
				if t.cursorStore != nil {
					if serr := t.cursorStore.SaveCursor(ctx, cursor); serr != nil {
						log.CtxWarn(ctx, "event-tail 初始游标保存失败（重启后将重走初始化）", logx.ErrKey, serr)
					}
				}
				continue // 初始化拍不读存量
			}
			evs, err := t.store.ListAfter(ctx, cursor, t.limit)
			if err != nil {
				// 瞬时抖动：游标不动，下一拍补读（append-only 幂等）。
				log.CtxWarn(ctx, "event-tail 读取失败（游标不动，下拍补读）", logx.ErrKey, err)
				continue
			}
			for i := range evs {
				if next := cursor + 1; next != 0 && evs[i].ID > next { // next==0：游标已到 uint64 上界，无从再跳号
					// 跳号 = TailStore 违反「无空洞推进」契约（#183）：
					// 迟到提交/被删的行已被游标越过，大概率永久丢失——
					// 哨兵必须响，不静默。事件段照常投递（停摆只会让缺口更大）。
					t.reportGap(ctx, cursor+1, evs[i].ID-1)
				}
				t.hub.Publish(evs[i])
				cursor = evs[i].ID
			}
			if len(evs) > 0 && t.cursorStore != nil {
				if serr := t.cursorStore.SaveCursor(ctx, cursor); serr != nil {
					// 保存失败不停摆：游标落后 ⇒ 重启后重读（订阅侧幂等吸收），
					// 方向安全；停摆反而把实时补灌桥拆了。
					log.CtxWarn(ctx, "event-tail 游标保存失败（重启后可能重读，不丢不跳）", logx.ErrKey, serr)
				}
			}
		}
	}
}

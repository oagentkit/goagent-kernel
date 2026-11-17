// durable.go —— 持久化事件发布接缝（issue #160：宿主两份手写拷贝的下沉）。
//
// # 为什么要有个 DurablePublisher
//
// 宿主实证（bianque）：「事件落库拿单调游标 → 广播；落库失败不广播」这条
// 次序不变量有两份同构手写拷贝——服务侧出口（internal/service/common/
// systevent.go 的 InsertEvent→Broadcast）与引擎侧出口（internal/engine/
// scheduler/publish/publisher.go 的 Pub）。两份拷贝承载的是同一条契约：
//
//   - SSE after 游标从此只有 bq_event ID 一个单调空间——广播帧必须携带
//     **存储铸造**的权威 ID（跨重启、跨副本都成立）；
//   - 落库失败放弃广播——**宁可 SSE 少一帧，不伪造游标 ID**；
//   - 失败经 onDrop 回调留痕，不静默。
//
// 各宿主各写一遍必然各判一套（ctx/超时差异、失败口径、是否留痕全凭自觉），
// 正是本包「契约单源」定位要消除的漂移面——下沉之后，宿主只剩一份 Store
// 实现加一次装配。
//
// # 纯内存章程的扩展边界（issue (e) 风险节，必须写死）
//
// 本包章程是**纯内存事件面**；持久化发布以**可选 durable 面**扩展，边界三条：
//
//  1. **包零 DB 依赖**：Store 接口由宿主实现，本包不 import 任何存储驱动、
//     不认任何 DB 方言、不出现任何 SQL——存储如何达成「单调唯一游标」
//     是宿主的事（DB 自增/序列天然覆盖唯一与单调；但「单调唯一」不等于
//     「提交可见序」，与尾随读侧配对的形态收口见 TailStore 契约——#264）；
//  2. **可选**：不装配 DurablePublisher 的宿主，Bus/Hub 的纯内存语义
//     一字不变（本文件不给 Bus/Hub 加任何字段）；
//  3. **判定留包内、状态归存储**：次序不变量（先落库→取 ID→广播；失败
//     →不广播+留痕）收口在本文件，权威游标 ID 由存储铸造——与 #145 落的
//     commitgate「令牌由存储铸造」、ticket「TTL 判定留包内、票据状态归
//     Backend」同一条分工。
//
// # 脱敏/字段裁剪等宿主差异不越界（issue (e) 风险节）
//
// 两份宿主拷贝的 redaction 管线不同（publisher 多数值快照回填），下沉**只收
// 次序不变量**：事件内容（脱敏、裁剪、payload 补齐）在调用方构建 Event 时
// 完成，或经宿主自己的 Store 实现在 Insert 内部处理——本包不改写语义字段
// （仅做 Level/OccurredAt 零值归一，与 Bus 既有缺省行为同口径，见 Publish）。
package eventbus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yi-nology/goagent-kernel/internal/subhub"
)

// Store 持久化存储接缝（宿主实现；issue #160，形态与 #145 的
// commitgate.Store / ticket.Backend 同风格）。
//
// 契约五条（含 #400 要求的三条接缝约束）：
//   - **游标由存储铸造**：Insert 返回的 uint64 是权威游标 ID（bq_event 式
//     自增/序列），单调唯一由实现保证；传入事件的 ev.ID 是输入侧遗留值，
//     一律忽略（DurablePublisher 在入口已清零，防御陈旧游标流入）。
//   - **基础设施故障 ⇒ error**：本包收到 error 即放弃广播——「不广播 +
//     onDrop」是契约的一部分，不是本包的实现选择；实现不得在 error 时
//     仍然写入了行（部分写入由实现的事务性负责）。
//   - **不得重入**：Insert 不得回调 DurablePublisher.Publish——发布按 ticket
//     串行（见 DurablePublisher 的并发序说明），重入等价于排在自身之后等待，
//     即自死锁；与 Hub.Relay 的「不得回流」同款红线。
//   - **不得阻塞**：Insert 在发布轮次内同步调用；实现须遵守 ctx 的超时/取消
//     并自行设上界，不得无限等待——发布面按 ticket 串行，一次挂起会拖住
//     后续全部发布（慢存储的代价已被显式接受，无上界的阻塞不被接受）。
//   - **panic 隔离**：Insert 的 panic 由内核 recover 成 error（走「不广播 +
//     onDrop」路径），宿主驱动/连接池/序列化异常不得掀翻调用方；实现仍应
//     以事务性保证 error/panic 时不留半行（与上一条「error 不得仍写入行」
//     同一要求）。
//
// # 与尾随读侧的配对（issue #264）
//
// 本接缝只覆盖写侧的「落库拿权威 ID」；配对读侧 TailStore 的「无空洞推进」
// 把被承诺支持的生产形态收口为 **Outbox**——事件行与业务状态同事务提交、
// ID 分配经已提交水位线串行化（铸造序 == 提交序、回滚不烧号）。自增/序列
// 直读 + WHERE id > cursor 的读法被显式声明不支持（丢帧与停摆两类后果见
// TailStore 契约）。与业务状态同事务的事件写入不在本接缝的形状里——事务
// 句柄归宿主、本包零 DB 依赖：宿主在自己的业务事务内调存储的事务内写入
// 入口，提交后再广播（广播在事务外，回滚后的幻影帧由此排除）。参考实现见
// examples/pgstore 的 EventStore（PublishInTx / Insert / TailStore 三张接缝）。
type Store interface {
	// Insert 持久化一条事件并返回权威游标 ID（语义同 SQL 的
	// INSERT ... RETURNING id）。ctx 的超时/取消由实现自判（超时 ⇒ error，
	// 走 drop 路径）。
	Insert(ctx context.Context, ev Event) (uint64, error)
}

// MemoryStore 进程内参考实现（并发安全）：自增游标 + 内存留痕。
//
// 无持久化——重启即丢、跨进程不成立，**不得上生产**（生产宿主接 DB；
// 进程内也拿不出跨副本单调空间）。职责是把次序不变量垫在契约之下让契约
// 可测——与 ticket.NewMemoryBackend、commitgate.NewMemoryStore 同口径
// （内核证契约，宿主接生产）。
type MemoryStore struct {
	mu  sync.Mutex
	seq uint64
	evs []Event
}

// NewMemoryStore 构造进程内参考实现（游标从 1 起）。
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Insert 自增铸造游标并留痕（行内的 ID 以铸造值为准——同 INSERT ... RETURNING）。
func (s *MemoryStore) Insert(_ context.Context, ev Event) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	// 入口深拷贝：落库行是独立数据，调用方之后复用/改写载荷不得改穿账面
	// （与广播面同一口径，issue #378）。
	row := ev.Clone()
	row.ID = s.seq
	s.evs = append(s.evs, row)
	return s.seq, nil
}

// DurablePublisher 持久化事件发布的收口点：把「insert→取 ID→broadcast；
// insert 失败→不广播+onDrop」的次序不变量收进包内（issue #160）。
//
// # 次序不变量（本包唯一收口的语义）
//
//  1. 先落库：Store.Insert 成功 ⇒ 拿到权威游标 ID；
//  2. 再广播：广播帧携带存储铸造的 ID（不重编号、不预分配），且与落库行
//     **同一内容**——Level/OccurredAt 的零值归一发生在 Insert 之前，
//     订阅者看到的帧就是持久化的那行；
//  3. 落库失败 ⇒ 放弃广播 + onDrop 留痕——宁可 SSE 少一帧，不伪造游标 ID。
//
// # 并发序：广播序 == 游标序（比宿主拷贝强的一档，代价写明）
//
// 发布按 **ticket 串行**：并发 Publish 领号后依次获得「调用 Insert 并广播」
// 的独占轮次，广播顺序与游标顺序严格一致——每个订阅者看到的 ID 严格递增，
// SSE after 游标的「唯一单调空间」在**实时面**同样成立（宿主两份拷贝只能
// 保证各自先 insert 后 broadcast，并发下广播序与游标序可能交错：
// A insert=1 → B insert=2 → B 广播 → A 广播，订阅者先见 2 后见 1）。
//
// 锁内只留判定（issue #399）：mu 只保护领号、轮次判定与广播簿记；宿主
// Store.Insert 在**锁外**调用——慢存储（DB 抖动）不再把互斥锁按在宿主
// 调用上，宿主 panic 也不会在锁内掀翻（panic 已转成 error，见 insertStore）。
// 代价是 Insert 仍被串行化：事件是人的可读留痕而非高频指标，串行不是瓶颈；
// 但 Insert 慢时全体发布排队等它——这是次序保证的固有代价，不藏。广播本身
// 非阻塞（满缓冲丢帧计数），持锁广播不会卡在慢订阅者上。
//
// 零值不可用，用 NewDurablePublisher 构造。
type DurablePublisher struct {
	bus   *Bus
	store Store

	// mu 排队簿记锁：nextTicket/serving 与广播次序由它保护。**不覆盖宿主
	// Store.Insert**（issue #399：锁内只留判定）；onDrop 也在任何锁之外
	// 调用——失败路径的旁路留痕不参与次序不变量（issue #328）。
	mu   sync.Mutex
	cond *sync.Cond // 轮次信号：serving 推进时唤醒下一位（与 mu 绑定）

	nextTicket uint64 // 下一张待发票号（Publish 入口在 mu 内领取）
	serving    uint64 // 当前持有独占轮次的票号（锁内推进）

	onDrop func(ev Event, cause error)
}

// DurableOption 发布器可选项（见 WithOnDrop）。
type DurableOption func(*DurablePublisher)

// WithOnDrop 注入落库失败留痕钩子（缺省 nil ⇒ 静默丢弃，与宿主拷贝的
// onDrop 可选形一致）。
//
// 同步调用、在发布轮次**之外**（失败不广播，onDrop 不参与次序不变量）：
// 回调可以安全查询发布面；但仍不得回调 Publish/Insert——那会再走一次落库，
// 失败时递归 onDrop。panic 已由本包隔离（与 internal/subhub 的回调纪律同款）
// ——宿主留痕钩子的异常不得掀翻发布面。本包不重试——重试会把「失败不广播」
// 变成「失败晚广播」，次序不变量就破了；重试/死信/告警是留痕钩子宿主侧的事。
func WithOnDrop(fn func(ev Event, cause error)) DurableOption {
	return func(p *DurablePublisher) { p.onDrop = fn }
}

// NewDurablePublisher 构造：bus 是广播面，store 是持久化接缝，均必填。
//
// store 为 nil ⇒ panic：没有存储的「持久化发布」只能广播一个非权威 ID——
// 伪造游标正是本结构要防的事，fail fast 在装配点，不留到运行时。bus 为 nil
// 同理（没有广播面的落库是单向写库，不经本包——直接调宿主 Store 即可）。
func NewDurablePublisher(b *Bus, store Store, opts ...DurableOption) *DurablePublisher {
	if b == nil {
		panic("eventbus: DurablePublisher 需要 Bus（nil bus 的落库不经本包，直接调宿主 Store）")
	}
	if store == nil {
		panic("eventbus: DurablePublisher 需要 Store（nil store 即广播无铸造游标的帧——伪造游标）")
	}
	p := &DurablePublisher{bus: b, store: store}
	p.cond = sync.NewCond(&p.mu)
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Publish 落库并广播一条事件：成功 ⇒ 返回广播帧（ID=权威游标，含零值归一），
// 供调用方直接用作 SSE 游标；失败 ⇒ 零值事件 + insert 错误，不广播
// （onDrop 已留痕）。
//
// 宿主 Store.Insert 的 panic 由本包隔离成 error（issue #399）——失败路径与
// 返回 error 的路径完全一致，不掀翻调用方。
//
// 字段口径（归一发生在 Insert 之前，落库行与广播帧同一内容）：
//   - ev.ID 输入侧一律**清零忽略**——游标由存储铸造（Store 契约），
//     调用方拷贝里的陈旧 ID 不得流入广播帧；
//   - Level 空 ⇒ info，OccurredAt 零值 ⇒ 当前时刻——与 Bus.Publish 的
//     既有缺省行为同口径，本包不新增字段语义。
func (p *DurablePublisher) Publish(ctx context.Context, ev Event) (Event, error) {
	ev, err := p.publishSerialized(ctx, ev)
	if err != nil {
		// 失败路径的旁路留痕在任何锁之外（issue #328）：钩子慢不占发布锁、
		// 钩子 panic 由 subhub.Do 隔离，不得掀翻发布面。
		p.drop(ev, err)
		return Event{}, err
	}
	return ev, nil
}

// publishSerialized 一次发布轮次：领号排队 →（锁外）归一 + Insert →（锁内）
// 广播 + 放行下一位。
//
// 返回的 Event 在失败路径是**归一后**的事件（onDrop 要看到的形态），成功
// 路径是广播帧。锁内只做簿记（issue #399）；宿主 Insert 的 panic 由
// insertStore 转为 error，轮次照常推进——不会把后续发布一起卡死。
func (p *DurablePublisher) publishSerialized(ctx context.Context, ev Event) (Event, error) {
	// 锁内：领号并等轮到自己。同步 Cond 的 Wait 在等待期间释放 mu，
	// 因此排队不持有排他锁；轮次推进由前一位在锁内 Broadcast。
	p.mu.Lock()
	ticket := p.nextTicket
	p.nextTicket++
	for p.serving != ticket {
		p.cond.Wait()
	}
	p.mu.Unlock()

	// 锁外：入口深拷贝 + 归一 + 宿主 Insert（panic 隔离在此发生）。
	// 深拷贝（issue #378）：发布方在 Publish 返回后复用入参 map 是常见写法，
	// 返回帧、落库行、广播帧都不得与它共享。
	ev = ev.Clone()
	ev.ID = 0 // 游标由存储铸造：输入侧 ID 一律忽略（防御陈旧游标伪造）
	if ev.Level == "" {
		ev.Level = "info"
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now()
	}

	// 交给存储的是**另一份独立快照**：Store 实现可能异步留存/落库，而本发布器
	// 之后还要用 ev 广播与返回——两份数据不得互相改穿（Store 契约不要求实现
	// 深拷贝调用方入参，故边界拷贝由本包负责）。
	stored := ev.Clone()
	id, err := insertStore(ctx, p.store, stored)

	// 锁内：广播帧（成功路径）并放行下一位。广播不调用宿主代码，
	// 满缓冲丢帧计数、不阻塞在慢订阅者上。
	p.mu.Lock()
	if err == nil {
		ev.ID = id
		p.bus.Broadcast(ev)
	}
	p.serving++
	p.cond.Broadcast()
	p.mu.Unlock()

	if err != nil {
		return ev, err
	}
	return ev, nil
}

// insertStore 调用宿主存储并把 panic 转成 error（issue #399）。
//
// 与 onDrop 的 subhub.Do 同一条纪律：宿主接缝的异常不得掀翻发布面。区别是
// Insert 有返回值，故 recover 必须转成 error（走「不广播 + onDrop」路径），
// 不能像 onDrop 那样静默吞掉——否则失败会变成一条「成功」的发布。
func insertStore(ctx context.Context, store Store, ev Event) (id uint64, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("eventbus: Store.Insert panic 已隔离: %v", r)
			id = 0
		}
	}()
	return store.Insert(ctx, ev)
}

// drop 落库失败留痕（nil 钩子静默跳过）。panic 隔离与 internal/subhub 的
// 回调纪律同款：留痕钩子的异常不得把一次「已确定失败」的发布变成进程级
// 事故（调用方的发布路径也随之中断）。
func (p *DurablePublisher) drop(ev Event, cause error) {
	if p.onDrop == nil {
		return
	}
	subhub.Do(func() { p.onDrop(ev, cause) })
}

// 处置策略变更订阅：把「换过没有」从**可查询**变成**可响应**。
//
// # 为什么单指针还不够
//
// 上面那份 Policy 解决的是**读的一致性**：一次 Load 拿到的就是同一份策略，不会读到
// 「面已关却回报阻断」这种从未被任何写侧设置过的组合。但它只让读侧自洽，写侧的
// 下游仍然是空的——Gen() 能回答「换过没有」，回答不了「换成了什么」，宿主只能自己
// 起轮询比对，而两次比对之间若换了两次，代次只说得出「变了」。
//
// 订阅把这件事一次做完：变更发生的**那一刻**由写侧把新快照推给订阅者，收到即所见
// ——回调参数带只读访问方法（policy.go），读到的是**收到的那一代**，不用再借
// 全局访问器（那读的是此刻的当前代，可能已经又换了）。
//
// # 订阅表的机制不在本文件
//
// 「快照式替换的订阅表 + 铸造句柄 + 锁外通知 + 回调 panic 隔离」这套机制在
// internal/subhub，与 netguard、guardset 共用**同一份实现**。它此前在本仓有三份
// 近乎逐字相同的副本，而这是一段并发正确性代码：只要有一份先被修，另外两份不会
// 跟着改，同一个不变量于是在三个包里有三种成色，失效形态是**偶发漏通知 /
// 偶发误留订阅**——不红不报，只在长跑进程里慢慢显形。
//
// # 为什么同一订阅者的投递要过闸（#296）
//
// subhub 的 Notify 把帧直接调进回调，两次通知并发时（多位写侧同时换策略），同一
// 订阅者的两次回调可以**并发进入**：A 的 Invoke(gen2) 还在回调里重建派生视图，
// B 的 Invoke(gen3) 已并发执行——订阅者先记录 gen3、后记录 gen2，观察序列回退
// （subscribe_order_test.go 在直投实现下可稳定复现 gen4 → gen2）。回退的后果不是
// 多收一帧：订阅者按帧重建的派生视图最终停在**旧代**，与全局策略永久失配。
//
// 故每个订阅者过一道投递闸 policySub：同一订阅者的回调**永不并发**，按代次升序
// 投出；并发写侧的帧在闸前排队（qmu），由在途投递收尾按序清空；乱序迟到的旧帧
// （其代次 ≤ 已投出的最大代次）直接丢弃——订阅者手里的快照永远不比它已见过的旧。
//
// # 为什么挂队的帧「挂队即回」而不是等投完
//
// 订阅者回调里再调 SetStrategy 是**文档承诺**（见下）。那一刻回调 goroutine 正持着
// 本订阅的投递权（回调在闸内执行），若它触发的通知帧在本闸前等待自己投完，就是
// 自己等自己——死锁。故挂队即回：递归产生的帧由在途投递（就是调用者自己）在回调
// 返回后按序补投。busy 旗 + 退出双检保证「挂队后没人来收」的滞留窗口不存在：
// 收尾方先摘旗、再复查一次队列，两者之间入队的帧读到 false，自己接手清队。
//
// # 为什么回调参数是 Policy 值而不是 *Policy
//
// 读侧 CurrentPolicy() 已经是值返回，订阅沿用同型：订阅者拿到的是一份独立副本，
// 写侧后续换策略不影响它手里的那份，订阅者也无需操心指针的存活期。代价是订阅者
// 想长期留档就得自己留副本——这是显式的，优于「指针还能用吗」这种隐式问题。
//
// # 为什么订阅者 panic 不得影响策略面
//
// 一次成功的 CAS **已经落地**，此时让某个订阅者抛异常就把整次变更说成「没发生」，
// 等于把已经发生的事实改写成没发生——而这恰恰是本文件要消灭的那类失效。故逐个
// recover（实现见 subhub.Invoke），让其余订阅者继续收到通知。
//
// 另一条同源的纪律：订阅者回调里再调 SetStrategy 是**允许**的（无锁，不会自锁；
// 投递闸让递归帧挂队即回，也不会死锁），但那会再触发一轮通知，递归深度由订阅者
// 自己收敛，本包不做环检测。
//
// # 为什么句柄是本包自己的类型而不是 subhub.Handle 的别名
//
// 别名会让 pii.Subscription 与 netguard.Subscription 变成**同一个类型**：把
// netguard 的句柄传给本包 Unsubscribe 将编译通过，然后静默什么也不注销。句柄各自
// 定义成独立命名类型（底层仍是 uint64），跨包误用仍是编译错误——这正是句柄存在
// 的意义（「这次注销是不是我登记的那一次」），别名把它抹掉了。

package pii

import (
	"cmp"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/internal/subhub"
)

// Subscription 订阅句柄，传给 Unsubscribe 注销。0 表示无效。
type Subscription uint64

// ErrNilSubscriber 订阅回调为空。
//
// 登记一个必然 panic 的订阅者毫无意义，故直接报错并返回零句柄：调用方能**凭返回值**
// 知道订阅没生效，而不是等到第一次变更时才发现「原来一直没跟上」。
//
// 判在**本包**而不是 subhub：三个使用方各有自己的哨兵与语境文案，共用一个会让
// `errors.Is(err, pii.ErrNilSubscriber)` 这类既有调用点判定链断掉。
var ErrNilSubscriber = errors.New("处置策略订阅回调为空")

// subs 处置策略变更订阅表。零值可用。表里挂的是投递闸（policySub），不是裸回调。
var subs subhub.Registry[Policy]

// policySub 单个订阅者的投递闸：串行化投递 + 代次去旧 + 忙时挂队。
//
// # 状态与锁的分工
//
//   - mu：投递权。持有者按代次升序把帧调进回调，同一订阅者的回调永不并发；
//   - busy：mu 被持有的旗（atomic）。挂队方用它判定「在途投递会不会来收我」；
//   - qmu/pend：忙时到达的帧的暂存队列。qmu 永不与回调同持——回调可再调
//     SetStrategy 再触发通知，通知在 qmu 之外挂队，不死锁；
//   - last：已投出的最大代次（mu 内），投递去旧的唯一基准。
type policySub struct {
	fn   func(Policy)
	mu   sync.Mutex
	busy atomic.Bool
	qmu  sync.Mutex
	pend []Policy
	last uint64
}

// deliver 投递一帧。拿不到投递权就挂队，见 policySub 与本文件头注释。
func (g *policySub) deliver(p Policy) {
	if !g.mu.TryLock() {
		g.enqueue(p)
		return
	}
	g.busy.Store(true)
	g.drain(p)
	g.mu.Unlock()
}

// enqueue 忙时到达的帧：挂队即回，随后按 busy 旗决定要不要自己接手清队。
func (g *policySub) enqueue(p Policy) {
	g.qmu.Lock()
	g.pend = append(g.pend, p)
	g.qmu.Unlock()
	if g.busy.Load() {
		return // 在途投递还没走到退出双检，本帧必被它收走
	}
	// busy 已摘：在途投递的退出双检早于本帧入队，没人来收了——自己接手。
	// TryLock 暂时失败只说明又有新一轮投递者持锁（它同样会清队），重试到
	// 接手为止；持锁方必然收尾，故循环必然终止。
	for !g.mu.TryLock() {
		runtime.Gosched()
	}
	g.busy.Store(true)
	g.drain(Policy{}) // 本帧已在队里；零值帧在 deliverOne 里按代次去旧跳过
	g.mu.Unlock()
}

// drain 持锁清空：先投首帧，再按代次升序清挂队帧；队空后走退出双检——
// 先摘 busy 旗、再复查一次队列，两者之间入队的帧读到 false 会自己接手。
func (g *policySub) drain(p Policy) {
	for {
		g.deliverOne(p)
		frames := g.takePend()
		if len(frames) == 0 {
			g.busy.Store(false)
			if len(g.takePend()) == 0 {
				return
			}
			g.busy.Store(true)
			continue
		}
		for _, f := range frames {
			g.deliverOne(f)
		}
		p = Policy{} // 首帧已投，后续轮次只清队列
	}
}

// deliverOne 投递单帧：代次去旧 + panic 隔离。乱序迟到的旧帧（代次 ≤ 已投出的
// 最大值）直接丢弃——它的内容已被更新的快照覆盖，投出去只会把订阅者的派生视图
// 拖回旧代。零值帧（gen 0）恒小于 last，自然被这里挡住。
func (g *policySub) deliverOne(p Policy) {
	if p.gen <= g.last {
		return
	}
	g.last = p.gen
	subhub.Invoke(g.fn, p)
}

// takePend 取走暂存帧并按代次升序排序：并发写侧的入队顺序与代次顺序无关，
// 投递顺序必须只由代次决定（确定性，不依赖调度）。
func (g *policySub) takePend() []Policy {
	g.qmu.Lock()
	frames := g.pend
	g.pend = nil
	g.qmu.Unlock()
	if len(frames) > 1 {
		slices.SortFunc(frames, func(a, b Policy) int { return cmp.Compare(a.gen, b.gen) })
	}
	return frames
}

// Subscribe 登记一个处置策略变更订阅者，返回句柄与是否成功。
//
// # 为什么注册完立刻回调一次当前快照
//
// 不这么做，订阅者对「现在生效的是什么」一无所知，要等到下一次变更才知道。策略注入
// 的典型形态恰恰是「注册完之后很久没有下一次变更」，于是订阅者带着一个从没填过的
// 视图上线——比没有订阅更危险，因为它以为自己已经跟上了。
//
// 注册先于立即回调：宁可让订阅者多收一次（立即回调给旧快照、紧接着的变更通知给新
// 快照，顺序仍单调向前），也不能在两者之间漏掉一次变更。立即回调走与变更通知
// **同一条投递闸**：并发写侧的变更不会把更旧的一代抢在它前面送进同一个订阅者。
//
// 立即回调同样在锁外调用、panic 同样被 recover，与变更通知完全一致。
func Subscribe(fn func(Policy)) (Subscription, error) {
	if fn == nil {
		return 0, ErrNilSubscriber
	}
	g := &policySub{fn: fn}
	h := Subscription(subs.Add(g.deliver))
	g.deliver(CurrentPolicy())
	return h, nil
}

// Unsubscribe 注销订阅。重复注销同一句柄是安全的空操作。
//
// 有了它，订阅者才能在长生命周期场景下解绑：否则回调闭包连同它捕获的一切会被订阅表
// 永久留住，而这条泄漏不会自己显形。
func Unsubscribe(h Subscription) {
	subs.Remove(subhub.Handle(h))
}

// notifyPolicy 把新快照推给全部订阅者。**由写侧在 CAS 成功之后、锁外调用。**
//
// 遍历的是一次快照：并发增删订阅者不会让本次通知看到半张名单，且同一次变更对每个
// 订阅者送出的都是**同一份**策略——与「不可撕裂读」同源。各订阅者的投递闸互相
// 独立：闲闸立即投出，忙闸挂队由其在途投递按代次收尾，单个慢订阅者不拖住写侧。
func notifyPolicy(p Policy) {
	subs.Notify(p)
}

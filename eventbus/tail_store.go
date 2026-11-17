// tail_store.go —— 尾随读侧的**存储接缝契约面**（TailStore / WatermarkTailStore /
// CursorStore）；从 tail.go 拆出以守住文件行数阈值（与 tail_gap.go 的拆法同款）。
//
// 划界按**职责**：本文件只放「宿主实现的读侧契约」，尾随器本体（Tailer 与
// 它的可选项）在 tail.go。两者之间的关系写在各自文档里，不在本文件复刻第二份。
package eventbus

import (
	"context"
)

// TailStore 尾随读侧存储接缝（宿主实现；issue #166，形态与 durable.go 的
// Store 同风格：判定留包内、状态归存储、包零 DB 依赖）。
//
// 契约四条：
//   - **游标空间 append-only 且单调**：ListAfter 只回 id > cursor 的行，
//     且**必须按 ID 升序**——游标推进建立在这个序上（乱序返回会让游标
//     回退或跳号，两者都不在本包的观测面内）；
//   - **「ID 序 == 提交可见序」（无空洞推进，硬要求；issue #183，#264 收口）**：
//     ListAfter 返回的段从 cursor 之后第一条起必须**连续可见**——只要存在
//     「已铸造但本拍不可见」的 ID（典型：DB 序列下小 ID 的事务尚未提交），
//     就**不得返回比它大的行**。违反这条 ⇒ 迟到提交的小 ID 被永久跳过
//     （游标已越过它），且是静默丢帧——这正是「读失败游标不动 ⇒ 不丢」
//     的隐含前提。**被承诺支持的生产形态只有 Outbox**（#264）：事件行与
//     业务状态同事务提交，ID 分配经「已提交水位线」串行化（铸造序 ==
//     提交序，回滚不烧号 ⇒ 序列空洞结构性地不存在），读取面以水位线为
//     上界；实现声明面 = WatermarkTailStore（实现它即声明本形态，可机判
//     口径见其文档）。**显式不支持**：「自增/序列 ID 直读」（BIGSERIAL +
//     WHERE id > cursor）——乱序提交让迟到的小 ID 被游标永久越过（丢帧），
//     回滚烧号让空洞永不回填（尾随永久停摆，后续帧永不送达，与丢帧等效），
//     两类丢失都不在调用方可修。「观测到空洞即截断返回段」是守约读侧的
//     可观测行为，但配上烧号的分配器只会永久停摆，不构成被承诺的实现。
//     Tailer 的 WithTailOnGap 是对违约的**哨兵**（响即实现已违约），不是
//     兜底机制——告警不是兜底（#264 建议方向 4）；
//   - **行→Event 的转换归实现**：ID 取存储铸造的权威游标，Payload 已是
//     解析好的载荷（与 DurablePublisher 的 Insert 入参转换对称——那边是
//     发布前构造，这边是读出后还原）。本包**不改写**回读到的字段（权威行
//     不是本包的产物，改写即篡改）；
//   - **ctx 超时/取消由实现自判**：读失败返回 error 且**不得返回部分
//     结果**——本包收到 error 即游标不动，返回了一半会让「失败」与
//     「成功读到前半段」不可分。
//
// 接缝契约（宿主实现；内核在尾随拍里**同步**调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现不得回查本包（不得回调 Tailer 的方法、不得在读法里
// 经 Hub.Publish 回灌）——内核正同步等着它返回，回查即与正在进行的尾随拍
// 互相等待。内核侧**不持任何锁**（Tailer 无共享可变状态），故回查不自锁。
// no-blocking：读法必须**有界**——承接 ctx 的取消/超时并在期限内返回，不得
// 无限等待、不得在持存储内部锁时等待别的回调。尾随拍是同步的：读法慢则
// 整拍慢（这是「读失败游标不动」的另一面——宁可慢，不可跳读）。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本接口的 panic（本包无
// recover，Tailer.Run 直接调它），宿主实现须自行保证不 panic 或在实现内部
// recover 成 error；否则 panic 会掀翻宿主那个跑尾随的后台任务。
type TailStore interface {
	// MaxID 当前权威游标空间的**无空洞边界**（空表 = 0）。只在初始化时
	// 调用一次。Outbox 形态下它就是已提交水位线（与 WatermarkTailStore
	// 的 CommittedWatermark 同值同源）；返回更大的值（如裸 max(id)）⇒
	// 初始化本身就把在途小 ID 跳过——与 ListAfter 的跳号违约同罪（#264）。
	MaxID(ctx context.Context) (uint64, error)
	// ListAfter 列出 id > cursor 的事件（limit 为单拍上限，升序）。
	//
	// 返回段必须满足上面的「无空洞推进」硬要求：从 cursor+1 起连续可见，
	// 有「已铸造未可见」的 ID 卡在中间时，只返回它之前的段（可以是空段）。
	ListAfter(ctx context.Context, cursor uint64, limit int) ([]Event, error)
}

// WatermarkTailStore TailStore 的可选能力扩展：已提交水位线读法（issue #264）
// ——「无空洞推进」契约的**实现声明面**。
//
// 「已提交水位线」= 游标空间里「≤ 此值的 ID 已全部提交且可见」的最大值。
// Outbox 形态（TailStore 契约点名的唯一被承诺支持的生产形态）由写侧维护它：
// 事件行与业务状态同事务提交，水位线推进与事件行同刻可见——读取面以它为
// 上界，结构性地排除「已铸造未可见」的 ID 卡进返回段。
//
// 实现本接口 = 声明自己是 Outbox 形态。声明与实现不符（实现了接口但
// ListAfter 仍可能越过水位线返回跳号段）本身就是缺陷。可机判口径三条：
//   - 水位线只在提交时推进：在途铸造不推它；回滚既不推它也不烧号（下一
//     次分配复用同一 ID）——序列空洞结构性地不存在；
//   - ListAfter 的返回段不得越过水位线：水位线之上哪怕已可见（宿主绕过
//     写入协议落的行）也扣住不返——fail-closed 方向是扣住，不是放行；
//   - MaxID 与本读法同值同源（初始化与逐拍读法共用同一条无空洞边界）。
//
// Tailer 不经本接口改变算法（守约存储下 ListAfter 的水位线上界已把哨兵
// 钉在违约侧）；本接口给宿主的是监控/对账的独立读位——「水位线推进延迟」
// 直接可测，无需走 ListAfter 路径。
//
// 接缝契约（宿主实现；**内核不调用本接口**——它是宿主对自身的声明面）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：**宿主侧兑现**。Tailer 不经本接口改变算法（见上），故内核侧
// 没有调用点、也就没有内核锁可指：调用方是宿主自己在监控/对账路径上读它，
// 宿主实现不得在读法里回调本包的其它接缝（否则与正在进行的读互相等待）。
// no-blocking：**宿主侧兑现**。读法必须**有界**——承接 ctx 的取消/超时并在
// 期限内返回，不得无限等待、不得在持存储内部锁时等待别的回调。
// panic-isolated：**宿主侧兑现**。内核不碰本接口（无调用点 ⇒ 无隔离点），
// 宿主实现须自行保证不 panic 或在实现内部 recover 成 error。
type WatermarkTailStore interface {
	TailStore
	// CommittedWatermark 返回已提交水位线（≤ 此值的游标 ID 已全部提交
	// 且可见）。ctx 的超时/取消由实现自判（读失败返回 error 且不得返回
	// 半个水位线——调用方拿到 error 即视为未知，不得当作任何游标使用）。
	CommittedWatermark(ctx context.Context) (uint64, error)
}

// CursorStore 游标持久化接缝（宿主实现，可选；issue #183）。
//
// Tailer 的游标是 Run 的局部状态，停机即失。缺省（不装配本接缝）重启后走
// MaxID 初始化——**停机窗口内他副本发布的事件不会补灌**（从当前尾部起随，
// 是「补灌不是回放」的既有语义）。宿主要「重启后从上次停点续随」，就把
// 游标存进自己的存储并经 WithTailCursorStore 装配本接缝。
//
// 接缝契约（宿主实现；内核在尾随拍里**同步**调用它）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现不得回查本包（不得回调 Tailer 的方法）——内核正同步
// 等着它返回，回查即与正在进行的尾随拍互相等待。内核侧**不持任何锁**
// （Tailer 无共享可变状态），故回查不自锁。
// no-blocking：读法必须**有界**——承接 ctx 的取消/超时并在期限内返回，不得
// 无限等待、不得在持存储内部锁时等待别的回调。
// panic-isolated：**宿主侧兑现**——内核**不隔离**本接口的 panic（本包无
// recover，Tailer.Run 直接调它），宿主实现须自行保证不 panic 或在实现内部
// recover 成 error；否则 panic 会掀翻宿主那个跑尾随的后台任务。
type CursorStore interface {
	// LoadCursor 读上次保存的游标。found=false 表示从未存过（首次部署）
	// ——此时 Tailer 回退既有初始化（显式起点 / MaxID）。
	//
	// error ⇒ Tailer 停摆重试（每拍再试），**绝不静默回退 MaxID**：回退
	// 就会把停机窗口内的事件整个跳过——宁可停摆不可跳读。
	LoadCursor(ctx context.Context) (cursor uint64, found bool, err error)
	// SaveCursor 游标推进后保存（每拍至多一次，只在游标前进时调用）。
	//
	// 失败只 warn 不停摆：游标落后只会导致重启后**重读**（订阅侧游标幂等
	// 吸收，不丢不重——重读方向安全），停摆反而把实时补灌桥拆了。
	SaveCursor(ctx context.Context, cursor uint64) error
}

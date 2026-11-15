// Package reconcile 启动孤儿对账与心跳租约死主回收：非计划死之后，上一具
// 进程尸体名下的在途编排会话怎么收敛（issue #165）。
//
// 生命周期: Experimental（v0.38.0 定级）——三路判定与 Store 接缝刚下沉，
// 形状会随宿主适配（bianque recovery 三入口）的接线反馈调整；升级风险自负。
//
// # 问题形态
//
// 多副本部署下进程死掉后，它名下 running 的会话**不会自行推进**——会话卡
// running、后继输入只进队列无人消费（上一进程已死，没人再消费队列）。drain
// 的包注释两处明写「强制路径按宿主的进程停机分支处理（不写终态，**留给启动
// 对账**）」「排水超时：强制中断（**留启动对账收敛**）」——本包正是 drain
// 显式留给宿主、内核至今空缺的那半边：**drain = 计划内停机，reconcile =
// 非计划死，成对件**。
//
// # 三路判定的语义差异
//
// 三路收敛动作完全一致（见 reclaim），差别全在「凭什么说它死了」：
//
//  1. **启动锚点路**（RecoverBootOrphans）：进程启动即替代同 instanceID 的
//     上一具尸体——此刻名下 running 会话**定义性地是孤儿**（上一进程已死，
//     不会再产出任何事件），所以**无须等事件静默**（常规路缺省 10m）才定性。
//     旧路径下这段窗口正是「后继输入排队死锁」的体验来源：会话卡 running、
//     注入只进队列无人消费。判据只剩一条：最后事件早于本进程启动锚点
//     （boot 后仍有事件产生 = 本进程或同标识存活进程在推进，**天然免检**）。
//  2. **常规路·事件静默子路**（owner=本副本/空）：没有启动锚点可借（进程
//     活着、可能正是它自己在推进），只能等「超阈无任何事件」——活跃编排即
//     使在长步骤期间也有进度事件在持续产生，静默超阈才是死亡证据。本副本
//     **不适用**心跳免检：心跳新鲜的「新进程」没有旧编排的取消句柄，自副本
//     走静默恰是重启孤儿的安全网。
//  3. **常规路·心跳租约子路**（owner=他副本）：先看心跳租约——心跳新鲜
//     （含租约刚过期的重启重打宽限窗）则**免检**（活编排名下不等静默）；
//     租约过期则**不等事件静默立即回收**（死副本的回收从「等满 30m 静默」
//     提前到「租约过期+宽限」）；无心跳行（存量版本副本/心跳面读失败降级）
//     回落事件静默（更宽的 DeadOwner 阈值）。长步骤期间事件可能静默数分钟，
//     心跳是**主动续约**——租约路存在的意义就是让存活副本名下的会话不被他
//     副本按静默阈值误收。
//
// # 误收面（如实声明，issue (e) 原文）
//
// 双进程重叠窗口的静默长步骤存在误收面（常规路径有 recoverStall 缓冲）——
// 生产单副本 rsync 发版（杀旧起新无重叠）不受影响；conf
// cluster.recover_boot_grace 正值=额外宽限（last_event < boot-grace 才收），
// 负=整体关闭（回落纯静默路径）。
//
// # closed_by 归因
//
// 每次收敛都带一个归因标识（Cause.By，与宿主 turn 括号的 closed_by 同值）：
// `boot-reconcile` / `recovery` / `recovery:dead-owner` / `recovery:dead-lease`。
// 它回答「这次是谁、按哪条路把它收掉的」——审计与排障靠它分辨「发版重启的
// 正常收割」与「他副本心跳丢失的死主回收」，**不许把四条路写成同一个标识**。
//
// # 划界（与既有同域包互指；issue #165 划界段逐条）
//
//   - **vs `checkpoint`**（AGENTS.md 已定案不接，恢复语义相反）：checkpoint
//     是「续跑而非重放」；本包是 **deny-by-default 收敛为 failed、不重放
//     任何步骤**（重放=重复环节与二次审批）。方向相反、互不替代——本包不做
//     快照存取与 BuildPlan，checkpoint 也答不了「boot 锚点 / 事件静默 /
//     心跳租约」三路判定。是**补充**。
//   - **vs `drain`**：见上「成对件」。drain=计划内、reconcile=非计划死。
//   - **vs `worker.Recover`**：worker 接管任务板上过期认领的任务并**重跑**；
//     本包收敛的是**编排在途会话**，语义是**不重跑、只落 failed 终态**。
//     对象（任务 vs 会话轮）与动作（重跑 vs 收敛）都不同。
//   - **vs `sideeffect`/`coord` 的「对账」**：那是「结果未知的副作用裁决」；
//     本包是「在途编排工作的孤儿收敛」——同词不同域。
//   - **vs `pending`**：pending 只是挂起态列载体，生命周期外包给宿主，无任何
//     对账机制。
//
// # 边界：挂起审批路不在本下沉面
//
// 现象里提到的 waiting_approval 搁浅收敛（宿主的 RecoverApproved：每 15s 扫、
// 自带 30s 决策宽限）**不在本包**——它要重入执行尾段（受审执行是宿主治域），
// 与本包「只收敛不重跑」的方向不同，issue #165 的 (a) 范围也没点它。
//
// # 装配与依赖
//
// 全部存储事实经 Store 接缝注入（包零 gorm/DB 依赖，与 taskboard 同范式：
// 语义归包、落库归宿主）；状态词表复用 contract（终态语义不定义两次而漂移）；
// 收尾事件走 eventbus 词表与宿主的 Pub（落库+广播同一路）。时钟可注入
// （NewWithClock）：租约过期与静默窗判定要能被确定性测，不靠 sleep。
package reconcile

import (
	"context"
	"time"
)

// 归因标识（Cause.By；宿主 turn 括号的 closed_by 与事件载荷按此分辨收敛路径）。
// 四条路各一个标识——审计靠它分辨「发版重启的正常收割」与「死主回收」。
const (
	// ByBootReconcile 启动锚点路（进程启动对账收割的孤儿）。
	ByBootReconcile = "boot-reconcile"
	// ByRecovery 常规路·本副本/无主会话的事件静默收敛。
	ByRecovery = "recovery"
	// ByDeadOwner 常规路·他副本、无心跳行回落事件静默的收敛。
	ByDeadOwner = "recovery:dead-owner"
	// ByDeadLease 常规路·他副本心跳租约过期的立即回收。
	ByDeadLease = "recovery:dead-lease"
)

// Session 在途会话的判定所需最小面（宿主 running 行的投影；领域字段不进本包）。
type Session struct {
	// ID 会话标识（收敛与事件的主键）。
	ID string
	// CaseID 案例标识（事件载荷透传；宿主无此概念可置空）。
	CaseID string
	// Owner 归属副本标识；空 = 无主（无主行归本副本判定面）。
	Owner string
	// CreatedAt 创建时刻：无事件行（首个事件落库前即中断）时的锚点。
	CreatedAt time.Time
}

// Cause 一次收敛的结构化归因（机制事实；**报告文案由宿主生成**——领域文案
// 不进内核，宿主拿 By/Reason 拼自己的恢复说明报告）。
type Cause struct {
	// By 归因标识（ByXxx 常量；与宿主 turn 括号的 closed_by 同值）。
	By string
	// Reason 机制事实描述（判定路 + 阈值 + 观测时刻），由本包生成。
	Reason string
}

// Store 存储接缝（宿主实现；形态与 taskboard/eventbus 的接缝同风格：
// 判定留包内、状态归存储、包零 DB 依赖）。
//
// 契约五条：
//   - **ListRunning 是扫描面提示，不是判定权威**：owner=="" 列出全部 running
//     会话（常规路要判他副本，必须看全量）；owner==instanceID 可收窄到该实例
//     名下（启动路的优化面），但**不得漏 Owner 为空的行**（无主行归任何实例
//     的判定面）。判定权恒在内核——按 Session.Owner 复核，收窄必须是超集；
//   - **LastActivity 是只读事实**：返回最后事件时刻，零值 = 无事件行（内核
//     回落 CreatedAt 锚定）。返回 error = 该会话**不可判**，内核跳过不收
//     （读不到事实就不许收敛，宁可留给下一轮/另一条路）；
//   - **ListBeats 读失败由内核降级**：error ⇒ 全体走事件静默判定（恢复不因
//     观测面抖动停摆），错误随扫描返回值带出留痕；
//   - **ConvergeCAS 是一次原子收敛**：条件迁移（from→to）+ 恢复说明报告 +
//     排队输入结算 + 悬挂环节收口——宿主侧等价于「条件 UPDATE + 终态报告 +
//     结算」同事务完成（bianque FinalizeSession + Settle + FailSteps 的宿主
//     等价物）。返回 (false, nil) = 他方已处理（条件迁移零行），内核不再发
//     任何收尾动作；返回 error = 内核带出留痕；
//   - **CloseBracket 关闭该会话仍开放的 turn 括号**，以 closedBy 归因留痕。
//     宿主的典型实现即落 turn_ended 事件（载荷带 closed_by）——「有 start
//     无 end」的开放括号是中断轮的可检测孤儿态，关不掉它审计就读不出轮边界。
//     本包不重复发 turn_ended（那会与宿主实现双写）。返回 error = 括号没关成，
//     内核**并入聚合错误带出留痕**（issue #184：签名吞错误连「失败了」都
//     表达不了，宿主拿到错误才能补偿修复）；session_finished 照发——终态
//     事实已由 ConvergeCAS 成立，事件驱动收尾的下游不因审计面故障停摆。
type Store interface {
	// ListRunning 列出 running 态会话（owner 语义见上）。
	ListRunning(ctx context.Context, owner string) ([]Session, error)
	// LastActivity 最后事件时刻（零值 = 无事件行）。
	LastActivity(ctx context.Context, sessionID string) (time.Time, error)
	// ListBeats 副本心跳表（instanceID → 最后打点时刻）。
	ListBeats(ctx context.Context) (map[string]time.Time, error)
	// ConvergeCAS 条件收敛（from→to，原子带报告与结算；false = 他方已处理）。
	ConvergeCAS(ctx context.Context, sessionID, from, to string, cause Cause) (bool, error)
	// CloseBracket 关闭开放的 turn 括号（closedBy = Cause.By）。
	// 返回 error = 关闭失败（内核并入聚合错误带出，不吞）。
	CloseBracket(ctx context.Context, sessionID, closedBy string) error
}

// Pub 收尾事件发布接缝（宿主的落库+广播同一路；与 watchdog.Pub 同形）。
//
// 本包经它发 session_finished（turn_ended 归 CloseBracket，见 Store 注释）。
// 事件词表用 eventbus 的既有常量，不另立事件模型。
type Pub func(sessionID, caseID string, stepID *uint64, typ, level string, payload map[string]any)

// Config 装配配置（构造时定值；阈值 0=取包内缺省，与宿主 conf 旋钮同语义）。
type Config struct {
	// InstanceID 本副本标识（必填）：owner 判定的「本副本」零点。
	InstanceID string
	// Boot 进程启动锚点（RecoverBootOrphans 的判据零点；构造时定格）。
	// 零值 = 没有启动事实 ⇒ 启动路整体不跑（防零值锚点误收全量）。
	Boot time.Time
	// Stall 事件静默阈值（0=DefaultStall；<=0 一律取缺省——静默路不可关，
	// 关掉它等于没有任何死亡证据）。仅作用于 owner=本副本/空的会话。
	Stall time.Duration
	// DeadOwner 他副本无心跳行时的回落静默阈值（0=DefaultDeadOwner）。
	DeadOwner time.Duration
	// LeaseTTL 心跳租约缺省 TTL（0=DefaultLeaseTTL；**负=关闭心跳判定**，
	// 不读 ListBeats、全体回落事件静默）。
	LeaseTTL time.Duration
	// BootGrace 启动路额外宽限（>0=更保守，last < boot-grace 才收；
	// 0=无额外宽限；**负=整体关闭启动路**，回落纯静默路径）。
	BootGrace time.Duration
}

// Reconciler 三路扫描与收敛编排（零值不可用，用 New / NewWithClock 构造）。
//
// 并发安全面：自身无可变状态（Config 构造时定值），多次 Recover* 可并发调——
// 收敛的互斥由宿主的 ConvergeCAS 条件迁移保证（本包只判结果不加锁）。
type Reconciler struct {
	store Store
	pub   Pub
	cfg   Config

	// 阈值归一在构造期冻结（Config 本就按值传入，宿主 conf 变更走重建）。
	stall     time.Duration
	deadOwner time.Duration
	lease     time.Duration // <0 = 心跳判定关闭

	now func() time.Time // 时钟注入（租约/静默判定的确定性测试面）
}

// New 构造（时钟取 time.Now）。
func New(store Store, pub Pub, cfg Config) *Reconciler {
	return NewWithClock(store, pub, cfg, time.Now)
}

// NewWithClock 构造并**注入时钟**（ticket.NewWithClock 同款）：租约过期、
// 静默超阈、启动锚点三个判定全依赖 now，读真实时间的过期用例恒不触发——
// 判据会退化成一组空断言（全绿但什么都没验）。
//
// store / pub / InstanceID 任一缺失即 panic：装配期 fail fast，不留到运行时
// （没有存储的对账无从判定，没有 InstanceID 会把他副本名下判成反面）。
func NewWithClock(store Store, pub Pub, cfg Config, now func() time.Time) *Reconciler {
	if store == nil {
		panic("reconcile: 需要 Store（判定与收敛的事实全在存储接缝之后）")
	}
	if pub == nil {
		panic("reconcile: 需要 Pub（收敛必须发 session_finished——不发等于静默收敛）")
	}
	if cfg.InstanceID == "" {
		panic("reconcile: 需要 Config.InstanceID（空标识会把「本副本」判定面退化成只有无主行）")
	}
	if now == nil {
		now = time.Now
	}
	r := &Reconciler{store: store, pub: pub, cfg: cfg, now: now}
	// 阈值归一（与宿主 conf 旋钮同语义：0=缺省、LeaseTTL 负=关闭）。
	r.stall = cfg.Stall
	if r.stall <= 0 {
		r.stall = DefaultStall
	}
	r.deadOwner = cfg.DeadOwner
	if r.deadOwner <= 0 {
		r.deadOwner = DefaultDeadOwner
	}
	r.lease = cfg.LeaseTTL
	if r.lease == 0 {
		r.lease = DefaultLeaseTTL
	}
	return r
}

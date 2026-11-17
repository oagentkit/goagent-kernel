// store.go —— 控制面状态的**存储中立契约**（issue #189）。
//
// # 它补的是哪一段
//
// Plane 此前只有 MemPlane 一份进程内实现：两个副本各持一份状态，A 的介入
// （强释 / 裁决 / 终止）在 B 的视图里不可见，B 的旧视图还能通过 CAS——
// 「人工介入」在多副本下退化成「只在一个进程里算数」。
//
// Store 把 Plane 需要的状态拆成三类记录（任务 / 运行 / 效果键）与两个
// 原语（一致快照读、带版本锚的原子提交），落库仍归宿主：表结构、事务
// 边界、编码形态都是宿主资产。内核只给契约与判据（StorePlane 是唯一
// 一份判定逻辑；MemStore 是进程内参考实现）。
//
// # 三条硬契约
//
//   - **快照一致**：SnapshotTask / SnapshotRun 返回的任务集合与运行状态
//     来自同一瞬间（不是边改边读的流）；TaskView 的 run 级不变量判据
//     依赖这一点。
//   - **提交原子且带版本锚**：Commit* 的 expect 必须等于对应快照的
//     Version；不一致 ⇒ ErrStoreConflict 且**什么都不写**（调用方重读重判）。
//     版本由存储推进：要么全局修订号、要么记录行版本，两种实现都满足；
//     契约只要求「快照之后目标被改过 ⇒ 提交必失败」。
//   - **留痕与状态同事务**：Commit* 同时追加一条 AuditEntry 并写状态；
//     留痕失败 ⇒ 状态一字不动（ErrNoAuditor / 留痕后端错误原样传出）。
//     这是 issue #180「一次失败的介入不得改变可观测状态」在存储契约上的
//     落点——没有「先改后补痕迹」的窗口。
//
// # 为什么版本锚不取「值相等」
//
// 值相等断言在 ABA 场景（改走又改回）会误判「没动过」；版本随每次成功
// 提交推进，锚的是**变更次数**而不是变更后的内容——与 coord.Backend 的
// Version 断言、commitgate「凭令牌认占用」同一条纪律。
package controlplane

import (
	"context"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// Version 控制面记录的版本锚：每次成功提交推进（不复用、不回退）。
//
// 实现可以按记录维护，也可以维护一个全局修订号——只要满足
// 「快照之后目标被改过 ⇒ 版本不匹配」。参考实现用全局修订号（简单且
// 正确：多副本下同一时刻只有一个提交能落，假冲突由 StorePlane 重试消化）。
type Version uint64

// TaskSnapshot 任务快照（TaskView 的存储输入）。
type TaskSnapshot struct {
	// Version 快照版本（CommitTask 的 expect）。
	Version Version
	// RunID 任务所属运行。
	RunID RunID
	// Task 目标任务当前状态。
	Task taskboard.Task
	// RunTasks 所属 run 的全部任务（按任务 id 排序）。
	//
	// 不变量是 run 级判据（依赖与环要看全集），TaskView 只投影到目标任务；
	// 只取一个任务会让「依赖未完成却被认领」这类问题在视图里隐身。
	RunTasks []taskboard.Task
}

// RunSnapshot 运行快照（RunView 的存储输入）。
type RunSnapshot struct {
	// Version 快照版本（CommitRun 的 expect）。
	Version Version
	// RunID 运行标识。
	RunID RunID
	// Status 运行状态。
	Status string
	// Gen 运行代次。
	Gen uint64
	// Tasks 该 run 的全部任务（按任务 id 排序）。
	Tasks []taskboard.Task
}

// RunState 运行记录的变更形态（CommitRun 的写入面）。
type RunState struct {
	// Status 新状态。
	Status string
	// Gen 新代次。
	Gen uint64
	// TaskIDs 运行的任务集合（nil = 保持原集合不变）。
	TaskIDs []taskboard.TaskID
}

// EffectState 效果键的裁决账。
type EffectState struct {
	// Uncertain 结果是否仍未知（true = 尚未裁决）。
	Uncertain bool
	// Unsafe 动作是否声明为不可重试（未生效裁决不自动开门）。
	Unsafe bool
	// Resolved 已裁决的结论（Uncertain=false 时有效）。
	Resolved Verdict
}

// EffectSnapshot 效果键快照（Adjudicate 的存储输入）。
type EffectSnapshot struct {
	// Version 快照版本（CommitEffect 的 expect）。
	Version Version
	// Key 效果幂等键。
	Key string
	// Effect 当前裁决账。
	Effect EffectState
}

// Store 控制面状态的存储中立契约。
//
// 实现方必须保证（三条硬契约见文件头）：
//
//   - 三个 Snapshot* 返回**一致快照**；目标不存在 ⇒ ErrNotFound；
//   - 三个 Commit* 在 expect 与当前版本不符时返回 ErrStoreConflict 且
//     不写任何状态（**不**追加留痕——失败的 CAS 不是一次介入）；
//   - Commit* 把留痕与状态写在**同一次原子操作**里：留痕面缺位 ⇒
//     ErrNoAuditor；留痕写入失败 ⇒ 状态不变、错误原样传出；
//   - 并发安全：多个 StorePlane 实例共享同一 Store 时，介入必须按版本
//     锚串行化（后到者取新快照重判）。
//
// 参考实现 MemStore 见 memstore.go；跨进程实现把三个 Commit 落到数据库
// 事务（状态行 + 审计行同一事务）或 etcd/Redis 的条件事务上即可。
//
// # 接缝契约（issue #370 形态 B）
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：宿主实现内不得回查本包（回查即重入内核锁路径 ⇒ 死锁）。
// 内核侧兑现：StorePlane 在**不持内核锁**的位置调用本接口六个方法。
// no-blocking：宿主实现内不得无界等待（不得等锁 / 等其它回调）。
// 内核侧兑现：调用点不持内核锁，阻塞只拖慢当前介入；乐观并发路径带重试
// 循环，阻塞会拖长那一次介入。
// panic-isolated：**宿主侧兑现**——内核不隔离本接口的 panic（StorePlane
// 无 recover），宿主实现须自行保证不 panic 或自行 recover 后返回 error。
type Store interface {
	// SnapshotTask 读任务及其所属 run 的全部任务（一致快照）。
	SnapshotTask(ctx context.Context, id taskboard.TaskID) (TaskSnapshot, error)
	// SnapshotRun 读运行及其全部任务（一致快照）。
	SnapshotRun(ctx context.Context, id RunID) (RunSnapshot, error)
	// SnapshotEffect 读效果键裁决账。
	SnapshotEffect(ctx context.Context, key string) (EffectSnapshot, error)

	// CommitTask 原子提交任务状态 + 留痕（expect 见类型级契约）。
	CommitTask(ctx context.Context, expect Version, next taskboard.Task, entry AuditEntry) error
	// CommitRun 原子提交运行状态/代次 + 留痕。
	CommitRun(ctx context.Context, expect Version, id RunID, next RunState, entry AuditEntry) error
	// CommitEffect 原子提交裁决账 + 留痕。
	CommitEffect(ctx context.Context, expect Version, key string, next EffectState, entry AuditEntry) error
}

// 控制面存储的装配/提交错误哨兵。
var (
	// ErrNoStore 未装配 Store 就要构造 / 使用持久化 Plane。
	// 修声明：装配 Store；不得静默退化成进程内状态（多副本下两份真相）。
	ErrNoStore = errors.New("controlplane: 状态存储未装配")
	// ErrStoreConflict 版本锚不匹配：快照之后目标被另一个副本改过。
	// **重试**：重读快照、重判后再提交；这不是「介入失败」，而是一次
	// 乐观并发冲突（StorePlane 内部已自动重试，穷尽后才会把它交给调用方）。
	ErrStoreConflict = errors.New("controlplane: 状态版本冲突（快照已过期）")
)

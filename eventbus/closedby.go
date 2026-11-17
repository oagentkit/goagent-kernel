// closedby.go —— 轮括号闭合归因（closed_by）词表的**单一来源**（issue #391）。
//
// 生产方 reconcile（四路收敛归因）与消费方 watchdog（缺省词表成员校验）同仓
// 互不 import。两包各维护一份词表时实测漂移：reconcile 发
// recovery:dead-owner / recovery:dead-lease，watchdog 的缺省表只认
// loop / recovery / boot-reconcile / panic ⇒ 有归因的正常收敛被报成
// 「无归因」（turn_end_unattributed 假红）。
//
// 词表收在本包——reconcile 与 watchdog 都已依赖 eventbus，引用同一组常量即
// 可消除两份实现；一致性由两包的测试断言集合恒等（watchdog/turn_test.go 的
// TestDefaultClosedByAlignsWithReconcile、reconcile 侧的同名断言）。
package eventbus

// 闭合归因值（EventTurnEnded 载荷 closed_by）。宿主可经 watchdog.Config.ClosedBy
// 注册扩充；内核只判成员，不判归因语义（那归宿主恢复路径）。
const (
	// ClosedByLoop 收尾漏斗正常出口（宿主编排循环自己闭合）。
	ClosedByLoop = "loop"
	// ClosedByRecovery 恢复循环归因（中断会话的统一收敛体）。
	ClosedByRecovery = "recovery"
	// ClosedByBootReconcile 启动对账归因（发版重启后的孤儿回收）。
	ClosedByBootReconcile = "boot-reconcile"
	// ClosedByPanic 编排 panic 归因（recoverPanic 就地收口）。
	ClosedByPanic = "panic"
	// ClosedByDeadOwner 他副本无心跳行、回落事件静默的收敛归因（reconcile 常规路）。
	ClosedByDeadOwner = "recovery:dead-owner"
	// ClosedByDeadLease 他副本心跳租约过期的立即回收归因（reconcile 常规路）。
	ClosedByDeadLease = "recovery:dead-lease"
)

// ClosedByDefaults 返回内核缺省闭合归因词表。
//
// 缺省值 = 所有宿主共有的收尾路径 + 内核 reconcile 的四路归因；宿主注册
// （watchdog.Config.ClosedBy）在此表基础上**扩充**，只增不减——缺一项就会把
// 合法归因判成红。
//
// 返回副本：调用方可以安全 append，不会改到本函数的词表。
func ClosedByDefaults() []string {
	return []string{
		ClosedByLoop,
		ClosedByRecovery,
		ClosedByBootReconcile,
		ClosedByPanic,
		ClosedByDeadOwner,
		ClosedByDeadLease,
	}
}

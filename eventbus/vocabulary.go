// vocabulary.go —— 跨包共用的词表单源（issue #391 / #392）。
//
// # 为什么下沉到本包
//
// 两组词表曾经各有两份实现：
//
//   - **closed_by 闭合归因**：reconcile 生产（boot-reconcile / recovery /
//     recovery:dead-owner / recovery:dead-lease），watchdog 消费（判归因是否
//     在册）。两份各写各的 ⇒ 内核自己漏写两个值 ⇒ 有归因的正常收敛路径被判成
//     「无归因」（假红，issue #391）。
//   - **配对缺口 reason**：trajectory 的 Pairing 词表（投影面）与 watchdog 的
//     告警 reason 词表（监视面）是同一事实的两份实现，其中
//     declared_not_executed 与 missing_result 还是同一事实的**两个 token**
//     （issue #392）。
//
// 本包是三者都已依赖的底座（reconcile / watchdog / trajectory 都依赖
// eventbus），故词表下沉到这儿无环。**下沉的是词表，不是判定**：判定仍在
// 各自的包里（watchdog 只判「有没有（在册的）归因」，不判归因语义）。
package eventbus

// ── closed_by 闭合归因词表（issue #391）─────────────────────────────────
//
// 闭合归因回答「这次轮次是谁、按哪条路收掉的」。生产方是 reconcile 的三路
// 收敛与宿主自己的收尾路径；消费方是 watchdog（只判词表成员，不判语义）。
//
// 词表单源化：reconcile 的 ByXxx 与 watchdog 的 ClosedByXxx 都引用本组常量。
// 两包各写一份 ⇒ 内核自己漏写就变成假红——实测 recovery:dead-owner 与
// recovery:dead-lease（他副本死主回收，都是**有归因的正常收敛路径**）被
// watchdog 判成「无归因」。
const (
	// ClosedByLoop 收尾漏斗正常出口（宿主编排循环自己闭合）。
	ClosedByLoop = "loop"
	// ClosedByRecovery 常规恢复路归因（中断会话的事件静默收敛）。
	ClosedByRecovery = "recovery"
	// ClosedByBootReconcile 启动对账归因（发版重启后的孤儿回收）。
	ClosedByBootReconcile = "boot-reconcile"
	// ClosedByPanic 编排 panic 归因（就地收口）。
	ClosedByPanic = "panic"
	// ClosedByDeadOwner 他副本无心跳行、回落事件静默的死主回收归因。
	ClosedByDeadOwner = "recovery:dead-owner"
	// ClosedByDeadLease 他副本心跳租约过期的死主回收归因。
	ClosedByDeadLease = "recovery:dead-lease"
)

// AllClosedBy 返回闭合归因词表的全部取值（**新副本**：调用方改不动本包的事实）。
//
// 词表加值时，生产方（reconcile）与消费方（watchdog 的缺省判定面）都从这儿
// 取——两边各自维护清单就是 issue #391 的成因。
func AllClosedBy() []string {
	return []string{
		ClosedByLoop,
		ClosedByRecovery,
		ClosedByBootReconcile,
		ClosedByPanic,
		ClosedByDeadOwner,
		ClosedByDeadLease,
	}
}

// ── 配对缺口 reason 词表（issue #392）───────────────────────────────────
//
// 「工具调用配对缺口」是同一事实：投影面（trajectory 的 Pairing）与监视面
// （watchdog 的告警 reason）此前各写一份。三个 token 逐字同名，第四个
// （declared_not_executed vs missing_result）是同一事实的两个 token——
// 宿主按投影面的词表筛监视面的告警，会漏掉 declared_not_executed 这一类。
//
// 处置：**规范 token 归本包**（三者都已依赖它，无环），trajectory 的 Pairing
// 常量引用本包、watchdog 的 reason 常量引用 trajectory；别名
// （declared_not_executed）在 PairingReasonAliases 里**注册**，不是各写各的。
const (
	// PairingMissingResult 声明了调用、始终没等到结果（规范 token）。
	PairingMissingResult = "missing_result"
	// PairingUnpairedResult 结果没有对应的调用声明（规范 token）。
	PairingUnpairedResult = "unpaired_result"
	// PairingDuplicateResult 同一 call_id 收到第二个及以后结果（规范 token）。
	PairingDuplicateResult = "duplicate_result"
	// PairingDuplicateCall 同一 call_id 收到第二个调用声明（规范 token）。
	PairingDuplicateCall = "duplicate_call"
	// PairingReasonDeclaredNotExecuted 监视面既有的别名 token（同一事实：
	// 声明了、始终没等到结果）。它不是独立字面量——注册在 PairingReasonAliases。
	PairingReasonDeclaredNotExecuted = "declared_not_executed"
)

// PairingReasonAliases 注册别名表：别名 token → 规范 token。
//
// 「同一事实两个 token」不能靠两处各写各的收敛——那只是把两份实现换了个位置。
// 别名在此**注册**：宿主按规范 token 聚合时用它归一（把 declared_not_executed
// 归到 missing_result），两侧的词表差异从此是登记事实而不是暗改。
//
// **只读**：本表是词表事实，调用方不得改写（改动请改本包常量）。
var PairingReasonAliases = map[string]string{
	PairingReasonDeclaredNotExecuted: PairingMissingResult,
}

// AllPairingGaps 返回配对缺口的规范 token 全集（**新副本**）。
//
// 缺口四类（missing_result / duplicate_result / unpaired_result /
// duplicate_call）是**缺口**的封闭词表，与「行状态」（paired / no_call_id）
// 不是一回事。消费面（watchdog 的 reason 词表）按这份清单对齐：投影面加了
// 第五类缺口而消费面没跟，比对用例当场红（issue #392）。
func AllPairingGaps() []string {
	return []string{
		PairingMissingResult,
		PairingDuplicateResult,
		PairingUnpairedResult,
		PairingDuplicateCall,
	}
}

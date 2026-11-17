// pairing.go —— 工具调用配对的**唯一**键构造函数（issue #336）与配对缺口
// reason 的注册别名表（issue #392）。
//
// 背景：trajectory（投影流水）与 watchdog（不变量监视）都消费同一份事件流。
// 两处各写一份配对键（一侧 `session|step|call_id`、另一侧 `call_id`）时，
// 同一条 tool_result 在一侧配得上、在另一侧配不上——同一事实两个相反结论，
// 而两边都不会报错。配对键是事件载荷契约的一部分，故构造函数收在 eventbus
// 这个词表包里；门禁（architecture-policy.yaml 的
// tool-pairing-key-single-source）禁止其他包直接索引 `Payload["call_id"]`。
package eventbus

// ToolCallID 取工具事件的配对键（载荷键 call_id）。
//
// 空串 = 调用方未提供标识：这是「无 id 路径」，不是「结果找不到对应调用」
// ——两件事的处置不同（见 trajectory 的 PairingNoCallID）。
//
// 配对身份**不含 StepID**：工具调用与结果可以在不同环节/轮次到达，
// step 是归属信息而不是配对键。两包必须用本函数取键，不得各自构造。
func ToolCallID(ev Event) string {
	if ev.Payload == nil {
		return ""
	}
	callID, _ := ev.Payload["call_id"].(string)
	return callID
}

// PairingReasonDeclaredNotExecuted 监视器侧「声明了、始终没等到结果」的
// 历史 token（issue #392）。
//
// 它与 trajectory.PairingMissingResult 是**同一事实**：watchdog 的
// EventToolPairingLeak 与轨迹投影消费同一份事件流，两个 token 会让按条件
// 聚合的看板把同一条件拆成两个桶。保留线路值以兼容既有告警消费者，故登记为
// PairingMissingResult 的**别名**而不是独立状态——见 CanonicalPairingReason。
const PairingReasonDeclaredNotExecuted = "declared_not_executed"

// pairingReasonAliases 配对缺口 reason 的注册别名表：监视器侧 token → 规范 token。
//
// 本包不 import trajectory（词表包不反向依赖投影包），规范值以字面量登记；
// 与 trajectory.PairingMissingResult 的一致性由 watchdog 的跨包用例钉住
// （TestPairingReasonsAlignWithTrajectory）。表未导出：词表是只读契约，
// 不交出可改的 map。
var pairingReasonAliases = map[string]string{
	PairingReasonDeclaredNotExecuted: "missing_result",
}

// CanonicalPairingReason 把监视器侧的配对缺口 reason 归一为 trajectory.Pairing
// 的规范 token；未登记的值原样返回（规范值即自身）。
//
// 用途：把 watchdog 告警与 eval/trajectory 的配对判定按同一词表聚合。
func CanonicalPairingReason(reason string) string {
	if canonical, ok := pairingReasonAliases[reason]; ok {
		return canonical
	}
	return reason
}

// IsPairingReasonAlias 报告 reason 是否为注册别名（词表审计用：别名必须指向
// 另一个规范 token，不能自己就是规范值）。
func IsPairingReasonAlias(reason string) bool {
	_, ok := pairingReasonAliases[reason]
	return ok
}

// pairing.go —— 工具调用配对的**唯一**键构造函数（issue #336）。
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

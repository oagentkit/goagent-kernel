// rowhelpers.go —— 行投影的纯数据助手（轮次归属 / 默认摘要 / 载荷取值 / 环节号）。
//
// 都是无副作用的取值面：不调宿主回调、不读时钟——与 trajectory.go 的
// 「时长单源」纪律同一方向（数据是数据，视图是宿主的事）。

package trajectory

import (
	"github.com/oagentkit/goagent-kernel/eventbus"
)

// currentTurn 当前轮次序号（尚未开轮时为 0）。
func (p *Projector) currentTurn() int {
	if p.turn < 0 {
		return 0
	}
	return p.rows[p.turn].Turn
}

// defaultSummary 默认行摘要：通用标签，不含任何领域词。
func defaultSummary(r Row) string {
	switch r.Kind {
	case KindTurn, KindStep:
		return r.ID
	case KindToolCall, KindToolResult:
		if r.Tool == "" {
			return string(r.Kind)
		}
		return string(r.Kind) + ":" + r.Tool
	case KindSideEffect:
		if r.SideEffectKey == "" {
			return string(r.Kind)
		}
		return string(r.Kind) + ":" + r.SideEffectKey
	case KindCheckpoint:
		return r.ID
	default:
		return string(r.Kind)
	}
}

// payloadString 取事件载荷里的字符串键（缺失或类型不符时为空串）。
func payloadString(ev eventbus.Event, key string) string {
	if ev.Payload == nil {
		return ""
	}
	s, _ := ev.Payload[key].(string)
	return s
}

// stepIDOf 取事件的环节号。
func stepIDOf(ev eventbus.Event) (uint64, bool) {
	if ev.StepID == nil {
		return 0, false
	}
	return *ev.StepID, true
}

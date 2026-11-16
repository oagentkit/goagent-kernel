// payload.go —— 事件载荷取值助手（轨迹投影层读载荷的唯一入口）。
//
// 载荷是 map[string]any，经 JSON 往返后整数是 float64——「只认 int64」的
// 取值面会在宿主的发布路径上整片失明。三个助手把「缺失 / 类型不符」的
// 归一口诀收在一处：字符串空串、整数 0、布尔 false（布尔读成 false 是
// 保守方向，但调用方仍应发规范的 bool）。
//
// 数值面**不猜**（issue #305）：负值 / 非整数（3.7）/ NaN / ±Inf / 超出
// uint64 范围一律按「未设置」处理（行字段零值、属性面不写），并进
// PayloadErrors——「没给」与「给了个不可用的值」因此可区分。旧实现把
// 1e30 直接 uint64(v) 成 MaxUint64（导出时溢出成 -1）、把 3.7 静默截成 3，
// 都是猜出一个看起来合法的值。
package trajectory

import (
	"math"
	"slices"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// PayloadError 载荷数值字段的显式拒绝记录（issue #305）。
//
// 缺失**不算**错误——缺失是「未设置」，行字段为零值、属性面不写。只有
// 「给了值但无法按无符号整数解析」才记。于是宿主能区分两件事：
// 键没给（正常未设置）与键给了病态值（发布侧需要修）。
type PayloadError struct {
	// Key 载荷键（如 fence / seq）。
	Key string
	// Reason 拒绝原因（负值 / 非整数 / NaN / +Inf / -Inf / 超出 uint64 范围）。
	Reason string
}

// PayloadErrors 载荷数值字段的显式拒绝记录（累计，按发生顺序）。
//
// 与 Issues 分开：Issues 是配对缺口；这里是「载荷里给了值但本包拒绝猜」。
func (p *Projector) PayloadErrors() []PayloadError {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.payloadErrors)
}

// payloadUint 取事件载荷里的无符号整数键（缺失 / 类型不符 / 病态值时为 0）。
//
// 病态值（负值 / 非整数 / NaN / ±Inf / 超出 uint64）在调用方已持锁的投影
// 路径上执行，故登记错误面无需再加锁（issue #305）。
func payloadUint(p *Projector, ev eventbus.Event, key string) uint64 {
	v, reason := uintFromPayload(ev.Payload, key)
	if reason != "" {
		p.payloadErrors = append(p.payloadErrors, PayloadError{Key: key, Reason: reason})
	}
	return v
}

// uintFromPayload 严格解析无符号整数键：ok 用空 reason 表达。
//
// float64 的合法域是「非负、整值、有限、< 2^64」；其余一律拒绝，不截断、
// 不饱和。float64 在 2^53 以上会丢低位——那是 JSON 往返本身的限制，行字段
// 仍如实承载解析结果，宿主若需要 >2^53 的精确值应改发字符串（本包不猜）。
func uintFromPayload(payload map[string]any, key string) (uint64, string) {
	if payload == nil {
		return 0, ""
	}
	raw, ok := payload[key]
	if !ok || raw == nil {
		return 0, ""
	}
	switch v := raw.(type) {
	case uint64:
		return v, ""
	case int64:
		if v < 0 {
			return 0, "负值"
		}
		return uint64(v), ""
	case int:
		if v < 0 {
			return 0, "负值"
		}
		return uint64(v), ""
	case float64:
		switch {
		case math.IsNaN(v):
			return 0, "NaN"
		case math.IsInf(v, 1):
			return 0, "+Inf"
		case math.IsInf(v, -1):
			return 0, "-Inf"
		case v < 0:
			return 0, "负值"
		case v != math.Trunc(v):
			return 0, "非整数"
		case v >= math.Exp2(64):
			return 0, "超出 uint64 范围"
		}
		return uint64(v), ""
	default:
		// 类型不符沿用既有口径：按「未设置」处理（契约里写明的归一口诀）。
		return 0, ""
	}
}

// payloadBool 取事件载荷里的布尔键（缺失或类型不符时为 false——
// 「裁决说已生效」这类布尔读成 false 是保守方向，但调用方仍应发规范的 bool）。
func payloadBool(ev eventbus.Event, key string) bool {
	if ev.Payload == nil {
		return false
	}
	b, _ := ev.Payload[key].(bool)
	return b
}

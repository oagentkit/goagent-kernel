// payload.go —— 事件载荷取值助手（轨迹投影层读载荷的唯一入口）。
//
// 载荷是 map[string]any，经 JSON 往返后整数是 float64——「只认 int64」的
// 取值面会在宿主的发布路径上整片失明。三个助手把「缺失 / 类型不符」的
// 归一口诀收在一处：字符串空串、整数 0、布尔 false（布尔读成 false 是
// 保守方向，但调用方仍应发规范的 bool）。
//
// 整数取值的**范围纪律**（issue #305）：范围内的数逐字取用（小数按声明口径
// 向零取整）；超范围（负数 / NaN / ±Inf / ≥2^64）投影器**不猜**——不产出
// MaxUint64 这种看起来合法的值，而是按未设置处理，越界本身以独立标记显形，
// 与「载荷没带这个键」区分得开。
package trajectory

import (
	"math"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// payloadUintState payloadUint 对一个键的取值结论。
type payloadUintState uint8

const (
	// payloadAbsent 键缺失、类型不符（含 nil）——「没带这个值」。
	payloadAbsent payloadUintState = iota
	// payloadOK 载荷带了一个 uint64 可表示的值（小数按声明口径向零取整）。
	payloadOK
	// payloadOutOfRange 载荷带了一个值、但它超出 uint64 可表示范围
	// （负数 / NaN / ±Inf / ≥2^64）——投影器不猜，与「未设置」区分得开。
	payloadOutOfRange
)

// payloadUint 取事件载荷里的无符号整数键。
//
// 载荷经 JSON 往返后整数是 float64（丢精度的是 >2^53 的值——序号与令牌
// 到不了那个量级；到得了的宿主应改发字符串载荷，本包不猜）。超范围的数
// **不做** uint64 转换：直接转换会让 1e30 落成 MaxUint64、NaN 落成 0，
// 都是看起来合法的错值——返回 payloadOutOfRange 让调用方按各自的行族显形
// （issue #305）。
func payloadUint(ev eventbus.Event, key string) (uint64, payloadUintState) {
	if ev.Payload == nil {
		return 0, payloadAbsent
	}
	v, present := ev.Payload[key]
	if !present {
		return 0, payloadAbsent
	}
	switch n := v.(type) {
	case uint64:
		return n, payloadOK
	case int64:
		if n < 0 {
			return 0, payloadOutOfRange
		}
		return uint64(n), payloadOK
	case int:
		if n < 0 {
			return 0, payloadOutOfRange
		}
		return uint64(n), payloadOK
	case float64:
		// float64(math.MaxUint64) 会向上取整到 2^64，故这个比较恰好拒绝
		// 「≥2^64」的全体（含 ±Inf）；NaN 与任何数比较都为假，单独判。
		if math.IsNaN(n) || n < 0 || n >= 18446744073709551616.0 {
			return 0, payloadOutOfRange
		}
		return uint64(n), payloadOK // 向零取整是声明口径（见文件头注）
	default:
		return 0, payloadAbsent
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

// payloadString 取事件载荷里的字符串键（缺失或类型不符时为空串）。
func payloadString(ev eventbus.Event, key string) string {
	if ev.Payload == nil {
		return ""
	}
	s, _ := ev.Payload[key].(string)
	return s
}

// stepIDOf 取事件的环节号（环节号 0 是合法值，故用独立标志位表达「有没有」）。
func stepIDOf(ev eventbus.Event) (uint64, bool) {
	if ev.StepID == nil {
		return 0, false
	}
	return *ev.StepID, true
}

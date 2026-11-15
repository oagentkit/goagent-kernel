// panic.go —— ptc 内的 panic 收编单源。
//
// oekit/pkg 自 v0.45.0 起把「panic 值 → error」收成唯一实现
// errors.Recovered（*PanicError：原始值 + 现场栈 + error 身份经 Unwrap
// 保留），org 内各仓按 wave 消费同一 API。本文件是 ptc 的消费点：两处
// recover（Run 的 host_panic 收口、binding 的调用留痕）都从这里取收编
// 结果，免得各写一份「%v 拼字符串」——那样 panic 值的类型与栈会随调用点
// 漂移（此前 CallLog 里 panic(42) 只剩 "42"，类型与栈全丢）。
package ptc

import (
	"github.com/oekit/pkg/errors"
)

// panicError 把 recover 值收编为上游 *errors.PanicError。
//
// r 非 nil 时 Recovered 必返回 *PanicError；ok=false 是**不可达兜底**——
// 在 recover 的处理路径上，宁可退化成「只带值、无栈」，也不能让一次断言
// 失败把「已收敛」变成「再 panic 一次」（那会穿透 Run 的返回承诺，正是
// 这条兜底要防的事）。
func panicError(r any) *errors.PanicError {
	if pe, ok := errors.Recovered(r).(*errors.PanicError); ok {
		return pe
	}
	return &errors.PanicError{Value: r}
}

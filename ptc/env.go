package ptc

import (
	"context"
	"slices"
)

// RunEnv 一次运行的 PTC 环境（批三百零五 ctx 缝）：runner 在装配完成点（全部工具追加
// 之后）把本环节模型可见的最终工具表快照进 ctx——「程序内可达=本环节模型可达」恒等式
// 由快照时点保证，不在组合根持全局表（per-run 表只活在当次 Run 里）。run_program 于
// 调用期取出构绑定；OnRun=ptc_run 明细事件回调（nil=不发）。
type RunEnv struct {
	Table []ToolTableEntry
	OnRun func(Result)
}

// ToolTableEntry 一个可绑定工具：Name=工具名（JS 侧 host.<净化名>），Run=调用
// （args=JSON 文本，回工具输出原文）。
type ToolTableEntry struct {
	Name string
	Run  func(ctx context.Context, args string) (string, error)
}

type envKey struct{}

// WithEnv 注入 PTC 环境（runner 装配点调用）。
//
// 注入侧快照（issue #389）：RunEnv 按值传，但 Table 是切片 ⇒ 结构体被拷贝、
// 底层数组仍与调用方共享。工具表是「本环节模型可达」的绑定面，注入方之后
// 复用/改写同一份表（拿配置模板再生成是常见写法）会让快照变成活句柄。
// 元素里的 func 值不可变，slices.Clone 足够。
func WithEnv(ctx context.Context, env RunEnv) context.Context {
	env.Table = slices.Clone(env.Table)
	return context.WithValue(ctx, envKey{}, env)
}

// EnvOf 取 PTC 环境（零值=未装配——run_program 如实回包不假跑）。
//
// 取出侧同样拷贝（issue #389）：读取方改写拿到的表不得写穿 ctx 里那份；
// 注入侧与取出侧必须成对，只修一侧另一个方向仍能改穿。
func EnvOf(ctx context.Context) RunEnv {
	env, _ := ctx.Value(envKey{}).(RunEnv)
	env.Table = slices.Clone(env.Table)
	return env
}

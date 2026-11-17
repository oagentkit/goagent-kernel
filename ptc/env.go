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
	// OnRun ptc_run 明细事件回调（nil=不发）。由宿主 runner 在装配点取出 env 后调用。
	//
	// 接缝契约（宿主注入的明细事件回调）：
	//
	// @seam: no-reentry no-blocking panic-isolated
	//
	// no-reentry：回调内不得回查本包（不得再取本包的 PTC 环境快照）。内核侧兑现
	// 「调用点不持锁」：**本包不调用它**（由宿主 runner 调用），且交付的是拷贝
	// （issue #389），内核不在任何锁内等它。
	// no-blocking：明细通知同步发生，须廉价返回，不得无界等待。
	// panic-isolated：**宿主侧兑现**——本包不调用它、没有隔离点：宿主 runner 须
	// 自行 recover（若交给 ptc.Run 收口，则由它收敛为 host_panic 结果）。
	OnRun func(Result)
}

// ToolTableEntry 一个可绑定工具：Name=工具名（JS 侧 host.<净化名>），Run=调用
// （args=JSON 文本，回工具输出原文）。
type ToolTableEntry struct {
	Name string
	// Run 工具实现（args=JSON 文本，回工具输出原文）。由宿主 runner 取出表后调用
	// （本包不调用它）。
	//
	// 接缝契约（宿主注入的工具实现）：
	//
	// @seam: no-reentry no-blocking panic-isolated
	//
	// no-reentry：实现内不得回查本包（不得再走判定环/预算闸）。内核侧兑现
	// 「调用点不持锁」：本包不调用它，交付的是拷贝（issue #389）。
	// no-blocking：同步调用，须自行有界；宿主若把它包成 ptc.Binding.Call，则由
	// 随调用带出的 ctx 断阻塞调用（见 Binding.Call 的契约）。
	// panic-isolated：**宿主侧兑现**——本包不调用它；宿主直接调用时须自行 recover，
	// 包成 ptc.Binding.Call 时则由 ptc.Run 收口收敛为 host_panic 结果。
	Run func(ctx context.Context, args string) (string, error)
}

type envKey struct{}

// WithEnv 注入 PTC 环境（runner 装配点调用）。
//
// # 为什么入参侧要拷一次工具表（issue #389）
//
// RunEnv 按值传，但 Table 是切片 ⇒ 结构体被拷贝、**底层数组共享**。而 ctx 是
// **跨环节通道**（注入方 ≠ 读取方），不是同一个调用栈内的临时对象：注入方
// 事后改写 env.Table[i].Name，读到的就是改后的表——
// 「程序内可达 = 本环节模型可达」这条恒等式不再成立，审计面也不上账。
//
// 拷贝用 slices.Clone（元素含 func 值：func 是不可变的共享引用，拷它安全）。
func WithEnv(ctx context.Context, env RunEnv) context.Context {
	env.Table = slices.Clone(env.Table)
	return context.WithValue(ctx, envKey{}, env)
}

// EnvOf 取 PTC 环境（零值=未装配——run_program 如实回包不假跑）。
//
// 出参侧也拷一次（issue #389）：否则读取方改写 got.Table[i].Name 后，下一次
// EnvOf 取到的也是改后的表——两个方向都改穿，「快照」名不副实。
//
// 两个方向必须**成对**：只拷一侧，另一侧照样改穿（注入方改穿 ⇒ 读取方看到
// 被改的表；读取方改穿 ⇒ 下一次读取看到被改的表）。
func EnvOf(ctx context.Context) RunEnv {
	env, _ := ctx.Value(envKey{}).(RunEnv)
	env.Table = slices.Clone(env.Table)
	return env
}

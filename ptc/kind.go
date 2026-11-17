package ptc

import (
	"github.com/oagentkit/guardx/toolfail"
)

// kindForErrorKind 把本地 ErrorKind 映射成 toolfail 的失败分类。
//
// # 为什么要有映射
//
// 跨进程传的失败分类必须是 **toolfail 的分类**（wire 的不变量 2：分类跨进程
// 同源）——父侧拿到后要据此决定「可重试 / 需人工 / 永久拒绝」。而本地的
// ErrorKind 是**诊断口径**（给模型看的形态名），两套词表不是一回事。
//
// # 幂等透传（issue #364）
//
// 宿主绑定抛出的规范失败（*toolfail.Failure）带的分类**本身就是处置口径**
// ——rejectionKindOf 探到的就是 f.Class（issue #58）。那类值再走一遍映射表
// 就成了「把处置口径折回处置口径」：9 个 toolfail 分类里 8 个落 default，
// 跨进程失败分类退化为恒定 unknown，可重试的 transient / rate_limited /
// self_reported 被降级成不可重试（编排层的退避重试不再触发），归因信息
// （permanent / authentication / not_found / invalid_argument 的处置文案）
// 全部丢失。
//
// 故判据是**先判是不是已是 toolfail 分类**：是则原样返回（幂等），不是才走
// 本地诊断词表 → toolfail 分类的映射。
//
// 本地词表那条路（syntax / deadline 等 → permanent / transient）保持不变，
// 映射表本体在 errorkind.go（与修复建议同一份口径，单一真相）。
func kindForErrorKind(kind string) string {
	// 已是 toolfail 分类 ⇒ 原样透传（幂等）。
	if isToolfailClass(kind) {
		return kind
	}
	// 本地诊断词表 → 处置分类（表在 errorkind.go）。
	if spec, ok := errorKindTable[kind]; ok {
		return string(spec.class)
	}
	// 不可分类 = 不可重试（保守侧）。
	return string(toolfail.ClassUnknown)
}

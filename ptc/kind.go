package ptc

import (
	"slices"

	"github.com/oagentkit/guardx/toolfail"
)

// ErrorKind 词表（本包产出的失败形态名，单一真相）。
//
// # 为什么要有导出的常量集（issue #365）
//
// 此前 ErrorKind 是散落在各文件的字符串字面量，外加一个 ErrorKindUnmanaged
// 常量；「分类」与「建议」两份手写 switch 各自维护 case 清单，覆盖面互不
// 相等（unmanaged 两份都落 default、binding_signature / permission_denied
// 只有分类没有建议、host_panic 只有建议没有分类）。
//
// 现在词表只有一处：常量集 + errorKindTable。新增一个取值必须同时登记
// 分类与建议（TestAllErrorKindsHaveClassAndSuggestions），且常量集与表
// 必须逐字一致（TestErrorKindConstantsMatchRegistry）——两份判据不可能
// 再静默漂移。
//
// # 与 toolfail 分类的关系
//
// ErrorKind 是**诊断口径**（给模型看的形态名），跨进程 wire 传的是 toolfail
// 的失败分类。两者不是一回事，但 toolfail 分类可以经 rejectionKindOf 原样
// 流进 Result.ErrorKind——kindForErrorKind 对这类值幂等透传（issue #364）。
const (
	// ErrorKindSyntax 程序语法错误（编译期就失败）。
	ErrorKindSyntax = "syntax"
	// ErrorKindBadResult 返回值不是 Promise 或不可 JSON 序列化。
	ErrorKindBadResult = "bad_result"
	// ErrorKindRuntime 程序运行时错误（宿主绑定抛错、JS 异常）。
	ErrorKindRuntime = "runtime"
	// ErrorKindPending await 永不落定（VM 无定时器）。
	ErrorKindPending = "pending"
	// ErrorKindBindingCollision 两个工具名净化成同一个 host 标识符（装配期缺陷）。
	ErrorKindBindingCollision = "binding_collision"
	// ErrorKindBindingSignature 绑定函数签名与程序调用形状不符。
	ErrorKindBindingSignature = "binding_signature"
	// ErrorKindDeadline 墙钟超时。
	ErrorKindDeadline = "deadline"
	// ErrorKindBudgetCalls 工具调用次数超限。
	ErrorKindBudgetCalls = "budget_calls"
	// ErrorKindBudgetBytes 返回字节数超限。
	ErrorKindBudgetBytes = "budget_bytes"
	// ErrorKindHostPanic 宿主绑定自己 panic（宿主侧缺陷）。
	ErrorKindHostPanic = "host_panic"
	// ErrorKindStageDenied 判定环判了「不许」（正常裁决）。
	ErrorKindStageDenied = "stage_denied"
	// ErrorKindStageFailed 判定环自己坏了（宿主门禁故障）。
	ErrorKindStageFailed = "stage_failed"
	// ErrorKindPermissionDenied 宿主工具/判定环报权限不足。
	ErrorKindPermissionDenied = "permission_denied"

	// ErrorKindUnmanaged 未受管执行被拒：装配了工具绑定，却没装判定环。
	//
	// 它与 stage_denied / stage_failed 分开不是分类洁癖：
	//   - stage_denied：判定环**判了**，结论是不许（正常裁决）；
	//   - stage_failed：判定环**自己坏了**（宿主门禁的故障）；
	//   - unmanaged：判定环**压根不在**，于是无人判过——这是「没接线」，不是「判了不许」。
	//
	// 三者的处置完全不同：前两者改程序或修门禁代码，第三者**必须先装上门禁**。
	// 合成一类的话，调用方会去改一段本来没问题的程序，而真正该做的是把门禁接上。
	ErrorKindUnmanaged = "unmanaged"
)

// errorKindInfo 单个 ErrorKind 的两个消费面：toolfail 失败分类 + 给模型的修复建议。
//
// 两个消费者共用同一张表（issue #365）：分类改了建议同表可见，新增取值漏登
// 任何一个面都会被用例拦住。
type errorKindInfo struct {
	// Class 跨进程 wire 的失败分类（toolfail 词表）。
	Class toolfail.Class
	// Suggestions 面向模型的修复建议（每条写「怎么办」，不是「发生了什么」）。
	Suggestions []string
}

// errorKindTable ErrorKind → （分类，建议）的单一真相。
//
// 映射按「处置含义」而非字面：
//
//	预算类（budget_calls / budget_bytes）→ 限流类（可重试但要退避）
//	确定性失败（syntax / binding_* / bad_result）→ 永久类（重试无意义）
//	超时与中断 → 瞬时类（值得重试）
//	判定环拒绝（stage_denied）→ 权限类（重试也不会满足，走审批或换工具）
//	判定环故障（stage_failed）→ unknown（宿主门禁坏了：程序改不了它，
//	  也不是「重试就会好」的瞬时故障——保守侧不可重试，issue #65）
//	未受管（unmanaged）→ 永久类（没接线：重试不会把门禁装上；与
//	  stage_failed 的 unknown 分开归因，issue #365）
//	宿主 panic（host_panic）→ 永久类（同一输入会再次 panic，改程序无用）
var errorKindTable = map[string]errorKindInfo{
	ErrorKindSyntax: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"检查括号、引号、模板字符串是否配对",
			"程序体是 async 函数体：顶层可直接用 await 与 return",
		},
	},
	ErrorKindBadResult: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"return 值必须是 JSON 可序列化的：不能返回函数或循环引用",
			"返回数组/对象字面量即可；字符串请确保是合法 JSON 文本",
		},
	},
	ErrorKindRuntime: {
		Class: toolfail.ClassTransient,
		Suggestions: []string{
			"宿主绑定抛出的错误会直接冒泡：确认调用参数形状与该工具的契约一致",
			"可以用 try/catch 包裹可疑调用，让程序带着已取得的部分结果继续",
		},
	},
	ErrorKindPending: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"await 永不落定：VM 无定时器，宿主绑定均为同步调用，程序内不存在会自己 resolve 的 Promise",
			"改造成对已 resolve 值的同步调用链",
		},
	},
	ErrorKindUnmanaged: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"这次执行被拒是因为宿主没有装配判定环，不是程序写错：改写法、换参数都不会通过",
			"请宿主装配 invoke.Stage 判定环（与普通工具调用同一套），或显式声明 AllowUnmanaged；程序本身无需改动",
		},
	},
	ErrorKindBindingCollision: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"这是装配期缺陷而非程序问题：两个工具名净化成同一个 host 标识符（如 get-logs 与 get_logs）",
			"请宿主重命名其中一个工具后重试，程序本身无需改动",
		},
	},
	ErrorKindBindingSignature: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"程序调用 host.<tool> 的参数个数/形状与绑定函数签名不符：按工具契约补齐必填参数、去掉多余参数",
			"可选参数可以省略，必填参数不能少；以宿主声明的工具契约为准",
		},
	},
	ErrorKindDeadline: {
		Class: toolfail.ClassTransient,
		Suggestions: []string{
			"墙钟超时：把长耗时操作拆成多次调用，或先取小结果再决定是否继续",
			"程序内不要 await 一个永不落定的 Promise（VM 无定时器）",
		},
	},
	ErrorKindBudgetCalls: {
		Class: toolfail.ClassRateLimited,
		Suggestions: []string{
			"工具调用次数超限：把多次小调用合并成一次带筛选条件的调用",
			"必要时改用结构化查询一次取回，而不是逐条遍历",
		},
	},
	ErrorKindBudgetBytes: {
		Class: toolfail.ClassRateLimited,
		Suggestions: []string{
			"返回字节超限：不要把工具原始输出原样 return，用 map/filter 聚合后只返回小结果",
			"原始数据在程序内处理，不要带过模型面",
		},
	},
	ErrorKindHostPanic: {
		Class: toolfail.ClassPermanent,
		Suggestions: []string{
			"这是宿主侧缺陷（绑定函数自己崩了），不是程序写错：改写法、换参数、try/catch 都无济于事",
			"把这个 panic 连同 tool 名与参数报给宿主维护者；排除前避开该工具",
		},
	},
	ErrorKindStageDenied: {
		Class: toolfail.ClassPermissionDenied,
		Suggestions: []string{
			"这次调用被宿主的门禁判定拒绝：不是参数形状错，也不是程序 bug，改写法重试不会通过",
			"改用被允许的工具或路径；确需该操作时先与宿主确认审批或白名单流程",
		},
	},
	ErrorKindStageFailed: {
		Class: toolfail.ClassUnknown,
		Suggestions: []string{
			"这次失败来自宿主门禁自身（判定环崩溃或调用没有可判定的对象），不是程序的问题：改程序不会有任何用",
			"同类调用请稍后重试；若持续失败，请宿主排查门禁代码与该工具的注册信息",
		},
	},
	ErrorKindPermissionDenied: {
		Class: toolfail.ClassPermissionDenied,
		Suggestions: []string{
			"宿主工具因权限不足拒绝执行：不是程序语法或参数形状错误，原样重试不会提升权限",
			"改用被允许的工具/路径；确需该操作时先走宿主的审批或授权流程",
		},
	},
}

// AllErrorKinds 返回本包产出的全部 ErrorKind（按字典序，返回副本）。
//
// 词表与 errorKindTable 同源：新增取值只改表，本函数自动包含；常量集与表的
// 一致性由用例钉住（issue #365）。
func AllErrorKinds() []string {
	out := make([]string, 0, len(errorKindTable))
	for kind := range errorKindTable {
		out = append(out, kind)
	}
	slices.Sort(out)
	return out
}

// kindForErrorKind 把本地 ErrorKind 映射成 toolfail 的失败分类。
//
// # 为什么要有映射
//
// 跨进程传的失败分类必须是 **toolfail 的分类**（wire 的不变量 2：分类跨进程
// 同源）——父侧拿到后要据此决定「可重试 / 需人工 / 永久拒绝」。而本地的
// ErrorKind 是**诊断口径**（给模型看的形态名），两套词表不是一回事。
//
// # 已是 toolfail 分类的值必须幂等透传（issue #364）
//
// rejectionKindOf 探测到的就是 toolfail 分类值（宿主绑定用 toolfail.New 表过
// 态）：它们经 Result.ErrorKind 流到这里时**已经是**跨进程分类，再走本地映射
// 只会落 default 降成 unknown——transient / rate_limited / self_reported 这些
// 「可重试」会被降成「不可重试」，permanent / authentication 等的处置文案也
// 全部丢失。故先判 AllClasses 命中并原样返回，未命中再走本地诊断词表。
func kindForErrorKind(kind string) string {
	for _, c := range toolfail.AllClasses() {
		if kind == string(c) {
			return string(c)
		}
	}
	if info, ok := errorKindTable[kind]; ok {
		return string(info.Class)
	}
	return string(toolfail.ClassUnknown)
}

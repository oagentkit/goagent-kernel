// errorkind.go —— 失败类别的**单一真相**（issue #365 / #364）。
//
// # 为什么要有单一真相
//
// 「程序失败属于哪一类」这件事在本包有两个消费者，此前各写一份**手写 switch**：
//
//   - kindForErrorKind（kind.go）：映射成 toolfail 的失败分类（跨进程 wire 的
//     分类出口，也是沙箱执行面的失败分类出口）；
//   - suggestionsFor（diagnostic.go）：给模型的修复建议。
//
// 两份的覆盖面**互不相等**，而本包实际产出的 ErrorKind 又散落在各文件的
// 字符串字面量里（ptc.go / execute.go / binding.go / child_run.go 各写一遍）。
// 于是「新增一个失败类别」这件事是**静默**的：两份 switch 都落各自的
// default ⇒ 新错误既没有失败分类（折成 unknown，丢掉归因与可重试语义），
// 模型侧也拿不到任何修复建议——**没有编译期信号，也没有任何测试会红**。
//
// 故本文件是词表的**唯一声明面**：
//
//   - 导出的常量集（本包所有产出点引用它们，不再写字面量）；
//   - AllErrorKinds()（词表全集）；
//   - errorKindTable（词表 → 处置分类 + 修复建议的**唯一一张表**）。
//
// 两份判据都从这张表派生，而词表里的每个取值都必须在这张表里有明确归属
// （由 ptc/errorkind_test.go 钉住：新增常量而不同步这张表 ⇒ 断言转红）。
//
// # 两套词表不是一回事
//
// 本包的 ErrorKind 是**诊断口径**（给模型看的形态名：syntax / deadline / …），
// toolfail 的分类是**处置口径**（可重试 / 需人工 / 永久拒绝）。两者不是同一
// 个维度，故需要一张映射表。
//
// 但宿主绑定抛出的规范失败（*toolfail.Failure）**本身就带处置口径**——
// rejectionKindOf 探到的就是 f.Class。那类值**不能**再走一遍映射表：
// 再映射一次就是把处置口径折回处置口径，可重试的 transient / rate_limited /
// self_reported 会被降级成不可重试的 unknown，「重试有意义」这条信息在最后
// 一跳丢失（issue #364）。故 kindForErrorKind 对已是 toolfail 分类的值
// **幂等透传**。
package ptc

import "github.com/oagentkit/guardx/toolfail"

// 本包产出的失败类别（诊断口径）。
//
// 全部产出点引用这些常量：字面量散落各文件时，「新增一个类别」没有任何
// 编译期信号，也没有任何测试会红（两份手写 switch 各自静默落 default）。
const (
	// ErrorKindSyntax 程序体语法错误（预检期即拒，带行号）。
	ErrorKindSyntax = "syntax"
	// ErrorKindBadResult return 值不可 JSON 序列化（或不是 Promise）。
	ErrorKindBadResult = "bad_result"
	// ErrorKindRuntime JS 异常、宿主绑定报错或执行被中断。
	ErrorKindRuntime = "runtime"
	// ErrorKindPending await 永不落定的 Promise（VM 无定时器）。
	ErrorKindPending = "pending"
	// ErrorKindBindingCollision 绑定撞名（装配期缺陷，见 findBindingCollision）。
	ErrorKindBindingCollision = "binding_collision"
	// ErrorKindBindingSignature 调用形态非法或与绑定名不一致（装配期缺陷）。
	ErrorKindBindingSignature = "binding_signature"
	// ErrorKindDeadline 墙钟到点。
	ErrorKindDeadline = "deadline"
	// ErrorKindBudgetCalls 工具调用次数超限。
	ErrorKindBudgetCalls = "budget_calls"
	// ErrorKindBudgetBytes 返回字节超限。
	ErrorKindBudgetBytes = "budget_bytes"
	// ErrorKindHostPanic 宿主绑定自身 panic（宿主侧的缺陷，不是程序写错）。
	ErrorKindHostPanic = "host_panic"
	// ErrorKindStageDenied 判定环判定拒绝（正常裁决）。
	ErrorKindStageDenied = "stage_denied"
	// ErrorKindStageFailed 判定环自身故障（宿主门禁坏了，见 stageOutcome）。
	ErrorKindStageFailed = "stage_failed"
	// ErrorKindPermissionDenied 权限不足（宿主显式归类的规范失败）。
	ErrorKindPermissionDenied = "permission_denied"

	// ErrorKindUnmanaged 未受管执行被拒：装配了工具绑定，却没装判定环。
	//
	// 它与 stage_denied / stage_failed 分开不是分类洁癖：
	//   - stage_denied：判定环**判了**，结论是不许（正常裁决）；
	//   - stage_failed：判定环**自己坏了**（宿主门禁的故障）；
	//   - unmanaged：判定环**压根不在**，于是无人判过——这是「没接线」，
	//     不是「判了不许」。
	//
	// 三者的处置完全不同：前两者改程序或修门禁代码，第三者**必须先装上门禁**。
	// 合成一类的话，调用方会去改一段本来没问题的程序，而真正该做的是把门禁
	// 接上。故它有自己的失败分类与修复建议（见 errorKindTable），
	// **不落 default 桶**（落进去就等于说「不可分类」，而它是可分类的）。
	ErrorKindUnmanaged = "unmanaged"
)

// AllErrorKinds 本包失败类别的全集（词表的唯一声明）。
//
// 它是**诊断口径**的词表，不是「本包一定产出的全集」：Result.ErrorKind 还可以
// 是宿主带来的 toolfail 分类（transient / permanent / …），那类值由
// kindForErrorKind 幂等透传，不在本地词表里。
//
// 新增类别的流程（漏第三步会被断言拦下）：
//  1. 加常量（上面那组）；
//  2. 加进本函数；
//  3. 在 errorKindTable 里给它处置分类与修复建议。
func AllErrorKinds() []string {
	return []string{
		ErrorKindSyntax,
		ErrorKindBadResult,
		ErrorKindRuntime,
		ErrorKindPending,
		ErrorKindUnmanaged,
		ErrorKindBindingCollision,
		ErrorKindBindingSignature,
		ErrorKindDeadline,
		ErrorKindBudgetCalls,
		ErrorKindBudgetBytes,
		ErrorKindHostPanic,
		ErrorKindStageDenied,
		ErrorKindStageFailed,
		ErrorKindPermissionDenied,
	}
}

// errorKindSpec 一个失败类别的完整口径。
type errorKindSpec struct {
	// class toolfail 的失败分类（处置口径：可重试 / 需人工 / 永久拒绝）。
	class toolfail.Class
	// suggestions 给模型的修复建议（诊断口径：怎么改）。非空——建议是
	// 给模型读的契约，与类别一一对应（见 diagnostic.go 的说明）。
	suggestions []string
}

// errorKindTable 词表 → 口径的**唯一一张表**。
//
// 两份判据都读它：kindForErrorKind 取 class，suggestionsFor 取 suggestions。
// 故「同一个类别两处不同措辞/不同分类」这件事在结构上不可能发生。
//
// class 的取法：
//
//   - 预算类（budget_calls / budget_bytes）→ 限流类（可重试但要退避）；
//   - 确定性失败（syntax / binding_* / bad_result / pending）→ 永久类
//     （重试无意义）；
//   - 超时与中断 → 瞬时类（值得重试）；
//   - 判定环拒绝（stage_denied）→ 权限类（重试也不会满足）；
//   - 判定环故障（stage_failed）→ unknown（宿主门禁坏了：程序改不了它，
//     也不是「重试就会好」的瞬时故障——保守侧不可重试，issue #65）；
//   - 未受管执行（unmanaged）→ 永久类（没接判定环这件事不会因重试而改变；
//     它与 stage_denied / stage_failed 的差别在**建议**里说清，见下）；
//   - 宿主绑定 panic（host_panic）→ 永久类（宿主侧的缺陷，原样重试同样崩）。
var errorKindTable = map[string]errorKindSpec{
	ErrorKindSyntax: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"检查括号、引号、模板字符串是否配对",
			"程序体是 async 函数体：顶层可直接用 await 与 return",
		},
	},
	ErrorKindBadResult: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"return 值必须是 JSON 可序列化的：不能返回函数或循环引用",
			"返回数组/对象字面量即可；字符串请确保是合法 JSON 文本",
		},
	},
	ErrorKindRuntime: {
		class: toolfail.ClassTransient,
		suggestions: []string{
			"宿主绑定抛出的错误会直接冒泡：确认调用参数形状与该工具的契约一致",
			"可以用 try/catch 包裹可疑调用，让程序带着已取得的部分结果继续",
		},
	},
	ErrorKindPending: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"await 永不落定：VM 无定时器，宿主绑定均为同步调用，程序内不存在会自己 resolve 的 Promise",
			"改造成对已 resolve 值的同步调用链",
		},
	},
	ErrorKindUnmanaged: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"这次执行被拒是因为**判定环没装**：装配了工具绑定却没有判定环，改程序不会有任何用",
			"请宿主装配 invoke.Stage 判定环（与普通工具调用同一套）；若本层工具确由别处管控，请显式声明",
		},
	},
	ErrorKindBindingCollision: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"这是装配期缺陷而非程序问题：两个工具名净化成同一个 host 标识符（如 get-logs 与 get_logs）",
			"请宿主重命名其中一个工具后重试，程序本身无需改动",
		},
	},
	ErrorKindBindingSignature: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"这是装配期缺陷而非程序问题：绑定的调用形态非法，或形态里的函数名与该绑定在 host 下的名字不一致",
			"请宿主修正绑定的调用形态后重试，程序本身无需改动",
		},
	},
	ErrorKindDeadline: {
		class: toolfail.ClassTransient,
		suggestions: []string{
			"墙钟超时：把长耗时操作拆成多次调用，或先取小结果再决定是否继续",
			"程序内不要 await 一个永不落定的 Promise（VM 无定时器）",
		},
	},
	ErrorKindBudgetCalls: {
		class: toolfail.ClassRateLimited,
		suggestions: []string{
			"工具调用次数超限：把多次小调用合并成一次带筛选条件的调用",
			"必要时改用结构化查询一次取回，而不是逐条遍历",
		},
	},
	ErrorKindBudgetBytes: {
		class: toolfail.ClassRateLimited,
		suggestions: []string{
			"返回字节超限：不要把工具原始输出原样 return，用 map/filter 聚合后只返回小结果",
			"原始数据在程序内处理，不要带过模型面",
		},
	},
	ErrorKindHostPanic: {
		class: toolfail.ClassPermanent,
		suggestions: []string{
			"这是宿主侧缺陷（绑定函数自己崩了），不是程序写错：改写法、换参数、try/catch 都无济于事",
			"把这个 panic 连同 tool 名与参数报给宿主维护者；排除前避开该工具",
		},
	},
	ErrorKindStageDenied: {
		class: toolfail.ClassPermissionDenied,
		suggestions: []string{
			"这次调用被宿主的门禁判定拒绝：不是参数形状错，也不是程序 bug，改写法重试不会通过",
			"改用被允许的工具或路径；确需该操作时先与宿主确认审批或白名单流程",
		},
	},
	ErrorKindStageFailed: {
		class: toolfail.ClassUnknown,
		suggestions: []string{
			"这次失败来自宿主门禁自身（判定环崩溃或调用没有可判定的对象），不是程序的问题：改程序不会有任何用",
			"同类调用请稍后重试；若持续失败，请宿主排查门禁代码与该工具的注册信息",
		},
	},
	ErrorKindPermissionDenied: {
		class: toolfail.ClassPermissionDenied,
		suggestions: []string{
			"这次调用因权限不足被拒：不是参数形状错，改写法重试不会通过",
			"改用被允许的工具或路径；确需该操作时先与宿主确认权限或白名单流程",
		},
	},
}

// isToolfailClass 判某个词是否**已是** toolfail 的失败分类（处置口径）。
//
// 它是幂等透传的前置判据（issue #364）：宿主绑定抛出的规范失败带的分类
// 本身就是处置口径，再走一遍映射表会把它折回 unknown——可重试的 transient /
// rate_limited / self_reported 因此被降级成不可重试，「重试有意义」这条信息
// 在最后一跳丢失。
func isToolfailClass(kind string) bool {
	for _, c := range toolfail.AllClasses() {
		if string(c) == kind {
			return true
		}
	}
	return false
}

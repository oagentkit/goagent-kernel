// Package dispatch 派发域的**失败面**：改派错误分类（transient/fatal/other 三档）
// 与同域/跨域兜底改派环（issue #169，下沉自宿主 internal/engine/scheduler/experts/dispatch.go）。
//
// 生命周期: Experimental（v0.38.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 它回答的是第四问
//
// 多执行者编排有四问，前三问各有归属，第四问此前无人回答：
//
//	① 这次派发**能不能**发生？   → 上游 oagentkit/agent/dispatch（allow 矩阵 +
//	   深度上限 + 自派发拒绝；原 dispatch 门面 v0.35.0 已整包删除，消费方直连上游）
//	② 排队**怎么排**？           → scheduling（优先级/公平/配额/背压）
//	③ 同一次调用要不要**原样重发**？ → guardx/toolfail（处置层语义，单源）
//	④ 失败后**还能不能再派、派给谁**？ → 本包
//
// 本包是**新立的机制包**，不是可达性门面：不碰 allow 矩阵、深度上限、
// 自派发拒绝三条判定，也不做队列、不排序、不执行（执行步与候选枚举都经
// 回调注入，见 StickyFailover）。
//
// # 两条硬纪律（包头写死，测试各钉一条）
//
//  1. **判定面只读首次失败的原始错误。** 改派环会用
//     `%w（已同域改派 X 仍失败）` 把改派说明拼进累计错误——那类文本**只进事件
//     与最终消息**（给人看），不进 Classify 的判定面。理由：文本兜底按关键词判定，
//     说明里若混进「权限」「连接」这类词，一次瞬态会被误判成 fatal（跨域兜底被
//     错误抑制），或 fatal 被误判成 transient（多烧一次跨域改派）。判定面只应看
//     **失败本身**，不该看编排过程。
//  2. **状态码判定必须 `\b` 词边界。** 裸 `strings.Contains(msg, "403")` 会被
//     「省略中段 4032 字节」这类字节数/行号命中：瞬态被误判成 fatal，或
//     「共 429 行」把 fatal 误升级成 transient。词边界把「数字是更长数字的
//     一部分」整类排除。
//
// # 三档词表
//
//	transient 瞬态：可跨域兜底改派（超时/连接类）。
//	fatal     终态类：换执行者也是白跑（权限/凭证/审批/治理类）——直接终态。
//	other     不确定：仅同域重试，不跨域。
//
// # 两级判定：上游单源优先，文本只兜底
//
// 第一级消费 `toolgate/policy.Disposition`——它是「toolfail 链 → MCP 协议层」
// 两级合成的**唯一**实现，本包零文本匹配。链与协议层都判不出类型
// （ClassUnknown）才落第二级文本兜底（词表内置在本包）。类型已判出时**不看文本**：
// 「类型说了 A、文案说 B 时听文案」与判定纪律相反。
//
// # 与上游的两轴映射（不是照搬）
//
// 上游回答「同一次调用要不要原样重发」，本包回答「换一个执行者有没有意义」——
// 故逐档映射而非照搬：
//
//	上游处置类            本包档     换执行者有没有意义
//	transient             transient  有：基础设施抖动，换执行者/换后端可能通
//	rate_limited          transient  有：限流在服务端侧，换执行者可能绕开
//	self_reported         transient  有：生产者自报可重试
//	permanent             other      有：「能力不支持这类请求」⇒ 换**能力**可能支持
//	authentication        fatal      无：同一套凭证，换执行者不刷新凭证
//	permission_denied     fatal      无：同一套权限面，需人工放开
//	not_found             other      有：换执行者可能指向别的目标/用别的工具
//	invalid_argument      other      有：换执行者的参数构造不同，可能就对了
//	unknown               →文本兜底  不可分类 = 保守（不乐观）
//
// 关键分歧：上游把 not_found / invalid_argument 判「不可重试」，本包的「重试」
// 却是**换执行者**——换执行者正是「换构造参数的人 / 换目标对象」⇒ 不终态，
// 只是不跨域。
package dispatch

import (
	"regexp"
	"strings"

	"github.com/oagentkit/guardx/toolfail"
	toolgatepolicy "github.com/oagentkit/toolgate/policy"
)

// Class 改派错误档（三档词表，机读值；取值见下常量集）。
//
// 用字符串而非整数枚举：档名要随事件载荷落日志、随调用链跨进程传递，
// 数字序一旦被无意依赖就会变成隐藏契约。
type Class string

const (
	// ClassTransient 瞬态：可跨域兜底改派（超时/连接类）。
	ClassTransient Class = "transient"
	// ClassFatal 终态类：换执行者也是白跑（权限/凭证/审批/治理类）——直接终态。
	ClassFatal Class = "fatal"
	// ClassOther 不确定：仅同域重试，不跨域。
	ClassOther Class = "other"
)

// String 机读名（进事件载荷与日志）。
func (c Class) String() string { return string(c) }

// transientStatus / fatalStatus：HTTP 状态码判定**必须带词边界**（纪律②）。
//
// 为什么不能用裸 strings.Contains("403")：判定面吃的是宿主包装后的错误文本，
// 其中常拼进工具输出片段，而输出超预算的提示文案是「省略中段 %d 字节（全长 %d）」。
// 字节数、行号、端口里出现 3 位数字是常态——「省略中段 4032 字节」会被裸
// Contains("403") 命中判成 fatal → 瞬态跨域兜底被错误抑制；「共 429 行」
// 这类计数则会误升级成 transient → 白烧一次跨域改派（额外调用与配额）。
// \b 词边界把「数字是更长数字的一部分」整类排除。
var (
	transientStatus = regexp.MustCompile(`\b(429|502|503|504)\b`)
	fatalStatus     = regexp.MustCompile(`\b(401|403)\b`)
)

// transientWords / fatalWords：第二级文本兜底词表（链与协议层都 Unknown 才走）。
var (
	transientWords = []string{
		"timeout", "deadline", "context canceled", "connection", "connect",
		"refused", "unavailable", "temporarily",
	}
	fatalWords = []string{
		"权限", "未授权", "unauthorized", "forbidden", "审批", "黑名单",
		"拒绝", "checksum", "身份校验",
	}
)

// Classify 一次失败的改派档位（两级判定，见包头）。
//
// err 为 nil 时返回 ClassOther（没有失败可处置，保守按「仅同域」处理——
// 既不跨域也不终态）。
//
// # 判定面只读这个 err 本身（纪律①）
//
// 调用方**必须**把首次失败的原始错误传进来，不要传改派环累计出来的错误：
// 累计错误里的改派说明只进事件与最终消息。StickyFailover 已按此实现
// （它自己只在进入环之前判一次），此处再写明是因为宿主也可能绕过环直接调本函数。
func Classify(err error) Class {
	if err == nil {
		return ClassOther
	}
	// 第一级：上游单源（toolfail 链 → MCP 协议层），全程零文本匹配。
	switch toolgatepolicy.Disposition(err) {
	case toolfail.ClassTransient, toolfail.ClassRateLimited, toolfail.ClassSelfReported:
		return ClassTransient
	case toolfail.ClassAuthentication, toolfail.ClassPermissionDenied:
		return ClassFatal
	case toolfail.ClassPermanent, toolfail.ClassNotFound, toolfail.ClassInvalidArgument:
		// 「换执行者有没有意义」在这三档上都是**有**（见包头两轴映射表）⇒ other。
		return ClassOther
	}
	// ClassUnknown：链与协议层都判不出类型 ⇒ 落第二级文本兜底。
	l := strings.ToLower(err.Error())
	for _, w := range transientWords {
		if strings.Contains(l, w) {
			return ClassTransient
		}
	}
	if transientStatus.MatchString(l) {
		return ClassTransient
	}
	for _, w := range fatalWords {
		if strings.Contains(l, w) {
			return ClassFatal
		}
	}
	if fatalStatus.MatchString(l) {
		return ClassFatal
	}
	return ClassOther
}

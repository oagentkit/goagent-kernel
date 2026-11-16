// Package pii PII 价值模式检测与打码（批次二百七十八，对标 octop-harness
//
// 生命周期: Stable（v0.33.0 定级）——只允许 additive 变更（新增导出符号 /
// 新增方法且给零值默认实现 / 新增错误值）；改签名、改语义、改默认值、
// 删导出符号都算 breaking，判定规则与变更流程见 docs/api-compat.md，
// 逐包理由见 api-lifecycle.yaml。
//
// PIIMiddleware 的价值面检测）：在 logredact（agentkit，凭据类值模式：
// key=xxx/Authorization/JWT/sk- 前缀等）之外补「个人信息价值面」——大陆手机号与
// 18 位居民身份证。凭据脱敏按形态命中，个人信息没有键名可依（批266「logredact
// 键名盲区」教训的價值面延伸：审计链永久留存+SIEM 外发，工单摘要外发 Jira，
// 值本身即敏感，与键名无关——键位上出现的同类值走同一条判定面，见下）。
//
// 保守检测纪律（octop 同款）：宁可漏打不可错打。身份证须过四重校验——GB/T 2260
// 省级码白名单 + 出生日期合法（含未来拒绝）+ 顺序码非全零 + MOD 11-2 校验码——
// 订单号/流水号/银行卡号等长数字串几乎不可能同时通过；手机号要求两侧不邻接
// 字母数字（Go RE2 无环视断言，边界在代码判定），13 位毫秒时间戳因尾邻数字不
// 会误命中。数字形态归一化（#297，normalize.go）在匹配前展开：全角 ASCII 变体
// 折半、零宽字符剔除、3-4-4 分组手机号按标准书写形态收口（分组连排 / 叠用 /
// 其他分组形状一律不认——宁可漏打）；归一化与判定共用一条路径，防误命中语义
// 不被形态扩张拉宽。缺省策略 mask 保长度（前段+星号+尾段），日志对齐与残留
// 定位友好；其余策略的处置形态由宿主注入的 StrategyKind 决定（见 policy.go）。
//
// 结构面：ApplyValue（按敏感面）递归下探 map[string]any /
// map[string]string / []any / []string——其中 map[string]string 是 agentkit
// logredact.RedactValue 的已知结构盲区（alert_svc redactMap 绕行注释在案），
// 本包一并收口。**键与值走同一条判定面**：以手机号为键的载荷（手机号 → 订单/
// 客户资料，中文业务系统的一等形态）不再整键明文外泄，键命中与值命中同样产出
// span 级处置与 blocked 信号；普通键逐字不变；不同原键处置出同一形态时按原键
// 字典序加 "#n" 消解——不丢条目，也不依赖 map 的随机迭代序。nil 容器原样保留：
// JSON 的 null 与空容器在审计链上是两种形状，处置过程无权把它们合并成一种。
//
// 本包是零内部依赖的叶子层（architecture policy 模块 id=pii，requires=[]，
// 错误走 oekit/pkg/errors 单 import）：出口面（审计链/事件双出口/工单与告警外发文案）
// 无条件调用本包，并按各自敏感面走 Apply / ApplyValue；开关收敛在 Enabled 与面开关
// （缺省全开）——run.go 从 conf security.pii_value_mask 注入全局开关（缺省开；
// 显式关闭时启动 WARN 显性化）。
package pii

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kind PII 类型（span 标注与打码策略分派用）。
type Kind string

const (
	KindMobile     Kind = "cn_mobile"      // 大陆手机号（可带 +86/0086 前缀）
	KindResidentID Kind = "cn_resident_id" // 18 位居民身份证（末位 X 大小写均可）
)

// Span 一段命中（s 的字节偏移，[Start,End)）。
type Span struct {
	Kind  Kind
	Start int
	End   int
}

// 手机号：可选 +86/0086 前缀 + 1[3-9] 开头 11 位。裸 86 前缀不认（与数字串
// 无法区分，保守放弃）。
var mobileRe = regexp.MustCompile(`(?:\+86|0086)?1[3-9][0-9]{9}`)

// groupedMobileRe 手机号的**标准 3-4-4 分组**书写形态（#297）：1[3-9] 开头，
// 组间单个分隔符（`-` / `.` / 空格——归一化后全角连字符/句点/表意空格也落到
// 这三类）。误检面口径（评估后写死）：分组**连排**（3-4-4-4 的分组列数据）、
// 分隔符**叠用**、**其他分组形状**（4-4-4-4 银行卡等）一律不认——宁可漏打
// 不可错打；命中之后再过 groupedTrailingOK 的「不得紧跟分隔符+数字」护栏。
// 身份证不设分组形态（分组书写罕见，而 18 位数字串的误检面比手机号宽）。
var groupedMobileRe = regexp.MustCompile(`(?:(?:\+86|0086)[-. ]?)?1[3-9][0-9][-. ][0-9]{4}[-. ][0-9]{4}`)

// 身份证粗筛：17 位数字 + 校验位（数字或 X/x）。语义校验在 validResidentID。
var residentIDRe = regexp.MustCompile(`[0-9]{17}[0-9Xx]`)

// gb2260Provinces GB/T 2260 省级码（含 71 台湾、81/82 港澳）。
var gb2260Provinces = map[string]bool{
	"11": true, "12": true, "13": true, "14": true, "15": true,
	"21": true, "22": true, "23": true,
	"31": true, "32": true, "33": true, "34": true, "35": true, "36": true, "37": true,
	"41": true, "42": true, "43": true, "44": true, "45": true, "46": true,
	"50": true, "51": true, "52": true, "53": true, "54": true,
	"61": true, "62": true, "63": true, "64": true, "65": true,
	"71": true, "81": true, "82": true,
}

// idWeights MOD 11-2 加权因子与校验码表（GB 11643-1999）。
var idWeights = [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}

const idCheckChars = "10X98765432"

// Detect 找出 s 中全部 PII span（已完成语义校验与边界判定，重叠时保留更长者）。
// 返回按 Start 升序、互不重叠；坐标是**原文**字节偏移，处置面拿原文切片。
//
// # 数字形态归一化（#297）
//
// 匹配与判定跑在「判定视角」文本上（normalize.go）：全角 ASCII 变体折半、
// 表意空格折空格、零宽字符剔除，span 经 formMap 映射回原文——归一化与判定
// 共用一条路径，防误命中语义不被形态扩张拉宽。纯 ASCII 输入视角即原文
// （零分配快路），既有行为逐字节不变。
func Detect(s string) []Span {
	if len(s) < 11 {
		return nil
	}
	if asciiOnly(s) {
		if !strings.ContainsFunc(s, hasASCIIDigit) {
			return nil
		}
		return detectIn(s, nil) // 视角 == 原文，映射免掉
	}
	view, starts, ends := normalizeForm(s)
	if !strings.ContainsFunc(view, hasASCIIDigit) {
		return nil
	}
	return detectIn(view, &formMap{starts: starts, ends: ends})
}

// detectIn 在判定视角文本上跑三类形态匹配，span 映射回原文坐标后消解重叠。
func detectIn(view string, m *formMap) []Span {
	var out []Span
	for _, match := range mobileRe.FindAllStringIndex(view, -1) {
		if !boundaryOK(view, match[0], match[1]) {
			continue
		}
		start, end := m.span(match[0], match[1])
		out = append(out, Span{Kind: KindMobile, Start: start, End: end})
	}
	for _, match := range groupedMobileRe.FindAllStringIndex(view, -1) {
		if !boundaryOK(view, match[0], match[1]) || !groupedTrailingOK(view, match[1]) {
			continue
		}
		start, end := m.span(match[0], match[1])
		out = append(out, Span{Kind: KindMobile, Start: start, End: end})
	}
	for _, match := range residentIDRe.FindAllStringIndex(view, -1) {
		if !boundaryOK(view, match[0], match[1]) {
			continue
		}
		if !validResidentID(view[match[0]:match[1]]) {
			continue
		}
		start, end := m.span(match[0], match[1])
		out = append(out, Span{Kind: KindResidentID, Start: start, End: end})
	}
	return dedupeSpans(out)
}

// groupedTrailingOK 分组命中的尾护栏：命中之后不得紧跟「分隔符串+数字」——
// 那是分组列数据（多组连排），不是单号；宁可漏打。普通符号收尾（句号、
// 括号等）放行。
func groupedTrailingOK(view string, end int) bool {
	i := end
	for i < len(view) && (view[i] == '-' || view[i] == '.' || view[i] == ' ') {
		i++
	}
	return i >= len(view) || !isASCIIDigit(view[i])
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

func hasASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// boundaryOK 命中两侧不得邻接字母数字（RE2 无环视，代码判定；下划线/连字符等
// 符号边界放行——「tel:13812345678」「工单-11010519491231002X」应打码）。
func boundaryOK(s string, start, end int) bool {
	if start > 0 && isWordByte(s[start-1]) {
		return false
	}
	return end >= len(s) || !isWordByte(s[end])
}

func isWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// validResidentID 四重校验（保守面）：省码白名单 + 出生日期合法且不在未来 +
// 顺序码非全零 + MOD 11-2 校验码。15 位旧证不检（已停用 20 年，纯 15 位数字
// 误伤面大，octop 同取舍）。
func validResidentID(s string) bool {
	if len(s) != 18 {
		return false
	}
	if !gb2260Provinces[s[:2]] {
		return false
	}
	birth, err := time.Parse("20060102", s[6:14])
	if err != nil || birth.Year() < 1900 || birth.After(time.Now()) {
		return false
	}
	if s[14:17] == "000" {
		return false
	}
	sum := 0
	for i := range 17 {
		d, err := strconv.Atoi(s[i : i+1])
		if err != nil {
			return false
		}
		sum += d * idWeights[i]
	}
	return idCheckChars[sum%11] == strings.ToUpper(s[17:])[0]
}

// dedupeSpans 重叠消解：起点升序、同起点长者优先，贪心保留不重叠集——身份证
// （18 位）吞掉其内部可能的手机号子串命中。
func dedupeSpans(in []Span) []Span {
	if len(in) < 2 {
		return in
	}
	slices.SortFunc(in, func(a, b Span) int {
		if c := cmp.Compare(a.Start, b.Start); c != 0 {
			return c
		}
		return cmp.Compare(b.End-b.Start, a.End-a.Start)
	})
	var out []Span
	lastEnd := -1
	for _, sp := range in {
		if sp.Start >= lastEnd {
			out = append(out, sp)
			lastEnd = sp.End
		}
	}
	return out
}

// maskSpan 保长度打码：手机号留前 3 尾 4（+86/0086 前缀保留——归属信息不敏感），
// 身份证留前 4（省市级可见；生日、顺序码、MOD 11-2 校验位全掩）。
//
// 校验位**不保留**（issue #298）：MOD 11-2 校验位原样留下，就成了对
// 「生日 × 顺序码 × 区县」候选的免费验算器——原本候选还需自洽才算命中。
// 留前 4 已够宿主做省市级定位，尾 2 没有等价的定位价值。
//
// # 非 ASCII 形态先过同一条归一化（#297）
//
// 识别面新增的形态（全角/零宽/分组）进到处置面时拿到的是**原文切片**，直接按
// ASCII 取位会把全角的多字节切烂。故先 normalizeText 取位——处置输出的数字
// 形态恒为半角 ASCII；既有 ASCII 连续形态的输出逐字节不变。代价：非连续形态
// 的字节长度不保（全角 33 字节 → 掩码 11 字节），保长性质仅在原形态为 ASCII
// 连续时成立（USAGE 已声明）。形态异常（位数对不上）时全掩 fail-closed：
// 处置面拿不到可定位的前后段，就一段都不留。
func maskSpan(v string, kind Kind) string {
	switch kind {
	case KindMobile:
		return maskMobile(v)
	case KindResidentID:
		return maskResidentID(v)
	}
	return strings.Repeat("*", len(v))
}

// maskMobile 手机号掩码：末 11 位数字为本地号（留前 3 尾 4），其余为前缀
// （+86/0086，分组间分隔符不保留——与连续形态输出同形）。
func maskMobile(v string) string {
	n := normalizeText(v)
	idx, digits := trailingDigits(n, 11)
	if idx < 0 {
		return strings.Repeat("*", len(v)) // 形态异常：全掩，不放行任何数字
	}
	prefix := strings.Map(func(r rune) rune {
		if r == '-' || r == '.' || r == ' ' {
			return -1 // 分组间分隔符不进输出（与连续形态同形）
		}
		return r
	}, n[:idx])
	return prefix + string(digits[:3]) + "****" + string(digits[7:])
}

// maskResidentID 身份证掩码：留前 4（省市级可见），生日、顺序码与校验位全掩
// （#298 收紧：校验位不保留——它是「生日 × 顺序码 × 区县」候选的免费验算器）。
func maskResidentID(v string) string {
	n := normalizeText(v)
	if len(n) != 18 {
		return strings.Repeat("*", len(v)) // 形态异常：全掩
	}
	return n[:4] + strings.Repeat("*", 14)
}

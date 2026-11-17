package steer

import (
	"strings"

	"github.com/yi-nology/goagent-kernel/contract"
)

// 摘要正文的机械骨架（文案头由宿主注入，正文不带任何领域词）。
const (
	summaryLead  = "当前编排被新输入中断，已完成环节："
	summaryEmpty = "（尚未完成任何环节）"
	summaryTail  = "未完成部分将结合新输入重新评估。"
	// degradedLead 读取失败的降级正文：不因一次读取失败中断收尾
	//（收尾在脱离 ctx 里跑，引入新的失败面等于把「保产」变成「丢产」）。
	degradedLead = "当前编排被新输入中断（环节明细读取失败）。"
)

// 摘要截断上限的缺省值（对齐宿主实测口径：单环节一条短摘要、整份控制在一屏内）。
const (
	// DefaultStepSummaryCap 单环节摘要缺省上限（字符）。
	DefaultStepSummaryCap = 150
	// DefaultReportCap 整份摘要缺省上限（字符）。
	DefaultReportCap = 1200
)

// DoneStep 一个环节的注入事实（宿主从环节行取；本包不读库）。
//
// Status 是**环节状态**，词表以 contract 为准（BuildSummary 只计
// contract.StatusSucceeded；statemachine 的状态常量是同一批值的别名，
// issue #354/#357）——skipped / rolled_back / rejected / failed 无有效产出，
// 失败环节留给下一轮重新评估。宿主的环节状态词表若不同，在注入边界上映射，
// 不要改本包的判定词。
type DoneStep struct {
	Slug    string // 环节标识
	Summary string // 环节产出摘要（宿主从环节报告提取；畸形报告按空处理）
	Status  string // 环节状态（contract 词表；statemachine 同值别名）
}

// Caps 摘要截断上限（零值取缺省；负值 = 不截断）。
type Caps struct {
	// StepSummary 单环节摘要上限（0 → DefaultStepSummaryCap）。
	StepSummary int
	// Report 整份摘要上限（0 → DefaultReportCap）。
	Report int
}

// withDefaults 零值补缺省（负值原样透出 = 不截断，与 drain.Budget 的
// 「负值是显式语义、不归一」同一条纪律）。
func (c Caps) withDefaults() Caps {
	if c.StepSummary == 0 {
		c.StepSummary = DefaultStepSummaryCap
	}
	if c.Report == 0 {
		c.Report = DefaultReportCap
	}
	return c
}

// BuildSummary 保产摘要的机械拼接（纯函数，**不调 LLM**）：
// 只计成功环节，逐条 slug：摘要，截断后拼成正文。
//
// 输入是宿主注入的环节清单（含状态），输出是**正文**——文案头（如标题行）
// 留宿主拼接（compose 由 Run 调用；宿主直接用本函数时自行前置）。
// 畸形/超长输入不报错：单条按空摘要处理、整份按 Caps.Report 截断——
// 摘要是收尾期的保产面，宁可少一段也不许把收尾卡死。
func BuildSummary(steps []DoneStep, caps Caps) string {
	caps = caps.withDefaults()
	var b strings.Builder
	b.WriteString(summaryLead)
	b.WriteByte('\n')
	n := 0
	for _, st := range steps {
		if st.Status != contract.StatusSucceeded {
			continue
		}
		b.WriteString("\n- ")
		b.WriteString(strings.TrimSpace(st.Slug))
		b.WriteString("：")
		b.WriteString(trunc(strings.TrimSpace(st.Summary), caps.StepSummary))
		n++
	}
	if n == 0 {
		b.WriteString(summaryEmpty)
	}
	b.WriteString("\n\n")
	b.WriteString(summaryTail)
	return trunc(b.String(), caps.Report)
}

// DegradedReport 环节清单读取失败时的降级正文（与 BuildSummary 同骨架位，
// 明示读取失败——「没有检查」与「检查通过」必须分得开）。
func DegradedReport() string { return degradedLead }

// compose 文案头（宿主注入）+ 机械正文；头为空则只落正文。
func compose(header, body string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return body
	}
	return header + "\n\n" + body
}

// trunc 按字符（rune）截断，超出时以 … 收尾；n<=0 = 不截断。
//
// 截断单位是 rune 不是 byte：按 byte 切会把多字节字符拦腰截断，
// 产出的报告直接带乱码。总长含省略号（n 位封顶）。
func trunc(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

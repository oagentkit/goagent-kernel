package main

// wordtable.go —— README 职责列里的**判定词表**接进机器判据（issue #384）。
//
// # 为什么要有这张表
//
// README 包表的「职责」列此前只判**非空**（docsync 明确声明「职责列散文不比对
// 内容」）。但有三处不是散文——它们是**判定词表**，代码侧都有导出常量：
//
//	trajectory  配对态（6 值）
//	watchdog    工具级缺口 reason（4 值）
//	finalize    四分路由终态（4 值）
//
// 这三张表在 README 里是字面量，在代码里是导出常量：同一事实两份表达，只有
// 一份有编译期保障。代码侧新增一个取值 ⇒ README 继续对外声明一份**过时的
// 词表** ⇒ 没有任何判据会红（读 README 的人按过时清单做对账/告警分类/路由
// ⇒ 漏一个状态 ⇒ 误判）。
//
// # 判据（双向）
//
//	漏列：词表的**全部非空取值**必须出现在该包的 README 行里；
//	多列：README 该行词表片段里出现的同族词，必须都在该词表内。
//
// # 取值必须从代码的导出常量派生
//
// 门禁自己再抄一份字面量 = 门禁自己也会漂：内核新增一个取值时，门禁那份不会
// 跟着变，且照样报「一切正常」。故本文件 import 那三个包，读它们的导出常量。
//
// # 抽片段靠锚点正则，锚点找不到必须**报红**
//
// README 是散文，词表藏在括号/从句里。锚点（抽取正则）不稳定就会静默漏判——
// 故「定位不到片段」与「词表一致」必须能区分：前者报红（措辞改了要同步改
// 抽取正则），后者才绿。

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yi-nology/goagent-kernel/finalize"
	"github.com/yi-nology/goagent-kernel/trajectory"
	"github.com/yi-nology/goagent-kernel/watchdog"
)

// wordTable 一张「README 职责列必须列全的词表」的声明。
type wordTable struct {
	// pkg README 包表里的包名。
	pkg string
	// label 人读的名字（报错用）。
	label string
	// region 从该包的 README 职责列里**抽出词表片段**的正则（第一个捕获组）。
	region *regexp.Regexp
	// values 词表取值（从代码的导出常量派生）。
	values []string
}

// wordTables 三张判定词表的声明面。
//
// ⚠️ 取值**不得**在门禁里另抄一份字面量——那会让门禁自己成为第四份会漂的
// 真相（内核新增取值时它不会跟着变，且照样报「一切正常」）。
var wordTables = []wordTable{
	{
		pkg:    "trajectory",
		label:  "配对态",
		region: regexp.MustCompile(`配对状态\*\*（([^）]*)）`),
		values: trajectoryPairingValues(),
	},
	{
		pkg:    "watchdog",
		label:  "工具级缺口 reason",
		region: regexp.MustCompile(`缺口按 ([a-z0-9_ /]*)`),
		values: watchdogReasonValues(),
	},
	{
		pkg:    "finalize",
		label:  "四分路由终态",
		region: regexp.MustCompile(`\*\*四分路由\*\*（([^）]*)）`),
		values: finalize.AllRouteStatuses(),
	},
}

// wordRe 同族词的形态：小写字母开头的下划线词（paired / timed_out / …）。
//
// 片段里混着中文散文（「接管不落终态回 created」）：中文既不是词表成员也不是
// 错列，按形态过滤掉比逐个维护「散文词表」稳。
var wordRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// trajectoryPairingValues trajectory 的配对态取值（**全部非空**）。
//
// 取值来自导出常量；PairingNone 是空串（非工具行），不属于词表。
func trajectoryPairingValues() []string {
	return nonEmptyValues(
		string(trajectory.PairingNone),
		string(trajectory.PairingPaired),
		string(trajectory.PairingMissingResult),
		string(trajectory.PairingDuplicateResult),
		string(trajectory.PairingUnpairedResult),
		string(trajectory.PairingDuplicateCall),
		string(trajectory.PairingNoCallID),
	)
}

// watchdogReasonValues watchdog 的工具级缺口 reason 取值。
//
// 取值来自导出常量（它们本身又引用 eventbus / trajectory 的单源常量）。
func watchdogReasonValues() []string {
	return nonEmptyValues(
		watchdog.ReasonDeclaredNotExecuted,
		watchdog.ReasonUnpairedResult,
		watchdog.ReasonDuplicateResult,
		watchdog.ReasonDuplicateCall,
	)
}

// nonEmptyValues 去掉空串（零值不属于词表）。
func nonEmptyValues(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// checkWordTables 判 README 职责列里的判定词表与代码一致（issue #384）。
//
// 双向：漏列（词表取值没出现在 README 行里）与多列（README 里出现的同族词
// 不在词表内）。抽不到词表片段也算一条问题——「定位不到」不得当成「一致」。
//
// 返回实际**判了几张**表：README 包表里没有那个包时跳过（判据面缺失不等于
// 漂移，与 USAGE/ADR 口径同款），但跳过要说出来——静默跳过与「判过了」在
// 日志里长得一样。
func checkWordTables(rows []row) (problems []string, checked int) {
	byPkg := make(map[string]string, len(rows))
	for _, r := range rows {
		byPkg[r.pkg] = r.desc
	}
	for _, wt := range wordTables {
		desc, ok := byPkg[wt.pkg]
		if !ok {
			continue
		}
		checked++
		m := wt.region.FindStringSubmatch(desc)
		if m == nil {
			problems = append(problems, fmt.Sprintf(
				"%s: README 职责列里找不到 %s 的词表片段（措辞变了？改文案要同步改门禁的抽取正则）",
				wt.pkg, wt.label))
			continue
		}
		listed := listTokens(m[1])
		var missing, extra []string
		for _, v := range wt.values {
			if !slices.Contains(listed, v) {
				missing = append(missing, v)
			}
		}
		for _, t := range listed {
			if !slices.Contains(wt.values, t) {
				extra = append(extra, t)
			}
		}
		if len(missing) > 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: README 漏列 %s 的取值 %v", wt.pkg, wt.label, missing))
		}
		if len(extra) > 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: README 多列 %s 的取值 %v（不在词表内）", wt.pkg, wt.label, extra))
		}
	}
	return problems, checked
}

// listTokens 从词表片段里抽出同族词。
func listTokens(fragment string) []string {
	fields := strings.FieldsFunc(fragment, func(r rune) bool {
		switch r {
		case '/', ' ', '\t', '、', ',', '，':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "*`")
		if wordRe.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}

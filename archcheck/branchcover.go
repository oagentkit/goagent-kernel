// branchcover.go —— 规则样例的**分支覆盖**判据：每条正则的每一个分支都必须
// 至少被一条 want: hit 的样例钉住。
//
// # 它补的是哪一类失效
//
// examples.go 已经保证三件事：样例与判据一致（走真实判定核）、正反各至少一条、
// 样例文本非空且 want 取值合法。但它**保证不了**「每一条正则的每一个分支
// 都被样例钉住」——样例通常只覆盖每条正则的**第一个分支**。
//
// 于是有一条非常具体的失效路径（issue #386 实测复现过）：
//
//  1. 维护者把 harness-single-errors-package 的第二个分支
//     （github.com/pkg/errors）删掉——以为「没人用第二套 errors 包」；
//  2. 样例的正例是 errors（命中第一支）、反例是 github.com/oekit/pkg/errors
//     （不命中）——两条样例照样通过；
//  3. require_rule_examples 的加载期自校验也通过；
//  4. 门禁**全绿**，而 github.com/pkg/errors 从此合法——
//     与 AGENTS.md 明写的「只允许这一套」相反。
//
// 失效形态是**变绿**（比变红更贵）：规则照常出现在报告里、severity 照常是
// error、唯独一件事都不判了。这正是「从没见过它触发的规则等于没有规则」的
// 最后一层——前两层（有没有执行体、样例与判据是否一致）已被 examples.go 兜住，
// 这一层（分支覆盖）是剩下的那一个。
//
// # 分支怎么切
//
// 按**文本上的括号/字符类/转义配平**切，不走 regexp/syntax 的语法树：那个
// 解析器会做**字面量前缀合并**（git\.a/x|git\.b/y 被改写成
// git(?:\.a/x|\.b/y)），于是语法树上的「顶层交替」与 policy 里作者写的
// 「顶层 |」已经不是一回事——而作者写的那个才是能被看见、能被改掉的东西。
//
// 只认两种交替：
//
//   - **根层交替**：不在任何括号里的 | （如 ^errors$|^github\.com/pkg/errors$）；
//   - **组内交替**：某个括号组里、**该组自己这一层**上的 | （如 (logf|logFn)）。
//
// 组内更深层的 | （嵌在子组里的）不归这一组管——否则一条正则会被拆成
// 指数级多个「分支」，样例要求也随之膨胀到没法写。
//
// # 什么样的分支才需要样例
//
// 只要求**有内容**的分支：至少含一个字母或数字（字符类里含字母数字也算）。
// 边界型的交替（(/|$) 里的 / 与 $）不是独立的一支——它们是同一条路径前缀的
// 两种边界写法，给它们各要一条样例等于要求作者为同一件事写两遍。
//
// # 覆盖怎么判
//
// 「这条样例是不是经由这一支命中的」不能靠「样例文本里含这个分支的字面量」
// 来判——那只是子串包含，不是**这一支**在起作用。判法是把整条正则改写成
// 「只留这一支」（其余上下文原样保留），再用**真实判定核**判一遍样例：
// 命中即说明这一支被样例钉住了。
//
// 判定核与生产判定同一份代码（regexViolationsIn / importViolationsIn），
// 否则样例那套与真判定那套会各自漂移——见 examples.go 头注的同一条理由。

package archcheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// altGroup 是一条正则里的一个「交替表达式」：同一层级上被 | 切开的若干分支。
type altGroup struct {
	// Start/End 是整个交替表达式在原正则里的字节范围（根层交替 = 整条正则；
	// 组内交替 = 该括号组的整体，含括号）。
	Start, End int
	// Branches 是各分支的文本与范围。
	Branches []altBranch
}

// altBranch 是一个分支。
type altBranch struct {
	Start, End int
	Text       string
}

// splitAlternations 列出正则里的全部交替表达式（根层 + 各组内）。
//
// 形态上只认两种：不在任何括号里的 | ，与某个括号组内**该组自己这一层**上的 | 。
// 转义（\x）与字符类（[...]）里的 | 不算分隔符。
func splitAlternations(pattern string) []altGroup {
	type frame struct {
		open   int        // ( 的位置；根层为 -1
		splits []int      // 该层级上 | 的位置
		groups []altGroup // 该层级内已经闭合的组内交替
	}
	frames := []frame{{open: -1}}
	escaped := false
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '[':
			if !inClass {
				inClass = true
			}
		case ']':
			if inClass {
				inClass = false
			}
		case '(':
			if inClass {
				break
			}
			frames = append(frames, frame{open: i})
		case ')':
			if inClass {
				break
			}
			if len(frames) == 1 {
				break // 不配平的 )：交给 regexp 编译去报错，不在这里猜
			}
			f := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			if len(f.splits) > 0 {
				g := groupAlternation(pattern, f.open, i+1, f.open+1, i, f.splits)
				frames[len(frames)-1].groups = append(frames[len(frames)-1].groups, g)
			}
		case '|':
			if inClass {
				break
			}
			cur := &frames[len(frames)-1]
			cur.splits = append(cur.splits, i)
		}
	}
	var out []altGroup
	root := frames[0]
	if len(root.splits) > 0 {
		out = append(out, groupAlternation(pattern, 0, len(pattern), 0, len(pattern), root.splits))
	}
	for _, f := range frames {
		out = append(out, f.groups...)
	}
	return out
}

// groupAlternation 把一组分隔点组装成一个交替表达式。
//
// 分支文本**不含**分隔用的 | ：上一版把分隔点直接当成了下一支的起点，
// 于是分支文本形如「|beta」——把它套进 (?:...) 后就是「空分支 | beta」，
// 空分支匹配任何字符串，于是**每一个样例都被判成「经由这一支命中」**，
// 覆盖判据整体失效（反向验证当场抓到：只有第一支报了未覆盖）。
func groupAlternation(pattern string, exprStart, exprEnd, contentStart, contentEnd int, splits []int) altGroup {
	g := altGroup{Start: exprStart, End: exprEnd}
	prev := contentStart
	for _, sp := range splits {
		g.Branches = append(g.Branches, altBranch{Start: prev, End: sp, Text: pattern[prev:sp]})
		prev = sp + 1 // 跳过分隔符本身
	}
	g.Branches = append(g.Branches, altBranch{Start: prev, End: contentEnd, Text: pattern[prev:contentEnd]})
	return g
}

// branchHasContent 报告一个分支是否「有内容」。
//
// 判据：分支文本里至少有一个字母或数字（字符类里含字母数字也算）。
// 边界型的交替（/ 与 $）因此被排除——它们是同一条前缀的两种边界写法，
// 不是独立的一支，给它们各要一条样例等于要求作者为同一件事写两遍。
func branchHasContent(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	if i := strings.IndexByte(text, '['); i >= 0 {
		if j := strings.IndexByte(text[i:], ']'); j > 0 {
			for _, r := range text[i+1 : i+j] {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return true
				}
			}
		}
	}
	return false
}

// branchCoverProblems 判一条规则的样例是否覆盖了正则的**每一个**分支。
//
// 返回人话问题清单（空 = 覆盖完整）。口径属 require_rule_examples：
// 只有开了那条口径的 policy 才要求分支覆盖——强制补样例会让宿主的一次
// 「升级判据」变成一次「策略改写」。
func branchCoverProblems(r Rule) []string {
	pattern := r.ForbidRegex
	if r.ForbidImport != "" {
		pattern = r.ForbidImport
	}
	if pattern == "" {
		return nil // 没有执行体的规则由 selfcheck 的另一条口径报
	}
	var hits []string
	for _, ex := range r.Examples {
		if ex.Want == exampleWantHit {
			hits = append(hits, ex.Text)
		}
	}
	if len(hits) == 0 {
		return nil // 一条 hit 样例都没有，由 exampleCoverageProblems 报
	}
	var out []string
	for _, alt := range splitAlternations(pattern) {
		for _, br := range alt.Branches {
			if !branchHasContent(br.Text) {
				continue
			}
			covered := false
			for _, h := range hits {
				ok, err := r.branchHits(pattern, alt, br, h)
				if err != nil {
					out = append(out, err.Error())
					break
				}
				if ok {
					covered = true
					break
				}
			}
			if !covered {
				out = append(out, fmt.Sprintf(
					"规则 %q 的正则分支 %q **没有任何 want: %s 的样例覆盖它**："+
						"删掉这一支（改路径前缀 / 删词 / 改回调名）后门禁不会变红——"+
						"样例照样全绿，而这一支从此不再被任何样例钉住",
					r.ID, br.Text, exampleWantHit))
			}
		}
	}
	return out
}

// branchHits 判一条 hit 样例是否**经由该分支**命中整条正则。
//
// 判法：把整条正则改写成「这一支套一层**捕获组**」（其余上下文原样保留），
// 然后要求样例命中它、**且**那个捕获组真的参与了匹配。
//
// 为什么必须是捕获组的「参与」而不是「命中了改写版」：
//
//   - 只看「命中改写版」不够——改写版里**其余分支还在**，样例完全可能走的是
//     另一支。上一版就栽在这里：域词表里的一支被另一支的样例顶替，
//     覆盖判据整体失效（反向验证当场抓到）。
//   - 也不能用「其余分支不命中」来代替：两条分支**允许重叠**
//     （本仓的裸 goroutine 规则里，go func() 同时命中「func(」与
//     「标识符(」两支），于是「其余不命中」永远不成立。
//
// 捕获组参与 ⇒ 这一支是样例**真正走的那一支**，与分支是否重叠无关。
//
// 组号要算准：包装组是「这一支」在改写版里的第一个捕获组，故组号 =
// 该位置之前的捕获组个数 + 1（分支文本内部自带的捕获组排在它后面）。
func (r Rule) branchHits(pattern string, alt altGroup, br altBranch, sample string) (bool, error) {
	mod := pattern[:alt.Start] + "(" + br.Text + ")" + pattern[alt.End:]
	re, err := regexp.Compile(mod)
	if err != nil {
		return false, fmt.Errorf("规则 %q 的分支 %q 改写成「只留这一支」后正则非法（%v）："+
			"样例覆盖判据给不出结论", r.ID, br.Text, err)
	}
	group := countCaptures(pattern[:alt.Start]) + 1
	if r.ForbidImport != "" {
		// import 规则的样例面是 import 路径（与真判定同构，见 imports.go）。
		src := "package sample\n\nimport _ " + strconv.Quote(sample) + "\n"
		vs, verr := importViolationsIn(r, re, sampleFileName(r), []byte(src))
		if verr != nil {
			return false, verr
		}
		if len(vs) == 0 {
			return false, nil
		}
		return captureParticipates(re, group, sample), nil
	}
	// 文本规则：命中判定走**真实判定核**（含注释豁免与 negative_follow 中和），
	// 捕获组参与判定只做收紧。
	vs := regexViolationsIn(r, re, sampleFileName(r), sample)
	if len(vs) == 0 {
		return false, nil
	}
	lines := strings.Split(sample, "\n")
	for _, v := range vs {
		if v.Line-1 < 0 || v.Line-1 >= len(lines) {
			continue
		}
		if captureParticipates(re, group, lines[v.Line-1]) {
			return true, nil
		}
	}
	return false, nil
}

// captureParticipates 报告第 group 号捕获组是否参与了匹配。
//
// 未参与的捕获组在 FindStringSubmatchIndex 里的下标是 -1（区别于「参与了
// 但匹配了空串」——那种情形下标是有效位置，只是起止相同）。
func captureParticipates(re *regexp.Regexp, group int, text string) bool {
	m := re.FindStringSubmatchIndex(text)
	if m == nil {
		return false
	}
	i := 2 * group
	if i+1 >= len(m) {
		return false
	}
	return m[i] >= 0
}

// countCaptures 数一段正则文本里**捕获组**的个数。
//
// (?: 与 (?i) 之类不是捕获组；(?P<name>...) 是捕获组。
func countCaptures(text string) int {
	n := 0
	escaped := false
	inClass := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '[':
			if !inClass {
				inClass = true
			}
		case ']':
			if inClass {
				inClass = false
			}
		case '(':
			if inClass {
				break
			}
			if i+1 < len(text) && text[i+1] == '?' {
				// (?P<name> 是捕获组，其余 (? 开头的是非捕获组或标志组。
				//
				// 下标必须算准：'(' 在 i，故 '?' 是 i+1、'P' 是 i+2、'<' 是 i+3。
				// 算错一格 ⇒ 命名捕获组被漏数 ⇒ 组号整体偏移 ⇒ 覆盖判据
				// 去看错的那个组（本包 branchHits 的组号由此算出）。
				if i+3 < len(text) && text[i+2] == 'P' && text[i+3] == '<' {
					n++
				}
				break
			}
			n++
		}
	}
	return n
}

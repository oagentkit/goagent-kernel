// checks.go —— 台账的四条判据。
//
// 每条判据只做一件事，且**失败形态是变红**：有违反就是非零退出 + 指到
// 文件与行的文案。这里没有「警告」档——判据要么成立要么不成立，
// 把它降级成提示等于把它删掉。
package main

import (
	"fmt"
	"regexp"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
)

// closingRefRe 合法的关闭 PR 号形态：#N。
var closingRefRe = regexp.MustCompile(`^#\d+$`)

// checkLedger 全部判据的聚合（只聚合不截断：报错成本在「人要去改」）。
func checkLedger(led *ledgerstatus.Ledger) []string {
	var probs []string
	probs = append(probs, checkStatusConsistency(led)...)
	probs = append(probs, checkClosingPRPresent(led)...)
	probs = append(probs, checkClosingPRShape(led)...)
	probs = append(probs, checkRegistered(led)...)
	probs = append(probs, checkShape(led)...)
	return probs
}

// checkStatusConsistency 判据 1：同一单号在台账多处出现时，各处状态必须一致。
//
// 为什么这条最贵：状态矛盾时，读台账的人拿到的是两个相反的结论，
// 而「单还挂着 open 标签 ≠ 还没人做」这个坑就是这么踩的。
func checkStatusConsistency(led *ledgerstatus.Ledger) []string {
	var probs []string
	for _, e := range led.Entries() {
		var closed, open *ledgerstatus.Occurrence
		for i := range e.Occurrences {
			occ := &e.Occurrences[i]
			switch occ.Status {
			case ledgerstatus.StatusClosed:
				if closed == nil {
					closed = occ
				}
			case ledgerstatus.StatusOpen:
				if open == nil {
					open = occ
				}
			}
		}
		if closed == nil || open == nil {
			continue
		}
		probs = append(probs, fmt.Sprintf(
			"%s:%d 与 %s:%d 对单号 #%d 的状态互相矛盾（一处「已合入」，一处「仍开着」）——"+
				"同一件事两个结论，读的人无从取舍",
			closed.File, closed.Line, open.File, open.Line, e.Number))
	}
	return probs
}

// checkClosingPRPresent 判据 2：「已合入」必须带关闭 PR 号。
//
// 只判**登记表里的行**：散文里的「已合入」是叙述，不承担「这一条已清偿」
// 的声明责任（真正的声明在登记表的状态列上）。
func checkClosingPRPresent(led *ledgerstatus.Ledger) []string {
	var probs []string
	for _, e := range led.Entries() {
		for _, occ := range e.Occurrences {
			if occ.Kind != ledgerstatus.OccurrenceRegistry {
				continue
			}
			if occ.Status != ledgerstatus.StatusClosed {
				continue
			}
			if occ.ClosingRef != "" {
				continue
			}
			probs = append(probs, fmt.Sprintf(
				"%s:%d: 单号 #%d 标了「已合入」却没给关闭 PR 号——「已清偿」无从核验（写成「已合入 (#N)」）",
				occ.File, occ.Line, occ.Number))
		}
	}
	return probs
}

// checkClosingPRShape 判据 4：关闭 PR 号本身必须形如 #N。
func checkClosingPRShape(led *ledgerstatus.Ledger) []string {
	var probs []string
	for _, e := range led.Entries() {
		for _, occ := range e.Occurrences {
			if occ.ClosingRef == "" || closingRefRe.MatchString(occ.ClosingRef) {
				continue
			}
			probs = append(probs, fmt.Sprintf(
				"%s:%d: 单号 #%d 的关闭 PR 号写 %q，必须形如 #N",
				occ.File, occ.Line, occ.Number, occ.ClosingRef))
		}
	}
	return probs
}

// checkRegistered 判据 3：正文里被引用的单号必须在登记面登记。
//
// 「登记面」= 台账里登记单号的那些列（单号 / 单 / 已落单 / 结论单）。
// 单号在正文里被提到却没进任何一张登记表 ⇒ 没人认领它 ⇒ 无主。
func checkRegistered(led *ledgerstatus.Ledger) []string {
	var probs []string
	for _, e := range led.Entries() {
		if e.Registered {
			continue
		}
		occ := e.Occurrences[0]
		probs = append(probs, fmt.Sprintf(
			"%s:%d: 单号 #%d 在正文里被引用，却没在台账任何一张登记表里登记——单号漂浮、无主（要么登记，要么别提）",
			occ.File, occ.Line, e.Number))
	}
	return probs
}

// checkShape 结构判据：表格形状不对要报出来。
//
// 「解析不出来却不说话」= 判据静默失效：台账是散文，表格随时被手改歪，
// 而歪掉的那一列里的单号就此没人管了。
func checkShape(led *ledgerstatus.Ledger) []string {
	var probs []string
	for _, p := range led.Problems() {
		probs = append(probs, fmt.Sprintf("%s:%d: 台账结构有问题（%s）：%s",
			p.File, p.Line, p.Kind, p.Message))
	}
	return probs
}

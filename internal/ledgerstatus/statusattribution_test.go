// statusattribution_test.go —— 状态词的**归属**判据（issue #411 的误判）。
//
// # 这组用例证明什么
//
// 旧实现对**整行**算一个状态，行里所有单号无条件继承——于是
//
//	——#411 就是这条的实例。**本轮动了一处**（#411 的用例换成……），**仍开着**——……
//
// 这行里「仍开着」修饰的是**那条建议**的开放状态，却把 #411 派生成待清偿；
// 而它其实早在 2026-11-17 就关了 ⇒ 判据 5 拿它去对账 GitHub，判出「关了没登记」。
//
// # 反向验证必须成对
//
// 只测「多单号不猜」，一个**一律不猜**的错实现照样全过；只测「单单号照猜」，
// 一个「一律不猜」的实现会红、但一个「多单号也猜」的错实现仍过。两个方向都要有。
//
// 另钉住**权威面不受影响**：登记表状态列给的是整行状态，与单号个数无关——
// 若它被这条约束误伤，判据 5 会大面积失去对账依据（那比误判更贵）。
package ledgerstatus

import (
	"strings"
	"testing"
)

// attributionLedger 造一份含登记表的最小台账。
func attributionLedger(row string) string {
	return strings.Join([]string{
		"# 合成台账",
		"",
		"## 9. issue 台账",
		"",
		"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
		"|---|---|---|---|---|",
		row,
	}, "\n")
}

// proseStatus 取**正文那处出现点**的状态（这才是「归属」判据要判的东西）。
//
// 不用合并状态 e.Status：登记表状态列**空着本身就是 open 声明**（parse 的设计，
// 见 statusFromCell），它会把正文归属的结论盖掉——那测的就不是归属了。
func proseStatus(t *testing.T, body string, n int) Status {
	t.Helper()
	led := ParseText(body, "ledger.md")
	e, ok := led.Entry(n)
	if !ok {
		t.Fatalf("单号 #%d 不在台账里", n)
	}
	for _, occ := range e.Occurrences {
		if occ.Kind == OccurrenceReference && strings.Contains(occ.Text, "正文") {
			return occ.Status
		}
	}
	t.Fatalf("#%d 找不到正文出现点", n)
	return StatusUnknown
}

// parseOne 解析台账并取某个单号的**合并**状态（状态列那组用例用它）。
func parseOne(t *testing.T, body string, n int) Status {
	t.Helper()
	led := ParseText(body, "ledger.md")
	e, ok := led.Entry(n)
	if !ok {
		t.Fatalf("单号 #%d 不在台账里", n)
	}
	return e.Status
}

// TestSameNumberTwiceDoesNotInheritStatus 同一单号在同一行出现两次 + 状态词 ⇒
// **不归属**（这是 #411 的原形：前半句讲它是什么、后半句讲「这条建议」的状态）。
func TestSameNumberTwiceDoesNotInheritStatus(t *testing.T) {
	body := attributionLedger("| #901 | 合成单 | S1 | 合成判据 |  |") +
		"\n正文：#901 就是这条的实例。本轮动了一处（#901 的用例换成新写法），仍开着——全仓还没审过。\n"
	if got := proseStatus(t, body, 901); got != StatusUnknown {
		t.Fatalf("同行两次出现时状态词修饰谁不确定，应派生 unknown，实得 %s", got)
	}
}

// TestMultipleNumbersDoNotInheritStatus 一行多个**不同**单号 + 状态词 ⇒ 不归属。
func TestMultipleNumbersDoNotInheritStatus(t *testing.T) {
	body := attributionLedger("| #901 | 合成甲 | S1 | 合成判据 |  |") +
		"\n正文：#901 / #902 / #903 三条都已合入。\n"
	if got := proseStatus(t, body, 901); got != StatusUnknown {
		t.Fatalf("多单号行的状态词归属不确定，应派生 unknown，实得 %s", got)
	}
	if got := proseStatus(t, body, 902); got != StatusUnknown {
		t.Fatalf("多单号行的第二个单号同样不应继承，实得 %s", got)
	}
}

// TestSingleNumberStillInheritsStatus **单**单号 + 状态词 ⇒ 仍归属。
//
// 少了这条，一个「一律不猜」的实现也能让上面两条通过——而那会让正文里
// 「#901 已合入」这类**明确**的声明全部失效，判据 1 随之漏报矛盾。
func TestSingleNumberStillInheritsStatus(t *testing.T) {
	body := attributionLedger("| #901 | 合成单 | S1 | 合成判据 |  |") +
		"\n正文：#901 已合入，关闭 PR 见登记表。\n"
	if got := proseStatus(t, body, 901); got != StatusClosed {
		t.Fatalf("单单号行的明确声明必须归属（实得 %s）——否则判据 1 漏报", got)
	}
	bodyOpen := attributionLedger("| #901 | 合成单 | S1 | 合成判据 |  |") +
		"\n正文：#901 仍开着，等下一轮。\n"
	if got := proseStatus(t, bodyOpen, 901); got != StatusOpen {
		t.Fatalf("单单号行的「仍开着」必须归属，实得 %s", got)
	}
}

// TestStatusColumnStillRulesWhenRowHasMultipleNumbers **登记表状态列**不受这条
// 约束影响：状态列给的是**整行**状态，与该行单号个数无关。
//
// 这条钉的是权威面：若状态列被「多单号不猜」误伤，判据 5 会失去几乎全部
// 对账依据（登记表一行常常提到多个单号）——那比原来的误判更贵。
func TestStatusColumnStillRulesWhenRowHasMultipleNumbers(t *testing.T) {
	body := attributionLedger("| #901 / #902 | 合成双号行 | S1 | 合成判据 | ✅ 已合入 (#999) |")
	for _, n := range []int{901, 902} {
		if got := parseOne(t, body, n); got != StatusClosed {
			t.Fatalf("状态列的声明必须生效（单号个数无关），#%d 实得 %s", n, got)
		}
	}
	bodyOpen := attributionLedger("| #901 / #902 | 合成双号行 | S1 | 合成判据 |  |")
	for _, n := range []int{901, 902} {
		if got := parseOne(t, bodyOpen, n); got != StatusOpen {
			t.Fatalf("状态列空 = 明写「仍开着」，#%d 实得 %s", n, got)
		}
	}
}

// TestStatusColumnStillBeatsProseConflict 状态列与**单单号**正文声明矛盾时，
// 两处状态都要在（判据 1 才报得出矛盾）——本修复只收紧「多单号行」的归属。
func TestStatusColumnStillBeatsProseConflict(t *testing.T) {
	body := attributionLedger("| #901 | 合成单 | S1 | 合成判据 | ✅ 已合入 (#999) |") +
		"\n正文：#901 仍开着。\n"
	led := ParseText(body, "ledger.md")
	e, ok := led.Entry(901)
	if !ok {
		t.Fatal("#901 不在台账里")
	}
	var sawClosed, sawOpen bool
	for _, occ := range e.Occurrences {
		switch occ.Status {
		case StatusClosed:
			sawClosed = true
		case StatusOpen:
			sawOpen = true
		}
	}
	if !sawClosed || !sawOpen {
		t.Fatalf("两处状态都要在（判据 1 才报得出矛盾），实得 closed=%v open=%v", sawClosed, sawOpen)
	}
}

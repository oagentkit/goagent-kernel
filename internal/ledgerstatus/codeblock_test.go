// codeblock_test.go —— 围栏代码块内的行不进单号视图。
//
// 代码块是**示例与引用**（合成数据、被改掉的原文、命令、输出），不是台账的叙述。
// 把它们当声明会凭空造出状态归属：§36 / §37 为了说明「状态标记词的歧义」逐字
// 引用了那句原文，判据当场把 #411 派生成待清偿——而它 2026-11-17 就关了。
//
// 这个坑连踩三次，每次都是「改措辞绕开」——靠作者记得改措辞防不住，故由机制挡。
package ledgerstatus

import (
	"strings"
	"testing"
)

// TestCodeBlockLinesDoNotProduceOccurrences 代码块里的单号与状态词**都不产生**
// 出现点（既不登记、也不派生状态）。
func TestCodeBlockLinesDoNotProduceOccurrences(t *testing.T) {
	body := strings.Join([]string{
		"# 合成台账",
		"",
		"## 9. issue 台账",
		"",
		"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
		"|---|---|---|---|---|",
		"| #901 | 合成单 | S1 | 合成判据 |  |",
		"",
		"正文里 #901 没有状态声明。",
		"",
		"```",
		"——#901 就是这条的实例。本轮动了一处（#901 的用例换成新写法），仍开着——……",
		"```",
	}, "\n")
	led := ParseText(body, "ledger.md")
	e, ok := led.Entry(901)
	if !ok {
		t.Fatal("#901 应在登记表里出现（那行不在代码块内）")
	}
	// 代码块那行不该产生出现点：出现点数应等于「登记表 1 处 + 正文 1 处」。
	if len(e.Occurrences) != 2 {
		t.Fatalf("代码块内的行不得产生出现点，实得 %d 处：%v", len(e.Occurrences), e.Occurrences)
	}
	if e.Status != StatusOpen { // 登记表状态列空 = 明写仍开着
		t.Fatalf("状态只应来自登记表状态列（open），实得 %s", e.Status)
	}
	for _, occ := range e.Occurrences {
		if occ.Line == 12 { // 代码块内那一行
			t.Fatalf("第 12 行在代码块内，不得产生出现点，实得 %+v", occ)
		}
	}
}

// TestProseOutsideCodeBlockStillCounts 代码块**外**的同款文本照常产生出现点——
// 少了这条，一个「整文件跳过」的错实现也能让上面那条通过。
func TestProseOutsideCodeBlockStillCounts(t *testing.T) {
	body := strings.Join([]string{
		"# 合成台账",
		"",
		"## 9. issue 台账",
		"",
		"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
		"|---|---|---|---|---|",
		"| #901 | 合成单 | S1 | 合成判据 |  |",
		"",
		"正文里 #901 已合入。",
	}, "\n")
	led := ParseText(body, "ledger.md")
	e, ok := led.Entry(901)
	if !ok {
		t.Fatal("#901 不在台账里")
	}
	var sawClosed bool
	for _, occ := range e.Occurrences {
		if occ.Status == StatusClosed {
			sawClosed = true
		}
	}
	if !sawClosed {
		t.Fatalf("代码块外的「已合入」必须仍被识别，实得 occurrences=%+v", e.Occurrences)
	}
}

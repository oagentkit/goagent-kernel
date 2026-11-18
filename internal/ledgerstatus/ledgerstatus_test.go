// ledgerstatus_test.go —— 台账派生的可执行判据。
//
// 夹具是**合成台账**：单号、标题、阶段全是编的，不写进任何真实领域词或
// 真实仓库状态——夹具是契约的示例，示例带真实味就等于把契约钉死在某一次
// 仓库状态上。
package ledgerstatus

import (
	"strings"
	"testing"
)

// syntheticLedger 合成台账：三种状态 + 一处漂浮引用 + 一处散文标记。
//
// 用行切片拼而不是反引号原串：原串里的反引号在某些生成链路里会被当成
// 标记符，夹具被截断时排查成本远高于这一行的收益。
var syntheticLedger = strings.Join([]string{
	"# 合成台账",
	"",
	"## 9. issue 台账",
	"",
	"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
	"|---|---|---|---|---|",
	"| #101 | 合成缺陷甲 | S1 | 合成判据甲 | ✅ 已合入 (#201) |",
	"| #102 | 合成缺陷乙 | S1 | 合成判据乙 |  |",
	"| #103 | 合成缺陷丙 | S2 | 合成判据丙 | ✅ 已合入 (#203) |",
	"",
	"## 10. 合成第二轮台账",
	"",
	"| 面 | 已落单 | 判定 | 一句话形态 |",
	"|---|---|---|---|",
	"| 合成面 | #104 / #105 | 合成判定 | 合成形态 |",
	"",
	"> 合成备注：#101 已合入。",
	"> 合成备注：#102 仍开着。",
	"> 合成备注：#106 还没进登记面。",
	"> 落文档 PR #301。",
	"> 合并 PR 分布在 #302 / #303。",
}, "\n")

// TestStatusDerivedFromLedger 已合入 ⇒ closed 且带关闭 PR；空状态 ⇒ open。
func TestStatusDerivedFromLedger(t *testing.T) {
	l := ParseText(syntheticLedger, "synthetic.md")

	if got := l.Status(101); got != StatusClosed {
		t.Fatalf("#101 状态 = %q, want %q", got, StatusClosed)
	}
	pr, ok := l.ClosingPR(101)
	if !ok || pr != 201 {
		t.Fatalf("#101 关闭 PR = %d/%v, want 201/true", pr, ok)
	}
	if got := l.Status(102); got != StatusOpen {
		t.Fatalf("#102 状态 = %q, want %q", got, StatusOpen)
	}
	if _, ok := l.ClosingPR(102); ok {
		t.Fatal("#102 仍开着，不该有关闭 PR")
	}
	if got := l.Status(103); got != StatusClosed {
		t.Fatalf("#103 状态 = %q, want %q", got, StatusClosed)
	}
}

// TestEmptyStatusCellMeansOpen 状态列**空着**也是一声声明：「还没合入」的明写。
//
// 为什么要有这条：登记表那一列就是「这一行写没写清」的登记表——空着是
// 「还没合入」的**明写**，不是「没说话」。判据 1（同一单号各处状态必须一致）
// 要靠这个才能把「表里说开着」与「别处说开着」对上；少了它，登记表里没填
// 状态的那一行会被当成「这张表不谈状态」，于是同一条单在两处得出两个结论。
func TestEmptyStatusCellMeansOpen(t *testing.T) {
	txt := strings.Join([]string{
		"| 单号 | 标题 | 状态 |",
		"|---|---|---|",
		"| #401 | 合成未合入 |  |",
	}, "\n")
	l := ParseText(txt, "synthetic.md")

	if got := l.Status(401); got != StatusOpen {
		t.Fatalf("#401 状态 = %q, want %q（状态列空着也是一声声明）", got, StatusOpen)
	}
}

// TestUnregisteredReferenceIsNotRegistered 散文里提到但没进登记面的单号
// 不得被当成「已登记」——它就是漂浮的那一类。
func TestUnregisteredReferenceIsNotRegistered(t *testing.T) {
	l := ParseText(syntheticLedger, "synthetic.md")

	if !l.Registered(101) {
		t.Fatal("#101 在登记面里，不该判成未登记")
	}
	if l.Registered(106) {
		t.Fatal("#106 只在散文里出现，不该判成已登记")
	}
	entry, ok := l.Entry(106)
	if !ok {
		t.Fatal("#106 应有出现点记录")
	}
	if len(entry.Occurrences) != 1 || entry.Occurrences[0].Kind != OccurrenceReference {
		t.Fatalf("#106 出现点 = %+v, want 恰好一个 reference", entry.Occurrences)
	}
	if entry.Occurrences[0].Line == 0 {
		t.Fatal("出现点必须带行号（指到文件与行）")
	}
}

// TestSecondRegistryColumnRegisters 第二种登记列（已落单）同样算数。
func TestSecondRegistryColumnRegisters(t *testing.T) {
	l := ParseText(syntheticLedger, "synthetic.md")

	for _, n := range []int{104, 105} {
		if !l.Registered(n) {
			t.Fatalf("#%d 应在「已落单」列里登记", n)
		}
	}
}

// TestPRReferencesAreNotIssues PR 号不是单号：PR 形态的 #N 不进单号视图。
func TestPRReferencesAreNotIssues(t *testing.T) {
	l := ParseText(syntheticLedger, "synthetic.md")

	for _, n := range []int{301, 302, 303} {
		if _, ok := l.Entry(n); ok {
			t.Fatalf("#%d 是 PR 号，不该出现在单号视图里", n)
		}
	}
}

// TestConflictingStatusCollapsesToUnknown 同一个单号两处状态矛盾时，
// 派生层**不选边**（选边就是替台账做决定）；矛盾由门禁报出来。
func TestConflictingStatusCollapsesToUnknown(t *testing.T) {
	txt := strings.Replace(syntheticLedger,
		"> 合成备注：#102 仍开着。",
		"> 合成备注：#102 已合入。", 1)
	l := ParseText(txt, "synthetic.md")

	if got := l.Status(102); got != StatusUnknown {
		t.Fatalf("矛盾时状态 = %q, want %q（不选边）", got, StatusUnknown)
	}
	entry, _ := l.Entry(102)
	var sawOpen, sawClosed bool
	for _, occ := range entry.Occurrences {
		switch occ.Status {
		case StatusOpen:
			sawOpen = true
		case StatusClosed:
			sawClosed = true
		}
	}
	if !sawOpen || !sawClosed {
		t.Fatalf("两处状态声明都要如实保留，got %+v", entry.Occurrences)
	}
}

// TestMissingClosingPRKeepsRawRef 「已合入」没带关闭 PR 时，原文为空——
// 门禁据此报「缺关闭 PR 号」。
func TestMissingClosingPRKeepsRawRef(t *testing.T) {
	txt := strings.Replace(syntheticLedger, "✅ 已合入 (#201)", "✅ 已合入", 1)
	l := ParseText(txt, "synthetic.md")

	entry, ok := l.Entry(101)
	if !ok {
		t.Fatal("#101 应有出现点记录")
	}
	if entry.Occurrences[0].Status != StatusClosed {
		t.Fatalf("#101 状态声明 = %q, want %q", entry.Occurrences[0].Status, StatusClosed)
	}
	if entry.Occurrences[0].ClosingRef != "" {
		t.Fatalf("关闭 PR 原文 = %q, want 空（门禁要据此报缺）", entry.Occurrences[0].ClosingRef)
	}
}

// TestMalformedClosingPRKeepsRawText 关闭 PR 号写成非 #N 形态时，
// 原文必须原样留着——解析期丢掉，门禁就永远看不到它。
func TestMalformedClosingPRKeepsRawText(t *testing.T) {
	txt := strings.Replace(syntheticLedger, "✅ 已合入 (#201)", "✅ 已合入 (PR-201)", 1)
	l := ParseText(txt, "synthetic.md")

	entry, _ := l.Entry(101)
	if got := entry.Occurrences[0].ClosingRef; got != "PR-201" {
		t.Fatalf("关闭 PR 原文 = %q, want %q", got, "PR-201")
	}
	if _, ok := l.ClosingPR(101); ok {
		t.Fatal("非 #N 形态的关闭 PR 不该被解析成号")
	}
}

// TestTableShapeProblemReported 表格列数不齐要报出可读的问题（指到行）。
func TestTableShapeProblemReported(t *testing.T) {
	txt := strings.Replace(syntheticLedger,
		"| #102 | 合成缺陷乙 | S1 | 合成判据乙 |  |",
		"| #102 | 合成缺陷乙 | S1 |", 1)
	l := ParseText(txt, "synthetic.md")

	if len(l.Problems()) == 0 {
		t.Fatal("列数不齐必须报出问题，不能静默吞掉")
	}
	p := l.Problems()[0]
	if p.Kind != ProblemTableShape {
		t.Fatalf("问题类别 = %q, want %q", p.Kind, ProblemTableShape)
	}
	if p.File != "synthetic.md" || p.Line == 0 {
		t.Fatalf("问题必须指到文件与行，got %q:%d", p.File, p.Line)
	}
	// 报出问题不影响其它行的解析。
	if got := l.Status(101); got != StatusClosed {
		t.Fatalf("#101 状态 = %q, want %q", got, StatusClosed)
	}
}

// TestProseMarkerAppliesToSameLine 散文里的状态标记只作用于同一行的单号。
func TestProseMarkerAppliesToSameLine(t *testing.T) {
	txt := syntheticLedger + "\n> 合成备注：#201 已合入。\n"
	l := ParseText(txt, "synthetic.md")

	entry, ok := l.Entry(201)
	if !ok {
		t.Fatal("#201 应有出现点记录")
	}
	if entry.Occurrences[0].Status != StatusClosed {
		t.Fatalf("#201 状态声明 = %q, want %q", entry.Occurrences[0].Status, StatusClosed)
	}
}

// TestAmbiguousMarkersYieldNoClaim 一行里两种标记都在时判不出状态，
// 不猜——猜出来的声明会被门禁当成真的状态去判矛盾。
func TestAmbiguousMarkersYieldNoClaim(t *testing.T) {
	txt := syntheticLedger + "\n> 合成备注：#107 已合入，仍开着。\n"
	l := ParseText(txt, "synthetic.md")

	entry, ok := l.Entry(107)
	if !ok {
		t.Fatal("#107 应有出现点记录")
	}
	if entry.Occurrences[0].Status != StatusUnknown {
		t.Fatalf("#107 状态声明 = %q, want %q（两种标记并存时判不出）",
			entry.Occurrences[0].Status, StatusUnknown)
	}
}

// TestEntriesSorted 全部单号按号升序返回（输出可复现）。
func TestEntriesSorted(t *testing.T) {
	l := ParseText(syntheticLedger, "synthetic.md")

	entries := l.Entries()
	if len(entries) == 0 {
		t.Fatal("应有单号视图")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Number >= entries[i].Number {
			t.Fatalf("单号未升序：%d 在 %d 之前", entries[i-1].Number, entries[i].Number)
		}
	}
}

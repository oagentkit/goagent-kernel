// parse_test.go —— 登记面 md 解析的可执行判据（合成文档，不读盘）。
package invariantledger

import (
	"strings"
	"testing"
)

// fullDoc 合成一份结构完整的登记面（同时是 docs/INVARIANTS.md 的格式样板）。
func fullDoc() string {
	var b strings.Builder
	b.WriteString("# 执行不变量与插点矩阵\n\n")
	b.WriteString("叙事段落。\n\n")
	b.WriteString("updated: 2026-11-27\n\n")
	b.WriteString("## 不变量登记表\n\n")
	b.WriteString("| id | 陈述 | 判定机制 | 成文处 | 必红判据 | 缺口 |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, id := range RequiredInvariants {
		tid := "pkg.Test_" + strings.ReplaceAll(id, "-", "_")
		b.WriteString("| " + id + " | 陈述-" + id + " | 机制-" + id + " | docs/x.md | `" +
			tid + "` | 无 |\n")
	}
	b.WriteString("\n## 插点矩阵\n\n")
	b.WriteString("| 间隙 | F1 崩溃 | F2 双副本 | F3 响应丢失 | F4 租约过期+旧恢复 | F5 事件丢失 | F6 双终态 | 豁免/备注 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	labels := map[string]string{
		"G1": "创建→领取", "G2": "领取→执行", "G3": "执行→副作用",
		"G4": "副作用→事件", "G5": "事件→checkpoint", "G6": "checkpoint→终态",
		"G7": "终态→终态事件",
	}
	for _, id := range RequiredGaps {
		b.WriteString("| " + id + " " + labels[id] + " | `pkg.Test_" +
			strings.ReplaceAll(id, "-", "_") + "` | — | — | — | — | — | — |\n")
	}
	return b.String()
}

// TestParseDocFull 全量文档：两张表都解析对，指针抽取带反引号也认。
func TestParseDocFull(t *testing.T) {
	reg, err := ParseDoc(fullDoc())
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if reg.Updated != "2026-11-27" {
		t.Fatalf("版本戳应为 2026-11-27，实得 %q", reg.Updated)
	}
	if len(reg.Invariants) != 7 {
		t.Fatalf("不变量应 7 行，实得 %d", len(reg.Invariants))
	}
	first := reg.Invariants[0]
	if first.ID != "state-transition" {
		t.Fatalf("首行 id 应为 state-transition，实得 %q", first.ID)
	}
	if len(first.Judgments) != 1 || first.Judgments[0] != "pkg.Test_state_transition" {
		t.Fatalf("指针抽取应去掉反引号，实得 %v", first.Judgments)
	}
	if first.Gap != "无" {
		t.Fatalf("缺口格应为「无」，实得 %q", first.Gap)
	}
	if len(reg.Gaps) != 7 {
		t.Fatalf("间隙应 7 行，实得 %d", len(reg.Gaps))
	}
	if reg.Gaps[0].ID != "G1" {
		t.Fatalf("间隙 id 应取首词 G1（标签供人读），实得 %q", reg.Gaps[0].ID)
	}
	if len(reg.Gaps[0].Evidence) != 1 {
		t.Fatalf("G1 应抽到 1 条场景，实得 %v", reg.Gaps[0].Evidence)
	}
	if reg.Gaps[0].Waiver != "—" {
		t.Fatalf("豁免列应为 —，实得 %q", reg.Gaps[0].Waiver)
	}
}

// TestParseDocAcceptanceRoundTrip 解析结果过得了 Judge（格式样板自洽）。
func TestParseDocAcceptanceRoundTrip(t *testing.T) {
	reg, err := ParseDoc(fullDoc())
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	idx := TestIndex{}
	for _, inv := range reg.Invariants {
		for _, ref := range inv.Judgments {
			idx[ref] = true
		}
	}
	for _, g := range reg.Gaps {
		for _, ref := range g.Evidence {
			idx[ref] = true
		}
	}
	if res := Judge(reg, idx); len(res.Problems) != 0 {
		t.Fatalf("格式样板应判全绿，实得：%v", res.Problems)
	}
}

// TestParseDocMissingUpdated 没有版本戳行 ⇒ 解析错误（给不出结论，不是判红）。
func TestParseDocMissingUpdated(t *testing.T) {
	doc := strings.Replace(fullDoc(), "updated: 2026-11-27\n\n", "", 1)
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "版本戳") {
		t.Fatalf("缺版本戳应报错并说明，实得：%v", err)
	}
}

// TestParseDocMissingTable 缺不变量登记表 ⇒ 解析错误。
func TestParseDocMissingTable(t *testing.T) {
	doc := fullDoc()
	doc = doc[:strings.Index(doc, "## 不变量登记表")]
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "不变量登记表") {
		t.Fatalf("缺表应报错并点名，实得：%v", err)
	}
}

// TestParseDocMissingColumn 表缺「缺口」列 ⇒ 解析错误（缺列的表读出来的行是歪的）。
func TestParseDocMissingColumn(t *testing.T) {
	doc := strings.Replace(fullDoc(), "| 缺口 |", "| 备注 |", 1)
	doc = strings.Replace(doc, "| 无 |", "| — |", 7)
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "缺口") {
		t.Fatalf("缺列应报错并点名列名，实得：%v", err)
	}
}

// TestParseDocMissingWaiverColumn 插点矩阵缺豁免列 ⇒ 解析错误（零场景的行没有豁免落点）。
func TestParseDocMissingWaiverColumn(t *testing.T) {
	doc := strings.Replace(fullDoc(), "| 豁免/备注 |", "| 备注 |", 1)
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "豁免") {
		t.Fatalf("缺豁免列应报错，实得：%v", err)
	}
}

// TestParseDocShortRow 行比表头短（手改表格漏格）⇒ 不 panic，缺格按空处理。
func TestParseDocShortRow(t *testing.T) {
	doc := strings.Replace(fullDoc(),
		"| state-transition | 陈述-state-transition | 机制-state-transition | docs/x.md | `pkg.Test_state_transition` | 无 |",
		"| state-transition | 陈述 | 机制 | docs/x.md |", 1)
	reg, err := ParseDoc(doc)
	if err != nil {
		t.Fatalf("短行不该是解析错误（判据层去判缺格），实得：%v", err)
	}
	// 判据层会把空缺口格判红——这里只钉「不 panic、缺口为空」。
	if reg.Invariants[0].Gap != "" {
		t.Fatalf("短行的缺口格应为空，实得 %q", reg.Invariants[0].Gap)
	}
	res := Judge(reg, TestIndex{})
	if !hasProblem(res.Problems, "缺口格为空") {
		t.Fatalf("短行的空缺口格应由 Judge 报，实得：%v", res.Problems)
	}
}

// TestExtractRefsDedup 同一指针多处出现只算一条（计数口径去重）。
func TestExtractRefsDedup(t *testing.T) {
	refs := extractRefs("`a.TestX` 与 a.TestX 还有 b.TestY")
	if len(refs) != 2 {
		t.Fatalf("应去重为 2 条，实得 %v", refs)
	}
}

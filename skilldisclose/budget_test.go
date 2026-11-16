package skilldisclose

// 预算计费口径（#289）：披露交付的是整个 Skill（含 Tags 与 Resources），
// 计费口径若只算 name+description，「守住预算」与「交付内容」就对不上账——
// 超预算时 Truncated 与 Reason 双双沉默，宿主连告警都发不出。计费与交付
// 必须共用同一份实现（skillBytes）。

import (
	"strings"
	"testing"
)

// deliveredBytes 独立实现的交付字节口径：逐字段求和，**刻意不复用**生产的
// skillBytes——同一个判据写错时两边会一起错，对照就失去意义。
func deliveredBytes(s Skill) int {
	n := len(s.Name) + len(s.Description)
	for _, t := range s.Tags {
		n += len(t)
	}
	for _, r := range s.Resources {
		n += len(r)
	}
	return n
}

// boundarySkill 计费 24 字节、其中 name+description 只有 15 字节的技能：
// 旧口径（只计 name+description）下 20 字节预算照样放行且两处信号全沉默，
// 新口径下必被裁掉。
func boundarySkill() Skill {
	return Skill{
		Name:        "deploy",                 // 6 字节
		Description: "短描述",                   // 9 字节
		Tags:        []string{"a", "b", "c"},  // 3 字节
		Resources:   []string{"r1", "r2", "r3"}, // 6 字节
	}
}

// TestBudgetMetersWhatItDelivers 计入的字节数 == 实际交付 Skill 的字节数。
//
// 反向验证：把 Tags/Resources 从计费口径里删掉（回到 name+description）⇒
// 15 字节 < 20 预算照放 ⇒ Truncated=false、Reason="" ⇒ 本条必红。
func TestBudgetMetersWhatItDelivers(t *testing.T) {
	s := boundarySkill()
	if got, want := skillBytes(s), deliveredBytes(s); got != want {
		t.Fatalf("计入口径 %d != 交付口径 %d（两者必须共用同一份事实）", got, want)
	}
	c := mustCatalog(t, s)
	res, err := c.Disclose(Query{Mention: "deploy"}, Budget{MaxBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatalf("计入 %d 字节 > 预算 20：必须裁掉并标出（Truncated 沉默是 #289 的缺陷本身），实得 %+v", skillBytes(s), res)
	}
	if res.Reason == "" {
		t.Fatal("超预算必须给出原因，Truncated 与 Reason 不得双双沉默")
	}
	if len(res.Skills) != 0 {
		t.Fatalf("唯一候选超出预算必须被裁，实得 %+v", res.Skills)
	}
}

// TestBudgetBoundaryEqualsDelivery 预算恰好等于交付字节：放行且完整交付——
// 放行这一步本身就钉死了「计入口径与交付口径同源」：若计费漏掉 Tags/Resources
// 而交付含它们，这里对边界值 24 的判定就会与交付内容脱钩。
func TestBudgetBoundaryEqualsDelivery(t *testing.T) {
	s := boundarySkill()
	if skillBytes(s) != 24 {
		t.Fatalf("前置不成立：夹具计费应为 24 字节，实得 %d", skillBytes(s))
	}
	c := mustCatalog(t, s)
	res, err := c.Disclose(Query{Mention: "deploy"}, Budget{MaxBytes: 24})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skills) != 1 || res.Truncated {
		t.Fatalf("预算恰好等于交付字节应放行一条，实得 %+v", res)
	}
	// 交付必须完整：Tags 与 Resources 原样进披露面（它们正是计费的对象）。
	got := res.Skills[0]
	if len(got.Tags) != 3 || len(got.Resources) != 3 {
		t.Fatalf("交付的元数据必须含标签与资源，实得 %+v", got)
	}
	if used := deliveredBytes(got); used > 24 {
		t.Fatalf("已披露条目的交付字节 %d 不得超过预算", used)
	}
}

// TestBudgetKeepsDeliveredBytesUnderBudget 多候选各自完整交付，账面与内容一致：
// 逐条断言「交付字节 ≤ 预算」且计费函数对每条已交付 Skill 与独立口径相等。
func TestBudgetKeepsDeliveredBytesUnderBudget(t *testing.T) {
	c := mustCatalog(t, boundarySkill(),
		Skill{Name: "docs", Description: "文档", Tags: []string{"x"}, Resources: []string{"y"}})
	res, err := c.Disclose(Query{Mention: "docs"}, Budget{MaxBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Skills {
		if deliveredBytes(s) > 20 {
			t.Fatalf("披露了 %q（交付 %d 字节）却超预算 20，计费与交付脱钩", s.Name, deliveredBytes(s))
		}
		if skillBytes(s) != deliveredBytes(s) {
			t.Fatalf("技能 %q 的计费 %d != 交付 %d", s.Name, skillBytes(s), deliveredBytes(s))
		}
	}
	// 「docs」5+「文档」6+「x」1+「y」1 = 13 ≤ 20：这条应放行，证明预算没有一刀切全拒。
	if len(res.Skills) != 1 || res.Skills[0].Name != "docs" {
		t.Fatalf("未超预算的候选应放行，实得 %+v", res.Skills)
	}
	if strings.Contains(res.Reason, "docs") {
		t.Fatalf("放行的候选不应背上裁剪原因，实得 %q", res.Reason)
	}
}

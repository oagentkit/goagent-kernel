package skilldisclose

// 预算计费口径与裁剪策略（issues #289/#292）。
//
// 旧计费只算 name+description，而实际交付整个 Skill（含 Tags/Resources）：
// 宿主以为预算守住了，实际超支且无信号。裁剪又是 first-fit + break：第一条放不下
// 就整体停止，后面更小的候选被连带丢弃，Reason 还把「排序靠前的一条太大」
// 说成「预算连一条都放不下」。

import (
	"strings"
	"testing"
)

// TestMetadataBytesCountsAllDeliveredFields 计费口径 = 交付口径（四项全算）。
func TestMetadataBytesCountsAllDeliveredFields(t *testing.T) {
	s := Skill{Name: "deploy", Description: "短描述",
		Tags: []string{"a", "b", "c"}, Resources: []string{"r1", "r2", "r3"}}
	want := len(s.Name) + len(s.Description)
	for _, tag := range s.Tags {
		want += len(tag)
	}
	for _, r := range s.Resources {
		want += len(r)
	}
	if got := MetadataBytes(s); got != want {
		t.Fatalf("MetadataBytes=%d want %d（必须含 Tags/Resources）", got, want)
	}
}

// TestBudgetBillsActualDeliveredMetadata 预算按交付字节判：N-1 不可，N 可。
func TestBudgetBillsActualDeliveredMetadata(t *testing.T) {
	s := Skill{Name: "s", Description: "d", Tags: []string{"tag1", "tag2", "tag3"}, Resources: []string{"res1"}}
	c := mustCatalog(t, s)
	n := MetadataBytes(s)

	res, _ := c.Disclose(Query{Text: "s"}, Budget{MaxBytes: n - 1})
	if len(res.Skills) != 0 {
		t.Fatalf("预算 %d < 实际元数据 %d 时不得披露: %+v", n-1, n, res.Skills)
	}
	if !res.Truncated || res.Reason == "" {
		t.Fatalf("超预算必须显式标记并给 Reason: %+v", res)
	}

	res2, _ := c.Disclose(Query{Text: "s"}, Budget{MaxBytes: n})
	if len(res2.Skills) != 1 {
		t.Fatalf("预算恰好等于实际元数据 %d 时应披露: %+v", n, res2)
	}
	if res2.Truncated || res2.Reason != "" {
		t.Fatalf("未裁剪不应标截断: %+v", res2)
	}
}

// TestBudgetSkipsOversizedAndKeepsSmaller 超大候选应跳过而不是 break：
// 后面更小的候选仍要参与。
func TestBudgetSkipsOversizedAndKeepsSmaller(t *testing.T) {
	big := Skill{Name: "a_big", Description: strings.Repeat("共同", 32)} // 靠前、很大
	small := Skill{Name: "b_small", Description: "共同"}                 // 靠后、很小
	c := mustCatalog(t, big, small)
	res, _ := c.Disclose(Query{Text: "共同"}, Budget{MaxBytes: 60})
	if len(res.Skills) != 1 || res.Skills[0].Name != "b_small" {
		t.Fatalf("大候选放不下时应继续看后面的小候选，实得 %+v", res.Skills)
	}
	if !res.Truncated {
		t.Fatal("裁掉了候选必须标 Truncated（含空结果那次）")
	}
	if !strings.Contains(res.Reason, "候选过大") {
		t.Fatalf("Reason 应说明是候选过大被跳过，实得 %q", res.Reason)
	}
}

// TestBudgetReasonDistinguishesTooSmallVsOversized 两种失败形态的 Reason 必须不同。
func TestBudgetReasonDistinguishesTooSmallVsOversized(t *testing.T) {
	big := Skill{Name: "a_big", Description: strings.Repeat("共同", 32)}
	small := Skill{Name: "b_small", Description: "共同"}

	mixed, _ := mustCatalog(t, big, small).Disclose(Query{Text: "共同"}, Budget{MaxBytes: 60})
	none, _ := mustCatalog(t, big).Disclose(Query{Text: "共同"}, Budget{MaxBytes: 1})
	if !none.Truncated || len(none.Skills) != 0 {
		t.Fatalf("预算连一条都放不下时必须标截断，实得 %+v", none)
	}
	if !strings.Contains(none.Reason, "放不下") {
		t.Fatalf("一条都放不下的 Reason 应明说，实得 %q", none.Reason)
	}
	if mixed.Reason == none.Reason {
		t.Fatal("「候选中有一条过大」与「一条都放不下」不得共用 Reason")
	}
}

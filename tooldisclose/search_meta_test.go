package tooldisclose

// 检索截断的显式信号（issue #287）。
//
// Search 契约写明「已截断」，但返回值只有 Tool+Score：宿主拿不到「还有多少候选
// 没披露」，模型看到三条就会认定「只有这三个相关」。与 skilldisclose.Result.Truncated
// 对齐：裁剪必须可判，不静默。

import (
	"strconv"
	"testing"
)

// TestSearchWithMetaReportsTruncation N > limit 时必须报出总数与未披露数。
func TestSearchWithMetaReportsTruncation(t *testing.T) {
	var ts []Tool
	for i := 0; i < 40; i++ {
		ts = append(ts, Tool{Name: "tool_" + strconv.Itoa(i), Description: "共用的检索词"})
	}
	c := NewCatalog(ts)
	const limit = DefaultSearchLimit
	got := c.SearchWithMeta("共用")
	if len(got.Matches) != limit {
		t.Fatalf("命中应被截到 %d 条，实得 %d", limit, len(got.Matches))
	}
	if got.Total != len(ts) {
		t.Fatalf("Total 应为截断前命中总数 %d，实得 %d", len(ts), got.Total)
	}
	if got.Omitted != len(ts)-limit {
		t.Fatalf("Omitted 应为 %d，实得 %d", len(ts)-limit, got.Omitted)
	}
	if !got.Truncated {
		t.Fatal("截断了候选就必须标 Truncated（与 skilldisclose.Result.Truncated 同义）")
	}
	// Search 与 SearchWithMeta 的 Matches 必须逐项一致（同一实现，不另算一份）。
	plain := c.Search("共用")
	if len(plain) != len(got.Matches) {
		t.Fatalf("Search 与 SearchWithMeta 条数不一致: %d vs %d", len(plain), len(got.Matches))
	}
	for i := range plain {
		if plain[i].Name != got.Matches[i].Name || plain[i].Score != got.Matches[i].Score {
			t.Fatalf("第 %d 项不一致: %+v vs %+v", i, plain[i], got.Matches[i])
		}
	}
}

// TestSearchWithMetaNotTruncated 命中数未超上限时不标截断。
func TestSearchWithMetaNotTruncated(t *testing.T) {
	c := NewCatalog([]Tool{
		{Name: "alpha_lookup", Description: "共用的检索词"},
		{Name: "beta_report", Description: "共用的检索词"},
	})
	got := c.SearchWithMeta("共用")
	if got.Total != 2 || got.Omitted != 0 || got.Truncated {
		t.Fatalf("未截断时不得报截断: %+v", got)
	}
	if len(got.Matches) != 2 {
		t.Fatalf("应返回两条，实得 %d", len(got.Matches))
	}
}

// TestSearchWithMetaNoHits 无命中/无检索词的统计口径：Total=0、不标截断。
func TestSearchWithMetaNoHits(t *testing.T) {
	c := NewCatalog(tools())
	for _, q := range []string{"", "完全查不到的词"} {
		got := c.SearchWithMeta(q)
		if got.Total != 0 || got.Omitted != 0 || got.Truncated || len(got.Matches) != 0 {
			t.Fatalf("查询 %q 应得空统计，实得 %+v", q, got)
		}
	}
}

package tooldisclose

// 检索截断的显式性（#287）：命中集因检索上限被截断时，调用方必须拿得到
// 「还有多少没给」的信号。静默截断与「无命中兜底给随便几个」是同一种反模式
// 的两个分支——模型会以为「就这三个工具相关」，下一轮不再检索，召回被静默削掉。

import (
	"strconv"
	"testing"
)

// statTools 全部命中同一关键词的合成目录：截断信号只在「命中数 > 上限」时亮。
func statTools(n int) []Tool {
	out := make([]Tool, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Tool{Name: "stat_tool_" + strconv.Itoa(i), Description: "都带同一个关键词"})
	}
	return out
}

// TestSearchWithStatsReportsTruncation N > limit 的全命中目录：返回值必须携带
// 「还有 N-limit 条未披露」。
//
// 反向验证：Total 置 0 / Truncated 恒 false ⇒ 本条必红。
func TestSearchWithStatsReportsTruncation(t *testing.T) {
	const n = 10
	c := NewCatalog(statTools(n)) // 检索上限取默认 DefaultSearchLimit
	r := c.SearchWithStats("关键词")
	if r.Total != n {
		t.Fatalf("命中总数应为 %d（截断前的全部命中），实得 %d", n, r.Total)
	}
	if len(r.Matches) != DefaultSearchLimit {
		t.Fatalf("Matches 应仍按检索上限截断，实得 %d", len(r.Matches))
	}
	if !r.Truncated {
		t.Fatal("Total > len(Matches) 必须标 Truncated（裁剪显式，#287）")
	}
	if omitted := r.Total - len(r.Matches); omitted != n-DefaultSearchLimit {
		t.Fatalf("未披露数应为 %d，实得 %d", n-DefaultSearchLimit, omitted)
	}
	// 既有契约不变：Search 仍是同一份截断命中（排序、数量逐项一致）。
	old := c.Search("关键词")
	if len(old) != len(r.Matches) {
		t.Fatalf("Search 与 SearchWithStats.Matches 必须同源，实得 %d vs %d", len(old), len(r.Matches))
	}
	for i := range old {
		if old[i].Name != r.Matches[i].Name || old[i].Score != r.Matches[i].Score {
			t.Fatalf("两条出口的结果在第 %d 项分叉: %v vs %v", i, old[i], r.Matches[i])
		}
	}
}

// TestSearchWithStatsNoTruncation 命中数不超上限：Truncated 必须为 false，
// 否则这个标记就不再是信号（与 skilldisclose.Result.Truncated 同一条判据：
// 没裁就不该标裁掉）。
func TestSearchWithStatsNoTruncation(t *testing.T) {
	c := NewCatalog(statTools(2))
	r := c.SearchWithStats("关键词")
	if r.Total != 2 || len(r.Matches) != 2 || r.Truncated {
		t.Fatalf("未超上限不得标截断，实得 total=%d matches=%d truncated=%v", r.Total, len(r.Matches), r.Truncated)
	}
	// 无命中：全零结果，Truncated=false——空是空，截断是截断，两者不得混淆。
	empty := c.SearchWithStats("完全查不到的词")
	if empty.Total != 0 || empty.Truncated || len(empty.Matches) != 0 {
		t.Fatalf("无命中应为全零结果，实得 %+v", empty)
	}
	// 无检索词：零值结果（与 Search 返回 nil 的既有口径一致——没有意图不猜）。
	var emptyCatalog Catalog
	if got := emptyCatalog.SearchWithStats(""); got.Total != 0 || got.Truncated || got.Matches != nil {
		t.Fatalf("无检索词应返回零值结果，实得 %+v", got)
	}
}

// TestSearchWithStatsTotalCountsAllHits Total 必须数**全部**命中，与目录遍历顺序无关：
// 计数少一个，「还有多少没给」就少报一个，宿主的再检索决策就建立在错的账上。
func TestSearchWithStatsTotalCountsAllHits(t *testing.T) {
	// 并列分值 + 大目录：ranker 的前 k 插入会挤掉末位，但 total 不得少记。
	n := DefaultSearchLimit*3 + 2
	c := NewCatalog(statTools(n))
	for i := 0; i < 20; i++ { // map 序随机化，重复验证计数稳定
		r := c.SearchWithStats("关键词")
		if r.Total != n {
			t.Fatalf("第 %d 次：命中总数必须稳定为 %d，实得 %d", i, n, r.Total)
		}
		if !r.Truncated || len(r.Matches) != DefaultSearchLimit {
			t.Fatalf("第 %d 次：截断信号缺失，实得 %+v", i, r)
		}
	}
}

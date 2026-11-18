// chart_status_test.go —— 图表状态词的导出枚举面判据（issue #356）。
package protocol

import (
	"slices"
	"testing"
)

// TestAllChartStatusesPinned 档名出口的取值逐条钉住。
//
// 宿主渲染图例要靠它拿到「ok / warn / down」三个档名——档名是宿主与内核
// 之间的约定值，多一个少一个都是接口变更，故逐条钉住。
func TestAllChartStatusesPinned(t *testing.T) {
	got := AllChartStatuses()
	want := []string{"down", "ok", "warn"}
	if !slices.Equal(got, want) {
		t.Fatalf("档名出口 = %v，want %v", got, want)
	}
}

// TestChartStatusWordsPinned 三个档的词形条数逐条钉住。
//
// 条数变了 = 内核认的词形变了（宿主那份手抄清单会漂），故必须显式改这条
// 断言才允许变。
func TestChartStatusWordsPinned(t *testing.T) {
	cases := []struct {
		bucket string
		want   int
	}{
		{"ok", 14},
		{"warn", 10},
		{"down", 19},
	}
	for _, c := range cases {
		if got := len(ChartStatusWords(c.bucket)); got != c.want {
			t.Errorf("档 %q 的词形条数 = %d，want %d（内核认的词形变了，需显式确认）", c.bucket, got, c.want)
		}
	}
	if got := ChartStatusWords("no-such-bucket"); got != nil {
		t.Errorf("未知档名必须返回 nil（不臆断），实得 %v", got)
	}
}

// TestChartStatusWordsFollowsTheTable 反向验证：导出出口必须**跟着词表走**。
//
// 临时从 chartStatusOK 删掉一个词形，导出出口必须同步少一个——若出口是另抄
// 的一份清单，这里立刻转红（issue #356 验收 1 的反向验证）。
func TestChartStatusWordsFollowsTheTable(t *testing.T) {
	saved, had := chartStatusOK["healthy"]
	delete(chartStatusOK, "healthy")
	defer func() {
		if had {
			chartStatusOK["healthy"] = saved
			return
		}
		delete(chartStatusOK, "healthy")
	}()
	for _, w := range ChartStatusWords("ok") {
		if w == "healthy" {
			t.Fatal("词形出口仍含已从词表删除的取值（出口是另抄的一份清单）")
		}
	}
	for _, w := range AllChartStatusWords() {
		if w == "healthy" {
			t.Fatal("全部词形出口仍含已从词表删除的取值（出口是另抄的一份清单）")
		}
	}
	if got := ChartStatus("healthy"); got != "" {
		t.Fatalf("词表删除后归一结果 = %q，want 空串（未识别词保持空）", got)
	}
}

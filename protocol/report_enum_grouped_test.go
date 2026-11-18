// report_enum_grouped_test.go —— 分组词表的登记面与一致性判据（issue #356）。
//
// 与 report_enum_test.go（扁平词表登记面）分开：那边的形状是「一张扁平词表 ↔
// 一个 All*() 出口 ↔ 一个 Is*Valid 谓词」，本文件处理的是**分组**词表——
// 词表按档分组，宿主要的「合法值集合」是「档名」与「每档的词形」两件事。
//
// 为什么另立一格而不是塞进扁平格：硬塞会逼出一份「把各档词形拍平」的假表——
// 那正是 #356 要消灭的第二份真相。登记面照样要求「导出出口与词表同源」+
// 「双向断言」，只是断言按档做。
package protocol

import (
	"slices"
	"testing"
)

// groupedVocabularyCase 分组词表的登记面（issue #356）。
//
// # 为什么另立一格而不是塞进 vocabularyCases
//
// vocabularyCases 的形状是「一张扁平词表 ↔ 一个 All*() 出口 ↔ 一个 Is*Valid
// 谓词」：all() 返回的集合必须与 table 的成员集合逐字相等。图表状态词**套不上**：
// 它是 43 个输入词形分归 ok / warn / down 三档的**归一输入词表**，宿主要的
// 「合法值集合」是「档名」与「每档的词形」两件事，不是一张扁平集合。
//
// 硬塞进扁平格会逼出一份「把 43 个词拍平」的假表——那正是本条要消灭的
// 「第二份真相」。故另立一格：登记面照样要求「导出出口与词表同源」+
// 「双向断言」，只是断言按档做。
type groupedVocabularyCase struct {
	name       string                // 登记名
	tableNames []string              // 包内声明的 map[string]bool 变量名（各档的词表）
	buckets    func() []string       // 档名集合的导出出口
	words      func(string) []string // 某档的输入词形
	allWords   func() []string       // 全部输入词形
	normalizes func(word, bucket string) bool
}

// groupedVocabularyCases 分组词表的清单。
var groupedVocabularyCases = []groupedVocabularyCase{
	{
		name:       "图表状态输入词（ok / warn / down 三档）",
		tableNames: []string{"chartStatusOK", "chartStatusWarn", "chartStatusDown"},
		buckets:    AllChartStatuses,
		words:      ChartStatusWords,
		allWords:   AllChartStatusWords,
		normalizes: func(word, bucket string) bool { return ChartStatus(word) == bucket },
	},
}

// TestGroupedVocabularyEnumerationMatchesNormalization 分组词表的导出出口与
// 归一判定必须**双向**一致（issue #356）。
//
// 断言三条：
//
//  1. 每个档名都对应一组非空词形，且该词形归一后**回到同一档**（往返一致）；
//  2. 各档词形互不相交（同一词不能归两档——否则归一结果取决于遍历顺序）；
//  3. 「全部词形」出口恰好是各档词形的并集。
//
// 反向验证：把某个词从一张分档表里删掉 → 断言 3 转红（并集少一个）；
// 把某个词加进另一档 → 断言 2 转红（词出现在多个档里）。
func TestGroupedVocabularyEnumerationMatchesNormalization(t *testing.T) {
	for _, g := range groupedVocabularyCases {
		t.Run(g.name, func(t *testing.T) {
			buckets := g.buckets()
			if !slices.IsSorted(buckets) {
				t.Fatalf("档名出口必须返回排序后的切片：%v", buckets)
			}
			union := map[string]bool{}
			for _, b := range buckets {
				ws := g.words(b)
				if len(ws) == 0 {
					t.Fatalf("档 %q 没有输入词形", b)
				}
				if !slices.IsSorted(ws) {
					t.Fatalf("档 %q 的词形出口必须返回排序后的切片：%v", b, ws)
				}
				for _, w := range ws {
					if union[w] {
						t.Fatalf("词形 %q 出现在多个档里（归一结果将取决于遍历顺序）", w)
					}
					union[w] = true
					if !g.normalizes(w, b) {
						t.Fatalf("词形 %q 归一后不是 %q（出口与判定面不同源）", w, b)
					}
				}
			}
			all := g.allWords()
			if len(all) != len(union) {
				t.Fatalf("全部词形出口 %d 个，各档并集 %d 个：%v", len(all), len(union), all)
			}
			for _, w := range all {
				if !union[w] {
					t.Fatalf("全部词形出口里的 %q 不属于任何档", w)
				}
			}
			// 副本语义：改写返回值不得影响下一次读取（宿主会改写拿到的清单）。
			if len(buckets) > 0 {
				buckets[0] = "__mutated_by_host__"
				again := g.buckets()
				if slices.Contains(again, "__mutated_by_host__") {
					t.Fatal("档名出口返回了共享底层数组：宿主改写污染了词表")
				}
			}
			if len(all) > 0 {
				all[0] = "__mutated_by_host__"
				again := g.allWords()
				if slices.Contains(again, "__mutated_by_host__") {
					t.Fatal("全部词形出口返回了共享底层数组：宿主改写污染了词表")
				}
			}
		})
	}
}

package skilldisclose

// 中文分词与空结果原因（issue #288）。
//
// 目录内容以中文为主，查询侧 Text 是自然语言描述。旧 tokenize 只认 ASCII，
// 全部汉字被当分隔符冲掉：中文查询整条相关度通道失效，空结果 Reason 还把宿主
// 推向「没有这个能力」。分词口径必须与「披露面以中文为主」对齐。

import (
	"strings"
	"testing"
)

// TestChineseQueryHitsDescriptionAndTag 纯中文查询命中中文描述与中文标签。
func TestChineseQueryHitsDescriptionAndTag(t *testing.T) {
	c := mustCatalog(t,
		Skill{Name: "chinese_skill", Description: "审查组件的写法与性能问题", Tags: []string{"前端"}},
		Skill{Name: "other_skill", Description: "无关描述", Tags: []string{"后端"}},
	)
	for _, q := range []string{"审查组件的写法", "性能问题", "前端", "审查、组件"} {
		res, err := c.Disclose(Query{Text: q}, Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skills) == 0 || res.Skills[0].Name != "chinese_skill" {
			t.Fatalf("中文查询 %q 应命中 chinese_skill，实得 %+v", q, res.Skills)
		}
	}
}

// TestEmptyReasonDistinguishesNoSignal 无相关度信号与「确无匹配」必须可区分。
func TestEmptyReasonDistinguishesNoSignal(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "alpha", Description: "处理条目", Tags: []string{"t"}})

	noSignal, _ := c.Disclose(Query{}, Budget{})
	if !strings.Contains(noSignal.Reason, "无相关度信号") {
		t.Fatalf("空查询应说明「无相关度信号」，实得 %q", noSignal.Reason)
	}

	noMatch, _ := c.Disclose(Query{Text: "完全无关的词"}, Budget{})
	if !strings.Contains(noMatch.Reason, "没有技能命中") {
		t.Fatalf("有查询但无匹配应说明「没有技能命中」，实得 %q", noMatch.Reason)
	}
	if noSignal.Reason == noMatch.Reason {
		t.Fatal("两种空结果的原因不得共用一句话——宿主无法据此决策")
	}
}

// TestChineseTokenizer 分词本身：非 ASCII 连续段是一个词元。
func TestChineseTokenizer(t *testing.T) {
	words := tokenize("审查组件的写法、性能问题 ABC_42")
	for _, want := range []string{"审查组件的写法", "性能问题", "abc", "42"} {
		if !words[want] {
			t.Fatalf("分词缺少 %q: %v", want, words)
		}
	}
	if words["、"] {
		t.Fatal("标点不应成为词元")
	}
}

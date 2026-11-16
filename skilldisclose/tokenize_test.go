package skilldisclose

// 中文相关度通道（#288）：分词只认 ASCII 的旧口径下，汉字全部被当分隔符冲掉，
// 纯中文查询切不出任何词元，整条相关度通道（描述命中、标签命中）在中文语境下
// 是死的——而披露面以中文为主是既定事实。空结果的 Reason 还会把宿主推向
// 「回退全量披露」，包的存在理由被自己的失败模式推翻。

import (
	"strings"
	"testing"
)

// TestChineseQueryHitsChineseDescription 纯中文查询必须命中中文描述。
//
// 反向验证：把 tokenize 改回 ASCII-only（删掉非 ASCII 字母分支）⇒ 查询切不出
// 词元 ⇒ 零命中 ⇒ 本条必红。
func TestChineseQueryHitsChineseDescription(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "chinese_skill", Description: "审查组件的写法与性能问题"})
	res, err := c.Disclose(Query{Text: "审查组件的写法"}, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skills) != 1 || res.Skills[0].Name != "chinese_skill" {
		t.Fatalf("纯中文查询必须命中中文描述（连续汉字段成词元后按子串命中），实得 %+v", res.Scored)
	}
	if res.Scored[0].Score <= 0 {
		t.Fatalf("描述命中必须拿到正分，实得 %d", res.Scored[0].Score)
	}
}

// TestChineseQueryHitsChineseTag 中文标签词必须能被中文查询命中（旧口径下同样 0 条）。
func TestChineseQueryHitsChineseTag(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "chinese_skill", Description: "审查组件的写法与性能问题", Tags: []string{"前端"}})
	res, _ := c.Disclose(Query{Text: "前端 性能"}, Budget{})
	if len(res.Skills) != 1 {
		t.Fatalf("中文标签词必须被中文查询命中，实得 %+v", res.Scored)
	}
	if res.Scored[0].Score < scoreTag {
		t.Fatalf("标签命中应拿到标签分（≥%d），实得 %d", scoreTag, res.Scored[0].Score)
	}
}

// TestTokenizerProducesCJKTokens 词元集合必须出现非 ASCII 词元（判据的落点本体）：
// 连续汉字段成一个词元、中英混排各自成词元、中文标点仍是分隔符。
// 三个断言在 ASCII-only 分词下全部落空。
func TestTokenizerProducesCJKTokens(t *testing.T) {
	got := tokenize("审查组件的写法")
	if !got["审查组件的写法"] {
		t.Fatalf("连续汉字段应成一个词元，实得 %v", got)
	}
	mixed := tokenize("审查 vue 组件")
	if !mixed["审查"] || !mixed["组件"] || !mixed["vue"] {
		t.Fatalf("混排文本应各自成词元，实得 %v", mixed)
	}
	punct := tokenize("审查、组件。写法")
	if len(punct) != 3 || !punct["审查"] || !punct["组件"] || !punct["写法"] {
		t.Fatalf("中文标点应作分隔符，实得 %v", punct)
	}
}

// TestEmptyReasonDistinguishesNoSignalFromNoMatch 两种空结局的 Reason 必须可区分（#288）：
// 「查询没有携带任何相关度信号」与「有信号但没命中」共用一句话时，宿主无从决策
// （该补全查询，还是回退全量披露）。
func TestEmptyReasonDistinguishesNoSignalFromNoMatch(t *testing.T) {
	c := mustCatalog(t, Skill{Name: "a", Description: "处理条目"})
	noSignal, err := c.Disclose(Query{}, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	noMatch, err := c.Disclose(Query{Text: "完全不存在的技能词"}, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(noSignal.Reason, "相关度信号") {
		t.Fatalf("无信号查询的 Reason 必须明说「没有相关度信号、无从打分」，实得 %q", noSignal.Reason)
	}
	if !strings.Contains(noMatch.Reason, "没有技能命中") {
		t.Fatalf("有信号没命中的 Reason 必须明说「没有技能命中」，实得 %q", noMatch.Reason)
	}
	if noSignal.Reason == noMatch.Reason {
		t.Fatal("两种空结局共用一句话，宿主无法决策（#288）")
	}
}

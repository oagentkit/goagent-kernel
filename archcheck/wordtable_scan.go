// wordtable_scan.go —— 判定词表门禁的「手抄」扫描面。
//
// # 为什么扫描面单独成文件
//
// 扫描面判的是**出现点级**的事（这一处字面量有没有名字），
// 而 wordtable.go 判的是**登记面**的事（这张表在不在、出口读不读它）。
// 两种判据的失败形态不同（前者是假红，后者是静默失管），
// 混在一个文件里时，后写的那个会盖掉先写的那个——而读门禁的人
// 恰恰要先知道自己踩的是哪一类坑。
package archcheck

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// wordTableHandCopies 扫全仓生产代码，找出**手抄**了登记词表取值的字面量。
//
// # 判据（出现点级）
//
// 一处字面量落在某张判定词表的取值域里，且**没有名字** ⇒ 手抄。
//
// 「没有名字」是三件事的合取：
//
//   - 它不是常量声明处的字面量（那是本包给这个取值起的名字）；
//   - 它不在主人表自身的定义区间里（那是这张表的定义，不是抄）；
//   - 它不是 owner.Const 这样的限定引用（引用天然不是字面量——
//     这条放行臂是**结构性**的，不需要单独的分支）。
//
// # 为什么是「出现点级」而不是「包级」
//
// 包级判据（这个包有没有命名过这个取值）有个洞：同一个包里既定义了常量、
// 又写了同一取值的裸字面量，后者不判——而那正是「命名过了却还写字面量」，
// 比「压根没命名」更该改。出现点级把豁免精确到**那处字面量**。
//
// 局限（写明判不了什么）：本判据问的是「有没有名字」，不是
// 「这段字面量在不在判定路径上」——后者要 go/types 档的类型信息
// （RunOptions.TypeAware），本轮做不到。
func wordTableHandCopies(root string, idx *wordRepo, facts []wordTableFacts, seg WordTables) []Violation {
	// 主人表自己的**定义处**：主人用自己的词是定义，不是手抄。
	ownDefs := map[string][]wordRange{}
	for _, f := range facts {
		ownDefs[f.pkg] = append(ownDefs[f.pkg], f.defRanges...)
	}
	var out []Violation
	for _, p := range idx.packages() {
		if wordTableIgnored(seg.IgnoreDirs, p.rel) {
			continue
		}
		for _, lit := range p.literals {
			// 这一处字面量落在**哪些**表里？同一个拼写可能是多张表的取值
			// （contract 的 failed 与 taskboard 的 failed 同形不同义）。
			// 同一处只报一次，但必须把**全部归属**说出来——只报第一张表
			// 会把人引到另一张表的常量上（那是**错误指路**，比不报更坏）。
			var hits []*wordTableFacts
			for fi := range facts {
				f := &facts[fi]
				if f.kind != wordTableKindDecision {
					// 输入归一词表不判消费方：那些词形是外部输入，不是判定依据。
					continue
				}
				if _, ok := f.values[lit.value]; !ok {
					continue
				}
				hits = append(hits, f)
			}
			if len(hits) == 0 {
				continue
			}
			if lit.constDef {
				// 命名处：本包给这个取值起了名字（常量声明处）。
				continue
			}
			if inWordRanges(lit, ownDefs[p.rel]) {
				// 主人表自身的定义处。
				continue
			}
			out = append(out, wordTableHandCopyViolation(root, idx, p, hits, lit))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// wordTableHandCopyViolation 造一条手抄违反（文案要说清「改成什么」）。
//
// 文案必须**点名**可落地的修法：读者看到「手抄了 failed」不知道该写什么，
// 看到「本包已把它命名成常量 StateFailed——改用那个常量」才知道。
func wordTableHandCopyViolation(root string, idx *wordRepo, p *wordPkg, hits []*wordTableFacts, lit stringLit) Violation {
	rel, _ := filepath.Rel(root, lit.path)
	// 归属表与候选常量：同一个取值可能同时属多张表（contract 的 failed 与
	// taskboard 的 failed 同形不同义），文案必须把全部归属列出来。
	owners := make([]string, 0, len(hits))
	cands := make([]string, 0, len(hits))
	for _, f := range hits {
		owners = append(owners, f.pkg)
		if c := f.values[lit.value]; c != "" {
			cands = append(cands, f.pkg+"."+c)
		}
	}
	hint := "在本包把它声明成自己的常量"
	switch {
	case p.ownConsts[lit.value] != "":
		// 命名过了却还在写字面量——这条比「没命名」更该改。
		hint = "本包已把它命名成常量 " + p.ownConsts[lit.value] + "——改用那个常量"
	case len(hits) == 1 && idx.referencesOwner(p, hits[0].pkg, hits[0].values[lit.value]):
		// 已经引用了主人常量却还写字面量：改用那个引用。
		hint = "你已经引用了 " + hits[0].pkg + "." + hits[0].values[lit.value] + "——改用它"
	case len(hits) > 1 && len(cands) > 0:
		// 多张表同形 ⇒ 指路必须给全部候选，否则会把人引到另一张表的常量上。
		hint = "引用你要表达的那张表的常量（" + strings.Join(cands, " / ") + "）"
	case len(cands) > 0:
		hint = "引用 " + strings.Join(cands, " / ")
	}
	text := fmt.Sprintf("手抄了 %s 的词表取值 %s（裸字面量）——%s；"+
		"裸字面量只允许出现在常量定义处：命名可以，写字面量不行",
		hits[0].pkg, strconv.Quote(lit.value), hint)
	if len(hits) > 1 {
		text = fmt.Sprintf("手抄了词表取值 %s（裸字面量）——它同时属 %d 张表（%s）：%s；"+
			"裸字面量只允许出现在常量定义处：命名可以，写字面量不行",
			strconv.Quote(lit.value), len(hits), strings.Join(owners, " / "), hint)
	}
	return Violation{
		Rule: wordTableHandCopyRuleID,
		Path: filepath.ToSlash(rel),
		Line: lit.line,
		Text: text,
		Kind: "word_table_handcopy",
	}
}

// inWordRanges 报告某处字面量是否落在给定区间内。
func inWordRanges(lit stringLit, ranges []wordRange) bool {
	for _, r := range ranges {
		if r.contains(lit.path, lit.pos) {
			return true
		}
	}
	return false
}

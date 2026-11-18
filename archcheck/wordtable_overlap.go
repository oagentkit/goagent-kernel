// wordtable_overlap.go —— 判定词表登记门禁的「同形撞车」判据。
//
// # 这条判据防的是什么
//
// 同一个拼写可能是**多张判定词表**的取值，而它们的**含义**可能相同也可能不同：
// contract 的 "failed" 是会话失败、taskboard 的 "failed" 是任务失败。
//
// 手抄判据只按「值在不在表里」判 ⇒ 它分不出「这处字面量想说的是哪张表」
// ⇒ 修法文案若只指向某一张表，就可能把人引到**错误的概念**上
// （把任务失败改成引用会话失败常量 = 语义损坏）。
//
// ⇒ 凡两张登记的**判定词表**有同形取值，必须在 policy 的 relations 段
// **显式声明关系**：
//
//   - same     ：同一概念（一张表的别名/子集），引用任一家都对；
//   - distinct ：不同概念（同形只是拼写巧合），手抄方必须**在本包命名**。
//
// # 为什么用**中心清单**而不是各表自己声明
//
// 关系是**两张表之间**的事，写在任一方都会引出「两边都声明了但不一致」的
// 第三种状态。中心清单（word_tables.relations）让「这对表是什么关系」
// 只有一个答案。
//
// # 为什么值域必须封闭
//
// 写错的关系名若被当成「不是 same」，就会被落进「不同概念」那一档——
// 而「不同概念」那一档的修法是**在本包另起一个名字**：把「同一概念」
// 误判成「不同概念」，代价是每个消费方都要多抄一份名字（词表从此不是
// 唯一真相源）。反向误判更贵：把「不同概念」当成「同一概念」⇒ 手抄指路
// 把人引向错误的常量 = 语义损坏。⇒ 写错的关系名必须当天即红。
//
// # 失败形态
//
// 全是**变红**：
//
//   - 撞了没声明 ⇒ 关系缺失（拼写巧合还是同一概念，没人说得清）；
//   - 声明了没撞 ⇒ 声明失真（今天没关系，将来两表一旦同形就会无声生效）。
package archcheck

import (
	"fmt"
	"sort"
	"strings"
)

// 同形关系的两个取值（值域封闭，见文件头）。
const (
	// wordTableRelationSame 同一概念：一张表的别名/子集，引用任一家都对。
	wordTableRelationSame = "same"
	// wordTableRelationDistinct 不同概念：同形只是拼写巧合，手抄方必须在本包命名。
	wordTableRelationDistinct = "distinct"
)

// wordTableCollisionRuleID 同形撞车判据的规则 id。
const wordTableCollisionRuleID = "harness-word-table-value-collision"

// wordTableRelations 是「两张表之间是什么关系」的查表。
type wordTableRelations struct {
	byPair map[string]string // 无序对键 → 关系值
}

// buildWordTableRelations 把 policy 的 relations 段编成查表。
//
// 用**无序对**做键：关系是**对称**的（a~b 与 b~a 是同一件事），
// 写成有序键会引出「同一对表声明两次、值还不一样」的第三种状态。
func buildWordTableRelations(rels []WordTableRelation) *wordTableRelations {
	out := &wordTableRelations{byPair: map[string]string{}}
	for _, r := range rels {
		out.byPair[wordTablePairKey(r.A, r.B)] = r.Relation
	}
	return out
}

// wordTablePairKey 生成一对表的无序键。
func wordTablePairKey(a, b WordTableRef) string {
	x := a.Pkg + "|" + a.Enum
	y := b.Pkg + "|" + b.Enum
	if x > y {
		x, y = y, x
	}
	return x + "\x00" + y
}

// wordTableRefOf 取一张派生事实的**身份**（主人包 + 枚举出口）。
//
// 为什么用枚举出口而不是包名：一个包里可能有多张表（protocol 有五张），
// 包名不足以区分它们。
func wordTableRefOf(f wordTableFacts) WordTableRef {
	return WordTableRef{Pkg: f.pkg, Enum: f.enum}
}

// wordTableCollisionViolations 判「同形撞车必须显式声明关系」。
//
// 判定面是**两张登记判定词表的值集合**——两边都是事实，不需要类型信息。
func wordTableCollisionViolations(facts []wordTableFacts, rels *wordTableRelations, policyPath string) []Violation {
	var out []Violation
	// 只在**判定词表**之间判：
	// 输入归一词表的词形是外部输入（healthy/down/正常/宕机……），
	// 消费方自由使用 ⇒ 同形是常态，不是缺陷。
	var decision []int
	for i := range facts {
		if facts[i].kind == wordTableKindDecision && len(facts[i].values) > 0 {
			decision = append(decision, i)
		}
	}
	for ii := 0; ii < len(decision); ii++ {
		for jj := ii + 1; jj < len(decision); jj++ {
			a := &facts[decision[ii]]
			b := &facts[decision[jj]]
			overlap := wordTableOverlapValues(a.values, b.values)
			key := wordTablePairKey(wordTableRefOf(*a), wordTableRefOf(*b))
			rel, declared := rels.byPair[key]
			switch {
			case len(overlap) > 0 && !declared:
				out = append(out, Violation{
					Rule: wordTableCollisionRuleID,
					Path: policyPath,
					Text: fmt.Sprintf("两张判定词表 %s.%s 与 %s.%s 有同形取值（%s）却没声明关系——"+
						"「这处字面量想说的是哪张表」没人说得清，手抄判据只能把人引到其中一张"+
						"（可能是错的概念）：在 policy 的 word_tables.relations 里声明这对表的关系"+
						"（%s = 同一概念 / %s = 不同概念）",
						a.pkg, a.enum, b.pkg, b.enum, strings.Join(overlap, " / "),
						wordTableRelationSame, wordTableRelationDistinct),
					Kind: "word_table_value_collision",
				})
			case len(overlap) == 0 && declared:
				out = append(out, Violation{
					Rule: wordTableCollisionRuleID,
					Path: policyPath,
					Text: fmt.Sprintf("policy 声明了 %s.%s 与 %s.%s 的同形关系（%s），"+
						"但两张表的值集合没有交集——声明失真（今天没关系，将来一旦同形就会无声生效）："+
						"删掉这条声明，或修正它指向的表",
						a.pkg, a.enum, b.pkg, b.enum, rel),
					Kind: "word_table_relation_stale",
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}

// wordTableOverlapValues 取两张表的同形取值（按字典序，输出稳定 = 报告可复现）。
func wordTableOverlapValues(a, b map[string]string) []string {
	var out []string
	for v := range a {
		if _, ok := b[v]; ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// wordTableConceptGroups 把命中的一组表按「是不是同一个概念」分组。
//
// 关系为 same 的两张表是**同一个概念**（一张表的别名/子集），引用任一家都对；
// 没有 same 关系的两张表是**不同概念**（同形只是拼写巧合），引用任一家都可能
// 语义损坏 ⇒ 必须在**本包**命名。
//
// 没声明关系的对（撞了却漏声明）按**不同概念**处理——保守方向：
// 指路把人引到错误的常量上，比指路让人自己命名更坏。
func wordTableConceptGroups(hits []*wordTableFacts, rels *wordTableRelations) [][]*wordTableFacts {
	if len(hits) == 0 {
		return nil
	}
	parent := make([]int, len(hits))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(i, j int) {
		ri, rj := find(i), find(j)
		if ri != rj {
			parent[rj] = ri
		}
	}
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			key := wordTablePairKey(wordTableRefOf(*hits[i]), wordTableRefOf(*hits[j]))
			if rels.byPair[key] == wordTableRelationSame {
				union(i, j)
			}
		}
	}
	groups := map[int][]*wordTableFacts{}
	order := []int{}
	for i := range hits {
		r := find(i)
		if _, ok := groups[r]; !ok {
			order = append(order, r)
		}
		groups[r] = append(groups[r], hits[i])
	}
	out := make([][]*wordTableFacts, 0, len(order))
	for _, r := range order {
		out = append(out, groups[r])
	}
	return out
}

// wordTableGroupLabel 给一个概念组起个**可指路**的标签：列出该组里这处取值
// 对应的量（"contract.StatusFailed" 这样）。
//
// 为什么标签要落到常量上：读者看到「它同时属两个概念」还不知道是哪两个意思，
// 看到两个常量名才知道自己正在混淆什么。
func wordTableGroupLabel(g []*wordTableFacts, value string) string {
	parts := make([]string, 0, len(g))
	for _, f := range g {
		if c := f.values[value]; c != "" {
			parts = append(parts, f.pkg+"."+c)
		}
	}
	return strings.Join(parts, " / ")
}

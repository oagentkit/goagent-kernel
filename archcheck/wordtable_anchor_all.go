// wordtable_anchor_all.go —— 关系锚点的「all 形态」：锚住两端**共有**的全部取值。
//
// # 为什么要有这个形态
//
// 抽样锚定只钉住一两个取值 ⇒ 其余共有取值漂了（取值改了、别名指向变了、
// 某一端删了常量）要靠族覆盖 / 派生漂移判据兜 ⇒ 关系「说谎」在那部分取值
// 上没人判。all 形态把锚点从「抽样」升级成「覆盖全部同形取值」。
//
// # 为什么这不是新机制
//
// all 形态只是把**锚点取值集合**换成「两张表的共有取值集合」⇒ 判据 1~4
// 原样复用：
//
//   - 判据 1（锚点存在）：每个共有取值在本端必须有对应常量（没有 ⇒ 这一端
//     锚不住它 ⇒ 那一个取值上关系没人钉）；
//   - 判据 3（成对）：两端锚定的都是共有取值 ⇒ 天然成对；
//   - 判据 4（distinct 不同处声明）：每个共有取值的两端根声明必须不同
//     ⇒ 「不同概念」在**全部**共有取值上都被验过，而不是只验了抽样的那几个。
package archcheck

import "fmt"

// wordTableAnchorsAll 报告这一端的锚点是不是 all 形态。
func wordTableAnchorsAll(list []WordTableAnchor) bool {
	for _, a := range list {
		if a.All {
			return true
		}
	}
	return false
}

// wordTableAnchorAllSide 解析 all 形态的一端：取值 → 根声明。
//
// 「共有取值」= 两张表都有的取值（关系的同形前提）。每个共有取值在**本端**
// 必须有对应常量；解析不出根声明时该取值的根声明留空——判据 4 跳过它
// （「判不了」不当成「判过了」；缺常量这件事已由判据 1 报出）。
func wordTableAnchorAllSide(idx *wordRepo, f, other *wordTableFacts, r WordTableRelation, side, policyPath string) (map[string]string, []Violation) {
	vals := map[string]string{}
	var out []Violation
	for _, v := range sortedWordNames(f.values) {
		if _, shared := other.values[v]; !shared {
			// 不是同形取值 ⇒ 不在这条关系的锚定面里（all 覆盖的是「共有」，
			// 不是「全部」——一端多出来的取值不属于这条关系）。
			continue
		}
		constName := f.values[v]
		if constName == "" {
			out = append(out, Violation{
				Rule: wordTableAnchorRuleID,
				Path: policyPath,
				Text: fmt.Sprintf("关系 %s.%s ↔ %s.%s 声明为「锚住全部共有取值」，"+
					"但共有取值 %q 在 %s 端没有对应常量——这一端锚不住它"+
					"（该取值漂出了这张表，或它在本包里从来就不是常量）："+
					"补上常量并把它加进表，或把这条关系改回抽样锚定",
					r.A.Pkg, r.A.Enum, r.B.Pkg, r.B.Enum, v, side),
				Kind: "word_table_anchor_no_constant",
			})
			vals[v] = "" // 仍是共有取值（成对判据要看得见它），只是没有根声明
			continue
		}
		rootPkg, rootName, value, ok := idx.resolveConstRoot(f.pkg, constName, 0)
		if !ok || value == "" {
			out = append(out, Violation{
				Rule: wordTableAnchorRuleID,
				Path: policyPath,
				Text: fmt.Sprintf("关系 %s.%s ↔ %s.%s 的 %s 侧共有取值 %q 对应的常量 %s.%s 解析不出取值——"+
					"锚点指向了不存在的东西（改名/被删）＝ 登记漂移：改常量名，或把它加进表",
					r.A.Pkg, r.A.Enum, r.B.Pkg, r.B.Enum, v, side, f.pkg, constName),
				Kind: "word_table_anchor_missing",
			})
			vals[v] = ""
			continue
		}
		vals[v] = rootPkg + "|" + rootName
	}
	return vals, out
}

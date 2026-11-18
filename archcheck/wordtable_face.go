// wordtable_face.go —— 词表登记门禁的「函数面」判据（判定谓词 / 输入归一出口）。
//
// # 为什么单独成文件
//
// 登记面判的是「这张表在不在、出口读不读它」（wordtable.go），
// 而本文件判的是「这张表**登记的另外两个函数面**与代码事实是否一致」：
//
//   - 判定谓词（predicate）：表的**判定面**。枚举出口读同一张表，只保证
//     「表 ↔ 出口」不漂；而判定面（IsKnownStatus / Valid）是另一个独立
//     读表的地方——它若另抄一份清单，判定就会与词表分叉。
//   - 输入归一出口（normalizer）：只属于 **kind: input**。没有归一出口，
//     消费方只能**自己**判这个词形 ⇒ 同一个输入词在不同消费方得到不同结论。
//
// 两者的失败形态都是**静默失管**（登记与事实偏离却没人发现），
// 故与登记面同口径：一律报红，且文案点名该改哪里。
package archcheck

import ()

// 词表门禁的函数面两条规则 ID（内置规则，不在 policy 的 rules 段里）。
const (
	wordTablePredicateRuleID  = `harness-word-table-predicate-drift`
	wordTableNormalizerRuleID = `harness-word-table-normalizer-missing`
)

// wordTableFuncFaceViolations 判「这张表登记的函数面」与代码事实是否一致。
//
// 返回的每条违反都指向 **policy**（不是代码）：要改的是声明面。
func wordTableFuncFaceViolations(root string, idx *wordRepo, pol *Policy, seg WordTables) []Violation {
	policyPath := PolicyPath(root, pol)
	var out []Violation
	for _, t := range seg.Tables {
		owner := idx.byRel[t.Pkg]
		if owner == nil {
			// 包不存在：drift 已由登记面报红（同一个事实不重复报两次）。
			continue
		}
		out = append(out, wordTablePredicateViolations(policyPath, owner, t)...)
		out = append(out, wordTableNormalizerViolations(policyPath, owner, t)...)
	}
	return out
}

// wordTablePredicateViolations 判判定谓词与代码事实是否一致。
//
// 判据：谓词必须存在，且必须**读登记的那张表**——读别的 map 就是
// 第二个真相源（判定与词表分叉，而门禁不会红）。
func wordTablePredicateViolations(policyPath string, owner *wordPkg, t WordTable) []Violation {
	if t.Predicate == "" {
		// 没声明判定面 = 这张表没有登记判定面（不判）。
		// 声明面靠 policy，不靠自觉：宿主要守住自己的判定面就写上它。
		return nil
	}
	fn := owner.resolveFunc(t.Predicate)
	if fn == nil {
		return []Violation{{
			Rule: wordTablePredicateRuleID,
			Path: policyPath,
			Text: `word_tables 段登记的词表 ` + t.Pkg + ` 的判定谓词 ` + t.Predicate +
				` 不存在——判定面改名/迁走会让这条登记静默失管`,
			Kind: `word_table_decl_drift`,
		}}
	}
	if t.Backing != "" && !bodyReferences(fn, t.Backing) {
		return []Violation{{
			Rule: wordTablePredicateRuleID,
			Path: policyPath,
			Text: `word_tables 段登记的词表 ` + t.Pkg + ` 的判定谓词 ` + t.Predicate +
				` 没有读登记的那张表 ` + t.Backing +
				`——它读的是另一份清单，判定与词表会分叉`,
			Kind: `word_table_decl_drift`,
		}}
	}
	return nil
}

// wordTableNormalizerViolations 判输入归一出口与代码事实是否一致。
//
// 判据（kind: input）：
//   - 必须**声明**归一出口：没有它，消费方只能自己判这个词形；
//   - 归一出口必须**存在**、必须**导出**（宿主要用它；未导出等于没有出口）、
//     且必须读登记的那张表（归一到哪一档由那张表说了算）。
//
// kind: decision 的表不该声明归一出口——归一出口是输入归一词表的属性；
// 留着无用声明不如报错（门禁的失败形态是变红，不是静默忽略）。
func wordTableNormalizerViolations(policyPath string, owner *wordPkg, t WordTable) []Violation {
	kind := t.wordTableKind()
	if kind != wordTableKindInput {
		// 「decision 类不该声明归一出口」「input 类必须声明归一出口」
		// 这两条是**声明面内部矛盾**，归 policy 自校验（恒开、提交前就拦）。
		// 执行体只判**代码事实**：归一出口在不在、导没导出、读不读那张表。
		return nil
	}
	fn := owner.resolveFunc(t.Normalizer)
	if fn == nil {
		return []Violation{{
			Rule: wordTableNormalizerRuleID,
			Path: policyPath,
			Text: `word_tables 段登记的词表 ` + t.Pkg + ` 的归一出口 ` + t.Normalizer +
				` 不存在——归一出口改名/迁走会让这条登记静默失管`,
			Kind: `word_table_decl_drift`,
		}}
	}
	if !exportedFuncName(t.Normalizer) {
		return []Violation{{
			Rule: wordTableNormalizerRuleID,
			Path: policyPath,
			Text: `word_tables 段登记的词表 ` + t.Pkg + ` 的归一出口 ` + t.Normalizer +
				` 没有导出——宿主要用它归一输入词形，未导出的函数等于没有出口`,
			Kind: `word_table_decl_drift`,
		}}
	}
	if t.Backing != "" && !bodyReferences(fn, t.Backing) {
		return []Violation{{
			Rule: wordTableNormalizerRuleID,
			Path: policyPath,
			Text: `word_tables 段登记的词表 ` + t.Pkg + ` 的归一出口 ` + t.Normalizer +
				` 没有读登记的那张表 ` + t.Backing +
				`——它归一到的是另一份清单，等于第二个真相源`,
			Kind: `word_table_decl_drift`,
		}}
	}
	return nil
}

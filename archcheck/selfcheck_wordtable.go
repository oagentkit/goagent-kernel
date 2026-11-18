// selfcheck_wordtable.go —— policy 自校验的 word_tables 段分支。
//
// # 为什么词表登记面要单独接进自校验
//
// word_tables 段的声明面会**自相矛盾**：包名写错、类别写错、归一出口挂在
// 判定词表上、同一张表登记两次……这些都不是「代码与声明不符」，而是**声明
// 自己跟自己打架**。
//
// 放在执行体里报红有两个毛病：
//
//  1. **太晚**：执行体要跑到词表门禁那一步才报，而自校验是**提交前**就该拦的；
//  2. **太窄**：自校验恒开（它是「声明面能不能信」的地基），而词表门禁段
//     可以被 policy 关掉——关掉之后，声明面里的矛盾就没人判了。
//
// ⇒ 声明面内部矛盾归**自校验**（恒开、提交前）；代码事实类（谓词还在不在、
// 出口读不读那张表）留在执行体。同一件事只由一个主人判——两处都判会
// 变成「同一件事报两次」，读者要自己判断哪条才是根因。
package archcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// wordTableSelfCheck 校验 word_tables 段的声明面。
//
// 段缺省即关闭：段被显式关掉时，它的声明面也不判（段都没有了，
// 声明面矛盾也就无从谈起）。
func wordTableSelfCheck(root string, policyPath string, seg *WordTables) []Violation {
	if seg == nil || !seg.on() {
		return nil
	}
	var out []Violation
	add := func(format string, args ...any) {
		out = append(out, Violation{
			Rule: selfCheckRuleID,
			Path: policyPath,
			Text: fmt.Sprintf(format, args...),
			Kind: "policy_self_check",
		})
	}

	// 同一张表不许登记两次：两张登记会各自派生一份取值，
	// 而它们可能指向同一张表也可能指向不同的表——前者重复判，
	// 后者则让「这张表有哪些取值」有两个答案。
	seenTable := map[string]string{}
	for _, t := range seg.Tables {
		pkg := strings.TrimSpace(t.Pkg)
		if pkg == "" {
			add("word_tables 段登记了一张表却没写 pkg：没有主人包，这张表的取值无从派生")
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(pkg))); err != nil {
			// 陈旧包根当天即红（与 modules 段同款纪律）：包改名/迁走会让
			// 这条登记静默失管，而没有任何机器会告诉你。
			add("word_tables 段登记的词表包 %q 不存在：包改名/迁走会让这条登记静默失管"+
				"（声明与事实已偏离），且这条登记连派生取值都做不到", pkg)
			continue
		}
		if t.Backing == "" && t.Enum == "" {
			add("word_tables 段登记的词表 %s 既没写 backing 也没写 enum：取值来源都没有，"+
				"这张表一条都判不了却仍计入「N 张登记词表」", pkg)
		}
		switch t.wordTableKind() {
		case wordTableKindDecision:
			if t.Normalizer != "" {
				add("word_tables 段登记的词表 %s 是判定词表（kind: decision），"+
					"不该声明归一出口（normalizer）——归一出口是输入归一词表的属性", pkg)
			}
		case wordTableKindInput:
			if t.Normalizer == "" {
				add("word_tables 段登记的词表 %s 是输入归一词表（kind: input），"+
					"必须声明归一出口（normalizer）——没有它，消费方只能自己判这个词形，"+
					"同一输入词在不同消费方得到不同结论", pkg)
			}
		default:
			// 值域必须封闭：写错的类别名若被当成「不是 decision」，
			// 这条登记就静默降级成「不判消费方」——登记了一张表却没生效，
			// 且没有任何机器能发现（失败形态是门禁变绿）。
			add("word_tables 段登记的词表 %s 的 kind %q 不是已知类别"+
				"（只有 %s / %s）——写错的类别会让这条登记静默降级成不判消费方",
				pkg, t.wordTableKind(), wordTableKindDecision, wordTableKindInput)
		}
		if t.Predicate != "" && strings.Count(t.Predicate, ".") > 1 {
			add("word_tables 段登记的词表 %s 的判定谓词 %q 形态非法："+
				"只能是包级函数名（IsKnownStatus）或「类型名.方法名」（StepStatus.Valid）",
				pkg, t.Predicate)
		}
		for _, ex := range t.FamilyExclude {
			if strings.TrimSpace(ex.Prefix) == "" {
				add("word_tables 段登记的词表 %s 的 family_exclude 有空前缀："+
					"空前缀什么都不豁免，只让清单看起来更长", pkg)
				continue
			}
			if strings.TrimSpace(ex.Reason) == "" {
				add("word_tables 段登记的词表 %s 的 family_exclude（%s）缺少理由："+
					"没有理由的豁免等于让那族常量重新变成「没人管的真相」",
					pkg, ex.Prefix)
			}
		}
		key := pkg + "|" + t.Backing + "|" + t.Family.String()
		if _, dup := seenTable[key]; dup {
			add("word_tables 段把 %s 这张表登记了两次（backing=%q family=%q）："+
				"两张登记会各自派生一份取值，而「这张表有哪些取值」只该有一个答案",
				pkg, t.Backing, t.Family)
		}
		seenTable[key] = pkg
	}

	// 同形关系声明（word_tables.relations）：四条判据，全部是**声明面内部
	// 矛盾**——它们在 policy 这一个文件里就能判出来，不该等到执行体。
	//
	//  1. 关系值域封闭：只有 same / distinct（写错的关系名会被落进
	//     「不同概念」那一档 ⇒ 手抄指路把人引向错误的常量 = 语义损坏）；
	//  2. 两端都必须指向**已登记**的表（按 pkg+enum 认表）；
	//  3. 两端必须都是**判定词表**（输入归一词表的词形是外部输入，同形是
	//     常态，声明了关系也不会生效）；
	//  4. 同一对表不许声明两次（关系只该有一个答案）。
	byIdentity := map[string]WordTable{}
	for _, t := range seg.Tables {
		if t.Enum == "" {
			continue
		}
		byIdentity[t.Pkg+"|"+t.Enum] = t
	}
	seenPair := map[string]string{}
	for _, r := range seg.Relations {
		aID := r.A.Pkg + "|" + r.A.Enum
		bID := r.B.Pkg + "|" + r.B.Enum
		if r.A.Pkg == "" || r.A.Enum == "" || r.B.Pkg == "" || r.B.Enum == "" {
			add("word_tables.relations 有条目没写全两端（pkg + enum）：不知道它说的是哪两张表")
			continue
		}
		if aID == bID {
			add("word_tables.relations 里的关系指向同一张表（%s）：一张表跟自己的关系没有意义", aID)
			continue
		}
		switch r.Relation {
		case wordTableRelationSame, wordTableRelationDistinct:
		default:
			add("word_tables.relations 里 %s 与 %s 的关系 %q 不是已知取值"+
				"（只有 %s / %s）——写错的关系名会让「不同概念」被当成「同一概念」，"+
				"手抄指路把人引向错误的常量（= 语义损坏）",
				r.A.Pkg, r.B.Enum, r.Relation, wordTableRelationSame, wordTableRelationDistinct)
			continue
		}
		at, aok := byIdentity[aID]
		bt, bok := byIdentity[bID]
		if !aok || !bok {
			add("word_tables.relations 里的关系指向了未登记的词表（%s / %s）："+
				"那张表不在 tables 段里 ⇒ 这条关系永远不生效", aID, bID)
			continue
		}
		if at.wordTableKind() != wordTableKindDecision || bt.wordTableKind() != wordTableKindDecision {
			add("word_tables.relations 里的关系（%s / %s）有一端不是判定词表："+
				"输入归一词表的词形是外部输入，同形是常态，这条关系永远不会生效", aID, bID)
			continue
		}
		key := wordTablePairKey(r.A, r.B)
		if _, dup := seenPair[key]; dup {
			add("word_tables.relations 把同一对表声明了两次（%s / %s）：关系只该有一个答案",
				aID, bID)
			continue
		}
		seenPair[key] = r.Relation
	}

	// 「有意不登记」的清单：每个条目都要写明**是什么**与**为什么**。
	//
	// 为什么要有这个清单：登记面是稀疏的，而稀疏本身没人记——
	// 「这张表我们决定不登记」这句话若只留在某个人的脑子里，
	// 下一个人会重新调研一遍，甚至直接登记它（把 40 处假阳引进门禁）。
	// 不登记也要留痕，与 exceptions 段同款纪律。
	for _, nr := range seg.NotRegistered {
		if strings.TrimSpace(nr.Subject) == "" {
			add("word_tables 段的 not_registered 有条目没写 subject：不知道它指的是哪张表")
			continue
		}
		if strings.TrimSpace(nr.Reason) == "" {
			add("word_tables 段的 not_registered 条目（%s）缺少理由："+
				"不登记也要写明为什么，否则「没人管」这件事没人记得", nr.Subject)
		}
	}
	return out
}

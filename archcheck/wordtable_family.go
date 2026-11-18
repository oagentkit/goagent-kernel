// wordtable_family.go —— 判定词表登记门禁的「族覆盖」判据（反向派生）。
//
// # 这条判据防的是什么
//
// 取值派生只有**一个方向**：表的键 → 取值。由此产生一个没人管的洞——
// 主人包**新增**一个同族常量却忘了把它加进表：
//
//   - 它不在表里 ⇒ 手抄判据判不到它（它不是「表里的取值」）；
//   - 它不在枚举出口里 ⇒ 宿主看不到它（出口读的是那张表）；
//   - 它在主人包里是个**正经的导出常量** ⇒ 谁看代码都以为它受管。
//
// ⇒ 那个新取值的消费侧从此**没人管**：主人新增了取值，消费侧还在手抄旧清单，
// 而没有任何判据会红。这正是「派生方向只有「表 → 取值」，反向没判」。
//
// # 判据（两条，都可机判）
//
//  1. **族 ⊆ 表**：族里的每个常量，其取值都必须在登记的那张表里。
//     ⇒ 「常量加了、表没加」当天即红。
//  2. **表 ⊆ 族**：表的每个键（常量名形态）都必须属于族前缀。
//     ⇒ 族前缀写错、或表里塞了别的族的键，当天即红。
//
// # 「族」怎么声明：前缀 + 豁免
//
// 族用**前缀**声明（policy 的 family 字段）：主人包里名字以它开头的、
// 取值可解析为非空字符串的常量，就是族成员。
//
// 为什么是前缀而不是「列出常量名」：列出常量名就是**第二份会漂的真相**
// ——policy 里抄一份清单，清单自己会漂，而它漂的方向恰好是「门禁判的和
// 代码跑的不是一套词表」。前缀是**形状**声明，与 backing / enum / predicate 同类。
//
// 同族里常含**另一张表**的常量（protocol 的 Decision* 同时含 decisionAll 的
// 3 个与 decisionSources 的 7 个）⇒ 族前缀判不出「哪些属于哪张表」。
// 故给一个显式豁免口（family_exclude，**按前缀**豁免一段，且必须写理由）：
// 按前缀一段排除，不必把另一张表的常量逐个列出来——逐个列就是那份清单。
//
// # 失败形态
//
// 全是**变红**：登记与事实偏离（族成员漏进表、族前缀写错、豁免已失效）
// 若被当成「没有违反」，门禁就是**静默空转**——一条都判不了却照常报绿。
package archcheck

import (
	"fmt"
	"sort"
	"strings"
)

// wordTableFamilyViolations 判「族覆盖」这条反向判据。
//
// 判定面是**主人包自己的常量声明** + **登记的那张表**——两者都是事实，
// 不需要类型信息，也不需要语义推断。
func wordTableFamilyViolations(idx *wordRepo, facts []wordTableFacts, policyPath string) []Violation {
	var out []Violation
	for i := range facts {
		f := &facts[i]
		if f.family == "" {
			// 没声明族 ⇒ 这张表不判反向覆盖。
			// 「派生不出」若被当成「已覆盖」，门禁就是静默空转——
			// 所以这里只是**不判**，不是「判它覆盖了」。
			continue
		}
		owner := idx.byRel[f.pkg]
		if owner == nil {
			// 包不存在：登记漂移已由登记面判据报出（同一件事不说两遍）。
			continue
		}
		members := wordTableFamilyMembers(idx, f)
		excluded := wordTableFamilyExcluded(f, members, policyPath, &out)

		if len(f.values) == 0 {
			// 这张表**派生不出取值**（分组形状：一张表分成若干组，取值在组内）。
			// 判据 1 比的是「取值」，没有取值可比 ⇒ 判据 1 不适用。
			//
			// 这里**不判「已覆盖」**——「判不了」若被当成「覆盖了」，门禁就是
			// 静默空转。分组形状的判定表怎么登记，写在台账「下一阶段建议」里。
			continue
		}

		// 判据 1：族 ⊆ 表（按**取值**比：表的取值集合就是它的词形集合）。
		for _, name := range sortedWordNames(members) {
			if _, ok := excluded[name]; ok {
				continue
			}
			if _, ok := f.values[members[name]]; ok {
				continue
			}
			out = append(out, Violation{
				Rule: wordTableFamilyRuleID,
				Path: policyPath,
				Text: fmt.Sprintf("常量 %s.%s（取值 %q）已声明但不在登记的词表 %s.%s 里——"+
					"它的消费侧从此没人管（不在表里 ⇒ 手抄判据判不到它；不在枚举出口里 ⇒ 宿主看不到它）："+
					"把它加进那张表，或在 policy 的 family_exclude 里写明理由",
					f.pkg, name, members[name], f.pkg, f.backing),
				Kind: "word_table_family_uncovered",
			})
		}

		// 判据 2：表 ⊆ 族（表的键必须属于族前缀）。
		for _, name := range sortedWordSet(f.keyNames) {
			if strings.HasPrefix(name, f.family) {
				continue
			}
			out = append(out, Violation{
				Rule: wordTableFamilyRuleID,
				Path: policyPath,
				Text: fmt.Sprintf("登记的词表 %s.%s 的键 %s 不属于族前缀 %s——"+
					"族声明与表不符（要么族前缀写错，要么表里塞了别的族的键）",
					f.pkg, f.backing, name, f.family),
				Kind: "word_table_family_key_outside",
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return out
}

// wordTableFamilyMembers 取一张表的族成员：主人包里名字以族前缀开头、
// 且取值可解析为**非空字符串**的常量。
//
// 为什么只认字符串常量：同名族里常混着别的概念（contract 的 StatusOpen 是
// 分类枚举 StatusClass 的取值，不是会话状态）。取值解析不出来 ⇒ 它不是
// 这个族的词形 ⇒ 不算族成员。
func wordTableFamilyMembers(idx *wordRepo, f *wordTableFacts) map[string]string {
	out := map[string]string{}
	owner := idx.byRel[f.pkg]
	if owner == nil {
		return out
	}
	for name := range owner.consts {
		if !strings.HasPrefix(name, f.family) {
			continue
		}
		v, ok := idx.resolveConst(f.pkg, name, 0)
		if !ok || v == "" {
			continue
		}
		out[name] = v
	}
	return out
}

// wordTableFamilyExcluded 算族内被豁免的成员（豁免按**前缀**一段一段）。
//
// 豁免失效即红：豁免的前缀在族里一个常量都匹配不到 ⇒ 它今天什么都没豁免，
// 却会在将来出现同名常量时无声地开始生效——与 exceptions 段「豁免必须指向
// 真实存在的东西」同款纪律。
func wordTableFamilyExcluded(f *wordTableFacts, members map[string]string, policyPath string, out *[]Violation) map[string]string {
	excluded := map[string]string{}
	for _, ex := range f.familyExclude {
		hit := 0
		for name := range members {
			if !strings.HasPrefix(name, ex.Prefix) {
				continue
			}
			excluded[name] = ex.Reason
			hit++
		}
		if hit == 0 {
			*out = append(*out, Violation{
				Rule: wordTableFamilyRuleID,
				Path: policyPath,
				Text: fmt.Sprintf("登记的词表 %s.%s 的 family_exclude 前缀 %q 在族里一个常量都匹配不到——"+
					"豁免已失效（今天什么都没豁免，将来出现同名常量时却会无声生效）："+
					"删掉这条豁免，或修正前缀",
					f.pkg, f.backing, ex.Prefix),
				Kind: "word_table_family_exclude_stale",
			})
		}
	}
	return excluded
}

// sortedWordNames 按名字排序返回 map 的键（输出顺序稳定 = 报告可复现）。
func sortedWordNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedWordSet 按名字排序返回集合的成员（输出顺序稳定 = 报告可复现）。
func sortedWordSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

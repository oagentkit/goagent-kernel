// wordtable_family_owner.go —— 族归属的派生：常量前缀归哪张表。
//
// # 为什么要有这个文件
//
// 同一族里常含**另一张表**的常量（protocol 的 Decision* 同时含 decisionAll 的
// 3 个与 decisionSources 的 7 个）⇒ 族前缀判不出「哪些属于哪张表」。
// 此前靠**手写豁免**（family_exclude）逐段排除：一族分表变多时要逐段手写，
// 而手写的那份正是「第二份会漂的真相」——它漂的方向恰好是「门禁判的和
// 代码跑的不是一套词表」。
//
// # 归属怎么派生
//
// 每张表声明自己**拥有**的常量名前缀（family 字段）；一个常量归哪张表，
// 按**最长匹配前缀**决定（同包里更具体的前缀赢）：
//
//	ConfidenceHigh     → "Confidence"     → confidenceAll
//	ConfidenceKindXxx  → "ConfidenceKind" → confidenceKinds（更具体 ⇒ 赢）
//	DecisionPending    → "Decision"       → decisionAll
//	DecisionSourceRule → "DecisionSource" → decisionSources（未登记也声明）
//
// 为什么是「最长匹配」而不是「谁先声明谁赢」：前缀之间有**包含关系**
// （Confidence ⊂ ConfidenceKind），包含关系下唯一能自洽的规则就是
// 「更具体的那个拥有它」——与 http mux 的最长匹配同一条思路。
//
// # 为什么未登记的表也要声明前缀
//
// 不登记 ≠ 没人管：那张表的常量仍会被同包另一张表的族前缀命中。若不声明
// 归属，它们会被算成「漏进表」（假红）——那正是手写豁免存在的唯一理由。
// 声明了归属，豁免就不必手写。
package archcheck

import "strings"

// wordTablePrefixOwner 一条「前缀 → 归属表」的声明。
type wordTablePrefixOwner struct {
	// prefix 是被声明的常量名前缀。
	prefix string
	// owner 是归属表的身份（pkg|enum）；未登记的表用 "not_registered:" + subject。
	owner string
}

// wordTablePackageOwners 收集一个包里所有**声明过的前缀**及其归属。
//
// 声明来自两处：登记的表（family 字段）与**未登记但声明了归属**的表
// （not_registered 段的 pkg + prefixes）。
func wordTablePackageOwners(seg WordTables, pkg string) []wordTablePrefixOwner {
	var out []wordTablePrefixOwner
	for _, t := range seg.Tables {
		if t.Pkg != pkg {
			continue
		}
		for _, p := range t.Family {
			out = append(out, wordTablePrefixOwner{prefix: p, owner: t.Pkg + "|" + t.Enum})
		}
	}
	for _, nr := range seg.NotRegistered {
		if nr.Pkg != pkg {
			continue
		}
		for _, p := range nr.Prefixes {
			out = append(out, wordTablePrefixOwner{prefix: p, owner: "not_registered:" + nr.Subject})
		}
	}
	return out
}

// wordTableOwnerOfConstant 按**最长匹配前缀**判定一个常量归哪张表。
//
// 返回 ok=false 表示这个常量**没被任何声明过的前缀命中** ⇒ 它不属于任何
// 登记词表（不是族成员）——「判不了归属」不当成「归属某张表」。
func wordTableOwnerOfConstant(name string, owners []wordTablePrefixOwner) (string, bool) {
	best := ""
	owner := ""
	for _, o := range owners {
		if !strings.HasPrefix(name, o.prefix) {
			continue
		}
		if len(o.prefix) > len(best) {
			best = o.prefix
			owner = o.owner
		}
	}
	if best == "" {
		return "", false
	}
	return owner, true
}

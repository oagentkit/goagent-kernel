// wordtable_family_owner_test.go —— 族归属派生的用例（第十四阶段）。
//
// # 这组用例守的是「族归属不是手写出来的」
//
// 同一族里常含**另一张表**的常量（protocol 的 Decision* 同时含 decisionAll
// 的 3 个与 decisionSources 的 7 个）。此前靠手写豁免（family_exclude）逐段
// 排除 ⇒ 一族分表变多时要逐段手写，而手写的那份正是「第二份会漂的真相」。
//
// ⇒ 归属改由**各表自己声明的前缀**派生，按**最长匹配前缀**决定（同包里更
// 具体的前缀赢）。本文件的第一条用例就是承重夹具：把归属派生改回「前缀
// startswith 就算族成员」，它立刻转红。
//
// # 失败形态
//
// 全是**变红**：判据若被改坏（归属没人判、废弃字段被静默忽略），本文件
// 必须转红。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableFamilyOwnerPkg 一个包里两张表，前缀互相包含（Confidence ⊂
// ConfidenceKind）：更具体的那张表拥有更具体的常量。
const wordTableFamilyOwnerPkg = `package owner

const (
	ConfidenceHigh       = "high"
	ConfidenceKindOrdinal = "ordinal"
	DecisionPending      = "pending"
	DecisionSourceRule   = "rule"
)

var confidenceAll = map[string]bool{ConfidenceHigh: true}
var confidenceKinds = map[string]bool{ConfidenceKindOrdinal: true}
var decisionAll = map[string]bool{DecisionPending: true}

func AllConfidences() []string {
	out := make([]string, 0, len(confidenceAll))
	for s := range confidenceAll {
		out = append(out, s)
	}
	return out
}

func AllConfidenceKinds() []string {
	out := make([]string, 0, len(confidenceKinds))
	for s := range confidenceKinds {
		out = append(out, s)
	}
	return out
}

func AllDecisions() []string {
	out := make([]string, 0, len(decisionAll))
	for s := range decisionAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableFamilyOwnerPolicy 造一份「同包两张表 + 前缀包含」的登记面。
func wordTableFamilyOwnerPolicy() *Policy {
	return &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "owner", Backing: "confidenceAll", Enum: "AllConfidences", Family: FamilyPrefixes{"Confidence"}},
			{Pkg: "owner", Backing: "confidenceKinds", Enum: "AllConfidenceKinds", Family: FamilyPrefixes{"ConfidenceKind"}},
			{Pkg: "owner", Backing: "decisionAll", Enum: "AllDecisions", Family: FamilyPrefixes{"Decision"}},
		},
		NotRegistered: []WordTableNotRegistered{{
			Subject:  "owner.sourceAll（判定来源）",
			Pkg:      "owner",
			Prefixes: FamilyPrefixes{"DecisionSource"},
			Reason:   "合成夹具：未登记但声明了归属前缀",
		}},
	}}
}

// TestWordTableFamilyOwnershipByLongestPrefix 前缀包含时，更具体的那张表拥有常量。
//
// 这是**承重夹具**：若归属派生退回「前缀 startswith 就算族成员」，
// ConfidenceKindOrdinal 会被算成 confidenceAll 的族成员，而它不在那张表里
// ⇒ 族覆盖判据会报「漏进表」⇒ 本用例转红。
func TestWordTableFamilyOwnershipByLongestPrefix(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{"owner/owner.go": wordTableFamilyOwnerPkg})
	vs, err := wordTableViolations(root, wordTableFamilyOwnerPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Rule == wordTableFamilyRuleID {
			t.Fatalf("归属派生后不该有族覆盖违反，实得 %v", vs)
		}
	}
}

// TestWordTableFamilyUnregisteredOwnerPrefix 未登记但声明了归属的表 ⇒ 它的常量不算登记表的族成员。
//
// 这一条守的是「不登记 ≠ 没人管」：decisionSources 没登记成判定词表（不判
// 手抄），但它的常量仍被 decisionAll 的族前缀命中——若不声明归属，就会被
// 算成「漏进表」（假红）。
func TestWordTableFamilyUnregisteredOwnerPrefix(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{"owner/owner.go": wordTableFamilyOwnerPkg})
	vs, err := wordTableViolations(root, wordTableFamilyOwnerPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Rule == wordTableFamilyRuleID && strings.Contains(v.Text, "DecisionSourceRule") {
			t.Fatalf("未登记表声明了归属前缀，它的常量不该被判成漏进表：%v", v)
		}
	}
}

// TestWordTableSelfCheckDuplicatePrefixIsRed 同包两张表声明同一前缀 ⇒ 红。
//
// 归属靠「最长匹配」分胜负 ⇒ 同一个前缀被声明两次就没有唯一归属 ⇒ 当天即红。
func TestWordTableSelfCheckDuplicatePrefixIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{"owner/owner.go": wordTableFamilyOwnerPkg})
	pol := &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "owner", Backing: "confidenceAll", Enum: "AllConfidences", Family: FamilyPrefixes{"Confidence"}},
			{Pkg: "owner", Backing: "confidenceKinds", Enum: "AllConfidenceKinds", Family: FamilyPrefixes{"Confidence"}},
		},
	}}
	vs := selfCheckViolations(root, pol)
	found := false
	for _, v := range vs {
		if v.Rule == selfCheckRuleID && strings.Contains(v.Text, "归属不唯一") {
			found = true
		}
	}
	if !found {
		t.Fatalf("同包同一前缀被两个归属声明应报自校验违反，实得 %v", vs)
	}
}

// TestWordTableSelfCheckFamilyExcludeDeprecatedIsRed 仍写 family_exclude ⇒ 红。
//
// 字段被**静默忽略**是最贵的失效形态（宿主以为豁免还在，其实归属早变了）
// ⇒ 保留字段但报红，并写明怎么改。
func TestWordTableSelfCheckFamilyExcludeDeprecatedIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{"owner/owner.go": wordTableFamilyOwnerPkg})
	pol := &Policy{WordTables: &WordTables{
		Tables: []WordTable{{
			Pkg: "owner", Backing: "confidenceAll", Enum: "AllConfidences",
			Family: FamilyPrefixes{"Confidence"},
			FamilyExclude: []WordTableFamilyExclude{{
				Prefix: "ConfidenceKind",
				Reason: "合成夹具：已废弃的豁免段",
			}},
		}},
	}}
	vs := selfCheckViolations(root, pol)
	found := false
	for _, v := range vs {
		if v.Rule == selfCheckRuleID && strings.Contains(v.Text, "已废弃") {
			found = true
		}
	}
	if !found {
		t.Fatalf("仍写 family_exclude 应报自校验违反（废弃提示），实得 %v", vs)
	}
}

// wordtable_anchor_all_test.go —— 关系锚点「all 形态」的用例（第十四阶段）。
//
// # 这组用例守的是「锚点覆盖的是不是全部同形取值」
//
// 抽样锚定只钉住一两个取值 ⇒ 其余共有取值漂了（取值改了、别名指向变了、
// 某一端删了常量）没人判。all 形态把锚定面换成「两张表的**共有取值**集合」
// ⇒ 四条判据原样复用，覆盖面却从抽样变成全覆盖。
//
// # 失败形态
//
// 全是**变红**：判据若被改坏（all 形态没真判、只报绿），本文件必须转红。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableAnchorAllOwnerA 主人包 A：表里有**两个**取值（多出来的那个不在 B 的表里）。
const wordTableAnchorAllOwnerA = `package ownerA

const (
	StatusFailed = "failed"
	StatusOther  = "other"
)

var statusAll = map[string]bool{StatusFailed: true, StatusOther: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableAnchorAllOwnerBNoConst 主人包 B：表里那个取值的键是**字面量**
// （不是常量名）⇒ 这一端没有对应常量 ⇒ all 形态下这一端锚不住它。
const wordTableAnchorAllOwnerBNoConst = `package ownerB

var statusAll = map[string]bool{"failed": true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableAnchorAllPolicy 造一份「all 形态」的登记面。
func wordTableAnchorAllPolicy(rel string) *Policy {
	return &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
			{Pkg: "ownerB", Backing: "statusAll", Enum: "AllStatuses"},
		},
		Relations: []WordTableRelation{{
			A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
			B:        WordTableRef{Pkg: "ownerB", Enum: "AllStatuses"},
			Relation: rel,
			Anchors: &WordTableAnchors{
				A: []WordTableAnchor{{All: true}},
				B: []WordTableAnchor{{All: true}},
			},
		}},
	}}
}

// wordTableAnchorAllFixture 造一份含两个主人包的合成仓（ownerB 内容可换）。
func wordTableAnchorAllFixture(t *testing.T, ownerA, ownerB string) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": ownerA,
		"ownerB/ownerB.go": ownerB,
	})
	return root
}

// TestWordTableRelationAllAnchorsAreGreen all 形态把共有取值全部锚住 ⇒ 绿。
//
// 反向验证：若 all 形态解析不出取值（静默空转），本用例会转红
// （另有用例专门钉住「该红的会红」）。
func TestWordTableRelationAllAnchorsAreGreen(t *testing.T) {
	root := wordTableAnchorAllFixture(t, wordTableAnchorAllOwnerA, wordTableAnchorOwnerB)
	pol := wordTableAnchorAllPolicy(wordTableRelationSame)
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if kinds := wordTableAnchorKinds(vs); len(kinds) != 0 {
		t.Fatalf("all 形态把共有取值都锚住了，不该有锚点违反，实得 %v", vs)
	}
}

// TestWordTableRelationAllAnchorsMissingConstantIsRed 共有取值在本端没有常量 ⇒ 红。
//
// 这一条守的是「all 覆盖的是**真的**覆盖」：某一端没有对应常量 ⇒ 那一个
// 取值上关系没人钉（取值漂出表、或它在本包里从来就不是常量）。
func TestWordTableRelationAllAnchorsMissingConstantIsRed(t *testing.T) {
	root := wordTableAnchorAllFixture(t, wordTableAnchorOwnerA, wordTableAnchorAllOwnerBNoConst)
	pol := wordTableAnchorAllPolicy(wordTableRelationSame)
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vs {
		if v.Rule == wordTableAnchorRuleID && v.Kind == "word_table_anchor_no_constant" {
			found = true
			if !strings.Contains(v.Text, "failed") {
				t.Fatalf("违规文案必须点名那个共有取值：%q", v.Text)
			}
		}
	}
	if !found {
		t.Fatalf("共有取值在 b 端没有常量应报 anchor 违反，实得 %v", vs)
	}
}

// TestWordTableRelationAllAnchorsDistinctSameDeclarationIsRed distinct 却同源 ⇒ 红。
//
// ownerB 的 StatusFailed 是 ownerA 的**别名**（同一处声明）⇒ 两端解析到同
// 一处 ⇒ 「不同概念」是说谎的（all 形态下每个共有取值都被验一遍）。
func TestWordTableRelationAllAnchorsDistinctSameDeclarationIsRed(t *testing.T) {
	root := wordTableAnchorAllFixture(t, wordTableAnchorOwnerA, wordTableAnchorOwnerB)
	pol := wordTableAnchorAllPolicy(wordTableRelationDistinct)
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range wordTableAnchorKinds(vs) {
		if k == "word_table_anchor_same_declaration" {
			found = true
		}
	}
	if !found {
		t.Fatalf("distinct 关系两端同源应报 anchor 违反，实得 %v", vs)
	}
}

// TestWordTableRelationAllAnchorsIgnoreNonSharedValues 非共有的取值不参与锚定 ⇒ 绿。
//
// all 覆盖的是「**共有**取值」，不是「全部取值」：一端多出来的取值不属于
// 这条关系（否则这条关系会变成「两张表必须完全一样」——那是另一件事）。
func TestWordTableRelationAllAnchorsIgnoreNonSharedValues(t *testing.T) {
	root := wordTableAnchorAllFixture(t, wordTableAnchorAllOwnerA, wordTableAnchorOwnerB)
	pol := wordTableAnchorAllPolicy(wordTableRelationSame)
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if kinds := wordTableAnchorKinds(vs); len(kinds) != 0 {
		t.Fatalf("非共有的取值不该被锚定判据判红，实得 %v", vs)
	}
}

// TestWordTableSelfCheckAllAnchorConflictsAreRed all 形态的三条声明面矛盾 ⇒ 红。
//
// 声明面内部矛盾归自校验（恒开、提交前就拦）：all 与 const 互斥、all 两端
// 成对、all 不与具体锚点混写。
func TestWordTableSelfCheckAllAnchorConflictsAreRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	cases := []struct {
		name    string
		anchors *WordTableAnchors
		want    string
	}{
		{"all 与 const 同时写", &WordTableAnchors{
			A: []WordTableAnchor{{All: true, Const: "StatusFailed"}},
			B: []WordTableAnchor{{All: true}},
		}, "二选一"},
		{"只一端写 all", &WordTableAnchors{
			A: []WordTableAnchor{{All: true}},
			B: []WordTableAnchor{{Pkg: "ownerB", Const: "StatusFailed"}},
		}, "两端成对"},
		{"all 与具体锚点混写", &WordTableAnchors{
			A: []WordTableAnchor{{All: true}, {Pkg: "ownerA", Const: "StatusFailed"}},
			B: []WordTableAnchor{{All: true}},
		}, "两种形态互斥"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := &Policy{WordTables: &WordTables{
				Tables: []WordTable{
					{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
					{Pkg: "ownerB", Backing: "statusAll", Enum: "AllStatuses"},
				},
				Relations: []WordTableRelation{{
					A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
					B:        WordTableRef{Pkg: "ownerB", Enum: "AllStatuses"},
					Relation: wordTableRelationSame,
					Anchors:  tc.anchors,
				}},
			}}
			vs := selfCheckViolations(root, pol)
			found := false
			for _, v := range vs {
				if v.Rule == selfCheckRuleID && strings.Contains(v.Text, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("应报自校验违反（%s），实得 %v", tc.want, vs)
			}
		})
	}
}

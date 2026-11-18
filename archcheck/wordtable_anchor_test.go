// wordtable_anchor_test.go —— 「关系锚点」判据的用例（第十三阶段）。
//
// # 这组用例守的是「声明指向的东西还对不对」
//
// 关系声明本身是**人写的事实**（「会话失败 ≠ 任务失败」是人判的）。锚点把
// 它绑到**具体常量**上，于是四条原本没人判的事实当天即红：
//
//  1. 锚点常量改名或被删（= 登记漂移）；
//  2. 锚点常量的取值不再属于它那张表（锚点说的是别的表的词）；
//  3. 两端锚定的取值不成对（关系的前提「同形」不成立）；
//  4. distinct 的两端锚点解析到**同一处声明**（「不同概念」是说谎的）。
//
// # 失败形态
//
// 全是**变红**：判据若被改坏（检查失效），本文件必须转红——
// 「锚点判据照常报绿却什么都没判」是最贵的失效形态。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableAnchorOwnerA 主人包 A：自己的常量 + 一个**不在表里**的旁证常量。
const wordTableAnchorOwnerA = `package ownerA

const (
	StatusFailed = "failed"
	StatusOther  = "other"
)

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableAnchorOwnerB 主人包 B：StatusFailed 是 ownerA 的**别名**
// （同一处声明 ⇒「同一概念」是机器证过的事实），另有一个不同取值的自有常量。
const wordTableAnchorOwnerB = `package ownerB

import "example.test/h/ownerA"

const (
	StatusFailed  = ownerA.StatusFailed
	StatusSkipped = "skipped"
)

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableAnchorPolicy 造一份登记两张同形词表 + 一条带锚点关系的 policy。
//
// aAnchor / bAnchor 传 nil 表示该端**不写**锚点。
func wordTableAnchorPolicy(rel string, aAnchor, bAnchor *WordTableAnchor) *Policy {
	r := WordTableRelation{
		A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
		B:        WordTableRef{Pkg: "ownerB", Enum: "AllStatuses"},
		Relation: rel,
	}
	if aAnchor != nil || bAnchor != nil {
		r.Anchors = &WordTableAnchors{}
		if aAnchor != nil {
			r.Anchors.A = []WordTableAnchor{*aAnchor}
		}
		if bAnchor != nil {
			r.Anchors.B = []WordTableAnchor{*bAnchor}
		}
	}
	return &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
			{Pkg: "ownerB", Backing: "statusAll", Enum: "AllStatuses"},
		},
		Relations: []WordTableRelation{r},
	}}
}

// wordTableAnchorFixture 造一份含两个主人包的合成仓。
func wordTableAnchorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableAnchorOwnerA,
		"ownerB/ownerB.go": wordTableAnchorOwnerB,
	})
	return root
}

// wordTableAnchorKinds 取锚点判据报出的 kind 集合。
func wordTableAnchorKinds(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		if v.Rule == wordTableAnchorRuleID {
			out = append(out, v.Kind)
		}
	}
	return out
}

// TestWordTableRelationAnchorMissingConstantIsRed 锚点常量改名/被删 ⇒ 红。
//
// 反向验证：若「锚点存在性」检查失效（改名不报红），本用例会转红。
func TestWordTableRelationAnchorMissingConstantIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerA", Const: "StatusFailedRenamed"},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"})
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	kinds := wordTableAnchorKinds(vs)
	found := false
	for _, k := range kinds {
		if k == "word_table_anchor_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("锚点常量不存在应报 anchor 违反，实得 %v", vs)
	}
	for _, v := range vs {
		if v.Rule != wordTableAnchorRuleID || v.Kind != "word_table_anchor_missing" {
			continue
		}
		if !strings.Contains(v.Text, "StatusFailedRenamed") {
			t.Fatalf("锚点违反文案必须点名那个不存在的常量：%q", v.Text)
		}
	}
}

// TestWordTableRelationAnchorValueNotInTableIsRed 锚点取值不在它那张表里 ⇒ 红。
//
// 这一条守的是「锚点说的是这张表的词」——锚到别的表的常量上，锚点就没有意义。
func TestWordTableRelationAnchorValueNotInTableIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerA", Const: "StatusOther"},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"})
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range wordTableAnchorKinds(vs) {
		if k == "word_table_anchor_not_member" {
			found = true
		}
	}
	if !found {
		t.Fatalf("锚点取值不在表里应报 anchor 违反，实得 %v", vs)
	}
}

// TestWordTableRelationAnchorPairsMustMatchIsRed 两端锚定取值不成对 ⇒ 红。
//
// 锚点是「按取值成对」的：a 侧锚定的每个取值，b 侧必须有同名值的锚点。
// 不成对 ⇒ 关系的前提（两张表在这个取值上同形）没被锚住。
func TestWordTableRelationAnchorPairsMustMatchIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerA", Const: "StatusFailed"},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusSkipped"})
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range wordTableAnchorKinds(vs) {
		if k == "word_table_anchor_unpaired" {
			found = true
		}
	}
	if !found {
		t.Fatalf("两端锚定取值不成对应报 anchor 违反，实得 %v", vs)
	}
}

// TestWordTableRelationDistinctAnchorSameDeclarationIsRed distinct 却锚到同一处声明 ⇒ 红。
//
// 同一处声明**不可能**是两个概念 ⇒「不同概念」是说谎的；手抄指路会要求
// 本包另起名字，而正确做法是引用主人常量。
func TestWordTableRelationDistinctAnchorSameDeclarationIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationDistinct,
		&WordTableAnchor{Pkg: "ownerA", Const: "StatusFailed"},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"})
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
		t.Fatalf("distinct 却锚到同一处声明应报 anchor 违反，实得 %v", vs)
	}
}

// TestWordTableRelationSameAnchorAliasIsGreen same + 别名锚点 ⇒ 零违反。
//
// 与上一条成对：只测「会红」不测「不该红」，等于把判据做成瞎报。
func TestWordTableRelationSameAnchorAliasIsGreen(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerA", Const: "StatusFailed"},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"})
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("same + 别名锚点应零违反，实得 %v", vs)
	}
}

// TestWordTableRelationWithoutAnchorsIsGreen 没写锚点的关系不受锚点判据管。
//
// 锚点是**可选的加固**：键是字面量、根本没有常量可锚的表写不出锚点
// （强制要求会把那种表逼成「登记不进来」⇒ 门禁逼人造假比没有锚点更坏）。
func TestWordTableRelationWithoutAnchorsIsGreen(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationDistinct, nil, nil)
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("没写锚点的关系不应触发锚点判据，实得 %v", vs)
	}
}

// TestWordTableSelfCheckAnchorWrongPackageIsRed 锚点包名与该端不符 ⇒ 红。
//
// 这是**声明面内部矛盾**（锚点与端点都在同一个 policy 文件里）⇒ 归自校验
// （恒开、提交前就拦）。
func TestWordTableSelfCheckAnchorWrongPackageIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"})
	vs := selfCheckViolations(root, pol)
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "包名与该端表的主人包") {
			found = true
		}
	}
	if !found {
		t.Fatalf("锚点包名错位应报自校验违反，实得 %v", vs)
	}
}

// TestWordTableSelfCheckAnchorEmptyConstIsRed 锚点没写 const ⇒ 红。
func TestWordTableSelfCheckAnchorEmptyConstIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerA", Const: "  "},
		&WordTableAnchor{Pkg: "ownerB", Const: "StatusFailed"})
	vs := selfCheckViolations(root, pol)
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "没写 const") {
			found = true
		}
	}
	if !found {
		t.Fatalf("锚点缺 const 应报自校验违反，实得 %v", vs)
	}
}

// TestWordTableSelfCheckAnchorDuplicateIsRed 同一端重复锚点 ⇒ 红。
func TestWordTableSelfCheckAnchorDuplicateIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame, nil, nil)
	pol.WordTables.Relations[0].Anchors = &WordTableAnchors{
		A: []WordTableAnchor{
			{Pkg: "ownerA", Const: "StatusFailed"},
			{Pkg: "ownerA", Const: "StatusFailed"},
		},
		B: []WordTableAnchor{{Pkg: "ownerB", Const: "StatusFailed"}},
	}
	vs := selfCheckViolations(root, pol)
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "重复") {
			found = true
		}
	}
	if !found {
		t.Fatalf("重复锚点应报自校验违反，实得 %v", vs)
	}
}

// TestWordTableSelfCheckAnchorOneSidedIsRed 只给一端写锚点 ⇒ 红。
//
// 半边锚点没有配对语义 ⇒ 要写就两端都写。
func TestWordTableSelfCheckAnchorOneSidedIsRed(t *testing.T) {
	root := wordTableAnchorFixture(t)
	pol := wordTableAnchorPolicy(wordTableRelationSame,
		&WordTableAnchor{Pkg: "ownerA", Const: "StatusFailed"}, nil)
	vs := selfCheckViolations(root, pol)
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "只给了一端写锚点") {
			found = true
		}
	}
	if !found {
		t.Fatalf("单边锚点应报自校验违反，实得 %v", vs)
	}
}

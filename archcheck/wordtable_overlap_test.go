// wordtable_overlap_test.go —— 「同形撞车」判据与手抄**指路**的用例
// （第十二阶段：建议③ 同形必须显式声明关系 + 指路按关系改口）。
//
// # 这组用例守的是「指路正确」
//
// 同一个拼写可能是多张判定词表的取值，而它们的**含义**可能相同也可能不同。
// 门禁若只报「这个值属两张表」却不说「你该改成哪个常量」，读者就得自己猜——
// 而猜错就是**语义损坏**（把任务失败写成会话失败）。
//
// ⇒ 关系声明为 same ⇒ 指路给候选常量；
// ⇒ 关系声明为 distinct ⇒ 指路要求「在本包另起一个名字」。
//
// # 失败形态
//
// 撞了没声明关系 ⇒ 报红（没人说得清这处字面量想说的是哪张表）；
// 声明了却没撞 ⇒ 报红（声明失真，将来一旦同形会无声生效）。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableOverlapOwnerA 主人包 A：会话状态里的 failed。
const wordTableOverlapOwnerA = `package ownerA

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableOverlapOwnerB 主人包 B：任务状态里的 failed（同形不同义）。
const wordTableOverlapOwnerB = `package ownerB

const StateFailed = "failed"

var stateAll = map[string]bool{StateFailed: true}

func AllStates() []string {
	out := make([]string, 0, len(stateAll))
	for s := range stateAll {
		out = append(out, s)
	}
	return out
}

// Normalize 归一出口（input 类必须有）：读同一张表。
func Normalize(s string) string {
	if stateAll[s] {
		return StateFailed
	}
	return ""
}
`

// wordTableOverlapOwnerC 主人包 C：另一个取值（与 A/B 不撞车）。
const wordTableOverlapOwnerC = `package ownerC

const StateDone = "done"

var stateAll = map[string]bool{StateDone: true}

func AllStates() []string {
	out := make([]string, 0, len(stateAll))
	for s := range stateAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableOverlapConsumer 消费方：手抄了 "failed"。
const wordTableOverlapConsumer = `package consumer

func f() string { return "failed" }
`

// wordTableOverlapPolicy 造一份登记两张同形词表的 policy（关系由参数给）。
func wordTableOverlapPolicy(rel string) *Policy {
	p := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
		{Pkg: "ownerB", Backing: "stateAll", Enum: "AllStates"},
	}}}
	if rel != "" {
		p.WordTables.Relations = []WordTableRelation{{
			A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
			B:        WordTableRef{Pkg: "ownerB", Enum: "AllStates"},
			Relation: rel,
		}}
	}
	return p
}

// TestWordTableCollisionWithoutRelationIsRed 两张判定词表同形却没声明关系 ⇒ 红。
//
// 反向验证：若实现不判「撞车没声明」，本用例转红。
func TestWordTableCollisionWithoutRelationIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerB/ownerB.go": wordTableOverlapOwnerB,
	})
	vs, err := wordTableViolations(root, wordTableOverlapPolicy(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableCollisionRuleID {
		t.Fatalf("同形撞车没声明关系应报 1 条撞车违反，实得 %v", vs)
	}
	for _, want := range []string{"ownerA", "ownerB", "failed"} {
		if !strings.Contains(vs[0].Text, want) {
			t.Fatalf("撞车文案必须点名两张表与同形取值（缺 %s）：%q", want, vs[0].Text)
		}
	}
}

// TestWordTableCollisionWithRelationIsGreen 声明了关系 ⇒ 不报撞车。
func TestWordTableCollisionWithRelationIsGreen(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerB/ownerB.go": wordTableOverlapOwnerB,
	})
	vs, err := wordTableViolations(root, wordTableOverlapPolicy(wordTableRelationDistinct))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("已声明关系的两张同形词表应零违反，实得 %v", vs)
	}
}

// TestWordTableRelationStaleIsRed 声明了关系却没撞车 ⇒ 报红（声明失真）。
//
// 为什么这条承重：失真的声明今天不生效，却会在将来两表一旦同形时**无声生效**
// ——那正是「门禁的失败形态是变绿」的样子。
func TestWordTableRelationStaleIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerC/ownerC.go": wordTableOverlapOwnerC,
	})
	p := &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
			{Pkg: "ownerC", Backing: "stateAll", Enum: "AllStates"},
		},
		Relations: []WordTableRelation{{
			A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
			B:        WordTableRef{Pkg: "ownerC", Enum: "AllStates"},
			Relation: wordTableRelationDistinct,
		}},
	}}
	vs, err := wordTableViolations(root, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableCollisionRuleID {
		t.Fatalf("声明了关系却没撞车应报 1 条撞车违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "没有交集") {
		t.Fatalf("撞车文案必须说清是「声明失真」：%q", vs[0].Text)
	}
}

// TestWordTableDistinctRelationGuidanceRequiresLocalConst distinct ⇒ 指路要求本包命名。
//
// 这条是「错误指路比不指路更坏」的守门人：不同概念时，把人引向其中一张表的
// 常量 = 让人把概念改错。
func TestWordTableDistinctRelationGuidanceRequiresLocalConst(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go":     wordTableOverlapOwnerA,
		"ownerB/ownerB.go":     wordTableOverlapOwnerB,
		"consumer/consumer.go": wordTableOverlapConsumer,
	})
	vs, err := wordTableViolations(root, wordTableOverlapPolicy(wordTableRelationDistinct))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("distinct 关系下手抄应报 1 条手抄违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "在本包把它声明成自己的常量") {
		t.Fatalf("distinct 关系的指路必须要求在本包命名：%q", vs[0].Text)
	}
	if strings.Contains(vs[0].Text, "改用 ownerA.StatusFailed") ||
		strings.Contains(vs[0].Text, "改用它") {
		t.Fatalf("distinct 关系下不得把人引向某一张表的常量（错误指路）：%q", vs[0].Text)
	}
}

// TestWordTableSameRelationGuidanceListsCandidates same ⇒ 指路给候选常量。
//
// 同一概念（一张表的别名/子集）时，引用任一家都对 ⇒ 指路必须给全部候选。
func TestWordTableSameRelationGuidanceListsCandidates(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go":     wordTableOverlapOwnerA,
		"ownerB/ownerB.go":     wordTableOverlapOwnerB,
		"consumer/consumer.go": wordTableOverlapConsumer,
	})
	vs, err := wordTableViolations(root, wordTableOverlapPolicy(wordTableRelationSame))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("same 关系下手抄应报 1 条手抄违反，实得 %v", vs)
	}
	for _, want := range []string{"ownerA.StatusFailed", "ownerB.StateFailed"} {
		if !strings.Contains(vs[0].Text, want) {
			t.Fatalf("same 关系的指路必须给出全部候选常量（缺 %s）：%q", want, vs[0].Text)
		}
	}
	if strings.Contains(vs[0].Text, "在本包把它声明成自己的常量") {
		t.Fatalf("same 关系（同概念）下不该要求另起名字：%q", vs[0].Text)
	}
}

// TestWordTableCollisionIgnoresInputTables 输入词表不参与撞车判定。
//
// 为什么：输入归一词表的词形是**外部喂进来的**（healthy/down/正常/宕机……），
// 消费方自由使用 ⇒ 同形是常态，不是缺陷。若把 input 表拉进撞车判定，
// 门禁会对着外部输入的通用词报红——判红到没人信它，它就等于没有。
func TestWordTableCollisionIgnoresInputTables(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerB/ownerB.go": wordTableOverlapOwnerB,
	})
	p := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
		{
			Pkg: "ownerB", Backing: "stateAll", Enum: "AllStates",
			Kind: wordTableKindInput, Normalizer: "Normalize",
		},
	}}}
	vs, err := wordTableViolations(root, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Rule == wordTableCollisionRuleID {
			t.Fatalf("输入词表不参与撞车判定（它的词形是外部输入，同形是常态）：%v", vs)
		}
	}
}

// TestWordTableSelfCheckRelationValueDomainIsRed 关系值域必须封闭。
//
// 写错的关系名若被当成「不是 same」，就会落进「不同概念」那一档——
// 而那一档的代价是每个消费方都要多抄一份名字。
func TestWordTableSelfCheckRelationValueDomainIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerB/ownerB.go": wordTableOverlapOwnerB,
	})
	vs := selfCheckViolations(root, wordTableOverlapPolicy("same-ish"))
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("关系值域不封闭应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "same-ish") {
		t.Fatalf("自校验文案必须点名写错的关系值：%q", vs[0].Text)
	}
}

// TestWordTableSelfCheckRelationUnknownTableIsRed 关系指向未登记的表 ⇒ 红。
//
// 那张表不在 tables 段里 ⇒ 这条关系永远不生效——而声明面失效必须是变红。
func TestWordTableSelfCheckRelationUnknownTableIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
	})
	p := &Policy{WordTables: &WordTables{
		Tables: []WordTable{{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"}},
		Relations: []WordTableRelation{{
			A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
			B:        WordTableRef{Pkg: "ownerB", Enum: "AllStates"},
			Relation: wordTableRelationDistinct,
		}},
	}}
	vs := selfCheckViolations(root, p)
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("关系指向未登记表应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "ownerB") {
		t.Fatalf("自校验文案必须点名未登记的那张表：%q", vs[0].Text)
	}
}

// TestWordTableSelfCheckRelationInputEndpointIsRed 关系端点是输入词表 ⇒ 红。
//
// 输入归一词表的词形是外部输入，同形是常态 ⇒ 这条关系永远不会生效。
func TestWordTableSelfCheckRelationInputEndpointIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerB/ownerB.go": wordTableOverlapOwnerB,
	})
	p := &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
			{
				Pkg: "ownerB", Backing: "stateAll", Enum: "AllStates",
				Kind: wordTableKindInput, Normalizer: "Normalize",
			},
		},
		Relations: []WordTableRelation{{
			A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
			B:        WordTableRef{Pkg: "ownerB", Enum: "AllStates"},
			Relation: wordTableRelationDistinct,
		}},
	}}
	vs := selfCheckViolations(root, p)
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("关系端点是输入词表应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "判定词表") {
		t.Fatalf("自校验文案必须说清端点不是判定词表：%q", vs[0].Text)
	}
}

// TestWordTableSelfCheckRelationDuplicatePairIsRed 同一对表声明两次 ⇒ 红。
//
// 关系只该有一个答案：两次声明而值不同 ⇒ 第三种状态（既不是 same 也不是
// distinct）没人判。
func TestWordTableSelfCheckRelationDuplicatePairIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableOverlapOwnerA,
		"ownerB/ownerB.go": wordTableOverlapOwnerB,
	})
	p := &Policy{WordTables: &WordTables{
		Tables: []WordTable{
			{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
			{Pkg: "ownerB", Backing: "stateAll", Enum: "AllStates"},
		},
		Relations: []WordTableRelation{
			{
				A:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
				B:        WordTableRef{Pkg: "ownerB", Enum: "AllStates"},
				Relation: wordTableRelationSame,
			},
			{
				A:        WordTableRef{Pkg: "ownerB", Enum: "AllStates"},
				B:        WordTableRef{Pkg: "ownerA", Enum: "AllStatuses"},
				Relation: wordTableRelationDistinct,
			},
		},
	}}
	vs := selfCheckViolations(root, p)
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("同一对表声明两次应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "两次") {
		t.Fatalf("自校验文案必须说清是重复声明：%q", vs[0].Text)
	}
}

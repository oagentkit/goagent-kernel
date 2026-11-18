// wordtable_shape_test.go —— 词表**载体形状**与**族前缀形态**的用例
// （第十二阶段：建议① 常量切片可登记 / 建议② 族支持多个前缀）。
//
// # 为什么形状要拆
//
// 切片承载的词表有两种形态，此前被混为一谈：
//
//   - 元素是**常量名**（[]Action{ActionForce, ...}）⇒ **扁平表**，取值可派生，
//     与 map 形状（键→取值）等价 ⇒ 可以登记成判定词表（第十二阶段新增能力）；
//   - 元素是**复合字面量**（[]struct{...}{{...}}）⇒ **分组表**，取值在组内，
//     顶层派生不出 ⇒ 只能当输入归一词表。
//
// 混为一谈的代价是**实测**出来的：controlplane 的介入动作表（常量切片）
// 被误判成「分组表」而登记不进来——一张有判定面（IsKnownInterventionAction）
// 的词表，从此没有门禁管它的消费侧。
//
// # 为什么族前缀要支持多个
//
// 一族的常量**不一定**共享同一个前缀（StatusXxx 与 PhaseXxx 同族）。
// 单前缀会漏掉一族的一半 ⇒ 漏掉的那半永远没人管（它不在表里 ⇒ 手抄判据
// 判不到它；它不在枚举出口里 ⇒ 宿主看不到它）。
package archcheck

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// wordTableConstSliceOwnerPkg 合成仓的主人包：一张**常量切片**词表。
//
// 形状：var actions = []Action{ActionForce, ActionAdjudicate}
// —— 切片元素是**常量名** ⇒ 这是**扁平表**（取值可派生），不是分组表。
const wordTableConstSliceOwnerPkg = `package owner

type Action string

const (
	ActionForce      Action = "force"
	ActionAdjudicate Action = "adjudicate"
)

// actions 介入动作词表（常量切片形状）。
var actions = []Action{ActionForce, ActionAdjudicate}

func AllActions() []Action {
	return actions
}

// IsKnownAction 判定谓词：读同一张表。
func IsKnownAction(a Action) bool {
	for _, x := range actions {
		if x == a {
			return true
		}
	}
	return false
}
`

// wordTableMultiPrefixOwnerPkg 合成仓的主人包：一族常量跨**两个前缀**
// （Status* 与 Phase*），表里只收了 StatusOK。
const wordTableMultiPrefixOwnerPkg = `package owner

const (
	StatusOK   = "ok"
	PhaseReady = "ready"
)

// statusAll 判定用的那张表（键是常量名，取值在常量声明里）。
var statusAll = map[string]bool{
	StatusOK: true,
}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// TestWordTableConstSliceBackingIsRegistrable 常量切片能登记成判定词表。
//
// 反向验证：若执行体把「切片」一律当分组表（第十二阶段之前的形态），
// 本用例会转红——它报的是「分组形状…请写 kind: input」那条漂移。
func TestWordTableConstSliceBackingIsRegistrable(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableConstSliceOwnerPkg,
	})
	pol := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{Pkg: "owner", Backing: "actions", Enum: "AllActions", Predicate: "IsKnownAction"},
	}}}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("常量切片形状的判定词表应零违反（它与 map 形状等价），实得 %v", vs)
	}
}

// TestWordTableConstSliceBackingCatchesHandCopy 常量切片的取值同样受手抄判据管。
//
// 这条守的是「登记了不等于只登记一半」：派生能出取值，消费侧才被判得到。
func TestWordTableConstSliceBackingCatchesHandCopy(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableConstSliceOwnerPkg,
		"consumer/consumer.go": `package consumer

func f() string { return "force" }
`,
	})
	pol := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{Pkg: "owner", Backing: "actions", Enum: "AllActions", Predicate: "IsKnownAction"},
	}}}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("常量切片的取值被手抄应报 1 条手抄违反，实得 %v", vs)
	}
}

// TestWordTableFamilyMultiplePrefixes 族跨两个前缀时两个前缀都算族成员。
//
// 反向验证：若实现只认**第一个**前缀，PhaseReady 就不是族成员 ⇒ 判据 1
// （族 ⊆ 表）不触发 ⇒ 本用例转红。
func TestWordTableFamilyMultiplePrefixes(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableMultiPrefixOwnerPkg,
	})
	pol := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{
			Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses",
			Family: FamilyPrefixes{"Status", "Phase"},
		},
	}}}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableFamilyRuleID {
		t.Fatalf("族跨两个前缀时，PhaseReady 未进表应报 1 条族违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "PhaseReady") {
		t.Fatalf("族违反文案必须点名漏掉的常量：%q", vs[0].Text)
	}
}

// TestWordTableFamilySinglePrefixIgnoresOtherGroup 单前缀时另一个前缀不算族成员。
//
// 与上一条成对：只测「会红」不测「不该红」，等于把判据做成瞎报。
func TestWordTableFamilySinglePrefixIgnoresOtherGroup(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableMultiPrefixOwnerPkg,
	})
	pol := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{
			Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses",
			Family: FamilyPrefixes{"Status"},
		},
	}}}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("族只声明 Status 前缀时，PhaseReady 不算族成员，应零违反，实得 %v", vs)
	}
}

// TestWordTableFamilyAcceptsStringAndList family 字段接受字符串与列表两种形态。
//
// 为什么承重：policy 是**声明面**——声明面多一种写法，就多一种「宿主写不出来
// 只能改执行体」的风险。单前缀是常态，列表是「一族跨了两个前缀」时的写法。
func TestWordTableFamilyAcceptsStringAndList(t *testing.T) {
	var seg struct {
		Tables []WordTable `yaml:"tables"`
	}
	src := "tables:\n" +
		"  - pkg: p\n" +
		"    family: Status\n" +
		"  - pkg: q\n" +
		"    family: [Status, Phase]\n"
	if err := yaml.Unmarshal([]byte(src), &seg); err != nil {
		t.Fatal(err)
	}
	if len(seg.Tables) != 2 {
		t.Fatalf("应解析出 2 张表，实得 %d", len(seg.Tables))
	}
	if got := seg.Tables[0].Family; len(got) != 1 || got[0] != "Status" {
		t.Fatalf("字符串形态应解析成单前缀，实得 %v", got)
	}
	if got := seg.Tables[1].Family; len(got) != 2 || got[0] != "Status" || got[1] != "Phase" {
		t.Fatalf("列表形态应解析成两个前缀，实得 %v", got)
	}
}

// TestWordTableFamilyBadYAMLFormIsRed family 写成别的形态必须报错。
//
// 失败形态必须是**响亮失败**：写错形态若被当成「没声明族」，这条登记就静默
// 降级成「不判反向覆盖」——而门禁照常报绿。
func TestWordTableFamilyBadYAMLFormIsRed(t *testing.T) {
	var seg struct {
		Tables []WordTable `yaml:"tables"`
	}
	src := "tables:\n  - pkg: p\n    family: {a: b}\n"
	if err := yaml.Unmarshal([]byte(src), &seg); err == nil {
		t.Fatal("family 写成映射形态应报错（它只能是字符串或字符串列表）")
	}
}

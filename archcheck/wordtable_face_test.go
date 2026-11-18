// wordtable_face_test.go —— 词表登记门禁「函数面」判据的用例（谓词 / 归一出口）。
//
// # 为什么单独成文件
//
// 这两条判据守的是**登记面与代码事实的一致性**：登记了判定谓词就必须还在、
// 必须读那张表；输入归一词表必须有导出的归一出口。
// 它们的失败形态都是**静默失管**（登记漂移却没人发现）——故每种漂移各一条
// 「必须红」的用例，且每条放行（没声明就不判）各有一条「必须绿」的用例。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableFacePolicy 造一份登记一张词表、带函数面的 policy。
func wordTableFacePolicy(t WordTable) *Policy {
	return &Policy{WordTables: &WordTables{Tables: []WordTable{t}}}
}

// wordTableOwnerWithPredicate 主人包：一张表 + 一个读它的判定谓词。
const wordTableOwnerWithPredicate = `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

// AllStatuses 导出枚举出口：读同一张表。
func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}

// IsKnown 判定谓词：读**同一张**表。
func IsKnown(s string) bool {
	_, ok := statusAll[s]
	return ok
}
`

// TestWordTablePredicateRegisteredAndConsistent 登记的谓词与代码一致必须绿。
//
// 反向验证：若谓词面判定被改坏（不再校验存在性/读表），
// 本用例**不会**变红——所以它守的是「合法登记不被误判」，
// 真正的承重在下面两条「必须红」的用例。
func TestWordTablePredicateRegisteredAndConsistent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerWithPredicate,
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Predicate: "IsKnown",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("谓词与代码一致时应零违规，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTablePredicateMissingIsRed 登记的谓词不存在必须红。
//
// 判定面改名/迁走 ⇒ 登记静默失管（没人再判它），失败形态必须是变红。
func TestWordTablePredicateMissingIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerWithPredicate,
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Predicate: "IsGone",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTablePredicateRuleID {
		t.Fatalf("谓词不存在应报 1 条 predicate 规则违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "IsGone") {
		t.Fatalf("违规文案必须点名缺失的谓词：%q", vs[0].Text)
	}
}

// TestWordTablePredicateReadingAnotherTableIsRed 谓词读别的表必须红。
//
// 谓词另抄一份清单 ⇒ 判定与词表分叉：同一个状态在词表里合法、在判定里非法。
func TestWordTablePredicateReadingAnotherTableIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

// another 另一份清单——谓词读的是它，不是登记的那张表。
var another = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}

func IsKnown(s string) bool {
	_, ok := another[s]
	return ok
}
`,
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Predicate: "IsKnown",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTablePredicateRuleID {
		t.Fatalf("谓词读错表应报 1 条 predicate 规则违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "statusAll") {
		t.Fatalf("违规文案必须点名登记的那张表：%q", vs[0].Text)
	}
}

// TestWordTablePredicateMethodFormResolves 方法形态的谓词必须能解析。
//
// 谓词常是方法（StepStatus.Valid / State.Valid），policy 的声明形态是
// 「类型名.方法名」。反向验证：若方法面解析被改坏（只认包级函数），
// 本用例立刻转红。
func TestWordTablePredicateMethodFormResolves(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}

// StepStatus 环节状态（方法形态的判定谓词）。
type StepStatus string

// Valid 判定：读同一张表。
func (s StepStatus) Valid() bool {
	return statusAll[string(s)]
}
`,
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Predicate: "StepStatus.Valid",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("方法形态的谓词应能解析（零违规），实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableInputWithoutNormalizerIsRed 输入归一词表没有归一出口必须红。
//
// 没有归一出口，消费方只能自己判这个词形 ⇒ 同一个输入词在不同消费方
// 得到不同结论。这正是「输入归一词表」这个类别必须有的出口。
func TestWordTableInputWithoutNormalizerIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerWithPredicate,
	})
	// 「input 类必须声明归一出口」是**声明面内部矛盾** ⇒ 归 policy 自校验
	// （恒开、提交前就拦），不在词表执行体里判。
	vs := selfCheckViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Kind: "input", Backing: "statusAll", Enum: "AllStatuses",
	}))
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("input 类缺归一出口应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "normalizer") {
		t.Fatalf("违规文案必须点名要补的字段：%q", vs[0].Text)
	}
}

// TestWordTableNormalizerMustBeExported 未导出的归一出口必须红。
//
// 宿主要用它归一输入词形——未导出的函数等于没有出口。
func TestWordTableNormalizerMustBeExported(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}

// normalize 归一出口：未导出 ⇒ 宿主够不着。
func normalize(s string) string {
	if statusAll[s] {
		return "ok"
	}
	return ""
}
`,
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Kind: "input", Backing: "statusAll", Enum: "AllStatuses", Normalizer: "normalize",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableNormalizerRuleID {
		t.Fatalf("未导出的归一出口应报 1 条 normalizer 规则违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "导出") {
		t.Fatalf("违规文案必须点明「没有导出」：%q", vs[0].Text)
	}
}

// TestWordTableNormalizerReadingAnotherTableIsRed 归一出口读别的表必须红。
//
// 它归一到的是另一份清单 ⇒ 同一个输入词在不同档位有两个答案。
func TestWordTableNormalizerReadingAnotherTableIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

var another = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}

// Normalize 归一出口：读的是另一份清单。
func Normalize(s string) string {
	if another[s] {
		return "ok"
	}
	return ""
}
`,
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Kind: "input", Backing: "statusAll", Enum: "AllStatuses", Normalizer: "Normalize",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableNormalizerRuleID {
		t.Fatalf("归一出口读错表应报 1 条 normalizer 规则违反，实得 %v", vs)
	}
}

// TestWordTableDecisionWithNormalizerIsRed 判定词表声明归一出口必须红。
//
// 归一出口是**输入归一词表**的属性；判定词表留着它是无用声明——
// 而无用声明要么删、要么报错（门禁的失败形态是变红，不是静默忽略）。
func TestWordTableDecisionWithNormalizerIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerWithPredicate,
	})
	// 「decision 类不该声明归一出口」同样是**声明面内部矛盾** ⇒ 归自校验。
	vs := selfCheckViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Normalizer: "IsKnown",
	}))
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("判定词表声明归一出口应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "normalizer") {
		t.Fatalf("违规文案必须点名写错的字段：%q", vs[0].Text)
	}
}

// TestWordTableFaceSkipsMissingPackage 包不存在时函数面不重复报。
//
// 陈旧登记（包改名）已由登记面报红；同一个事实不报两次——
// 重复报会让报告里的条目数与真因对不上。
func TestWordTableFaceSkipsMissingPackage(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"other/other.go": "package other\n",
	})
	vs, err := wordTableViolations(root, wordTableFacePolicy(WordTable{
		Pkg: "ghost", Backing: "statusAll", Enum: "AllStatuses", Predicate: "IsKnown",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Rule == wordTablePredicateRuleID || v.Rule == wordTableNormalizerRuleID {
			t.Fatalf("包不存在时函数面不该重复报：%v", vs)
		}
	}
}

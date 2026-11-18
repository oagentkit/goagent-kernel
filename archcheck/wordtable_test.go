// wordtable_test.go —— 判定词表登记门禁的用例（含反向验证）。
//
// 为什么这条门禁的用例要这么写：它判的是「别人写不写得对」，而它自己的
// 三处最容易**静默失配**——
//
//  1. 派生不出取值时若被当成「没有手抄」，门禁就是**空转**（一条都判不了
//     却照常报绿）。故跨包别名（statemachine 形态）必须有专门用例；
//  2. 「主人包自己」「本包自有常量」「引用主人常量」三条放行臂若被改坏，
//     门禁会立刻变成假红（合法用法被报红）——而假红到没人信它，它就等于没有。
//     故每条放行臂各有一条「必须零违规」的用例；
//  3. 登记与事实偏离（包改名 / 表改名 / 枚举出口读错表）若被静默跳过，
//     这条登记就变成死条文——故每种偏离各有一条「必须红」的用例。
package archcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wordTableOwnerPkg 合成仓的主人包：一张词表 + 一个读同一张表的枚举出口。
const wordTableOwnerPkg = `package owner

const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// statusAll 判定用的那张表（键是常量名，取值在常量声明里）。
var statusAll = map[string]bool{
	StatusOK:     true,
	StatusFailed: true,
}

// AllStatuses 导出枚举出口：读同一张表，不另抄清单。
func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}

// Normalize 归一出口（input 类必须有）：读同一张表。
//
// 注意它返回的是**常量** StatusOK 而不是字面量 "ok"——主人包自己也该用名字：
// 词表门禁对主人包与消费方一视同仁（否则「主人随手写字面量」就没人管了）。
func Normalize(s string) string {
	if statusAll[s] {
		return StatusOK
	}
	return ""
}
`

// wordTablePolicy 造一份只登记一张词表的 policy。
func wordTablePolicy() *Policy {
	return &Policy{
		WordTables: &WordTables{
			Tables: []WordTable{
				{Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses"},
			},
		},
	}
}

// writeWordTableRepo 造一个最小仓：主人包 + 消费包。
func writeWordTableRepo(t *testing.T, consumer string) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go":       wordTableOwnerPkg,
		"consumer/consumer.go": consumer,
	})
	return root
}

// TestWordTableHandCopyIsReported 手抄登记词表的取值必须被报出来。
//
// 这是判据的**主路径**：没有它，整条门禁什么都不会判。
func TestWordTableHandCopyIsReported(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

func f() string { return "failed" }
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("手抄一处应报 1 条，实得 %d 条：%v", len(vs), vs)
	}
	if vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("规则 id 实得 %q，期望 %q", vs[0].Rule, wordTableHandCopyRuleID)
	}
	if !strings.Contains(vs[0].Text, "failed") {
		t.Fatalf("违规文案必须点名取值：%q", vs[0].Text)
	}
}

// TestWordTableOwnConstantIsExempt 本包常量**定义处**的字面量必须放行。
//
// 反向验证：若「命名处」这条放行臂被改坏（不再认 const 声明处的字面量），
// 本用例立刻转红——它守的是「合法用法不被判红」。
//
// 豁免的粒度是**那一处定义**，不是整个包：同包另一处再写裸字面量仍要红
// （见 TestWordTableLiteralAfterOwnConstantIsRed）。
func TestWordTableOwnConstantIsExempt(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

const localFailed = "failed"

func f() string { return localFailed }
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("本包自有常量应放行，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableOwnerReferenceIsExempt 引用主人常量必须放行。
//
// 「引用主人常量」这条放行臂是**结构性**的：限定选择器（owner.Const）
// 本来就不是字面量，因而不可能被当成手抄——它不需要单独分支。
// 但「限定引用不被当成字面量」这件事必须被钉住：哪天采集器顺手把
// 选择器的取值也收进来，门禁就会开始误报合法引用。
//
// 反向验证：把采集器改成「连限定引用一起收」，本用例立刻转红。
func TestWordTableOwnerReferenceIsExempt(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

import "example.test/h/owner"

func f() string { return owner.StatusFailed }
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("引用主人常量应放行，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableOwnerPackageIsExempt 主人包自己用自己的词不是手抄。
func TestWordTableOwnerPackageIsExempt(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableOwnerPkg,
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("主人包应豁免，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableStructTagAndImportPathAreNotValues 结构体标签与 import 路径
// 不是「取值」，不得算成手抄。
//
// 排除这两者的理由：它们是**键名**——yaml:"failed" 里的 "failed" 与某个
// 词表取值拼写相同，但它不是那个概念；把它算进手抄，判据就成了拼写匹配器。
func TestWordTableStructTagAndImportPathAreNotValues(t *testing.T) {
	// 夹具里标签与 import 路径的**取值本身就是 "failed"**——
	// 否则这条用例不承重：排除失效时多收的那两个字面量拼写不同，
	// 判据照样判不出手抄，用例照样全绿（而门禁已经失守了）。
	root := writeWordTableRepo(t, "package consumer\n\nimport _ \"failed\"\n\ntype S struct {\n\tState string \"failed\"\n}\n")
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("结构体标签与 import 路径不应算手抄，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableOwnerLiteralKeyIsExempt 主人的表用**字面量键**时，
// 主人包自己不得被判手抄。
//
// 为什么需要这条：有的词表把取值直接写成 map 的键（protocol 的图表词表
// 就是这个形状）。此时主人包里的那些字面量**就是这张表的定义**，不是手抄。
//
// 这条用例守着「主人表**定义处**豁免」这条放行臂：若它被改坏，主人包会
// 开始报自己的定义（假红）。反过来，若没有这条夹具，这条放行臂就是
// **不承重的**——主人包的取值恰好也是字面量常量时，另一条臂（命名处）
// 会顺手兜住它，于是「这条臂坏了」这件事永远不会被用例发现。
func TestWordTableOwnerLiteralKeyIsExempt(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

var statusAll = map[string]bool{"failed": true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`,
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("主人包的字面量键不应算手抄，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableMissingEnumIsRed 登记的词表没有导出枚举出口必须红。
//
// 出口是「这张表有哪些值」的唯一对外答案；缺了它，宿主只能手抄清单。
func TestWordTableMissingEnumIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}
`,
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableEnumRuleID {
		t.Fatalf("缺枚举出口应报 1 条 enum 规则违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "AllStatuses") {
		t.Fatalf("违规文案必须点名缺失的出口：%q", vs[0].Text)
	}
}

// TestWordTableEnumReadingAnotherTableIsRed 枚举出口读的是**另一张表**必须红。
//
// 读别的 map 就等于第二个真相源：出口给出的清单与判定用的表会漂移，
// 而下游会信出口那份——比没有出口更糟。
func TestWordTableEnumReadingAnotherTableIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

// another 另一份清单（枚举出口读的是它，不是登记的那张表）。
var another = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(another))
	for s := range another {
		out = append(out, s)
	}
	return out
}
`,
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableEnumRuleID {
		t.Fatalf("枚举读错表应报 1 条 enum 规则违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "statusAll") {
		t.Fatalf("违规文案必须点名登记的那张表：%q", vs[0].Text)
	}
}

// TestWordTableStaleRegistrationIsRed 登记的包不存在必须红。
//
// 包改名/迁走会让这条登记**静默失管**（没人再判它），而失败形态必须是变红。
func TestWordTableStaleRegistrationIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"other/other.go": "package other\n",
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableEnumRuleID {
		t.Fatalf("陈旧登记应报 1 条 enum 规则违反，实得 %v", vs)
	}
	if vs[0].Kind != "word_table_decl_drift" {
		t.Fatalf("陈旧登记的违规形态实得 %q，期望 word_table_decl_drift", vs[0].Kind)
	}
}

// TestWordTableCrossPackageAliasResolves 主人的常量是**跨包别名**时，
// 取值必须仍能派生出来。
//
// 反向验证：statemachine 的环节状态常量就是 contract 的别名
// （StatusCreated = contract.StatusCreated）。若解析只在本包进行，
// 那两张表会派生不出任何取值——而「派生不出」若被当成「没有手抄」，
// 门禁就是**静默空转**：一条都判不了却照常报绿。
func TestWordTableCrossPackageAliasResolves(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"base/base.go": `package base

const StatusFailed = "failed"
`,
		"owner/owner.go": `package owner

import "example.test/h/base"

const StatusFailed = base.StatusFailed

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`,
		"consumer/consumer.go": `package consumer

func f() string { return "failed" }
`,
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("跨包别名下仍须判出手抄，实得 %v", vs)
	}
}

// TestWordTableSegmentDisabledIsNoop 段缺省或显式关闭时不判。
func TestWordTableSegmentDisabledIsNoop(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

func f() string { return "failed" }
`)
	pol := &Policy{}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("段缺省时应不判，实得 %d 条：%v", len(vs), vs)
	}
	off := false
	pol.WordTables = &WordTables{Enabled: &off}
	vs, err = wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("段显式关闭时应不判，实得 %d 条：%v", len(vs), vs)
	}
}

// TestRepoWordTablesHaveNoViolations 本仓自己的 word_tables 段必须零违规。
//
// 这是「登记了就得守住」的落地：CI 跑门禁时也会跑这一条，故本仓自己
// 手抄了登记词表的取值会当场变红。
func TestRepoWordTablesHaveNoViolations(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("本仓未找到 policy 文件（跳过）：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "architecture-policy.yaml")); err != nil {
		t.Skip("本仓未找到 architecture-policy.yaml")
	}
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) > 0 {
		t.Fatalf("本仓词表登记面有 %d 条违反：%v", len(vs), vs)
	}
}

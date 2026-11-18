// wordtable_scan_test.go —— 判定词表门禁「出现点级」判据与两类词表（kind）的用例。
//
// # 为什么单独成文件
//
// 这一组用例守的是两件事：
//
//  1. **出现点级**判据：豁免精确到「那一处字面量」，而不是「整个包」；
//  2. **kind 两类词表**：判定词表判消费方，输入归一词表不判消费方
//     但仍判「出口与表同源」。
//
// 两件事的失败形态都是「门禁变绿」，而变绿最贵的形态是**看不出哪里没判**：
// 判据写错了却报绿，读报告的人根本不会知道有东西没被判到。
// 故每条放行臂与每条判据各有一条用例，且用例数量与判据数量对得上。
package archcheck

import (
	"strings"
	"testing"
)

// wordTablePolicyKind 造一份登记一张词表、指定类别的 policy。
//
// input 类会补上归一出口（Normalize）：输入归一词表**必须**有它——
// 没有它消费方只能自己判这个词形（那是另一条判据要拦的事）。
func wordTablePolicyKind(kind string) *Policy {
	t := WordTable{Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Kind: kind}
	if kind == wordTableKindInput {
		t.Normalizer = "Normalize"
	}
	return &Policy{
		WordTables: &WordTables{
			Tables: []WordTable{t},
		},
	}
}

// wordTableGroupedOwnerPkg 合成仓的主人包：一张**分组**词表。
//
// 形状差异不是装饰：protocol 的图表词表就是分组形状（三档 × 若干词形），
// 而取值派生只认扁平表——故分组形状必须显式写成 kind: input，
// 否则门禁得能说清「你该改 kind」而不是含糊地报「派生不出取值」。
//
// ⚠️ 夹具必须是**真正的分组形状**（切片元素是复合字面量）。第十二阶段实测：
// 切片有两种形状——元素是**常量名**（[]string{wordFailed, wordOK}）时那是
// **扁平表**，取值可派生、可以登记成判定词表；元素是**复合字面量**时才是
// 分组表。夹具写错形状 ⇒ 用例测的其实是另一条路径 ⇒ 门禁的覆盖出现盲区。
const wordTableGroupedOwnerPkg = `package owner

var statusAll = []struct {
	name  string
	words []string
}{
	{name: "failed", words: []string{"failed", "fatal"}},
	{name: "ok", words: []string{"ok"}},
}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for _, g := range statusAll {
		out = append(out, g.words...)
	}
	return out
}

// Normalize 归一出口（input 类必须有）：读同一张表。
func Normalize(s string) string {
	for _, g := range statusAll {
		for _, w := range g.words {
			if w == s {
				return g.name
			}
		}
	}
	return ""
}
`

// TestWordTableLiteralAfterOwnConstantIsRed 命名过了却还写字面量必须红。
//
// 这是**出现点级**判据的主路径：同一个包里既定义了常量、又写了同一取值的
// 裸字面量，后者判红——「命名过了却还写字面量」比「压根没命名」更该改，
// 因为名字就在眼前却不用。
//
// 反向验证：若豁免退回**包级**（整个包因为有同名常量而放过），
// 本用例立刻转红。
func TestWordTableLiteralAfterOwnConstantIsRed(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

const localFailed = "failed"

func f() string { return "failed" }
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("命名过却仍写字面量应报 1 条，实得 %d 条：%v", len(vs), vs)
	}
	if vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("规则 id 实得 %q，期望 %q", vs[0].Rule, wordTableHandCopyRuleID)
	}
	if !strings.Contains(vs[0].Text, "localFailed") {
		t.Fatalf("违规文案必须点名本包已有的常量：%q", vs[0].Text)
	}
}

// TestWordTableLocalConstDefinitionIsExempt 函数内的局部 const 定义处必须放行。
//
// 局部 const 同样是「给它起了个名字」——把它判红会把「在函数里起个局部名」
// 这种正常写法拦下来。反向验证：若常量收集退化成只看包级声明，
// 本用例立刻转红。
func TestWordTableLocalConstDefinitionIsExempt(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

func f() string {
	const localFailed = "failed"
	return localFailed
}
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("局部 const 定义处应放行，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableOwnerReferenceDoesNotMaskLiteral 引用主人常量不得掩盖别处的裸字面量。
//
// 主人常量引用是**结构性**豁免（限定选择器不是字面量），不是包级通行证：
// 引用了 owner.StatusFailed 之后另写一处 "failed"，那一处仍要红。
//
// 反向验证：若豁免被改成包级「本包引用过主人常量就整体放过」，
// 本用例立刻转红。
func TestWordTableOwnerReferenceDoesNotMaskLiteral(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

import "example.test/h/owner"

func f() string { return owner.StatusFailed }
func g() string { return "failed" }
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("引用主人常量不掩盖别处字面量，应报 1 条，实得 %d 条：%v", len(vs), vs)
	}
	if !strings.Contains(vs[0].Text, "owner.StatusFailed") {
		t.Fatalf("违规文案必须点名主人常量：%q", vs[0].Text)
	}
	if !strings.Contains(vs[0].Text, "你已经引用了") {
		t.Fatalf("违规文案要点名「你已经引用了主人常量」——否则读者以为要新加依赖：%q", vs[0].Text)
	}
}

// TestWordTableQualifiedReferenceIsNotALiteral 限定引用不得被当成字面量（白盒）。
//
// 这条是「引用主人常量」那条放行臂的**承重**夹具：那条臂没有独立分支
// （限定选择器本来就不是字面量），所以它的正确性只能由采集器的行为保证。
// 若哪天采集器开始把限定引用的取值也收进字面量集，门禁会把合法引用
// 判成手抄——而这类假红到没人信它，它就等于没有。
//
// 断言用「字面量条数为零」而不是「没有某个取值」：任何多收一条的改动
// （结构体标签、import 路径、限定引用）都在这条用例的射程内。
func TestWordTableQualifiedReferenceIsNotALiteral(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

import "example.test/h/owner"

func f() string { return owner.StatusFailed }
`)
	idx, err := buildRepoIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	p := idx.byRel["consumer"]
	if p == nil {
		t.Fatal("合成仓里没有 consumer 包")
	}
	if len(p.literals) != 0 {
		t.Fatalf("限定引用不得进字面量集，实得 %d 条：%v", len(p.literals), p.literals)
	}
}

// TestWordTableKindInputDoesNotJudgeConsumers 输入归一词表不得判消费方。
//
// 输入词表里的取值是**外部喂进来的词形**（healthy / down / 正常 / 宕机……），
// 消费方自由使用它们——那是输入数据，不是判定依据。按判定词表登记会引出
// 成片假阳（实测 protocol 的 43 个图表词形会命中 17 处，大半是 JSON 键、
// HTTP 响应体这类无关上下文）。
func TestWordTableKindInputDoesNotJudgeConsumers(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

func f() string { return "failed" }
`)
	vs, err := wordTableViolations(root, wordTablePolicyKind("input"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("输入归一词表不应判消费方，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableKindInputStillChecksEnumSource 输入归一词表仍要判「出口读同一张表」。
//
// 不判消费方 ≠ 不判这张表：出口读的是另一份清单时，它给出的词形清单会与
// 归一判定漂移——而下游会信出口那份（比没有出口更糟）。
func TestWordTableKindInputStillChecksEnumSource(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": `package owner

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

var another = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(another))
	for s := range another {
		out = append(out, s)
	}
	return out
}

// Normalize 归一出口（input 类必须有）：读同一张表。
func Normalize(s string) string {
	if statusAll[s] {
		return StatusOK
	}
	return ""
}
`,
	})
	vs, err := wordTableViolations(root, wordTablePolicyKind("input"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableEnumRuleID {
		t.Fatalf("输入词表的出口读错表仍须报红，实得 %v", vs)
	}
}

// TestWordTableGroupedBackingWithoutInputKindIsRed 分组形状却没写 kind: input 必须红。
//
// 失败形态必须是**变红且说清怎么改**：含糊地报「派生不出取值」，
// 维护者要自己猜「那该怎么登记」；报「请写 kind: input」才是可执行的。
func TestWordTableGroupedBackingWithoutInputKindIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableGroupedOwnerPkg,
	})
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableEnumRuleID {
		t.Fatalf("分组形状缺 kind 应报 1 条 enum 规则违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "kind: input") {
		t.Fatalf("违规文案必须说清该怎么改（kind: input）：%q", vs[0].Text)
	}
}

// TestWordTableGroupedBackingWithInputKindIsGreen 分组形状写成 kind: input 即通过。
//
// 与上一条成对：只测「会红」不测「该怎么写才绿」，等于让维护者猜。
func TestWordTableGroupedBackingWithInputKindIsGreen(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableGroupedOwnerPkg,
	})
	vs, err := wordTableViolations(root, wordTablePolicyKind("input"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("分组形状写 kind: input 应通过，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableUnknownKindIsRed kind 写错必须红。
//
// 失败形态必须是变红：写错的类别名（kind: judgement 之类）若被当成
// 「不是 decision」，这条登记就静默降级成「不判消费方」——登记了一张表却
// 没生效，且没有任何机器能发现。
func TestWordTableUnknownKindIsRed(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

func f() string { return "failed" }
`)
	// kind 的值域封闭是**声明面内部矛盾** ⇒ 归 policy 自校验（恒开、
	// 提交前就拦）；执行体只负责「不把写错的类别当成合法类别继续派生」。
	vs := selfCheckViolations(root, wordTablePolicyKind("judgement"))
	if len(vs) != 1 || vs[0].Rule != selfCheckRuleID {
		t.Fatalf("未知的 kind 应报 1 条自校验违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "judgement") {
		t.Fatalf("违规文案必须点名写错的类别：%q", vs[0].Text)
	}
}

// TestWordTableKindDefaultIsDecision kind 缺省必须是判定词表。
//
// 缺省值若被改成 input，这条门禁就退化成「什么都不判却照常报绿」——
// 登记了一张表却没生效，且没有任何机器能发现。
func TestWordTableKindDefaultIsDecision(t *testing.T) {
	root := writeWordTableRepo(t, `package consumer

func f() string { return "failed" }
`)
	vs, err := wordTableViolations(root, wordTablePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("kind 缺省应按判定词表判消费方，实得 %d 条：%v", len(vs), vs)
	}
	// 断言**规则 id**而不只是条数：条数相同但规则不同（如换成「缺归一出口」
	// 那条）同样是判据坏了——只看条数会让这类变异蒙混过关。
	if vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("kind 缺省应判手抄规则，实得规则 %q：%v", vs[0].Rule, vs)
	}
}

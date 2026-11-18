// wordtable_family_test.go —— 族覆盖判据（反向派生）的用例。
//
// # 这条判据守的是「登记与事实的对应」
//
// 取值派生只有「表 → 取值」一个方向。主人包**新增**一个同族常量却忘了
// 加进表 ⇒ 那个新取值的消费侧从此没人管：它不在表里 ⇒ 手抄判据判不到它；
// 它不在枚举出口里 ⇒ 宿主看不到它。而它在主人包里是个正经的导出常量 ⇒
// 谁看代码都以为它受管。
//
// ⇒ 失败形态必须是**变红**。下面每条「必须红」的用例都是承重的：
// 把族判据改坏（不判、判反、豁免失效不报），它们必须转红。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableFamilyOwner 主人包：族里有两个状态常量，表里只登记了一个。
//
// StatusOpen 是**非字符串**常量（分类枚举）——它不该算族成员：
// 同名族里混着别的概念是常态（contract 的 StatusOpen 是分类，不是状态）。
const wordTableFamilyOwner = `package owner

const (
	StatusFailed  = "failed"
	StatusCreated = "created"
	StatusOpen    = 0
)

var statusAll = map[string]bool{StatusFailed: true}

// AllStatuses 导出枚举出口：读同一张表。
func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableFamilyOwnerComplete 主人包：族里两个常量，表里都登记了。
const wordTableFamilyOwnerComplete = `package owner

const (
	StatusFailed  = "failed"
	StatusCreated = "created"
)

var statusAll = map[string]bool{StatusFailed: true, StatusCreated: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableFamilyOwnerAlias 主人包：族常量是**跨包别名**（statemachine 形态）。
//
// 取值解析必须能追过去——追不过去时族成员会「派生不出」，
// 而「派生不出」若被当成「没有族成员」，判据就是静默空转。
const wordTableFamilyOwnerAlias = `package owner

import "example.test/h/other"

const (
	StatusFailed  = other.StatusFailed
	StatusCreated = other.StatusCreated
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

// wordTableFamilyOther 别名指向的那个包。
const wordTableFamilyOther = `package other

const (
	StatusFailed  = "failed"
	StatusCreated = "created"
)
`

// wordTableFamilyPolicy 造一份登记了族前缀的 policy。
func wordTableFamilyPolicy(t WordTable) *Policy {
	return &Policy{WordTables: &WordTables{Tables: []WordTable{t}}}
}

// TestWordTableFamilyUncoveredIsRed 族成员没进表必须红。
//
// 这就是「常量加了、表没加」：那个新取值的消费侧从此没人管，
// 而没有任何别的东西会红。门禁的失败形态必须是变红。
func TestWordTableFamilyUncoveredIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwner,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: "Status",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableFamilyRuleID {
		t.Fatalf("族成员漏进表应报 1 条族覆盖违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "StatusCreated") {
		t.Fatalf("违规文案必须点名漏掉的常量：%q", vs[0].Text)
	}
}

// TestWordTableFamilyCoveredIsGreen 族被表完整覆盖必须绿。
//
// 反向验证：族判据若被改坏成「一律红」，本用例会红——所以它守的是
// 「已覆盖的族不被误判」。真正的承重在上面那条「必须红」。
func TestWordTableFamilyCoveredIsGreen(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwnerComplete,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: "Status",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("族被完整覆盖时应零违规，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableFamilyExcludeApplies 豁免必须真放行。
//
// 同族里常含**另一张表**的常量（protocol 的 Decision* 同时含两张表的成员）
// ⇒ 族前缀判不出归属，必须显式豁免且写明理由。
func TestWordTableFamilyExcludeApplies(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwner,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: "Status",
		FamilyExclude: []WordTableFamilyExclude{
			{Prefix: "StatusCreated", Reason: "属于另一张表的族"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("族内豁免命中时应零违规，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableFamilyExcludeStaleIsRed 豁免失效必须红。
//
// 豁免的前缀在族里一个常量都匹配不到 ⇒ 它今天什么都没豁免，
// 却会在将来出现同名常量时无声地开始生效——与 exceptions 段
// 「豁免必须指向真实存在的东西」同款纪律。
func TestWordTableFamilyExcludeStaleIsRed(t *testing.T) {
	root := t.TempDir()
	// 夹具用**族被完整覆盖**的那份：否则漏进表的常量会**同时**报一条，
	// 断言就分不清「豁免失效」这条是不是真的报了（一个断言只能钉一件事）。
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwnerComplete,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: "Status",
		FamilyExclude: []WordTableFamilyExclude{
			{Prefix: "StatusGone", Reason: "常量已改名"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableFamilyRuleID {
		t.Fatalf("失效的族豁免应报 1 条族覆盖违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "StatusGone") {
		t.Fatalf("违规文案必须点名失效的豁免前缀：%q", vs[0].Text)
	}
}

// TestWordTableFamilyKeyOutsideIsRed 表的键不属于族必须红。
//
// 族前缀写错、或表里塞了别的族的键——两种都是声明与事实不符。
func TestWordTableFamilyKeyOutsideIsRed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwnerComplete,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: "Step",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// 族前缀写成 Step ⇒ 表里两个键都不属于它 ⇒ 两条违反（逐键点名）。
	if len(vs) != 2 {
		t.Fatalf("表的键不属于族应逐键报 2 条族覆盖违反，实得 %d 条：%v", len(vs), vs)
	}
	for _, v := range vs {
		if v.Rule != wordTableFamilyRuleID {
			t.Fatalf("规则 id 实得 %q，期望 %q", v.Rule, wordTableFamilyRuleID)
		}
	}
	if !strings.Contains(vs[0].Text, "StatusCreated") || !strings.Contains(vs[1].Text, "StatusFailed") {
		t.Fatalf("违规文案必须逐个点名不属于族的键：%v", vs)
	}
}

// TestWordTableFamilyWithoutDeclarationIsNotJudged 没声明族就不判。
//
// 「派生不出族」若被当成「族已被覆盖」，门禁就是静默空转——
// 所以这里只保证**不判**，不保证「已覆盖」。
func TestWordTableFamilyWithoutDeclarationIsNotJudged(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwner,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("没声明族时不该判族覆盖，实得 %d 条：%v", len(vs), vs)
	}
}

// TestWordTableFamilyAliasResolved 跨包别名常量必须算族成员。
//
// 别名解析追不过去时，族成员会「派生不出」——而「派生不出」若被当成
// 「没有族成员」，那条漏进表的常量就永远没人发现（判据静默空转）。
func TestWordTableFamilyAliasResolved(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"owner/owner.go": wordTableFamilyOwnerAlias,
		"other/other.go": wordTableFamilyOther,
	})
	vs, err := wordTableViolations(root, wordTableFamilyPolicy(WordTable{
		Pkg: "owner", Backing: "statusAll", Enum: "AllStatuses", Family: "Status",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Rule != wordTableFamilyRuleID {
		t.Fatalf("跨包别名的族成员漏进表应报 1 条族覆盖违反，实得 %v", vs)
	}
	if !strings.Contains(vs[0].Text, "StatusCreated") {
		t.Fatalf("违规文案必须点名漏掉的别名常量：%q", vs[0].Text)
	}
}

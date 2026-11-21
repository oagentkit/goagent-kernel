// testnocall_test.go —— test_no_direct_call 规则的复现用例（合成夹具）。
//
// # 这组用例证明什么
//
// 规则「测试面不得直调包级全局态 setter」（issue #449）的执行体是 AST 判定
// （testnocall.go）。它的失败形态是**门禁变绿**：判据被放宽（把直调放过、
// 或把字符串里的同名文本当成调用而误报到没人信它），都不会报错，只是不再命中。
//
// # 反向验证必须「点名注入的那一条」
//
// 只断言「有违规」在登记面与派生面不一致时会**恒成立** ⇒ 假绿。故这里断言
// 违规条数 + **确切行号** + 文本里点名的符号名。
//
// # 受管辅助的机制也要被钉住
//
// 「policy 声明了受管辅助 ⇒ 它函数体内部的直调不判」这条机制若失效（名单被
// 硬编码进执行体、或声明根本不生效），门禁不会变红——它只是变成了「登记了
// 一张表却没生效」。故这里双向断言：未登记 ⇒ 判；登记 ⇒ 不判。
//
// # 这组用例自身必须守规矩
//
// 本文件在 archcheck 包内，而本规则的判定面是**调用点**——夹具一律是字符串，
// 本文件自身不出现对被禁符号的调用。
package archcheck

import (
	"strings"
	"testing"
)

// tncFixtureRule 是夹具用的规则声明（与真实 policy 同口径）。
func tncFixtureRule() Rule {
	return Rule{
		ID:          "harness-test-no-direct-global-setter",
		Scope:       []string{"."},
		Include:     []string{"*.go"},
		OnlyTest:    true,
		CheckKind:   checkKindTestNoDirectCall,
		ForbidCalls: []string{"SetEnabled", "SetSurfaces"},
	}
}

// TestNoDirectCallHitsDirectCall 用例体里的直调 ⇒ 必须判中并点名符号与行号。
func TestNoDirectCallHitsDirectCall(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestDirectSet(t *testing.T) {
	SetEnabled(false)
	if Enabled() {
		t.Fatal("x")
	}
}
`)
	vs, err := testNoDirectCallViolations(root, tncFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("用例体里的直调必须判中 1 条，实得 %d 条: %s", len(vs), textsOf(vs))
	}
	if vs[0].Line != 4 {
		t.Fatalf("必须点名确切行号（第 4 行），实得 %d: %s", vs[0].Line, textsOf(vs))
	}
	if !strings.Contains(vs[0].Text, "SetEnabled") {
		t.Fatalf("违规文本必须点名被调符号，实得 %s", textsOf(vs))
	}
}

// TestNoDirectCallMissesHelperRoute 走包级 helper ⇒ 不判。
//
// 少了这条，一个「一律判中」的坏实现也能让上面那条正例通过。
func TestNoDirectCallMissesHelperRoute(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func setEnabledForTest(on bool) {
	SetEnabled(on)
}

func TestViaHelper(t *testing.T) {
	setEnabledForTest(false)
}
`)
	r := tncFixtureRule()
	r.ManagedHelpers = []string{"setEnabledForTest"}
	vs, err := testNoDirectCallViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("受管辅助内的直调不得判中，实得 %d 条: %s", len(vs), textsOf(vs))
	}
}

// TestNoDirectCallFlagsUnregisteredHelperThenHonorsRegistration 双向钉住
// 受管辅助机制：**未登记**的包内助手体内的直调照判（助手是绕开 helper 的第二条
// 路），登记之后不判。
//
// 只测「登记了不判」的话，一个把 managed_helpers 当摆设（恒不判助手）的坏实现
// 也能过——那正是「登记了一张表却没生效」的形态。
func TestNoDirectCallFlagsUnregisteredHelperThenHonorsRegistration(t *testing.T) {
	src := `package sample

func sneakySet() {
	SetEnabled(false)
}

func TestViaSneaky(t *testing.T) {
	sneakySet()
}
`
	root := gjFixture(t, "sample_test.go", src)

	// ① 未登记 ⇒ 判中，且点名助手与行号。
	vs, err := testNoDirectCallViolations(root, tncFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("未登记助手中的直调必须判中 1 条，实得 %d 条: %s", len(vs), textsOf(vs))
	}
	if vs[0].Line != 4 || !strings.Contains(vs[0].Text, "sneakySet") {
		t.Fatalf("必须点名助手与行号（第 4 行 sneakySet），实得 line=%d: %s", vs[0].Line, textsOf(vs))
	}

	// ② 登记为受管辅助 ⇒ 不判。
	r := tncFixtureRule()
	r.ManagedHelpers = []string{"sneakySet"}
	vs, err = testNoDirectCallViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("登记后的助手不得判中，实得 %d 条: %s", len(vs), textsOf(vs))
	}
}

// TestNoDirectCallIgnoresNonCallSites 同名**文本**不是调用点：函数定义、字符串、
// 测试名里出现被禁符号，都不得判中。
//
// 判据走 go/ast 而不是正则的原因就在这条：逐行匹配会把它们一起命中，规则
// 误报到没人信它，就等于没有规则（而它看起来一直在跑）。
func TestNoDirectCallIgnoresNonCallSites(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

// 函数定义不是调用点。
func SetEnabled(on bool) {}

func TestSetEnabledGuards(t *testing.T) {
	t.Fatal("SetEnabled(false) 后仍为 true")
}
`)
	vs, err := testNoDirectCallViolations(root, tncFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("定义 / 字符串 / 测试名里的同名文本都不是调用点，实得 %d 条: %s", len(vs), textsOf(vs))
	}
}

// wallclock_test.go —— wallclock_ruler 规则的复现用例（合成夹具）。
//
// # 这组用例证明什么
//
// 规则「测试面不许拿裸 time.Sleep 当尺子」（issue #441）的执行体是 AST 判定
// （wallclock.go）。它的失败形态是**门禁变绿**：判据被放宽（把尺子当轮询
// 退避放过）、或 import 别名形态认不出，都不会报错，只是不再命中。
//
// # 反向验证必须「点名注入的那一条」
//
// 只断言「有违规」在登记面与派生面不一致时会**恒成立** ⇒ 假绿。故这里断言
// 违规条数 + **确切行号** + 文本里点名的 sleep 形态。
//
// # 受管辅助的机制也要被钉住
//
// 「policy 声明了受管辅助 ⇒ 它函数体内部的 sleep 不判」这条机制若失效
// （名单被硬编码进执行体、或声明根本不生效），门禁不会变红——它只是
// 变成了「登记了一张表却没生效」。故这里同时断言两个方向：声明了 ⇒ 不判；
// 不声明 ⇒ 判（且点名那一处）。
//
// # 这组用例自身必须守规矩
//
// 本文件在 archcheck 包内，而本规则**也扫本仓的测试文件** ⇒ 夹具一律是
// **字符串**，本文件自身不出现裸 time.Sleep。
package archcheck

import (
	"strings"
	"testing"
)

// wcFixtureRule 是夹具用的规则声明（与真实 policy 同口径）。
func wcFixtureRule() Rule {
	return Rule{
		ID:        "harness-test-wallclock-ruler",
		Scope:     []string{"."},
		Include:   []string{"*.go"},
		OnlyTest:  true,
		CheckKind: checkKindWallclockRuler,
	}
}

// TestWallclockRulerHitsBareSleep 用例体里的裸 sleep ⇒ 必须判中。
func TestWallclockRulerHitsBareSleep(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestRuler(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	if !ready() {
		t.Fatalf("没准备好")
	}
}

func ready() bool { return true }
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("裸 sleep 必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 6 {
		t.Fatalf("违规必须指向 sleep 所在行（第 6 行），实得 %d", vs[0].Line)
	}
	if !strings.Contains(vs[0].Text, "time.Sleep") {
		t.Fatalf("违规必须点名 sleep 形态，实得 %q", vs[0].Text)
	}
}

// TestWallclockRulerNamesInjectedViolation 反向验证：注入的那一条被点名。
//
// 夹具里放两处裸 sleep，断言两条都在、各自行号精确；再放一处只有第二处的
// 夹具，断言它只报第二处。
func TestWallclockRulerNamesInjectedViolation(t *testing.T) {
	two := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestFirst(t *testing.T) {
	time.Sleep(10 * time.Millisecond)
}

func TestSecond(t *testing.T) {
	time.Sleep(20 * time.Millisecond)
}
`)
	vs, err := wallclockRulerViolations(two, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("两处裸 sleep 必须判 2 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 6 || vs[1].Line != 10 {
		t.Fatalf("两条违规必须各自指向 sleep 行（6 / 10），实得 %d / %d", vs[0].Line, vs[1].Line)
	}

	one := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestOnlySecond(t *testing.T) {
	time.Sleep(20 * time.Millisecond)
}
`)
	vs, err = wallclockRulerViolations(one, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("只注入一处必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 6 {
		t.Fatalf("违规必须指向注入的那一行（第 6 行），实得 %d", vs[0].Line)
	}
}

// TestWallclockRulerMissesPollSleep 轮询退避（sleep 在 for/range 循环体内）
// 不是尺子 ⇒ 不判（这条边界是本规则显式登记的）。
func TestWallclockRulerMissesPollSleep(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestPoll(t *testing.T) {
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		if ready() {
			return
		}
	}
}

func ready() bool { return true }
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("轮询退避不是尺子，不该判违规: %v", textsOf(vs))
	}
}

// TestWallclockRulerMissesGoroutineSleep goroutine 体里的 sleep 是被测机制
// 自己的节奏，不是测试侧的尺子 ⇒ 不判。
func TestWallclockRulerMissesGoroutineSleep(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestWorker(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(done)
	}()
	<-done
}
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("goroutine 体里的 sleep 不是尺子，不该判违规: %v", textsOf(vs))
	}
}

// TestWallclockRulerManagedHelperMechanism 受管辅助的机制必须**双向**可判：
//
//   - 声明了（managed_helpers）⇒ 助手函数体内部的 sleep 不判；
//   - 没声明 ⇒ 判（并点名那一处）。
//
// 只测第一个方向的话，「名单被硬编码进执行体」这种失效查不出来。
func TestWallclockRulerManagedHelperMechanism(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestUsesHelper(t *testing.T) {
	waitForFact()
}

func waitForFact() {
	time.Sleep(30 * time.Millisecond)
}
`)
	// 未声明 ⇒ 判。
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("未登记为受管辅助时必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 10 {
		t.Fatalf("违规必须指向助手里的 sleep 行（第 10 行），实得 %d", vs[0].Line)
	}

	// 声明了 ⇒ 不判。
	r := wcFixtureRule()
	r.ManagedHelpers = []string{"sample.waitForFact"}
	vs, err = wallclockRulerViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("登记为受管辅助后不该判违规: %v", textsOf(vs))
	}
}

// TestWallclockRulerRegisteredInPolicy 真实 policy 里必须登记这条规则，且它
// 必须自带样例（加载期验活）。
func TestWallclockRulerRegisteredInPolicy(t *testing.T) {
	pol := testPolicy(t)
	found := false
	for _, r := range pol.Rules {
		if r.ID != "harness-test-wallclock-ruler" {
			continue
		}
		found = true
		if r.CheckKind != checkKindWallclockRuler {
			t.Fatalf("真实 policy 里这条规则的 check_kind 应为 %q，实得 %q", checkKindWallclockRuler, r.CheckKind)
		}
		if !r.OnlyTest {
			t.Fatal("这条规则必须只扫测试文件（only_test: true）")
		}
		if len(r.Examples) == 0 {
			t.Fatal("这条规则必须自带 examples（加载期验活）")
		}
	}
	if !found {
		t.Fatal("真实 policy 里没有登记 harness-test-wallclock-ruler")
	}
	for _, info := range ruleInfos(pol, severityMap(pol)) {
		if info.ID == "harness-test-wallclock-ruler" {
			return
		}
	}
	t.Fatal("harness-test-wallclock-ruler 没有出现在报告的规则清单里")
}

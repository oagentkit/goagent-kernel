// testgoroutine_defer_test.go —— defer_cleanup 规则的复现用例（合成夹具）。
//
// # 这组用例证明什么
//
// 规则「用例内起的 goroutine 的收尾等待点必须写在 defer 里」（issue #440）的
// 执行体是 AST 判定（testgoroutine_defer.go）。它的失败形态是**门禁变绿**：
// 判据被放宽（把「收尾等待」误判成「同步等待」）、或等待点识别漏掉某种形态，
// 都不会报错，只是不再命中。故必须有一组**反向**用例把这两条失效形态钉住：
//
//   - 夹具里真的有「失败路径上会被 Goexit 跳过」的等待点 ⇒ 必须判中；
//   - 夹具里合规（同步等待 / defer 形态 / defer 调用的收尾助手）⇒ 必须不判中。
//
// # 反向验证必须「点名注入的那一条」
//
// 只断言「有违规」在登记面与派生面不一致时会**恒成立** ⇒ 假绿。故这里的用例
// 一律断言：违规条数 + **确切行号** + 文本里点名的用例名/等待点/终止调用。
// 「注入 A 处 ⇒ 只报 A 处」与「注入 B 处 ⇒ 只报 B 处」要能互相区分。
//
// # 这组用例自身必须守规矩
//
// 本文件在 archcheck 包内，而本规则**也扫本仓的测试文件**（scope: [.]，
// only_test: true）⇒ 本文件里的夹具一律是**字符串**（不产生真 goroutine），
// 也不出现裸 go 语句。
package archcheck

import (
	"strings"
	"testing"
)

// dcFixtureRule 是夹具用的规则声明（与真实 policy 同口径）。
func dcFixtureRule() Rule {
	return Rule{
		ID:        "harness-test-defer-cleanup",
		Scope:     []string{"."},
		Include:   []string{"*.go"},
		OnlyTest:  true,
		CheckKind: checkKindDeferCleanup,
	}
}

// TestDeferCleanupRuleHitsTailWait 等待点写在普通语句里、且前面有可终止用例的
// 调用 ⇒ 必须判中（失败路径上 Goexit 会跳过它）。
func TestDeferCleanupRuleHitsTailWait(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestTailWait(t *testing.T) {
	done := make(chan struct{})
	go worker()
	if !ready() {
		t.Fatalf("没准备好")
	}
	<-done
}

func worker() {}
func ready() bool { return true }
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("收尾等待必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 5 {
		t.Fatalf("违规必须指向 go 语句所在行（第 5 行），实得 %d", vs[0].Line)
	}
	// 报告必须点名：测试函数名、等待点形态、以及那个「能让用例提前终止的调用」。
	for _, want := range []string{"TestTailWait", "<-done", "t.Fatalf"} {
		if !strings.Contains(vs[0].Text, want) {
			t.Fatalf("违规必须点名 %q，实得 %q", want, vs[0].Text)
		}
	}
}

// TestDeferCleanupRuleNamesInjectedViolation 反向验证：**注入的那一条**被点名，
// 而不是「随便判出了别的」。
//
// 夹具里放两处收尾等待，断言两条都在、各自行号精确；再放一处只有第二处的
// 夹具，断言它只报第二处（不掺别的）。
func TestDeferCleanupRuleNamesInjectedViolation(t *testing.T) {
	two := gjFixture(t, "sample_test.go", `package sample

func TestFirst(t *testing.T) {
	done := make(chan struct{})
	go worker()
	t.Fatalf("第一个失败")
	<-done
}

func TestSecond(t *testing.T) {
	done := make(chan struct{})
	go worker()
	t.Fatalf("第二个失败")
	<-done
}

func worker() {}
`)
	vs, err := deferCleanupViolations(two, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("两处收尾等待必须判 2 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 5 || vs[1].Line != 12 {
		t.Fatalf("两条违规必须各自指向 go 语句行（5 / 12），实得 %d / %d", vs[0].Line, vs[1].Line)
	}
	if !strings.Contains(vs[0].Text, "TestFirst") || !strings.Contains(vs[1].Text, "TestSecond") {
		t.Fatalf("两条违规必须各自点名用例名，实得 %q / %q", vs[0].Text, vs[1].Text)
	}

	// 只注入第二处 ⇒ 只报第二处（证明判据认的是「注入的那一条」）。
	one := gjFixture(t, "sample_test.go", `package sample

func TestOnlySecond(t *testing.T) {
	done := make(chan struct{})
	go worker()
	t.Fatalf("唯一失败")
	<-done
}

func worker() {}
`)
	vs, err = deferCleanupViolations(one, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("只注入一处必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if !strings.Contains(vs[0].Text, "TestOnlySecond") {
		t.Fatalf("违规必须点名注入的那个用例，实得 %q", vs[0].Text)
	}
}

// TestDeferCleanupRuleMissesSyncWait 等待点之前没有可终止用例的调用 ⇒ 同步等待，
// goroutine 一定在用例继续之前结束 ⇒ 合规。
func TestDeferCleanupRuleMissesSyncWait(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestSyncWait(t *testing.T) {
	done := make(chan struct{})
	go worker()
	<-done
}

func worker() {}
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("同步等待不该判违规: %v", textsOf(vs))
	}
}

// TestDeferCleanupRuleMissesDeferWait defer 形态 ⇒ 收尾等待 ⇒ 合规。
func TestDeferCleanupRuleMissesDeferWait(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestDeferWait(t *testing.T) {
	done := make(chan struct{})
	go worker()
	defer func() { <-done }()
}

func worker() {}
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("defer 形态的等待点不该判违规: %v", textsOf(vs))
	}
}

// TestDeferCleanupRuleMissesDeferHelper defer 调用的包内助手里有等待点 ⇒ 合规。
func TestDeferCleanupRuleMissesDeferHelper(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestDeferHelper(t *testing.T) {
	done := make(chan struct{})
	go worker()
	defer stopIt(done)
}

func stopIt(done <-chan struct{}) {
	<-done
}

func worker() {}
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("defer 调用的收尾助手不该判违规: %v", textsOf(vs))
	}
}

// TestDeferCleanupRuleMissesNoWait 完全没有等待点 ⇒ 这条规则不判（那是兄弟规则
// harness-test-goroutine-joined 的面）。
func TestDeferCleanupRuleMissesNoWait(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestNoWait(t *testing.T) {
	go worker()
}

func worker() {}
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("没有等待点时这条规则不该判违规: %v", textsOf(vs))
	}
}

// TestDeferCleanupRuleIgnoresSubtestFatal 子用例闭包里的 t.Fatal 终止的是
// **子用例**，不是父用例 ⇒ 父用例的等待点仍会执行 ⇒ 不该判违规。
//
// 这条用例钉住「执行上下文」这一层：不区分它就会把「子用例失败」误当成
// 「父用例被提前终止」⇒ 假红。
func TestDeferCleanupRuleIgnoresSubtestFatal(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestSubtest(t *testing.T) {
	done := make(chan struct{})
	go worker()
	t.Run("sub", func(t *testing.T) {
		t.Fatal("子用例失败")
	})
	<-done
}

func worker() {}
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("子用例的 t.Fatal 不终止父用例，不该判违规: %v", textsOf(vs))
	}
}

// TestDeferCleanupRuleRegisteredInPolicy 真实 policy 里必须登记这条规则，
// 且它必须自带样例（加载期验活）。
func TestDeferCleanupRuleRegisteredInPolicy(t *testing.T) {
	pol := testPolicy(t)
	found := false
	for _, r := range pol.Rules {
		if r.ID != "harness-test-defer-cleanup" {
			continue
		}
		found = true
		if r.CheckKind != checkKindDeferCleanup {
			t.Fatalf("真实 policy 里这条规则的 check_kind 应为 %q，实得 %q", checkKindDeferCleanup, r.CheckKind)
		}
		if !r.OnlyTest {
			t.Fatal("这条规则必须只扫测试文件（only_test: true）")
		}
		if len(r.Examples) == 0 {
			t.Fatal("这条规则必须自带 examples（加载期验活）")
		}
	}
	if !found {
		t.Fatal("真实 policy 里没有登记 harness-test-defer-cleanup")
	}
	for _, info := range ruleInfos(pol, severityMap(pol)) {
		if info.ID == "harness-test-defer-cleanup" {
			return
		}
	}
	t.Fatal("harness-test-defer-cleanup 没有出现在报告的规则清单里")
}

// TestDeferCleanupRuleMissesNonBlockingDrain 非阻塞 drain（select 带 default）
// **不是等待点** ⇒ 不该判违规。
//
// # 为什么要有这条用例
//
// 非阻塞 drain 是**断言**（看此刻通道里有没有东西），不是 join。它被当成等待点
// 时，规则会给出「把这个 drain 塞进 defer」的处方——而 drain 本来就不等东西，
// 塞进 defer 什么也等不到 ⇒ **判据会指错路**。正确处方是给那个 goroutine
// 补一个真正的收尾 join。
//
// 断言必须**双向**：只测「drain 不判红」不够——判据整个瘫掉（什么都不判）
// 也会让这条用例绿。⇒ 紧跟一条「阻塞接收仍然判红」的用例（见下）。
func TestDeferCleanupRuleMissesNonBlockingDrain(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestNonBlockingDrain(t *testing.T) {
	done := make(chan struct{})
	ch := make(chan struct{})
	go worker()
	if !ready() {
		t.Fatalf("没准备好")
	}
	select {
	case <-ch:
	default:
	}
	_ = done
}

func worker() {}
func ready() bool { return true }
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("非阻塞 drain 不是等待点，不该判违规: %v", textsOf(vs))
	}
}

// TestDeferCleanupRuleHitsBlockingReceiveInSelect select **没有** default ⇒
// 仍会挂起 ⇒ 仍是阻塞接收 ⇒ 仍是等待点 ⇒ 必须判红。
//
// 这条与上一条成对：它们把「阻塞 / 非阻塞」这条分界双向钉住——
// 少一条，另一条就成了「判据瘫掉也绿」的假绿。
func TestDeferCleanupRuleHitsBlockingReceiveInSelect(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestBlockingReceive(t *testing.T) {
	done := make(chan struct{})
	go worker()
	if !ready() {
		t.Fatalf("没准备好")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("没等到")
	}
}

func worker() {}
func ready() bool { return true }
`)
	vs, err := deferCleanupViolations(root, dcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("阻塞接收必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if !strings.Contains(vs[0].Text, "TestBlockingReceive") {
		t.Fatalf("违规必须点名用例名，实得 %q", vs[0].Text)
	}
}

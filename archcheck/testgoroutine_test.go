// testgoroutine_test.go —— 规则侧复现用例（合成夹具）。
//
// # 这组用例证明什么
//
// 规则「用例内起的 goroutine 必须在测试侧被等待」的执行体是 AST 判定
// （testgoroutine.go）。它的失败形态是**门禁变绿**：判据被放宽、等待点识别漏掉
// 某种形态，都不会报错，只是不再命中。于是必须有一组**反向**用例把这两条失效
// 形态钉住：
//
//   - 夹具里真的起了没等的 goroutine ⇒ 必须判中（判据放宽会当场红）；
//   - 夹具里合规（直接等 / 助手等 / WaitGroup.Wait）⇒ 必须不判中（判据过宽会红）。
//
// 夹具是**合成**的：一段最小 Go 源码写到临时目录，判定跑在临时目录上。
// 不依赖本仓真实用例 ⇒ 真实用例改了也不会让这条用例失去意义（它判的是
// **判据**，不是某个用例的现状）。
//
// # 这组用例自身必须守规矩
//
// 本文件在 archcheck 包内，而本规则**也扫本仓的测试文件**（scope: [.]，
// only_test: true）⇒ 本文件里的 go 语句必须自带等待点，否则规则会把自己
// 判成违规。故这里的夹具一律用**普通函数**驱动，不用真 goroutine。
package archcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gjFixtureRule 是夹具用的规则声明（与真实 policy 同口径）。
func gjFixtureRule() Rule {
	return Rule{
		ID:        "harness-test-goroutine-joined",
		Scope:     []string{"."},
		Include:   []string{"*.go"},
		OnlyTest:  true,
		CheckKind: checkKindGoroutineJoin,
	}
}

// gjFixture 写一份夹具源码，返回临时仓库根（判定跑在它上面）。
func gjFixture(t *testing.T, name, src string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGoroutineRuleHitsUnjoinedGoroutine 起了没等的 goroutine 必须判中。
func TestGoroutineRuleHitsUnjoinedGoroutine(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestLeak(t *testing.T) {
	go worker()
}

func worker() {}
`)
	vs, err := testGoroutineViolations(root, gjFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("起了没等的 goroutine 必须判 1 条，实得 %d 条: %v", len(vs), vs)
	}
	if vs[0].Rule != "harness-test-goroutine-joined" {
		t.Fatalf("违规必须点名规则 id，实得 %q", vs[0].Rule)
	}
	if vs[0].Line != 4 {
		t.Fatalf("违规必须指向 go 语句所在行（第 4 行），实得 %d", vs[0].Line)
	}
	if !strings.Contains(vs[0].Text, "TestLeak") {
		t.Fatalf("违规必须点名用例名，实得 %q", vs[0].Text)
	}
}

// TestGoroutineRuleHitsSelfWait <-ch 写在被启动的那个 goroutine 体内 ⇒ 仍判。
//
// 它是「长得像等待、其实不是」的形态：那个 goroutine 在等**别人**，不是测试侧
// 在等它。若判据把它算成等待点，等于给「起了不等」的写法发免死金牌。
func TestGoroutineRuleHitsSelfWait(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestSelfWait(t *testing.T) {
	ch := make(chan struct{})
	go func() { <-ch }()
}
`)
	vs, err := testGoroutineViolations(root, gjFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("goroutine 体内的等待不算测试侧的等待，必须判 1 条，实得 %d 条: %v", len(vs), vs)
	}
}

// TestGoroutineRuleMissesDirectJoin 用例体内直接 <-done 等过 ⇒ 不判。
func TestGoroutineRuleMissesDirectJoin(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestJoined(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	<-done
}
`)
	vs, err := testGoroutineViolations(root, gjFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("用例体内已 <-done 等过，不该判违规: %v", vs)
	}
}

// TestGoroutineRuleMissesHelperJoin 助手里等过 ⇒ 不判。
//
// 收尾助手是本仓惯用范式（收尾助手：cancel → <-done → 超时兜底）。若判据只看
// 用例体，会把已经修好的用例判成违规——误报到没人信它，门禁等于没有。
func TestGoroutineRuleMissesHelperJoin(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestViaHelper(t *testing.T) {
	done := make(chan struct{})
	go func() { close(done) }()
	stopIt(done)
}

func stopIt(done <-chan struct{}) {
	<-done
}
`)
	vs, err := testGoroutineViolations(root, gjFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("助手里有等待点，不该判违规: %v", vs)
	}
}

// TestGoroutineRuleMissesWaitGroup X.Wait() 也是等待点 ⇒ 不判。
func TestGoroutineRuleMissesWaitGroup(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

func TestWaitGroup(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done() }()
	wg.Wait()
}
`)
	vs, err := testGoroutineViolations(root, gjFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("X.Wait() 是等待点，不该判违规: %v", vs)
	}
}

// TestGoroutineRuleIgnoresNonTestFunc 生产文件里的 goroutine 不在判定面内。
//
// 判定面是**测试函数**：生产代码的 goroutine 由 harness-no-bare-goroutine 管
// （裸 goroutine 的 panic 无法被外层 recover）。两条规则各管一面，不重叠。
func TestGoroutineRuleIgnoresNonTestFunc(t *testing.T) {
	root := gjFixture(t, "sample.go", `package sample

func Worker() {
	go other()
}

func other() {}
`)
	vs, err := testGoroutineViolations(root, gjFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("生产代码的 goroutine 不在本规则判定面内，实得 %v", vs)
	}
}

// TestGoroutineRuleRegisteredInPolicy 真实 policy 里必须登记这条规则，
// 且它必须出现在报告的规则清单里（判了但看不见 ≈ 没判）。
func TestGoroutineRuleRegisteredInPolicy(t *testing.T) {
	pol := testPolicy(t)
	found := false
	for _, r := range pol.Rules {
		if r.ID != "harness-test-goroutine-joined" {
			continue
		}
		found = true
		if r.CheckKind != checkKindGoroutineJoin {
			t.Fatalf("真实 policy 里这条规则的 check_kind 应为 %q，实得 %q", checkKindGoroutineJoin, r.CheckKind)
		}
		if !r.OnlyTest {
			t.Fatal("这条规则必须只扫测试文件（only_test: true）")
		}
		if len(r.Examples) == 0 {
			t.Fatal("这条规则必须自带 examples（加载期验活）")
		}
	}
	if !found {
		t.Fatal("真实 policy 里没有登记 harness-test-goroutine-joined")
	}
	for _, info := range ruleInfos(pol, severityMap(pol)) {
		if info.ID == "harness-test-goroutine-joined" {
			return
		}
	}
	t.Fatal("harness-test-goroutine-joined 没有出现在报告的规则清单里")
}

// TestUnknownCheckKindRejected 未知 check_kind 必须当天即红。
//
// 写错的类别名若被当成「不是已知类别」，这条登记就**静默降级**成「不判」：
// 规则在、severity 是 error、报告里照常计入条数，只是永不触发。失败形态是门禁
// **变绿**——比变红更贵，因为它看起来一切正常。
func TestUnknownCheckKindRejected(t *testing.T) {
	pol := Policy{Rules: []Rule{{ID: "x", Severity: "error", CheckKind: "no_such_kind"}}}
	vs := selfCheckViolations(t.TempDir(), &pol)
	if !containsText(vs, "check_kind") {
		t.Fatalf("未知 check_kind 必须被自校验报出，实得 %v", textsOf(vs))
	}
}

// TestCheckKindRuleRejectsSecondBody 一条规则只能有一个执行体。
func TestCheckKindRuleRejectsSecondBody(t *testing.T) {
	pol := Policy{Rules: []Rule{{
		ID:          "x",
		Severity:    "error",
		CheckKind:   checkKindGoroutineJoin,
		ForbidRegex: "^go ",
	}}}
	vs := selfCheckViolations(t.TempDir(), &pol)
	if !containsText(vs, "只能有一个执行体") {
		t.Fatalf("同时声明两个执行体必须被自校验报出，实得 %v", textsOf(vs))
	}
}

// wallclock_threshold_test.go —— 墙钟阈值形态的检测与分类对账用例（issue #481）。
//
// # 这组用例证明什么
//
// 规则「断言的尺子必须是可观测事实，不是墙钟窗口」（#441）在 #481 扩了两块判定面：
// **固定秒数断言**与**纯计时器窗口**。它们的失败形态是**门禁变绿**：
// 判据被放宽（派生阈值也判、事实分支也判）、或形态漏认（先赋值再比较的那一类），
// 都不会报错，只是不再命中。
//
// # 反向验证必须「点名注入的那一条」
//
// 分类对账有**三个方向**（漏登记 / 登记成尺子类 / 僵尸登记），少判一个方向，
// 这条判据就只剩一半 ⇒ 这里逐个方向各钉一条用例。
//
// # 这组用例自身必须守规矩
//
// 本文件在 archcheck 包内，而本规则**也扫本仓的测试文件** ⇒ 夹具一律是
// **字符串**，本文件自身不出现墙钟阈值形态。
package archcheck

import (
	"strings"
	"testing"
)

// wcFixtureClass 造一条分类登记（夹具用）。
//
// Package 用 "."：夹具文件写在临时仓库根上，filepath.Dir("sample_test.go") = "."。
func wcFixtureClass(fn, form, class string) RulerClassification {
	return RulerClassification{
		Package: ".", Func: fn, Form: form, Class: class,
		Reason: "夹具理由：阈值比机制自身的时限宽一个数量级，结论由事实决定",
	}
}

// wcFixtureRuleWith 返回一条带分类登记的夹具规则。
func wcFixtureRuleWith(cs ...RulerClassification) Rule {
	r := wcFixtureRule()
	r.RulerClassifications = cs
	return r
}

// ── 检测面 ────────────────────────────────────────────────────────────

// TestWallclockThresholdDetectsFixedSecondsDirect 直接形态：把已流逝时长与
// 固定时长常量比较 ⇒ 必须判中。
func TestWallclockThresholdDetectsFixedSecondsDirect(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestDirect(t *testing.T) {
	start := time.Now()
	doWork()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("太慢了")
	}
}
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("固定秒数断言必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Kind != "test_wallclock_unclassified" {
		t.Fatalf("未登记的墙钟判定必须报 unclassified，实得 kind=%q", vs[0].Kind)
	}
	if vs[0].Line != 8 {
		t.Fatalf("违规必须指向比较所在行（第 8 行），实得 %d", vs[0].Line)
	}
}

// TestWallclockThresholdDetectsFixedSecondsViaVariable 经变量形态：
// `el := time.Since(start)` 之后拿 `el` 去比 ⇒ 必须判中。
//
// 这一条不是可选的补充：**实测本仓三条现存判定里有两条是这个形状**
// （`el := time.Since(start); el > 15*time.Second`）。只认直接形态的判据会把
// 它们整条漏掉，而漏掉的方向恰好是门禁变绿 ⇒ 这条用例就是那份漏掉的证据。
func TestWallclockThresholdDetectsFixedSecondsViaVariable(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestViaVar(t *testing.T) {
	start := time.Now()
	doWork()
	elapsed := time.Since(start)
	if elapsed > 15*time.Second {
		t.Fatalf("太慢了")
	}
}
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("经变量的固定秒数断言必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Line != 9 {
		t.Fatalf("违规必须指向比较所在行（第 9 行），实得 %d", vs[0].Line)
	}
}

// TestWallclockThresholdMissesDerivedThreshold 派生阈值（limit/2）不是
// 「固定秒数」⇒ 不判（判它会把「阈值随夹具参数变」的用法一起误伤）。
func TestWallclockThresholdMissesDerivedThreshold(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestDerived(t *testing.T) {
	start := time.Now()
	doWork()
	if time.Since(start) > limit/2 {
		t.Fatalf("太慢了")
	}
}
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("派生阈值不是固定秒数，不该判违规: %v", textsOf(vs))
	}
}

// TestWallclockThresholdDetectsPureTimerSelect 纯计时器窗口（select 里只有
// 计时器分支）⇒ 必须判中；带事实分支的 select ⇒ 不判（窗口是「等事实的上限」）。
func TestWallclockThresholdDetectsPureTimerSelect(t *testing.T) {
	only := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestTimerOnly(t *testing.T) {
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("这 2s 内什么都没发生")
	}
}
`)
	vs, err := wallclockRulerViolations(only, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("纯计时器窗口必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Kind != "test_wallclock_unclassified" {
		t.Fatalf("未登记的纯计时器窗口必须报 unclassified，实得 kind=%q", vs[0].Kind)
	}

	bounded := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestBoundedWait(t *testing.T) {
	select {
	case <-ch:
		t.Log("到了")
	case <-time.After(2 * time.Second):
		t.Fatal("超时")
	}
}
`)
	vs, err = wallclockRulerViolations(bounded, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("带事实分支的 select 不是尺子，不该判违规: %v", textsOf(vs))
	}
}

// TestWallclockThresholdRespectsImportAlias import 别名形态也要判得到
// （判据按 import 路径解析本地名，不按源码文本认 "time."）。
func TestWallclockThresholdRespectsImportAlias(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import tm "time"

func TestAlias(t *testing.T) {
	start := tm.Now()
	doWork()
	if tm.Since(start) > 2*tm.Second {
		t.Fatalf("太慢了")
	}
}
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("别名导入形态必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
}

// ── 分类对账（三个方向）────────────────────────────────────────────────

// TestWallclockThresholdUnclassifiedIsRed 反向验证①：漏登记即红。
//
// 这条是本仓最贵的失效形态（门禁变绿）：一处墙钟判定若没人给结论，它会继续
// 随机器快慢漂，而门禁一声不吭。
func TestWallclockThresholdUnclassifiedIsRed(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestUnclassified(t *testing.T) {
	start := time.Now()
	doWork()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("太慢了")
	}
}
`)
	vs, err := wallclockRulerViolations(root, wcFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("未登记的墙钟判定必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Kind != "test_wallclock_unclassified" {
		t.Fatalf("必须报 unclassified，实得 kind=%q", vs[0].Kind)
	}
	if !strings.Contains(vs[0].Text, "TestUnclassified") {
		t.Fatalf("违规必须点名归属函数，实得 %q", vs[0].Text)
	}
}

// TestWallclockThresholdRulerClassIsRed 反向验证②：登记成**尺子类**仍红。
//
// 登记只是记账，不是放行——尺子类的结论由窗口长度单独决定，必须改写。
func TestWallclockThresholdRulerClassIsRed(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestRulerClass(t *testing.T) {
	start := time.Now()
	doWork()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("太慢了")
	}
}
`)
	r := wcFixtureRuleWith(wcFixtureClass("TestRulerClass", wcFormFixedSeconds, wcClassRuler))
	vs, err := wallclockRulerViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("登记为尺子类的墙钟判定必须仍判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Kind != "test_wallclock_ruler_class" {
		t.Fatalf("必须报 ruler_class，实得 kind=%q", vs[0].Kind)
	}
}

// TestWallclockThresholdCapClassPasses 超时上限类登记 ⇒ 绿（且必须真的绿，
// 否则这条规则就成了「一律红」，人只会学会忽略它）。
func TestWallclockThresholdCapClassPasses(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestCap(t *testing.T) {
	start := time.Now()
	doWork()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("太慢了")
	}
}
`)
	r := wcFixtureRuleWith(wcFixtureClass("TestCap", wcFormFixedSeconds, wcClassTimeoutCap))
	vs, err := wallclockRulerViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("登记为超时上限类后不该判违规: %v", textsOf(vs))
	}
}

// TestWallclockThresholdStaleClassIsRed 反向验证③：僵尸登记即红。
//
// 那一处改写成可观测事实之后，登记必须跟着删——留着不删，清单会烂成装饰
// （而装饰不会红，于是下一次新增的墙钟判定也就没人管了）。
func TestWallclockThresholdStaleClassIsRed(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestOther(t *testing.T) {
	doWork()
}
`)
	r := wcFixtureRuleWith(wcFixtureClass("TestGone", wcFormFixedSeconds, wcClassTimeoutCap))
	vs, err := wallclockRulerViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("僵尸登记必须判 1 条，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Kind != "test_wallclock_stale_class" {
		t.Fatalf("必须报 stale_class，实得 kind=%q", vs[0].Kind)
	}
}

// TestWallclockThresholdMissingEntryIsRed 同一键下登记少一条 ⇒ 多出来的那处
// 仍是漏登记（登记与判定点按出现顺序一一对应）。
func TestWallclockThresholdMissingEntryIsRed(t *testing.T) {
	root := gjFixture(t, "sample_test.go", `package sample

import "time"

func TestTwo(t *testing.T) {
	start := time.Now()
	doWork()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("太慢了")
	}
	start2 := time.Now()
	doWork()
	if time.Since(start2) > 3*time.Second {
		t.Fatalf("太慢了")
	}
}
`)
	// 只登记第一处 ⇒ 第二处漏登记。
	r := wcFixtureRuleWith(wcFixtureClass("TestTwo", wcFormFixedSeconds, wcClassTimeoutCap))
	vs, err := wallclockRulerViolations(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("登记少一条时多出来的那处必须判红，实得 %d 条: %v", len(vs), textsOf(vs))
	}
	if vs[0].Kind != "test_wallclock_unclassified" {
		t.Fatalf("必须报 unclassified，实得 kind=%q", vs[0].Kind)
	}
}

// ── 真实 policy ───────────────────────────────────────────────────────

// TestWallclockPolicyHasClassifications 真实 policy 里这条规则必须带分类登记
// （否则「逐条判定」就只是文档里的一句话），且每条登记都要写明理由。
func TestWallclockPolicyHasClassifications(t *testing.T) {
	pol := testPolicy(t)
	found := false
	for _, r := range pol.Rules {
		if r.ID != "harness-test-wallclock-ruler" {
			continue
		}
		found = true
		if len(r.RulerClassifications) == 0 {
			t.Fatal("这条规则必须带 ruler_classifications（逐条判定的结论不许只留在散文里）")
		}
		for _, c := range r.RulerClassifications {
			if strings.TrimSpace(c.Reason) == "" {
				t.Fatalf("分类登记 %s.%s#%s 必须写明理由", c.Package, c.Func, c.Form)
			}
			if !wcKnownForm(c.Form) {
				t.Fatalf("分类登记的 form %q 不在已知取值域 %v 内", c.Form, wcFormNames())
			}
			if !wcKnownClass(c.Class) {
				t.Fatalf("分类登记的 class %q 不在已知取值域 %v 内", c.Class, wcClassNames())
			}
		}
	}
	if !found {
		t.Fatal("真实 policy 里没有登记 harness-test-wallclock-ruler")
	}
}

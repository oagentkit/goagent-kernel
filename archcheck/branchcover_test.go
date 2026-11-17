// branchcover_test.go —— 分支覆盖判据的反向验证。
//
// # 这组用例钉住的是什么
//
// 「每条正则的每一个分支都被样例钉住」这条判据，失败形态是**变绿**：
// 样例只覆盖第一支时，其余分支被删/改名，门禁照样全绿。
// 故每条用例都要能回答：**坏实现会红吗**。
//
// 夹具一律用**合成词**（alpha / beta / gamma / delta）——
// 样例文本若写成真实领域词面，等于把契约钉死在宿主域上，
// 而这条规则自己就是「不得出现领域词」的那条。

package archcheck

import (
	"strconv"
	"strings"
	"testing"
)

// splitRule 把一条规则拆成「样例齐全」与「少一条样例」两份，
// 用来断言「删掉样例 ⇒ 判据转红」。
func splitRule(r Rule, drop string) Rule {
	out := r
	out.Examples = nil
	for _, ex := range r.Examples {
		if ex.Text == drop {
			continue
		}
		out.Examples = append(out.Examples, ex)
	}
	return out
}

// TestBranchCoverProblemsReportsUncoveredBranch 少一条样例 ⇒ 报出未覆盖的分支。
func TestBranchCoverProblemsReportsUncoveredBranch(t *testing.T) {
	r := Rule{
		ID:           "demo-two-branch",
		ForbidImport: `^errors$|^github\.com/pkg/errors$`,
		Examples: []RuleExample{
			{Text: "errors", Want: exampleWantHit},
			{Text: "github.com/oekit/pkg/errors", Want: exampleWantMiss},
		},
	}
	problems := branchCoverProblems(r)
	if len(problems) == 0 {
		t.Fatal("第二个分支没有任何 hit 样例覆盖：判据必须报红（这是门禁变绿的那一类失效）")
	}
	found := false
	for _, p := range problems {
		// 报错里的分支文本走 %q（反斜杠被转义成两个），故按同样的形态比对。
		if strings.Contains(p, strconv.Quote("^github\\.com/pkg/errors$")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("报错必须点名未覆盖的分支，实得：%v", problems)
	}
}

// TestBranchCoverProblemsCleanWhenEveryBranchCovered 每个分支都有样例 ⇒ 不报。
func TestBranchCoverProblemsCleanWhenEveryBranchCovered(t *testing.T) {
	r := Rule{
		ID:           "demo-two-branch",
		ForbidImport: `^errors$|^github\.com/pkg/errors$`,
		Examples: []RuleExample{
			{Text: "errors", Want: exampleWantHit},
			{Text: "github.com/pkg/errors", Want: exampleWantHit},
			{Text: "github.com/oekit/pkg/errors", Want: exampleWantMiss},
		},
	}
	if problems := branchCoverProblems(r); len(problems) != 0 {
		t.Fatalf("样例齐全时不得报红，实得：%v", problems)
	}
}

// TestBranchCoverProblemsSampleMustExerciseThatBranch 样例命中**另一支**不算覆盖。
//
// 这一条钉的是「经由这一支命中」的语义：只含第一支的样例不能顶替第二支。
func TestBranchCoverProblemsSampleMustExerciseThatBranch(t *testing.T) {
	r := Rule{
		ID:          "demo-wordlist",
		ForbidRegex: `(alpha|beta|gamma)`,
		IgnoreTest:  true,
		Examples: []RuleExample{
			{Text: "x := alpha", Want: exampleWantHit},
			{Text: "x := delta", Want: exampleWantMiss},
		},
	}
	problems := branchCoverProblems(r)
	if len(problems) != 2 {
		t.Fatalf("beta 与 gamma 两支都未被覆盖，必须报 2 条，实得 %d：%v", len(problems), problems)
	}
}

// TestBranchCoverProblemsOverlappingBranches 允许重叠的两支各自算数。
//
// 本仓的裸 goroutine 规则里，go func() 同时命中「func(」与「标识符(」两支。
// 若判据用「其余分支不命中」来区分，这一条会永远红（假红）；
// 故判据用的是**捕获组参与**——两条分支各被自己的样例钉住。
func TestBranchCoverProblemsOverlappingBranches(t *testing.T) {
	r := Rule{
		ID:          "demo-overlap",
		ForbidRegex: `^\s*go\s+(func\(|(?:[A-Za-z_]\w*\.)*[A-Za-z_]\w*\()`,
		IgnoreTest:  true,
		Examples: []RuleExample{
			{Text: "\tgo func() {", Want: exampleWantHit},
			{Text: "\tgo worker.Start()", Want: exampleWantHit},
			{Text: "\tgoFunc := 1", Want: exampleWantMiss},
		},
	}
	if problems := branchCoverProblems(r); len(problems) != 0 {
		t.Fatalf("两支各被自己的样例钉住，不得报红，实得：%v", problems)
	}
}

// TestBranchCoverProblemsIgnoresStructuralBranches 边界型交替不算独立分支。
//
// (/|$) 里的 / 与 $ 是同一条路径前缀的两种边界写法，不是独立的一支。
func TestBranchCoverProblemsIgnoresStructuralBranches(t *testing.T) {
	r := Rule{
		ID:           "demo-boundary",
		ForbidImport: `example\.com/demo/(/|$)`,
		Examples: []RuleExample{
			{Text: "example.com/demo/x", Want: exampleWantHit},
			{Text: "example.com/other", Want: exampleWantMiss},
		},
	}
	if problems := branchCoverProblems(r); len(problems) != 0 {
		t.Fatalf("边界型交替不应要求样例，实得：%v", problems)
	}
}

// TestSplitAlternationsBranchTextHasNoSeparator 分支文本不得带分隔用的竖线。
//
// 上一版把分隔点直接当成下一支的起点，分支文本形如「|beta」——
// 套进捕获组后是「空分支 | beta」，空分支匹配任何字符串，
// 于是**每个样例都被判成经由每一支命中**，覆盖判据整体失效。
func TestSplitAlternationsBranchTextHasNoSeparator(t *testing.T) {
	groups := splitAlternations(`(alpha|beta|gamma)`)
	if len(groups) != 1 {
		t.Fatalf("应切出一个交替组，实得 %d", len(groups))
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(groups[0].Branches) != len(want) {
		t.Fatalf("应切出 %d 支，实得 %d", len(want), len(groups[0].Branches))
	}
	for i, br := range groups[0].Branches {
		if br.Text != want[i] {
			t.Fatalf("第 %d 支应是 %q，实得 %q", i, want[i], br.Text)
		}
	}
}

// TestSplitAlternationsIgnoresSeparatorsInClassAndEscape 转义与字符类里的竖线不算分隔符。
func TestSplitAlternationsIgnoresSeparatorsInClassAndEscape(t *testing.T) {
	groups := splitAlternations(`[a|b]\|c|d`)
	total := 0
	for _, g := range groups {
		total += len(g.Branches)
	}
	if total != 2 {
		t.Fatalf("字符类与转义里的竖线不算分隔符：应切出 2 支，实得 %d", total)
	}
	if got := groups[0].Branches[0].Text; got != `[a|b]\|c` {
		t.Fatalf("第一支应是 %q（字符类里的竖线与转义的竖线都留在支内），实得 %q", `[a|b]\|c`, got)
	}
}

// TestRealPolicyBranchCoverageCovered 真 policy 的每条规则都覆盖了每个分支。
//
// 这是**正对照**：现状 policy 补齐样例后必须全绿。
func TestRealPolicyBranchCoverageCovered(t *testing.T) {
	pol := loadRealPolicy(t)
	for _, r := range pol.Rules {
		if r.ForbidRegex == "" && r.ForbidImport == "" {
			continue
		}
		for _, p := range branchCoverProblems(r) {
			t.Errorf("规则 %s 未覆盖分支：%s", r.ID, p)
		}
	}
}

// TestRealPolicyBranchCoverageDetectsDroppedSample 真 policy 少一条样例 ⇒ 转红。
//
// 这是**反向验证**（对应 issue #386 的验收）：把某条规则的第二个分支的样例
// 删掉，判据必须报红，且红点直指「分支无样例覆盖」。
func TestRealPolicyBranchCoverageDetectsDroppedSample(t *testing.T) {
	pol := loadRealPolicy(t)
	r := ruleByID(t, pol, "harness-single-errors-package")
	if len(branchCoverProblems(r)) != 0 {
		t.Fatal("真 policy 的样例应当已覆盖全部分支")
	}
	// 删掉第二个分支（第二套 errors 包）的那条样例。
	dropped := splitRule(r, "github.com/pkg/errors")
	problems := branchCoverProblems(dropped)
	if len(problems) == 0 {
		t.Fatal("删掉第二个分支的样例后判据必须转红（否则门禁会静默变绿）")
	}
	if !strings.Contains(problems[0], "分支") || !strings.Contains(problems[0], "样例") {
		t.Fatalf("红点必须直指「分支无样例覆盖」，实得：%s", problems[0])
	}
}

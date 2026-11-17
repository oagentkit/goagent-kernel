// branchcover_capture_test.go —— 「捕获组参与」判据的反向验证。
//
// # 这组用例钉住的是什么
//
// branchcover.go 用「**捕获组参与匹配**」区分两件不同的事：
//
//   - 样例**经由这一支**命中（真覆盖）；
//   - 样例只是**含该分支的字面量**，实际走的是**另一支**（假覆盖）。
//
// 判据走两步：把整条正则改写成「只留这一支」（套一层捕获组），
// 再看那个捕获组**有没有参与**匹配。
//
// # 为什么已有用例打不中它
//
// 已有的样例用的是 **互不相交**的分支（alpha|beta|gamma）：
// 改写版只留一支时，别支的样例**根本命中不了改写版** ⇒
// 「捕获组恒真」与「真判参与」在这些样例上**没有可观测差异**
// （把 captureParticipates 改成恒真，全部用例照样绿）。
//
// 故本文件构造**互相包含**的分支与**嵌在更大模式里**的交替组：
// 改写版里**其余分支还在**，样例完全可能走的是另一支——
// 这时「恒真」与「真判参与」的结论才**可区分**。
//
// 夹具一律用**合成词**（alpha / beta / gamma / q / pkg）：
// 样例文本若写成真实领域词面，等于把契约钉死在宿主域上。

package archcheck

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestCaptureParticipatesDistinguishesParticipation 「参与」与「没参与」必须可区分。
//
// 改写版 (alpha)|gamma 里**其余分支还在**：样例 "gamma" 命中的是 gamma 那一支，
// alpha 这一支的捕获组**没有参与**匹配（Go 报告为 -1）。
// 若把 captureParticipates 改成恒真，这里立刻转红。
func TestCaptureParticipatesDistinguishesParticipation(t *testing.T) {
	re := regexp.MustCompile(`(alpha)|gamma`)
	if captureParticipates(re, 1, "gamma") {
		t.Fatal("样例走的是 gamma 那一支：alpha 这一支的捕获组没有参与匹配，" +
			"判据必须把它判成「这一支没被钉住」")
	}
	if !captureParticipates(re, 1, "alpha") {
		t.Fatal("样例经由 alpha 这一支命中：捕获组参与了匹配，判据必须认它被钉住")
	}
}

// TestBranchCoverProblemsReportsBranchOnlyHitByOtherBranch 样例命中**另一支**
// 不算覆盖（文本规则面）。
//
// 交替组 (alpha|beta) 嵌在更大的模式 (alpha|beta)|gamma 里 ⇒
// 改写版 (beta)|gamma **仍含 gamma 那一支** ⇒ 样例 "x := gamma" 命中的是
// gamma，不是 beta。beta 这一支因此**没有被任何样例钉住**，必须报红。
//
// 若 captureParticipates 恒真，beta 会被样例 "x := gamma" 顶替 ⇒ 不报红 ⇒
// 这条用例转红。
func TestBranchCoverProblemsReportsBranchOnlyHitByOtherBranch(t *testing.T) {
	r := Rule{
		ID:          "demo-nested-alt",
		ForbidRegex: `(alpha|beta)|gamma`,
		IgnoreTest:  true,
		Examples: []RuleExample{
			{Text: "x := alpha", Want: exampleWantHit},
			{Text: "x := gamma", Want: exampleWantHit},
			{Text: "x := delta", Want: exampleWantMiss},
		},
	}
	problems := branchCoverProblems(r)
	if len(problems) != 1 {
		t.Fatalf("只有 beta 这一支没被钉住（gamma 那条样例命中的是另一支），"+
			"必须报 1 条，实得 %d：%v", len(problems), problems)
	}
	if !strings.Contains(problems[0], strconv.Quote("beta")) {
		t.Fatalf("红点必须点名未覆盖的分支 beta，实得：%s", problems[0])
	}
}

// TestBranchCoverProblemsImportBranchOnlyHitByOtherBranch 同一语义在
// **import 路径面**上同样成立。
//
// import 规则的样例面是 import 路径：改写版 (beta/pkg)|gamma/pkg 里
// gamma/pkg 那一支仍在 ⇒ 样例 "gamma/pkg" 命中的是 gamma 那一支，
// beta/pkg 这一支没被钉住，必须报红。
func TestBranchCoverProblemsImportBranchOnlyHitByOtherBranch(t *testing.T) {
	r := Rule{
		ID:           "demo-nested-alt-import",
		ForbidImport: `(alpha/pkg|beta/pkg)|gamma/pkg`,
		Examples: []RuleExample{
			{Text: "alpha/pkg", Want: exampleWantHit},
			{Text: "gamma/pkg", Want: exampleWantHit},
			{Text: "delta/pkg", Want: exampleWantMiss},
		},
	}
	problems := branchCoverProblems(r)
	if len(problems) != 1 {
		t.Fatalf("只有 beta/pkg 这一支没被钉住（gamma/pkg 那条样例命中的是另一支），"+
			"必须报 1 条，实得 %d：%v", len(problems), problems)
	}
	if !strings.Contains(problems[0], strconv.Quote("beta/pkg")) {
		t.Fatalf("红点必须点名未覆盖的分支 beta/pkg，实得：%s", problems[0])
	}
}

// TestBranchCoverProblemsUsesCorrectCaptureGroupNumber 捕获组的**组号**要算准。
//
// 组号 = 该位置之前的捕获组个数 + 1（分支文本内部自带的捕获组排在它后面）。
// 模式 (q)?(alpha|beta)|gamma 里，交替组之前有一个捕获组 (q)? ⇒
// 「只留这一支」的改写版是 (q)?(alpha)|gamma ⇒ 被守护的捕获组是**第 2 号**
// （第 1 号是那个可选的 (q)?——样例没走它时它报告为 -1）。
//
// 组号写死成 1 ⇒ 判据去看的是那个没参与的组 ⇒ alpha / beta 两支被误判成
// 没被钉住 ⇒ 这条用例转红。
func TestBranchCoverProblemsUsesCorrectCaptureGroupNumber(t *testing.T) {
	r := Rule{
		ID:          "demo-group-number",
		ForbidRegex: `(q)?(alpha|beta)|gamma`,
		IgnoreTest:  true,
		Examples: []RuleExample{
			{Text: "x := alpha", Want: exampleWantHit},
			{Text: "x := beta", Want: exampleWantHit},
			{Text: "x := gamma", Want: exampleWantHit},
			{Text: "x := delta", Want: exampleWantMiss},
		},
	}
	if problems := branchCoverProblems(r); len(problems) != 0 {
		t.Fatalf("每一支都被自己的样例钉住，不得报红，实得：%v", problems)
	}
}

// TestBranchCoverProblemsCountsNamedCaptureGroups 组号算式要把**命名捕获组**
// 也数进去。
//
// 模式 (?P<pre>q)?(alpha|beta)|gamma 里，交替组之前有一个命名捕获组 ⇒
// 改写版 (?P<pre>q)?(alpha)|gamma 里被守护的组是**第 2 号**。
// 命名捕获组被漏数 ⇒ 组号算成 1 ⇒ 去看那个没参与的命名组 ⇒
// alpha / beta 两支被误判成没被钉住 ⇒ 这条用例转红。
func TestBranchCoverProblemsCountsNamedCaptureGroups(t *testing.T) {
	r := Rule{
		ID:          "demo-named-group",
		ForbidRegex: `(?P<pre>q)?(alpha|beta)|gamma`,
		IgnoreTest:  true,
		Examples: []RuleExample{
			{Text: "x := alpha", Want: exampleWantHit},
			{Text: "x := beta", Want: exampleWantHit},
			{Text: "x := gamma", Want: exampleWantHit},
			{Text: "x := delta", Want: exampleWantMiss},
		},
	}
	if problems := branchCoverProblems(r); len(problems) != 0 {
		t.Fatalf("每一支都被自己的样例钉住，不得报红，实得：%v", problems)
	}
}

// TestCountCapturesCountsOnlyCapturingGroups 组号算式的输入：只数**捕获组**。
//
// (?: 与 (?i) 不是捕获组；(?P<name>...) 是捕获组。
func TestCountCapturesCountsOnlyCapturingGroups(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{text: "", want: 0},
		{text: "(q)?", want: 1},
		{text: "(?:q)(x)", want: 1},
		{text: "(?i)(x)", want: 1},
		{text: `(?P<name>x)`, want: 1},
		{text: `(a|b)(c)`, want: 2},
		{text: `[()]`, want: 0},
		{text: `\(x\)`, want: 0},
	}
	for _, c := range cases {
		if got := countCaptures(c.text); got != c.want {
			t.Errorf("countCaptures(%q) = %d，应为 %d", c.text, got, c.want)
		}
	}
}

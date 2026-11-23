// externalseam_test.go —— 外部依赖接缝对账的复现用例（issue #482）。
//
// # 这组用例证明什么
//
// 规则「问外部世界的生产函数必须登记角色；decide 角色须有接缝或显式豁免理由」
// 的执行体是 AST 检测 + 清单对账（externalseam.go / externalseam_detect.go）。
// 它的失败形态是**门禁变绿**：检测面漏形态（函数值形态漏了、测试文件误算进来）、
// 或对账面漏方向（漏登记不报、僵尸登记不报），都不会报错，只是不再命中。
//
// # 反向验证必须「点名注入的那一条」
//
// 对账有**三个方向**（漏登记 / 陈旧 / 判定缺失），少判一个方向这条判据就只剩
// 一半 ⇒ 这里逐个方向各钉一条用例。
package archcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extFixtureRule 是夹具用的规则声明。
func extFixtureRule() Rule {
	return Rule{
		ID:        "harness-external-dep-seam",
		Scope:     []string{"."},
		Include:   []string{"*.go"},
		CheckKind: CheckKindExternalSeam,
		Registry:  "registry.yaml",
	}
}

// extFixture 造一份夹具：生产源码 + 清单，返回临时仓库根。
func extFixture(t *testing.T, src, registry string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "registry.yaml"), []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// extFixtureSample 生产源码（一次外部时钟读）。
const extFixtureSample = `package sample

import "time"

func Decide() time.Time {
	return time.Now()
}
`

// ── 检测面 ────────────────────────────────────────────────────────────

// TestExternalSeamDetectsProductionTouch 生产代码里的外部触达必须被检出。
func TestExternalSeamDetectsProductionTouch(t *testing.T) {
	root := extFixture(t, extFixtureSample, "deps: []\n")
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("生产代码里的 time.Now 必须被判成「漏登记」")
	}
	if !strings.Contains(vs[0].Text, "Decide") {
		t.Fatalf("违规必须点名漏登记的函数，实得 %q", vs[0].Text)
	}
}

// TestExternalSeamDetectsFuncValueForm 函数值形态（time.Now 当值传下去）
// 也必须被检出——它正是「接缝的缺省实现」的样子，漏了它清单就少了最有价值的那半边。
func TestExternalSeamDetectsFuncValueForm(t *testing.T) {
	root := extFixture(t, `package sample

import "time"

func New() func() time.Time {
	return time.Now
}
`, "deps: []\n")
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("函数值形态的 time.Now 必须被判成「漏登记」")
	}
	if !strings.Contains(vs[0].Text, "New") {
		t.Fatalf("违规必须点名漏登记的函数，实得 %q", vs[0].Text)
	}
}

// TestExternalSeamIgnoresTestFiles 测试文件不在这条规则的判定面内
// （夹具也问外部世界，但结论由用例自己给）。
func TestExternalSeamIgnoresTestFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample_test.go"),
		[]byte("package sample\n\nimport \"time\"\n\nfunc TestX() { _ = time.Now() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "registry.yaml"), []byte("deps: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("测试文件不该进这条规则的判定面: %v", textsOf(vs))
	}
}

// ── 对账面（三个方向）──────────────────────────────────────────────────

// TestExternalSeamStaleEntryIsRed 反向验证①：僵尸登记即红。
func TestExternalSeamStaleEntryIsRed(t *testing.T) {
	root := extFixture(t, extFixtureSample, `deps:
  - package: "."
    function: Gone
    deps: [clock]
    role: tool
`)
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("清单里登记了一个已不存在的函数，必须判红")
	}
	// 这份夹具同时触发「漏登记」（代码里的 Decide 没登记）与「僵尸登记」
	//（登记的 Gone 不存在）⇒ 两个方向都要在违规清单里找得到。
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "Gone") {
			found = true
		}
	}
	if !found {
		t.Fatalf("违规必须点名僵尸登记的条目，实得 %v", textsOf(vs))
	}
}

// TestExternalSeamDecideWithoutSeamIsRed 反向验证②：decide 角色既无接缝又无
// 豁免理由即红——「有外部依赖却无接缝」不许蒙混过去。
func TestExternalSeamDecideWithoutSeamIsRed(t *testing.T) {
	root := extFixture(t, extFixtureSample, `deps:
  - package: "."
    function: Decide
    deps: [clock]
    role: decide
`)
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("decide 角色既无接缝又无豁免理由，必须判红")
	}
	if !strings.Contains(vs[0].Text, "接缝") {
		t.Fatalf("违规必须点名「缺接缝」，实得 %q", vs[0].Text)
	}
}

// TestExternalSeamDecideWithExemptPasses decide 角色显式登记豁免理由 ⇒ 绿。
func TestExternalSeamDecideWithExemptPasses(t *testing.T) {
	root := extFixture(t, extFixtureSample, `deps:
  - package: "."
    function: Decide
    deps: [clock]
    role: decide
    exempt: "这条依赖就是协议的��身，注入它等于改了协议"
    reason: "同上：协议的一部分不可注入"
`)
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("decide 角色登记了豁免理由后不该判红: %v", textsOf(vs))
	}
}

// TestExternalSeamMissingSeamSymbolIsRed 反向验证③：声明的接缝符号在包内不存在
// 即红——「接缝声明不能是一句空话」。
func TestExternalSeamMissingSeamSymbolIsRed(t *testing.T) {
	root := extFixture(t, extFixtureSample, `deps:
  - package: "."
    function: Decide
    deps: [clock]
    role: stamp
    seam: "Demo.now"
`)
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("接缝符号不存在，必须判红")
	}
	if !strings.Contains(vs[0].Text, "Demo.now") {
		t.Fatalf("违规必须点名不可用的接缝符号，实得 %q", vs[0].Text)
	}
}

// TestExternalSeamDepsDriftIsRed 登记的依赖类与代码现状不符即红
// （登记与事实漂移的方向恰好是「门禁判的和代码跑的不是一套」）。
func TestExternalSeamDepsDriftIsRed(t *testing.T) {
	root := extFixture(t, extFixtureSample, `deps:
  - package: "."
    function: Decide
    deps: [fs]
    role: tool
`)
	vs, err := extViolations(root, extFixtureRule())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("登记的依赖类与实测不符，必须判红")
	}
	if !strings.Contains(vs[0].Text, "依赖类") {
		t.Fatalf("违规必须点名依赖类漂移，实得 %q", vs[0].Text)
	}
}

// ── 真实 policy ───────────────────────────────────────────────────────

// TestExternalSeamRegisteredInPolicy 真实 policy 里必须登记这条规则，且它必须
// 带清单路径（没有清单 ⇒ 它一条都判不了，而规则在、报告里照常计入条数）。
func TestExternalSeamRegisteredInPolicy(t *testing.T) {
	pol := testPolicy(t)
	found := false
	for _, r := range pol.Rules {
		if r.ID != "harness-external-dep-seam" {
			continue
		}
		found = true
		if r.CheckKind != CheckKindExternalSeam {
			t.Fatalf("这条规则的 check_kind 应为 %q，实得 %q", CheckKindExternalSeam, r.CheckKind)
		}
		if strings.TrimSpace(r.Registry) == "" {
			t.Fatal("这条规则必须带 registry（清单是它的判定面）")
		}
		if len(r.Examples) == 0 {
			t.Fatal("这条规则必须自带 examples（加载期验活）")
		}
	}
	if !found {
		t.Fatal("真实 policy 里没有登记 harness-external-dep-seam")
	}
}

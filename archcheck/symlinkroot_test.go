package archcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlinkPolicyYAML 是含一条全仓正则规则的最小 policy。
//
// 规则形态选「scope: [.] + forbid_regex」是因为它**只依赖文件系统扫描面**：
// 软链根下扫描面为空时，这条规则一条都判不出来 ⇒ 正好用来钉住本次缺陷。
const symlinkPolicyYAML = "version: 1\n" +
	"modules:\n" +
	"  - id: x\n" +
	"    roots: [x]\n" +
	"    requires: []\n" +
	"    owner: test\n" +
	"    managed: true\n" +
	"dependency_direction:\n" +
	"  enabled: false\n" +
	"rules:\n" +
	"  - id: harness-test-badword\n" +
	"    desc: 夹具：禁这个词\n" +
	"    severity: error\n" +
	"    scope: [.]\n" +
	"    include: [\"*.go\"]\n" +
	"    forbid_regex: BADWORD\n" +
	"thresholds:\n" +
	"  scan_roots: [x]\n" +
	"  max_file_lines: 400\n" +
	"  enforce: false\n"

// emptySurfacePolicyYAML 是「没有任何 Go 源文件」时用的最小 policy。
//
// modules 与 scan_roots 同时为空 ⇒ 声明面自洽（policy 自校验不报），于是
// Run 里唯一能拦住它的是「扫描面为空 ⇒ 给不出结论」那条守门。
const emptySurfacePolicyYAML = "version: 1\n" +
	"modules: []\n" +
	"dependency_direction:\n" +
	"  enabled: false\n" +
	"rules: []\n" +
	"thresholds:\n" +
	"  scan_roots: []\n" +
	"  max_file_lines: 400\n" +
	"  enforce: false\n"

// writeSymlinkFixture 造一个「仓库」：policy + 一个含 BADWORD 的包文件。
//
// 夹具是合成的（包名 x、词 BADWORD）——夹具是契约的示例，示例带领域味
// 就等于把契约钉死在某个域上。
func writeSymlinkFixture(t *testing.T, polYAML string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "architecture-policy.yaml"), []byte(polYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	// 违禁词必须落在**真实代码**上：正则判定跳过注释行（见 regexViolationsIn），
	// 放进注释会让这条用例变成「测的是注释豁免」，与本用例要钉的东西无关。
	if err := os.WriteFile(filepath.Join(root, "x", "a.go"), []byte("package x\n\nvar s = \"BADWORD\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// linkTo 造一个指向 real 的符号链接；本机不支持时跳过用例。
func linkTo(t *testing.T, real string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("该平台不能创建符号链接，跳过：%v", err)
	}
	return link
}

// TestFindRootResolvesSymlink 钉住「FindRoot 必须返回解析后的真实路径」。
//
// 为什么这条必须成立：filepath.Walk **不跟随符号链接**——返回软链路径时，
// Walk 会把仓库根当成「一个文件」，整棵子树一个文件都扫不到。
// 反向验证：把 FindRoot 改回「原样返回 dir」，本用例即红。
func TestFindRootResolvesSymlink(t *testing.T) {
	real := writeSymlinkFixture(t, symlinkPolicyYAML)
	link := linkTo(t, real)

	got, err := FindRoot(link)
	if err != nil {
		t.Fatalf("FindRoot 失败：%v", err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FindRoot 必须返回解析后的真实路径：实得 %q，期望 %q（返回软链路径会让 filepath.Walk 扫不到任何文件）", got, want)
	}
}

// TestRunScansThroughSymlinkedRoot 端到端钉住「软链根下门禁照样扫到违规」。
//
// 这条是本次缺陷的正例：软链根下扫描面为空 ⇒ 门禁把「没扫到」当成「没有违规」
// ⇒ **假绿**（门禁显示通过，实际什么都没判）。
// 反向验证：把 RunWithOptions 里的真实路径解析去掉，本用例即红
// （violations 里不会出现 BADWORD）。
func TestRunScansThroughSymlinkedRoot(t *testing.T) {
	real := writeSymlinkFixture(t, symlinkPolicyYAML)
	link := linkTo(t, real)

	pol, err := Load(filepath.Join(link, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败：%v", err)
	}
	res, err := Run(link, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论：%v", err)
	}
	if !containsText(res.Violations, "BADWORD") {
		t.Fatalf("软链根下必须仍然扫到包里的违规（扫空 = 门禁假绿）：实得 %v", textsOf(res.Violations))
	}
}

// TestRunFailsClosedOnEmptyScanSurface 钉住「扫描面为空 ⇒ 给不出结论」。
//
// 这条是本次缺陷的兜底：门禁的失败形态必须是**变红**，不是变绿。空扫描面下
// 「每条规则都没扫到文件」与「没有违规」在报告里长得一模一样（都是 0 违反），
// 而前者其实是「给不出结论」。
// 反向验证：把「扫描面为空」那条守门去掉，本用例即红（Run 会返回 nil 错）。
func TestRunFailsClosedOnEmptyScanSurface(t *testing.T) {
	root := t.TempDir()
	// 只有 policy，没有任何 Go 源文件 ⇒ 扫描面为空。
	if err := os.WriteFile(filepath.Join(root, "architecture-policy.yaml"), []byte(emptySurfacePolicyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败：%v", err)
	}
	res, err := Run(root, pol)
	if err == nil {
		t.Fatalf("扫描面为空时门禁必须给不出结论（报错），不能报「无违反」：实得 %v", textsOf(res.Violations))
	}
	if !strings.Contains(err.Error(), "扫描面为空") {
		t.Fatalf("错误必须点名「扫描面为空」：实得 %v", err)
	}
	if res != nil {
		t.Fatalf("给不出结论时不得同时给一份结果（那会被调用方当成「判过了」）：实得 %v", textsOf(res.Violations))
	}
}

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectMagic 魔数判据的逐条对照。
//
// 覆盖两种方向：正例必须命中（否则门禁对真二进制失明），反例必须不命中
// （否则门禁对正常文本资源误报）。反例里特意放了图片头与 Go 源码——它们
// 都是合法被跟踪资源，判成编译产物就是在制造噪声。
func TestDetectMagic(t *testing.T) {
	cases := []struct {
		name   string
		head   []byte
		want   string
		wantOK bool
	}{
		{"ELF", []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}, "ELF 可执行文件", true},
		{"Mach-O 64", []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01}, "Mach-O 64 位", true},
		{"Mach-O 通用", []byte{0xca, 0xfe, 0xba, 0xbe, 0x00, 0x00, 0x00, 0x02}, "Mach-O 通用二进制", true},
		{"PE", []byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}, "Windows PE 可执行文件", true},
		{"ar 静态库", []byte("!<arch>\n"), "ar 静态库", true},
		{"wasm", []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}, "WebAssembly 模块", true},
		{"纯文本", []byte("package main\n"), "", false},
		{"空文件", nil, "", false},
		{"PNG 资源", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, "", false},
		{"PDF 资源", []byte("%PDF-1.7\n"), "", false},
		{"ELF 差一个字节", []byte{0x7f, 'E', 'L', 0x00}, "", false},
	}
	for _, tc := range cases {
		got, ok := detect(tc.head)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("%s: detect() = (%q, %v), 期望 (%q, %v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

// writeFile 写一个文件（父目录已存在）。
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("写 %s 失败: %v", path, err)
	}
}

// TestCheckFlagsBinaryInTrackedList 判据命中被跟踪的二进制。
func TestCheckFlagsBinaryInTrackedList(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tool"), []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00})
	writeFile(t, filepath.Join(dir, "readme.md"), []byte("# 文档\n"))

	bad, err := check(dir, []string{"tool", "readme.md"})
	if err != nil {
		t.Fatalf("check 报错: %v", err)
	}
	if len(bad) != 1 {
		t.Fatalf("应恰好命中 1 个二进制（实得 %d: %+v）", len(bad), bad)
	}
	if bad[0].path != "tool" || bad[0].format != "ELF 可执行文件" {
		t.Fatalf("命中结论应指明路径与格式（实得 %+v）", bad[0])
	}
}

// TestCheckSkipsMissingFile 已删除但仍被跟踪的文件跳过（内容无从判）。
func TestCheckSkipsMissingFile(t *testing.T) {
	dir := t.TempDir()
	bad, err := check(dir, []string{"已删除.go"})
	if err != nil {
		t.Fatalf("check 报错: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("缺失文件不应被判为违规（实得 %+v）", bad)
	}
}

// TestReportNamesOffender 报告必须点名路径与格式，并给出修法。
func TestReportNamesOffender(t *testing.T) {
	var buf bytes.Buffer
	if report(&buf, 3, []offender{{path: "docsync", format: "Mach-O 64 位"}}) {
		t.Fatal("有违规时 report 不得报通过")
	}
	for _, want := range []string{"docsync", "Mach-O 64 位", ".gitignore"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("报告应含 %q（实得 %q）", want, buf.String())
		}
	}
}

// TestReportCleanPass 通过时的报告。
func TestReportCleanPass(t *testing.T) {
	var buf bytes.Buffer
	if !report(&buf, 12, nil) {
		t.Fatal("零违规时 report 应报通过")
	}
	if !strings.Contains(buf.String(), "12") {
		t.Errorf("通过报告应带被检查文件数（实得 %q）", buf.String())
	}
}

// TestRunFlagsBinaryInTempRepo 反向验证：把一个二进制加进索引 ⇒ 门禁转红。
//
// 这一条是本门禁的「坏实现会红」对照：判据若跑不起来却报绿（git 不可用、
// 路径拼错、只看扩展名），本用例就绿着过——所以它必须真的红。
func TestRunFlagsBinaryInTempRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git 不可用，跳过: %v", err)
	}
	writeFile(t, filepath.Join(dir, "build-output"), []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00})
	// -f：绕开任何全局 ignore 规则，确保二进制真的进了索引。
	if out, err := exec.Command("git", "-C", dir, "add", "-f", "build-output").CombinedOutput(); err != nil {
		t.Fatalf("git add 失败: %v（%s）", err, out)
	}

	var buf bytes.Buffer
	if code := run(dir, &buf); code == 0 {
		t.Fatalf("索引里有二进制时门禁必须红（退出码 0，报告 %q）", buf.String())
	}
	if !strings.Contains(buf.String(), "build-output") {
		t.Errorf("红点必须点名违规文件（实得 %q）", buf.String())
	}
}

// TestRunGreenOnRepoWithoutBinary 反向验证的另一半：索引干净 ⇒ 门禁绿。
func TestRunGreenOnRepoWithoutBinary(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git 不可用，跳过: %v", err)
	}
	writeFile(t, filepath.Join(dir, "main.go"), []byte("package main\n"))
	if out, err := exec.Command("git", "-C", dir, "add", "main.go").CombinedOutput(); err != nil {
		t.Fatalf("git add 失败: %v（%s）", err, out)
	}

	var buf bytes.Buffer
	if code := run(dir, &buf); code != 0 {
		t.Fatalf("索引干净时门禁必须绿（退出码 %d，报告 %q）", code, buf.String())
	}
}

// TestRunFlagsForceAddedIgnoredFile 反向验证：**文本**产物被 `git add -f`
// 强加进库 ⇒ 门禁转红（§16 第八阶段建议 3：bin/ 与覆盖配置的位置）。
//
// 这一条钉的是魔数判据的盲区：coverage.out 是文本，魔数永远不响；
// 判据改回「只判二进制」时本用例即红。
func TestRunFlagsForceAddedIgnoredFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git 不可用，跳过: %v", err)
	}
	// 覆盖配置：.gitignore 收着，但 -f 能绕过去——这正是判据要拦的形态。
	writeFile(t, filepath.Join(dir, ".gitignore"), []byte("/coverage.out\n"))
	writeFile(t, filepath.Join(dir, "coverage.out"), []byte("mode: set\n"))
	if out, err := exec.Command("git", "-C", dir, "add", ".gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git add .gitignore 失败: %v（%s）", err, out)
	}
	// -f：忽略规则挡不住强加——这正是判据要拦的形态。
	if out, err := exec.Command("git", "-C", dir, "add", "-f", "coverage.out").CombinedOutput(); err != nil {
		t.Fatalf("git add -f 失败: %v（%s）", err, out)
	}

	var buf bytes.Buffer
	if code := run(dir, &buf); code == 0 {
		t.Fatalf("强加了命中 .gitignore 的文件时门禁必须红（退出码 0，报告 %q）", buf.String())
	}
	if !strings.Contains(buf.String(), "coverage.out") {
		t.Errorf("红点必须点名被强加的文件（实得 %q）", buf.String())
	}
}

// TestRunGreenWhenIgnoredFileNotTracked 上一条的另一半：忽略规则在、但文件没进
// 索引 ⇒ 绿（本地构建产物留在工作树里是正确状态，判「文件存在」就是噪声）。
func TestRunGreenWhenIgnoredFileNotTracked(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git 不可用，跳过: %v", err)
	}
	writeFile(t, filepath.Join(dir, ".gitignore"), []byte("/coverage.out\n"))
	writeFile(t, filepath.Join(dir, "coverage.out"), []byte("mode: set\n"))
	writeFile(t, filepath.Join(dir, "main.go"), []byte("package main\n"))
	for _, p := range []string{".gitignore", "main.go"} {
		if out, err := exec.Command("git", "-C", dir, "add", p).CombinedOutput(); err != nil {
			t.Fatalf("git add %s 失败: %v（%s）", p, err, out)
		}
	}

	var buf bytes.Buffer
	if code := run(dir, &buf); code != 0 {
		t.Fatalf("只在工作树里的忽略产物不得判红（退出码 %d，报告 %q）", code, buf.String())
	}
}

// TestRunFailsWhenGitUnavailable git 不可用时**明确失败**，不静默跳过。
//
// 判据跑不起来却报绿，是最贵的失效形态——所以这一条钉住「非零退出 + 报错」，
// 而不是「没扫到文件 ⇒ 算通过」。
func TestRunFailsWhenGitUnavailable(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if code := run(dir, &buf); code == 0 {
		t.Fatalf("git 不可用时门禁必须红（退出码 0，报告 %q）", buf.String())
	}
	if !strings.Contains(buf.String(), "git") {
		t.Errorf("红点应说明是 git 这一环失败（实得 %q）", buf.String())
	}
}

// TestRunOnThisRepo 本仓自校验：当前工作树不得有被跟踪的编译产物。
//
// 这是 #413 的直接落点——docsync 曾被提交进库（b469d8b），本用例会在那种
// 状态下转红。
func TestRunOnThisRepo(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Skipf("不在 git 仓库里，跳过: %v", err)
	}
	var buf bytes.Buffer
	if code := run(root, &buf); code != 0 {
		t.Fatalf("本仓不得跟踪编译产物二进制（退出码 %d）：%s", code, buf.String())
	}
}

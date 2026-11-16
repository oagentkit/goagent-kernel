package main

// run_test.go —— run() 的端到端用例（真 git 仓，t.Chdir 切进去）。
//
// 为什么必须真跑 git：run() 的判据是「git 事实 + CHANGELOG 内容」的组合，
// 纯函数用例证明不了「读出来的事实对不对」（比如 tag 是不是 annotated、
// 区间取的是哪个 prev tag）。这些恰是上一版靠人手工核对的部分。
//
// 判据风格沿用本仓：每条**应红**的场景各一个用例（坏实现必红），
// 外加一条全合规的对照（否则「全拒」的实现也能过）。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v: %s", args, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "t@example.com")
	gitRun(t, dir, "config", "user.name", "t")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	gitRun(t, dir, "config", "tag.gpgsign", "false")
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("写 %s 失败: %v", name, err)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", msg)
}

// TestRunSkipsWithoutExactTag 非发版提交必须显式跳过（否则本地/PR 上必红）。
func TestRunSkipsWithoutExactTag(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n")
	commitAll(t, dir, "chore: init")
	t.Chdir(dir)
	if err := run(); err != nil {
		t.Fatalf("无 exact tag 时应跳过而不是报错，实得 %v", err)
	}
}

// TestRunPassesCompliantRelease 全合规的对照用例：
// 没有这条，「全拒」的实现也能让前面几条应红的用例通过。
func TestRunPassesCompliantRelease(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n\n## [v0.1.0] — 2026-01-01\n\n- 内容\n")
	commitAll(t, dir, "feat: init")
	gitRun(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	t.Chdir(dir)
	if err := run(); err != nil {
		t.Fatalf("合规发版应当通过，实得 %v", err)
	}
}

func TestRunRejectsMissingSection(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n\n## [v0.0.9] — 旧版本\n")
	commitAll(t, dir, "feat: init")
	gitRun(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	t.Chdir(dir)
	err := run()
	if err == nil || !strings.Contains(err.Error(), "发版纪律未满足") {
		t.Fatalf("缺本版段必须报错，实得 %v", err)
	}
}

func TestRunRejectsReplacementChar(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n\n## [v0.1.0] — x\n\n自动派\uFFFD\uFFFD签名\n")
	commitAll(t, dir, "feat: init")
	gitRun(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	t.Chdir(dir)
	if err := run(); err == nil {
		t.Fatal("CHANGELOG 含 U+FFFD 必须报错")
	}
}

func TestRunRejectsLightweightTag(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n\n## [v0.1.0] — x\n")
	commitAll(t, dir, "feat: init")
	gitRun(t, dir, "tag", "v0.1.0") // 轻量 tag
	t.Chdir(dir)
	if err := run(); err == nil {
		t.Fatal("轻量 tag 必须报错（它承载不了发布说明）")
	}
}

// TestRunRejectsMixedCommit 依赖文件与非依赖文件混装的提交必须被区间扫描抓到。
func TestRunRejectsMixedCommit(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n\n## [v0.2.0] — y\n\n## [v0.1.0] — x\n")
	commitAll(t, dir, "feat: init")
	gitRun(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package a\n")
	commitAll(t, dir, "chore(deps): 抬地板并顺手改了代码")
	gitRun(t, dir, "tag", "-a", "v0.2.0", "-m", "release")
	t.Chdir(dir)
	if err := run(); err == nil {
		t.Fatal("混装提交必须报错（否则行为变更会藏进依赖段）")
	}
}

// TestRunAllowsDepOnlyCommit 纯依赖提交不得误伤。
func TestRunAllowsDepOnlyCommit(t *testing.T) {
	dir := initRepo(t)
	write(t, dir, "CHANGELOG.md", "# CHANGELOG\n\n## [v0.2.0] — y\n\n## [v0.1.0] — x\n")
	commitAll(t, dir, "feat: init")
	gitRun(t, dir, "tag", "-a", "v0.1.0", "-m", "release")
	write(t, dir, "go.mod", "module x\n")
	commitAll(t, dir, "chore(deps): 只抬地板")
	gitRun(t, dir, "tag", "-a", "v0.2.0", "-m", "release")
	t.Chdir(dir)
	if err := run(); err != nil {
		t.Fatalf("纯依赖提交不应报错，实得 %v", err)
	}
}

// TestGitOutPropgatesFailure git 失败要能被上层看见（不能被当成「没匹配上」）。
func TestGitOutPropgatesFailure(t *testing.T) {
	dir := initRepo(t)
	t.Chdir(dir)
	if _, err := gitOut("definitely-not-a-git-subcommand"); err == nil {
		t.Fatal("非法 git 子命令必须返回错误")
	}
}

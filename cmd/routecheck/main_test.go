package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ledgerText 一份最小台账（登记面 + 状态列）。
func ledgerText(issue262Status string) string {
	return strings.Join([]string{
		"## 9. issue 台账",
		"",
		"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
		"|---|---|---|---|---|",
		"| #261 | L1 的单 | L1 | 判据 | ✅ 已合入 (#331) |",
		"| #262 | L2 的单 | L2 | 判据 | " + issue262Status + " |",
		"| #279 | 总纲单 | 全程 | 判据 | 🔴 仍开着 |",
		"",
	}, "\n")
}

// registryText 一份合规的路线登记面。
func registryText() string {
	return strings.Join([]string{
		"updated: 2026-11-24",
		"overview_issue: 279",
		"stages:",
		"  - id: L1",
		"    title: 身份与租约闭环",
		"    issues: [261, 262]",
		"    exit_criteria: 动作身份唯一；超 TTL 后旧持有者的提交被拒绝",
		"",
	}, "\n")
}

// newRepo 搭一个临时仓：登记面 + 台账。
func newRepo(t *testing.T, registry, ledger string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatalf("建 docs 失败：%v", err)
	}
	if registry != "" {
		if err := os.WriteFile(filepath.Join(dir, "docs", "ROUTE-LEDGER.yaml"),
			[]byte(registry), 0o600); err != nil {
			t.Fatalf("写登记面失败：%v", err)
		}
	}
	if ledger != "" {
		if err := os.WriteFile(filepath.Join(dir, "docs", "DISTRIBUTED-EVOLUTION-20261117.md"),
			[]byte(ledger), 0o600); err != nil {
			t.Fatalf("写台账失败：%v", err)
		}
	}
	return dir
}

// 退出码 0：登记齐、成员都达成。
func TestRun_合规登记退出零(t *testing.T) {
	dir := newRepo(t, registryText(), ledgerText("✅ 已合入 (#331)"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("合规登记应退出 0，实际 %d；stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "路线达成对账通过") {
		t.Fatalf("stdout 应有通过结论，实际：%s", stdout.String())
	}
}

// 退出码 1：成员仍开着 ⇒ 红。
func TestRun_成员未达成退出壹(t *testing.T) {
	dir := newRepo(t, registryText(), ledgerText("🔴 仍开着"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("成员未达成应退出 1，实际 %d；stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "#262") {
		t.Fatalf("stderr 应点名未达成的成员，实际：%s", stderr.String())
	}
}

// 退出码 2：登记面读不到 ⇒ 给不出结论（不是「没有违反」）。
func TestRun_登记面读不到退出贰(t *testing.T) {
	dir := newRepo(t, "", ledgerText("✅ 已合入 (#331)"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("登记面读不到应退出 2，实际 %d；stderr=%s", code, stderr.String())
	}
}

// 退出码 2：台账读不到 ⇒ 给不出结论。
func TestRun_台账读不到退出贰(t *testing.T) {
	dir := newRepo(t, registryText(), "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("台账读不到应退出 2，实际 %d；stderr=%s", code, stderr.String())
	}
}

// 严格模式：登记面里出现未定义的键 ⇒ 报错（默默忽略 = 以为登记了其实没登记）。
func TestRun_未知键报错(t *testing.T) {
	bad := registryText() + "    typo_field: x\n"
	dir := newRepo(t, bad, ledgerText("✅ 已合入 (#331)"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("登记面有未知键应退出 2，实际 %d；stderr=%s", code, stderr.String())
	}
}

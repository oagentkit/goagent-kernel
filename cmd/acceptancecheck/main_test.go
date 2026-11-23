package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registryBody 造一份登记面。
func registryBody(body string) string {
	return strings.Join([]string{
		"updated: 2026-11-23",
		"paths:",
		body,
	}, "\n")
}

// newRepo 搭一个临时仓：Makefile + 登记面 + 证据文件。
func newRepo(t *testing.T, registry string, evidence string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"),
		[]byte("bench: ## 基线  #acceptance-path: bench-baseline\n\t@echo bench\n"), 0o600); err != nil {
		t.Fatalf("写 Makefile 失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatalf("建 docs 失败：%v", err)
	}
	if registry != "" {
		if err := os.WriteFile(filepath.Join(dir, "docs", "acceptance-paths.yaml"),
			[]byte(registry), 0o600); err != nil {
			t.Fatalf("写登记面失败：%v", err)
		}
	}
	if evidence != "" {
		if err := os.WriteFile(filepath.Join(dir, "docs", "bench.md"),
			[]byte(evidence), 0o600); err != nil {
			t.Fatalf("写证据文件失败：%v", err)
		}
	}
	return dir
}

const goodEvidence = "acceptance-path-id: bench-baseline\n" +
	"acceptance-path-ran-at: 2026-11-13\n" +
	"acceptance-path-conclusion: pass\n"

// 退出码 0：登记齐、证据齐、入口齐。
func TestRun_合规登记退出零(t *testing.T) {
	dir := newRepo(t, registryBody(
		"  - id: bench-baseline\n    command: make bench\n    evidence: docs/bench.md\n"+
			"    ran_at: 2026-11-13\n    conclusion: pass"), goodEvidence)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("合规登记应退出 0，实际 %d；stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "验收路径对账通过") {
		t.Fatalf("stdout 应有通过结论，实际：%s", stdout.String())
	}
}

// 退出码 1：登记了却没证据 ⇒ 显形点名。
func TestRun_证据缺失退出壹(t *testing.T) {
	dir := newRepo(t, registryBody(
		"  - id: bench-baseline\n    command: make bench\n    evidence: docs/bench.md\n"+
			"    ran_at: 2026-11-13\n    conclusion: pass"), "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("证据缺失应退出 1，实际 %d；stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "从未被执行过") {
		t.Fatalf("stdout 应点名未跑过的路径，实际：%s", stdout.String())
	}
}

// 退出码 2：登记面读不到 ⇒ 给不出结论（不是「没有违反」）。
func TestRun_登记面读不到退出贰(t *testing.T) {
	dir := newRepo(t, "", goodEvidence)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("登记面读不到应退出 2，实际 %d；stderr=%s", code, stderr.String())
	}
}

// 严格模式：登记面里出现未定义的键 ⇒ 报错（默默忽略 = 以为登记了其实没登记）。
func TestRun_未知键报错(t *testing.T) {
	dir := newRepo(t, registryBody(
		"  - id: bench-baseline\n    command: make bench\n    evidence: docs/bench.md\n"+
			"    ran_at: 2026-11-13\n    conclusion: pass\n    typo_field: x"), goodEvidence)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("登记面有未知键应退出 2，实际 %d；stderr=%s", code, stderr.String())
	}
}

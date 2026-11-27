// main_test.go —— 命令行接线的可执行判据（临时仓 fixture，不联网）。
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/internal/invariantledger"
)

// writeFixture 铺一座最小临时仓：登记面 + 一个含单测试的包。
//
// ref 是登记面引用的唯一测试指针（形如 fixture.TestOk）；withTest 控制
// 仓里是否真的存在它。
func writeFixture(t *testing.T, withTest bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.WriteString("# 执行不变量与插点矩阵\n\nupdated: 2026-11-27\n\n")
	b.WriteString("| id | 陈述 | 判定机制 | 成文处 | 必红判据 | 缺口 |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, id := range invariantledger.RequiredInvariants {
		b.WriteString("| " + id + " | s | m | docs/x.md | `fixture.TestOk` | 无 |\n")
	}
	b.WriteString("\n| 间隙 | F1 崩溃 | F2 双副本 | F3 响应丢失 | F4 租约过期+旧恢复 | F5 事件丢失 | F6 双终态 | 豁免/备注 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, id := range invariantledger.RequiredGaps {
		b.WriteString("| " + id + " | `fixture.TestOk` | — | — | — | — | — | — |\n")
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "INVARIANTS.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if withTest {
		src := "package fixture\n\nimport \"testing\"\n\nfunc TestOk(t *testing.T) {}\n"
		if err := os.WriteFile(filepath.Join(root, "fixture", "foo_test.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		// 空包（只有生产文件）让 buildTestIndex 仍能扫到别处测试——此处全仓
		// 无测试会走「空索引」错误分支，所以留一个不匹配指针名的测试。
		src := "package fixture\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n"
		if err := os.WriteFile(filepath.Join(root, "fixture", "foo_test.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runWith 在 fixture 上跑一次命令，返回退出码与两路输出。
func runWith(t *testing.T, root string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run([]string{"-root", root}, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestRunGreen 全绿 fixture ⇒ 退出 0，成功行带计数。
func TestRunGreen(t *testing.T) {
	root := writeFixture(t, true)
	code, out, errOut := runWith(t, root)
	if code != 0 {
		t.Fatalf("应通过，退出码 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(out, "不变量绑定对账通过") {
		t.Fatalf("成功行缺失，实得：%q", out)
	}
}

// TestRunMissingTestRef 指针指向的测试不存在 ⇒ 退出 1 并点名。
func TestRunMissingTestRef(t *testing.T) {
	root := writeFixture(t, false)
	code, _, errOut := runWith(t, root)
	if code != 1 {
		t.Fatalf("应判违反（1），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "fixture.TestOk") {
		t.Fatalf("必须点名缺失指针，实得：\n%s", errOut)
	}
}

// TestRunMissingDoc 登记面读不到 ⇒ 退出 2（给不出结论，不是判红）。
func TestRunMissingDoc(t *testing.T) {
	code, _, errOut := runWith(t, t.TempDir())
	if code != 2 {
		t.Fatalf("应为给不出结论（2），实得 %d", code)
	}
	if !strings.Contains(errOut, "读不到登记面") {
		t.Fatalf("错误要说清读不到什么，实得：\n%s", errOut)
	}
}

// TestRunBrokenDoc 结构坏的登记面 ⇒ 退出 2（解析错误与判红是两件事）。
func TestRunBrokenDoc(t *testing.T) {
	root := writeFixture(t, true)
	doc := filepath.Join(root, "docs", "INVARIANTS.md")
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), "updated: 2026-11-27", "updated: not-a-date", 1)
	if err := os.WriteFile(doc, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runWith(t, root)
	// 版本戳行在（只是值歪）⇒ 解析成功、Judge 判形状红 ⇒ 1。
	if code != 1 {
		t.Fatalf("歪版本戳应由 Judge 判红（1），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "Updated") {
		t.Fatalf("应点名版本戳，实得：\n%s", errOut)
	}
}

// TestBuildTestIndexSkipsHidden 扫描跳过隐藏目录与 vendor（与包图口径同向）。
func TestBuildTestIndexSkipsHidden(t *testing.T) {
	root := writeFixture(t, true)
	for _, dir := range []string{".git", "vendor"} {
		p := filepath.Join(root, dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package x\n\nimport \"testing\"\n\nfunc TestHidden(t *testing.T) {}\n"
		if err := os.WriteFile(filepath.Join(p, "x_test.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := buildTestIndex(root)
	if err != nil {
		t.Fatalf("扫仓失败：%v", err)
	}
	if idx["fixture.TestOk"] != true {
		t.Fatalf("fixture.TestOk 应在索引里，实得 %v", idx)
	}
	for k := range idx {
		if strings.HasPrefix(k, ".git") || strings.HasPrefix(k, "vendor") {
			t.Fatalf("隐藏/vendor 目录不该进索引：%s", k)
		}
	}
}

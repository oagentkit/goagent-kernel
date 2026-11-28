// maturity_test.go —— 成熟度盘点判据的可执行判据（合成数据，不读盘）。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseRows 在合成盘点面上跑解析。
func parseRows(t *testing.T, body string) []maturityRow {
	t.Helper()
	rows, updated, err := parseMaturity(body)
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if updated != "2026-11-28" {
		t.Fatalf("版本戳应为 2026-11-28，实得 %q", updated)
	}
	return rows
}

// TestParseMaturityFull 六列行全解析对。
func TestParseMaturityFull(t *testing.T) {
	rows := parseRows(t, miniMaturity)
	if len(rows) != 2 {
		t.Fatalf("应 2 行，实得 %d", len(rows))
	}
	if rows[0].Pkg != "beta" || rows[0].Class != classKeep {
		t.Fatalf("首行解析错：%+v", rows[0])
	}
	if rows[0].Basis == "" {
		t.Fatal("依据格应非空")
	}
}

// TestParseMaturityMissingTable 缺表 ⇒ 解析错误（给不出结论）。
func TestParseMaturityMissingTable(t *testing.T) {
	if _, _, err := parseMaturity("# 空文档\n\nupdated: 2026-11-28\n"); err == nil ||
		!strings.Contains(err.Error(), "盘点表") {
		t.Fatalf("缺表应报错并点名，实得：%v", err)
	}
}

// TestCheckMaturityGreen 全绿夹具：零问题。
func TestCheckMaturityGreen(t *testing.T) {
	root := writeMiniRepo(t)
	m, err := LoadManifest(filepath.Join(root, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	probs, err := checkMaturity(root, m)
	if err != nil {
		t.Fatalf("不该给不出结论：%v", err)
	}
	if len(probs) != 0 {
		t.Fatalf("全绿夹具不该有问题，实得：%v", probs)
	}
}

// TestCheckMaturityMissingRow 漏盘一个 Experimental 包 ⇒ 红并点名。
func TestCheckMaturityMissingRow(t *testing.T) {
	root := writeMiniRepo(t)
	// 摘掉 internal/deep 行
	body := readFile(t, root, maturityName)
	body = strings.Replace(body, "| internal/deep | 维持演进 | — | — | — | 夹具——嵌套包示例 |\n", "", 1)
	writeFile(t, root, maturityName, body)
	m, _ := LoadManifest(filepath.Join(root, manifestName))
	probs, err := checkMaturity(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProb(probs, "internal/deep 没有盘点行") {
		t.Fatalf("漏盘点必须点名，实得：%v", probs)
	}
}

// TestCheckMaturityPromoteNeedsThreeEvidence 晋级候选缺证据格 ⇒ 红并点名缺哪格。
func TestCheckMaturityPromoteNeedsThreeEvidence(t *testing.T) {
	root := writeMiniRepo(t)
	body := readFile(t, root, maturityName)
	body = strings.Replace(body,
		"| beta | 维持演进 | — | — | — | 夹具——仍在演进示例 |",
		"| beta | 晋级候选 | 一个 minor 无 breaking | — | 契约套件 | 夹具——候选示例 |", 1)
	writeFile(t, root, maturityName, body)
	m, _ := LoadManifest(filepath.Join(root, manifestName))
	probs, err := checkMaturity(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProb(probs, "宿主接入") {
		t.Fatalf("晋级候选缺宿主接入证据必须报，实得：%v", probs)
	}
}

// TestCheckMaturityRowForStable 晋级后没撤行（行指向 Stable 包）⇒ 红。
func TestCheckMaturityRowForStable(t *testing.T) {
	root := writeMiniRepo(t)
	body := readFile(t, root, maturityName)
	body = strings.Replace(body,
		"| internal/deep | 维持演进 | — | — | — | 夹具——嵌套包示例 |",
		"| alpha | 维持演进 | — | — | — | 夹具——已晋级没撤行 |", 1)
	writeFile(t, root, maturityName, body)
	m, _ := LoadManifest(filepath.Join(root, manifestName))
	probs, err := checkMaturity(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProb(probs, "不是清单里的 Experimental") {
		t.Fatalf("非 Experimental 行必须报，实得：%v", probs)
	}
}

// TestCheckMaturityBadClass 分类值域外 ⇒ 红。
func TestCheckMaturityBadClass(t *testing.T) {
	root := writeMiniRepo(t)
	body := readFile(t, root, maturityName)
	body = strings.Replace(body, "| beta | 维持演进 |", "| beta | 半弃用 |", 1)
	writeFile(t, root, maturityName, body)
	m, _ := LoadManifest(filepath.Join(root, manifestName))
	probs, err := checkMaturity(root, m)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProb(probs, "半弃用") {
		t.Fatalf("值域外分类必须点名，实得：%v", probs)
	}
}

// TestCheckMaturityMissingFile 读不到盘点面 ⇒ 给不出结论（error）。
func TestCheckMaturityMissingFile(t *testing.T) {
	root := writeMiniRepo(t)
	if err := removeFile(t, root, maturityName); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadManifest(filepath.Join(root, manifestName))
	if _, err := checkMaturity(root, m); err == nil || !strings.Contains(err.Error(), "读不到") {
		t.Fatalf("缺文件应给不出结论，实得：%v", err)
	}
}

// readFile 读夹具文件。
func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// removeFile 删夹具文件。
func removeFile(t *testing.T, root, rel string) error {
	t.Helper()
	return os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
}

// hasProb 判问题列表里有没有包含子串的条目。
func hasProb(probs []string, sub string) bool {
	for _, p := range probs {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// main_test.go —— CLI 装配与边界路径的用例。
//
// 边界路径也要有判据：判据门禁最容易的失效形态是「错误路径没人走」
// ⇒ 一条都判不了却照常报绿。
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunPasses 正常形态：退出码 0，且把派生说明打给人看。
func TestRunPasses(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	var stdout, stderr bytes.Buffer
	if rc := run([]string{root}, &stdout, &stderr); rc != 0 {
		t.Fatalf("正常形态应退出 0，实得 %d；stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "说明") {
		t.Fatalf("必须打出派生说明，实得 %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "✓") {
		t.Fatalf("必须打出通过标记，实得 %q", stdout.String())
	}
}

// TestRunReportsViolations 有违反时：退出码 1，违反原文进 stderr。
func TestRunReportsViolations(t *testing.T) {
	m := strings.Replace(demoMatrix, "storage: demo-store", "storage: demo-ghost", 1)
	root := writeRoot(t, m, demoSelection)
	var stdout, stderr bytes.Buffer
	if rc := run([]string{root}, &stdout, &stderr); rc != 1 {
		t.Fatalf("有违反应退出 1，实得 %d", rc)
	}
	if !strings.Contains(stderr.String(), "不在选型声明面的允许集内") {
		t.Fatalf("违反原文必须进 stderr，实得 %q", stderr.String())
	}
}

// TestRunDefaultsToCurrentDir 不给参数时以当前目录为仓根。
func TestRunDefaultsToCurrentDir(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	t.Chdir(root)
	var stdout, stderr bytes.Buffer
	if rc := run(nil, &stdout, &stderr); rc != 0 {
		t.Fatalf("应以当前目录为仓根，实得 %d；stderr=%s", rc, stderr.String())
	}
}

// TestRunErrorsWhenRootMissing 仓根不存在 ⇒ 报错退出（不是静默绿）。
func TestRunErrorsWhenRootMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nope")
	var stdout, stderr bytes.Buffer
	if rc := run([]string{root}, &stdout, &stderr); rc != 1 {
		t.Fatalf("仓根缺失应退出 1，实得 %d", rc)
	}
	if !strings.Contains(stderr.String(), "读声明面失败") {
		t.Fatalf("必须报出读声明面失败，实得 %q", stderr.String())
	}
}

// TestCheckRejectsSelectionVersionMismatch 声明面版本不对 ⇒ 红。
func TestCheckRejectsSelectionVersionMismatch(t *testing.T) {
	s := strings.Replace(demoSelection, "version: 1", "version: 2", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "version 必须为 1")
}

// TestCheckRejectsDuplicateStorage 同一个存储登记两次 ⇒ 红。
func TestCheckRejectsDuplicateStorage(t *testing.T) {
	s := strings.Replace(demoSelection, "  - id: demo-other", "  - id: demo-store", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "重复登记")
}

// TestCheckRejectsUnknownRequiredCapability 场景要求了能力表里没有的能力 ⇒ 红。
func TestCheckRejectsUnknownRequiredCapability(t *testing.T) {
	m := strings.Replace(demoMatrix, "required: [cap-persist]", "required: [cap-ghost]", 1)
	root := writeRoot(t, m, demoSelection)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "不在部署矩阵的能力表里")
}

// TestCheckRejectsBadEvidenceKind 证据类型不是 contract ⇒ 红。
func TestCheckRejectsBadEvidenceKind(t *testing.T) {
	s := strings.Replace(demoSelection, "          - kind: contract", "          - kind: doc", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "证据类型只允许 contract")
}

// TestCheckRejectsMissingSource 能力断言缺 source 标注 ⇒ 红。
func TestCheckRejectsMissingSource(t *testing.T) {
	s := strings.Replace(demoSelection, "        source: storage\n", "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "缺 source 标注")
}

// TestCheckRejectsUnknownSource source 取值不在词表内 ⇒ 红。
func TestCheckRejectsUnknownSource(t *testing.T) {
	s := strings.Replace(demoSelection, "source: storage", "source: magic", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "只允许")
}

// TestCheckErrorsOnUnknownFieldInSelection 声明面里出现未知字段 ⇒ 解析报错。
//
// 字段名拼错会让整段声明**读不进去**（读不进去 = 当成没写）⇒ 必须当场红，
// 不能当成「没这条声明」放过。
func TestCheckErrorsOnUnknownFieldInSelection(t *testing.T) {
	s := demoSelection + "surprise_field: 1\n"
	root := writeRoot(t, demoMatrix, s)
	_, _, err := check(root)
	if err == nil {
		t.Fatal("未知字段必须报错（声明面里拼错的字段名会让整段声明静默失效）")
	}
	if !strings.Contains(err.Error(), "解析声明面失败") {
		t.Fatalf("报错要说清是解析失败，实得 %v", err)
	}
}

// TestCheckErrorsWhenMatrixMissing 部署矩阵缺失 ⇒ 报错（不是静默绿）。
func TestCheckErrorsWhenMatrixMissing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "STORAGE-SELECTION.yaml"), demoSelection)
	_, _, err := check(root)
	if err == nil {
		t.Fatal("矩阵缺失必须报错")
	}
	if !strings.Contains(err.Error(), "读声明面失败") {
		t.Fatalf("报错要说清是读失败，实得 %v", err)
	}
}

// TestCheckErrorsWhenSelectionMissing 选型声明面缺失 ⇒ 报错。
func TestCheckErrorsWhenSelectionMissing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs", "DEPLOY-MATRIX.yaml"), demoMatrix)
	_, _, err := check(root)
	if err == nil {
		t.Fatal("声明面缺失必须报错")
	}
	if !strings.Contains(err.Error(), "读声明面失败") {
		t.Fatalf("报错要说清是读失败，实得 %v", err)
	}
}

// TestUnderRoot 落点归类：只有代码根之下才算「自证」。
func TestUnderRoot(t *testing.T) {
	cases := []struct {
		root string
		path string
		want bool
	}{
		{"pkgdemo", "pkgdemo/store.go", true},
		{"pkgdemo", "pkgdemo", true},
		{"pkgdemo", "pkgdemo2/store.go", false},
		{"pkgdemo", "other/store.go", false},
		{"", "pkgdemo/store.go", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := underRoot(c.root, c.path); got != c.want {
			t.Fatalf("underRoot(%q, %q) = %v, 期望 %v", c.root, c.path, got, c.want)
		}
	}
}

// TestResolveRefRejectsBadShapes 落点形态不对 ⇒ 判不成立且给出理由。
func TestResolveRefRejectsBadShapes(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	if ok, reason := resolveRef(root, "pkgdemo/store.go"); ok || reason == "" {
		t.Fatal("缺 #Symbol 必须判不成立且给出理由")
	}
	if ok, _ := resolveRef(root, "pkgdemo/store.go#Store"); !ok {
		t.Fatal("合法落点必须判成立")
	}
}

// TestDeclaredSymbolsOnUnparsableFile 解析不了的文件 ⇒ 报错，不当「符号不存在」。
func TestDeclaredSymbolsOnUnparsableFile(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.go")
	if err := os.WriteFile(bad, []byte("package x\nfunc (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := declaredSymbols(bad); err == nil {
		t.Fatal("解析失败必须报错（不能当成「符号不存在」——那是两种不同的结论）")
	}
}

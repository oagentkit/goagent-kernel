// main_test.go —— deploymatrix 的可执行判据（issue #277）。
//
// 用 t.TempDir 造一个最小仓库：清单 / USAGE 机器口径块 / Makefile / 证据
// 文件。每个用例只改一处，断言「坏实现会红」——只测「好实现能过」的用例，
// 一个把 state 写死成 available 的实现照样全过。
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validMatrix 最小合法清单：一项 available（contract 证据）、一项 partial
// （contract + issue），一个场景依赖前者。
const validMatrix = `version: 1
capabilities:
  - id: alpha-cap
    title: Alpha 能力
    state: available
    evidence:
      - kind: contract
        ref: alpha/alpha.go#Alpha
  - id: beta-cap
    title: Beta 能力
    state: partial
    evidence:
      - kind: contract
        ref: alpha/alpha.go#Alpha.Do
      - kind: issue
        ref: "#1"
scenarios:
  - id: alpha-scenario
    title: Alpha 场景
    storage: memory
    worker: single-process
    required: [alpha-cap]
    readiness: available
`

const validUsage = `# USAGE

<!-- deploy-matrix:begin -->
| 场景 | 推荐存储 | Worker 模式 | 必需能力 | 当前状态 |
|---|---|---|---|---|
| ` + "`alpha-scenario`" + ` Alpha 场景 | memory | single-process | Alpha 能力 | available |
<!-- deploy-matrix:end -->
`

const validAlpha = `package alpha

type Alpha struct{}

func (a Alpha) Do() {}
`

const validMakefile = "test:\n\t@echo ok\n"

// writeRepo 造最小仓库；overrides 覆盖默认文件。
func writeRepo(t *testing.T, overrides map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"docs/DEPLOY-MATRIX.yaml": validMatrix,
		"docs/USAGE.md":           validUsage,
		"docs/claims.md":          "见 #1 与 #273。\n",
		"Makefile":                validMakefile,
		"alpha/alpha.go":          validAlpha,
	}
	for p, body := range overrides {
		files[p] = body
	}
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runCheck 跑判定并返回退出码；err 视为测试失败（给不出结论也是坏形态）。
func runCheck(t *testing.T, root string) int {
	t.Helper()
	code, err := run(root, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return code
}

// TestValidMatrixPasses 合法清单必须过（否则判据只是噪声）。
func TestValidMatrixPasses(t *testing.T) {
	root := writeRepo(t, nil)
	if code := runCheck(t, root); code != 0 {
		t.Fatalf("合法清单应通过，退出码 %d", code)
	}
}

// TestStateMustDeriveFromEvidence 手写 state 与证据不符 ⇒ 红。
func TestStateMustDeriveFromEvidence(t *testing.T) {
	bad := strings.Replace(validMatrix, "state: partial", "state: available", 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("state 谎报必须红，退出码 %d", code)
	}
}

// TestContractSymbolMustExist 契约证据指向不存在的符号 ⇒ 红。
func TestContractSymbolMustExist(t *testing.T) {
	bad := strings.Replace(validMatrix, "alpha/alpha.go#Alpha.Do", "alpha/alpha.go#Missing", 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("失效的契约符号必须红，退出码 %d", code)
	}
}

// TestIssueMustHaveLocalLanding 单号在 docs/CHANGELOG 里查不到 ⇒ 红（引用失效）。
func TestIssueMustHaveLocalLanding(t *testing.T) {
	bad := strings.Replace(validMatrix, `ref: "#1"`, `ref: "#999999"`, 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("查不到落点的单号必须红，退出码 %d", code)
	}
}

// TestGateTargetMustExist gate 证据指向不存在的 Makefile 目标 ⇒ 红。
func TestGateTargetMustExist(t *testing.T) {
	bad := strings.Replace(validMatrix,
		"      - kind: contract\n        ref: alpha/alpha.go#Alpha\n",
		"      - kind: gate\n        ref: no-such-target\n", 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("不存在的门禁目标必须红，退出码 %d", code)
	}
}

// TestReadinessMustMatchWorstCapability 场景就绪态不等于必需能力最差者 ⇒ 红。
func TestReadinessMustMatchWorstCapability(t *testing.T) {
	bad := strings.Replace(validMatrix, "required: [alpha-cap]", "required: [alpha-cap, beta-cap]", 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("readiness 与必需能力不符必须红，退出码 %d", code)
	}
}

// TestUnknownCapabilityReference 场景引用未登记能力 ⇒ 红。
func TestUnknownCapabilityReference(t *testing.T) {
	bad := strings.Replace(validMatrix, "required: [alpha-cap]", "required: [nope]", 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("未登记的能力引用必须红，退出码 %d", code)
	}
}

// TestUsageBlockMustExist USAGE 机器口径块被删 ⇒ 红（不得静默跳过）。
func TestUsageBlockMustExist(t *testing.T) {
	root := writeRepo(t, map[string]string{"docs/USAGE.md": "# USAGE\n没有机器口径块\n"})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("USAGE 块缺失必须红，退出码 %d", code)
	}
}

// TestUsageMustMatchMatrix USAGE 与清单口径不一致 ⇒ 红。
func TestUsageMustMatchMatrix(t *testing.T) {
	bad := strings.Replace(validUsage, "| memory |", "| redis |", 1)
	root := writeRepo(t, map[string]string{"docs/USAGE.md": bad})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("USAGE 存储口径与清单不一致必须红，退出码 %d", code)
	}
}

// TestUsageExtraScenario USAGE 登记清单里不存在的场景 ⇒ 红。
func TestUsageExtraScenario(t *testing.T) {
	extra := strings.Replace(validUsage, "<!-- deploy-matrix:end -->",
		"| `ghost` 幽灵场景 | memory | single-process | x | available |\n<!-- deploy-matrix:end -->", 1)
	root := writeRepo(t, map[string]string{"docs/USAGE.md": extra})
	if code := runCheck(t, root); code != 1 {
		t.Fatalf("USAGE 多出的场景必须红，退出码 %d", code)
	}
}

// TestUnknownYAMLField 清单拼错字段名 ⇒ 解析即红（KnownFields）。
func TestUnknownYAMLField(t *testing.T) {
	bad := strings.Replace(validMatrix, "title: Alpha 能力", "titel: Alpha 能力", 1)
	root := writeRepo(t, map[string]string{"docs/DEPLOY-MATRIX.yaml": bad})
	if _, err := run(root, io.Discard); err == nil {
		t.Fatal("未知字段必须让解析失败（拼错字段名 = 声明面静默失效）")
	}
}

// TestFindRoot 从子目录向上找到清单所在根。
func TestFindRoot(t *testing.T) {
	root := writeRepo(t, nil)
	got, err := findRoot(filepath.Join(root, "alpha"))
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	if got != root {
		t.Fatalf("findRoot = %q, want %q", got, root)
	}
}

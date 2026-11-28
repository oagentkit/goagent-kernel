// main_test.go —— 命令行接线的可执行判据（临时仓 fixture，不联网）。
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture 铺一座最小临时仓：登记面 + 生命周期清单 + 两份文档。
//
// surfaceExtra 追加在登记面末尾（注入违反用）；readmeImport 控制 README 代码块
// 里的 kernel import（空串 = 不写 import 块）。
func writeFixture(t *testing.T, readmeImport string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	surface := `# 宿主接入面

updated: 2026-11-28

## 主链六步对照

| 步骤 | 宿主提供 | kernel 兑现 |
|---|---|---|
| create 创建 | 宿主拓扑 | 内核落账 |
| claim 领取 | 宿主装配 | 内核认领 |
| execute 执行 | 宿主工具 | 内核执行 |
| commit 提交 | 宿主存储 | 内核闸 |
| recover 恢复 | 宿主重启 | 内核接管 |
| intervene 介入 | 宿主审批 | 内核裁决 |

## 包三分类

| pkg | 分类 | 职责 |
|---|---|---|
| contract | 必须接 | 编排运行契约 |
| internal/routeledger | 内部件 | 路线对账判据 |
`
	lifecycle := "packages:\n  - pkg: contract\n    level: Stable\n  - pkg: internal/routeledger\n    level: Experimental\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "HOST-SURFACE.md"), []byte(surface), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "api-lifecycle.yaml"), []byte(lifecycle), 0o644); err != nil {
		t.Fatal(err)
	}

	readme := "# test\n"
	if readmeImport != "" {
		readme += "\n```go\nimport (\n\t\"fmt\"\n\n\t\"" + readmeImport + "\"\n)\n```\n"
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "USAGE.md"), []byte("# usage\n"), 0o644); err != nil {
		t.Fatal(err)
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

// TestRunGreen 全绿 fixture ⇒ 退出 0。
func TestRunGreen(t *testing.T) {
	root := writeFixture(t, "github.com/yi-nology/goagent-kernel/contract")
	code, out, errOut := runWith(t, root)
	if code != 0 {
		t.Fatalf("应通过，退出码 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(out, "接入面绑定对账通过") {
		t.Fatalf("成功行缺失，实得：%q", out)
	}
}

// TestRunInternalImportDoc README 教宿主 import 内部件 ⇒ 退出 1 并点名。
func TestRunInternalImportDoc(t *testing.T) {
	root := writeFixture(t, "github.com/yi-nology/goagent-kernel/internal/routeledger")
	code, _, errOut := runWith(t, root)
	if code != 1 {
		t.Fatalf("应判违反（1），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "内部件 internal/routeledger") {
		t.Fatalf("必须点名内部件 import，实得：\n%s", errOut)
	}
}

// TestRunMissingDoc 登记面读不到 ⇒ 退出 2（给不出结论）。
func TestRunMissingDoc(t *testing.T) {
	root := writeFixture(t, "")
	if err := os.Remove(filepath.Join(root, "docs", "HOST-SURFACE.md")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runWith(t, root)
	if code != 2 {
		t.Fatalf("应为给不出结论（2），实得 %d", code)
	}
	if !strings.Contains(errOut, "读不到接入面登记面") {
		t.Fatalf("错误要说清读不到什么，实得：\n%s", errOut)
	}
}

// TestCollectDocImportsShapes 单行与分组两种 import 形态都抽得出。
func TestCollectDocImportsShapes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	readme := "# t\n" +
		"```go\nimport \"github.com/yi-nology/goagent-kernel/dispatch\"\n```\n" +
		"```go\nimport (\n\t\"context\"\n\n\tcheckpoint \"github.com/yi-nology/goagent-kernel/checkpoint\"\n)\n```\n" +
		"```text\ngo get github.com/yi-nology/goagent-kernel@latest\n```\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "USAGE.md"), []byte("# u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := collectDocImports(root)
	if err != nil {
		t.Fatalf("抽取失败：%v", err)
	}
	for _, want := range []string{"dispatch", "checkpoint"} {
		if !got[want] {
			t.Fatalf("应抽出 %s，实得 %v", want, got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("只认 import 声明（go get 行不算），应恰 2 条，实得 %v", got)
	}
}

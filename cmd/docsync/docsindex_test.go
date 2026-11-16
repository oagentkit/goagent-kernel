package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckDocIndex 校验三类漂移都能被抓住（#314 / #315 的负对照）。
func TestCheckDocIndex(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/adr/ADR-0001-a.md", "# a")
	write("docs/adr/ADR-0002-b.md", "# b")
	write("docs/BENCH.md", "# bench")
	write("docs/OTHER.md", "# other")
	write("docs/adr/README.md", "| ADR-0001 | [a](ADR-0001-a.md) | x | 已采纳 |\n| ADR-0002 | [b](ADR-0002-a.md) | x | 已采纳 |\n")
	write("README.md", "[ADR-0001](docs/adr/ADR-0001-a.md)\n| [docs/OTHER.md](docs/OTHER.md) |\n")

	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	problems, adrCount, docCount, err := checkDocIndex()
	if err != nil {
		t.Fatal(err)
	}
	if adrCount != 2 || docCount != 2 {
		t.Fatalf("adrCount=%d docCount=%d, want 2/2", adrCount, docCount)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		"ADR-0002 的链接指向 ADR-0002-a.md",  // 索引死链
		"README.md: ADR 速览表漏抄 ADR-0002", // 速览漏抄
		"文档一览表漏登记 docs/BENCH.md",        // 文档一览漏登
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems 缺少 %q；实际：\n%s", want, joined)
		}
	}

	// 正向对照：补齐后无问题。
	write("docs/adr/README.md", "| ADR-0001 | [a](ADR-0001-a.md) | x |\n| ADR-0002 | [b](ADR-0002-b.md) | x |\n")
	write("README.md", "[ADR-0001](docs/adr/ADR-0001-a.md)\n[ADR-0002](docs/adr/ADR-0002-b.md)\n| [docs/OTHER.md](docs/OTHER.md) |\n| [docs/BENCH.md](docs/BENCH.md) |\n")
	problems, _, _, err = checkDocIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("补齐后仍有问题：%v", problems)
	}
}

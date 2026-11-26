package main

import (
	"strings"
	"testing"
)

// checks_test.go —— 纯判据的用例。
//
// 判据的价值在「坏输入必红」：每条都配一个**应当被抓到**的样本，
// 否则测的只是「好输入不红」——那是任何实现都能过的用例。

func TestParseLogSplitsCommitsAndPaths(t *testing.T) {
	raw := "\x01aaa1111\n\ngo.mod\ngo.sum\n\x01bbb2222\n\neventbus/tail.go\n"
	got := parseLog(raw)
	if len(got) != 2 {
		t.Fatalf("应解析出 2 条提交，实得 %d", len(got))
	}
	if got[0].sha != "aaa1111" || len(got[0].paths) != 2 {
		t.Fatalf("首条解析错: %+v", got[0])
	}
	if got[1].sha != "bbb2222" || got[1].paths[0] != "eventbus/tail.go" {
		t.Fatalf("次条解析错: %+v", got[1])
	}
}

func TestParseLogHandlesCRLF(t *testing.T) {
	// Windows 上 git 输出 CRLF：不裁掉 \r 就会让每个路径都带它，
	// isDepPath 全部失配 ⇒ 判据在该平台整体空转。
	raw := "\x01aaa1111\r\n\r\ngo.mod\r\na.go\r\n"
	got := parseLog(raw)
	if len(got) != 1 {
		t.Fatalf("应解析出 1 条提交，实得 %d", len(got))
	}
	for _, p := range got[0].paths {
		if strings.Contains(p, "\r") {
			t.Fatalf("路径不得残留 CR: %q", p)
		}
	}
	if got[0].paths[0] != "go.mod" {
		t.Fatalf("路径解析错: %q", got[0].paths[0])
	}
}

func TestMixViolations(t *testing.T) {
	cases := []struct {
		name  string
		c     commit
		wantN int
	}{
		{"依赖+代码 = 混装", commit{sha: "abc1234", paths: []string{"go.mod", "backend/x.go"}}, 1},
		{"依赖+文档 = 混装", commit{sha: "abc1234", paths: []string{"go.sum", "README.md"}}, 1},
		{"只有依赖", commit{sha: "abc1234", paths: []string{"go.mod", "go.sum"}}, 0},
		{"只有代码", commit{sha: "abc1234", paths: []string{"backend/x.go", "README.md"}}, 0},
	}
	for _, tc := range cases {
		got := mixViolations([]commit{tc.c}, nil)
		if len(got) != tc.wantN {
			t.Fatalf("%s：违反数 = %d，want %d", tc.name, len(got), tc.wantN)
		}
	}
}

func TestMixViolationsHonorsLegacy(t *testing.T) {
	c := commit{sha: "7d8d1afdeadbeef", paths: []string{"go.mod", "backend/x_test.go"}}
	if got := mixViolations([]commit{c}, map[string]string{"7d8d1af": "已发布，不可重写"}); len(got) != 0 {
		t.Fatalf("登记的已发布混装应被放行（否则门禁在当前 tag 上永远红），实得 %d 条", len(got))
	}
	// 闭合清单只认登记过的那一条：别的混装必须照红。
	other := commit{sha: "ffffffffdeadbeef", paths: []string{"go.mod", "backend/x_test.go"}}
	if got := mixViolations([]commit{other}, map[string]string{"7d8d1af": "已发布，不可重写"}); len(got) != 1 {
		t.Fatalf("未登记的混装必须红，实得 %d 条", len(got))
	}
}

func TestHasSectionAnchorsAtLineStart(t *testing.T) {
	md := "# CHANGELOG\n\n## [v0.41.0] — 2026-11-16\n\n正文\n"
	if !hasSection(md, "v0.41.0") {
		t.Fatal("行首的本版标题应命中")
	}
	if hasSection(md, "v0.40.0") {
		t.Fatal("不存在的版本不得命中")
	}
	// 正文里引用旧版本号（勘误段常这么写）不能被当成本版段——
	// 否则一句引用就能让门禁变绿，正好放过真正的缺失。
	refOnly := "# CHANGELOG\n\n## [v0.40.0] — 2026-11-15\n\n- 见 ## [v0.41.0] 那一段\n"
	if hasSection(refOnly, "v0.41.0") {
		t.Fatal("正文中的引用不得算作本版段（必须锚行首）")
	}
}

func TestFirstReplacementChar(t *testing.T) {
	if _, bad := firstReplacementChar("干净文本\n第二行\n"); bad {
		t.Fatal("无替换字符时不应报")
	}
	line, bad := firstReplacementChar("第一行\n第二行\uFFFD坏\n")
	if !bad || line != 2 {
		t.Fatalf("应报第 2 行，实得 line=%d bad=%v", line, bad)
	}
}

// TestMissingSections 纪律 5（issue #495 的反向验证要求）：
// 「段都在」必须绿；删一个历史段 / 改一个段头必须红并点名缺的 tag。
func TestMissingSections(t *testing.T) {
	full := "# CHANGELOG\n\n## [v0.41.0] — 2026-11-16\n\n正文\n\n## [v0.40.0] — 2026-11-15\n\n正文\n"
	tags := []string{"v0.41.0", "v0.40.0"}

	// 段都在 ⇒ 绿（空列表）
	if got := missingSections(tags, full); len(got) != 0 {
		t.Fatalf("段都在时应为空，实得 %v", got)
	}

	// 删掉一个历史段 ⇒ 红并点名缺的 tag
	deleted := "# CHANGELOG\n\n## [v0.41.0] — 2026-11-16\n\n正文\n"
	got := missingSections(tags, deleted)
	if len(got) != 1 || got[0] != "v0.40.0" {
		t.Fatalf("删段必须点名 v0.40.0，实得 %v", got)
	}

	// 改一个段头（v0.40.0 → v0.40.1）⇒ 旧 tag 的段即缺
	retitled := "# CHANGELOG\n\n## [v0.41.0] — 2026-11-16\n\n正文\n\n## [v0.40.1] — 2026-11-15\n\n正文\n"
	got = missingSections(tags, retitled)
	if len(got) != 1 || got[0] != "v0.40.0" {
		t.Fatalf("改段头必须点名 v0.40.0，实得 %v", got)
	}

	// 历史整批消失（v0.64.2 事故形态：只剩最新段）⇒ 每个缺的都点名
	onlyNewest := "# CHANGELOG\n\n## [v0.41.0] — 2026-11-16\n\n正文\n"
	got = missingSections([]string{"v0.41.0", "v0.40.0", "v0.39.0", "v0.2.0"}, onlyNewest)
	if len(got) != 3 {
		t.Fatalf("整批消失必须逐个点名（缺 3 个），实得 %v", got)
	}
}

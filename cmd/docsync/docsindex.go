package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ADR 与文档一览的机器判据（issue #314 / #315）。
//
// 两张表都是手工抄写，此前没有任何判据：ADR 索引与 README 速览表各漂出一条
// 死链（ADR-0009 的结论挂在 ADR-0008 的编号与文件名上），README 文档一览漏登
// docs/BENCH-BASELINE-20261113.md——而它是性能基线数字的唯一口径来源。
// 判据只判**可机械判定的部分**：编号集合 + 链接可解析 + docs/*.md 被登记；
// 表格文案与排序是文档取舍，不判。

var (
	// docs/adr/README.md 的索引行：| ADR-0001 | [标题](文件名) | ... |
	adrIndexRe = regexp.MustCompile(`\|\s*(ADR-\d{4})\s*\|\s*\[[^\]]*\]\(([^)]+)\)`)
	// README 速览表行：| [ADR-0001](docs/adr/...md) | ... |
	readmeADRRe = regexp.MustCompile(`\[ADR-(\d{4})\]\(([^)]+)\)`)
	// README 文档一览里指向 docs 的 md 链接。
	readmeDocLinkRe = regexp.MustCompile(`\[[^\]]*\]\((docs/[^)#]+\.md)\)`)
)

// checkDocIndex 校验 ADR 索引 / README ADR 速览 / README 文档一览三张表与磁盘一致。
func checkDocIndex() (problems []string, adrCount, docCount int, err error) {
	adrFiles, err := filepath.Glob("docs/adr/ADR-*.md")
	if err != nil {
		return nil, 0, 0, err
	}
	adrCount = len(adrFiles)
	onDisk := map[string]string{} // ADR-0001 → 文件名
	for _, p := range adrFiles {
		base := filepath.Base(p)
		id := strings.SplitN(base, "-", 3)
		if len(id) < 3 {
			problems = append(problems, fmt.Sprintf("%s: ADR 文件名不符合 ADR-XXXX-标题.md 约定", base))
			continue
		}
		onDisk[id[0]+"-"+id[1]] = base
	}

	// 1) docs/adr/README.md 索引：编号全覆盖 + 链接可解析。
	// 合成仓/最小仓没有 ADR 目录是合法状态（与 USAGE 口径同款：跳过要说出来，
	// 但不当红——判据面缺失不等于漂移）。
	idxBody, err := os.ReadFile(filepath.Join("docs", "adr", "README.md"))
	if err != nil {
		if len(adrFiles) == 0 {
			// 无 ADR 体系：ADR 两类判据不适用，文档一览仍判。
		} else {
			return nil, 0, 0, err
		}
	}
	indexed := map[string]bool{}
	for _, m := range adrIndexRe.FindAllStringSubmatch(string(idxBody), -1) {
		id, target := m[1], m[2]
		indexed[id] = true
		if want, ok := onDisk[id]; ok && target != want {
			problems = append(problems, fmt.Sprintf("docs/adr/README.md: %s 的链接指向 %s，磁盘上的文件名是 %s", id, target, want))
		}
		if _, statErr := os.Stat(filepath.Join("docs", "adr", target)); statErr != nil {
			problems = append(problems, fmt.Sprintf("docs/adr/README.md: %s 的链接目标 %s 不存在（死链）", id, target))
		}
	}
	for id := range onDisk {
		if !indexed[id] {
			problems = append(problems, fmt.Sprintf("docs/adr/README.md: 索引表漏收 %s（磁盘上有该 ADR）", id))
		}
	}

	// 2) README ADR 速览表：编号全覆盖 + 链接可解析。
	readmeBody, err := os.ReadFile("README.md")
	if err != nil {
		return nil, 0, 0, err
	}
	inReadme := map[string]bool{}
	for _, m := range readmeADRRe.FindAllStringSubmatch(string(readmeBody), -1) {
		id, target := "ADR-"+m[1], m[2]
		inReadme[id] = true
		if _, statErr := os.Stat(target); statErr != nil {
			problems = append(problems, fmt.Sprintf("README.md: %s 的链接目标 %s 不存在（死链）", id, target))
		}
	}
	for id := range onDisk {
		if !inReadme[id] {
			problems = append(problems, fmt.Sprintf("README.md: ADR 速览表漏抄 %s（docs/adr/README.md 已登记）", id))
		}
	}

	// 3) README 文档一览：docs/*.md 顶层文档必须被登记（不含历史快照目录与
	// 子目录索引——那些由各自索引负责）。
	docFiles, err := filepath.Glob("docs/*.md")
	if err != nil {
		return nil, 0, 0, err
	}
	docCount = len(docFiles)
	linked := map[string]bool{}
	for _, m := range readmeDocLinkRe.FindAllStringSubmatch(string(readmeBody), -1) {
		linked[m[1]] = true
	}
	sort.Strings(docFiles)
	for _, p := range docFiles {
		// Glob 在 Windows 上返回反斜杠路径，而 Markdown 链接恒为正斜杠——
		// 归一化后比对（否则 Windows 上每条文档都被误报漏登记）。
		rel := filepath.ToSlash(p)
		if !linked[rel] {
			problems = append(problems, fmt.Sprintf("README.md: 文档一览表漏登记 %s", rel))
		}
	}
	return problems, adrCount, docCount, nil
}

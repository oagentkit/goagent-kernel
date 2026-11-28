// Command semanticscheck 判交付语义分级与失真声明（issue #526）。
//
// # 它回答什么
//
// 评审（2026-11-27 §3.3 问题三）：Exactly Once 的语义必须严格限定——四档分级要
// 成文，「把上界说成 exactly-once」要会红。本命令是 docs/DELIVERY-SEMANTICS.md
// 的执行体：能力面表覆盖与档位值域、判据测试存在、失真声明扫描（白名单 + 拒绝
// 语境窗口两道放行）。
//
// # 判定面：登记面 + 仓内事实
//
// 解析与判定在 judge.go（纯函数），本命令只做 I/O：读登记面与 api-lifecycle、
// 扫仓产测试名索引、扫生产文档与包注释找「把上界说成 exactly-once」的失真
// （含 ±2 行窗口文本——拒绝语境窗口的判定材料）。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（文件读不到 / 解析不了）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// 默认文件（相对仓库根）。
const (
	defaultDoc       = "docs/DELIVERY-SEMANTICS.md"
	defaultLifecycle = "api-lifecycle.yaml"
)

// 失真扫描的目标面：根目录两份纪律文档 + docs/*.md + 生产 .go。
//
// 刻意不含：CHANGELOG.md（历史快照）、site/（文档站镜像面，与 docsync 同口径）、
// *_test.go（测试示例不是承诺面）——边界都写在 docs/DELIVERY-SEMANTICS.md §三。
var scanDocFiles = []string{"README.md", "AGENTS.md"}

// testFuncRe 抓测试函数名。
var testFuncRe = regexp.MustCompile(`(?m)^func\s+(Test[A-Za-z0-9_]+)\(`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("semanticscheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	doc := fs.String("doc", defaultDoc, "交付语义登记面（相对仓库根）")
	lifecycle := fs.String("lifecycle", defaultLifecycle, "API 生命周期清单（相对仓库根）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	reg, err := loadDoc(filepath.Join(*root, *doc))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "semanticscheck: %v\n", err)
		return 2
	}
	registered, stable, err := loadLifecycle(filepath.Join(*root, *lifecycle))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "semanticscheck: %v\n", err)
		return 2
	}
	tests, err := buildTestIndex(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "semanticscheck: %v\n", err)
		return 2
	}
	wlFiles, err := statWhitelist(*root, reg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "semanticscheck: %v\n", err)
		return 2
	}
	hits, err := scanClaims(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "semanticscheck: %v\n", err)
		return 2
	}

	res := Judge(reg, Facts{
		Registered: registered,
		Stable:     stable,
		Tests:      tests,
		Whitelist:  wlFiles,
		Hits:       hits,
	})
	if len(res.Problems) > 0 {
		for _, msg := range res.Problems {
			_, _ = fmt.Fprintf(stderr, "违反：%s\n", msg)
		}
		_, _ = fmt.Fprintf(stderr, "交付语义对账未通过：%d 行（交付 %d）/ Stable 覆盖 %d / 扫描命中 %d\n",
			res.Rows, res.Delivery, res.StableHit, res.Scanned)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "交付语义对账通过：%d 行（交付 %d / ≥6）/ Stable 覆盖 %d/%d / 扫描命中 %d（全部放行有据）\n",
		res.Rows, res.Delivery, res.StableHit, len(stable), res.Scanned)
	return 0
}

// loadDoc 读交付语义登记面（解析在 judge.go）。
func loadDoc(path string) (*Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到交付语义登记面 %s：%w", path, err)
	}
	reg, err := parseDoc(string(b))
	if err != nil {
		return nil, fmt.Errorf("交付语义登记面 %s 解析不了：%w", path, err)
	}
	return reg, nil
}

// lifecycleDoc api-lifecycle 的最小形状。
type lifecycleDoc struct {
	Packages []struct {
		Pkg   string `yaml:"pkg"`
		Level string `yaml:"level"`
	} `yaml:"packages"`
}

// loadLifecycle 读包全集与 Stable 子集（覆盖判据的基准面）。
func loadLifecycle(path string) (registered, stable map[string]bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读不到 api-lifecycle %s：%w", path, err)
	}
	var doc lifecycleDoc
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(false)
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("解析 api-lifecycle %s 失败：%w", path, err)
	}
	registered = map[string]bool{}
	stable = map[string]bool{}
	for _, p := range doc.Packages {
		if p.Pkg == "" {
			return nil, nil, fmt.Errorf("api-lifecycle %s 有空 pkg 条目", path)
		}
		registered[p.Pkg] = true
		if p.Level == "Stable" {
			stable[p.Pkg] = true
		}
	}
	if len(registered) == 0 || len(stable) == 0 {
		return nil, nil, fmt.Errorf("api-lifecycle %s 包全集或 Stable 子集为空：空基准面会让覆盖判据永远为真", path)
	}
	return registered, stable, nil
}

// buildTestIndex 扫仓内全部 *_test.go，产出 `dir/TestName` 全键集合。
func buildTestIndex(root string) (map[string]bool, error) {
	idx := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if filepath.Clean(path) != filepath.Clean(root) &&
				(name == ".git" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		pkg := filepath.ToSlash(rel)
		if pkg == "." {
			pkg = filepath.Base(filepath.Dir(path))
		}
		for _, m := range testFuncRe.FindAllStringSubmatch(string(b), -1) {
			idx[pkg+"."+m[1]] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(idx) == 0 {
		return nil, fmt.Errorf("%s 下没扫到任何测试文件（空索引会让全部登记指针误判成不存在）", root)
	}
	return idx, nil
}

// statWhitelist 核白名单文件真实存在（僵尸白名单判据的数据面）。
func statWhitelist(root string, reg *Registry) (map[string]bool, error) {
	out := map[string]bool{}
	for _, w := range reg.Whitelist {
		if w.File == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(w.File))); err == nil {
			out[w.File] = true
		}
		// 不存在的不放进集合——Judge 会按「僵尸白名单」判红。
	}
	return out, nil
}

// scanClaims 扫描目标面里的 exactly-once 字样（失真扫描的目标），收集命中与
// ±2 行窗口——白名单与拒绝标记窗口的放行判定在 judge.checkClaims。
func scanClaims(root string) ([]Hit, error) {
	files := append([]string{}, scanDocFiles...)
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		return nil, err
	}
	for _, p := range docs {
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil, relErr
		}
		files = append(files, filepath.ToSlash(rel))
	}
	// 生产 .go（非测试）。
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if filepath.Clean(path) != filepath.Clean(root) &&
				(name == ".git" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	var hits []Hit
	for _, rel := range files {
		b, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if readErr != nil {
			return nil, readErr
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if !exactlyOnceRe.MatchString(line) {
				continue
			}
			lo, hi := i-2, i+2
			if lo < 0 {
				lo = 0
			}
			if hi >= len(lines) {
				hi = len(lines) - 1
			}
			hits = append(hits, Hit{
				File:   rel,
				Line:   i + 1,
				Text:   line,
				Window: lines[lo : hi+1],
			})
		}
	}
	return hits, nil
}

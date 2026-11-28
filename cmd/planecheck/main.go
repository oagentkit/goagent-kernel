// Command planecheck 判三平面映射与跨面依赖（issue #527）。
//
// # 它回答什么
//
// 评审（2026-11-27 §3.3 问题四）：控制面/执行面/恢复面概念上必须分清。本命令是
// docs/PLANES.md 的执行体：映射覆盖全集、跨面方向逐对登记、禁止方向被真实
// import 命中 ⇒ 红。
//
// # 判定面：登记面 + 仓内 import 边
//
// 解析与判定在 judge.go（纯函数）；本命令只做 I/O——读登记面与 api-lifecycle、
// 用 go/ast 扫生产代码 import（与 docsync pkgtable 同一取法：判 import 声明，
// 不扫文本）。测试代码的 import 不进判定面（与 dependency_direction 同口径）。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（文件读不到 / 解析不了 / 源码扫不动）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 默认文件（相对仓库根）。
const (
	defaultDoc       = "docs/PLANES.md"
	defaultLifecycle = "api-lifecycle.yaml"
)

// selfModule 本仓 module 路径。
const selfModule = "github.com/yi-nology/goagent-kernel/"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("planecheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	doc := fs.String("doc", defaultDoc, "三平面登记面（相对仓库根）")
	lifecycle := fs.String("lifecycle", defaultLifecycle, "API 生命周期清单（相对仓库根）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	reg, err := loadDoc(filepath.Join(*root, *doc))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "planecheck: %v\n", err)
		return 2
	}
	registered, err := loadLifecycle(filepath.Join(*root, *lifecycle))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "planecheck: %v\n", err)
		return 2
	}
	edges, err := scanEdges(*root, registered)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "planecheck: %v\n", err)
		return 2
	}

	res := Judge(reg, Facts{Registered: registered, Edges: edges})
	if len(res.Problems) > 0 {
		for _, msg := range res.Problems {
			_, _ = fmt.Fprintf(stderr, "违反：%s\n", msg)
		}
		_, _ = fmt.Fprintf(stderr, "三平面对账未通过：%d 个包映射 / %d 条规则 / %d 条跨面边\n",
			res.Mapping, res.Rules, res.Cross)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "三平面对账通过：%d 个包映射 / %d 条规则 / %d 条跨面边（全部登记且未违禁）\n",
		res.Mapping, res.Rules, res.Cross)
	return 0
}

// loadDoc 读三平面登记面（解析在 judge.go）。
func loadDoc(path string) (*Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到三平面登记面 %s：%w", path, err)
	}
	reg, err := parseDoc(string(b))
	if err != nil {
		return nil, fmt.Errorf("三平面登记面 %s 解析不了：%w", path, err)
	}
	return reg, nil
}

// lifecycleDoc api-lifecycle 的最小形状。
type lifecycleDoc struct {
	Packages []struct {
		Pkg string `yaml:"pkg"`
	} `yaml:"packages"`
}

// loadLifecycle 读包全集。
func loadLifecycle(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到 api-lifecycle %s：%w", path, err)
	}
	var doc lifecycleDoc
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(false)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("解析 api-lifecycle %s 失败：%w", path, err)
	}
	out := map[string]bool{}
	for _, p := range doc.Packages {
		if p.Pkg == "" {
			return nil, fmt.Errorf("api-lifecycle %s 有空 pkg 条目", path)
		}
		out[p.Pkg] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("api-lifecycle %s 一个包都没读到：空全集会让覆盖判据永远为真", path)
	}
	return out, nil
}

// scanEdges 扫生产代码（非测试）里「两侧都是登记包」的 import 边，去重。
//
// 用 go/ast 判 import 声明（与 docsync pkgtable 同一取法）——扫文本会把注释里的
// 示例路径当真依赖。测试文件跳过：测试依赖是临时的，交付物里的形状才作数。
func scanEdges(root string, registered map[string]bool) ([]Edge, error) {
	fset := token.NewFileSet()
	seen := map[string]bool{}
	var edges []Edge
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
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		from := filepath.ToSlash(rel)
		if from == "." || !registered[from] {
			return nil // 根目录或非登记包（cmd/* 等工具面）不作为边的起点
		}
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("解析 %s 失败：%w", path, parseErr)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, selfModule) {
				continue
			}
			to := strings.TrimPrefix(p, selfModule)
			if to == from || !registered[to] {
				continue
			}
			key := from + "→" + to
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, Edge{From: from, To: to})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return edges, nil
}

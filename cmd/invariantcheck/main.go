// Command invariantcheck 判执行不变量与插点矩阵的绑定对账（issue #523）。
//
// # 它回答什么
//
// 评审（2026-11-27 §3.3 问题二）要的「执行正确性证明体系」有一个最低形态：
// 每条不变量有登记、有测试守着；每个步骤间隙有场景或书面豁免。这两句话此前
// 只能靠人读 docs/INVARIANTS.md 得出结论——而本仓的纪律是「事实要有主，
// 判据不能靠人读」。本命令就是那两句话的执行体。
//
// # 三条判据
//
//  1. **登记面自身形状**：评审点名的七类不变量 + 插点矩阵 G1–G7 齐全不重，
//     每行机制 / 成文处 / 缺口格非空；
//  2. **判据绑定存在**：登记的每条 `dir/TestName` 指针在仓内 *_test.go 里
//     真实存在（清单里写着一条没人守的测试，比没有清单更坏）；
//  3. **零静默空隙**：每个间隙要么有场景指针，要么有书面豁免（「—」不算理由）。
//
// # 判定面：登记面 + 仓内测试事实
//
// 解析与判定走 internal/invariantledger（判据纯函数），本命令只做 I/O：
// 读 md、扫描仓内测试名、把结论转成退出码。测试事实由本命令扫描注入 ⇒
// 判据层不碰文件系统、不联网，用例可确定性测。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（登记面读不到 / 解析不了 /
// 仓扫不动）。
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yi-nology/goagent-kernel/internal/invariantledger"
)

// defaultDoc 登记面相对仓库根的路径。
const defaultDoc = "docs/INVARIANTS.md"

// testFuncRe 抓测试函数名：`func TestXxx(`。
var testFuncRe = regexp.MustCompile(`(?m)^func\s+(Test[A-Za-z0-9_]+)\(`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("invariantcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	doc := fs.String("doc", defaultDoc, "不变量登记面（相对仓库根）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	body, err := loadDoc(filepath.Join(*root, *doc))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invariantcheck: %v\n", err)
		return 2
	}
	reg, err := invariantledger.ParseDoc(body)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invariantcheck: 登记面 %s 解析不了：%v\n", *doc, err)
		return 2
	}
	idx, err := buildTestIndex(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invariantcheck: 扫仓内测试失败：%v\n", err)
		return 2
	}

	res := invariantledger.Judge(reg, idx)
	if len(res.Problems) > 0 {
		for _, msg := range res.Problems {
			_, _ = fmt.Fprintf(stderr, "违反：%s\n", msg)
		}
		_, _ = fmt.Fprintf(stderr, "不变量绑定对账未通过：%d 条不变量 / %d 个间隙 / %d 条测试指针\n",
			res.Invariants, res.Gaps, res.Refs)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "不变量绑定对账通过：%d 条不变量 / %d 个间隙 / %d 条测试指针（书面豁免 %d 行）\n",
		res.Invariants, res.Gaps, res.Refs, res.Waived)
	return 0
}

// loadDoc 读登记面文档。
//
// 判定不在本层（结论全在 internal/invariantledger.Judge）——读真文件是门禁
// 纪律本身要求的（登记面必须是仓里那一份，不是注入的副本）。
func loadDoc(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读不到登记面 %s：%w", path, err)
	}
	return string(b), nil
}

// buildTestIndex 扫仓内全部 *_test.go，产出 `dir/TestName` 全键集合。
//
// 跳过 .git / vendor / 隐藏目录（与 `go test ./...` 的覆盖口径同向：vendor
// 不进包图，隐藏目录不是包）。扫描失败要响亮失败——「扫不出测试」的索引是
// 空索引，会让每条登记指针都判成不存在，那不是判据是噪声。
func buildTestIndex(root string) (invariantledger.TestIndex, error) {
	idx := invariantledger.TestIndex{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// 根目录本身不参与隐藏判定：root 传 "." 时它的名字就是 "."，
			// 按隐藏目录跳过会把整棵树跳掉（空索引误判全部指针不存在）。
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

// Command routecheck 判路线达成对账（issue #279 的判据形态兑现）。
//
// # 它回答什么
//
// 总纲单（issue #279）声明的路线此前只以散文存在：issue 正文的阶段表格 +
// 台账 §4 的「目标态定义」。两处都写着「已全部达成」，但**没有任何机器判据**
// 去核这句话 ⇒ 「路线是否真的全达成」只能靠人读两段散文下结论。
//
// 而总纲单在台账 §9 的判据形态写的就是「台账与本文档一致」⇒ 那句话此前
// **没有执行体**。本命令就是它的执行体。
//
// # 三条判据
//
//  1. **登记面自身形状**：阶段 id 唯一 / 成员非空 / 出口条件非空 / 总纲单已声明；
//  2. **成员达成**：每个阶段的每个成员在台账里必须已合入（带关闭 PR 号），
//     否则红并点名（哪个阶段、哪个单号、台账里它实际是什么状态）；
//  3. **总纲单在册**：总纲单在台账登记面里必须有一行。
//
// # 判定面：登记面 + 台账派生事实
//
// 解析走 internal/routeledger（判据纯函数）+ internal/ledgerstatus（单号 →
// 状态的唯一派生器）——本命令不自己再解析一遍台账：两处各读一遍，结论就会分叉，
// 而分叉的方向恰好是「门禁判的和台账写的不是一套」。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（登记面 / 台账读不到）。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yi-nology/goagent-kernel/internal/ledgerstatus"
	"github.com/yi-nology/goagent-kernel/internal/routeledger"
	"gopkg.in/yaml.v3"
)

// defaultRegistry 路线登记面相对仓库根的路径。
const defaultRegistry = "docs/ROUTE-LEDGER.yaml"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("routecheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	registry := fs.String("registry", defaultRegistry, "路线登记面（相对仓库根）")
	ledger := fs.String("ledger", ledgerstatus.DefaultPath, "演进台账路径（相对仓库根）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	reg, err := loadRegistry(filepath.Join(*root, *registry))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "routecheck: %v\n", err)
		return 2
	}
	led, err := ledgerstatus.Parse(filepath.Join(*root, *ledger))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "routecheck: %v\n", err)
		return 2
	}

	res := routeledger.Judge(reg, led)
	if len(res.Problems) > 0 {
		for _, msg := range res.Problems {
			_, _ = fmt.Fprintf(stderr, "违反：%s\n", msg)
		}
		_, _ = fmt.Fprintf(stderr, "路线达成对账未通过：%d 个阶段 / %d 个成员单号 / %d 个已达成\n",
			res.Stages, res.Members, res.Achieved)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "路线达成对账通过：%d 个阶段 / %d 个成员单号 / %d 个已达成\n",
		res.Stages, res.Members, res.Achieved)
	return 0
}

// loadRegistry 读路线登记面。
//
// 严格模式（KnownFields）：登记面里出现一个没定义的键就报错，而不是默默忽略——
// 默默忽略意味着「以为登记了某个字段，其实没登记」。
func loadRegistry(path string) (*routeledger.Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到路线登记面 %s：%w", path, err)
	}
	var reg routeledger.Registry
	dec := yaml.NewDecoder(newReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("解析路线登记面 %s 失败：%w", path, err)
	}
	return &reg, nil
}

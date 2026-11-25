// Command acceptancecheck 判验收路径「被执行过」的证据对账（issue #483）。
//
// # 它回答什么
//
// 真集群类验收很贵（docker + kind + 拉镜像），人不会天天跑 ⇒
// 「哪条路径跑过、上次什么时候跑的、结论是什么」没人说得清。
// 第十八轮台账 §27 实测撞见过「判据代码从未被执行过」这一形态（测试在、绿着，
// 但从没跑过）——真集群验收比那条更贵，代价是**连「从没跑过」都不显形**。
//
// ⇒ 本命令判一张登记面的三件事：
//
//  1. **没跑过的路径显形点名**（阻断）：登记了却找不到证据 ⇒ 它从未被执行过。
//  2. **时效只回显不判红**：跑过一次即算数；过期只回显（与 issue-state 快照
//     同口径——「时间判据让门禁红在与被判事实无关的原因上」）。
//  3. **登记的入口必须真的存在**：本仓自有的路径，其 Makefile 目标必须存在、
//     且带 `# acceptance-path: <id>` 标记；反向也对账（Makefile 声明过的验收
//     路径必须进登记面）⇒ 挡住「判据体搬走了、入口没跟着搬」。
//
// # 判定面：登记面 + 仓内事实
//
// 解析走 internal/acceptancepath（判据纯函数），本命令只做 I/O：
// 读 yaml、注入时钟、把结论转成退出码。判据与数据源分离 ⇒ 用例可确定性测。
//
// 退出码：0 = 通过；1 = 有违反；2 = 给不出结论（登记面读不到 / Makefile 读不到）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/oagentkit/goagent-kernel/internal/acceptancepath"
	"gopkg.in/yaml.v3"
)

// defaultRegistry 登记面相对仓库根的路径。
const defaultRegistry = "docs/acceptance-paths.yaml"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 命令行主体（返回退出码而不是 os.Exit：用例要够得着）。
func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, stdout, stderr, time.Now)
}

// runWith 可注入时钟的实现。
//
// 时钟是**接缝**：本命令拿「当下墙钟」判时效回显，而判据层已经吃注入的时钟
// （internal/acceptancepath.Options.Now）——门禁入口自己却读死 time.Now 的话，
// 时效这条判据就没法被确定性测。接缝与判据层的口径是同一条（issue #482）。
func runWith(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	fs := flag.NewFlagSet("acceptancecheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "仓库根目录")
	registry := fs.String("registry", defaultRegistry, "验收路径登记面（相对仓库根）")
	staleDays := fs.Int("stale-days", int(acceptancepath.DefaultStaleAfter/(24*time.Hour)),
		"回显而不判红的时效阈值（天）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	reg, err := loadRegistry(filepath.Join(*root, *registry))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "acceptancecheck: %v\n", err)
		return 2
	}

	res := acceptancepath.Judge(reg, acceptancepath.Options{
		Root:       *root,
		Now:        now(),
		StaleAfter: time.Duration(*staleDays) * 24 * time.Hour,
		Makefile:   "Makefile",
	})

	for _, msg := range res.Echoes {
		_, _ = fmt.Fprintf(stdout, "回显：%s\n", msg)
	}
	if len(res.Never) > 0 {
		_, _ = fmt.Fprintf(stdout, "从未被执行过的验收路径（%d 条）：%v\n",
			len(res.Never), res.Never)
	}
	if len(res.Fatal) > 0 {
		for _, msg := range res.Fatal {
			_, _ = fmt.Fprintf(stderr, "acceptancecheck: %s\n", msg)
		}
		return 2
	}
	if len(res.Problems) > 0 {
		for _, msg := range res.Problems {
			_, _ = fmt.Fprintf(stderr, "违反：%s\n", msg)
		}
		_, _ = fmt.Fprintf(stderr, "验收路径对账未通过：%d 条登记 / %d 条跑过 / %d 条从未跑过\n",
			len(reg.Paths), len(res.Ran), len(res.Never))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "验收路径对账通过：%d 条登记 / %d 条跑过 / %d 条从未跑过\n",
		len(reg.Paths), len(res.Ran), len(res.Never))
	return 0
}

// loadRegistry 读登记面。
//
// 严格模式（KnownFields）：登记面里出现一个没定义的键就报错，而不是默默忽略——
// 默默忽略意味着「以为登记了某个字段，其实没登记」（本仓反复踩的形态）。
func loadRegistry(path string) (*acceptancepath.Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到验收路径登记面 %s：%w", path, err)
	}
	var reg acceptancepath.Registry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("解析验收路径登记面 %s 失败：%w", path, err)
	}
	return &reg, nil
}

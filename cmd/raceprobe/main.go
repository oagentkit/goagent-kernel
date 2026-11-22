// main.go —— raceprobe 的 CLI 装配层。
//
// 参数：
//
//	-packages <模式>        要跑的包（默认 ./...）
//	-timeout <时长>        传给 go test -timeout 的用例超时（默认 10m）
//	-watchdog <时长>       本工具的兜底看门狗（默认 15m；必须 > timeout + 1m）
//	-out <目录>             取证落盘目录（默认 $TMPDIR/raceprobe）
//	-recheck <bool>         是否允许串行复核这条反事实实验（默认 true）
//	-recheck-timeout <时长> 复核自己的上限（默认 10m）
//	-race <bool>            是否开 -race（默认 true；false = 复用给不判竞态的门禁）
//	-run <模式>             传给 go test -run 的过滤（默认空）
//	-v                      传给 go test -v（默认关）
//	-label <名字>           门禁名，进取证文件名与报告文案（默认 race）
//
// 退出码：0 = 通过（含「环境面已复核」）；1 = 未通过；2 = 用法/配置错。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// flagOutDir 取证目录的标志名。
//
// 为什么写成常量而不是就地写字面量：本仓的判定词表门禁要求「词表取值不许散落
// 成无名字字面量」，而 "out" 恰好与 protocol 的 scope 词表同形（不同概念）。
// 门禁放行的是「本包给这个取值起了名字」——它要的是命名，不是不许用。
const flagOutDir = "out"

func main() {
	os.Exit(runMain())
}

// runMain 解析参数并跑一轮门禁。
func runMain() int {
	fs := flag.NewFlagSet("raceprobe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	packages := fs.String("packages", "./...", "要跑的包模式")
	goTimeout := fs.Duration("timeout", 10*time.Minute, "go test 的用例超时")
	watchdog := fs.Duration("watchdog", 15*time.Minute, "本工具的兜底看门狗（须 > timeout + 1m）")
	outDir := fs.String(flagOutDir, filepath.Join(os.TempDir(), "raceprobe"), "取证落盘目录")
	recheck := fs.Bool("recheck", true, "启动期楔死时是否做串行复核（反事实实验）")
	recheckTimeout := fs.Duration("recheck-timeout", 10*time.Minute, "串行复核自己的上限")
	noRace := fs.Bool("race", true, "是否开 -race（false = 复用本工具跑不判竞态的门禁，如 fault-injection）")
	runPattern := fs.String("run", "", "传给 go test -run 的过滤（如 ^TestFault；空 = 不加）")
	verbose := fs.Bool("v", false, "传给 go test -v")
	label := fs.String("label", "race", "门禁名：进取证文件名与报告文案")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}

	o := options{
		packages:       *packages,
		goTimeout:      *goTimeout,
		watchdog:       *watchdog,
		outDir:         *outDir,
		recheck:        *recheck,
		recheckTimeout: *recheckTimeout,
		noRace:         !*noRace,
		runPattern:     *runPattern,
		verbose:        *verbose,
		label:          *label,
		spawn:          defaultSpawner,
		now:            time.Now,
	}
	code, msg := runRace(context.Background(), o)
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return code
}

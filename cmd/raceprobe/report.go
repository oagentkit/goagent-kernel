// report.go —— 结论的**渲染与落盘**（人读的那一面）。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runReport 一轮门禁的完整结论。
type runReport struct {
	// Started 起始时刻（UTC RFC3339）。
	Started string
	// Duration 墙钟耗时。
	Duration string
	// ExitCode 首轮 go test 的退出码。
	ExitCode int
	// TimedOut 首轮是否被看门狗掐掉。
	TimedOut bool
	// Verdict 形态结论。
	Verdict Verdict
	// Passed 跑完且通过的包。
	Passed []string
	// Failed 跑完但失败的包。
	Failed []string
	// Targets 复核重跑的包集合。
	Targets []string
	// Recheck 复核结论（一句话）。
	Recheck string
	// Recovered 是否判绿（环境面已复核）。
	Recovered bool
	// LogPath 首轮原始输出路径。
	LogPath string
	// RecheckLog 复核原始输出路径。
	RecheckLog string
}

// renderReport 渲染结论块。
//
// 判绿（环境面已复核）与判红用**不同的开头**——
// 判绿那一档必须显眼，否则「红变绿」就变成了无声的豁免。
func renderReport(r runReport) string {
	var b strings.Builder
	if r.Recovered {
		b.WriteString("⚠️  raceprobe: 检出「启动期楔死」（环境面），并已用**串行复核**排除代码面\n")
		b.WriteString("    本次门禁判绿——但事件已留痕，且这不是豁免，是一次有据的复核。\n")
	} else {
		b.WriteString("raceprobe: race 门禁未通过\n")
	}
	fmt.Fprintf(&b, "    · 起始：%s　耗时：%s　退出码：%d\n", r.Started, r.Duration, r.ExitCode)
	if r.TimedOut {
		b.WriteString("    · 看门狗到点：本工具掐掉了这轮 go test（Go 自己的兜底闹钟没响）\n")
	}
	fmt.Fprintf(&b, "    · 形态：%s\n", r.Verdict.Kind)
	fmt.Fprintf(&b, "    · 结论：%s\n", r.Verdict.Detail)
	if r.Verdict.Timeout != "" {
		fmt.Fprintf(&b, "    · 兜底闹钟时限：%s\n", r.Verdict.Timeout)
	}
	fmt.Fprintf(&b, "    · 通过 %d 个包；失败 %d 个包", len(r.Passed), len(r.Failed))
	if len(r.Failed) > 0 {
		b.WriteString("：" + strings.Join(r.Failed, ", "))
	}
	b.WriteString("\n")
	if len(r.Targets) > 0 {
		fmt.Fprintf(&b, "    · 复核包：%s\n", strings.Join(r.Targets, ", "))
	}
	if r.Recheck != "" {
		fmt.Fprintf(&b, "    · 复核：%s\n", r.Recheck)
	}
	fmt.Fprintf(&b, "    · 原始输出：%s\n", r.LogPath)
	if r.RecheckLog != "" {
		fmt.Fprintf(&b, "    · 复核输出：%s\n", r.RecheckLog)
	}
	if r.Recovered {
		b.WriteString("    · 若同一台机器上反复出现，查机器面：内存压力 / 同时有别的构建在跑 / 并行启动的测试二进制太多。\n")
	}
	return b.String()
}

// writeEvidence 把结论块写进取证目录（与原始输出同一目录，一眼能找到）。
func writeEvidence(dir, stamp, text string) error {
	path := filepath.Join(dir, "race-"+stamp+".verdict.txt")
	return os.WriteFile(path, []byte(text), 0o644)
}

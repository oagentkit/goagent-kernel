// race.go —— race 门禁的**编排层**：跑一轮、判形态、必要时做串行复核、落取证。
//
// # 为什么编排层要单独一层
//
// 判据（classify.go）是纯函数，编排层只负责三件事：
//  1. **跑**：把 go test 的输出同时送到终端、日志文件与内存缓冲；
//  2. **判**：把缓冲喂给判据，拿一个形态结论；
//  3. **复核**：结论是「启动期楔死」时，做一次**反事实实验**（同树、串行重跑
//     受害包），把「这是环境面」从一句断言变成一次实测。
//
// # 串行复核是什么、为什么它有资格改判
//
// 假设：启动期楔死是**并行启动期的机器面争用**（macOS 动态链接器在内存压力下
// 抢不到锁），不是代码缺陷。反事实：同一棵树、同一台机器、**不并行启动**
// （-p 1）重跑受害包。
//
//	· 复核通过 ⇒ 代码在这台机器上能跑 ⇒ 原失败与「并行启动」这个变量相关，
//	  而不是与代码相关 ⇒ 判为环境面；
//	· 复核仍红 ⇒ 代码面（或另一类环境面）⇒ 按复核结果判红。
//
// 这条反事实与人工定责用的是**同一个方法**（换一个变量重跑一次对照），
// 差别只在于这里由机器执行、每次都执行。
//
// # 判绿的条件有多严
//
// 只有**同时**满足下面四条才判绿：
//  1. 首轮形态是 startup-wedge（Go 运行时压根没起来 ⇒ 没有 goroutine 栈）；
//  2. 首轮确实有受害包可复核（没有就无从反事实）；
//  3. 串行复核**全部通过**（退出码 0 且没被看门狗掐掉）；
//  4. 复核期间**没有**再出现新的失败形态。
//
// 任何一条不满足 ⇒ 判红。且判绿时**必留痕**：原始输出、复核输出、结论块
// 三份都落盘，并打印在终端上——不是「悄悄变绿」。
//
// # 为什么「环境面已复核」可以判绿而不是判红
//
// 门禁变红的含义是「有东西不对，需要人处理」。若失败已被实测排除到代码面之外，
// 继续红就只剩两种后果：人每次都要重做一遍同样的复核，或者人学会无视这个红。
// 后者更贵——**一个会喊「狼来了」的门禁等于没有门禁**。
//
// 判绿不是掩盖：结论块、证据路径、原始输出全在，且写明「若反复出现，查机器面」。
package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/yi-nology/goagent-kernel/raceprobe"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"
)

// options 一轮 race 门禁的运行参数。
type options struct {
	// packages 要跑的包模式（默认 ./...）。
	packages string
	// goTimeout 传给 go test -timeout 的用例超时（决定 Go 自己的看门狗时限）。
	goTimeout time.Duration
	// watchdog 本工具的兜底看门狗（必须大于 goTimeout + 1m，见 validateBudget）。
	watchdog time.Duration
	// outDir 取证落盘目录。
	outDir string
	// recheck 是否允许「串行复核」这条反事实实验。
	recheck bool
	// recheckTimeout 复核自己的上限。
	recheckTimeout time.Duration
	// noRace 关掉 -race（零值 = 开 race，即 race 门禁的默认形态）。
	//
	// 给**别的门禁**复用本工具时用（如 fault-injection：它不判竞态，
	// 跑 -race 只会白慢一轮，且 cgo 依赖会把它绑到编译器上）。
	noRace bool
	// runPattern 传给 go test -run 的过滤（零值 = 不加该 flag）。
	// fault-injection 用它钉住 `^TestFault`，与原 `go test -run` 形态逐字一致。
	runPattern string
	// verbose 传给 go test -v（零值 = 不加）。故障注入层要它：
	// 每个注入场景的失败形态直接可见（这类用例的红就是它们的文档）。
	verbose bool
	// label 门禁名：进取证文件名与报告文案（零值归一为 "race"）。
	label string
	// spawn 可注入接缝（用例用假的 spawner）。
	spawn spawner
	// now 取时间的函数（用例可固定）。
	now func() time.Time
	// stdout / stderr 终端输出接缝（用例可换成缓冲）。
	stdout io.Writer
	stderr io.Writer
}

// validateBudget 校验看门狗与 Go 用例超时的关系。
//
// 关系必须是：**看门狗 > Go 用例超时 + 1 分钟**。
//
// 那个 +1 分钟是 cmd/go 自己的兜底闹钟（testKillTimeout = testTimeout + 1m）：
// 它杀测试二进制时打印的那行「*** Test killed with quit: ran too long (11m0s).」
// **正是启动期楔死的指纹**。若看门狗比它早 ⇒ 我们先把 go test 杀了 ⇒ 拿不到
// 那行指纹 ⇒ 形态只能判成 unknown ⇒ 取证能力自己把自己废了。
func validateBudget(goTimeout, watchdog time.Duration) error {
	if goTimeout <= 0 {
		return errors.New("raceprobe: Go 用例超时必须为正")
	}
	if watchdog <= 0 {
		return errors.New("raceprobe: 看门狗超时必须为正")
	}
	if min := goTimeout + time.Minute; watchdog <= min {
		return errors.New(fmt.Sprintf(
			"raceprobe: 看门狗(%s) 必须大于 Go 用例超时(%s) + 1m（cmd/go 兜底闹钟）= %s —— "+
				"否则我们先于 cmd/go 杀掉 go test，拿不到启动期楔死的指纹，判定会退化成 unknown",
			watchdog, goTimeout, min))
	}
	return nil
}

// testArgs 拼 go test 的参数。
//
// 参数顺序有讲究：`go test [flags] [packages] [test binary flags]`——
// 包列表之后的参数会被当成测试二进制的参数，故包列表必须放在最后。
//
//   - -race     竞态检测（必须开 cgo，见 childEnv 的字面注入）。
//   - -count=1  禁用测试缓存：race 的结论必须来自这一次真跑。
//   - -timeout  用例超时（Go 自己的看门狗 + cmd/go 的兜底闹钟都由它决定）。
//   - -p        并行度；复核时取 1（反事实实验要拿掉「并行启动」这个变量）。
func (o options) testArgs(parallelism string, packages []string) []string {
	args := []string{"test"}
	if parallelism != "" {
		args = append(args, "-p", parallelism)
	}
	if !o.noRace {
		args = append(args, "-race")
	}
	if o.verbose {
		args = append(args, "-v")
	}
	args = append(args, "-count=1", "-timeout", o.goTimeout.String())
	if o.runPattern != "" {
		args = append(args, "-run", o.runPattern)
	}
	return append(args, packages...)
}

// runRace 跑一轮 race 门禁并给出结论，返回进程退出码与结论文本。
func runRace(ctx context.Context, o options) (int, string) {
	if err := validateBudget(o.goTimeout, o.watchdog); err != nil {
		return 2, err.Error()
	}
	if err := os.MkdirAll(o.outDir, 0o755); err != nil {
		return 2, "raceprobe: 建取证目录失败: " + err.Error()
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.label == "" {
		o.label = "race"
	}

	stamp := o.now().UTC().Format("20060102T150405Z")
	logPath := filepath.Join(o.outDir, o.label+"-"+stamp+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return 2, "raceprobe: 建日志文件失败: " + err.Error()
	}
	defer func() { _ = logFile.Close() }()

	stdout := o.stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := o.stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	var buf bytes.Buffer
	var mu sync.Mutex
	outW := &syncWriter{mu: &mu, w: io.MultiWriter(stdout, logFile, &buf)}
	errW := &syncWriter{mu: &mu, w: io.MultiWriter(stderr, logFile, &buf)}

	started := o.now()
	runCtx, cancel := context.WithTimeout(ctx, o.watchdog)
	defer cancel()
	outcome := runGoTest(runCtx, o, o.testArgs("", []string{o.packages}), outW, errW)
	elapsed := o.now().Sub(started)

	output := buf.String()
	verdict := raceprobe.Classify(output)
	report := runReport{
		Label:    o.label,
		Started:  started.UTC().Format(time.RFC3339),
		Duration: elapsed.Round(time.Second).String(),
		ExitCode: outcome.ExitCode,
		TimedOut: outcome.TimedOut,
		Verdict:  verdict,
		Passed:   raceprobe.PassedPackages(output),
		Failed:   raceprobe.FailedPackages(output),
		LogPath:  logPath,
	}

	if outcome.ExitCode == 0 {
		return 0, fmt.Sprintf("raceprobe: %s 门禁通过（%d 个包，耗时 %s，原始输出 %s）",
			o.label, len(report.Passed), report.Duration, logPath)
	}

	// 启动期楔死 ⇒ 做串行复核（反事实实验）。
	if verdict.Kind == raceprobe.KindStartupWedge && o.recheck {
		targets := raceprobe.RecheckTargets(output)
		report.Targets = targets
		if len(targets) > 0 {
			code, recheckLog, recheckOut := o.runRecheck(ctx, targets, stamp)
			report.RecheckLog = recheckLog
			if code == 0 {
				report.Recovered = true
				report.Recheck = fmt.Sprintf(
					"同树串行重跑（-p 1）%d 个受害包全部通过 ⇒ 并行启动期的机器面争用，不是代码缺陷",
					len(targets))
				text := renderReport(report)
				_ = writeEvidence(o.outDir, o.label, stamp, text)
				return 0, text
			}
			// 复核仍红 ⇒ 按复核的结论判（复核可能揭出真正的代码面缺陷）。
			report.Verdict = raceprobe.Classify(recheckOut)
			report.Recheck = "串行复核未通过 ⇒ 以复核结论为准"
		} else {
			report.Recheck = "输出里没有可复核的受害包（cmd/go 没收到失败包行）⇒ 无法做反事实实验"
		}
	}

	text := renderReport(report)
	_ = writeEvidence(o.outDir, o.label, stamp, text)
	return 1, text
}

// runRecheck 做串行复核（反事实实验）：同树、-p 1、只跑受害包。
//
// 返回（退出码，复核日志路径，复核输出）。
func (o options) runRecheck(ctx context.Context, targets []string, stamp string) (int, string, string) {
	logPath := filepath.Join(o.outDir, o.label+"-"+stamp+".recheck.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return 1, logPath, ""
	}
	defer func() { _ = logFile.Close() }()

	stdout := o.stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := o.stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	var buf bytes.Buffer
	var mu sync.Mutex
	outW := &syncWriter{mu: &mu, w: io.MultiWriter(stdout, logFile, &buf)}
	errW := &syncWriter{mu: &mu, w: io.MultiWriter(stderr, logFile, &buf)}

	runCtx, cancel := context.WithTimeout(ctx, o.recheckTimeout)
	defer cancel()
	outcome := runGoTest(runCtx, o, o.testArgs("1", targets), outW, errW)
	if outcome.TimedOut {
		return 1, logPath, buf.String()
	}
	return outcome.ExitCode, logPath, buf.String()
}

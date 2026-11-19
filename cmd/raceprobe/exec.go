// exec.go —— race 门禁的**执行层**：起子进程、兜底超时、整树杀。
//
// # 这层的两条硬约束（都是被实测打出来的，不是洁癖）
//
//  1. **看门狗不得持有父进程的 stdout**：若继承了管道，写 `make check | tail`
//     的那端会一直等 EOF（孤儿进程握着管道 ⇒ 输出延迟半小时才刷出，
//     且管道把 make 的非零退出码吞成 tail 的 0 ⇒ 「红」看起来像「绿」）。
//     本工具的子进程输出走 **os/exec 自己建的管道**，本进程写完就退，
//     不存在「谁替我们握着父 stdout」。
//  2. **看门狗不得留下孤儿**：只杀 go test 一个进程，会留下它的测试二进制
//     （每个测试二进制都是 go test 的子进程），而卡在启动期的那个**永不退出**
//     （Go 运行时压根没启动 ⇒ 连它自己的超时都不触发）。
//     故超时处置是**杀整棵进程树**（unix 下靠独立进程组，见 proc_unix.go）。
package main

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/oagentkit/runtime/procx"
	"github.com/oekit/pkg/errors"
)

// killGrace 取消/超时后强杀前的宽限。
const killGrace = 5 * time.Second

// spawner 起一个子进程并等它结束，把它的输出写到给定的两个 writer。
//
// 它是**可注入接缝**：编排层的用例用假的 spawner 喂构造出来的输出，
// 于是「哪种输出判成哪种形态」「复核通过要不要恢复绿」这些判定
// 都不需要真的跑一场 11 分钟的挂死。
type spawner func(ctx context.Context, argv []string, env []string, out, errOut io.Writer) (int, error)

// defaultSpawner 用 os/exec 起子进程；超时/取消时**杀整棵树**。
func defaultSpawner(ctx context.Context, argv []string, env []string, out, errOut io.Writer) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("raceprobe: 子进程命令为空")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = errOut
	prepareTree(cmd)
	// 超时/取消时的处置：杀整棵进程树，而不是只杀 go test 一个。
	cmd.Cancel = func() error { return killTree(cmd) }
	// 兜底：Cancel 之后仍不退出（管道被孙进程握着）时，强制收场。
	cmd.WaitDelay = killGrace
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	err := cmd.Wait()
	// 超时/取消优先报 ctx 的事实：进程被信号杀掉时 Wait 返回的是 ExitError，
	// 退出码 -1——单看退出码分不出「被我们杀掉」与「它自己退的」。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, err
}

// childEnv 返回子进程环境。
//
// 纪律：**不继承全量 environ**（本仓对这条是硬红线，见 policy 的 egress_paths 段）。
// 走唯一事实源 procx.ChildEnv（PATH/HOME 等基础白名单 + 禁交互提示）。
//
// 这里补两类：
//   - **按名透传**的 Go 工具链变量（父进程没有就丢弃）：GOCACHE / GOMODCACHE /
//     GOPATH / GOFLAGS / GOTOOLCHAIN / GOPROXY / GOSUMDB / GOPRIVATE。
//     不透传的后果是子进程用另一套缓存目录（整仓重编一遍）或换一套代理。
//   - **字面注入**的 CGO_ENABLED=1：race 必须开 cgo（Linux 上 `-race requires cgo`）。
//     字面注入而不是透传，是为了不继承父进程可能带的 CGO_ENABLED=0。
func childEnv() []string {
	extra := []string{
		"CGO_ENABLED=1",
		"GOFLAGS", "GOCACHE", "GOMODCACHE", "GOPATH",
		"GOTOOLCHAIN", "GOPROXY", "GOSUMDB", "GOPRIVATE", "GOEXPERIMENT",
	}
	return procx.ChildEnv(extra)
}

// goTestOutcome 一轮 go test 的结果。
type goTestOutcome struct {
	// ExitCode 子进程退出码（-1 = 启动/托管期失败）。
	ExitCode int
	// TimedOut 本轮是否被看门狗到点掐掉。
	TimedOut bool
	// Err 托管期错误（非零退出不算错）。
	Err error
}

// runGoTest 跑一轮 go test。
func runGoTest(ctx context.Context, o options, args []string, out, errOut io.Writer) goTestOutcome {
	spawn := o.spawn
	if spawn == nil {
		spawn = defaultSpawner
	}
	argv := append([]string{"go"}, args...)
	code, err := spawn(ctx, argv, childEnv(), out, errOut)
	return goTestOutcome{
		ExitCode: code,
		TimedOut: ctx.Err() != nil,
		Err:      err,
	}
}

// syncWriter 把并发写（os/exec 为 stdout/stderr 各起一个复制 goroutine）
// 串行化——共享的日志文件与内存缓冲必须只被一个写者碰。
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

// Write 串行写。
func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

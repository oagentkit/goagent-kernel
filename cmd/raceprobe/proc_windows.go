//go:build windows

package main

import (
	"os"
	"os/exec"

	"github.com/oekit/pkg/errors"
)

// prepareTree windows 下不做进程组隔离。
//
// 为什么：windows 没有 unix 的进程组语义，等价能力是 Job Object，
// 而 Go 标准库不暴露它。上游 runtime/procx 有 windows 路径，本工具不引它：
// 引它就是多一条跨平台依赖边，而 windows 上跑 race 门禁的是 CI 的 windows job
// ——那里不会发生 macOS 动态链接器启动期楔死（本形态的成因是 unix 的 dyld）。
func prepareTree(cmd *exec.Cmd) {}

// killTree 杀直接进程。
//
// **已知局限，写明而不假装覆盖**：windows 下测试二进制可能残留
// （没有 Job Object 就没法整树杀）。这不影响门禁判定——非零退出照样红，
// 只是残留进程不由本工具清理。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

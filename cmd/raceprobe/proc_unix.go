//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/oekit/pkg/errors"
)

// prepareTree 把子进程放进**独立进程组**。
//
// 为什么要进程组：go test 给每个包起一个测试二进制（全是 go test 的子进程）。
// 超时后若只杀 go test 一个，那些测试二进制就成了孤儿——而卡在启动期的那个
// **永不退出**（进程在、CPU 占用 0%、连 Go 自带的用例超时都不触发，
// 因为 Go 运行时压根没启动）。
//
// 独立进程组是「杀一个 pid 就等于杀整棵树」的前提：kill(-pgid) 一次带走组内
// 全部成员（含测试二进制）。
func prepareTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree 杀整棵进程树（整个进程组）。
//
// ESRCH（组已经不在了）不按错误处理，但要**兜底杀直接进程**：组不存在有两种
// 可能——① 进程自己已经退了（那正是我们要的状态）；② Setpgid 没生效
// （组没建起来）。②若不兜底，子进程就留在那儿了，而「不留孤儿」是本层的
// 硬约束。直接进程已退出时 os.Process.Kill 返回 os.ErrProcessDone，按成功处理。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		if perr := cmd.Process.Kill(); perr != nil && !errors.Is(perr, os.ErrProcessDone) {
			return perr
		}
		return nil
	}
	return err
}

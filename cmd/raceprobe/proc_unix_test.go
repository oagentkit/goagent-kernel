//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestKillTreeKillsWholeGroup 钉住「杀整棵树」而不是只杀直接子进程。
//
// 为什么这条要真跑：只杀 go test 一个进程的话，它起的测试二进制会变成孤儿——
// 而卡在启动期的那个**永不退出**（Go 运行时压根没启动，连它自己的超时都不触发）。
// 孤儿进程会一直占着机器，且没人知道它是谁。
func TestKillTreeKillsWholeGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & sleep 30")
	prepareTree(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("起子进程失败: %v", err)
	}
	pgid := cmd.Process.Pid
	// 等后台那个 sleep 起来（否则测的是「本来就没起来」）。
	time.Sleep(300 * time.Millisecond)
	if err := killTree(cmd); err != nil {
		t.Fatalf("杀进程树失败: %v", err)
	}
	_ = cmd.Wait()

	// 组应当清空：kill(-pgid, 0) 在组内无成员时返回 ESRCH。
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if err != nil && err == syscall.ESRCH {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("进程组没有清空（组内仍有成员），可能留下了孤儿")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

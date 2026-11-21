//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// 后台子进程的存在必须**可观测**：它一启动就落一个 marker 文件，
	// 测试轮询等 marker 出现，才知道「那个后台进程真的起来了」。
	// 不用墙钟窗口当尺子——窗口落空时测的是「本来就没起来」（证据落空），
	// 而「起来晚了」与「压根没起来」在这个用例里长得一模一样。
	// 轮询间隔（10ms）不是尺子，尺子是 marker 文件这个可观测事实。
	//
	// marker 路径经环境变量传给子 shell：路径里有空格或引号时不必转义。
	// 内层 shell 用 exec 把自己换成 sleep ⇒ 后台仍是**一个** sleep 进程
	// （与原用例的进程树同形），只是在 exec 之前先 touch 了 marker。
	//
	// 进程组语义不变：非交互 sh 不做 job control ⇒ 后台子进程留在**同一**
	// 进程组（prepareTree 已把外层 sh 设为组长）——这正是本用例要验的前提。
	marker := filepath.Join(t.TempDir(), "bg-started")
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "RACE_PROBE_BG_MARKER=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "RACE_PROBE_BG_MARKER="+marker)
	cmd := exec.Command("sh", "-c", `sh -c 'touch "$RACE_PROBE_BG_MARKER"; exec sleep 30' & sleep 30`)
	cmd.Env = env
	prepareTree(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("起子进程失败: %v", err)
	}
	pgid := cmd.Process.Pid
	// 等后台那个 sleep 真的起来（marker 由它自己 touch ⇒ 它确实跑起来了）。
	startDeadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(startDeadline) {
			// 证据没到手：先收干净，别留下孤儿进程再报错。
			_ = killTree(cmd)
			_ = cmd.Wait()
			t.Fatalf("后台子进程 10s 内未起来（marker 未出现）——本用例失去证据价值")
		}
		time.Sleep(10 * time.Millisecond)
	}
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

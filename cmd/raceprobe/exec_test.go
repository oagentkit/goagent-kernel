// exec_test.go —— 执行层的行为钉住（真起子进程，但都是一秒内的小命令）。
package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDefaultSpawnerCapturesOutput 钉住「stdout 与 stderr 都被采集」。
//
// 为什么两条流都要采：cmd/go 的兜底闹钟那行（"*** Test killed with quit: ran too
// long (11m0s)."）与 "signal: quit" 打在 **stderr** 上——只采 stdout 会漏掉
// 启动期楔死最关键的那条证据，形态就会退化成 unknown。
func TestDefaultSpawnerCapturesOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	code, err := defaultSpawner(context.Background(),
		[]string{"sh", "-c", "echo hello; echo oops >&2"}, nil, &out, &errOut)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if code != 0 {
		t.Fatalf("退出码错：%d", code)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("stdout 没采到：%q", out.String())
	}
	if !strings.Contains(errOut.String(), "oops") {
		t.Fatalf("stderr 没采到：%q", errOut.String())
	}
}

// TestDefaultSpawnerNonZeroExit 钉住「非零退出不算托管错误」。
func TestDefaultSpawnerNonZeroExit(t *testing.T) {
	code, err := defaultSpawner(context.Background(),
		[]string{"sh", "-c", "exit 3"}, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("非零退出不该算托管错误: %v", err)
	}
	if code != 3 {
		t.Fatalf("退出码错：%d", code)
	}
}

// TestDefaultSpawnerEmptyArgv 钉住空命令报错（而不是起个空进程）。
func TestDefaultSpawnerEmptyArgv(t *testing.T) {
	if _, err := defaultSpawner(context.Background(), nil, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("空命令必须报错")
	}
}

// TestDefaultSpawnerTimeoutKillsTree 钉住「超时即收场，不留挂死的子进程」。
//
// 这条是「不留孤儿」纪律的机器形态：超时后进程必须很快退掉（而不是等
// WaitDelay 走完），否则门禁会在超时之后再挂半小时。
func TestDefaultSpawnerTimeoutKillsTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := defaultSpawner(ctx, []string{"sh", "-c", "sleep 30"}, nil, io.Discard, io.Discard)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("超时必须返回错误")
	}
	if elapsed > 15*time.Second {
		t.Fatalf("超时后收场太慢：%s（说明进程没被杀掉）", elapsed)
	}
}

// TestChildEnvDoesNotInheritFullEnv 是**反向验证**：
// 子进程环境必须是白名单生成的，不得带整份父环境。
//
// 一个把 childEnv 改成 os.Environ() 的实现会在这条上变红——
// 那正是 egress 门禁第 5 条要拦的形态（宿主凭据递给子进程）。
func TestChildEnvDoesNotInheritFullEnv(t *testing.T) {
	t.Setenv("RACEPROBE_FAKE_SECRET", "s3cret")
	for _, kv := range childEnv(true) {
		if strings.HasPrefix(kv, "RACEPROBE_FAKE_SECRET=") {
			t.Fatalf("子进程环境带上了父进程的任意变量：%s", kv)
		}
	}
}

// TestChildEnvInjectsCGOEnabled 钉住 race 必需的 cgo 开关是**字面注入**。
//
// 父进程带 CGO_ENABLED=0 时也必须注入 1——否则 Linux 上 `-race requires cgo`
// 直接报错，而 macOS 上则是编出没有竞态检测的二进制（**静默失去门禁意义**）。
func TestChildEnvInjectsCGOEnabled(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	var found string
	for _, kv := range childEnv(true) {
		if strings.HasPrefix(kv, "CGO_ENABLED=") {
			found = kv
		}
	}
	if found != "CGO_ENABLED=1" {
		t.Fatalf("CGO_ENABLED 注入错：%q", found)
	}
}

// TestSyncWriterSerializesConcurrentWrites 钉住并发写的串行化。
//
// os/exec 为 stdout/stderr 各起一个复制 goroutine ⇒ 两个 writer 会被并发调用；
// 不串行化就会写花日志与内存缓冲（race 门禁的证据不能是花的）。
func TestSyncWriterSerializesConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	w := &syncWriter{mu: &mu, w: &buf}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = w.Write([]byte("x"))
			}
		}()
	}
	wg.Wait()
	if buf.Len() != 800 {
		t.Fatalf("写入字节数错：%d", buf.Len())
	}
}

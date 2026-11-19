// classify_test.go —— 形态判据的行为钉住。
//
// # 这层为什么值得单独写用例
//
// 判据是「哪种输出算哪种形态」的**唯一事实源**，而它要判的那个形态
// （11m0s 的启动期楔死）在本机**不可稳定复现**——判据跑不到 ⇒ 改坏了没人知道。
// 故这里用**构造出来的输出文本**把每条判定逐条钉死。
//
// # 反向验证在哪
//
// 「坏实现会红」不是靠断言数量，而是靠**顺序**与**边界**两组用例：
//   - 顺序：栈、竞态、超时 panic、兜底闹钟四类标记**同时出现**时，判据必须
//     按证据强度取最硬的那一个。一个把顺序写反的实现（先判兜底闹钟）
//     会在「带栈的兜底闹钟」这条上变红。
//   - 边界：拿不到受害包时不得凭空判绿；复核没跑过不得判绿。
package main

import (
	"strings"
	"testing"
)

// 构造出来的输出文本。四段都是**真实形态**的复刻：
// wedgeOut 取自 2026-11-18 实录的失败日志（11m0s / signal: quit / 无栈）。
const (
	// wedgeOut 启动期楔死：cmd/go 兜底闹钟杀了测试二进制，**没有 goroutine 栈**。
	wedgeOut = "ok  	github.com/yi-nology/goagent-kernel/contract	0.006s\n" +
		"*** Test killed with quit: ran too long (11m0s).\n" +
		"signal: quit\n" +
		"FAIL	github.com/yi-nology/goagent-kernel/statemachine	660.101s\n" +
		"FAIL\n"

	// hangOut 用例挂死：Go 运行时活着，打出了 goroutine 栈。
	hangOut = "panic: test timed out after 10m0s\n" +
		"goroutine 1 [chan receive]:\n" +
		"main.main()\n" +
		"	/usr/local/go/src/testing/testing.go:2032\n" +
		"FAIL	github.com/yi-nology/goagent-kernel/statemachine	600.123s\n"

	// raceOut 数据竞争：竞态检测器自己报的。
	raceOut = "==================\n" +
		"WARNING: DATA RACE\n" +
		"Write at 0x00c0000b4010 by goroutine 12:\n" +
		"  foo.bar()\n" +
		"FAIL	github.com/yi-nology/goagent-kernel/ptc	1.234s\n"

	// failOut 普通用例失败（断言失败）。
	failOut = "--- FAIL: TestFoo (0.00s)\n" +
		"    foo_test.go:10: want 1, got 2\n" +
		"FAIL	github.com/yi-nology/goagent-kernel/statemachine	0.012s\n" +
		"FAIL\n"
)

// TestClassifyStartupWedge 钉住「无栈 + 兜底闹钟 ⇒ 启动期楔死」。
func TestClassifyStartupWedge(t *testing.T) {
	v := Classify(wedgeOut)
	if v.Kind != KindStartupWedge {
		t.Fatalf("形态判错：得到 %q，期望 %q（输出=%q）", v.Kind, KindStartupWedge, wedgeOut)
	}
	if v.Timeout != "11m0s" {
		t.Fatalf("兜底闹钟时限没取到：得到 %q，期望 11m0s", v.Timeout)
	}
	if v.Package != "github.com/yi-nology/goagent-kernel/statemachine" {
		t.Fatalf("受害包没取到：得到 %q", v.Package)
	}
}

// TestClassifyStackBeatsKillAlarm 是**顺序**的反向验证：
// 带 goroutine 栈的输出即使带着兜底闹钟那行，也必须判「用例挂死」。
//
// 为什么这条最要紧：2026-11-18 那次误归因就是把「有兜底闹钟记录」当成了
// 「用例挂死」的同义词。若判据按出现顺序取第一个命中，楔死形态就会
// 被判成挂死 ⇒ 处置从「查机器面」跑偏到「改代码」。
func TestClassifyStackBeatsKillAlarm(t *testing.T) {
	v := Classify(wedgeOut + hangOut)
	if v.Kind != KindTestHang {
		t.Fatalf("有 goroutine 栈时必须判用例挂死，得到 %q", v.Kind)
	}
}

// TestClassifyDataRaceBeatsAll 是**顺序**的反向验证：
// 竞态标记是最硬的证据，与其它标记同时出现时必须取它。
func TestClassifyDataRaceBeatsAll(t *testing.T) {
	v := Classify(raceOut + hangOut + wedgeOut)
	if v.Kind != KindDataRace {
		t.Fatalf("竞态标记必须优先，得到 %q", v.Kind)
	}
}

// TestClassifyTimeoutPanicBeatsStack 钉住 Go 自己的超时 panic 与栈同形时的判定。
func TestClassifyTimeoutPanicBeatsStack(t *testing.T) {
	v := Classify(hangOut)
	if v.Kind != KindTestHang {
		t.Fatalf("用例挂死判错：得到 %q", v.Kind)
	}
}

// TestClassifyTestFailure 钉住「普通断言失败」不是竞态也不是挂死。
func TestClassifyTestFailure(t *testing.T) {
	v := Classify(failOut)
	if v.Kind != KindTestFailure {
		t.Fatalf("普通用例失败判错：得到 %q，期望 %q", v.Kind, KindTestFailure)
	}
	if v.Package != "github.com/yi-nology/goagent-kernel/statemachine" {
		t.Fatalf("失败包没取到：得到 %q", v.Package)
	}
}

// TestClassifyUnknown 钉住「认不出就说认不出」。
func TestClassifyUnknown(t *testing.T) {
	v := Classify("something went wrong\npanic: weird\n")
	if v.Kind != KindUnknown {
		t.Fatalf("认不出的形态必须判 unknown，得到 %q", v.Kind)
	}
}

// TestClassifyNone 钉住绿路径。
func TestClassifyNone(t *testing.T) {
	v := Classify("ok  	github.com/yi-nology/goagent-kernel/contract	0.006s\n")
	if v.Kind != KindNone {
		t.Fatalf("绿路径判错：得到 %q", v.Kind)
	}
}

// TestPackageBookkeeping 钉住包行解析（含「不带包名的收尾 FAIL 行」）。
func TestPackageBookkeeping(t *testing.T) {
	passed := PassedPackages(wedgeOut)
	if len(passed) != 1 || passed[0] != "github.com/yi-nology/goagent-kernel/contract" {
		t.Fatalf("通过包解析错：%v", passed)
	}
	failed := FailedPackages(wedgeOut)
	if len(failed) != 1 || failed[0] != "github.com/yi-nology/goagent-kernel/statemachine" {
		t.Fatalf("失败包解析错：%v", failed)
	}
	if strings.Contains(strings.Join(failed, ","), "FAIL") {
		t.Fatalf("收尾那行不带包名的 FAIL 不该被当成包：%v", failed)
	}
}

// TestRecheckTargets 钉住复核集合 = 跑过但没跑通的包。
func TestRecheckTargets(t *testing.T) {
	targets := RecheckTargets(wedgeOut)
	if len(targets) != 1 || targets[0] != "github.com/yi-nology/goagent-kernel/statemachine" {
		t.Fatalf("复核集合错：%v", targets)
	}
	if len(RecheckTargets("ok  \tpkg\t1.0s\n")) != 0 {
		t.Fatalf("全绿时不该有复核目标")
	}
}

// fixtures_test.go —— 编排层用例要的**输出样本**。
//
// 这三个样本与 `raceprobe/classify_test.go` 里的同名夹具**同源**（都是真实失败
// 日志的复刻）。分开放是因为 Go 的 `_test.go` 不跨包可见，而编排层用例（本包）
// 要把真实形态的输出喂给判定层——判定层的样例属于它的契约，编排层只是消费。
//
// 改动需同步：它们是同一份契约的两处示例，单改一份会让「哪份是对的」变成问题。
package main

const (
	// wedgeOut 启动期楔死：cmd/go 兜底闹钟杀了测试二进制，**没有 goroutine 栈**。
	//（取自 2026-11-18 实录的失败日志：11m0s / signal: quit / 无栈）
	wedgeOut = "ok \tgithub.com/yi-nology/goagent-kernel/contract\t0.006s\n" +
		"*** Test killed with quit: ran too long (11m0s).\n" +
		"signal: quit\n" +
		"FAIL\tgithub.com/yi-nology/goagent-kernel/statemachine\t660.101s\n" +
		"FAIL\n"

	// raceOut 数据竞争：竞态检测器自己报的。
	raceOut = "==================\n" +
		"WARNING: DATA RACE\n" +
		"Write at 0x00c0000b4010 by goroutine 12:\n" +
		"  foo.bar()\n" +
		"FAIL\tgithub.com/yi-nology/goagent-kernel/ptc\t1.234s\n"

	// failOut 普通用例失败（断言失败）。
	failOut = "--- FAIL: TestFoo (0.00s)\n" +
		"    foo_test.go:10: want 1, got 2\n" +
		"FAIL\tgithub.com/yi-nology/goagent-kernel/statemachine\t0.012s\n" +
		"FAIL\n"
)

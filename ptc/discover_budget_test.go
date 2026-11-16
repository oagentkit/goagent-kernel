package ptc

// discover 也是宿主数据进入程序的入口，必须与 host.<tool> 同受三预算闸记账
// （issue #200）。此前它是唯一不经记账的入口：循环调 discover 既不触发
// budget_calls 也不触发 budget_bytes，只有墙钟能停，返回的 signature /
// description 字符串全部物化进 VM。

import (
	"context"
	"strings"
	"testing"
)

// TestDiscoverCountsAgainstCallBudget 发现调用计入调用数预算：第二次即被
// 中断收口，且第一次的账（Calls / Bytes / CallLog）可观测。
func TestDiscoverCountsAgainstCallBudget(t *testing.T) {
	d := func(string) []Discovered {
		return []Discovered{{Name: "tool_a", Signature: "tool_a()", Description: "合成描述"}}
	}
	const prog = `
		for (let i = 0; i < 100; i++) { discover(""); }
		return "done";
	`
	r := Run(context.Background(), prog, nil, Options{
		MaxCalls: 1,
		MaxBytes: 1 << 20,
		Discover: d,
	})

	if r.OK {
		t.Fatalf("MaxCalls=1 时循环 discover 必须被中断，实得 OK=true Value=%q", r.Value)
	}
	if r.ErrorKind != "budget_calls" {
		t.Fatalf("超调用数预算应以 budget_calls 收口，实得 ErrorKind=%q Error=%q", r.ErrorKind, r.Error)
	}
	if r.Calls != 1 {
		t.Fatalf("首次 discover 应计 1 次调用（第二次在记账前被拒），实得 Calls=%d", r.Calls)
	}
	if r.Bytes == 0 {
		t.Fatal("discover 返回的宿主数据必须计入字节预算，实得 Bytes=0")
	}
	if len(r.CallLog) != 1 {
		t.Fatalf("CallLog 应恰好记录放行的 1 次 discover，实得 %+v", r.CallLog)
	}
	if r.CallLog[0].Name != discoverGlobalName || r.CallLog[0].Status != StatusOK {
		t.Fatalf("CallLog 应记录 discover 的成功调用，实得 %+v", r.CallLog[0])
	}
}

// TestDiscoverCountsAgainstByteBudget 发现结果计入字节预算：大 description
// 攒过上限即中断，不允许「只受墙钟限制」。
func TestDiscoverCountsAgainstByteBudget(t *testing.T) {
	const maxBytes = 16 << 10
	big := strings.Repeat("x", 8<<10)
	d := func(string) []Discovered {
		return []Discovered{{Name: "tool_a", Signature: "tool_a()", Description: big}}
	}
	const prog = `
		for (let i = 0; i < 50; i++) { discover(""); }
		return "done";
	`
	r := Run(context.Background(), prog, nil, Options{
		MaxCalls: 50,
		MaxBytes: maxBytes,
		Discover: d,
	})

	if r.OK {
		t.Fatalf("discover 结果攒过字节上限必须中断，实得 OK=true Value=%q", r.Value)
	}
	if r.ErrorKind != "budget_bytes" {
		t.Fatalf("超字节预算应以 budget_bytes 收口，实得 ErrorKind=%q Error=%q", r.ErrorKind, r.Error)
	}
	if r.Bytes <= maxBytes {
		t.Fatalf("记账字节应已越过上限（Bytes=%d max=%d）——发现路径未记账即回归", r.Bytes, maxBytes)
	}
	if len(r.CallLog) < 2 {
		t.Fatalf("每次 discover 都应留痕，实得 %d 条", len(r.CallLog))
	}
	for i, c := range r.CallLog {
		if c.Name != discoverGlobalName || c.Status != StatusOK || c.Bytes == 0 {
			t.Fatalf("第 %d 条 discover 记账不完整: %+v", i, c)
		}
	}
}

// TestDiscoverWithoutDiscovererIsAbsent 未装配 Discoverer 时不挂全局——
// 记账改造不得把「没装配」变成「挂一个空壳」。
func TestDiscoverWithoutDiscovererIsAbsent(t *testing.T) {
	r := Run(context.Background(), `return typeof discover`, nil, Options{})
	if !r.OK {
		t.Fatalf("纯计算程序应正常执行: %q / %q", r.ErrorKind, r.Error)
	}
	if r.Value != `"undefined"` {
		t.Fatalf("未装配 Discoverer 时不应存在 discover 全局，实得 %s", r.Value)
	}
}

package contract

// status_test.go —— 会话状态词表的判定面与枚举面（issue #354 / #356）。

import (
	"slices"
	"testing"
)

// wantStatuses 会话状态词表的**冻结清单**（7 值）。
//
// 为什么要冻结：枚举与判定同源（都读 statusClass），双向断言在同源设计下
// 天然恒绿——真正的牙齿是这份冻结清单：词表新增一个取值而没同步这里，
// 用例立刻转红（issue #356 的「反向验证」落点）。
var wantStatuses = []string{
	"cancelled",
	"created",
	"failed",
	"running",
	"succeeded",
	"timed_out",
	"waiting_approval",
}

// TestAllStatuses 枚举出口：全集、排序、**新切片**（宿主改写不污染词表）。
func TestAllStatuses(t *testing.T) {
	got := AllStatuses()
	if !slices.Equal(got, wantStatuses) {
		t.Fatalf("会话状态词表漂移：AllStatuses=%v，期望 %v", got, wantStatuses)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("AllStatuses 必须返回排序后的切片: %v", got)
	}
	// 副本语义：改写返回值不得牵动判定面。
	got[0] = "__mutated_by_host__"
	again := AllStatuses()
	if slices.Contains(again, "__mutated_by_host__") {
		t.Fatal("AllStatuses 返回了共享底层数组：宿主改写污染了词表")
	}
	if !slices.Equal(again, wantStatuses) {
		t.Fatalf("改写返回值后词表被污染: %v", again)
	}
}

// TestIsKnownStatus 词表成员判定：7 个成员为真，其余（含空串）为假。
func TestIsKnownStatus(t *testing.T) {
	for _, s := range wantStatuses {
		if !IsKnownStatus(s) {
			t.Errorf("%q 是词表成员，IsKnownStatus 应为 true", s)
		}
	}
	// 空串不是词表成员（尚未声明状态 ≠ 词表外）。
	for _, s := range []string{"", "OPEN", "degraded", "rolled_back", "unknown"} {
		if IsKnownStatus(s) {
			t.Errorf("%q 不是词表成员，IsKnownStatus 应为 false", s)
		}
	}
}

// TestClassifyStatus 三态分类：过程态 / 终态 / 读不懂。
//
// 空串归 StatusOpen（尚未声明状态，不是「读不懂」）——把它归到 unknown
// 会让宿主把「还没开始」当成「不认识」，两者的处置完全不同。
func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status string
		want   StatusClass
	}{
		{"", StatusOpen},
		{StatusCreated, StatusOpen},
		{StatusRunning, StatusOpen},
		{StatusWaitingApproval, StatusOpen},
		{StatusSucceeded, StatusTerminal},
		{StatusFailed, StatusTerminal},
		{StatusCancelled, StatusTerminal},
		{StatusTimedOut, StatusTerminal},
		{"degraded", StatusUnknown},
		{"OPEN", StatusUnknown},
		{"rolled_back", StatusUnknown},
	}
	for _, c := range cases {
		if got := ClassifyStatus(c.status); got != c.want {
			t.Errorf("ClassifyStatus(%q)=%v，期望 %v", c.status, got, c.want)
		}
	}
}

// TestIsSessionTerminal 会话终态判定：4 个终态为真，过程态与词表外为假。
//
// 这条用例同时是 #354 的**反向验证锚点**：把 StatusFailed 从会话终态
// 判定里去掉（statusClass 里删掉 / 改成 StatusOpen），本用例必须转红。
func TestIsSessionTerminal(t *testing.T) {
	for _, s := range []string{StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut} {
		if !IsSessionTerminal(s) {
			t.Errorf("%q 应为会话终态", s)
		}
	}
	for _, s := range []string{StatusCreated, StatusRunning, StatusWaitingApproval, "", "degraded", "unknown"} {
		if IsSessionTerminal(s) {
			t.Errorf("%q 不应为会话终态", s)
		}
	}
}

// TestIsTerminalStatusAlias 兼容别名与新名**逐字同义**：
// 旧名 IsTerminalStatus 保留给既有调用点，语义不得与 IsSessionTerminal 分叉。
func TestIsTerminalStatusAlias(t *testing.T) {
	for _, s := range append([]string{""}, wantStatuses...) {
		if IsTerminalStatus(s) != IsSessionTerminal(s) {
			t.Errorf("IsTerminalStatus(%q)=%v 与 IsSessionTerminal 不一致",
				s, IsTerminalStatus(s))
		}
	}
}

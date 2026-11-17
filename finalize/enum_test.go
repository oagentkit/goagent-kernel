package finalize_test

// enum_test.go —— 路由臂与终态词的**取值面**（issue #384）。
//
// 这两个出口是门禁（cmd/docsync 判 README 职责列里的词表）的取值来源：取值
// 必须从代码的导出常量派生，门禁自己不得再抄一份字面量。故这里钉住它们：
//
//   - 臂全集与常量逐字一致（不多不少）；
//   - 终态词全集 = 各臂 Status() 的非空取值（停机臂不落终态 ⇒ 不在其中）；
//   - 返回的是**副本**：调用方改写它不影响本包的事实。

import (
	"slices"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/finalize"
)

// TestAllActionsMatchesConstants 臂全集与常量逐字一致。
func TestAllActionsMatchesConstants(t *testing.T) {
	want := []finalize.Action{
		finalize.ActionFailed,
		finalize.ActionTakeover,
		finalize.ActionCancelled,
		finalize.ActionTimedOut,
		finalize.ActionShutdown,
	}
	got := finalize.AllActions()
	if len(got) != len(want) {
		t.Fatalf("臂全集基数 = %d，期望 %d", len(got), len(want))
	}
	for _, a := range want {
		if !slices.Contains(got, a) {
			t.Fatalf("臂全集缺 %v（实得 %v）", a, got)
		}
	}
	// 副本：调用方改不动本包的事实。
	got[0] = finalize.ActionTakeover
	if finalize.AllActions()[0] != finalize.ActionFailed {
		t.Fatal("AllActions 必须返回副本（调用方改它不得影响本包的事实）")
	}
}

// TestAllRouteStatusesDerivedFromArms 终态词全集由各臂派生：不落终态的臂
// （停机）不在其中，空串不得出现。
func TestAllRouteStatusesDerivedFromArms(t *testing.T) {
	want := []string{
		contract.StatusCreated,
		contract.StatusCancelled,
		contract.StatusTimedOut,
		contract.StatusFailed,
	}
	got := finalize.AllRouteStatuses()
	if len(got) != len(want) {
		t.Fatalf("终态词基数 = %d，期望 %d（实得 %v）", len(got), len(want), got)
	}
	for _, s := range want {
		if !slices.Contains(got, s) {
			t.Fatalf("终态词缺 %q（实得 %v）", s, got)
		}
	}
	for _, s := range got {
		if s == "" {
			t.Fatal("终态词表不得含空串（空串 = 不落终态，宿主不得映射成状态）")
		}
	}
	// 停机臂不落终态 ⇒ 它的 Status() 是空串，且空串不在词表里。
	if finalize.ActionShutdown.Status() != "" {
		t.Fatal("停机臂不得有终态词")
	}
}

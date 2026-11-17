// step_terminal_test.go —— 终态判据必须读转移表（issue #374）。

package statemachine

import "testing"

// TestTerminalMatchesTransitionTable IsTerminal 的接受集必须恰好等于转移表里
// 「没有出边」的状态集合；写法变体同结论；词表外不是终态。
func TestTerminalMatchesTransitionTable(t *testing.T) {
	for _, status := range allStepStatuses() {
		want := len(transitions[status]) == 0
		for _, variant := range stateVariants(string(status)) {
			if got := IsTerminal(StepStatus(variant)); got != want {
				t.Errorf("IsTerminal(%q)=%v，转移表说 %v（%q 的出边 %v）",
					variant, got, want, status, transitions[status])
			}
		}
	}
	for _, unknown := range []StepStatus{"", "bogus", "DEGRADED"} {
		if IsTerminal(unknown) {
			t.Errorf("词表外 %q 不得被判为终态（「读不懂」不比「终态」安全）", unknown)
		}
	}
}

// TestTerminalFollowsTransitionTable 反向验证的常驻形式：给一个当前终态临时
// 加一条出边，IsTerminal 必须立即跟随；恢复后必须回到终态。
//
// 这正是 issue #374 的失效场景——硬编码清单在转移表变化后仍说旧答案。
// 用例在包内串行执行（本包没有 t.Parallel），defer 恢复全局表。
func TestTerminalFollowsTransitionTable(t *testing.T) {
	if !IsTerminal(StatusSucceeded) {
		t.Fatal("前置不成立：succeeded 当前应为终态")
	}
	transitions[StatusSucceeded] = []StepStatus{StatusRolledBack}
	defer delete(transitions, StatusSucceeded)
	if IsTerminal(StatusSucceeded) {
		t.Fatal("给 succeeded 加出边后 IsTerminal 仍为 true——判据没有读转移表")
	}
	if IsTerminal(StepStatus("SUCCEEDED")) {
		t.Fatal("写法变体也不得在被加出边后仍判终态")
	}
}

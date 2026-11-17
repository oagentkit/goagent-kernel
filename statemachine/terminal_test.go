package statemachine

// terminal_test.go —— 环节终态的派生口径与跨包概念边界
// （issue #354 / #374）。

import (
	"slices"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

// wantTerminalSteps 环节终态的**冻结清单**（7 值）。
//
// 为什么要冻结：终态集从转移表**派生**（无出边 = 终态），故「派生集 ==
// 转移表无出边集」在同源设计下天然恒绿——真正的牙齿是这份冻结清单：
// 给转移表加一条出边（而不同步终态语义），本用例立刻转红（#374 的
// 反向验证落点）。
var wantTerminalSteps = []string{
	"cancelled",
	"expired",
	"interrupted",
	"rejected",
	"rolled_back",
	"skipped",
	"succeeded",
}

// TestIsStepTerminalMatchesTransitionTable 终态判据必须与转移表一致。
//
// 两条断言：
//
//  1. **派生性质**：IsStepTerminal 的接受集 == 转移表中无出边的状态；
//  2. **冻结清单**：派生集 == 上面那份 7 值清单（转移表改了而终态语义
//     没同步 ⇒ 第 2 条转红）。
func TestIsStepTerminalMatchesTransitionTable(t *testing.T) {
	var derived, noEdge []string
	for _, s := range AllStepStatuses() {
		if IsStepTerminal(StepStatus(s)) {
			derived = append(derived, s)
		}
		if len(transitions[StepStatus(s)]) == 0 {
			noEdge = append(noEdge, s)
		}
	}
	if !slices.Equal(derived, noEdge) {
		t.Fatalf("终态集与转移表不一致：IsStepTerminal 接受 %v，转移表无出边的是 %v", derived, noEdge)
	}
	if !slices.Equal(derived, wantTerminalSteps) {
		t.Fatalf("环节终态清单漂移：派生集 %v，期望 %v（转移表与终态语义必须同步）", derived, wantTerminalSteps)
	}
	// failed **不是**终态：它还有出边 → rolled_back（失败的工作通常要重做）。
	if IsStepTerminal(StatusFailed) {
		t.Fatal("failed 不得是环节终态（它还有出边 → rolled_back）")
	}
	if !CanTransition(StatusFailed, StatusRolledBack) {
		t.Fatal("failed → rolled_back 必须仍是合法迁移（它是失败后的正常去向）")
	}
	// 词表外不是终态（那会掩盖「读不懂」的事实）。
	if IsStepTerminal(StepStatus("bogus")) {
		t.Fatal("词表外取值不得被判终态")
	}
}

// TestIsTerminalAlias 兼容别名与新名**逐字同义**：
// 旧名 IsTerminal 保留给既有调用点，语义不得与 IsStepTerminal 分叉。
func TestIsTerminalAlias(t *testing.T) {
	for _, s := range AllStepStatuses() {
		if IsTerminal(StepStatus(s)) != IsStepTerminal(StepStatus(s)) {
			t.Errorf("IsTerminal(%q) 与 IsStepTerminal 结论不一致", s)
		}
	}
}

// TestSessionTerminalAndStepTerminalAreDistinctConcepts 跨包断言：
// **会话终态**与**环节终态是两个概念**，各有各的词表（issue #354）。
//
// 为什么结论相反**不是分叉**：
//
//   - 会话侧 failed 是终态——这一轮编排不会再自行前进，落库为 failed 的
//     会话已经结束（宿主据此落终态、停止续跑）；
//   - 环节侧 failed **不是**终态——环节还有出边 → rolled_back：失败的工作
//     通常要重做，回滚是失败后的正常去向。
//
// 两个判据各自服务于自己的生命周期；把它们合并成一张表反而会让「失败后
// 能不能重做」这个语义消失（会话表没有 rolled_back，环节表没有
// timed_out，且 failed 的归属相反——不存在同时满足两边的词表）。
func TestSessionTerminalAndStepTerminalAreDistinctConcepts(t *testing.T) {
	// 会话侧：failed 是终态。
	if !contract.IsSessionTerminal(contract.StatusFailed) {
		t.Fatal("failed 在**会话**侧必须是终态（这一轮编排不会再自行前进）")
	}
	// 环节侧：failed 不是终态。
	if IsStepTerminal(StatusFailed) {
		t.Fatal("failed 在**环节**侧不得是终态（它还有出边 → rolled_back）")
	}
	// 两包各自的词表成员集合（冻结清单，写进用例）。
	wantSession := []string{
		"cancelled", "created", "failed", "running", "succeeded", "timed_out", "waiting_approval",
	}
	if got := contract.AllStatuses(); !slices.Equal(got, wantSession) {
		t.Fatalf("会话状态词表漂移：%v，期望 %v", got, wantSession)
	}
	if got := AllStepStatuses(); !slices.Equal(got, wantStepStatuses) {
		t.Fatalf("环节状态词表漂移：%v，期望 %v", got, wantStepStatuses)
	}
	// 会话侧独有的 timed_out：环节词表里**没有**（会话超时不是环节状态）。
	if slices.Contains(AllStepStatuses(), contract.StatusTimedOut) {
		t.Fatal("timed_out 是会话侧独有的取值，不得混进环节词表")
	}
	// 环节侧独有的 rolled_back：会话词表里**没有**。
	if slices.Contains(contract.AllStatuses(), string(StatusRolledBack)) {
		t.Fatal("rolled_back 是环节侧独有的取值，不得混进会话词表")
	}
}

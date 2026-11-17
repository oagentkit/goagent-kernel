package statemachine

import (
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// 全转移表覆盖（DONE_CHECKLIST 2.6：合法转移每条 ≥1 正用例、非法转移 ≥1 负用例）。
func TestLegalTransitions(t *testing.T) {
	legal := [][2]StepStatus{
		{StatusCreated, StatusRunning},
		{StatusCreated, StatusSkipped},
		{StatusRunning, StatusSucceeded},
		{StatusRunning, StatusFailed},
		{StatusRunning, StatusWaitingApproval},
		{StatusWaitingApproval, StatusApproved},
		{StatusWaitingApproval, StatusRejected},
		{StatusWaitingApproval, StatusExpired},
		{StatusApproved, StatusRunning},
		{StatusFailed, StatusRolledBack},
	}
	for _, pair := range legal {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("期望合法: %s → %s", pair[0], pair[1])
		}
	}
}

func TestIllegalTransitions(t *testing.T) {
	illegal := [][2]StepStatus{
		{StatusCreated, StatusSucceeded},         // 跳过运行
		{StatusCreated, StatusApproved},          // 跳过审批
		{StatusSucceeded, StatusRunning},         // 终态复活
		{StatusRejected, StatusRunning},          // 驳回后直接运行（必须走修订重提）
		{StatusExpired, StatusApproved},          // 超时后批准
		{StatusRunning, StatusApproved},          // 运行态直接批
		{StatusApproved, StatusSucceeded},        // 批准未执行即成功
		{StatusRolledBack, StatusRunning},        // 回退后复活
		{StatusWaitingApproval, StatusSucceeded}, // 待批直接成功
	}
	for _, pair := range illegal {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("期望非法: %s → %s", pair[0], pair[1])
		}
		if err := MustTransition(pair[0], pair[1]); err == nil {
			t.Errorf("MustTransition 应报错: %s → %s", pair[0], pair[1])
		}
	}
}

func TestRequiresApproval(t *testing.T) {
	cases := map[StepType]bool{
		TypeReadonly: false,
		TypePlan:     true,
		TypeExecute:  true,
		TypeApproval: false,
		TypeVerify:   false,
	}
	for typ, want := range cases {
		if got := RequiresApproval(typ); got != want {
			t.Errorf("RequiresApproval(%s)=%v, want %v", typ, got, want)
		}
	}
}

// TestTerminal 12 个环节状态全覆盖（旧用例只列了 5 个终态，failed /
// interrupted / cancelled 三个「无争议也无人钉」的状态因此漂了很久——
// issue #354）。期望值来自 stepVocabulary 的唯一口径。
func TestTerminal(t *testing.T) {
	for _, tc := range stepVocabulary {
		if got := IsTerminal(tc.status); got != tc.wantTerminal {
			t.Errorf("IsTerminal(%s) = %v, want %v", tc.status, got, tc.wantTerminal)
		}
	}
}

// TestStepTypeValid 词表外取值必须可判定：宿主需要区分「这个类别免审」与
// 「内核不认识这个类别」。
func TestStepTypeValid(t *testing.T) {
	valid := []StepType{
		TypeReadonly, TypePlan, TypeApproval, TypeExecute, TypeVerify,
		// 写法变体归一后仍属词表内（同一类别，不是新类别）。
		"PLAN", " plan ", "Execute", "READONLY", " verify ",
	}
	for _, typ := range valid {
		if !typ.Valid() {
			t.Errorf("StepType(%q).Valid() 应为 true", typ)
		}
	}
	invalid := []StepType{"", "remediate", "PLANN", "exec", "审批", "approval "}
	// "approval " 归一后合法，单列出来说明它不是非法值。
	invalid = invalid[:len(invalid)-1]
	for _, typ := range invalid {
		if typ.Valid() {
			t.Errorf("StepType(%q).Valid() 应为 false", typ)
		}
	}
}

// TestRequiresApprovalFailClosed 审批类判定面的沉默必须落在**最严**一侧：
// 词表外类别（含大小写变体写成词表外）一律要求审批，不得判免审。
func TestRequiresApprovalFailClosed(t *testing.T) {
	cases := []struct {
		typ  StepType
		want bool
	}{
		{TypeReadonly, false},
		{TypePlan, true},
		{TypeApproval, false},
		{TypeExecute, true},
		{TypeVerify, false},
		// 大小写/空白变体与规范形态结论一致。
		{"PLAN", true},
		{"Execute", true},
		{" plan ", true},
		{"READONLY", false},
		{" verify ", false},
		// 词表外：保守侧（要求审批）。
		{"remediate", true},
		{"", true},
		{"PLANN", true},
		{"审批", true},
	}
	for _, c := range cases {
		if got := RequiresApproval(c.typ); got != c.want {
			t.Errorf("RequiresApproval(%q)=%v, want %v", c.typ, got, c.want)
		}
	}
}

// TestMustTransitionUnknownStatus 「状态不在词表内」必须与「非法转移」分成
// 两条可判定的错误：前者是「读不懂」，后者是「迁移写错了」，排障方向完全不同。
func TestMustTransitionUnknownStatus(t *testing.T) {
	unknownFrom := MustTransition(StepStatus("bogus"), StatusRunning)
	if unknownFrom == nil {
		t.Fatal("词表外 from 必须报错")
	}
	if !errors.Is(unknownFrom, ErrUnknownStatus) {
		t.Fatalf("词表外 from 应报 ErrUnknownStatus，实得 %v", unknownFrom)
	}
	if errors.Is(unknownFrom, ErrIllegalTransition) {
		t.Fatalf("词表外 from 不得被报成非法转移：%v", unknownFrom)
	}
	if strings.Contains(unknownFrom.Error(), "非法状态转移") {
		t.Fatalf("词表外 from 的错误文案不得含「非法状态转移」：%v", unknownFrom)
	}

	unknownTo := MustTransition(StatusCreated, StepStatus("bogus"))
	if !errors.Is(unknownTo, ErrUnknownStatus) {
		t.Fatalf("词表外 to 应报 ErrUnknownStatus，实得 %v", unknownTo)
	}
	if strings.Contains(unknownTo.Error(), "非法状态转移") {
		t.Fatalf("词表外 to 的错误文案不得含「非法状态转移」：%v", unknownTo)
	}

	// 词表内但迁移非法：报 ErrIllegalTransition，不是未知状态。
	illegal := MustTransition(StatusCreated, StatusApproved)
	if !errors.Is(illegal, ErrIllegalTransition) {
		t.Fatalf("非法迁移应报 ErrIllegalTransition，实得 %v", illegal)
	}
	if errors.Is(illegal, ErrUnknownStatus) {
		t.Fatalf("非法迁移不得被报成未知状态：%v", illegal)
	}
	if !strings.Contains(illegal.Error(), "非法状态转移") {
		t.Fatalf("非法迁移的文案应保留「非法状态转移」：%v", illegal)
	}
}

// TestStepStatusValid 全部规范状态必须 Valid，词表外一律 false。
func TestStepStatusValid(t *testing.T) {
	valid := []StepStatus{
		StatusCreated, StatusRunning, StatusWaitingApproval, StatusApproved,
		StatusRejected, StatusExpired, StatusSucceeded, StatusFailed,
		StatusInterrupted, StatusRolledBack, StatusSkipped, StatusCancelled,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("StepStatus(%q).Valid() 应为 true", s)
		}
	}
	// "RUNNING" / "running " 是合法状态 running 的写法变体（issue #373），
	// 归一后为 true；真词表外用 bogus / degraded 这类形态。
	for _, s := range []StepStatus{"", "bogus", "RUNNNG", "degraded", "status "} {
		if s.Valid() {
			t.Errorf("StepStatus(%q).Valid() 应为 false", s)
		}
	}
}

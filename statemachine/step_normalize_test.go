// step_normalize_test.go —— 环节状态的「是否在词表内」口径必须同形（issue #373）。
//
// StepType 归一后判定，StepStatus 却精确查表。纯大小写变体会被
// MustTransition 报成「状态不在词表内」（读不懂），把宿主导向「查数据
// 来源/版本」——而它只是写法差异，该查的是迁移代码。
//
// 归一口径只有一条：StepStatus 与 StepType 同形（ToLower + TrimSpace），
// 且 CanTransition 的查表也走同一归一。

package statemachine

import (
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// stateVariants 一个取值的写法变体：原值、全大写、首字母大写、前后空白。
func stateVariants(v string) []string {
	if v == "" {
		return []string{""}
	}
	upper := strings.ToUpper(v)
	title := upper[:1] + v[1:]
	return []string{v, upper, title, " " + v + " ", "  " + upper + "  "}
}

// allStepStatuses 本包的完整状态词表（用于表驱动断言）。
func allStepStatuses() []StepStatus {
	return []StepStatus{
		StatusCreated, StatusRunning, StatusWaitingApproval, StatusApproved,
		StatusRejected, StatusExpired, StatusSucceeded, StatusFailed,
		StatusInterrupted, StatusRolledBack, StatusSkipped, StatusCancelled,
	}
}

// allStepTypes 本包的完整环节类别词表。
func allStepTypes() []StepType {
	return []StepType{TypeReadonly, TypePlan, TypeApproval, TypeExecute, TypeVerify}
}

// TestStepStatusValidNormalizesLikeStepType 同一组大小写/空白变体必须给出一致
// 结论：StepStatus 与 StepType 是同一个包里的两个枚举，不能各用一套约定。
func TestStepStatusValidNormalizesLikeStepType(t *testing.T) {
	for _, status := range allStepStatuses() {
		for _, variant := range stateVariants(string(status)) {
			if !StepStatus(variant).Valid() {
				t.Errorf("StepStatus(%q).Valid() 应为 true（%q 的写法变体）", variant, status)
			}
		}
	}
	for _, typ := range allStepTypes() {
		for _, variant := range stateVariants(string(typ)) {
			if !StepType(variant).Valid() {
				t.Errorf("StepType(%q).Valid() 应为 true（%q 的写法变体）", variant, typ)
			}
		}
	}
	// 归一不是「什么都收」：真词表外仍然 false。
	for _, bad := range []StepStatus{"", "bogus", "RUNNNG", "PLANN", "degraded", "status"} {
		if bad.Valid() {
			t.Errorf("StepStatus(%q).Valid() 应为 false", bad)
		}
	}
	for _, bad := range []StepType{"", "remediate", "PLANN", "exec"} {
		if bad.Valid() {
			t.Errorf("StepType(%q).Valid() 应为 false", bad)
		}
	}
}

// TestMustTransitionAcceptsSpellingVariants 词表内的写法变体必须走正常迁移
// 判定；只有真词表外才报 ErrUnknownStatus，真非法迁移才报 ErrIllegalTransition。
func TestMustTransitionAcceptsSpellingVariants(t *testing.T) {
	for _, from := range stateVariants(StatusRunning) {
		for _, to := range stateVariants(StatusSucceeded) {
			if err := MustTransition(StepStatus(from), StepStatus(to)); err != nil {
				t.Errorf("MustTransition(%q, %q) 应合法: %v", from, to, err)
			}
		}
	}

	// 合法词表 + 非法迁移：仍然必须是 ErrIllegalTransition（不能被归一吞成
	// 未知状态，也不能被归一放行）。
	err := MustTransition(StepStatus("RUNNING"), StepStatus("APPROVED"))
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("RUNNING → APPROVED 应报 ErrIllegalTransition，实得 %v", err)
	}

	// 真词表外：大小写变体救不了它。
	for _, unknown := range []StepStatus{"bogus", "BOGUS", "bogus ", "degraded", "PLANN"} {
		err := MustTransition(unknown, StatusRunning)
		if !errors.Is(err, ErrUnknownStatus) {
			t.Errorf("MustTransition(%q, running) 应报 ErrUnknownStatus，实得 %v", unknown, err)
		}
		if errors.Is(err, ErrIllegalTransition) {
			t.Errorf("词表外 %q 不得被报成非法迁移: %v", unknown, err)
		}
	}
}

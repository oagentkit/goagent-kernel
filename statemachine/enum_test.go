// enum_test.go —— 环节状态 / 环节类别的枚举出口与谓词一致性判据（issue #356）。
package statemachine

import (
	"slices"
	"testing"
)

// TestAllStepStatusesMatchesValid 枚举出口与谓词必须**双向**一致：
// 枚举里的每个值都过 Valid()；Valid() 为真的每个值都在枚举里。
//
// 反向验证：把 stepStatusAll 里删掉一个值 → 两侧同步少一个（断言仍绿，
// 因为它们同源）；把枚举改成另抄一份列表 → 本用例转红。
func TestAllStepStatusesMatchesValid(t *testing.T) {
	all := AllStepStatuses()
	if !slices.IsSorted(all) {
		t.Fatalf("枚举出口必须返回排序后的切片：%v", all)
	}
	if len(all) != len(stepStatusAll) {
		t.Fatalf("枚举出口漏值：AllStepStatuses() = %d 条，词表 %d 条（%v）", len(all), len(stepStatusAll), all)
	}
	for _, v := range all {
		if !StepStatus(v).Valid() {
			t.Fatalf("枚举出口出现词表外的值 %q（Valid() 不认它）", v)
		}
	}
	for v := range stepStatusAll {
		if !slices.Contains(all, string(v)) {
			t.Fatalf("词表里的值 %q 没有出现在枚举出口里", v)
		}
	}
	// 副本语义。
	if len(all) > 0 {
		all[0] = "__mutated_by_host__"
		if slices.Contains(AllStepStatuses(), "__mutated_by_host__") {
			t.Fatal("枚举出口返回了共享底层数组：宿主改写污染了词表")
		}
	}
}

// TestAllStepTypesMatchesValid 同上，环节类别面。
func TestAllStepTypesMatchesValid(t *testing.T) {
	all := AllStepTypes()
	if !slices.IsSorted(all) {
		t.Fatalf("枚举出口必须返回排序后的切片：%v", all)
	}
	if len(all) != len(stepTypeAll) {
		t.Fatalf("枚举出口漏值：AllStepTypes() = %d 条，词表 %d 条（%v）", len(all), len(stepTypeAll), all)
	}
	for _, v := range all {
		if !StepType(v).Valid() {
			t.Fatalf("枚举出口出现词表外的值 %q（Valid() 不认它）", v)
		}
	}
	for v := range stepTypeAll {
		if !slices.Contains(all, string(v)) {
			t.Fatalf("词表里的值 %q 没有出现在枚举出口里", v)
		}
	}
}

// TestStepStatusesExcludeSessionOnly 取值 timed_out 是**会话独有**取值，
// TestAllStepStatusesFollowsTheTable 反向验证：枚举出口必须**跟着词表走**。
//
// 临时从 stepStatusAll 删掉一个取值，枚举出口必须同步少一个——若枚举是另抄的
// 一份清单，这里立刻转红（issue #356 验收 1 的反向验证）。
func TestAllStepStatusesFollowsTheTable(t *testing.T) {
	saved, had := stepStatusAll[StatusSkipped]
	delete(stepStatusAll, StatusSkipped)
	defer func() {
		if had {
			stepStatusAll[StatusSkipped] = saved
			return
		}
		delete(stepStatusAll, StatusSkipped)
	}()
	for _, v := range AllStepStatuses() {
		if v == string(StatusSkipped) {
			t.Fatal("枚举出口仍含已从词表删除的取值（枚举是另抄的一份清单）")
		}
	}
	if StepStatus(StatusSkipped).Valid() {
		t.Fatal("词表删除后谓词仍为真")
	}
}

// 环节侧按词表外处理——那是轴的边界，不是判据分叉（issue #354）。
//
// 本用例把这条边界钉住：枚举出口里不得出现 timed_out。
func TestStepStatusesExcludeSessionOnly(t *testing.T) {
	if slices.Contains(AllStepStatuses(), "timed_out") {
		t.Fatal("环节状态枚举不得包含会话独有的 timed_out")
	}
}

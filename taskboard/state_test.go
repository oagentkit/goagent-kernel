// state_test.go —— 任务状态枚举出口与谓词一致性判据（issue #356）。
package taskboard

import (
	"slices"
	"testing"
)

// TestAllStatesMatchesValid 枚举出口与谓词必须**双向**一致：枚举里的每个
// 值都过 Valid()；Valid() 为真的每个值都在枚举里。
func TestAllStatesMatchesValid(t *testing.T) {
	all := AllStates()
	if !slices.IsSorted(all) {
		t.Fatalf("枚举出口必须返回排序后的切片：%v", all)
	}
	if len(all) != len(taskStateAll) {
		t.Fatalf("枚举出口漏值：AllStates() = %d 条，词表 %d 条（%v）", len(all), len(taskStateAll), all)
	}
	for _, v := range all {
		if !v.Valid() {
			t.Fatalf("枚举出口出现词表外的值 %q（Valid() 不认它）", v)
		}
	}
	for v := range taskStateAll {
		if !slices.Contains(all, v) {
			t.Fatalf("词表里的值 %q 没有出现在枚举出口里", v)
		}
	}
	// 副本语义。
	if len(all) > 0 {
		all[0] = State("__mutated_by_host__")
		if slices.Contains(AllStates(), State("__mutated_by_host__")) {
			t.Fatal("枚举出口返回了共享底层数组：宿主改写污染了词表")
		}
	}
}

// TestAllStatesCoversClaimableStates 枚举出口给出的四个取值都能认领
// TestAllStatesFollowsTheTable 反向验证：枚举出口必须**跟着词表走**。
//
// 临时从 taskStateAll 删掉一个取值，枚举出口必须同步少一个——若枚举是另抄的
// 一份清单，这里立刻转红（issue #356 验收 1 的反向验证）。
func TestAllStatesFollowsTheTable(t *testing.T) {
	saved, had := taskStateAll[StateFailed]
	delete(taskStateAll, StateFailed)
	defer func() {
		if had {
			taskStateAll[StateFailed] = saved
			return
		}
		delete(taskStateAll, StateFailed)
	}()
	for _, v := range AllStates() {
		if v == StateFailed {
			t.Fatal("枚举出口仍含已从词表删除的取值（枚举是另抄的一份清单）")
		}
	}
}

// （除终态 done）——枚举与写路径的判据不是两张表。
func TestAllStatesCoversClaimableStates(t *testing.T) {
	for _, s := range AllStates() {
		err := s.Claimable()
		if s == StateDone {
			if err == nil {
				t.Fatalf("终态 done 必须不可认领")
			}
			continue
		}
		if err != nil {
			t.Fatalf("状态 %q 应可认领，实得 %v", s, err)
		}
	}
}

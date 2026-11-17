// status_test.go —— 回执状态词表的**单一事实源**（issue #358）。
//
// valid() 此前是私有的：宿主连「合法值有哪些」都问不到，只能自己抄一份
// 清单——那份清单必然与内核漂移。
package delegate

import (
	"slices"
	"testing"
)

// TestAllStatusesAndValidAgree 枚举与谓词必须双向一致：
// 枚举 ⊆ 谓词 ∧ 谓词 ⊆ 枚举。
//
// 反向验证：删掉某个常量（谓词还在、枚举不在）⇒ 本条转红。
func TestAllStatusesAndValidAgree(t *testing.T) {
	all := AllStatuses()
	if len(all) != 3 {
		t.Fatalf("回执状态词表应为 3 个取值，实得 %v", all)
	}
	// 枚举 ⊆ 谓词
	for _, s := range all {
		if !s.Valid() {
			t.Fatalf("枚举里的 %q 必须通过谓词", s)
		}
	}
	// 谓词 ⊆ 枚举（三个常量逐条在枚举里）
	for _, s := range []Status{StatusCompleted, StatusFailed, StatusRefused} {
		if !s.Valid() {
			t.Fatalf("%q 在词表内，谓词必须为真", s)
		}
		if !slices.Contains(all, s) {
			t.Fatalf("枚举漏了 %q：AllStatuses=%v", s, all)
		}
	}
}

// TestStatusRejectsOutOfVocabulary 词表外取值必须被谓词拒绝。
func TestStatusRejectsOutOfVocabulary(t *testing.T) {
	for _, s := range []Status{"done", "", "completed_v2", "COMPLETED"} {
		if s.Valid() {
			t.Fatalf("词表外的 %q 必须被谓词拒绝", s)
		}
	}
}

// TestAllStatusesReturnsCopy 返回的是副本：调用方改写它不影响判定面。
func TestAllStatusesReturnsCopy(t *testing.T) {
	all := AllStatuses()
	all[0] = Status("mutated_by_host")
	if !StatusCompleted.Valid() {
		t.Fatal("AllStatuses 返回了共享底层数组：宿主改写污染了词表")
	}
	if slices.Contains(AllStatuses(), Status("mutated_by_host")) {
		t.Fatal("AllStatuses 返回了共享底层数组：宿主改写污染了词表")
	}
}

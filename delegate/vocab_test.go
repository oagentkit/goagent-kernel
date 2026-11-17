package delegate

// 回执状态词表的枚举出口（issue #358）：
// 宿主必须能问「合法值有哪些」，不得靠手抄三个常量；枚举与 Valid 的成员
// 集合必须双向一致（#320 已立的规矩）。

import "testing"

// TestAllStatusesMatchesValid 枚举与 valid 双向一致，且集合大小钉住。
//
// 反向验证：往 AllStatuses 里塞一个词表外取值（或从 Valid 的 case 里删掉
// 一个），本用例转红——「词表 + 判定」只能有一份成员集合。
func TestAllStatusesMatchesValid(t *testing.T) {
	all := AllStatuses()
	if len(all) != 3 {
		t.Fatalf("回执状态应恰为 3 个（completed / failed / refused），实得 %d: %v", len(all), all)
	}
	seen := make(map[Status]bool, len(all))
	for _, s := range all {
		if !s.Valid() {
			t.Errorf("AllStatuses 里的 %q 必须 Valid", s)
		}
		if seen[s] {
			t.Errorf("AllStatuses 出现重复取值 %q", s)
		}
		seen[s] = true
	}
	// 反向：每个合法常量都必须被枚举覆盖。
	for _, s := range []Status{StatusCompleted, StatusFailed, StatusRefused} {
		if !seen[s] {
			t.Errorf("AllStatuses 漏掉了合法状态 %q", s)
		}
	}
	// 词表外一律不合法。
	for _, s := range []Status{"", "done", "succeeded", "ok", "bogus"} {
		if s.Valid() {
			t.Errorf("词表外取值 %q 不得 Valid", s)
		}
	}
}

// TestAllStatusesReturnsCopy 枚举出口不得把内部切片交给宿主改（一次写入
// 污染后续所有调用）。
func TestAllStatusesReturnsCopy(t *testing.T) {
	got := AllStatuses()
	got[0] = Status("hacked")
	if again := AllStatuses(); again[0] != StatusCompleted {
		t.Fatalf("AllStatuses 返回了共享切片：第二次调用首项 = %q", again[0])
	}
}

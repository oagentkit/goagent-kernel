package handoff

// 回执状态词表与 delegate **同源**（issue #358）。
//
// delegate.Status 与 handoff.ReceiptStatus 是同一个语义槽位（完成 / 做过尝试
// 的失败 / 没尝试的拒绝）。此前两包各写一份字面量，完成态分别是 completed
// 与 done——宿主做跨包聚合时两列值域不同却语义相同。现在 handoff 的类型是
// delegate.Status 的**别名**，完成态统一为 "completed"。

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/delegate"
)

// TestReceiptVocabularySharedWithDelegate 三值逐条相等 + 完成态字面量相同。
//
// 反向验证：把 ReceiptCompleted 改成一个既非 "completed" 又非
// delegate.StatusCompleted 的第三个词，本用例转红。
func TestReceiptVocabularySharedWithDelegate(t *testing.T) {
	// 类型同一性（编译期）：两者是同一个类型，不是「值碰巧相同」。
	// 只有类型别名能不经转换直接传给 delegate.Status 形参；两个独立定义
	// 的 string 类型会在编译期失败。
	var r ReceiptStatus
	acceptDelegateStatus(r)

	if string(ReceiptCompleted) != string(delegate.StatusCompleted) {
		t.Fatalf("完成态字面量必须相同：handoff=%q delegate=%q",
			ReceiptCompleted, delegate.StatusCompleted)
	}
	if string(ReceiptCompleted) != "completed" {
		t.Fatalf("完成态统一为 completed，实得 %q", ReceiptCompleted)
	}
	// 历史常量 ReceiptDone 必须仍是指向同一取值的别名（保留名字不破坏既有调用方）。
	//nolint:staticcheck // 兼容别名是发布面的一部分，这里刻意引用它钉住同值。
	if string(ReceiptDone) != string(ReceiptCompleted) {
		t.Fatalf("ReceiptDone 必须是 ReceiptCompleted 的别名：%q vs %q", ReceiptDone, ReceiptCompleted)
	}
	if string(ReceiptFailed) != string(delegate.StatusFailed) {
		t.Fatalf("失败态必须相同：handoff=%q delegate=%q", ReceiptFailed, delegate.StatusFailed)
	}
	if string(ReceiptRefused) != string(delegate.StatusRefused) {
		t.Fatalf("拒绝态必须相同：handoff=%q delegate=%q", ReceiptRefused, delegate.StatusRefused)
	}

	// 合法值集合逐条相等：delegate 的枚举出口就是 handoff 的词表。
	all := delegate.AllStatuses()
	if len(all) != 3 {
		t.Fatalf("delegate 词表应恰为 3 个，实得 %v", all)
	}
	for _, s := range all {
		r := Receipt{Status: s}
		if s == delegate.StatusCompleted {
			r.Summary = "ok" // 完成态必须有结论，否则 Validate 会因自洽性而不是词表报错
		} else {
			r.Failure = "原因"
		}
		if err := r.Validate(); err != nil {
			t.Errorf("delegate 合法状态 %q 在 handoff 必须可校验通过: %v", s, err)
		}
	}
	// 旧的 done 字面量已不在词表内——写入即拒（#359 的写路径 fail-closed）。
	if err := (Receipt{Status: "done", Summary: "ok"}).Validate(); err == nil {
		t.Fatal("旧字面量 done 已不是合法回执状态，必须报错")
	}
}

// acceptDelegateStatus 编译期类型同一性探针：ReceiptStatus 若不是
// delegate.Status 的别名，本调用无法通过编译。
func acceptDelegateStatus(delegate.Status) {}

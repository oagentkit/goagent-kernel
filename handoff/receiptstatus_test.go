// receiptstatus_test.go —— 回执状态词表与 delegate 同源（issue #358）。
//
// 两包是同一条纪律（完成 / 尝试过的失败 / 没尝试的拒绝），注释自己就写着
// 「与 delegate.Status 同一条纪律」——却各写一份，且完成态字面量不同
// （completed vs done）。真源放 delegate（它已经是「回执状态」的契约面），
// handoff 引用之。
package handoff

import (
	"slices"
	"testing"

	"github.com/yi-nology/goagent-kernel/delegate"
)

// TestReceiptStatusSharesDelegateVocabulary 两边的合法值集合必须逐条相等，
// 完成态字面量必须相同。
//
// 反向验证：把 handoff 的 ReceiptDone 改成第三个词 ⇒ 本条转红。
func TestReceiptStatusSharesDelegateVocabulary(t *testing.T) {
	want := make([]string, 0, 3)
	for _, s := range delegate.AllStatuses() {
		want = append(want, string(s))
	}
	got := []string{string(ReceiptDone), string(ReceiptFailed), string(ReceiptRefused)}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("回执状态词表与 delegate 不一致：handoff=%v，delegate=%v", got, want)
	}
	if string(ReceiptDone) != string(delegate.StatusCompleted) {
		t.Fatalf("完成态字面量必须相同：handoff=%q，delegate=%q",
			ReceiptDone, delegate.StatusCompleted)
	}
}

// TestReceiptStatusNeedsNoHandCopy 宿主不必手抄词表：delegate 侧导出了枚举
// 与谓词，handoff 侧只引用它。
//
// 反向验证：把 delegate 的 valid() 改回私有 ⇒ 本条转红（编译期）。
func TestReceiptStatusNeedsNoHandCopy(t *testing.T) {
	for _, s := range delegate.AllStatuses() {
		if !s.Valid() {
			t.Fatalf("delegate 的枚举与谓词必须一致：%q 不在词表内", s)
		}
	}
	// 旧字面量（handoff 曾经用的 "done"）现在不在词表内：同一个语义槽位
	// 只允许一个字面量。
	if delegate.Status("done").Valid() {
		t.Fatal("完成态的字面量必须统一为 completed（done 不得再合法）")
	}
}

// status_test.go —— 状态词表的枚举面与三态分类的判据（issue #356 / #363）。
package contract

import (
	"slices"
	"testing"
)

// TestAllStatusesMatchesIsKnownStatus 枚举出口与成员谓词必须**双向**一致
// （issue #356）：枚举里的每个值都过谓词；谓词为真的每个值都在枚举里。
//
// 单向断言（只测「枚举出的值合法」）会漏掉「词表里有但没导出」——那正是
// 宿主手抄清单漂移的入口。
func TestAllStatusesMatchesIsKnownStatus(t *testing.T) {
	all := AllStatuses()
	if !slices.IsSorted(all) {
		t.Fatalf("枚举出口必须返回排序后的切片：%v", all)
	}
	if len(all) != len(statusClass) {
		t.Fatalf("枚举出口漏值：AllStatuses() = %d 条，词表 %d 条（%v）", len(all), len(statusClass), all)
	}
	for _, v := range all {
		if !IsKnownStatus(v) {
			t.Fatalf("枚举出口出现词表外的值 %q（成员谓词不认识它）", v)
		}
	}
	for v := range statusClass {
		if !slices.Contains(all, v) {
			t.Fatalf("词表里的值 %q 没有出现在枚举出口里", v)
		}
	}
	// 副本语义：宿主改写拿到的清单不得影响判定面。
	if len(all) > 0 {
		all[0] = "__mutated_by_host__"
		again := AllStatuses()
		if slices.Contains(again, "__mutated_by_host__") {
			t.Fatal("枚举出口返回了共享底层数组：宿主改写污染了词表")
		}
	}
}

// TestClassifyStatusPinned 三态分类逐值钉住（issue #363）。
//
// 这张表是「什么算词表外」这条边界的**唯一**真相：checkpoint 与
// controlplane 都读它，两处对同一状态必须得到同一结论。
//
// 归一：大小写与空白变体与规范形态同结论——否则「同一个状态在两包得到
// 相反结论」的形态会从写法差异里长出来（此前 checkpoint 只对终态侧归一、
// 对过程态侧精确比对，同一个 "RUNNING" 一处被认一处被判读不懂）。
func TestClassifyStatusPinned(t *testing.T) {
	cases := []struct {
		status string
		want   StatusClass
	}{
		// 尚未声明状态：崩溃快照的常态，按过程态处理。
		{"", StatusOpen},
		{"   ", StatusOpen},
		// 会话轴。
		{"created", StatusOpen},
		{"running", StatusOpen},
		{"waiting_approval", StatusOpen},
		{"succeeded", StatusTerminal},
		{"failed", StatusTerminal},
		{"cancelled", StatusTerminal},
		{"timed_out", StatusTerminal},
		// 环节轴独有。
		{"approved", StatusOpen},
		{"rejected", StatusTerminal},
		{"expired", StatusTerminal},
		{"interrupted", StatusTerminal},
		{"rolled_back", StatusTerminal},
		{"skipped", StatusTerminal},
		// 词表外：判不了。
		{"aborted", StatusUnknown},
		{"degraded", StatusUnknown},
		{"unknown", StatusUnknown},
		// taskboard 的取值**不在**会话/环节词表内——两根轴各自的词表不混。
		{"done", StatusUnknown},
		{"claimed", StatusUnknown},
		// 写法变体：与规范形态同结论。
		{"RUNNING", StatusOpen},
		{" succeeded ", StatusTerminal},
		{"Timed_Out", StatusTerminal},
		{"ABORTED", StatusUnknown},
	}
	for _, c := range cases {
		if got := ClassifyStatus(c.status); got != c.want {
			t.Errorf("ClassifyStatus(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}

// TestIsKnownStatusPinned 成员谓词逐值钉住。
//
// 与 ClassifyStatus 的区别：**空串不是词表成员**（本函数为假），但它被
// 判成 StatusOpen（尚未声明状态）。
func TestIsKnownStatusPinned(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{"", false},
		{"   ", false},
		{"created", true},
		{"running", true},
		{"waiting_approval", true},
		{"succeeded", true},
		{"failed", true},
		{"cancelled", true},
		{"timed_out", true},
		{"approved", true},
		{"rejected", true},
		{"expired", true},
		{"interrupted", true},
		{"rolled_back", true},
		{"skipped", true},
		{"done", false},
		{"aborted", false},
		{"RUNNING", true},
		{" succeeded ", true},
	}
	for _, c := range cases {
		if got := IsKnownStatus(c.status); got != c.want {
			t.Errorf("IsKnownStatus(%q) = %v, want %v", c.status, got, c.want)
		}
	}
}

// TestIsTerminalStatusMatchesClassification 终态判定与三态分类必须一致。
// TestAllStatusesFollowsTheTable 反向验证：枚举出口必须**跟着词表走**。
//
// 临时从 statusClass 删掉一个取值，枚举出口必须同步少一个——若枚举是另抄的
// 一份清单，这里立刻转红（issue #356 验收 1 的反向验证）。
//
// 本包无 t.Parallel()，用例串行执行，改动包内 map 是安全的。
func TestAllStatusesFollowsTheTable(t *testing.T) {
	saved, had := statusClass[StatusSkipped]
	delete(statusClass, StatusSkipped)
	defer func() {
		if had {
			statusClass[StatusSkipped] = saved
			return
		}
		delete(statusClass, StatusSkipped)
	}()
	for _, v := range AllStatuses() {
		if v == StatusSkipped {
			t.Fatal("枚举出口仍含已从词表删除的取值（枚举是另抄的一份清单）")
		}
	}
	if IsKnownStatus(StatusSkipped) {
		t.Fatal("词表删除后成员谓词仍为真")
	}
}

// IsTerminalStatus 的判定读 statusClass 表（与 ClassifyStatus 同一张），
// 故两者对任何输入都必然一致——本用例把这条恒等式钉住，防止将来有人把
// 终态清单改回另一份 switch（那会让两个判据面各判一套）。
func TestIsTerminalStatusMatchesClassification(t *testing.T) {
	inputs := append([]string{"", "   ", "aborted", "degraded", "done", "claimed", "RUNNING", " succeeded "}, AllStatuses()...)
	for _, s := range inputs {
		want := ClassifyStatus(s) == StatusTerminal
		if got := IsTerminalStatus(s); got != want {
			t.Errorf("IsTerminalStatus(%q) = %v，与分类面不一致（want %v）", s, got, want)
		}
	}
}

package statemachine

// enum_test.go —— 判定词表的枚举出口与判定面（issue #356 / #373）。

import (
	"slices"
	"strings"
	"testing"
)

// wantStepStatuses 环节状态词表的**冻结清单**（12 值，排序后）。
//
// 为什么要冻结：枚举与判定同源（都读 stepStatusAll），双向断言在同源
// 设计下天然恒绿——真正的牙齿是这份冻结清单：词表新增一个取值而没同步
// 这里，用例立刻转红。
var wantStepStatuses = []string{
	"approved",
	"cancelled",
	"created",
	"expired",
	"failed",
	"interrupted",
	"rejected",
	"rolled_back",
	"running",
	"skipped",
	"succeeded",
	"waiting_approval",
}

// wantStepTypes 环节类别词表的**冻结清单**（5 值，排序后）。
var wantStepTypes = []string{"approval", "execute", "plan", "readonly", "verify"}

// TestAllStepStatuses 枚举出口：全集、排序、**新切片**。
func TestAllStepStatuses(t *testing.T) {
	got := AllStepStatuses()
	if !slices.Equal(got, wantStepStatuses) {
		t.Fatalf("环节状态词表漂移：AllStepStatuses=%v，期望 %v", got, wantStepStatuses)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("AllStepStatuses 必须返回排序后的切片: %v", got)
	}
	got[0] = "__mutated_by_host__"
	if slices.Contains(AllStepStatuses(), "__mutated_by_host__") {
		t.Fatal("AllStepStatuses 返回了共享底层数组：宿主改写污染了词表")
	}
}

// TestAllStepTypes 枚举出口：全集、排序、**新切片**。
func TestAllStepTypes(t *testing.T) {
	got := AllStepTypes()
	if !slices.Equal(got, wantStepTypes) {
		t.Fatalf("环节类别词表漂移：AllStepTypes=%v，期望 %v", got, wantStepTypes)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("AllStepTypes 必须返回排序后的切片: %v", got)
	}
	got[0] = "__mutated_by_host__"
	if slices.Contains(AllStepTypes(), "__mutated_by_host__") {
		t.Fatal("AllStepTypes 返回了共享底层数组：宿主改写污染了词表")
	}
}

// TestEnumerationIsProjectionOfPrivateTable 枚举出口必须是判定用的**同一张
// 私有表**的投影（issue #356）。
//
// 判据不是「枚举出的值合法」（那恒真），而是「枚举 = 私有表的投影」：
// 条数相等 + 表里每个成员都在枚举里。若枚举是另抄一份清单，此处转红。
func TestEnumerationIsProjectionOfPrivateTable(t *testing.T) {
	statusAll := AllStepStatuses()
	if len(statusAll) != len(stepStatusAll) {
		t.Fatalf("枚举出口漏值：AllStepStatuses %d 条，私有词表 %d 条（%v）",
			len(statusAll), len(stepStatusAll), statusAll)
	}
	for s := range stepStatusAll {
		if !slices.Contains(statusAll, string(s)) {
			t.Fatalf("私有词表里的值 %q 没有出现在枚举出口里", s)
		}
	}
	typeAll := AllStepTypes()
	if len(typeAll) != len(stepTypeAll) {
		t.Fatalf("枚举出口漏值：AllStepTypes %d 条，私有词表 %d 条（%v）",
			len(typeAll), len(stepTypeAll), typeAll)
	}
	for s := range stepTypeAll {
		if !slices.Contains(typeAll, string(s)) {
			t.Fatalf("私有词表里的值 %q 没有出现在枚举出口里", s)
		}
	}
}

// TestEnumerationAndPredicateAgreeBothWays 枚举与谓词必须**双向**一致：
// 枚举里的每个值都过谓词；谓词为真的每个值都在枚举里。
//
// 单向断言（只测「枚举出的值合法」）会漏掉「词表里有但没导出」——那正是
// 宿主手抄清单漂移的入口。
//
// # 归一化下「⊆」怎么读（issue #373）
//
// 枚举给的是**规范形态**的全集；谓词按归一后的形态判定。于是
// "RUNNING" 对谓词为真，但它归一后的成员 "running" 才是枚举里的那条——
// 双向断言按**归一后的规范形态**比对，否则「写法变体」会被误报成
// 「词表里有但没导出」。
func TestEnumerationAndPredicateAgreeBothWays(t *testing.T) {
	cases := []struct {
		name      string
		all       func() []string
		valid     func(string) bool
		candidate []string
	}{
		{
			name:  "环节状态",
			all:   AllStepStatuses,
			valid: func(s string) bool { return StepStatus(s).Valid() },
			candidate: []string{
				string(StatusCreated), string(StatusRunning), string(StatusWaitingApproval),
				string(StatusApproved), string(StatusRejected), string(StatusExpired),
				string(StatusSucceeded), string(StatusFailed), string(StatusInterrupted),
				string(StatusRolledBack), string(StatusSkipped), string(StatusCancelled),
				"RUNNING", " created ", "Waiting_Approval",
			},
		},
		{
			name:  "环节类别",
			all:   AllStepTypes,
			valid: func(s string) bool { return StepType(s).Valid() },
			candidate: []string{
				string(TypeReadonly), string(TypePlan), string(TypeApproval),
				string(TypeExecute), string(TypeVerify),
				"READONLY", " plan ", "Execute",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := c.all()
			// 枚举 ⊆ 谓词为真。
			for _, v := range all {
				if !c.valid(v) {
					t.Fatalf("枚举出的值 %q 未通过对应谓词", v)
				}
			}
			// 谓词为真 ⊆ 枚举（归一到规范形态后比对）。
			for _, v := range c.candidate {
				if !c.valid(v) {
					continue
				}
				canonical := strings.ToLower(strings.TrimSpace(v))
				if !slices.Contains(all, canonical) {
					t.Fatalf("谓词为真的值 %q（规范形态 %q）没有出现在枚举出口里", v, canonical)
				}
			}
		})
	}
}

// TestStepStatusAndStepTypeNormalizeConsistently 同包两个枚举的「是否在
// 词表内」口径必须**一致**（issue #373）。
//
// 此前 StepType 归一后判定、StepStatus 精确查表，于是同一个词的写法变体
// 在两个枚举上得到相反结论："RUNNING" 是词表内的 running，而
// "RUNNING" 又被当成「读不懂」——误诊方向与本包立意相反（把「读不懂」
// 说成别的会让宿主照错方向查）。
func TestStepStatusAndStepTypeNormalizeConsistently(t *testing.T) {
	cases := []struct {
		name string
		typ  StepType
		st   StepStatus
	}{
		{"规范形态", TypePlan, StatusCreated},
		{"大写", "PLAN", "CREATED"},
		{"首尾空白", " plan ", " created "},
		{"混合大小写", "ExEcUtE", "FaIlEd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotStatus, gotType := c.st.Valid(), c.typ.Valid()
			if gotStatus != gotType {
				t.Fatalf("同一组写法变体下两个枚举结论不同：StepStatus(%q).Valid()=%v，StepType(%q).Valid()=%v",
					c.st, gotStatus, c.typ, gotType)
			}
			// 且结论必须是「词表内」：两个枚举在合法写法变体上都得为真，
			// 否则这条用例可能在「两边都假」时恒绿。
			if !gotStatus {
				t.Fatalf("写法变体 %q 应判为词表内", c.st)
			}
		})
	}
	// 词表外：两个枚举都必须为假（归一不映射未知取值）。
	for _, bad := range []string{"", "bogus", "审批"} {
		if StepStatus(bad).Valid() {
			t.Errorf("StepStatus(%q).Valid() 应为 false", bad)
		}
		if StepType(bad).Valid() {
			t.Errorf("StepType(%q).Valid() 应为 false", bad)
		}
	}
}

// TestStepStatusNormalizationDoesNotWidenVocabulary 归一只收拾**写法差异**，
// 不映射未知取值（协议层同款纪律）：归一后仍不在词表内的值仍为假。
func TestStepStatusNormalizationDoesNotWidenVocabulary(t *testing.T) {
	for _, bad := range []string{"runnin", "run", "createdd", "rolled back"} {
		if StepStatus(strings.ToUpper(bad)).Valid() {
			t.Errorf("StepStatus(%q).Valid() 应为 false（归一不映射未知取值）", bad)
		}
	}
}

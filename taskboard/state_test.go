package taskboard

// state_test.go —— 任务状态词表的判定面、枚举面与**注释一致性**
// （issue #356 / #360 / #375）。

import (
	"slices"
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// wantStates 任务状态词表的**冻结清单**（4 值）。
//
// 为什么要冻结：枚举与判定同源（都读 stateAll），双向断言在同源设计下
// 天然恒绿——真正的牙齿是这份冻结清单：词表新增一个取值而没同步这里，
// 用例立刻转红（issue #356 的反向验证落点）。
var wantStates = []State{StateClaimed, StateDone, StateFailed, StateOpen}

// TestAllStates 枚举出口：全集、排序、**新切片**。
func TestAllStates(t *testing.T) {
	got := AllStates()
	if !slices.Equal(got, wantStates) {
		t.Fatalf("任务状态词表漂移：AllStates=%v，期望 %v", got, wantStates)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("AllStates 必须返回排序后的切片: %v", got)
	}
	got[0] = "__mutated_by_host__"
	if slices.Contains(AllStates(), "__mutated_by_host__") {
		t.Fatal("AllStates 返回了共享底层数组：宿主改写污染了词表")
	}
}

// TestEnumerationIsProjectionOfPrivateTable 枚举出口必须是判定用的**同一张
// 私有表**的投影（issue #356）。
//
// 判据不是「枚举出的值合法」（那恒真），而是「枚举 = 私有表的投影」：
// 条数相等 + 表里每个成员都在枚举里。若枚举是另抄一份清单，此处转红。
func TestEnumerationIsProjectionOfPrivateTable(t *testing.T) {
	got := AllStates()
	if len(got) != len(stateAll) {
		t.Fatalf("枚举出口漏值：AllStates %d 条，私有词表 %d 条（%v）", len(got), len(stateAll), got)
	}
	for s := range stateAll {
		if !slices.Contains(got, s) {
			t.Fatalf("私有词表里的值 %q 没有出现在枚举出口里", s)
		}
	}
	// 双向：枚举里的每个值都过谓词；谓词为真的每个值都在枚举里。
	for _, s := range got {
		if !s.Valid() {
			t.Fatalf("枚举出的值 %q 未通过 Valid()", s)
		}
	}
	for _, s := range wantStates {
		if s.Valid() && !slices.Contains(got, s) {
			t.Fatalf("谓词为真的值 %q 没有出现在枚举出口里", s)
		}
	}
}

// TestClaimable 写路径的**单一判据**（issue #360）：
//
//   - 词表外 ⇒ ErrUnknownState（读不懂 ⇒ 拒绝认领）；
//   - 词表内且终态 ⇒ ErrTerminal；
//   - 词表内且非终态 ⇒ nil。
func TestClaimable(t *testing.T) {
	if err := State("weird_state").Claimable(); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("词表外状态必须报 ErrUnknownState，实得 %v", err)
	}
	// 词表外**不得**被报成终态：那是「读不懂」，报成终态会掩盖事实。
	if err := State("weird_state").Claimable(); errors.Is(err, ErrTerminal) {
		t.Fatal("词表外状态不得被报成 ErrTerminal")
	}
	if err := StateDone.Claimable(); !errors.Is(err, ErrTerminal) {
		t.Fatalf("终态必须报 ErrTerminal，实得 %v", err)
	}
	for _, s := range []State{StateOpen, StateClaimed, StateFailed} {
		if err := s.Claimable(); err != nil {
			t.Fatalf("%q 是非终态且在词表内，Claimable 应为 nil，实得 %v", s, err)
		}
	}
}

// TestStateConstantDocsMatchTerminal 常量注释与 Terminal() 必须一致
// （issue #375）。
//
// 规则（表驱动，逐个状态常量）：
//
//   - 注释含「非终态」⇒ Terminal() 必须为**假**；
//   - 注释含「终态」（且不含「非终态」）⇒ Terminal() 必须为**真**；
//   - 注释不含「终态」⇒ 该状态不受此判据约束。
//
// 两个方向都判，否则判据会半恒绿：只判「注释说终态 ⇒ 真」的话，把
// failed 的注释改回「终态」而断言仍绿（正是本条的反向验证）。
func TestStateConstantDocsMatchTerminal(t *testing.T) {
	docs := loadPkgDocs(t)
	for _, s := range AllStates() {
		name, ok := docs.constNameByValue(string(s))
		if !ok {
			t.Fatalf("词表里的值 %q 找不到对应的常量声明（判据按符号取注释）", s)
		}
		doc, ok := docs.consts[name]
		if !ok {
			t.Fatalf("常量 %s 没有文档注释", name)
		}
		switch {
		case strings.Contains(doc, "非终态"):
			if s.Terminal() {
				t.Fatalf("常量 %s 的注释写「非终态」，但 Terminal() 为真——注释与实现必须一致", name)
			}
		case strings.Contains(doc, "终态"):
			if !s.Terminal() {
				t.Fatalf("常量 %s 的注释宣称「终态」，但 Terminal() 为假——注释与实现必须一致", name)
			}
		}
	}
}

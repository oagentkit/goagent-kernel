// wordtable_multi_test.go —— 手抄判据「多表归属」指路的用例。
//
// # 为什么这条要单独判
//
// 同一个拼写可能是**多张**判定词表的取值（contract 的 "failed" 是会话状态、
// taskboard 的 "failed" 是任务状态、delegate 的 "failed" 是回执状态）。
// 只报第一张表会**错误指路**——把人引到另一张表的常量上，
// 那比不报更坏（照着改就把概念改错了）。
//
// ⇒ 同一处只报一次，但必须把**全部归属**列出来。
package archcheck

import (
	"strings"
	"testing"
)

// wordTableMultiOwnerA 主人包 A：会话状态里的 failed。
const wordTableMultiOwnerA = `package ownerA

const StatusFailed = "failed"

var statusAll = map[string]bool{StatusFailed: true}

func AllStatuses() []string {
	out := make([]string, 0, len(statusAll))
	for s := range statusAll {
		out = append(out, s)
	}
	return out
}
`

// wordTableMultiOwnerB 主人包 B：任务状态里的 failed（同形不同义）。
const wordTableMultiOwnerB = `package ownerB

const StateFailed = "failed"

var stateAll = map[string]bool{StateFailed: true}

func AllStates() []string {
	out := make([]string, 0, len(stateAll))
	for s := range stateAll {
		out = append(out, s)
	}
	return out
}
`

// TestWordTableHandCopyNamesAllOwningTables 一个取值属多张表时必须全部点名。
//
// 反向验证：若文案只报第一张表（错误指路），本用例会红——
// 它守的是「指路正确」，而单表那侧由别的用例守。
func TestWordTableHandCopyNamesAllOwningTables(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, map[string]string{
		"ownerA/ownerA.go": wordTableMultiOwnerA,
		"ownerB/ownerB.go": wordTableMultiOwnerB,
		"consumer/consumer.go": `package consumer

func f() string { return "failed" }
`,
	})
	pol := &Policy{WordTables: &WordTables{Tables: []WordTable{
		{Pkg: "ownerA", Backing: "statusAll", Enum: "AllStatuses"},
		{Pkg: "ownerB", Backing: "stateAll", Enum: "AllStates"},
	}}}
	vs, err := wordTableViolations(root, pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("同一处字面量只应报 1 条（不得按表重复报），实得 %d 条：%v", len(vs), vs)
	}
	if vs[0].Rule != wordTableHandCopyRuleID {
		t.Fatalf("规则 id 实得 %q，期望 %q", vs[0].Rule, wordTableHandCopyRuleID)
	}
	for _, want := range []string{"ownerA", "ownerB"} {
		if !strings.Contains(vs[0].Text, want) {
			t.Fatalf("违规文案必须点名全部归属表（缺 %s）：%q", want, vs[0].Text)
		}
	}
	if !strings.Contains(vs[0].Text, "同时属") {
		t.Fatalf("违规文案必须说明该取值同时属多张表：%q", vs[0].Text)
	}
}

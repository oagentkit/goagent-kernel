package trajectory

// copy_contract_test.go —— 「返回拷贝」契约的守护用例（issue #388 / #396）。
//
// 契约：Rows() / Issues() 返回的是**拷贝**——调用方改拿到的切片不得改穿投影器
// 内部状态（Rows 是轨迹行，Issues 是配对缺口清单：缺口内容是审计事实，被改穿
// 就不可信）。
//
// 旧用例的失效形态（issue #388）：它断言的 Rows() 与被测对象无关——改的是当场
// new 出来的字面量切片，断言按构造就不可能为真；删掉生产代码里的拷贝，整包
// 仍全绿。本文件的用例**真的持有投影器**：构造投影器 → 投事件 → 从投影器取
// 切片 → 改元素值 → 再从投影器重读 → 比**内容**（不是 len）。

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestRowsReturnsCopy Rows() 必须返回拷贝：改拿到的行，改不穿投影器内部状态。
func TestRowsReturnsCopy(t *testing.T) {
	p := NewProjector(Options{})
	p.ApplyAll([]eventbus.Event{
		seEv(eventbus.EventSideEffectGranted, 100, "act-a", "res-a", "", 1, nil),
	})
	rows := p.Rows()
	if len(rows) == 0 {
		t.Fatal("投影器应至少有一行")
	}
	rows[0].SideEffectKey = "mutated"
	again := p.Rows()
	if again[0].SideEffectKey == "mutated" {
		t.Fatal("Rows() 必须返回拷贝：调用方改动不得影响投影器内部状态")
	}
	if again[0].SideEffectKey != "act-a" {
		t.Fatalf("投影器内部状态被调用方改穿：%q", again[0].SideEffectKey)
	}
}

// TestIssuesReturnsCopy Issues() 必须返回拷贝：改拿到的缺口，改不穿清单。
//
// 缺口要真的存在才有得可改——投影器收了口（Finalize）才把「等了也没等到」
// 落成清单条目。
func TestIssuesReturnsCopy(t *testing.T) {
	p := NewProjector(Options{})
	p.Apply(toolEv(eventbus.EventToolCall, 100, 1, "c1", "alpha_fn"))
	p.Finalize()

	issues := p.Issues()
	if len(issues) == 0 {
		t.Fatal("收口后应有配对缺口清单")
	}
	issues[0].Pairing = PairingDuplicateCall
	issues[0].CallID = "mutated"

	again := p.Issues()
	if again[0].Pairing == PairingDuplicateCall || again[0].CallID == "mutated" {
		t.Fatalf("Issues() 必须返回拷贝：调用方改动不得影响投影器内部状态：%+v", again[0])
	}
	if again[0].Pairing != PairingMissingResult || again[0].CallID != "c1" {
		t.Fatalf("缺口清单内容被改穿：%+v", again[0])
	}
}

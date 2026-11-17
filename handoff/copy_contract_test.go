package handoff

import "testing"

// TestCloneRecordDoesNotAlias 钉住「Payload / 回执产出物不共享底层数组」的契约
// （issue #396）。
//
// cloneRecord/cloneReceipt 的注释自陈了承诺，但没有用例守护：去掉两处 append
// 拷贝后整包仍绿。交接载荷与回执产出物被改穿 ⇒ 交接留痕与实际内容不符。
//
// 两个方向都判：改副本不影响源，改源也不影响副本。
func TestCloneRecordDoesNotAlias(t *testing.T) {
	src := Record{
		Ticket: Ticket{Payload: []byte("abc")},
		Receipt: &AddressedReceipt{
			Receipt: Receipt{Artifacts: []string{"one.txt"}},
		},
	}
	got := cloneRecord(src)
	got.Ticket.Payload[0] = 'X'
	got.Receipt.Receipt.Artifacts[0] = "mutated-out"
	if src.Ticket.Payload[0] != 'a' {
		t.Fatalf("cloneRecord 的结果与入参共享 Payload 底层数组: %q", src.Ticket.Payload)
	}
	if src.Receipt.Receipt.Artifacts[0] != "one.txt" {
		t.Fatalf("cloneRecord 的结果与入参共享 Artifacts 底层数组: %v", src.Receipt.Receipt.Artifacts)
	}

	src2 := Record{
		Ticket: Ticket{Payload: []byte("abc")},
		Receipt: &AddressedReceipt{
			Receipt: Receipt{Artifacts: []string{"one.txt"}},
		},
	}
	got2 := cloneRecord(src2)
	src2.Ticket.Payload[0] = 'Y'
	src2.Receipt.Receipt.Artifacts[0] = "mutated-src"
	if got2.Ticket.Payload[0] != 'a' {
		t.Fatalf("改写入参写穿了 cloneRecord 的结果 Payload: %q", got2.Ticket.Payload)
	}
	if got2.Receipt.Receipt.Artifacts[0] != "one.txt" {
		t.Fatalf("改写入参写穿了 cloneRecord 的结果 Artifacts: %v", got2.Receipt.Receipt.Artifacts)
	}
}

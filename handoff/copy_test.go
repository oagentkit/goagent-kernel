// copy_test.go —— 「返回拷贝」契约的守护用例（issue #396）。
//
// 契约写着「Payload / 回执产出物不共享底层数组」，但没有任何用例钉住它：
// 把拷贝去掉，整包测试仍然全绿——恒绿的契约等于把承诺写成了散文。
//
// 用例形状：改副本 → 重读源 → 比**内容**。
package handoff

import (
	"context"
	"testing"
)

// TestTicketPayloadIsCopied 读出的载荷不得与账里的记录共享底层数组。
//
// 反向验证：去掉 cloneRecord 里的 Payload 拷贝 ⇒ 本条转红。
func TestTicketPayloadIsCopied(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryLedger()
	d := NewDesk(ledger)
	tk := baseTicket()
	if _, err := d.Offer(ctx, tk); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	rec, err := ledger.Load(ctx, tk.Key)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rec.Ticket.Payload) == 0 {
		t.Fatal("用例前提不成立：载荷为空")
	}
	rec.Ticket.Payload[0] = 'X'
	again, err := ledger.Load(ctx, tk.Key)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if again.Ticket.Payload[0] == 'X' {
		t.Fatalf("读出的载荷与账里的记录共享了底层数组：%q", again.Ticket.Payload)
	}
}

// TestReceiptArtifactsAreCopied 回执产出物不得与账里的记录共享底层数组。
//
// 反向验证：去掉 cloneReceipt 里的 Artifacts 拷贝 ⇒ 本条转红。
func TestReceiptArtifactsAreCopied(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryLedger()
	d := NewDesk(ledger)
	tk := baseTicket()
	if _, err := d.Offer(ctx, tk); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	h, err := d.Take(ctx, tk.Key, "proc-b")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	receipt := Receipt{
		Status:    ReceiptDone,
		Summary:   "合成结论",
		Artifacts: []string{"artifact-1", "artifact-2"},
	}
	if err := d.Reply(ctx, tk.Key, h.Token, receipt); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	got, err := d.Deliveries(ctx, tk.ReplyTo)
	if err != nil {
		t.Fatalf("Deliveries: %v", err)
	}
	if len(got) != 1 || len(got[0].Receipt.Artifacts) != 2 {
		t.Fatalf("回执取回内容不对：%+v", got)
	}
	got[0].Receipt.Artifacts[0] = "mutated"
	again, err := d.Deliveries(ctx, tk.ReplyTo)
	if err != nil {
		t.Fatalf("Deliveries again: %v", err)
	}
	if again[0].Receipt.Artifacts[0] == "mutated" {
		t.Fatalf("回执产出物与账里的记录共享了底层数组：%v", again[0].Receipt.Artifacts)
	}
}

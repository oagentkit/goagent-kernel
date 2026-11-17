// finalize_outbox_test.go —— issue #272 的 Outbox 补做与交付形态判据
// （与 finalize_test.go 的复现/夹具分开：结构阈值 400 行按判据的一侧切）。
package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

func TestFinalizeOutboxNoLossNoDuplicate(t *testing.T) {
	store := newFinalizeStore()
	pub := &dropPub{}
	fail := true
	var dispatched []string
	dispatch := func(_ context.Context, ev FinalizeEvent) error {
		if fail {
			return errDeliveryBoom
		}
		dispatched = append(dispatched, ev.ID)
		return nil
	}
	cfg := Config{InstanceID: "replica-a", FinalizeDispatch: dispatch}
	r := NewWithClock(store, pub.fn, cfg, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if err == nil {
		t.Fatal("投递失败必须带出留痕（不得静默）")
	}
	if !errors.Is(err, errDeliveryBoom) {
		t.Fatalf("留痕应包含投递失败原因（实得 %v）", err)
	}
	if n != 1 || !store.isTerminal("s1") {
		t.Fatalf("终态写入必须成立（n=%d terminal=%v）", n, store.isTerminal("s1"))
	}
	if store.pendingCount() != 1 {
		t.Fatalf("投递失败后 Outbox 必须保留 1 行（实得 %d）", store.pendingCount())
	}
	if len(dispatched) != 0 {
		t.Fatalf("失败的那次不应计入已投递（实得 %v）", dispatched)
	}

	// 进程重启：新的 Reconciler 从同一存储补做。
	fail = false
	r2 := NewWithClock(store, pub.fn, cfg, func() time.Time { return rbase })
	delivered, ferr := r2.FlushFinalizeOutbox(context.Background())
	if ferr != nil {
		t.Fatalf("补做不应失败: %v", ferr)
	}
	if delivered != 1 || len(dispatched) != 1 {
		t.Fatalf("Outbox 必须恰好补投一次（delivered=%d dispatched=%v）", delivered, dispatched)
	}
	if dispatched[0] != "finalize:s1:failed" {
		t.Fatalf("终态事件 ID 必须稳定（实得 %q）", dispatched[0])
	}
	if store.pendingCount() != 0 {
		t.Fatalf("补做成功后不得残留待投递行（实得 %d）", store.pendingCount())
	}

	// 重放：已投递的行不得重复发送。
	delivered, ferr = r2.FlushFinalizeOutbox(context.Background())
	if ferr != nil || delivered != 0 {
		t.Fatalf("重放不得重复投递（delivered=%d err=%v）", delivered, ferr)
	}
	if len(dispatched) != 1 {
		t.Fatalf("重放后投递次数必须仍是 1（实得 %d）", len(dispatched))
	}
	if g := r2.FinalizeGuarantee(); !g.Atomic || !g.ConfirmedDelivery {
		t.Fatalf("FinalizeStore + 可确认投递必须报告原子/已确认: %+v", g)
	}
}

// TestFinalizeAtomicFailureNoSplit 反向判据：Finalize 事务失败 ⇒ 状态不变、
// Outbox 无行（不允许「事件先落库、状态没写成」的反向分裂）；重试后同事务
// 一起成立。
func TestFinalizeAtomicFailureNoSplit(t *testing.T) {
	store := newFinalizeStore()
	store.finalizeErr = errDeliveryBoom
	pub := &dropPub{}
	r := NewWithClock(store, pub.fn, Config{InstanceID: "replica-a"}, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if n != 0 || err == nil {
		t.Fatalf("Finalize 失败必须整轮失败（n=%d err=%v）", n, err)
	}
	if store.isTerminal("s1") {
		t.Fatal("Finalize 失败不得留下终态（状态与事件必须同生共死）")
	}
	if store.pendingCount() != 0 {
		t.Fatal("Finalize 失败不得留下 Outbox 行")
	}

	store.mu.Lock()
	store.finalizeErr = nil
	store.mu.Unlock()
	// 无 dispatcher：回退旧 Pub（Outbox 行仍先落库，再标记已投递）。
	n, err = r.RecoverInterrupted(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("重试后应成功（n=%d err=%v）", n, err)
	}
	if !store.isTerminal("s1") || store.pendingCount() != 0 {
		t.Fatalf("同事务成功后状态/Outbox 必须一致（terminal=%v pending=%d）",
			store.isTerminal("s1"), store.pendingCount())
	}
	attempts, events := pub.snapshot()
	if attempts != 1 || len(events) != 1 {
		t.Fatalf("无 dispatcher 时回退 Pub 必须投递一次（attempts=%d events=%v）", attempts, events)
	}
	if g := r.FinalizeGuarantee(); !g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("无 dispatcher 的 FinalizeStore 必须报原子但投递未确认: %+v", g)
	}
}

// TestFinalizeOutboxMarkFailureReplays 标记已投递失败：行保留，下一轮补做。
// 投递是至少一次（Event.ID 稳定，订阅方按 ID 去重），但绝不丢。
func TestFinalizeOutboxMarkFailureReplays(t *testing.T) {
	store := newFinalizeStore()
	store.markErr = errDeliveryBoom
	pub := &dropPub{}
	var ids []string
	dispatch := func(_ context.Context, ev FinalizeEvent) error {
		ids = append(ids, ev.ID)
		return nil
	}
	r := NewWithClock(store, pub.fn, Config{InstanceID: "replica-a", FinalizeDispatch: dispatch}, func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if n != 1 || err == nil {
		t.Fatalf("标记失败必须带出留痕（n=%d err=%v）", n, err)
	}
	if store.pendingCount() != 1 {
		t.Fatalf("标记失败时 Outbox 行不得被吞（实得 %d）", store.pendingCount())
	}
	if len(ids) != 1 {
		t.Fatalf("投递本身已发生一次（实得 %v）", ids)
	}

	store.mu.Lock()
	store.markErr = nil
	store.mu.Unlock()
	delivered, ferr := r.FlushFinalizeOutbox(context.Background())
	if ferr != nil || delivered != 1 {
		t.Fatalf("补做必须成功（delivered=%d err=%v）", delivered, ferr)
	}
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("重放使用同一稳定 Event.ID（实得 %v）——订阅方据此去重", ids)
	}
	if store.pendingCount() != 0 {
		t.Fatal("补做成功后不得残留 Outbox 行")
	}
}

// TestFinalizeGuaranteeReportsModes 可查事实的三种组合。
func TestFinalizeGuaranteeReportsModes(t *testing.T) {
	legacy := NewWithClock(newLegacySplitStore(), (&dropPub{}).fn,
		Config{InstanceID: "replica-a"}, func() time.Time { return rbase })
	if g := legacy.FinalizeGuarantee(); g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("legacy: %+v", g)
	}

	atomic := NewWithClock(newFinalizeStore(), (&dropPub{}).fn,
		Config{InstanceID: "replica-a", FinalizeDispatch: func(context.Context, FinalizeEvent) error { return nil }},
		func() time.Time { return rbase })
	if g := atomic.FinalizeGuarantee(); !g.Atomic || !g.ConfirmedDelivery {
		t.Fatalf("atomic+dispatch: %+v", g)
	}

	unconfirmed := NewWithClock(newFinalizeStore(), (&dropPub{}).fn,
		Config{InstanceID: "replica-a"}, func() time.Time { return rbase })
	if g := unconfirmed.FinalizeGuarantee(); !g.Atomic || g.ConfirmedDelivery {
		t.Fatalf("atomic+legacy-pub: %+v", g)
	}
}

// TestRecoveryLoopAutoFlushesOutbox 进程重启后的恢复循环无需宿主额外接线：
// 下一轮 RecoverInterrupted 开始时会先补做上一轮遗留的终态事件（同一稳定 ID），
// 补做成功后不再重复投递。
func TestRecoveryLoopAutoFlushesOutbox(t *testing.T) {
	store := newFinalizeStore()
	pub := &dropPub{}
	fail := true
	var ids []string
	dispatch := func(_ context.Context, ev FinalizeEvent) error {
		if fail {
			return errDeliveryBoom
		}
		ids = append(ids, ev.ID)
		return nil
	}
	r := NewWithClock(store, pub.fn,
		Config{InstanceID: "replica-a", FinalizeDispatch: dispatch},
		func() time.Time { return rbase })

	n, err := r.RecoverInterrupted(context.Background())
	if n != 1 || err == nil {
		t.Fatalf("首轮应收敛但投递失败（n=%d err=%v）", n, err)
	}
	if store.pendingCount() != 1 {
		t.Fatalf("首轮失败后 Outbox 必须保留 1 行（实得 %d）", store.pendingCount())
	}

	// 恢复循环下一轮：开始即自动补做，无需会话再次出现在 running。
	fail = false
	n, err = r.RecoverInterrupted(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("补做轮不应再收敛会话（n=%d err=%v）", n, err)
	}
	if len(ids) != 1 || ids[0] != "finalize:s1:failed" {
		t.Fatalf("恢复轮必须用稳定 Event.ID 补投一次（实得 %v）", ids)
	}
	if store.pendingCount() != 0 {
		t.Fatalf("补做成功后不得残留行（实得 %d）", store.pendingCount())
	}

	// 再跑一轮：已投递的行不重复。
	if _, err := r.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("已投递事件不得重复投递（实得 %v）", ids)
	}
}

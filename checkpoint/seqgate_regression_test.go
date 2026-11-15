package checkpoint

import "testing"

// TestReleaseRestoresPreviousHighWaterMark 钉住 Release 的回滚语义。
//
// 背景：Release 早先走 delete(g.last, runID)，只为了让同序号可重试。但 last 只存
// 最大值，删记录等于把**上一个已成功落库的序号**一起抹掉——闸的地板从 5 跌回
// 「无记录」，seq=3 的陈旧慢写便畅通无阻，恢复点静默回退，正是 SeqGate 存在的
// 理由被自己打开。本测试走完整条路径，不直接调内部字段。
func TestReleaseRestoresPreviousHighWaterMark(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	// 首个快照成功落库：地板 = 5（成功落库的事实由 MarkPersisted 记下，
	// 这是 Accept → Save 契约的一半；Release 的回滚目标就是它）。
	if err := g.Accept(Snapshot{RunID: run, Seq: 5}); err != nil {
		t.Fatalf("首个快照应通过: %v", err)
	}
	g.MarkPersisted(run, 5)
	// 第二个快照接受后落库失败 → 必须回滚。
	if err := g.Accept(Snapshot{RunID: run, Seq: 7}); err != nil {
		t.Fatalf("更大的序号应通过: %v", err)
	}
	if !g.Release(run, 7) {
		t.Fatalf("Release 应认领 seq=7（last 恰为 7）")
	}

	if got := g.Last(run); got != 5 {
		t.Fatalf("回滚后地板应是上一个已落库的 5，实得 %d——回滚成删记录会让地板跌到 0", got)
	}

	// 关键断言：陈旧的 seq=3 必须仍被拒。
	if err := g.Accept(Snapshot{RunID: run, Seq: 3}); err == nil {
		t.Fatalf("seq=3 不该通过：seq=5 已成功落库，回滚不该把它变回可写（恢复点静默回退）")
	}

	// 同序号重试仍应放行——Release 的本职不能被前值保留破坏。
	if err := g.Accept(Snapshot{RunID: run, Seq: 7}); err != nil {
		t.Fatalf("seq=7 重试应放行: %v", err)
	}
}

// TestReleaseWithoutHistoryClearsGate 覆盖「本就没有前值」时退化成删除的分支。
func TestReleaseWithoutHistoryClearsGate(t *testing.T) {
	g := NewSeqGate()
	const run = "fresh"

	if err := g.Accept(Snapshot{RunID: run, Seq: 1}); err != nil {
		t.Fatalf("首个快照应通过: %v", err)
	}
	if !g.Release(run, 1) {
		t.Fatalf("Release 应认领 seq=1")
	}
	if got := g.Last(run); got != 0 {
		t.Fatalf("无前值时回滚应退回「无记录」（0），实得 %d", got)
	}
	// 无前值场景下同序号本就可重试——确认闸确实松开。
	if err := g.Accept(Snapshot{RunID: run, Seq: 1}); err != nil {
		t.Fatalf("无前值时同序号应可重试: %v", err)
	}
}

// TestReleaseIgnoresSupersededSeq 确认并发场景下不误退：期间已有更新的写入落地时，
// Release 必须不动任何状态（否则会把新写的闸退回去，给陈旧写开口子）。
func TestReleaseIgnoresSupersededSeq(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	if err := g.Accept(Snapshot{RunID: run, Seq: 3}); err != nil {
		t.Fatalf("seq=3 应通过: %v", err)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 9}); err != nil {
		t.Fatalf("seq=9 应通过: %v", err)
	}
	// seq=3 的慢写此刻落库失败，但它早已被 9 取代。
	if g.Release(run, 3) {
		t.Fatalf("last=9 ≠ 3，Release 不该认领")
	}
	if got := g.Last(run); got != 9 {
		t.Fatalf("Release 未认领时状态不应变，实得 %d", got)
	}
}

// TestSeedInvalidatesStaleRollbackTarget 确认 Seed 抬高基线后作废旧的前值：
// 宿主重启场景下内存里的历史本就不可信，退回它等于退回一个陈旧地板。
func TestSeedInvalidatesStaleRollbackTarget(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	if err := g.Accept(Snapshot{RunID: run, Seq: 2}); err != nil { // prev=0, last=2
		t.Fatalf("seq=2 应通过: %v", err)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 4}); err != nil { // prev=2, last=4
		t.Fatalf("seq=4 应通过: %v", err)
	}
	g.Seed(run, 10) // 基线抬到 10，旧前值 2 作废

	// last 已是 10，seq=4 不再是当前最大值，Release 不该认领。
	if g.Release(run, 4) {
		t.Fatalf("Seed 抬高基线后 last=10，Release(4) 不该认领")
	}
	if got := g.Last(run); got != 10 {
		t.Fatalf("Seed 抬高基线后地板应为 10，实得 %d", got)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 3}); err == nil {
		t.Fatalf("Seed 基线 10 之下，seq=3 必须被拒")
	}
}

// TestReleaseTwoInflightFailuresReturnToFloor 复现 issue #216：**两个在途提交
// 同时落库失败**，回滚不得把 last 钉在另一个在途序号上。
//
// 旧实现 prev 只有一格（假设「Release 只可能回滚最近一次 Accept」）：Accept(6)、
// Accept(7) 之后 prev=6、last=7，已落库基线 5 被覆盖。按先 7 后 6 回滚——
// Release(7) 退到 6、Release(6) 又停在 6——而 6 并没有落库成功，重试 6 被判
// ErrStaleSeq，该序号永远写不进去（跳过它又留下补不回的洞）。
func TestReleaseTwoInflightFailuresReturnToFloor(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	// seq=5 已成功落库：基线（floor）= 5。
	if err := g.Accept(Snapshot{RunID: run, Seq: 5}); err != nil {
		t.Fatalf("首个快照应通过: %v", err)
	}
	g.MarkPersisted(run, 5)

	// 两个环节并发提交 6、7，都通过 Accept（旧实现此刻基线 5 已被覆盖）。
	if err := g.Accept(Snapshot{RunID: run, Seq: 6}); err != nil {
		t.Fatal(err)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 7}); err != nil {
		t.Fatal(err)
	}

	// 两次 Save 都失败，按先 7 后 6 回滚。第一笔回滚一步退到已落库基线 5
	// （不是旧实现的钉在 6）；第二笔因 last 已是 5 而**不认领**——「期间已有
	// 更新的写入落地」的既有契约，且它已无事可做。
	if !g.Release(run, 7) {
		t.Fatal("Release 应认领 seq=7（last 恰为 7）")
	}
	if got := g.Last(run); got != 5 {
		t.Fatalf("首笔回滚应一步退回已落库基线 5，实得 %d——钉在在途值上，重试即永久 ErrStaleSeq", got)
	}
	if g.Release(run, 6) {
		t.Fatal("last 已退回 5，Release(6) 不该认领（期间基线已覆盖该在途值）")
	}
	if got := g.Last(run); got != 5 {
		t.Fatalf("不认领的回滚不得改动状态，实得 %d", got)
	}

	// 关键断言：重试 6 必须放行——这正是本单要救的「永久 ErrStaleSeq」。
	if err := g.Accept(Snapshot{RunID: run, Seq: 6}); err != nil {
		t.Fatalf("回滚到基线后 seq=6 重试必须通过: %v", err)
	}
}

// TestReleaseTwoInflightFailuresReverseOrder 同一场景的**反序**回滚
// （先 6 后 7）：Release(6) 时 last=7，按「已有更新的写入落地」契约返回 false
// 且不动状态；随后 Release(7) 一次退回基线。重试 6 仍必须放行——回滚的落点
// 不依赖调用方按什么顺序收场。
func TestReleaseTwoInflightFailuresReverseOrder(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	if err := g.Accept(Snapshot{RunID: run, Seq: 5}); err != nil {
		t.Fatal(err)
	}
	g.MarkPersisted(run, 5)
	if err := g.Accept(Snapshot{RunID: run, Seq: 6}); err != nil {
		t.Fatal(err)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 7}); err != nil {
		t.Fatal(err)
	}

	if g.Release(run, 6) {
		t.Fatal("last=7 ≠ 6，Release(6) 不该认领（期间已有更新的写入落地）")
	}
	if got := g.Last(run); got != 7 {
		t.Fatalf("被拒的回滚不得改动状态，实得 %d", got)
	}
	if !g.Release(run, 7) {
		t.Fatal("Release 应认领 seq=7")
	}
	if got := g.Last(run); got != 5 {
		t.Fatalf("回滚应退回已落库基线 5，实得 %d", got)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 6}); err != nil {
		t.Fatalf("基线之上 seq=6 重试必须通过: %v", err)
	}
}

// TestMarkPersistedRaisesRollbackBaseline 钉住成功侧契约：Save 成功后
// MarkPersisted 抬基线，此后回滚退到的是**它**，不是更旧的值。
func TestMarkPersistedRaisesRollbackBaseline(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	// 第一轮：5 落库成功。
	if err := g.Accept(Snapshot{RunID: run, Seq: 5}); err != nil {
		t.Fatal(err)
	}
	g.MarkPersisted(run, 5)
	// 第二轮：7 落库成功。
	if err := g.Accept(Snapshot{RunID: run, Seq: 7}); err != nil {
		t.Fatal(err)
	}
	g.MarkPersisted(run, 7)
	// 第三轮：9 落库失败 → 回滚到基线 7（不是 5，更不是「无记录」）。
	if err := g.Accept(Snapshot{RunID: run, Seq: 9}); err != nil {
		t.Fatal(err)
	}
	if !g.Release(run, 9) {
		t.Fatal("Release 应认领 seq=9")
	}
	if got := g.Last(run); got != 7 {
		t.Fatalf("回滚应退到最后一次确认落库的 7，实得 %d", got)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 3}); err == nil {
		t.Fatal("基线 7 之下 seq=3 必须被拒（陈旧慢写不得借回滚放行）")
	}

	// MarkPersisted 幂等且不回退：拿更旧的 seq 确认，基线不动。
	g.MarkPersisted(run, 5)
	if got := g.Last(run); got != 7 {
		t.Fatalf("更旧的确认不得降低基线，实得 %d", got)
	}

	// 退化输入不得写基线。
	g.MarkPersisted("", 99)
	g.MarkPersisted(run, 0)
	if got := g.Last(run); got != 7 {
		t.Fatalf("退化输入不得改动基线，实得 %d", got)
	}
}

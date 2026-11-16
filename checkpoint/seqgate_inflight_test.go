package checkpoint

// #216：SeqGate 的 prev 只有一格，无法容纳多个在途序号。
//
// 正规用法是 Accept → Store.Save → 失败 Release，而 Store 要求并发安全
// （多个环节可同时落库）。两次在途提交同时失败时，单格 prev 会在第一次
// 回滚后丢掉「再前一个地板」：要么把 last 固定在被回滚的序号上（重试被
// ErrStaleSeq 永久拒），要么把地板跌到 0（陈旧写重新获得通行权）。
//
// 本文件钉住：多格在途按**任意回滚顺序**都退回到已落库地板，且两次重试都成功。

import (
	"testing"

	"github.com/oekit/pkg/errors"
)

// TestTwoInflightBothFailThenBothRetriesSucceed issue #216 的原始触发：
// 5 已落库，6/7 并发在途且都落库失败，回滚后地板必须回到 5，6 与 7 都能重试。
func TestTwoInflightBothFailThenBothRetriesSucceed(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	accept(t, g, run, 5) // 已成功落库的地板
	accept(t, g, run, 6) // 在途 1
	accept(t, g, run, 7) // 在途 2

	// 先回滚后接受的 7，再回滚 6（issue 描述的顺序）。
	if !g.Release(run, 7) {
		t.Fatal("Release(7) 应认领当前最大值")
	}
	if !g.Release(run, 6) {
		t.Fatal("Release(6) 应认领回滚后的当前最大值")
	}
	if got := g.Last(run); got != 5 {
		t.Fatalf("两次在途都失败后地板应回到 5，实得 %d——回到 0 会让陈旧写通行，停在 6 会让重试永久被拒", got)
	}

	// 两次重试都必须成功。
	accept(t, g, run, 6)
	accept(t, g, run, 7)
}

// TestTwoInflightReleasesInAcceptOrder 回滚顺序与接受顺序相反也要成立：
// 并发失败的回调完成顺序不由实现决定，判据不能只钉一种顺序。
func TestTwoInflightReleasesInAcceptOrder(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	accept(t, g, run, 5)
	accept(t, g, run, 6)
	accept(t, g, run, 7)

	// 先回滚 6（已被 7 取代，返回 false 表示当前最大值未变，但它的在途格
	// 必须被移除——否则 7 回滚时会以 6 为地板）。
	g.Release(run, 6)
	if !g.Release(run, 7) {
		t.Fatal("Release(7) 应认领当前最大值")
	}
	if got := g.Last(run); got != 5 {
		t.Fatalf("任意回滚顺序都应退回到已落库地板 5，实得 %d", got)
	}

	accept(t, g, run, 6)
	accept(t, g, run, 7)
}

// TestMultiInflightRollbackKeepsStaleWritesRejected 回滚到多格之后的地板仍
// 必须挡陈旧写：多格支持不能把「恢复点静默回退」重新打开。
func TestMultiInflightRollbackKeepsStaleWritesRejected(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"

	accept(t, g, run, 5)
	accept(t, g, run, 6)
	accept(t, g, run, 8)
	g.Release(run, 8)
	g.Release(run, 6)

	if got := g.Last(run); got != 5 {
		t.Fatalf("地板应为 5，实得 %d", got)
	}
	if err := g.Accept(Snapshot{RunID: run, Seq: 3}); !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("地板 5 之下的 seq=3 必须仍被拒，实得 %v", err)
	}
}

// TestReleaseUnknownInflightIsNoop 未在途的序号回滚不得改动任何状态
// （重复 Release 是正常形态：交还闸不是错误路径）。
func TestReleaseUnknownInflightIsNoop(t *testing.T) {
	g := NewSeqGate()
	const run = "r1"
	accept(t, g, run, 5)

	if g.Release(run, 4) {
		t.Fatal("从未 Accept 的序号不得被 Release 认领")
	}
	if g.Release(run, 5) == false {
		t.Fatal("在途序号应可回滚")
	}
	if g.Release(run, 5) {
		t.Fatal("重复 Release 应为 no-op（不得二次回滚）")
	}
	if got := g.Last(run); got != 0 {
		t.Fatalf("重复 Release 后应保持无记录，实得 %d", got)
	}
}

// accept 断言一次 Accept 成功（失败即 Fatal）。
func accept(t *testing.T, g *SeqGate, runID string, seq uint64) {
	t.Helper()
	if err := g.Accept(Snapshot{RunID: runID, Seq: seq}); err != nil {
		t.Fatalf("Accept(%d) 应通过: %v", seq, err)
	}
}

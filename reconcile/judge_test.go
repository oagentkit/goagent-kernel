package reconcile

// 三判据纯函数契约：启动锚点 / 心跳租约 / 事件静默（含边界与关闭态）。

import (
	"testing"
	"time"
)

var jbase = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func TestOrphanAtBootJudgesAnchorBoundary(t *testing.T) {
	boot := jbase
	cutoff := boot // BootGrace=0 ⇒ cutoff 即锚点

	// 锚点前 1ns：孤儿（严格 Before）。
	if !OrphanAtBoot(boot.Add(-time.Nanosecond), boot, 0) {
		t.Fatal("最后事件早于锚点应判孤儿")
	}
	// 恰在锚点上：不算孤儿（宽限方向是多留一格，不是少留）。
	if OrphanAtBoot(cutoff, boot, 0) {
		t.Fatal("最后事件恰等于 cutoff 不应判孤儿（Before 严格小于）")
	}
	// 锚点后：存活进程在推进，免检。
	if OrphanAtBoot(boot.Add(time.Minute), boot, 0) {
		t.Fatal("锚点后仍有事件应免检")
	}
	// 额外宽限把 cutoff 往更早推（更保守）。
	if OrphanAtBoot(boot.Add(-30*time.Minute), boot, time.Hour) {
		t.Fatal("BootGrace=1h 时锚点前 30m 不应判孤儿（宽限未过）")
	}
	if !OrphanAtBoot(boot.Add(-2*time.Hour), boot, time.Hour) {
		t.Fatal("BootGrace=1h 时锚点前 2h 应判孤儿")
	}
}

func TestOrphanAtBootDisabledStatesAreNotOrphans(t *testing.T) {
	// 零锚点 = 没有启动事实：不得把「无法判定」写成「是孤儿」（误收全量）。
	if OrphanAtBoot(jbase.Add(-time.Hour), time.Time{}, 0) {
		t.Fatal("零锚点不得判孤儿")
	}
	// 负宽限 = 显式关闭整条路。
	if OrphanAtBoot(jbase.Add(-time.Hour), jbase, -time.Second) {
		t.Fatal("BootGrace<0 应整体关闭（判据恒 false）")
	}
}

func TestJudgeLeaseThreeStates(t *testing.T) {
	now := jbase
	lease, grace := DefaultLeaseTTL, GraceWindow

	// 无心跳行 / 心跳面关闭 → 回落静默。
	if s := JudgeLease(nil, "replica-b", now, lease, grace); s != LeaseNoRow {
		t.Fatalf("beats=nil 应 LeaseNoRow, got %v", s)
	}
	if s := JudgeLease(map[string]time.Time{}, "replica-b", now, lease, grace); s != LeaseNoRow {
		t.Fatal("owner 无心跳行应 LeaseNoRow（回落事件静默）")
	}
	if s := JudgeLease(map[string]time.Time{"replica-b": now}, "replica-b", now, -time.Second, grace); s != LeaseNoRow {
		t.Fatal("lease<=0（心跳判定关闭）应 LeaseNoRow")
	}

	// 心跳新鲜 → 免检。
	if s := JudgeLease(map[string]time.Time{"replica-b": now.Add(-10 * time.Second)}, "replica-b", now, lease, grace); s != LeaseLive {
		t.Fatalf("心跳 10s 前应 LeaseLive, got %v", s)
	}
	// 租约刚过期、仍在重打宽限窗内 → 仍是免检（重启副本一拍心跳未回）。
	if s := JudgeLease(map[string]time.Time{"replica-b": now.Add(-(lease + grace - time.Second))}, "replica-b", now, lease, grace); s != LeaseLive {
		t.Fatalf("租约+宽限内应 LeaseLive, got %v", s)
	}
	// 恰好越过租约+宽限 → 过期。
	if s := JudgeLease(map[string]time.Time{"replica-b": now.Add(-(lease + grace))}, "replica-b", now, lease, grace); s != LeaseExpired {
		t.Fatalf("静默恰达 lease+grace 应 LeaseExpired, got %v", s)
	}
	if s := JudgeLease(map[string]time.Time{"replica-b": now.Add(-(lease + grace + time.Second))}, "replica-b", now, lease, grace); s != LeaseExpired {
		t.Fatalf("租约过期应 LeaseExpired, got %v", s)
	}
}

func TestStalledBoundaryIncludesEquality(t *testing.T) {
	last := jbase
	now := last.Add(DefaultStall)

	// 恰等于阈值：超（宿主原实现 `now.Sub(last) < threshold 才跳过` 的反面）。
	if !Stalled(last, now, DefaultStall) {
		t.Fatal("静默恰达阈值应判超阈")
	}
	if Stalled(last, now.Add(-time.Nanosecond), DefaultStall) {
		t.Fatal("差 1ns 未达阈值不应判超阈")
	}
	if Stalled(now, now, DefaultStall) {
		t.Fatal("最后事件即当下不应判超阈")
	}
}

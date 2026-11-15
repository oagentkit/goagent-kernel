package reconcile

// 启动锚点路契约（issue #165 RecoverBootOrphans）：锚点前孤儿立即收、
// 锚点后事件免检、他副本归常规路、关闭态不碰存储、无事件行回落创建时间。

import (
	"strings"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/contract"
)

// ── 启动锚点路 ──────────────────────────────────────────────────────────

func TestBootOrphanReclaimedBeforeAnchor(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", CaseID: "c1", Owner: "replica-a", CreatedAt: rbase.Add(-2 * time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-10 * time.Minute)}, // 锚点前
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase}, store)

	n := mustRecover(t, r, true)
	if n != 1 {
		t.Fatalf("锚点前事件的孤儿应收敛, n=%d", n)
	}
	// 扫描面提示收窄到本实例；动作序 CAS → 括号 → 事件。
	want := []string{
		"list:replica-a",
		"last:s1",
		"cas:s1:running->failed:boot-reconcile",
		"bracket:s1:boot-reconcile",
		"pub:s1:session_finished:by=boot-reconcile",
	}
	got := store.log()
	if len(got) != len(want) {
		t.Fatalf("动作序不符:\n got=%v\nwant=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("动作序第 %d 步: got=%s want=%s（全序=%v）", i, got[i], want[i], got)
		}
	}
	// CAS 入参与归因。
	if store.lastCast.from != contract.StatusRunning || store.lastCast.to != contract.StatusFailed {
		t.Fatalf("收敛应 running->failed, got %s->%s", store.lastCast.from, store.lastCast.to)
	}
	if store.lastCast.cause.By != ByBootReconcile || store.lastCast.cause.Reason == "" {
		t.Fatalf("归因应为 boot-reconcile 且带机制事实, got %+v", store.lastCast.cause)
	}
}

func TestBootOrphanSkipsEventsAfterAnchorAndForeignOwner(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{
			{ID: "alive", Owner: "replica-a", CreatedAt: rbase.Add(-2 * time.Hour)},
			{ID: "foreign", Owner: "replica-b", CreatedAt: rbase.Add(-2 * time.Hour)},
		},
		last: map[string]time.Time{
			"alive":   rbase.Add(time.Minute),    // 锚点后仍有事件：免检
			"foreign": rbase.Add(-2 * time.Hour), // 他副本：归常规路
		},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase}, store)

	n := mustRecover(t, r, true)
	if n != 0 {
		t.Fatalf("锚点后事件与他副本会话都不该收, n=%d", n)
	}
	for _, c := range store.log() {
		if strings.HasPrefix(c, "cas:") || strings.HasPrefix(c, "bracket:") || strings.HasPrefix(c, "pub:") {
			t.Fatalf("免检会话不得进入收敛体: %s", c)
		}
	}
}

func TestBootPathDisabledStatesNeverScan(t *testing.T) {
	// BootGrace < 0：conf 显式关闭。
	store := &fakeStore{}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase, BootGrace: -time.Second}, store)
	if n := mustRecover(t, r, true); n != 0 || len(store.log()) != 0 {
		t.Fatalf("负 BootGrace 应整体关闭（不碰存储）, n=%d calls=%v", n, store.log())
	}
	// 零锚点：防零值锚点误收全量。
	store2 := &fakeStore{}
	r2, _ := newRig(t, Config{InstanceID: "replica-a"}, store2)
	if n := mustRecover(t, r2, true); n != 0 || len(store2.log()) != 0 {
		t.Fatalf("零锚点应整体关闭（不碰存储）, n=%d calls=%v", n, store2.log())
	}
}

func TestBootNoEventRowFallsBackToCreatedAt(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		// last 缺项 = 零值：以 CreatedAt 锚定（锚点前 → 孤儿）。
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase}, store)

	n := mustRecover(t, r, true)
	if n != 1 {
		t.Fatalf("无事件行应以创建时间锚定并收敛, n=%d", n)
	}
}

func TestBootGraceWidensCutoffConservatively(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-a", CreatedAt: rbase.Add(-2 * time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-30 * time.Minute)},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase, BootGrace: time.Hour}, store)

	n := mustRecover(t, r, true)
	if n != 0 {
		t.Fatalf("BootGrace=1h 时锚点前 30m 在宽限窗内，不应收（额外宽限=更保守）, n=%d", n)
	}
}

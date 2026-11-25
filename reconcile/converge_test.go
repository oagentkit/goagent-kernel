package reconcile

// 收敛体动作序、错误语义、时钟注入与装配契约（issue #165 reclaimInterrupted）：
// CAS → 括号 → 事件、CAS 拒绝即短路、单会话失败不中止整轮、边界确定性。

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oagentkit/goagent-kernel/eventbus"
)

// ── 收敛体与错误语义 ────────────────────────────────────────────────────

func TestReclaimStopsWhenCASRefused(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-(DefaultStall + time.Second))},
		refused:  map[string]bool{"s1": true}, // 他方已处理（条件迁移零行）
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n := mustRecover(t, r, false)
	if n != 0 {
		t.Fatalf("CAS 未认领不得计入收敛, n=%d", n)
	}
	for _, c := range store.log() {
		if strings.HasPrefix(c, "bracket:") || strings.HasPrefix(c, "pub:") {
			t.Fatalf("CAS 被拒后不得再发括号/事件（双写比不写更伤审计）: %s", c)
		}
	}
}

func TestReclaimCASFailureSurfacesWithoutSideEffects(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-(DefaultStall + time.Second))},
		casErr:   fmt.Errorf("存储故障"),
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n, err := r.RecoverInterrupted(context.Background())
	if n != 0 || err == nil || !strings.Contains(err.Error(), "条件收敛失败") {
		t.Fatalf("收敛写失败必须带出且不计数: n=%d err=%v", n, err)
	}
	for _, c := range store.log() {
		if strings.HasPrefix(c, "bracket:") || strings.HasPrefix(c, "pub:") {
			t.Fatalf("收敛失败不得发括号/事件: %s", c)
		}
	}
}

func TestScanContinuesPastSingleSessionFailure(t *testing.T) {
	// 单会话读事实失败不中止整轮：另一个孤儿照常收敛，错误聚合带出。
	store := &fakeStore{
		sessions: []Session{
			{ID: "bad", Owner: "replica-a", CreatedAt: rbase.Add(-2 * time.Hour)},
			{ID: "good", Owner: "replica-a", CreatedAt: rbase.Add(-2 * time.Hour)},
		},
		last: map[string]time.Time{
			"bad":  rbase.Add(-2 * time.Hour),
			"good": rbase.Add(-2 * time.Hour),
		},
		lastErr: map[string]error{"bad": fmt.Errorf("读超时")},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase}, store)

	n, err := r.RecoverBootOrphans(context.Background())
	if n != 1 {
		t.Fatalf("另一个孤儿应照常收敛, n=%d", n)
	}
	if err == nil || !strings.Contains(err.Error(), "读最后事件时刻失败") {
		t.Fatalf("读失败必须聚合带出, got %v", err)
	}
}

func TestListRunningFailureAbortsScan(t *testing.T) {
	store := &fakeStore{listErr: fmt.Errorf("存储不可达")}
	r, _ := newRig(t, Config{InstanceID: "replica-a", Boot: rbase}, store)

	if n, err := r.RecoverBootOrphans(context.Background()); n != 0 || err == nil || !strings.Contains(err.Error(), "列 running 会话失败") {
		t.Fatalf("列会话失败应立即返回: n=%d err=%v", n, err)
	}
}

// ── 时钟注入确定性 ──────────────────────────────────────────────────────

func TestClockInjectionMakesBoundaryDeterministic(t *testing.T) {
	// 固定在 rbase：静默恰达阈值（含等号）→ 收；租约恰达租约+宽限 → 过期。
	store := &fakeStore{
		sessions: []Session{
			{ID: "stall", Owner: "replica-a", CreatedAt: rbase.Add(-2 * time.Hour)},
			{ID: "lease", Owner: "replica-b", CreatedAt: rbase.Add(-2 * time.Hour)},
		},
		last: map[string]time.Time{
			"stall": rbase.Add(-DefaultStall), // 恰等于阈值
			"lease": rbase.Add(-2 * time.Hour),
		},
		beats: map[string]time.Time{
			"replica-b": rbase.Add(-(DefaultLeaseTTL + GraceWindow)), // 恰达租约+宽限
		},
	}
	r, _ := newRig(t, Config{InstanceID: "replica-a"}, store)

	n := mustRecover(t, r, false)
	if n != 2 {
		t.Fatalf("两个边界都应收敛（含等号）, n=%d", n)
	}
}

// ── 装配 ────────────────────────────────────────────────────────────────

func TestNewRejectsMissingDeps(t *testing.T) {
	pub := func(string, string, *uint64, string, string, map[string]any) {}
	for _, tc := range []struct {
		name  string
		store Store
		pub   Pub
		cfg   Config
	}{
		{"nil store", nil, pub, Config{InstanceID: "a"}},
		{"nil pub", &fakeStore{}, nil, Config{InstanceID: "a"}},
		{"空 InstanceID", &fakeStore{}, pub, Config{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("装配缺件应 fail fast panic")
				}
			}()
			New(tc.store, tc.pub, tc.cfg)
		})
	}
}

func TestSessionFinishedCarriesByAttribution(t *testing.T) {
	store := &fakeStore{
		sessions: []Session{{ID: "s1", CaseID: "c1", Owner: "replica-a", CreatedAt: rbase.Add(-time.Hour)}},
		last:     map[string]time.Time{"s1": rbase.Add(-(DefaultStall + time.Second))},
	}
	var got map[string]any
	var gotTyp string
	r := NewWithClock(store, func(_, _ string, _ *uint64, typ, _ string, payload map[string]any) {
		gotTyp, got = typ, payload
	}, Config{InstanceID: "replica-a"}, func() time.Time { return rbase })

	if _, err := r.RecoverInterrupted(context.Background()); err != nil {
		t.Fatalf("扫描不应带错: %v", err)
	}
	if gotTyp != eventbus.EventSessionFinished {
		t.Fatalf("收尾事件应为 session_finished, got %q", gotTyp)
	}
	if got["by"] != ByRecovery || !strings.Contains(got["error"].(string), ByRecovery) {
		t.Fatalf("事件载荷应带归因 by=recovery 且写进说明, got %v", got)
	}
}

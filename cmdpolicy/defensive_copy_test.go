package cmdpolicy_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/yi-nology/goagent-kernel/cmdpolicy"
)

// TestNewIsolatesFromCaller 构造期必须是**副本**（issue #385）：
// 宿主在 New 之后改自己的原变量不得改穿活策略。
//
// 方向 = 更松（fail-open）：一条 forbidden 规则被改掉后，Decide 的结论从
// forbidden 变成 allow，而报告里一切正常（不报错、不告警）。
func TestNewIsolatesFromCaller(t *testing.T) {
	rules := []cmdpolicy.Rule{{
		Name:     "wipe",
		Prefix:   cmdpolicy.Prefix{{"rm"}},
		Decision: cmdpolicy.DecisionForbidden,
		Match:    []string{"rm -rf /tmp/x"},
	}}
	p, err := cmdpolicy.New(rules)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	before := p.Decide([]string{"rm", "-rf", "/tmp/x"})
	if before.Decision != cmdpolicy.DecisionForbidden {
		t.Fatalf("正对照：规则应命中 forbidden，实得 %+v", before)
	}
	// 改写入参：Prefix 是**两层**切片，这里改的是内层（一层 slices.Clone 不够）。
	rules[0].Prefix[0][0] = "ls"
	rules[0].Match[0] = "ls -la"
	after := p.Decide([]string{"rm", "-rf", "/tmp/x"})
	if after != before || after.Explain() != before.Explain() {
		t.Fatalf("活策略被入参改穿（fail-open）：改前 %+v，改后 %+v", before, after)
	}
}

// TestRulesSnapshotIsIndependent 出口面必须是**副本**（issue #385）：
// Rules() 自称「快照」，但只按值复制结构体只复制切片头——宿主原地改写快照会
// 改穿活策略（forbidden → allow，fail-open）。
//
// 「再次 Rules() 逐字相同」也是契约的一部分：快照不是活策略的视图。
func TestRulesSnapshotIsIndependent(t *testing.T) {
	p := mustPolicy(t, cmdpolicy.Rule{
		Name:     "wipe",
		Prefix:   cmdpolicy.Prefix{{"rm"}},
		Decision: cmdpolicy.DecisionForbidden,
		Match:    []string{"rm -rf /tmp/x"},
	})
	before := p.Decide([]string{"rm", "-rf", "/tmp/x"})
	if before.Decision != cmdpolicy.DecisionForbidden {
		t.Fatalf("正对照：规则应命中 forbidden，实得 %+v", before)
	}
	orig := p.Rules()
	snap := p.Rules()
	if len(snap) != 1 {
		t.Fatalf("快照应含一条规则，实得 %+v", snap)
	}
	if !rulesEqual(orig, snap) {
		t.Fatalf("两次取快照必须逐字相同： %+v vs %+v", orig, snap)
	}
	// 原地改写快照：内层 token（Prefix 是两层切片）与 Match 项都要试。
	snap[0].Prefix[0][0] = "ls"
	snap[0].Match[0] = "ls -la"
	after := p.Decide([]string{"rm", "-rf", "/tmp/x"})
	if after != before || after.Explain() != before.Explain() {
		t.Fatalf("快照改穿活策略（fail-open）：改前 %+v，改后 %+v", before, after)
	}
	again := p.Rules()
	if !rulesEqual(orig, again) {
		t.Fatalf("改写快照后再次取快照必须仍是原值： %+v vs %+v", orig, again)
	}
}

// TestRulesSnapshotConcurrentMutationIsRaceFree 并发改写快照与判定之间不得有
// 数据竞争（issue #385）：坏实现下快照与活策略共享底层数组，-race 会当场报
// DATA RACE（宿主写 / 内核读）。
//
// 正对照：好实现下快照是独立副本 ⇒ 两个 goroutine 各写各的，无竞争。
func TestRulesSnapshotConcurrentMutationIsRaceFree(t *testing.T) {
	p := mustPolicy(t, cmdpolicy.Rule{
		Name:     "wipe",
		Prefix:   cmdpolicy.Prefix{{"rm"}},
		Decision: cmdpolicy.DecisionForbidden,
		Match:    []string{"rm -rf /tmp/x"},
	})
	const iters = 2000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			snap := p.Rules()
			if len(snap) == 0 {
				continue
			}
			snap[0].Prefix[0][0] = "ls"
			snap[0].Match[0] = "ls -la"
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			p.Decide([]string{"rm", "-rf", "/tmp/x"})
		}
	}()
	wg.Wait()
}

// rulesEqual 逐项比较两组规则快照（Rule 含切片，不能用 ==）。
func rulesEqual(a, b []cmdpolicy.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Decision != b[i].Decision ||
			a[i].Justification != b[i].Justification {
			return false
		}
		if len(a[i].Prefix) != len(b[i].Prefix) {
			return false
		}
		for j := range a[i].Prefix {
			if !slices.Equal(a[i].Prefix[j], b[i].Prefix[j]) {
				return false
			}
		}
		if !slices.Equal(a[i].Match, b[i].Match) || !slices.Equal(a[i].NotMatch, b[i].NotMatch) {
			return false
		}
	}
	return true
}

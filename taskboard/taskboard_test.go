// taskboard_test.go —— 快照不变量的**成对**判定。
//
// 每条不变量都配一个「干净快照不报」的对照：只钉「坏形态报出来」的用例
// 经不起变异——退化成「永远报」也能全绿，而那会把正确的图判成错的。
package taskboard

import (
	"testing"
	"time"
)

// testNow 表驱动用例共用的「现在」：到期时刻残留的形态需要非零 Until。
var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// clean 一张自洽的任务图：t1 已完成、t2 依赖 t1 且被认领。
func clean() []Task {
	return []Task{
		{ID: "t1", State: StateDone, Seq: 3},
		{ID: "t2", State: StateClaimed, Owner: "a1", Lease: Lease{Token: "tok"}, Deps: []TaskID{"t1"}, Seq: 2},
	}
}

func codesOf(ps []Problem) map[string]bool {
	out := map[string]bool{}
	for _, p := range ps {
		out[p.Code] = true
	}
	return out
}

func TestCheckCleanSnapshotIsSilent(t *testing.T) {
	if ps := Check(clean()); len(ps) != 0 {
		t.Fatalf("自洽快照不该报问题: %v", ps)
	}
}

func TestCheckCatchesEachInvariant(t *testing.T) {
	cases := []struct {
		name  string
		tasks []Task
		code  string
	}{
		{"空 id", append(clean(), Task{State: StateOpen}), CodeEmptyID},
		{"id 重复", append(clean(), Task{ID: "t1", State: StateOpen}), CodeDuplicateID},
		{"状态不在词表", append(clean(), Task{ID: "t3", State: "weird"}), CodeUnknownState},
		{"依赖不存在", append(clean(), Task{ID: "t3", State: StateOpen, Deps: []TaskID{"nope"}}), CodeUnknownDep},
		{"依赖自己", append(clean(), Task{ID: "t3", State: StateOpen, Deps: []TaskID{"t3"}}), CodeSelfDep},
		{"认领却没有令牌", append(clean(), Task{ID: "t3", State: StateClaimed, Owner: "a1"}), CodeClaimedNoLease},
		{"非认领态残留令牌", append(clean(), Task{ID: "t3", State: StateOpen, Lease: Lease{Token: "tok"}}), CodeLeaseNoClaim},
		{"非认领态残留代次", append(clean(), Task{ID: "t3", State: StateOpen, Lease: Lease{Fence: 3}}), CodeLeaseNoClaim},
		{"非认领态残留到期时刻", append(clean(), Task{ID: "t3", State: StateOpen, Lease: Lease{Until: testNow.Add(time.Minute)}}), CodeLeaseNoClaim},
		{"终态仍带 owner", append(clean(), Task{ID: "t3", State: StateDone, Owner: "a1", Seq: 1}), CodeTerminalClaimed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if codes := codesOf(Check(c.tasks)); !codes[c.code] {
				t.Fatalf("必须报出 %s，实得 %v", c.code, Check(c.tasks))
			}
		})
	}
}

// TestCheckCatchesBlockedClaim 单独一条：依赖未完成却被认领。
//
// 这是「前置约束被跳过」的形态，也是并发协调里最贵的一种——两段工作并行施加，
// 各自都成功，事后没有任何一处报错。
func TestCheckCatchesBlockedClaim(t *testing.T) {
	tasks := []Task{
		{ID: "t1", State: StateClaimed, Owner: "a1", Lease: Lease{Token: "tok"}},
		{ID: "t2", State: StateClaimed, Owner: "a2", Lease: Lease{Token: "tok2"}, Deps: []TaskID{"t1"}},
	}
	if codes := codesOf(Check(tasks)); !codes[CodeBlockedClaim] {
		t.Fatalf("依赖未完成却被认领必须报出，实得 %v", Check(tasks))
	}
}

// TestCheckCatchesCycle 环必须报出来：环上的任务永远不会就绪，而宿主看不见
// 任何错误——它只会觉得「这批子任务一直没完成」。
func TestCheckCatchesCycle(t *testing.T) {
	tasks := []Task{
		{ID: "t1", State: StateOpen, Deps: []TaskID{"t3"}},
		{ID: "t2", State: StateOpen, Deps: []TaskID{"t1"}},
		{ID: "t3", State: StateOpen, Deps: []TaskID{"t2"}},
	}
	if codes := codesOf(Check(tasks)); !codes[CodeCycle] {
		t.Fatalf("依赖成环必须报出，实得 %v", Check(tasks))
	}
}

// TestCheckIsDeterministic 判定结果必须与 map 遍历顺序无关：
// 不确定的报告顺序会让「同样的图两次跑出不同的 diff」。
func TestCheckIsDeterministic(t *testing.T) {
	tasks := []Task{
		{ID: "t3", State: StateOpen, Deps: []TaskID{"nope"}},
		{ID: "t1", State: "weird"},
		{ID: "t2", State: StateClaimed},
	}
	first := Check(tasks)
	for range 20 {
		got := Check(tasks)
		if len(got) != len(first) {
			t.Fatalf("两次判定条数不同: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("第 %d 条不稳定: %+v vs %+v", j, got[j], first[j])
			}
		}
	}
}

// TestCheckTenantIdentity 任务身份是 (Tenant, ID)：同 id 不同租户不是重复。
func TestCheckTenantIdentity(t *testing.T) {
	tasks := []Task{
		{Tenant: "tenant-a", ID: "t1", State: StateOpen},
		{Tenant: "tenant-b", ID: "t1", State: StateOpen},
	}
	if ps := Check(tasks); len(ps) != 0 {
		t.Fatalf("同 id 不同租户是两条任务，不得报重复: %v", ps)
	}
	// 同租户内同 id 仍是重复。
	tasks = append(tasks, Task{Tenant: "tenant-a", ID: "t1", State: StateOpen})
	ps := Check(tasks)
	if len(ps) != 1 || ps[0].Code != CodeDuplicateID || ps[0].Tenant != "tenant-a" {
		t.Fatalf("同租户内 id 重复必须报出且带租户归属，实得 %v", ps)
	}
}

// TestCheckTenantDepsResolveWithinTenant 依赖只在同租户内解析：
// 他租户的同 id 任务不可见，按「依赖不存在」处理。
func TestCheckTenantDepsResolveWithinTenant(t *testing.T) {
	tasks := []Task{
		{Tenant: "tenant-a", ID: "t1", State: StateDone},
		{Tenant: "tenant-b", ID: "t1", State: StateOpen},
		{Tenant: "tenant-b", ID: "t2", State: StateOpen, Deps: []TaskID{"t1"}},
	}
	if ps := Check(tasks); len(ps) != 0 {
		t.Fatalf("同租户内依赖 t1 已完成，不该报问题: %v", ps)
	}
	// 跨租户依赖：tenant-a 的 t1 已完成，不能替 tenant-b 的依赖作数。
	tasks = []Task{
		{Tenant: "tenant-a", ID: "t1", State: StateDone},
		{Tenant: "tenant-b", ID: "t2", State: StateOpen, Deps: []TaskID{"t1"}},
	}
	ps := Check(tasks)
	if len(ps) != 1 || ps[0].Code != CodeUnknownDep || ps[0].Tenant != "tenant-b" {
		t.Fatalf("跨租户依赖必须按不存在报出，实得 %v", ps)
	}
}

// TestCheckTenantCyclesArePerTenant 环按租户分别判定：一个租户的环不得
// 把另一个租户的同 id 任务卷进来。
func TestCheckTenantCyclesArePerTenant(t *testing.T) {
	tasks := []Task{
		// tenant-a 内部成环。
		{Tenant: "tenant-a", ID: "t1", State: StateOpen, Deps: []TaskID{"t2"}},
		{Tenant: "tenant-a", ID: "t2", State: StateOpen, Deps: []TaskID{"t1"}},
		// tenant-b 是自洽的同 id 图。
		{Tenant: "tenant-b", ID: "t1", State: StateOpen},
	}
	ps := Check(tasks)
	if len(ps) != 1 || ps[0].Code != CodeCycle || ps[0].Tenant != "tenant-a" {
		t.Fatalf("环必须只报 tenant-a 且带租户归属，实得 %v", ps)
	}
}

// TestStateValid 词表外状态必须可判定，且不因 Terminal()==false 被当成可领。
func TestStateValid(t *testing.T) {
	for _, s := range []State{StateOpen, StateClaimed, StateDone, StateFailed} {
		if !s.Valid() {
			t.Errorf("State(%q).Valid() 应为 true", s)
		}
	}
	for _, s := range []State{"", "weird_state", "OPEN", "done "} {
		if s.Valid() {
			t.Errorf("State(%q).Valid() 应为 false", s)
		}
	}
	// 词表外状态的 Terminal() 是 false（不是终态），写路径必须靠 Valid() 拦。
	if State("weird_state").Terminal() {
		t.Fatal("词表外状态不得被判终态（那会掩盖读不懂的事实）")
	}
}

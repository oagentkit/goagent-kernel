package checkpoint

// 恢复计划的行为钉住。合成图：step-alpha / step-beta / step-gamma。

import (
	"reflect"
	"slices"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

func graph(ids ...string) []StepDef {
	out := make([]StepDef, 0, len(ids))
	for _, id := range ids {
		out = append(out, StepDef{ID: id, Label: "环节 " + id})
	}
	return out
}

// TestFirstRunNoSnapshot 首次执行：全量跑，无异常。
func TestFirstRunNoSnapshot(t *testing.T) {
	p := BuildPlan(graph("step-alpha", "step-beta"), nil)
	if len(p.Run) != 2 || len(p.Skip) != 0 || len(p.Anomalies) != 0 {
		t.Fatalf("首次执行应全量无异常，实得 %+v", p)
	}
	if p.Blocked() {
		t.Fatal("首次执行不应被阻断")
	}
}

// TestPrefixDoneIsSkipped 正常路径：完成集是前缀 → 续跑剩余，不报异常。
func TestPrefixDoneIsSkipped(t *testing.T) {
	s := snap("run", 2, contract.StatusRunning, "step-alpha", "step-beta")
	p := BuildPlan(graph("step-alpha", "step-beta", "step-gamma"), &s)
	if !reflect.DeepEqual(p.Skip, []string{"step-alpha", "step-beta"}) {
		t.Fatalf("已完成应被跳过，实得 %v", p.Skip)
	}
	if !reflect.DeepEqual(p.Run, []string{"step-gamma"}) {
		t.Fatalf("未完成应续跑，实得 %v", p.Run)
	}
	if len(p.Anomalies) != 0 {
		t.Fatalf("正常路径不应报异常，实得 %+v", p.Anomalies)
	}
}

// TestHoleNotTrusted 完成集有空洞 → 空洞之后的完成**不采信**，整体重跑并报异常。
// 这是本包最关键的判定：采信一半等于承认一个已失序的快照仍然可信。
func TestHoleNotTrusted(t *testing.T) {
	s := snap("run", 3, contract.StatusRunning, "step-gamma")
	p := BuildPlan(graph("step-alpha", "step-beta", "step-gamma"), &s)
	if len(p.Skip) != 0 {
		t.Fatalf("空洞存在时不应采信任何完成项，实得 %v", p.Skip)
	}
	if !reflect.DeepEqual(p.Run, []string{"step-alpha", "step-beta", "step-gamma"}) {
		t.Fatalf("失序后应全量重跑，实得 %v", p.Run)
	}
	if !p.HasAnomaly(AnomalyDoneOutOfOrder) {
		t.Fatalf("应报告失序，实得 %+v", p.Anomalies)
	}
	if p.Blocked() {
		t.Fatal("失序不阻断：内核已按最保守解释给出可执行计划")
	}
}

// TestDoneNotInGraph 快照里的步骤在当前图中不存在 → 报告但不重跑。
func TestDoneNotInGraph(t *testing.T) {
	s := snap("run", 4, contract.StatusRunning, "step-alpha", "step-removed")
	p := BuildPlan(graph("step-alpha", "step-beta"), &s)
	if !reflect.DeepEqual(p.Run, []string{"step-beta"}) {
		t.Fatalf("只应续跑图内未完成项，实得 %v", p.Run)
	}
	found := false
	for _, a := range p.Anomalies {
		if a.Kind == AnomalyDoneNotInGraph && a.Step == "step-removed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应点名报告图外完成项，实得 %+v", p.Anomalies)
	}
}

// TestDuplicateStepInGraph 图内重复 ID → 去重并报告，且保持首次出现位置。
func TestDuplicateStepInGraph(t *testing.T) {
	p := BuildPlan(graph("step-alpha", "step-beta", "step-alpha"), nil)
	if !reflect.DeepEqual(p.Run, []string{"step-alpha", "step-beta"}) {
		t.Fatalf("重复 ID 应只留首次出现处，实得 %v", p.Run)
	}
	if !p.HasAnomaly(AnomalyDuplicateStep) {
		t.Fatalf("应报告重复步骤，实得 %+v", p.Anomalies)
	}
}

// TestEmptyStepIDDropped 空 ID 从图里剔除并报告（不进 Run）。
func TestEmptyStepIDDropped(t *testing.T) {
	p := BuildPlan(graph("", "step-alpha", ""), nil)
	if !reflect.DeepEqual(p.Run, []string{"step-alpha"}) {
		t.Fatalf("空 ID 不应进 Run，实得 %v", p.Run)
	}
	if !p.HasAnomaly(AnomalyEmptyStepID) {
		t.Fatalf("应报告空 ID，实得 %+v", p.Anomalies)
	}
}

// TestTerminalNotAutoReopened 快照标记已终结但仍有未完成步骤 → 阻断且不重开。
func TestTerminalNotReopened(t *testing.T) {
	s := snap("run", 9, contract.StatusCancelled, "step-alpha")
	p := BuildPlan(graph("step-alpha", "step-beta", "step-gamma"), &s)
	if !p.Blocked() {
		t.Fatal("已终结运行有待执行项时必须阻断")
	}
	if len(p.Run) != 0 || len(p.Skip) != 0 {
		t.Fatalf("被阻断的计划不应给出半截 Run/Skip: Run=%v Skip=%v", p.Run, p.Skip)
	}
	if !p.HasAnomaly(AnomalyCompleteWithPending) {
		t.Fatalf("应报告待执行项冲突，实得 %+v", p.Anomalies)
	}
}

// TestTerminalFullyDone 终态且全部完成 → 干净跳过，不阻断不报异常。
func TestTerminalFullyDone(t *testing.T) {
	s := snap("run", 12, contract.StatusSucceeded, "step-alpha", "step-beta")
	p := BuildPlan(graph("step-alpha", "step-beta"), &s)
	if len(p.Run) != 0 {
		t.Fatalf("已完成不该有 Run，实得 %v", p.Run)
	}
	if !reflect.DeepEqual(p.Skip, []string{"step-alpha", "step-beta"}) {
		t.Fatalf("全部完成应全跳过，实得 %v", p.Skip)
	}
	if p.Blocked() || len(p.Anomalies) != 0 {
		t.Fatalf("干净完成不应阻断或报异常: blocked=%v %+v", p.Blocked(), p.Anomalies)
	}
}

// TestTerminalStatusesAllBlock 等价终态都要被认出来（含 failed / timed_out）。
func TestTerminalStatusesAllBlock(t *testing.T) {
	for _, st := range []string{
		contract.StatusSucceeded, contract.StatusFailed,
		contract.StatusCancelled, contract.StatusTimedOut,
	} {
		s := snap("run", 1, st)
		if !BuildPlan(graph("step-alpha", "step-beta"), &s).Blocked() {
			t.Fatalf("状态 %q 应被认作终态并阻断", st)
		}
	}
}

// TestNonTerminalStatusStillRuns 待审批不是终态 → 正常续跑剩余。
func TestNonTerminalStatusStillRuns(t *testing.T) {
	s := snap("run", 3, contract.StatusWaitingApproval, "step-alpha")
	p := BuildPlan(graph("step-alpha", "step-beta"), &s)
	if p.Blocked() {
		t.Fatal("待审批不是终态，不应阻断")
	}
	if !reflect.DeepEqual(p.Run, []string{"step-beta"}) {
		t.Fatalf("应续跑剩余，实得 %v", p.Run)
	}
}

// TestEmptyDoneIgnored 快照 Done 里的空串不参与完成判定。
func TestEmptyDoneIgnored(t *testing.T) {
	s := snap("run", 2, contract.StatusRunning, "", "step-alpha")
	p := BuildPlan(graph("step-alpha", "step-beta"), &s)
	if !reflect.DeepEqual(p.Skip, []string{"step-alpha"}) {
		t.Fatalf("空串不应造成空洞，实得 %v", p.Skip)
	}
	if p.HasAnomaly(AnomalyDoneOutOfOrder) {
		t.Fatalf("空串不应被当成图外完成项: %+v", p.Anomalies)
	}
}

// TestPlanDeterministic 同一输入必得同一计划（编排可复现的基础）。
func TestPlanDeterministic(t *testing.T) {
	g := graph("step-alpha", "step-beta", "step-gamma", "step-alpha")
	s := snap("run", 5, contract.StatusRunning, "step-alpha", "step-beta")
	first := BuildPlan(g, &s)
	for i := 0; i < 20; i++ {
		if got := BuildPlan(g, &s); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次结果不一致:\n%+v\n%+v", i, first, got)
		}
	}
}

// TestSkipRunNoAlias 结果切片不得与**入参快照**共享底层数组（issue #393）。
//
// 此前这条用例断言的源是编排图的 id 列表——那个切片从未传给 BuildPlan
// （graph(ids...) 逐个转成新的 StepDef 值），结构上**不可能**失败：真出现
// 别名缺陷时它照样绿（区分力为零），且失败文案把排查方向指向了错误的源。
//
// 真正的别名面有两个，本用例逐条钉住：
//
//  1. 结果切片与快照的 Done（[]string ↔ []string，且真被传入）；
//  2. Skip 与 Run 互相共享底层数组。
func TestSkipRunNoAlias(t *testing.T) {
	// 完成集顺序与图顺序不同：别名一旦发生，写回的值就与原值不同，
	// 内容比对能看见（否则「写回同一个值」什么也证明不了）。
	s := snap("run", 1, contract.StatusRunning, "step-alpha", "step-beta")
	s.Done = append(make([]string, 0, 8), s.Done...)
	p := BuildPlan(graph("step-beta", "step-alpha", "step-gamma"), &s)

	if !reflect.DeepEqual(p.Skip, []string{"step-beta", "step-alpha"}) {
		t.Fatalf("跳过集不对：%v", p.Skip)
	}
	if !reflect.DeepEqual(p.Run, []string{"step-gamma"}) {
		t.Fatalf("续跑集不对：%v", p.Run)
	}
	// 快照侧：BuildPlan 返回后 Done 逐字不变。
	if !reflect.DeepEqual(s.Done, []string{"step-alpha", "step-beta"}) {
		t.Fatalf("结果切片与快照的 Done 共享了底层数组：%v", s.Done)
	}
	// 互不串：改 Skip 不得改 Run。
	runBefore := slices.Clone(p.Run)
	p.Skip[0] = "mutated"
	if !reflect.DeepEqual(p.Run, runBefore) {
		t.Fatalf("Skip 与 Run 共享了底层数组：Run=%v", p.Run)
	}
	// Skip 与 Run 的底层数组不同。
	if &p.Skip[0] == &p.Run[0] {
		t.Fatal("Skip 与 Run 共享同一个底层数组")
	}
}

// TestUnknownStatusNotResumed 词表外状态不得被当成「可续跑」（issue #219）：
// 此前 isTerminal 对未知值 fail-open——宿主把运行落库为自定义终态
// （如 "aborted"）时，已终结的运行被静默重开，且 Anomalies 一个字都没有。
// 「判不了」按阻塞侧处理（与 Reconcile 对未登记闸态同一条口径）。
func TestUnknownStatusNotResumed(t *testing.T) {
	s := snap("run", 7, "aborted", "step-alpha")
	p := BuildPlan(graph("step-alpha", "step-beta"), &s)
	if len(p.Run) != 0 {
		t.Fatalf("词表外状态可能是宿主自定义终态，不得自动续跑，实得 Run=%v", p.Run)
	}
	if !p.Blocked() {
		t.Fatal("词表外状态 + 有待执行项应阻断")
	}
	if !p.HasAnomaly(AnomalyUnknownStatus) {
		t.Fatalf("应报告 unknown_status 异常，实得 %+v", p.Anomalies)
	}
}

// TestUnknownStatusFullyDoneRunsNothing 词表外状态但全部完成：无待执行项时
// 不阻断——「要不要重开」的问题不存在，只是照快照跳过。
func TestUnknownStatusFullyDoneRunsNothing(t *testing.T) {
	s := snap("run", 8, "aborted", "step-alpha", "step-beta")
	p := BuildPlan(graph("step-alpha", "step-beta"), &s)
	if len(p.Run) != 0 || !reflect.DeepEqual(p.Skip, []string{"step-alpha", "step-beta"}) {
		t.Fatalf("全部完成应全跳过: Run=%v Skip=%v", p.Run, p.Skip)
	}
	if p.Blocked() {
		t.Fatalf("无待执行项不应阻断: %+v", p.Anomalies)
	}
}

// resume_classify_test.go —— 「什么算词表外」这条边界在 checkpoint 侧的可观测行为
// （issue #363 验收 1）。
package checkpoint

import (
	"testing"

	"github.com/oagentkit/goagent-kernel/contract"
)

// TestBuildPlanClassifiesEveryStatus 三态边界对**全词表 + 词表外取值**逐字
// 一致（issue #363）。
//
// 本用例把「同一个状态在 checkpoint 与 controlplane 必须得到同一结论」这条
// 承诺落到**可观测面**：
//
//   - 已知终态 ⇒ 报 complete_with_pending，不自动重开；
//   - 词表外   ⇒ 报 unknown_status，不自动续跑；
//   - 已知过程态 ⇒ 不报这两类异常，计划照常续跑。
//
// 分类判定面是 contract.ClassifyStatus（唯一一份，见 contract/classifier_gate_test.go），
// 故本表与 controlplane 侧那张表必然逐字一致。
//
// # 词表遍历而非手抄
//
// 取值来自 contract.AllStatuses()（词表的枚举出口），不手抄——手抄表会漂，
// 而漂了的测试给出的是**假绿**：内核新增一个状态，这条用例根本不会去看它。
func TestBuildPlanClassifiesEveryStatus(t *testing.T) {
	graph := []StepDef{{ID: "a"}, {ID: "b"}}
	statuses := append([]string{"", "aborted", "degraded", "done", "claimed", "RUNNING", " succeeded "}, contract.AllStatuses()...)
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			p := BuildPlan(graph, &Snapshot{RunID: "r1", Status: status})
			want := contract.ClassifyStatus(status)
			switch want {
			case contract.StatusTerminal:
				if !hasAnomaly(p, AnomalyCompleteWithPending) {
					t.Fatalf("已知终态 %q 必须报 complete_with_pending（不自动重开），实得 %+v", status, p.Anomalies)
				}
				if hasAnomaly(p, AnomalyUnknownStatus) {
					t.Fatalf("已知终态 %q 不得报 unknown_status，实得 %+v", status, p.Anomalies)
				}
			case contract.StatusUnknown:
				if !hasAnomaly(p, AnomalyUnknownStatus) {
					t.Fatalf("词表外状态 %q 必须报 unknown_status（判不了就阻塞），实得 %+v", status, p.Anomalies)
				}
				if hasAnomaly(p, AnomalyCompleteWithPending) {
					t.Fatalf("词表外状态 %q 不得报 complete_with_pending，实得 %+v", status, p.Anomalies)
				}
			default:
				if hasAnomaly(p, AnomalyUnknownStatus) || hasAnomaly(p, AnomalyCompleteWithPending) {
					t.Fatalf("已知过程态 %q 不得报终态/未知两类异常，实得 %+v", status, p.Anomalies)
				}
				if len(p.Run) == 0 {
					t.Fatalf("已知过程态 %q 必须照常续跑（计划不该被作废）", status)
				}
			}
		})
	}
}

// TestBuildPlanStatusSpotChecks 逐值钉住几个**判别性**取值。
//
// 上面那条用例的期望值由 contract.ClassifyStatus 算出——若判定面本身被改错，
// 那条用例会跟着错（自证）。故这里补一组**手写**期望，覆盖两侧结论最容易
// 分叉的取值：
//
//   - approved：环节轴的过程态（不是词表外）；
//   - done：taskboard 的取值，**不在**会话/环节词表内 ⇒ 判不了；
//   - aborted：宿主自定义终态 ⇒ 判不了；
//   - RUNNING / " succeeded "：写法变体与规范形态同结论。
func TestBuildPlanStatusSpotChecks(t *testing.T) {
	graph := []StepDef{{ID: "a"}, {ID: "b"}}
	cases := []struct {
		status string
		want   contract.StatusClass
	}{
		{"approved", contract.StatusOpen},
		{"done", contract.StatusUnknown},
		{"aborted", contract.StatusUnknown},
		{"RUNNING", contract.StatusOpen},
		{" succeeded ", contract.StatusTerminal},
	}
	for _, c := range cases {
		p := BuildPlan(graph, &Snapshot{RunID: "r1", Status: c.status})
		got := contract.StatusOpen
		switch {
		case hasAnomaly(p, AnomalyCompleteWithPending):
			got = contract.StatusTerminal
		case hasAnomaly(p, AnomalyUnknownStatus):
			got = contract.StatusUnknown
		}
		if got != c.want {
			t.Errorf("状态 %q 的可观测分类 = %v，want %v（异常 %+v）", c.status, got, c.want, p.Anomalies)
		}
	}
}

// hasAnomaly 报告计划里是否报了指定类型的异常。
func hasAnomaly(p Plan, kind AnomalyKind) bool {
	for _, a := range p.Anomalies {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

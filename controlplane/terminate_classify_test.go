// terminate_classify_test.go —— 「什么算词表外」这条边界在 controlplane 侧的
// 可观测行为（issue #363 验收 1）。
package controlplane

import (
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
)

// TestTerminateClassifiesEveryStatus 三态边界对**全词表 + 词表外取值**逐字
// 一致（issue #363）。
//
// 可观测面：
//
//   - 已知终态 ⇒ ErrTerminalRun（运行已收尾，不再终止）；
//   - 词表外   ⇒ ErrUnknownRunStatus（判不了就拒绝终止）；
//   - 已知过程态 ⇒ 终止成功。
//
// 分类判定面是 contract.ClassifyStatus（唯一一份，见 contract/classifier_gate_test.go），
// 故本表与 checkpoint 侧那张表必然逐字一致。
//
// 取值来自 contract.AllStatuses()（词表的枚举出口），不手抄——手抄表会漂，
// 而漂了的测试给出的是**假绿**。
func TestTerminateClassifiesEveryStatus(t *testing.T) {
	statuses := append([]string{"", "aborted", "degraded", "done", "claimed", "RUNNING", " succeeded "}, contract.AllStatuses()...)
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			aud := &MemAuditor{}
			m := NewMemPlane(aud)
			ctx := tenantCtx()
			runID := RunID("r-" + status)
			if err := m.SeedRun(runID, status, nil); err != nil {
				t.Fatalf("SeedRun: %v", err)
			}
			view, err := m.RunView(ctx, runID)
			if err != nil {
				t.Fatalf("RunView: %v", err)
			}
			err = m.Terminate(ctx, Termination{
				RunID: runID, ExpectedGen: view.Gen, Operator: "op-1", Basis: "合成依据",
			})
			switch contract.ClassifyStatus(status) {
			case contract.StatusTerminal:
				if !errors.Is(err, ErrTerminalRun) {
					t.Fatalf("已知终态 %q 必须报 ErrTerminalRun，实得 %v", status, err)
				}
			case contract.StatusUnknown:
				if !errors.Is(err, ErrUnknownRunStatus) {
					t.Fatalf("词表外状态 %q 必须报 ErrUnknownRunStatus，实得 %v", status, err)
				}
			default:
				if err != nil {
					t.Fatalf("已知过程态 %q 必须允许终止，实得 %v", status, err)
				}
			}
		})
	}
}

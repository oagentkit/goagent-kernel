// terminate_status_test.go —— 终态判据的三态口径（issue #334）。
package controlplane

import (
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// TestTerminateUnknownStatusFailsClosed issue #334：词表外状态（可能是宿主
// 自定义终态）不得按「非终态」放行。放行会把已终结的运行静默改写成
// cancelled、代次前进、审计谎报「已终止」——与 checkpoint 的三态判据
// （statusUnknown ⇒ 阻塞）方向相反。
func TestTerminateUnknownStatusFailsClosed(t *testing.T) {
	aud := &MemAuditor{}
	m := NewMemPlane(aud)
	ctx := tenantCtx()

	for _, status := range []string{"aborted", "degraded"} {
		t.Run(status, func(t *testing.T) {
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
			if !errors.Is(err, ErrUnknownRunStatus) {
				t.Fatalf("词表外状态必须拒绝终止并报 ErrUnknownRunStatus, got %v", err)
			}

			after, err := m.RunView(ctx, runID)
			if err != nil {
				t.Fatalf("RunView after: %v", err)
			}
			if after.Status != status {
				t.Fatalf("存储面状态必须一字不动（%q → %q）", status, after.Status)
			}
			if after.Gen != view.Gen {
				t.Fatalf("被拒的终止不得推进代次（%d → %d）", view.Gen, after.Gen)
			}

			entries, err := aud.Entries(ctx, string(runID))
			if err != nil {
				t.Fatalf("Entries: %v", err)
			}
			var refused bool
			for _, e := range entries {
				if strings.Contains(e.Detail, "已终止") {
					t.Fatalf("审计不得出现针对已终结运行的「已终止」记录: %+v", e)
				}
				if strings.Contains(e.Detail, "无法判定") {
					refused = true
				}
			}
			if !refused {
				t.Fatalf("拒绝必须留痕（可观测），实得 %+v", entries)
			}
		})
	}
}

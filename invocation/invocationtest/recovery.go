// recovery.go —— 恢复族契约（结果未知态与词表外取值）。
//
// 这一族单列一个文件：它们判的是**失败形态**（宿主实现容易在这两处放行），
// 与主套件里「存取与转移」的判据不同族，混在一起会让套件的场景清单读不出
// 结构。
package invocationtest

import (
	"context"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/invocation"
)

// runUncertainCanResume 结果未知态经裁决后可以重做（uncertain → executing）。
//
// 这一条是 P0-09 闭合的关键：崩溃落在「已取得执行权、结果未知」那一段时，
// 恢复路径必须**有地方可去**（裁决未生效 ⇒ 重做），而不是永久卡住或盲目
// 重试。存储实现若把 uncertain 当终态，恢复时会话报废。
func runUncertainCanResume(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	for _, to := range []invocation.State{invocation.StateExecuting, invocation.StateUncertain} {
		if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, to, invocation.Update{}); err != nil {
			t.Fatalf("推进到 %s 失败: %v", to, err)
		}
	}

	// 裁决「未生效」⇒ 允许重做（uncertain → executing 是合法转移）。
	got, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateExecuting, invocation.Update{})
	if err != nil {
		t.Fatalf("裁决未生效后应允许重做，got %v", err)
	}
	if got.State != invocation.StateExecuting {
		t.Fatalf("状态应为 executing，got %s", got.State)
	}
}

// runUnknownTargetRejected 目标状态不在词表内 ⇒ 拒绝（fail-closed）。
//
// 「读不懂的状态不许放行」是本包的底线：宿主实现若把词表外取值当默认态
// 写进去，恢复时会把一条**未知进度**的账目当成合法状态继续走。
func runUnknownTargetRejected(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	_, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.State("bogus"), invocation.Update{})
	if !errors.Is(err, invocation.ErrIllegalTransition) {
		t.Fatalf("词表外目标状态应判 ErrIllegalTransition，got %v", err)
	}

	// 且**不得**改动账目（失败的推进不许留下半截状态）。
	got, err := st.Get(context.Background(), e.SessionID, e.CallID)
	if err != nil {
		t.Fatalf("取回账目失败: %v", err)
	}
	if got.State != invocation.StatePlanned {
		t.Fatalf("被拒的推进不得改动状态，got %s", got.State)
	}
}

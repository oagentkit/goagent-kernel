package deadline

import (
	"context"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/contract"
)

// 本文件是 StatusOf 注入钩子（issue #237）的判据：
//   - 缺省（钩子 nil）⇒ contract 词表，行为与注入前逐字一致（消费不另造）；
//   - 注入宿主词表 ⇒ Status() 读数随宿主映射——非 contract 模型的宿主
//     不必再把 contract 词表自映射一遍；
//   - DefaultStatus 单独可用（宿主钩子「个别档重映射、其余回落内建」的垫底）。

// hostStatus 宿主自有词表（非 contract 模型）。
func hostStatus(o Outcome) string {
	switch o {
	case OutcomeTimedOut:
		return "session_wallclock_breached"
	case OutcomeCancelled:
		return "operator_aborted"
	default:
		return "" // 不落终态的两档保持空串约定
	}
}

// TestStatus缺省仍是contract词表 —— 注入路径不得改缺省行为：
// 钩子为 nil 时三个终态档的读数与 issue #237 之前逐字一致。
func TestStatus缺省仍是contract词表(t *testing.T) {
	defer func() { StatusOf = nil }() // 防本测试注入残留（顺序无关的保险）
	StatusOf = nil
	if got := OutcomeTimedOut.Status(); got != contract.StatusTimedOut {
		t.Fatalf("缺省 timed_out 应为 contract 词表，实得 %q", got)
	}
	if got := OutcomeCancelled.Status(); got != contract.StatusCancelled {
		t.Fatalf("缺省 cancelled 应为 contract 词表，实得 %q", got)
	}
	if got := OutcomeNone.Status(); got != "" {
		t.Fatalf("none 不落终态应为空串，实得 %q", got)
	}
	if got := OutcomeShutdown.Status(); got != "" {
		t.Fatalf("shutdown 不落终态应为空串，实得 %q", got)
	}
}

// TestStatus注入宿主词表 —— issue #237 验收：宿主（非 contract 模型）注入
// 自有映射后，全部 Outcome 的 Status() 读数随宿主词表；Route 的枚举判定
// （timed_out 臂先于停机臂）不受注入影响。
func TestStatus注入宿主词表(t *testing.T) {
	defer func() { StatusOf = nil }()
	StatusOf = hostStatus

	if got := OutcomeTimedOut.Status(); got != "session_wallclock_breached" {
		t.Fatalf("注入后 timed_out 应随宿主词表，实得 %q", got)
	}
	if got := OutcomeCancelled.Status(); got != "operator_aborted" {
		t.Fatalf("注入后 cancelled 应随宿主词表，实得 %q", got)
	}
	if got := OutcomeShutdown.Status(); got != "" {
		t.Fatalf("注入后不落终态档仍应为空串（宿主钩子保持约定），实得 %q", got)
	}

	// 枚举判定与词表映射是两件事：注入宿主词表后 Route 的三分不变
	//（次序纪律照旧——墙钟到点判 timed_out，不落停机臂）。
	tctx, _ := WithCause(context.Background(), -time.Second)
	if got := Route(tctx); got != OutcomeTimedOut {
		t.Fatalf("注入只改词表不改判定：应仍判 OutcomeTimedOut，实得 %v", got)
	}
}

// TestDefaultStatus回落垫底 —— 宿主钩子的典型形态：只重映射个别档、
// 其余回落内建；DefaultStatus 单独导出就是给这个形态用的（回落不必把
// contract 词表抄一遍）。
func TestDefaultStatus回落垫底(t *testing.T) {
	defer func() { StatusOf = nil }()
	StatusOf = func(o Outcome) string {
		if o == OutcomeCancelled {
			return "user_cancelled_v2" // 只重映射这一档
		}
		return DefaultStatus(o) // 其余回落内建（contract 词表）
	}
	if got := OutcomeCancelled.Status(); got != "user_cancelled_v2" {
		t.Fatalf("重映射档应随宿主，实得 %q", got)
	}
	if got := OutcomeTimedOut.Status(); got != contract.StatusTimedOut {
		t.Fatalf("回落档应随内建 contract 词表，实得 %q", got)
	}
	if got := DefaultStatus(OutcomeShutdown); got != "" {
		t.Fatalf("内建映射的不落终态档应为空串，实得 %q", got)
	}
}

package deadline

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

// TestStatusMappingInjection 钉住 issue #237 的接缝：默认映射 = contract 词表
// （零值兼容，行为与既有版本逐字一致）；宿主可注入自有 StatusOf 映射，
// 不必先把结论转成 contract 的状态常量。
func TestStatusMappingInjection(t *testing.T) {
	// ① 默认路径：Status 与 StatusWith(nil) 都走 contract 词表，逐字不变。
	// 不落终态的两档仍是空串（空串 = 本轮没有终态要写）。
	defaults := []struct {
		name string
		o    Outcome
		want string
	}{
		{"none 不落终态", OutcomeNone, ""},
		{"timed_out 消费 contract 词表", OutcomeTimedOut, contract.StatusTimedOut},
		{"cancelled 消费 contract 词表", OutcomeCancelled, contract.StatusCancelled},
		{"shutdown 不落终态", OutcomeShutdown, ""},
	}
	for _, tc := range defaults {
		t.Run("默认/"+tc.name, func(t *testing.T) {
			if got := tc.o.Status(); got != tc.want {
				t.Fatalf("Status() = %q, want %q", got, tc.want)
			}
			if got := tc.o.StatusWith(nil); got != tc.want {
				t.Fatalf("StatusWith(nil) 应回落默认映射 %q, got %q", tc.want, got)
			}
			if got := DefaultStatusOf(tc.o); got != tc.want {
				t.Fatalf("DefaultStatusOf() = %q, want %q", got, tc.want)
			}
		})
	}

	// ② 注入宿主词表：四档各给不同的词，证明映射钩子拿到的是**原始结论**
	// （不落终态的两档不会被默认实现先吞成空串）。
	host := StatusOf(func(o Outcome) string {
		switch o {
		case OutcomeTimedOut:
			return "expired"
		case OutcomeCancelled:
			return "aborted"
		case OutcomeShutdown:
			return "unfinished"
		case OutcomeNone:
			return "running"
		default:
			return "unknown"
		}
	})
	hostWords := []struct {
		o    Outcome
		want string
	}{
		{OutcomeNone, "running"},
		{OutcomeTimedOut, "expired"},
		{OutcomeCancelled, "aborted"},
		{OutcomeShutdown, "unfinished"},
	}
	for _, tc := range hostWords {
		t.Run("注入/"+tc.o.String(), func(t *testing.T) {
			if got := tc.o.StatusWith(host); got != tc.want {
				t.Fatalf("注入映射 StatusWith() = %q, want %q", got, tc.want)
			}
			// 默认方法不受注入影响：注入是逐次参数，不是全局开关。
			if got, want := tc.o.Status(), DefaultStatusOf(tc.o); got != want {
				t.Fatalf("注入映射污染了默认 Status(): got %q, want %q", got, want)
			}
		})
	}

	// ③ 组合形态：宿主只覆盖超时档，其余仍委托 DefaultStatusOf——
	// 这是 DefaultStatusOf 导出的用途，证明两种映射可叠加而不丢默认词表。
	partial := StatusOf(func(o Outcome) string {
		if o == OutcomeTimedOut {
			return "expired"
		}
		return DefaultStatusOf(o)
	})
	if got := OutcomeTimedOut.StatusWith(partial); got != "expired" {
		t.Fatalf("组合映射应覆盖超时档, got %q", got)
	}
	if got := OutcomeCancelled.StatusWith(partial); got != contract.StatusCancelled {
		t.Fatalf("组合映射应保留默认 cancelled 词, got %q", got)
	}
	if got := OutcomeShutdown.StatusWith(partial); got != "" {
		t.Fatalf("组合映射应保留默认「不落终态」, got %q", got)
	}
}

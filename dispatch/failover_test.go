package dispatch

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/oagentkit/guardx/toolfail"
	"github.com/oekit/pkg/errors"
)

// 改派环的契约判据（issue #169）：同域 N 次 → 瞬态跨域一次 → fatal 直接终态，
// 以及两条硬纪律（判定面只读首次失败的原始错误 / 词边界）在环上的落点。

// attemptLog 记录实际执行的候选序（执行步接缝的替身）。
type attemptLog struct {
	order []string
	fail  map[string]bool // 这些候选的执行结果是失败
}

func (a *attemptLog) attempt(_ context.Context, slug string) error {
	a.order = append(a.order, slug)
	if a.fail[slug] {
		return errors.New("attempt failed: " + slug)
	}
	return nil
}

// candLog 记录候选枚举的调用（断言「同域先于跨域」「fatal 不枚举候选」）。
type candLog struct {
	calls []bool // 每次调用的 sameDomain 实参
	same  []string
	cross []string
}

func (c *candLog) candidates(slug string, tried map[string]bool, sameDomain bool) []string {
	c.calls = append(c.calls, sameDomain)
	pool := c.same
	if !sameDomain {
		pool = c.cross
	}
	var out []string
	for _, s := range pool {
		if tried[s] || s == slug {
			continue
		}
		out = append(out, s)
	}
	return out
}

// TestStickyFailoverSameDomainThenCrossDomain 同域 N 次耗尽 → 瞬态跨域兜底一次。
func TestStickyFailoverSameDomainThenCrossDomain(t *testing.T) {
	first := errors.New("upstream connect error: connection refused") // 瞬态
	attempts := &attemptLog{fail: map[string]bool{"s1": true, "s2": true, "x1": true}}
	cands := &candLog{same: []string{"s1", "s2"}, cross: []string{"x1"}}
	var events []Event

	res := StickyFailover(context.Background(), Request{
		Slug:               "origin",
		FirstErr:           first,
		SameDomain:         2,
		CrossDomain:        true,
		FailoverCandidates: cands.candidates,
		Attempt:            attempts.attempt,
		OnEvent:            func(e Event) { events = append(events, e) },
	})

	if res.Class != ClassTransient {
		t.Fatalf("档位应为 transient, got %q", res.Class)
	}
	wantOrder := []string{"s1", "s2", "x1"}
	if fmt.Sprint(attempts.order) != fmt.Sprint(wantOrder) {
		t.Fatalf("执行序应为 %v, got %v（同域两次后才跨域）", wantOrder, attempts.order)
	}
	if res.Attempts != 3 || res.Slug != "" {
		t.Fatalf("全部失败：Attempts=3 Slug=\"\", got (%d, %q)", res.Attempts, res.Slug)
	}
	if !errors.Is(res.Err, first) {
		t.Fatalf("累计错误必须仍包着首次失败的原始错误: %v", res.Err)
	}
	// 改派说明只进累计错误（给人看）。
	if s := res.Err.Error(); !containsAll(s, "已同域改派 s2 仍失败", "已跨域兜底 x1 仍失败") {
		t.Fatalf("累计错误应带两段改派说明, got %q", s)
	}
	// 枚举序：同域先问，耗尽后才问跨域。
	if fmt.Sprint(cands.calls) != fmt.Sprint([]bool{true, false}) {
		t.Fatalf("候选枚举应为（同域, 跨域）, got %v", cands.calls)
	}
	// 事件面：2 次同域 + 1 次跨域，且各带当时的累计错误。
	if len(events) != 3 || events[0].Kind != EventSameDomain ||
		events[1].Kind != EventSameDomain || events[2].Kind != EventCrossDomain {
		t.Fatalf("事件序列不符: %+v", events)
	}
}

// TestStickyFailoverTransientSucceedsAfterSameDomain 瞬态在同域内改派成功即返回。
func TestStickyFailoverTransientSucceedsAfterSameDomain(t *testing.T) {
	attempts := &attemptLog{fail: map[string]bool{"s1": true}} // s2 成功
	cands := &candLog{same: []string{"s1", "s2"}, cross: []string{"x1"}}

	res := StickyFailover(context.Background(), Request{
		Slug:               "origin",
		FirstErr:           errors.New("connection reset"),
		SameDomain:         2,
		CrossDomain:        true,
		FailoverCandidates: cands.candidates,
		Attempt:            attempts.attempt,
	})
	if res.Slug != "s2" || res.Err != nil {
		t.Fatalf("同域第二次成功: Slug=%q Err=%v", res.Slug, res.Err)
	}
	if res.Attempts != 2 {
		t.Fatalf("成功即返回，不应再问跨域: Attempts=%d", res.Attempts)
	}
	if len(cands.calls) != 1 || !cands.calls[0] {
		t.Fatalf("只应枚举过同域候选, got %v", cands.calls)
	}
}

// TestStickyFailoverFatalSkipsAllAttempts fatal 换谁都是白跑：不枚举候选、
// 不执行任何改派、原样返回首次失败。
func TestStickyFailoverFatalSkipsAllAttempts(t *testing.T) {
	first := toolfail.ClassifyAs(toolfail.ClassPermissionDenied, errors.New("grant missing"))
	attempts := &attemptLog{fail: map[string]bool{}}
	cands := &candLog{same: []string{"s1"}, cross: []string{"x1"}}
	events := 0

	res := StickyFailover(context.Background(), Request{
		Slug:               "origin",
		FirstErr:           first,
		SameDomain:         3,
		CrossDomain:        true,
		FailoverCandidates: cands.candidates,
		Attempt:            attempts.attempt,
		OnEvent:            func(Event) { events++ },
	})
	if res.Class != ClassFatal {
		t.Fatalf("档位应为 fatal, got %q", res.Class)
	}
	if res.Attempts != 0 || res.Slug != "" || !errors.Is(res.Err, first) {
		t.Fatalf("fatal 不得进入任何改派: (%d, %q, %v)", res.Attempts, res.Slug, res.Err)
	}
	if len(cands.calls) != 0 || len(attempts.order) != 0 || events != 0 {
		t.Fatalf("fatal 不得枚举候选/执行/发事件: cands=%v attempts=%v events=%d",
			cands.calls, attempts.order, events)
	}
}

// TestStickyFailoverOtherNeverCrosses other 档仅同域重试：即使开了跨域也不跨。
func TestStickyFailoverOtherNeverCrosses(t *testing.T) {
	attempts := &attemptLog{fail: map[string]bool{"s1": true, "s2": true}}
	cands := &candLog{same: []string{"s1", "s2"}, cross: []string{"x1"}}

	res := StickyFailover(context.Background(), Request{
		Slug:               "origin",
		FirstErr:           errors.New("payload mismatch on field q"), // other
		SameDomain:         2,
		CrossDomain:        true,
		FailoverCandidates: cands.candidates,
		Attempt:            attempts.attempt,
	})
	if res.Class != ClassOther {
		t.Fatalf("档位应为 other, got %q", res.Class)
	}
	if len(attempts.order) != 2 {
		t.Fatalf("同域两次照常执行, got %v", attempts.order)
	}
	if len(cands.calls) != 1 {
		t.Fatalf("other 档不得枚举跨域候选, got %v", cands.calls)
	}
}

// TestStickyFailoverSameDomainZeroAllowsCrossOnly 同域 0 次（显式关闭）+ 瞬态
// → 直接问跨域兜底。
func TestStickyFailoverSameDomainZeroAllowsCrossOnly(t *testing.T) {
	attempts := &attemptLog{fail: map[string]bool{}}
	cands := &candLog{same: []string{"s1"}, cross: []string{"x1"}}

	res := StickyFailover(context.Background(), Request{
		Slug:               "origin",
		FirstErr:           errors.New("temporary unavailable"),
		SameDomain:         0,
		CrossDomain:        true,
		FailoverCandidates: cands.candidates,
		Attempt:            attempts.attempt,
	})
	if res.Slug != "x1" || res.Attempts != 1 {
		t.Fatalf("应只做一次跨域兜底, got Slug=%q Attempts=%d", res.Slug, res.Attempts)
	}
	if fmt.Sprint(cands.calls) != fmt.Sprint([]bool{true, false}) {
		t.Fatalf("枚举序应为（同域, 跨域）, got %v", cands.calls)
	}
}

// TestStickyFailoverClassifiesFirstErrorOnly 纪律①：判定面只读首次失败的原始
// 错误，改派说明（含能把档位判反的文本）只进事件与最终消息。
//
// 反例成立的前提先钉住：污染文本**确实会翻档**（说明里带「connection」会把
// other 翻成 transient）——否则下面「环内不翻档」那条断言测不到任何东西。
// 翻档的可观测后果正是跨域兜底被错误触发（额外一次调用与配额）。
func TestStickyFailoverClassifiesFirstErrorOnly(t *testing.T) {
	first := errors.New("payload mismatch on field q") // other：仅同域，不跨域
	polluted := fmt.Errorf("%w（已同域改派 %s 仍失败）", first, "probe-connection")
	if got := Classify(first); got != ClassOther {
		t.Fatalf("首次失败应判 other, got %q", got)
	}
	if got := Classify(polluted); got != ClassTransient {
		t.Fatalf("反例前提失效：污染文本应把档位判反成 transient, got %q——"+
			"（说明词表/判定次序变了，本用例需要重估）", got)
	}

	attempts := &attemptLog{fail: map[string]bool{"probe-connection": true}}
	cands := &candLog{same: []string{"probe-connection"}, cross: []string{"x1"}}
	res := StickyFailover(context.Background(), Request{
		Slug:               "origin",
		FirstErr:           first,
		SameDomain:         1,
		CrossDomain:        true,
		FailoverCandidates: cands.candidates,
		Attempt:            attempts.attempt,
	})
	// 若环内拿累计错误重判，档位会变 transient、跨域兜底会被错误触发。
	if res.Class != ClassOther {
		t.Fatalf("环内必须按首次失败判档, got %q", res.Class)
	}
	if res.Attempts != 1 || len(cands.calls) != 1 {
		t.Fatalf("other 档不得跨域：Attempts=%d 枚举=%v（跨域被污染文本触发了）",
			res.Attempts, cands.calls)
	}
	// 但改派说明确实进了最终消息（给人看的那一面）。
	if s := res.Err.Error(); !containsAll(s, "已同域改派 probe-connection 仍失败") {
		t.Fatalf("最终消息应带改派说明, got %q", s)
	}
	// fatal 方向的污染（噪声含「连接」把 fatal 判成 transient）在环内无从发生：
	// fatal 在进环前就返回了，永远产生不出改派说明。
	fatalFirst := toolfail.ClassifyAs(toolfail.ClassPermissionDenied, errors.New("grant missing"))
	if got := Classify(fatalFirst); got != ClassFatal {
		t.Fatalf("fatal 进环前应保持 fatal, got %q", got)
	}
}

// TestStickyFailoverWithoutSeams 没注入候选/执行接缝时只给档位与原错误，
// 不 panic、不假装改派过。
func TestStickyFailoverWithoutSeams(t *testing.T) {
	first := errors.New("connection refused")
	res := StickyFailover(context.Background(), Request{Slug: "origin", FirstErr: first, SameDomain: 3, CrossDomain: true})
	if res.Slug != "" || res.Attempts != 0 || !errors.Is(res.Err, first) {
		t.Fatalf("缺接缝时应原样返回: (%q, %d, %v)", res.Slug, res.Attempts, res.Err)
	}
	// FirstErr 为 nil：没有失败可处置。
	if res = StickyFailover(context.Background(), Request{Slug: "origin"}); res.Err != nil || res.Attempts != 0 {
		t.Fatalf("nil FirstErr 不得改派: (%q, %d, %v)", res.Slug, res.Attempts, res.Err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

package retryplan

import (
	"strings"
	"testing"
	"time"

	"github.com/oagentkit/guardx/toolfail"

	"github.com/oekit/pkg/errors"
)

// TestInjectedClassifierChangesDecision 钉住 issue #240 的注入接缝：
// 分类器与 sideeffect 的 RetrySafety 同层——是宿主对「这次失败是什么」的
// 表态；内核只做组合判定。缺省（nil）分类器 = 现有 toolfail 口径，行为零变化。
func TestInjectedClassifierChangesDecision(t *testing.T) {
	plain := errors.New("宿主自有的失败类型")

	// ① 默认口径：归不出类别的失败 → 不重试。
	if d := Decide(plain, Options{}); d.Path != PathNone || d.Retryable {
		t.Fatalf("默认口径应判不可重试, got path=%v retryable=%v", d.Path, d.Retryable)
	}

	// ② 注入分类器：同一个错误判成「可重试且原样重发安全」→ 结论翻成 PathDirect，
	// 且分类器给出的四维原样带出（注入替换的是分类，不是决策字段的映射）。
	direct := Classifier(func(error) Classification {
		return Classification{
			Class:          "host-retryable",
			Retryable:      true,
			SafeToResubmit: true,
			Backoff:        true,
			RetryAfter:     2 * time.Second,
			NeedsOperator:  true,
		}
	})
	d := Decide(plain, Options{Classifier: direct})
	if d.Path != PathDirect {
		t.Fatalf("注入「可重试且重发安全」后应判原样重发, got %v（%s）", d.Path, d.Reason)
	}
	if !d.Retryable || !d.Backoff || !d.NeedsOperator || d.RetryAfter != 2*time.Second {
		t.Fatalf("分类器给出的字段应原样带出: %+v", d)
	}

	// ③ 反方向：toolfail 明确判 transient 的失败默认走幂等键重放；
	// 注入「永久不可重试」分类后结论必须变成 PathNone，且理由里带自定义类别名。
	transient := toolfail.New(toolfail.ClassTransient, "code-x", "暂时不可用")
	if got := Decide(transient, Options{}); got.Path != PathReplayed {
		t.Fatalf("默认口径下 transient 应走幂等键重放, got %v", got.Path)
	}
	permanent := Classifier(func(error) Classification {
		return Classification{Class: "host-permanent"}
	})
	d = Decide(transient, Options{Classifier: permanent})
	if d.Path != PathNone || d.Retryable {
		t.Fatalf("注入不可重试分类后应判 PathNone, got path=%v retryable=%v", d.Path, d.Retryable)
	}
	if !strings.Contains(d.Reason, "host-permanent") {
		t.Fatalf("理由应带注入分类器的类别名, got %q", d.Reason)
	}

	// ④ nil 分类器与不注入逐字一致（零值兼容）。
	for _, err := range []error{plain, transient} {
		if got, want := Decide(err, Options{Classifier: nil}), Decide(err, Options{}); got != want {
			t.Fatalf("nil 分类器必须等同于缺省路径: got %+v want %+v", got, want)
		}
	}
}

// TestInjectedClassifierDoesNotBypassIdempotencyGate 注入分类器只替换分类输入，
// 不绕过「可重试但重发不安全 + 无幂等键 ⇒ 不重试」这条组合判据——
// 否则注入面就成了绕过内核取舍的后门。
func TestInjectedClassifierDoesNotBypassIdempotencyGate(t *testing.T) {
	err := errors.New("宿主自有的失败类型")
	retryableUnsafe := Classifier(func(error) Classification {
		return Classification{Class: "host-retryable-unsafe", Retryable: true}
	})

	if d := Decide(err, Options{Classifier: retryableUnsafe}); d.Path != PathReplayed {
		t.Fatalf("有幂等键时应走幂等键重放, got %v（%s）", d.Path, d.Reason)
	}
	d := Decide(err, Options{Classifier: retryableUnsafe, WithoutIdempotency: true})
	if d.Path != PathNone {
		t.Fatalf("无幂等键兜底时不得重试（注入分类器不能绕过该判据）, got %v（%s）", d.Path, d.Reason)
	}
	if !d.Retryable {
		t.Fatal("「值得重试」的事实仍应保留")
	}
}

// TestDefaultClassifierMatchesToolfail 默认分类器与 toolfail 的既有判定逐维对齐
// （#240 之前的 Decide 行为零变化）：类别、可重试、重发安全、退避、需人工、
// 等待时长，以及「宿主逐次幂等表态优先于类别缺省」这条顺序。
func TestDefaultClassifierMatchesToolfail(t *testing.T) {
	err := toolfail.New(toolfail.ClassTransient, "code-y", "暂时不可用").
		WithRetryAfter(3 * time.Second).Idempotent()
	got := DefaultClassifier(err)
	policy := toolfail.PolicyOf(toolfail.ClassTransient)

	if got.Class != string(toolfail.ClassTransient) {
		t.Fatalf("类别 = %q, want %q", got.Class, toolfail.ClassTransient)
	}
	if got.Retryable != policy.Retryable || got.Backoff != policy.Backoff ||
		got.NeedsOperator != policy.NeedsOperator {
		t.Fatalf("策略位漂移: got %+v policy %+v", got, policy)
	}
	if !got.SafeToResubmit {
		t.Fatal("逐次 Idempotent() 表态应优先于类别缺省（transient 缺省不安全）")
	}
	if got.RetryAfter != 3*time.Second {
		t.Fatalf("等待时长 = %v, want 3s", got.RetryAfter)
	}

	// 未逐次表态时取类别缺省。
	plain := DefaultClassifier(toolfail.New(toolfail.ClassRateLimited, "code-z", "被限流"))
	if !plain.SafeToResubmit {
		t.Fatal("rate_limited 的类别缺省是重发安全")
	}
}

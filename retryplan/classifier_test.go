package retryplan

import (
	"testing"
	"time"

	"github.com/oagentkit/guardx/toolfail"
	"github.com/oekit/pkg/errors"
)

// 本文件是 Classifier 注入接缝（issue #240）的判据：
//   - 缺省（Options.Classifier=nil）= DefaultClassifier = toolfail 既有
//     判定——注入路径不得改缺省行为（存量语义逐字不变）；
//   - 注入宿主分类器 ⇒ 分类与处置位随宿主口径，路径合成（三问→Decision）
//     仍是同一份；认不出的失败按不可重试处理（保守单向失败）；
//   - 逐次表态（toolfail.As 的 SafeToResubmit / RetryAfter）住在 err 本身，
//     与分类器是谁无关——现场事实优先于类别缺省的次序不因注入而变。

// hostClassifier 宿主自有分类器（非 toolfail 词表）。
type hostClassifier struct{ verdict Verdict }

func (h hostClassifier) Classify(error) Verdict { return h.verdict }

// Test缺省仍是toolfail判定 —— nil 分类器 = DefaultClassifier：
// 超时类（可重试不可原样重发）走幂等键重放；限流类（原样重发安全）直发；
// 参数类（不可重试）终态。存量语义逐字不变。
func Test缺省仍是toolfail判定(t *testing.T) {
	// toolfail 超时失败（transient：可重试、退避、不可原样重发）。
	err := toolfail.New(toolfail.ClassTransient, "ETIMEDOUT", "调用超时")
	d := Decide(err, Options{})
	if d.Path != PathReplayed || !d.Retryable || !d.Backoff {
		t.Fatalf("缺省超时类应走幂等键重放: %+v", d)
	}
	if d.Reason != "值得重试，但原样重发不安全；须走幂等键重放以保证至多一次" {
		t.Fatalf("缺省 Reason 漂移: %q", d.Reason)
	}
	// 无幂等键 → 降级不重试（关键取舍二）。
	if d := Decide(err, Options{WithoutIdempotency: true}); d.Path != PathNone {
		t.Fatalf("无幂等键必须不重试: %+v", d)
	}
	// 缺省分类器 = DefaultClassifier（同物同判）。
	if DefaultClassifier().Classify(err) != (toolfailClassifier{}).Classify(err) {
		t.Fatalf("DefaultClassifier 与缺省路径判定不一致: %+v vs %+v",
			DefaultClassifier().Classify(err), (toolfailClassifier{}).Classify(err))
	}
}

// Test注入宿主分类器 —— issue #240 验收：宿主自有错误体系经 Classifier
// 注入，分类名与处置位随宿主；合成逻辑同一份（这一档「可重试但不安全 +
// 有幂等键」照样给 PathReplayed）。
func Test注入宿主分类器(t *testing.T) {
	hostErr := errors.New("host: 上游队列拥塞")
	c := hostClassifier{verdict: Verdict{
		Class:     "queue_congestion",
		Retryable: true,
		Backoff:   true,
		// SafeToResubmit=false：宿主口径下原样重发不安全
	}}
	d := Decide(hostErr, Options{Classifier: c})
	if d.Path != PathReplayed {
		t.Fatalf("宿主口径（可重试不可重发+有幂等键）应走 PathReplayed: %+v", d)
	}
	if !d.Retryable || !d.Backoff {
		t.Fatalf("处置位应随宿主分类器: %+v", d)
	}
	if d.Reason != "值得重试，但原样重发不安全；须走幂等键重放以保证至多一次" {
		t.Fatalf("合成文案应保持同一份: %q", d.Reason)
	}

	// 宿主判定「不可重试」⇒ 终态，Reason 带宿主类别名（第二真相不进来）。
	d = Decide(hostErr, Options{Classifier: hostClassifier{verdict: Verdict{
		Class:     "quota_exhausted",
		Retryable: false,
	}}})
	if d.Path != PathNone || d.Retryable {
		t.Fatalf("宿主判不可重试必须终态: %+v", d)
	}
	if d.Reason != "该失败不可重试：quota_exhausted" {
		t.Fatalf("Reason 应带宿主类别名: %q", d.Reason)
	}

	// 宿主判「原样重发安全」⇒ 直发（无需幂等键兜底，合成逻辑同一份）。
	d = Decide(hostErr, Options{Classifier: hostClassifier{verdict: Verdict{
		Class: "read_only_probe", Retryable: true, SafeToResubmit: true,
	}}})
	if d.Path != PathDirect {
		t.Fatalf("宿主判原样重发安全必须直发: %+v", d)
	}
}

// Test逐次表态与分类器无关 —— SafeToResubmit/RetryAfter 是「这次调用的
// 现场事实」（住在 err 上，toolfail.As），优先于任何分类器的类别缺省；
// RetryAfter 直通 Decision（分类器不解释它）。
func Test逐次表态与分类器无关(t *testing.T) {
	// err 自带「本次原样重发安全」声明 + 服务端等待时长；分类器缺省判
	// 「不安全」（transient 类）——现场表态必须赢。
	err := toolfail.New(toolfail.ClassTransient, "E429", "被限流").
		WithRetryAfter(3 * time.Second).
		WithIdempotency(toolfail.IdempotencyYes)
	c := hostClassifier{verdict: Verdict{ // 宿主分类器对同一 err 的类别缺省
		Class: "upstream_busy", Retryable: true, Backoff: true, SafeToResubmit: false,
	}}
	d := Decide(err, Options{Classifier: c})
	if d.Path != PathDirect {
		t.Fatalf("现场表态（可安全重发）应优先于类别缺省: %+v", d)
	}
	if d.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter 应从 err 直通（与分类器无关）: %+v", d)
	}
}

// Test注入分类器对同一失败各判各的 —— 同一次构建、两次调用带不同分类器
// 互不干扰（与 RetrySafety 同层的字段注入，不是包级全局）。
func Test注入分类器对同一失败各判各的(t *testing.T) {
	err := errors.New("host: 一样失败")
	retryable := Decide(err, Options{Classifier: hostClassifier{verdict: Verdict{
		Class: "a_retryable", Retryable: true,
	}}})
	fatal := Decide(err, Options{Classifier: hostClassifier{verdict: Verdict{
		Class: "b_fatal", Retryable: false,
	}}})
	if retryable.Path == fatal.Path {
		t.Fatalf("不同分类器对同一失败应有不同结论: %+v vs %+v", retryable, fatal)
	}
	// nil 分类器的第三次调用仍是 toolfail 缺省——注入不泄漏。
	d := Decide(toolfail.New(toolfail.ClassInvalidArgument, "E", "参数非法"), Options{})
	if d.Retryable || d.Path != PathNone {
		t.Fatalf("nil 分类器必须回落 toolfail 缺省: %+v", d)
	}
}

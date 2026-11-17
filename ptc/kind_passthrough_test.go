// kind_passthrough_test.go —— toolfail 分类的幂等透传判据（issue #364）。
//
// # 反向验证
//
// 去掉 kindForErrorKind 开头的 AllClasses 透传循环 ⇒ 本文件全部断言转红
// （transient / rate_limited / self_reported 等落回 unknown，可重试语义丢失）。
package ptc

import (
	"context"
	"testing"

	"github.com/oagentkit/guardx/toolfail"
)

// TestKindForErrorKindPassesThroughToolfailClasses 已是 toolfail 分类的值必须
// 幂等透传（issue #364 的核心判据）。
func TestKindForErrorKindPassesThroughToolfailClasses(t *testing.T) {
	for _, class := range toolfail.AllClasses() {
		t.Run(string(class), func(t *testing.T) {
			if got := kindForErrorKind(string(class)); got != string(class) {
				t.Fatalf("kindForErrorKind(%q)=%q，必须原样透传——"+
					"分类在最后一跳被降成 unknown 会丢失可重试语义与归因", class, got)
			}
			// 透传后策略必须不变：可重试类不得被降成不可重试。
			want := toolfail.PolicyOf(class)
			got := toolfail.PolicyOf(toolfail.Class(kindForErrorKind(string(class))))
			if got.Retryable != want.Retryable {
				t.Fatalf("class=%q 透传后 Retryable 变了：want %v, got %v",
					class, want.Retryable, got.Retryable)
			}
			if got.NeedsOperator != want.NeedsOperator {
				t.Fatalf("class=%q 透传后 NeedsOperator 变了", class)
			}
		})
	}
}

// TestRunHostToolfailClassTransientEndToEnd 端到端：宿主绑定用 toolfail 表态的
// 分类要原样到达 Result.ErrorKind，并且映射面不降级（issue #364）。
func TestRunHostToolfailClassTransientEndToEnd(t *testing.T) {
	bind := Binding{Name: "net", Call: func(context.Context, string) (string, error) {
		return "", toolfail.New(toolfail.ClassTransient, "net_down", "网络抖动")
	}}
	r := Run(context.Background(), `return host.net("x")`, []Binding{bind}, Options{Stages: allowStages()})

	if r.OK {
		t.Fatal("宿主绑定返回失败时程序应判失败")
	}
	if r.ErrorKind != string(toolfail.ClassTransient) {
		t.Fatalf("Result.ErrorKind 应透传 toolfail 分类 %q，实得 %q（Error=%q）",
			toolfail.ClassTransient, r.ErrorKind, r.Error)
	}
	if mapped := kindForErrorKind(r.ErrorKind); mapped != string(toolfail.ClassTransient) {
		t.Fatalf("跨进程映射把 %q 降成了 %q", r.ErrorKind, mapped)
	}
	if !toolfail.PolicyOf(toolfail.Class(kindForErrorKind(r.ErrorKind))).Retryable {
		t.Fatal("transient 映射后必须仍可重试（编排层退避策略依赖它）")
	}
}

// subprocess_clone_test.go —— 子进程后端不得留存调用方的绑定面（issue #398）。
//
// Bindings 是**模型可调用工具集的授权面**，Stages 是判定环：provider 按值存
// opts 只复制了切片头，底层数组仍被装配期持有——宿主事后改写
// opts.Bindings[0] 就等于换了子进程里模型能调到的工具集。
package ptc

import (
	"context"
	"testing"

	"github.com/oagentkit/guardx/invoke"
)

// TestNewSubprocessProviderClonesBindings 改写调用方的绑定清单后，provider
// 派生出的绑定名必须不变。
func TestNewSubprocessProviderClonesBindings(t *testing.T) {
	opts := SubprocessOptions{
		Program:  `return 1;`,
		Bindings: []Binding{{Name: "alpha"}},
	}
	p := NewSubprocessProvider(opts)

	opts.Bindings[0].Name = "mutated"

	got := startBindings(p.opts.Bindings)
	if len(got) != 1 {
		t.Fatalf("派生绑定条数应为 1，实得 %d", len(got))
	}
	if got[0].Name != "alpha" {
		t.Fatalf("provider 派生出的绑定名被调用方改穿：实得 %q（应为 %q）", got[0].Name, "alpha")
	}
}

// TestNewSubprocessProviderClonesStages 判定环同理：装配期那一组才是这个
// 后端被授权时的那一组。
func TestNewSubprocessProviderClonesStages(t *testing.T) {
	opts := SubprocessOptions{
		Program: `return 1;`,
		Stages:  []invoke.Stage{allowStage("装配期放行")},
	}
	p := NewSubprocessProvider(opts)

	// 调用方事后把判定环换成恒拒绝。
	opts.Stages[0] = denyStage("调用方改写", "调用方事后改写", false)

	// EvaluateStages 返回 nil = 放行（判定环拒绝时返回错误）。
	if err := invoke.EvaluateStages(context.Background(), p.opts.Stages,
		invoke.Call{Name: "alpha", Args: "{}"}); err != nil {
		t.Fatalf("provider 持有的判定环被调用方改穿：装配期那一条应仍然放行（实得 %v）", err)
	}
}

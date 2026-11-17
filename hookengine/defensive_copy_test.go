package hookengine

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// argsRecorder 记录 Runner 实际收到的参数。
type argsRecorder struct {
	mu   sync.Mutex
	args [][]string
}

func (r *argsRecorder) Run(ctx context.Context, h Hook, p Payload) (Outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = append(r.args, slices.Clone(h.Args))
	return Outcome{}, nil
}

// TestNewIsolatesHookArgsFromCaller 构造期必须是**副本**（issue #390）：
// 宿主在 New 之后改自己的原变量（同份配置模板再生成别的规则是最常见写法）
// 不得改穿活 hook——hook 是检查层，参数变了等于**检查内容变了**，Blocking hook
// 的裁决直接决定放行/拒绝。
func TestNewIsolatesHookArgsFromCaller(t *testing.T) {
	hooks := []Hook{{
		Name:     "h1",
		Event:    "pre_tool",
		Command:  "check",
		Args:     []string{"alpha", "beta"},
		Blocking: true,
		Timeout:  time.Second,
	}}
	r := &argsRecorder{}
	e, err := New(hooks, r)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 宿主改自己的原变量。
	hooks[0].Args[0] = "mutated"

	res, err := e.Dispatch(context.Background(), Payload{Event: "pre_tool"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Ran) != 1 {
		t.Fatalf("应派发一条 hook，实得 %v", res.Ran)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.args) != 1 {
		t.Fatalf("Runner 应被调用一次，实得 %d", len(r.args))
	}
	got := r.args[0]
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("活 hook 的参数被入参改穿： %+v", got)
	}
}

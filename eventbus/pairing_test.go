package eventbus

import "testing"

// TestToolCallID 配对键的唯一读取点：只认字符串 call_id；缺失/类型不符返回空串
// （空串 = 调用方未提供标识，不是「找不到对应调用」）。
func TestToolCallID(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"正常", map[string]any{"call_id": "c1", "tool": "alpha_fn"}, "c1"},
		{"nil 载荷", nil, ""},
		{"空载荷", map[string]any{}, ""},
		{"空串", map[string]any{"call_id": ""}, ""},
		{"类型不符", map[string]any{"call_id": 42}, ""},
		{"缺失", map[string]any{"tool": "alpha_fn"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToolCallID(Event{Payload: c.payload}); got != c.want {
				t.Fatalf("ToolCallID = %q, want %q", got, c.want)
			}
		})
	}
}

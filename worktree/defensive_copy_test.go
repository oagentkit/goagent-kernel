package worktree

import "testing"

// TestNewIsolatesFromCaller 构造期必须是**副本**（issue #397）：
// 宿主在 New 之后改自己的原变量不得改穿计划——只读/可写边界是**隔离声明**
// （同一份路径既只读又可写，两边的读者会得出不同结论），改穿后两边的读者
// 就会得出不同结论。
//
// 与出口方向同一把尺子（Workspaces 深拷贝交付，issue #225）。
func TestNewIsolatesFromCaller(t *testing.T) {
	ws := []Workspace{{
		ID:       "alpha",
		ReadOnly: []string{"vendor/"},
		Writable: []string{"src/"},
	}}
	p, err := New(ws)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 宿主改自己的原变量（复用同一份声明模板是最常见写法）。
	ws[0].ReadOnly[0] = "mutated"
	ws[0].Writable[0] = "mutated"

	got := p.Workspaces()[0]
	if len(got.ReadOnly) != 1 || got.ReadOnly[0] != "vendor/" {
		t.Fatalf("计划内的只读区被宿主改穿： %+v", got.ReadOnly)
	}
	if len(got.Writable) != 1 || got.Writable[0] != "src/" {
		t.Fatalf("计划内的可写区被宿主改穿： %+v", got.Writable)
	}
}

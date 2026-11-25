package controlplane

import (
	"testing"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// TestCloneTaskDoesNotAliasDeps 钉住「账面切片不外借」的契约（issue #396）。
//
// cloneTask 的注释自陈「Deps 深拷贝——账面切片不外借」，但没有用例守护：
// 去掉 slices.Clone 后整包仍绿。任务账面被改穿 ⇒ 依赖图与实际登记不一致。
//
// 两个方向都判：结果改不到源，源也改不到结果——它们本来就是同一个别名面。
func TestCloneTaskDoesNotAliasDeps(t *testing.T) {
	src := taskboard.Task{ID: "t1", Deps: []taskboard.TaskID{"t0"}}
	got := cloneTask(src)
	got.Deps[0] = "mutated-out"
	if src.Deps[0] != "t0" {
		t.Fatalf("cloneTask 的结果与入参共享 Deps 底层数组（改结果写穿源）: %v", src.Deps)
	}

	src2 := taskboard.Task{ID: "t2", Deps: []taskboard.TaskID{"t0"}}
	got2 := cloneTask(src2)
	src2.Deps[0] = "mutated-src"
	if got2.Deps[0] != "t0" {
		t.Fatalf("cloneTask 的结果与入参共享 Deps 底层数组（改源写穿结果）: %v", got2.Deps)
	}
}

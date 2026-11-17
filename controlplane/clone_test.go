// clone_test.go —— 「返回拷贝」契约的守护用例（issue #396）。
//
// 契约写着「Deps 深拷贝——账面切片不外借」，但没有任何用例钉住它：
// 把拷贝去掉，整包测试仍然全绿——恒绿的契约等于把承诺写成了散文。
//
// 用例形状：改副本 → 重读源 → 比**内容**。
package controlplane

import (
	"context"
	"testing"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestCloneTaskCopiesDeps cloneTask 必须真的拷 Deps。
//
// 反向验证：去掉 slices.Clone ⇒ 本条转红。
func TestCloneTaskCopiesDeps(t *testing.T) {
	src := taskboard.Task{ID: "t1", Deps: []taskboard.TaskID{"d1", "d2"}}
	cp := cloneTask(src)
	cp.Deps[0] = "mutated"
	if src.Deps[0] != "d1" {
		t.Fatalf("cloneTask 必须拷 Deps：源被改穿 %v", src.Deps)
	}
}

// TestSnapshotTaskDoesNotShareDeps 读面返回的快照不得与内部共享 Deps 底层
// 数组（调用方改写快照不得改到存储里的账面）。
func TestSnapshotTaskDoesNotShareDeps(t *testing.T) {
	m := NewMemStore(&MemAuditor{})
	if err := m.SeedRun("r1", "", []taskboard.Task{
		{ID: "t1", Deps: []taskboard.TaskID{"d1"}},
	}); err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	ctx := context.Background()
	snap, err := m.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	if len(snap.Task.Deps) != 1 {
		t.Fatalf("依赖未登记: %+v", snap.Task.Deps)
	}
	snap.Task.Deps[0] = "mutated"
	again, err := m.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask again: %v", err)
	}
	if again.Task.Deps[0] != "d1" {
		t.Fatalf("快照与存储共享了 Deps 底层数组：%v", again.Task.Deps)
	}
}

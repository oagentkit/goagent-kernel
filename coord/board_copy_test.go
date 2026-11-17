package coord

// board_copy_test.go —— BoardAdapter 的拷贝义务（issue #379）。
//
// taskboard 的契约写着「List / Claim 返回的 Task 不得与实现内部共享 Deps
// 底层数组」——适配器是本仓的参考实现，必须自己先过这条。

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// TestBoardAdapterCopyObligation 适配器返回的 Task 不得与板内共享 Deps
// 底层数组：调用方改写返回值不得牵动板内依赖图。
func TestBoardAdapterCopyObligation(t *testing.T) {
	b, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	boardtest.RunCopyObligation(t, b)
}

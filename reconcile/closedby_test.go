package reconcile

// closedby_test.go —— 归因词表单源（issue #391）。
//
// 本包是 closed_by 归因的**生产方**：收敛会话时写下的归因值必须与 eventbus
// 单源逐字相同——消费方（watchdog）按同一份词表判「归因在不在册」。两边各写
// 一份 ⇒ 内核自己漏写 ⇒ 合法归因被判成「无归因」（假红）。

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
)

// TestAttributionValuesAreSingleSourced 导出的四条归因值与 eventbus 单源
// 逐字相同（词表只有一份实现）。
func TestAttributionValuesAreSingleSourced(t *testing.T) {
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"ByBootReconcile", ByBootReconcile, eventbus.ClosedByBootReconcile},
		{"ByRecovery", ByRecovery, eventbus.ClosedByRecovery},
		{"ByDeadOwner", ByDeadOwner, eventbus.ClosedByDeadOwner},
		{"ByDeadLease", ByDeadLease, eventbus.ClosedByDeadLease},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q，应与 eventbus 单源 %q 逐字相同", c.name, c.got, c.want)
		}
	}
}

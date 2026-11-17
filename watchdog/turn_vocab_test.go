// turn_vocab_test.go —— 缺省归因词表与生产方 reconcile 的恒等断言（issue #391）。
//
// watchdog 是 closed_by 的**消费方**：缺省表漏一个值，合法归因就被报成
// turn_end_unattributed（假红）。断言三条：
//  1. defaultClosedBy 与 eventbus.ClosedByDefaults() 逐值相等；
//  2. reconcile 导出的全部归因值都在缺省表内；
//  3. 缺省表覆盖 watchdog 自己导出的全部 ClosedBy* 常量。
//
// 反向验证：删掉缺省表任一项（或给 reconcile 加第五个归因值而不同步）
// ⇒ 本用例转红。
package watchdog

import (
	"slices"
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/reconcile"
)

// TestDefaultClosedByAlignsWithReconcile 消费方缺省集合 ⊇ 生产方全部归因值。
func TestDefaultClosedByAlignsWithReconcile(t *testing.T) {
	if !slices.Equal(defaultClosedBy, eventbus.ClosedByDefaults()) {
		t.Fatalf("watchdog 缺省词表与 eventbus 词表不一致:\n watchdog=%v\n eventbus=%v", defaultClosedBy, eventbus.ClosedByDefaults())
	}

	produced := map[string]string{
		"ByBootReconcile": reconcile.ByBootReconcile,
		"ByRecovery":      reconcile.ByRecovery,
		"ByDeadOwner":     reconcile.ByDeadOwner,
		"ByDeadLease":     reconcile.ByDeadLease,
	}
	for name, v := range produced {
		if !slices.Contains(defaultClosedBy, v) {
			t.Errorf("reconcile 的归因 %s=%q 不在 watchdog 缺省表里：有归因的收敛会被报成 turn_end_unattributed", name, v)
		}
	}

	exported := []string{
		ClosedByLoop, ClosedByRecovery, ClosedByBootReconcile, ClosedByPanic,
		ClosedByDeadOwner, ClosedByDeadLease,
	}
	for _, v := range exported {
		if !slices.Contains(defaultClosedBy, v) {
			t.Errorf("watchdog 导出的 ClosedBy 值 %q 不在缺省表里：同包两个面自相矛盾", v)
		}
	}
}

// TestTurnBracketAcceptsReconcileAttributions 行为面：reconcile 实际发出的
// 四路归因都必须静默通过（#391 的原始复现：dead-owner / dead-lease 曾报
// turn_end_unattributed）。
//
// 反向验证：从闭环词表删掉任一项 ⇒ 对应用例转红并打出 unattributed。
func TestTurnBracketAcceptsReconcileAttributions(t *testing.T) {
	cases := []struct {
		name     string
		closedBy string
	}{
		{"boot-reconcile", reconcile.ByBootReconcile},
		{"recovery", reconcile.ByRecovery},
		{"dead-owner", reconcile.ByDeadOwner},
		{"dead-lease", reconcile.ByDeadLease},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &rec{}
			w := New(r.pub)
			w.observe(turnStarted("s1"))
			w.observe(turnEnded("s1", "succeeded", tc.closedBy))
			if n := r.count(); n != 0 {
				t.Fatalf("closed_by=%q 是 reconcile 的合法归因，不应告警: %v", tc.closedBy, reasons(r, eventbus.EventTurnBracketViolation))
			}
		})
	}
}

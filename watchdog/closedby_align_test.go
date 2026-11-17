package watchdog

// closedby_align_test.go —— closed_by 归因词表的对齐（issue #391）。
//
// 生产方是 reconcile（收敛会话时以 closed_by 写归因），消费方是本包（判归因
// 是否在册）。两份各写一份 ⇒ 内核自己漏写 ⇒ **有归因的正常收敛路径**
// （他副本死主回收）被判成「无归因」——假红（issue #391 的实测：
// recovery:dead-owner / recovery:dead-lease 两条都报 unattributed）。
//
// 修法是词表单源化（eventbus）：两包引用同一份常量，本文件把「单源」钉住。

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/reconcile"
)

// TestDefaultClosedByCoversReconcileAttributions reconcile 产出的每一个归因值
// 都必须在 watchdog 的缺省判定面内——否则合法归因被判成「无归因」。
//
// 反向验证：给 reconcile 加第 5 个归因值而不同步本包 ⇒ 本用例红。
func TestDefaultClosedByCoversReconcileAttributions(t *testing.T) {
	inTable := map[string]bool{}
	for _, v := range defaultClosedBy {
		inTable[v] = true
	}
	for _, v := range reconcile.AttributionValues() {
		if !inTable[v] {
			t.Errorf("reconcile 产出的归因 %q 不在 watchdog 缺省判定面内：合法归因被判成「无归因」（假红，issue #391）", v)
		}
	}
}

// TestReconcileAttributionsAreSingleSourced reconcile 的导出归因值与 eventbus
// 单源逐字相同（词表只有一份实现）。
func TestReconcileAttributionsAreSingleSourced(t *testing.T) {
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"ByBootReconcile", reconcile.ByBootReconcile, eventbus.ClosedByBootReconcile},
		{"ByRecovery", reconcile.ByRecovery, eventbus.ClosedByRecovery},
		{"ByDeadOwner", reconcile.ByDeadOwner, eventbus.ClosedByDeadOwner},
		{"ByDeadLease", reconcile.ByDeadLease, eventbus.ClosedByDeadLease},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q，应与 eventbus 单源 %q 逐字相同（词表只有一份实现）", c.name, c.got, c.want)
		}
	}
}

// TestDefaultClosedByMatchesEventbusVocabulary 缺省判定面恒等于单源词表：
// 词表加了值而判定面没跟上（少认一个 ⇒ 假红），或判定面多认了不在册的值
// （认错 ⇒ 漏报），都当场红。
//
// 反向验证：把本包缺省表删掉一项 ⇒ 本用例红。
func TestDefaultClosedByMatchesEventbusVocabulary(t *testing.T) {
	want := map[string]bool{}
	for _, v := range eventbus.AllClosedBy() {
		want[v] = true
	}
	got := map[string]bool{}
	for _, v := range defaultClosedBy {
		got[v] = true
	}
	for v := range want {
		if !got[v] {
			t.Errorf("单源词表里的归因 %q 不在缺省判定面内：内核少认一个 ⇒ 合法归因被判成「无归因」（issue #391）", v)
		}
	}
	for v := range got {
		if !want[v] {
			t.Errorf("缺省判定面认了不在单源词表里的归因 %q：判定面与词表漂了", v)
		}
	}
}

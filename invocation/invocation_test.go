package invocation_test

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/invocation"
)

// TestCanTransition 合法转移表逐条钉住。
//
// 这张表是账本的**判据本体**：宿主实现若与它不一致，恢复路径就会各判一套
// （各判一套 = 漂移到没人发现为止）。
func TestCanTransition(t *testing.T) {
	legal := [][2]invocation.State{
		{invocation.StatePlanned, invocation.StateApproved},
		{invocation.StatePlanned, invocation.StateExecuting},
		{invocation.StatePlanned, invocation.StateRejected},
		{invocation.StateApproved, invocation.StateExecuting},
		{invocation.StateApproved, invocation.StateRejected},
		{invocation.StateExecuting, invocation.StateApplied},
		{invocation.StateExecuting, invocation.StateUncertain},
		{invocation.StateUncertain, invocation.StateApplied},
		{invocation.StateUncertain, invocation.StateExecuting},
		{invocation.StateApplied, invocation.StateResultRecorded},
	}
	for _, p := range legal {
		if !invocation.CanTransition(p[0], p[1]) {
			t.Fatalf("合法转移被拒: %s → %s", p[0], p[1])
		}
	}

	illegal := [][2]invocation.State{
		{invocation.StatePlanned, invocation.StateApplied},
		{invocation.StatePlanned, invocation.StateResultRecorded},
		{invocation.StateApplied, invocation.StateExecuting},
		{invocation.StateExecuting, invocation.StateResultRecorded},
		{invocation.StateRejected, invocation.StateExecuting},
		{invocation.StateResultRecorded, invocation.StateApplied},
		{invocation.StatePlanned, invocation.State("bogus")},
		{invocation.State("bogus"), invocation.StateApplied},
	}
	for _, p := range illegal {
		if invocation.CanTransition(p[0], p[1]) {
			t.Fatalf("非法转移被放行: %s → %s", p[0], p[1])
		}
	}
}

// TestKnown 词表封闭：读不懂的状态不是「还没初始化」。
func TestKnown(t *testing.T) {
	for _, s := range []invocation.State{
		invocation.StatePlanned, invocation.StateApproved, invocation.StateExecuting,
		invocation.StateApplied, invocation.StateUncertain, invocation.StateRejected,
		invocation.StateResultRecorded,
	} {
		if !invocation.Known(s) {
			t.Fatalf("词表内状态被判未知: %s", s)
		}
	}
	if invocation.Known(invocation.State("bogus")) {
		t.Fatal("词表外状态被判已知")
	}
}

// TestAllStates 枚举出口与判定谓词**双向**一致（单向断言会漏掉「表里有
// 但没导出」）。
//
// 这条是「枚举出口读的就是那张表」的牙齿：出口若另抄一份清单，两边会分叉，
// 而分叉的方向恰好是「宿主看不到内核新增的取值」。
func TestAllStates(t *testing.T) {
	all := invocation.AllStates()
	if len(all) != 7 {
		t.Fatalf("状态词表应为 7 值，got %d（%v）", len(all), all)
	}
	for _, s := range all {
		if !invocation.Known(s) {
			t.Fatalf("枚举出口给出的状态 %s 不被判已知——出口与词表分叉了", s)
		}
	}
	for _, s := range []invocation.State{
		invocation.StatePlanned, invocation.StateApproved, invocation.StateExecuting,
		invocation.StateApplied, invocation.StateUncertain, invocation.StateRejected,
		invocation.StateResultRecorded,
	} {
		found := false
		for _, a := range all {
			if a == s {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("词表成员 %s 不在枚举出口里——出口漏值", s)
		}
	}
}

// TestTerminal 终态判定（走不动了的状态）。
func TestTerminal(t *testing.T) {
	for _, s := range []invocation.State{
		invocation.StateRejected, invocation.StateResultRecorded,
	} {
		if !invocation.Terminal(s) {
			t.Fatalf("状态 %s 应判终态", s)
		}
	}
	for _, s := range []invocation.State{
		invocation.StatePlanned, invocation.StateApproved, invocation.StateExecuting,
		invocation.StateApplied, invocation.StateUncertain,
	} {
		if invocation.Terminal(s) {
			t.Fatalf("状态 %s 不应判终态", s)
		}
	}
	// 词表外不许冒充终态（那是「放行」的形状）。
	if invocation.Terminal(invocation.State("bogus")) {
		t.Fatal("词表外状态被判终态")
	}
}

// TestUnfinished 未完成判定：applied 也在内（结果还没回填 ⇒ 要回填不是重做）。
func TestUnfinished(t *testing.T) {
	for _, s := range []invocation.State{
		invocation.StatePlanned, invocation.StateApproved, invocation.StateExecuting,
		invocation.StateApplied, invocation.StateUncertain,
	} {
		if !invocation.Unfinished(s) {
			t.Fatalf("状态 %s 应判未完成", s)
		}
	}
	for _, s := range []invocation.State{
		invocation.StateRejected, invocation.StateResultRecorded,
	} {
		if invocation.Unfinished(s) {
			t.Fatalf("终态 %s 不应判未完成", s)
		}
	}
}

// TestPredecessors 前驱集与转移表**逐条对账**（反向派生，不许漂）。
//
// 这条出口是存储侧原子推进的地基：宿主拿它拼「WHERE state IN (前驱集)」。
// 若它与转移表对不上，宿主那条 UPDATE 就会放行**非法**转移（或是把合法转移
// 挡掉）⇒ 判据从内核挪到了宿主。
func TestPredecessors(t *testing.T) {
	for from, nexts := range map[invocation.State][]invocation.State{
		invocation.StatePlanned:        {invocation.StateApproved, invocation.StateExecuting, invocation.StateRejected},
		invocation.StateApproved:       {invocation.StateExecuting, invocation.StateRejected},
		invocation.StateExecuting:      {invocation.StateApplied, invocation.StateUncertain},
		invocation.StateApplied:        {invocation.StateResultRecorded},
		invocation.StateUncertain:      {invocation.StateApplied, invocation.StateExecuting},
		invocation.StateRejected:       nil,
		invocation.StateResultRecorded: nil,
	} {
		for _, to := range nexts {
			found := false
			for _, p := range invocation.Predecessors(to) {
				if p == from {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("转移 %s → %s 成立，但其前驱集里没有 %s", from, to, from)
			}
			if !invocation.CanTransition(from, to) {
				t.Fatalf("转移 %s → %s 被判非法", from, to)
			}
		}
	}
	// 反向：前驱集里的每个状态都必须真的有那条边。
	for _, to := range invocation.AllStates() {
		for _, p := range invocation.Predecessors(to) {
			if !invocation.CanTransition(p, to) {
				t.Fatalf("前驱集含 %s → %s，但该转移非法", p, to)
			}
		}
	}
	// 词表外目标状态：前驱集为空（存储侧自然匹配不到行 ⇒ 不会误放行）。
	if got := invocation.Predecessors(invocation.State("bogus")); len(got) != 0 {
		t.Fatalf("词表外目标状态的前驱集应为空，got %v", got)
	}
}

// TestRecovery 恢复处置逐条钉住（这是「恢复时该怎么办」的可机判结论）。
func TestRecovery(t *testing.T) {
	cases := []struct {
		state invocation.State
		want  invocation.RecoveryAction
	}{
		{invocation.StatePlanned, invocation.ActionExecute},
		{invocation.StateApproved, invocation.ActionExecute},
		{invocation.StateExecuting, invocation.ActionReconcile},
		{invocation.StateUncertain, invocation.ActionReconcile},
		{invocation.StateApplied, invocation.ActionRecord},
		{invocation.StateRejected, invocation.ActionNone},
		{invocation.StateResultRecorded, invocation.ActionNone},
	}
	for _, c := range cases {
		got := invocation.Recovery(invocation.Entry{State: c.state})
		if got != c.want {
			t.Fatalf("状态 %s 的恢复处置应为 %s，got %s", c.state, c.want, got)
		}
	}
	// 词表外不许当「已完成」放行 ⇒ 显式对账。
	if got := invocation.Recovery(invocation.Entry{State: invocation.State("bogus")}); got != invocation.ActionReconcile {
		t.Fatalf("词表外状态的处置应为对账，got %s", got)
	}
}

// TestRecoveryActionVocabulary 处置词表封闭且出口与谓词同源。
func TestRecoveryActionVocabulary(t *testing.T) {
	all := invocation.AllRecoveryActions()
	if len(all) != 4 {
		t.Fatalf("处置词表应为 4 值，got %d（%v）", len(all), all)
	}
	for _, a := range all {
		if !invocation.KnownRecoveryAction(a) {
			t.Fatalf("枚举出口给出的处置 %s 不被判已知——出口与词表分叉了", a)
		}
	}
	for _, a := range []invocation.RecoveryAction{
		invocation.ActionExecute, invocation.ActionReconcile,
		invocation.ActionRecord, invocation.ActionNone,
	} {
		if !invocation.KnownRecoveryAction(a) {
			t.Fatalf("词表成员 %s 不被判已知", a)
		}
	}
	if invocation.KnownRecoveryAction(invocation.RecoveryAction("bogus")) {
		t.Fatal("词表外处置被判已知")
	}
}

// TestDigest 摘要确定性（同参数同摘要，不同参数不同摘要）。
func TestDigest(t *testing.T) {
	// ⚠ 两次调用先落变量再比：staticcheck SA4000 会把「比较两侧是同形
	// 表达式」判成恒等比较（它不看函数返回值）⇒ 直接比会判红。
	same1 := invocation.Digest([]byte("{\"k\":1}"))
	same2 := invocation.Digest([]byte("{\"k\":1}"))
	if same1 != same2 {
		t.Fatal("同参数摘要应相同")
	}
	if invocation.Digest([]byte("{\"k\":1}")) == invocation.Digest([]byte("{\"k\":2}")) {
		t.Fatal("不同参数摘要应不同")
	}
}

// Package invocationtest @@invocation.Store@@ 的**共享契约测试套件**。
//
// 生命周期: Experimental（v0.75.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容，升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// 宿主写完账本存储实现后跑一遍：违反哪条契约就红在哪一条，免得「各判一套、
// 漂移到没人发现为止」（与 @@sidetest@@ / @@boardtest@@ 同一范式）。
//
// # 硬失败不静默跳过
//
// 显式接入套件 = 声明要满足契约。跳过等于把「没测过」伪装成「通过了」。
//
// # 并发规模
//
// 「状态转移必须原子」这条要求在小并发下几乎撞不出来：两个执行者同时推进同
// 一条账目，串行跑永远看不出问题。套件因此固定跑**并发推进**一档（两个执行者
// 争同一条账目的不同目标状态），宿主不必调档——这条判据本身就该被撞一次。
package invocationtest

import (
	"context"
	"sync"
	"testing"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/invocation"
)

// Factory 构造被测存储实现.
//
// @seam: no-reentry no-blocking panic-caller
//
// 三条约束对工厂的含��：
//
//   - **不得重入**：工厂不得回调自身（构造期间回调 ⇒ 半成品被当成品测）；
//   - **不得阻塞**：构造须在测试时限内完成（阻塞 ⇒ 套件挂死而不是判红）；
//   - **panic 由调用方处置**：构造 panic 时由套件判定为失败。
//
// factory 每个子测试调用一次，构造一份**全新**实现（各条例句互不污染）。
type Factory func(t *testing.T) invocation.Store

// Run 跑整套契约判据。
func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("落账后可取回身份", func(t *testing.T) { runRoundTrip(t, factory) })
	t.Run("同身份重复落账幂等", func(t *testing.T) { runPlanIdempotent(t, factory) })
	t.Run("异身份重复落账被拒", func(t *testing.T) { runPlanIdentityConflict(t, factory) })
	t.Run("不存在的账目被拒", func(t *testing.T) { runGetMissing(t, factory) })
	t.Run("会话之间互不可见", func(t *testing.T) { runSessionIsolation(t, factory) })
	t.Run("列出会话账目按序号升序", func(t *testing.T) { runListOrdered(t, factory) })
	t.Run("合法转移放行", func(t *testing.T) { runLegalTransition(t, factory) })
	t.Run("跳态被拒", func(t *testing.T) { runIllegalTransition(t, factory) })
	t.Run("终态不可再转移", func(t *testing.T) { runTerminalFrozen(t, factory) })
	t.Run("推进到当前状态幂等", func(t *testing.T) { runAdvanceIdempotent(t, factory) })
	t.Run("applied 记结果可取回", func(t *testing.T) { runAppliedKeepsResult(t, factory) })
	t.Run("结果未知态经裁决后可重做", func(t *testing.T) { runUncertainCanResume(t, factory) })
	t.Run("目标状态不在词表内被拒且不改账", func(t *testing.T) { runUnknownTargetRejected(t, factory) })
	t.Run("并发推进同一条账目只有一个赢家", func(t *testing.T) { runConcurrentAdvanceSingleWinner(t, factory) })
}

// entry 造一条待落账的条目（合成数据：会话/调用/工具都是中性词）。
func entry(sessionID, callID, tool string, args string) invocation.Entry {
	return invocation.Entry{
		SessionID:  sessionID,
		CallID:     callID,
		Tool:       tool,
		ArgsDigest: invocation.Digest([]byte(args)),
		Args:       []byte(args),
		State:      invocation.StatePlanned,
	}
}

// planOne 落一条账目并断言不报错。
func planOne(t *testing.T, st invocation.Store, e invocation.Entry) {
	t.Helper()
	if err := st.Plan(context.Background(), e); err != nil {
		t.Fatalf("落账失败: %v", err)
	}
}

// runRoundTrip 落账后可取回，且身份逐字段一致。
func runRoundTrip(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	got, err := st.Get(context.Background(), e.SessionID, e.CallID)
	if err != nil {
		t.Fatalf("取回账目失败: %v", err)
	}
	if got.SessionID != e.SessionID || got.CallID != e.CallID || got.Tool != e.Tool {
		t.Fatalf("身份字段不一致: got=%+v want=%+v", got, e)
	}
	if got.ArgsDigest != e.ArgsDigest {
		t.Fatalf("参数摘要不一致: got=%q want=%q", got.ArgsDigest, e.ArgsDigest)
	}
	if string(got.Args) != string(e.Args) {
		t.Fatalf("参数本体不一致: got=%q want=%q", got.Args, e.Args)
	}
	if got.State != invocation.StatePlanned {
		t.Fatalf("初始状态应为 planned，got %q", got.State)
	}
}

// runPlanIdempotent 同身份重复落账 = 幂等无操作（恢复重放同一计划不得报错）。
func runPlanIdempotent(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)
	planOne(t, st, e)

	list, err := st.List(context.Background(), e.SessionID)
	if err != nil {
		t.Fatalf("列账目失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("重复落同一身份应只有一条账目，got %d", len(list))
	}
}

// runPlanIdentityConflict 同一 (会话, 调用) 换身份 = **另一个动作** ⇒ 拒绝。
func runPlanIdentityConflict(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	planOne(t, st, entry("sess-a", "call-1", "tool-x", "{\"k\":1}"))

	other := entry("sess-a", "call-1", "tool-x", "{\"k\":2}")
	err := st.Plan(context.Background(), other)
	if !errors.Is(err, invocation.ErrIdentityConflict) {
		t.Fatalf("异身份重复落账应判 ErrIdentityConflict，got %v", err)
	}
}

// runGetMissing 取不存在的账目 ⇒ ErrNotFound（不给零值糊弄）。
func runGetMissing(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	if _, err := st.Get(context.Background(), "sess-a", "call-none"); !errors.Is(err, invocation.ErrNotFound) {
		t.Fatalf("取不存在的账目应判 ErrNotFound，got %v", err)
	}
}

// runSessionIsolation 会话之间互不可见（同名调用在两个会话里是两条账目）。
func runSessionIsolation(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	planOne(t, st, entry("sess-a", "call-1", "tool-x", "{\"k\":1}"))
	planOne(t, st, entry("sess-b", "call-1", "tool-x", "{\"k\":1}"))

	list, err := st.List(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("列账目失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("列一个会话应只见它的账目，got %d", len(list))
	}
	for _, e := range list {
		if e.SessionID != "sess-a" {
			t.Fatalf("列出了别的会话的账目: %+v", e)
		}
	}
}

// runListOrdered 列出按序号升序（批次内的执行顺序由序号表达）。
func runListOrdered(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	for _, id := range []string{"call-1", "call-2", "call-3"} {
		planOne(t, st, entry("sess-a", id, "tool-x", "{\"k\":1}"))
	}

	list, err := st.List(context.Background(), "sess-a")
	if err != nil {
		t.Fatalf("列账目失败: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("应列出 3 条账目，got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i].Seq <= list[i-1].Seq {
			t.Fatalf("账目序号未严格递增: %d then %d", list[i-1].Seq, list[i].Seq)
		}
	}
}

// runLegalTransition 一条完整合法路径走通（planned → executing → applied →
// result_recorded）。
func runLegalTransition(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	for _, to := range []invocation.State{
		invocation.StateExecuting,
		invocation.StateApplied,
		invocation.StateResultRecorded,
	} {
		got, err := st.Advance(context.Background(), e.SessionID, e.CallID, to, invocation.Update{})
		if err != nil {
			t.Fatalf("推进到 %s 失败: %v", to, err)
		}
		if got.State != to {
			t.Fatalf("推进后状态应为 %s，got %s", to, got.State)
		}
	}
}

// runIllegalTransition 跳态被拒（planned 直接跳 applied ⇒ 非法）。
func runIllegalTransition(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	_, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateApplied, invocation.Update{})
	if !errors.Is(err, invocation.ErrIllegalTransition) {
		t.Fatalf("跳态应判 ErrIllegalTransition，got %v", err)
	}
}

// runTerminalFrozen 终态不可再转移。
func runTerminalFrozen(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateRejected, invocation.Update{}); err != nil {
		t.Fatalf("推进到 rejected 失败: %v", err)
	}
	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateExecuting, invocation.Update{}); !errors.Is(err, invocation.ErrIllegalTransition) {
		t.Fatalf("终态再推进应判 ErrIllegalTransition，got %v", err)
	}
}

// runAdvanceIdempotent 推进到当前状态 = 幂等无操作（重试不得报错）。
func runAdvanceIdempotent(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateExecuting, invocation.Update{}); err != nil {
		t.Fatalf("首次推进失败: %v", err)
	}
	got, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateExecuting, invocation.Update{})
	if err != nil {
		t.Fatalf("推进到当前状态应幂等，got %v", err)
	}
	if got.State != invocation.StateExecuting {
		t.Fatalf("状态应为 executing，got %s", got.State)
	}
}

// runAppliedKeepsResult applied 记的结果要留在账上（回填历史时要用它）。
func runAppliedKeepsResult(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateExecuting, invocation.Update{}); err != nil {
		t.Fatalf("推进到 executing 失败: %v", err)
	}
	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateApplied,
		invocation.Update{Result: []byte("ok-42")}); err != nil {
		t.Fatalf("推进到 applied 失败: %v", err)
	}

	got, err := st.Get(context.Background(), e.SessionID, e.CallID)
	if err != nil {
		t.Fatalf("取回账目失败: %v", err)
	}
	if string(got.Result) != "ok-42" {
		t.Fatalf("结果应留在账上，got %q", got.Result)
	}
}

// runConcurrentAdvanceSingleWinner 并发推进同一条账目：**恰好一个赢家**。
//
// 两个执行者抢同一条账目、推到**不同**目标状态（一个 applied、一个 uncertain）
// ⇒ 存储侧必须原子判定「当前状态允许该转移」⇒ 一个成功、一个被拒。
//
// 若实现把「读 → 判 → 写」拆成三步，两个执行者会同时读到 executing、同时判
// 通过、同时写 ⇒ 末态由**写入顺序**决定 ⇒ 同一事实两份真相。
func runConcurrentAdvanceSingleWinner(t *testing.T, factory Factory) {
	t.Helper()
	st := factory(t)
	e := entry("sess-a", "call-1", "tool-x", "{\"k\":1}")
	planOne(t, st, e)

	// 先推进到 approved：从 approved 出发，executing 与 rejected **互斥**
	//（approved→executing 之后 executing→rejected 非法；approved→rejected
	// 之后 rejected 没有出边 ⇒ executing 也非法）⇒ 谁先落地，另一个必被拒。
	//
	// ★ 为什么挑这**一对**目标：判据要能区分「原子」与「非原子」，就得挑
	//   串行顺序下也互斥的一对。若用 executing→applied / executing→uncertain，
	//   原子实现里「uncertain 先落地、applied 后落地」两个都会成功（转表
	//   允许 uncertain→applied，那是**对账裁决**路径）⇒ 「恰好一个赢家」
	//   判的不是原子性，是运气。
	if _, err := st.Advance(context.Background(), e.SessionID, e.CallID, invocation.StateApproved, invocation.Update{}); err != nil {
		t.Fatalf("推进到 approved 失败: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		succ    int
		targets = []invocation.State{invocation.StateExecuting, invocation.StateRejected}
	)
	for _, to := range targets {
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			_, err := st.Advance(context.Background(), e.SessionID, e.CallID, to, invocation.Update{})
			if err == nil {
				mu.Lock()
				succ++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if succ != 1 {
		t.Fatalf("并发推进同一条账目应恰好一个赢家，got %d", succ)
	}
}

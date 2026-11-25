// managed_identity_test.go —— 动作身份的作用域判据（issue #261）。
//
// 核心情景：任务板分租户（coord 租户作用域适配器），副作用闸不分
// （默认键只由任务 id 派生）⇒ 两个租户的同名任务互相重放/冲突。
// 验收的反向验证落成显式测试：把键构造改回「只用 TaskID」⇒ 用例必红。
package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// replayEffects 以 Key 为判据的最小重放模型：忠实模拟 sideeffect.Store 的
// Key 级判定（已完成 ⇒ Replayed 取回既有结果；同 Key 异身份 ⇒ 冲突）。
// 资源租约与执行意图是另一族判据（资源占用 / 结果未知），与身份作用域
// 无关，不建模——本文件判的是「键是谁的身份」。
type replayEffects struct {
	mu       sync.Mutex
	identity map[sideeffect.Key]sideeffect.Action
	results  map[sideeffect.Key]sideeffect.Result
	applied  []sideeffect.Action // 真正执行过的动作（判「各自都真正执行」）
	calls    []sideeffect.Action // 全部 ExecuteWith 入参（判「重试同键」）
}

func newReplayEffects() *replayEffects {
	return &replayEffects{identity: map[sideeffect.Key]sideeffect.Action{}, results: map[sideeffect.Key]sideeffect.Result{}}
}

func (f *replayEffects) ExecuteWith(_ context.Context, act sideeffect.Action, eff sideeffect.Effect) (sideeffect.Outcome, sideeffect.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, act)
	prior, done := f.identity[act.Key]
	f.mu.Unlock()

	if done {
		// 身份（Resource / 安全标记 / 载荷摘要）一致 ⇒ 重放；不一致 ⇒ 冲突
		// （存储侧 ErrActionConflict 的同款判据，判定优先于重放）。
		if prior.Resource != act.Resource ||
			prior.RetrySafety != act.RetrySafety ||
			sideeffect.HashPayload(prior.Payload) != sideeffect.HashPayload(act.Payload) {
			return sideeffect.OutcomeContended, sideeffect.Result{},
				errors.Wrap(sideeffect.ErrActionConflict, "同 Key 的动作身份与首次登记不一致")
		}
		f.mu.Lock()
		res := f.results[act.Key]
		f.mu.Unlock()
		return sideeffect.OutcomeReplayed, res, nil
	}

	res, err := eff.Apply(context.Background(), nil, sideeffect.Lease{Resource: act.Resource, Fence: 1})
	if err != nil {
		return sideeffect.OutcomeUncertain, sideeffect.Result{}, err
	}
	f.mu.Lock()
	f.identity[act.Key] = act
	f.results[act.Key] = res
	f.applied = append(f.applied, act)
	f.mu.Unlock()
	return sideeffect.OutcomeApplied, res, nil
}

func (f *replayEffects) Reconcile(_ context.Context, _ sideeffect.Action, res sideeffect.Resolution) (sideeffect.Outcome, sideeffect.Result, error) {
	return sideeffect.OutcomeReplayed, res.Result, nil
}

func (f *replayEffects) snapshot() (applied, calls []sideeffect.Action) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sideeffect.Action(nil), f.applied...), append([]sideeffect.Action(nil), f.calls...)
}

// sameWork 相同动作参数的受管任务（键/资源留给执行器默认派生——缺陷
// 就在默认派生里；需要显式身份的用例经 override 覆写）。
func sameWork(override func(*sideeffect.Action)) ManagedWork {
	act := sideeffect.Action{
		Payload:     []byte("same-params"),
		RetrySafety: sideeffect.RetrySafetyUnsafe,
	}
	if override != nil {
		override(&act)
	}
	return ManagedWork{
		Action: act,
		Effect: sideeffect.Effect{
			Apply: func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
				return sideeffect.Result{Payload: []byte("ok")}, nil
			},
		},
	}
}

// twoTenantBoard 一块租户作用域任务板，两个租户各摆一条同名任务。
func twoTenantBoard(t *testing.T) *coord.BoardAdapter {
	t.Helper()
	board, err := coord.NewTenantBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	board.Seed(
		taskboard.Task{Tenant: "t-a", ID: "deploy-01", State: taskboard.StateOpen},
		taskboard.Task{Tenant: "t-b", ID: "deploy-01", State: taskboard.StateOpen},
	)
	return board
}

// TestManagedDefaultKeyScopesByTenant
//
// 验收（issue #261）第 1 条：两个租户使用相同 TaskID、相同动作参数
// ⇒ 各自都真正执行，**无一得到 Replayed**（也不冲突）。
//
// 修复前必红：默认键 = "task:deploy-01"（无租户段）⇒ 租户 B 拿到
// OutcomeReplayed，B 的业务动作从未执行（静默串味）。
func TestManagedDefaultKeyScopesByTenant(t *testing.T) {
	ctxA := tenant.WithContext(context.Background(), tenant.Context{Tenant: "t-a"})
	ctxB := tenant.WithContext(context.Background(), tenant.Context{Tenant: "t-b"})
	board := twoTenantBoard(t)
	effects := newReplayEffects()
	exec, err := NewManagedExecutor(board, effects, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	outcomeA, errA := exec.Execute(ctxA, "deploy-01", sameWork(nil))
	outcomeB, errB := exec.Execute(ctxB, "deploy-01", sameWork(nil))

	if errA != nil {
		t.Fatalf("租户 A 应正常执行：%v", errA)
	}
	if errB != nil {
		t.Fatalf("租户 B 应正常执行（不得被 A 的记录挡下）：%v", errB)
	}
	if outcomeA != sideeffect.OutcomeApplied || outcomeB != sideeffect.OutcomeApplied {
		t.Fatalf("两个租户都必须真正执行（无一 Replayed）：a=%s b=%s", outcomeA, outcomeB)
	}
	applied, calls := effects.snapshot()
	if len(applied) != 2 {
		t.Fatalf("两个租户的动作都必须真正执行，实得 %d 次：%+v", len(applied), applied)
	}
	if len(calls) != 2 {
		t.Fatalf("应恰好两次执行调用：%+v", calls)
	}
	if calls[0].Key == calls[1].Key {
		t.Fatalf("两个租户的默认键不得相同（作用域必须进键）：%q", calls[0].Key)
	}
	if calls[0].Resource == calls[1].Resource {
		t.Fatalf("两个租户的默认资源不得相同（跨租户不得互相抢资源）：%q", calls[0].Resource)
	}
	// 任务板侧：两条同名任务各自收场（租户作用域的既有判据，钉住编排闭环）。
	for _, c := range []context.Context{ctxA, ctxB} {
		tasks, lerr := board.List(c)
		if lerr != nil {
			t.Fatal(lerr)
		}
		if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
			t.Fatalf("本租户的同名任务应收场：%+v", tasks)
		}
	}
}

// TestDefaultKeyWithoutScopeReplaysAcrossTenants
//
// 验收（issue #261）反向验证：把键构造改回「只用 TaskID」⇒ 上面的用例
// 必须红。本函数把缺陷形态**显式**钉在案——同样的两个租户、同样的动作
// 参数，只是动作键显式退回 TaskID 口径（sideeffect.Key("task:" + id)）：
// 租户 B 必然拿到 Replayed（A 的执行结果被 B 白嫖，B 的动作从未执行）。
// 本用例永绿：它证明验收用例的红/绿确实由「键是否带作用域」决定。
func TestDefaultKeyWithoutScopeReplaysAcrossTenants(t *testing.T) {
	ctxA := tenant.WithContext(context.Background(), tenant.Context{Tenant: "t-a"})
	ctxB := tenant.WithContext(context.Background(), tenant.Context{Tenant: "t-b"})
	board := twoTenantBoard(t)
	effects := newReplayEffects()
	legacy := func(act *sideeffect.Action) {
		act.Key = sideeffect.Key("task:deploy-01") // 缺陷口径：只用 TaskID
		act.Resource = "deploy-01"
	}
	exec, err := NewManagedExecutor(board, effects, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	outcomeA, errA := exec.Execute(ctxA, "deploy-01", sameWork(legacy))
	outcomeB, errB := exec.Execute(ctxB, "deploy-01", sameWork(legacy))

	if errA != nil {
		t.Fatalf("首次执行应成功：%v", errA)
	}
	if errB != nil {
		t.Fatalf("重放是正常判据，不带 error：%v", errB)
	}
	if outcomeA != sideeffect.OutcomeApplied {
		t.Fatalf("A 首次应 applied：%s", outcomeA)
	}
	if outcomeB != sideeffect.OutcomeReplayed {
		t.Fatalf("缺陷形态：只用 TaskID 的键必然让 B 重放 A 的结果，实得 %s", outcomeB)
	}
	applied, _ := effects.snapshot()
	if len(applied) != 1 {
		t.Fatalf("B 的动作从未执行（白嫖 A 的结果），实得 %d 次：%+v", len(applied), applied)
	}
}

// TestManagedDefaultKeyScopesByRun
//
// 验收（issue #261）第 2 条：同一租户的**不同 Run** 使用相同 TaskID
// ⇒ 不互相重放、不互相冲突。两个 Run 的任务拓扑互不相干（两块任务板），
// 共享同一份副作用存储（同一 replayEffects）；运行身份由宿主经
// ManagedWork.Scope 给出（词汇归宿主）。
//
// 修复前必红：默认键只有 TaskID ⇒ 第二个 Run 得到 Replayed。
func TestManagedDefaultKeyScopesByRun(t *testing.T) {
	board1 := newTestBoard(newTestClock())
	board1.Seed(taskboard.Task{ID: "deploy-01", State: taskboard.StateOpen})
	board2 := newTestBoard(newTestClock())
	board2.Seed(taskboard.Task{ID: "deploy-01", State: taskboard.StateOpen})
	effects := newReplayEffects()
	exec1, err := NewManagedExecutor(board1, effects, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	exec2, err := NewManagedExecutor(board2, effects, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	run1 := sameWork(nil)
	run1.Scope = []string{"run-1"}
	run2 := sameWork(nil)
	run2.Scope = []string{"run-2"}

	outcome1, err1 := exec1.Execute(context.Background(), "deploy-01", run1)
	outcome2, err2 := exec2.Execute(context.Background(), "deploy-01", run2)
	if err1 != nil || err2 != nil {
		t.Fatalf("两个 Run 都应正常执行：%v / %v", err1, err2)
	}
	if outcome1 != sideeffect.OutcomeApplied || outcome2 != sideeffect.OutcomeApplied {
		t.Fatalf("两个 Run 都必须真正执行（不互相重放/冲突）：run1=%s run2=%s", outcome1, outcome2)
	}
	applied, calls := effects.snapshot()
	if len(applied) != 2 {
		t.Fatalf("两个 Run 的动作都必须真正执行，实得 %d 次：%+v", len(applied), applied)
	}
	if calls[0].Key == calls[1].Key {
		t.Fatalf("不同 Run 的默认键不得相同（运行作用域必须进键）：%q", calls[0].Key)
	}
	if calls[0].Resource == calls[1].Resource {
		t.Fatalf("不同 Run 的默认资源不得相同：%q", calls[0].Resource)
	}
}

// TestManagedDefaultKeyStableAcrossRetries
//
// 验收（issue #261）第 3 条：同一 Run 内同一逻辑动作的**重试**仍命中
// 同一个键——Attempt 变化不得产生新键（入段即每次重试一个新动作）。
// 第一次施加失败（结果未知 ⇒ 释放重试），第二次成功：两次调用的键与
// 资源必须逐字一致。
func TestManagedDefaultKeyStableAcrossRetries(t *testing.T) {
	board := newTestBoard(newTestClock())
	board.Seed(taskboard.Task{ID: "deploy-01", State: taskboard.StateOpen})
	effects := newReplayEffects()
	exec, err := NewManagedExecutor(board, effects, ManagedConfig{Agent: "w-1", ClaimTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	work := sameWork(nil)
	work.Scope = []string{"run-1"}
	attempt := 0
	work.Effect.Apply = func(context.Context, any, sideeffect.Lease) (sideeffect.Result, error) {
		attempt++
		if attempt == 1 {
			return sideeffect.Result{}, errors.New("首次施加失败（可重试出口）")
		}
		return sideeffect.Result{Payload: []byte("ok")}, nil
	}

	if _, err := exec.Execute(context.Background(), "deploy-01", work); err == nil {
		t.Fatal("首次施加失败必须上抛")
	}
	outcome, err := exec.Execute(context.Background(), "deploy-01", work)
	if err != nil || outcome != sideeffect.OutcomeApplied {
		t.Fatalf("重试应收口：outcome=%s err=%v", outcome, err)
	}
	_, calls := effects.snapshot()
	if len(calls) != 2 {
		t.Fatalf("应恰好两次尝试：%+v", calls)
	}
	if calls[0].Key != calls[1].Key {
		t.Fatalf("重试同一逻辑动作必须命中同一个键（Attempt 不得进键）：%q != %q", calls[0].Key, calls[1].Key)
	}
	if calls[0].Resource != calls[1].Resource {
		t.Fatalf("重试的资源身份同样必须稳定：%q != %q", calls[0].Resource, calls[1].Resource)
	}
}

// 编译期锚：replayEffects 满足执行器依赖面。
var _ EffectRunner = (*replayEffects)(nil)

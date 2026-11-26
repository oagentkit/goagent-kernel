// storeplane_test.go —— 持久化 Plane（Store + StorePlane）的契约用例。
//
// 四组判据：跨实例可见（两个 Plane 共享同一 Store）、代次单调（并发终止
// 只有一个赢家、旧视图被拒）、介入失败无副作用（留痕失败/无留痕面 ⇒
// 状态与版本一字不动）、租户 fail-closed 与 StorePlane 装配判据。
// 并发场景走 async.GoSafe（本仓禁止裸 goroutine）。
package controlplane

import (
	"sync"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/tenant"
)

// seededStore 场景夹具：run r1 挂两个任务（t1 可认领，t2 依赖 t1），
// 一个未知的副作用键（Unsafe）。
func seededStore(t *testing.T, aud Auditor) *MemStore {
	t.Helper()
	st := NewMemStore(aud)
	err := st.SeedRun("r1", contract.StatusRunning, []taskboard.Task{
		{ID: "t1", Title: "第一个环节", State: taskboard.StateOpen},
		{ID: "t2", Title: "第二个环节", State: taskboard.StateOpen, Deps: []taskboard.TaskID{"t1"}},
	})
	if err != nil {
		t.Fatalf("SeedRun: %v", err)
	}
	if err := st.SeedEffect("effect-1", true); err != nil {
		t.Fatalf("SeedEffect: %v", err)
	}
	return st
}

// twoPlanes 同一 Store 上的两个 Plane（多副本共享同一存储的替身）。
func twoPlanes(t *testing.T, st *MemStore, aud Auditor) (*StorePlane, *StorePlane) {
	t.Helper()
	p1, err := NewStorePlane(st, aud)
	if err != nil {
		t.Fatalf("NewStorePlane: %v", err)
	}
	p2, err := NewStorePlane(st, aud)
	if err != nil {
		t.Fatalf("NewStorePlane: %v", err)
	}
	return p1, p2
}

// TestStorePlaneCrossInstanceVisibility 钉「A 的介入 B 看得见」：共享同一
// Store 的两个 Plane，强释/终止/裁决的结果跨实例可见，留痕也在同一账上。
func TestStorePlaneCrossInstanceVisibility(t *testing.T) {
	aud := &MemAuditor{}
	st := seededStore(t, aud)
	p1, p2 := twoPlanes(t, st, aud)
	ctx := tenantCtx()

	held, err := st.Claim(ctx, "t1", "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// A 强制释放；B 的视图立即看到（旧令牌同时作废）。
	if err := p1.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op-1", Basis: "持有者失联"}); err != nil {
		t.Fatalf("p1.ForceRelease: %v", err)
	}
	tv, err := p2.TaskView(ctx, "t1")
	if err != nil {
		t.Fatalf("p2.TaskView: %v", err)
	}
	if tv.Task.State != taskboard.StateOpen || tv.Task.Owner != "" || !tv.Task.Lease.IsZero() {
		t.Fatalf("跨实例视图未看到强释结果： %+v", tv.Task)
	}
	if err := st.Complete(ctx, "t1", held.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("旧令牌迟到提交必须 ErrLeaseLost（实得 %v）", err)
	}

	// A 裁决未知结果；B 再裁同一键得到「先到者为准」。
	if err := p1.Adjudicate(ctx, Adjudication{EffectKey: "effect-1", Verdict: VerdictApplied, Operator: "op-1", Basis: "下游确认已写入"}); err != nil {
		t.Fatalf("p1.Adjudicate: %v", err)
	}
	err = p2.Adjudicate(ctx, Adjudication{EffectKey: "effect-1", Verdict: VerdictNotApplied, Operator: "op-2", Basis: "另一次查询"})
	if !errors.Is(err, sideeffect.ErrNotUncertain) {
		t.Fatalf("跨实例再裁必须 ErrNotUncertain（实得 %v）", err)
	}

	// A 终止运行；B 的视图看到状态与代次前进。
	view, err := p1.RunView(ctx, "r1")
	if err != nil {
		t.Fatalf("p1.RunView: %v", err)
	}
	if err := p1.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: view.Gen, Operator: "op-1", Basis: "超时未产出"}); err != nil {
		t.Fatalf("p1.Terminate: %v", err)
	}
	rv, err := p2.RunView(ctx, "r1")
	if err != nil {
		t.Fatalf("p2.RunView: %v", err)
	}
	if rv.Status != contract.StatusCancelled || rv.Gen != view.Gen+1 {
		t.Fatalf("跨实例视图未看到终止与代次前进： status=%s gen=%d", rv.Status, rv.Gen)
	}

	// 留痕同一账：三类介入（含被拒的再裁）都有记录。
	entries, err := aud.Entries(ctx, "")
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	actions := map[Action]int{}
	for _, e := range entries {
		actions[e.Action]++
	}
	if actions[ActionForceRelease] == 0 || actions[ActionAdjudicate] < 2 || actions[ActionTerminate] == 0 {
		t.Fatalf("留痕账不完整： %v（entries=%+v）", actions, entries)
	}
}

// TestStorePlaneStaleGenerationRejected 钉代次单调：终止推进代次后，
// 另一实例拿旧视图终止必须 ErrStaleRun；终态再终止 ErrTerminalRun。
func TestStorePlaneStaleGenerationRejected(t *testing.T) {
	aud := &MemAuditor{}
	st := seededStore(t, aud)
	p1, p2 := twoPlanes(t, st, aud)
	ctx := tenantCtx()

	view, err := p2.RunView(ctx, "r1")
	if err != nil {
		t.Fatalf("RunView: %v", err)
	}
	if err := p1.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: view.Gen, Operator: "op-1", Basis: "b"}); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	// 后到者凭同一份旧视图终止 → ErrStaleRun（不覆盖先到者）。
	err = p2.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: view.Gen, Operator: "op-2", Basis: "另一个介入者"})
	if !errors.Is(err, ErrStaleRun) {
		t.Fatalf("旧视图终止必须 ErrStaleRun（实得 %v）", err)
	}
	// 读最新代次后再终止：运行已终态 → ErrTerminalRun。
	cur, err := p2.RunView(ctx, "r1")
	if err != nil {
		t.Fatalf("RunView after: %v", err)
	}
	if cur.Gen != view.Gen+1 {
		t.Fatalf("代次未单调前进： %d -> %d", view.Gen, cur.Gen)
	}
	err = p2.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: cur.Gen, Operator: "op-2", Basis: "重试"})
	if !errors.Is(err, ErrTerminalRun) {
		t.Fatalf("终态再终止必须 ErrTerminalRun（实得 %v）", err)
	}
}

// TestStorePlaneConcurrentInterventionSingleWinner 钉跨实例并发介入的
// 串行化：两个实例同时终止同一运行，恰好一个成功、另一个凭代次被拒；
// 两个实例同时强释同一任务，恰好一个成功、另一个看到「已不在认领态」。
func TestStorePlaneConcurrentInterventionSingleWinner(t *testing.T) {
	aud := &MemAuditor{}
	st := seededStore(t, aud)
	p1, p2 := twoPlanes(t, st, aud)
	ctx := tenantCtx()

	if _, err := st.Claim(ctx, "t1", "worker-a", time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	view, err := p1.RunView(ctx, "r1")
	if err != nil {
		t.Fatalf("RunView: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []error
		start   = make(chan struct{})
	)
	run := func(fn func() error) {
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			err := fn()
			mu.Lock()
			results = append(results, err)
			mu.Unlock()
		})
	}
	run(func() error {
		return p1.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: view.Gen, Operator: "op-1", Basis: "b"})
	})
	run(func() error {
		return p2.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: view.Gen, Operator: "op-2", Basis: "b"})
	})
	close(start)
	wg.Wait()

	success, stale := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrStaleRun):
			stale++
		default:
			t.Fatalf("并发终止出现意外错误： %v", err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("并发终止应恰好一胜一败： success=%d stale=%d", success, stale)
	}

	// 并发强释同一任务：一胜，另一看到「已不在认领态」。
	results = nil
	start = make(chan struct{})
	run(func() error {
		return p1.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op-1", Basis: "b"})
	})
	run(func() error {
		return p2.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op-2", Basis: "b"})
	})
	close(start)
	wg.Wait()

	success, lost := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, taskboard.ErrLeaseLost):
			lost++
		default:
			t.Fatalf("并发强释出现意外错误： %v", err)
		}
	}
	if success != 1 || lost != 1 {
		t.Fatalf("并发强释应恰好一胜一败： success=%d lost=%d", success, lost)
	}
}

// TestStorePlaneAuditFailureNoSideEffects 钉「留痕失败即整体失败」：三类
// 介入都返回错误，且 Store 的版本/状态/代次/裁决账一字不动（issue #180）。
func TestStorePlaneAuditFailureNoSideEffects(t *testing.T) {
	broken := brokenAuditor{}
	st := seededStore(t, broken)
	p, err := NewStorePlane(st, broken)
	if err != nil {
		t.Fatalf("NewStorePlane: %v", err)
	}
	ctx := tenantCtx()

	if _, err := st.Claim(ctx, "t1", "worker-a", time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	run0, err := st.SnapshotRun(ctx, "r1")
	if err != nil {
		t.Fatalf("SnapshotRun: %v", err)
	}
	task0, err := st.SnapshotTask(ctx, "t1")
	if err != nil {
		t.Fatalf("SnapshotTask: %v", err)
	}
	eff0, err := st.SnapshotEffect(ctx, "effect-1")
	if err != nil {
		t.Fatalf("SnapshotEffect: %v", err)
	}

	cases := []struct {
		name string
		call func() error
	}{
		{"Terminate", func() error {
			return p.Terminate(ctx, Termination{RunID: "r1", ExpectedGen: run0.Gen, Operator: "op-1", Basis: "b"})
		}},
		{"ForceRelease", func() error {
			return p.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op-1", Basis: "b"})
		}},
		{"Adjudicate", func() error {
			return p.Adjudicate(ctx, Adjudication{EffectKey: "effect-1", Verdict: VerdictApplied, Operator: "op-1", Basis: "b"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("留痕失败时介入必须整体失败")
			}
			run, err := st.SnapshotRun(ctx, "r1")
			if err != nil {
				t.Fatalf("SnapshotRun after: %v", err)
			}
			task, err := st.SnapshotTask(ctx, "t1")
			if err != nil {
				t.Fatalf("SnapshotTask after: %v", err)
			}
			eff, err := st.SnapshotEffect(ctx, "effect-1")
			if err != nil {
				t.Fatalf("SnapshotEffect after: %v", err)
			}
			if run.Version != run0.Version || run.Status != run0.Status || run.Gen != run0.Gen {
				t.Fatalf("失败的介入改了运行快照： %+v -> %+v", run0, run)
			}
			if task.Version != task0.Version || task.Task.State != task0.Task.State ||
				task.Task.Owner != task0.Task.Owner || task.Task.Seq != task0.Task.Seq ||
				task.Task.Lease.Token != task0.Task.Lease.Token {
				t.Fatalf("失败的介入改了任务快照： %+v -> %+v", task0.Task, task.Task)
			}
			if eff.Version != eff0.Version || eff.Effect != eff0.Effect {
				t.Fatalf("失败的介入改了裁决账： %+v -> %+v", eff0.Effect, eff.Effect)
			}
		})
	}
}

// TestStorePlaneNoAuditor 留痕面缺位：查询可用，介入一律 ErrNoAuditor
// （fail-closed），且不改状态。
func TestStorePlaneNoAuditor(t *testing.T) {
	st := seededStore(t, nil)
	p, err := NewStorePlane(st, nil)
	if err != nil {
		t.Fatalf("NewStorePlane: %v", err)
	}
	ctx := tenantCtx()
	if _, err := p.TaskView(ctx, "t1"); err != nil {
		t.Fatalf("无留痕面时查询应可用（实得 %v）", err)
	}
	if err := p.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op", Basis: "b"}); !errors.Is(err, ErrNoAuditor) {
		t.Fatalf("无留痕面时强释必须 ErrNoAuditor（实得 %v）", err)
	}
	tv, err := p.TaskView(ctx, "t1")
	if err != nil {
		t.Fatalf("TaskView: %v", err)
	}
	if tv.Task.State != taskboard.StateOpen || !tv.Task.Lease.IsZero() {
		t.Fatalf("被拒的介入不得改状态： %+v", tv.Task)
	}
}

// TestStorePlaneTenantFailClosed 读/写共用租户闸（issue #197）：未注入
// 租户上下文一律 ErrNoContext；AllowAnonymousRead 只放开读。
func TestStorePlaneTenantFailClosed(t *testing.T) {
	aud := &MemAuditor{}
	st := seededStore(t, aud)
	p, err := NewStorePlane(st, aud)
	if err != nil {
		t.Fatalf("NewStorePlane: %v", err)
	}
	if _, err := p.TaskView(emptyCtx(), "t1"); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("未注入租户的读必须 ErrNoContext（实得 %v）", err)
	}
	if err := p.ForceRelease(emptyCtx(), ForceRelease{TaskID: "t1", Operator: "op", Basis: "b"}); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("未注入租户的写必须 ErrNoContext（实得 %v）", err)
	}

	anon, err := NewStorePlane(st, aud, AllowAnonymousRead(true))
	if err != nil {
		t.Fatalf("NewStorePlane anon: %v", err)
	}
	if _, err := anon.TaskView(emptyCtx(), "t1"); err != nil {
		t.Fatalf("显式匿名读应放行（实得 %v）", err)
	}
	if err := anon.ForceRelease(emptyCtx(), ForceRelease{TaskID: "t1", Operator: "op", Basis: "b"}); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("匿名开关不得放开写路径（实得 %v）", err)
	}
}

// TestNewStorePlaneRequiresStore 装配判据：缺 Store 显式失败（不静默退化
// 成进程内状态）。
func TestNewStorePlaneRequiresStore(t *testing.T) {
	if _, err := NewStorePlane(nil, &MemAuditor{}); !errors.Is(err, ErrNoStore) {
		t.Fatalf("缺 Store 必须 ErrNoStore（实得 %v）", err)
	}
}

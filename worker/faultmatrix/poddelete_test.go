// poddelete_test.go —— Pod 强制删除与副作用对账（矩阵行六，Cluster）。
package faultmatrix

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/sideeffect"
)

// runPodForceDeleteReconcile 旧 Pod 抢到闸、对外部世界施加了副作用，然后
// 在记账之前被强删（没有 Complete / Release / 优雅退出）。判据：
//
//   - 新 Pod 的同一逻辑动作**不得盲目重做**：过期执行意图 ⇒ OutcomeUncertain；
//   - 外部事实查询确认副作用已存在后，裁决为「已生效」⇒ 之后只重放结果；
//   - 外部副作用计数保持 1（强删 + 重启不得变成双份）。
//
// 反面形态（把过期意图当「没做过」重新授予执行权）在第二次 Execute 处必红。
func runPodForceDeleteReconcile(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	store, err := coord.NewStoreAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatalf("装配副作用存储失败: %v", err)
	}
	store.UseClock(clock.Now)

	act := sideeffect.Action{
		Key:      sideeffect.Key("pod-force-delete:apply"),
		Resource: "resource-1",
		// 未声明可安全重试：过期后只能先对账，不能直接重做。
		RetrySafety: sideeffect.RetrySafetyUnknown,
	}

	// ── 旧 Pod：抢到闸、施加副作用、然后死（不记账）。 ──
	outcome, _, _, err := store.Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatalf("旧 Pod 抢闸失败: %v", err)
	}
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("旧 Pod 应获得执行权（实得 %v）", outcome)
	}
	applied := 1 // 外部世界的事实：副作用已生效

	clock.Advance(2 * time.Minute) // 租约过期；旧 Pod 再也不会回来

	// ── 新 Pod：同一 Key 的尝试结果未知，不得重做。 ──
	runner := sideeffect.NewRunner(store, time.Minute)
	outcome, _, err = runner.Execute(ctx, act, func(context.Context, sideeffect.Lease) (sideeffect.Result, error) {
		t.Fatal("强删后重启不得盲目重做——过期执行意图必须先对账")
		return sideeffect.Result{}, nil
	})
	if outcome != sideeffect.OutcomeUncertain || !errors.Is(err, sideeffect.ErrOutcomeUnknown) {
		t.Fatalf("过期未裁决的尝试必须判为结果未知（实得 outcome=%v err=%v）", outcome, err)
	}
	if applied != 1 {
		t.Fatalf("对账前外部副作用计数不得变化（实得 %d）", applied)
	}

	// ── 对账：外部事实「已生效」⇒ 裁决为已完成，记录既有结果。 ──
	outcome, res, err := runner.Reconcile(ctx, act, sideeffect.Resolution{
		Applied: true,
		Result:  sideeffect.Result{Payload: []byte("already-applied")},
		Reason:  "外部事实查询：目标资源已存在本次动作的产物",
	})
	if err != nil {
		t.Fatalf("对账裁决失败: %v", err)
	}
	if outcome != sideeffect.OutcomeReplayed {
		t.Fatalf("确认已生效后应返回重放结论（实得 %v）", outcome)
	}
	if string(res.Payload) != "already-applied" {
		t.Fatalf("裁决必须留存既有结果（实得 %q）", string(res.Payload))
	}

	// ── 再次执行：取回结果，不再施加。 ──
	outcome, res, err = runner.Execute(ctx, act, func(context.Context, sideeffect.Lease) (sideeffect.Result, error) {
		t.Fatal("裁决完成后同一 Key 必须重放结果，不得再次施加")
		return sideeffect.Result{}, nil
	})
	if err != nil || outcome != sideeffect.OutcomeReplayed {
		t.Fatalf("裁决后应重放（实得 outcome=%v err=%v）", outcome, err)
	}
	if string(res.Payload) != "already-applied" {
		t.Fatalf("重放必须取回裁决留存的结果（实得 %q）", string(res.Payload))
	}
	if applied != 1 {
		t.Fatalf("强删 + 重启 + 对账后外部副作用必须恰好一次（实得 %d）", applied)
	}
}

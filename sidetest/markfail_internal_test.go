package sidetest

import (
	"context"
	"testing"
	"time"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// 本文件是「标记失败」一族的**负对照**：证明这个场景真的有分辨力。
//
// brokenMarkStore 模拟「授予执行权却不留意图」的坏实现：Acquire 照常发放
// 令牌，但**不登记**执行意图（MarkUncertain 也照常不写未知态、返回成功）。
// 经故障注入装饰器替换后，调用方看到「标记写失败」，而存储侧此刻既没有
// 「完成」、没有「未知」、也没有挡着重试的意图——下一个 worker 直接重放。
// 这正是契约第 5 条要封的形态：标记写失败时存储侧必须还剩执行意图，
// 这个实现什么都没剩，场景必须判它红。
//
// （issue #224 之后注入点在宿主写入**之前**：装饰器对被注入的调用什么都不
// 写，宿主的 MarkUncertain 不再被调用——负对照的坏点随之挪到 Acquire 的
// 意图登记上，那正是新判据支点（contended）唯一可能的来源。）
type brokenMarkStore struct {
	sideeffect.Store

	fence sideeffect.Fence
}

func newBrokenMarkStore() *brokenMarkStore {
	return &brokenMarkStore{}
}

func (b *brokenMarkStore) Acquire(_ context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Outcome, sideeffect.Lease, sideeffect.Result, error) {
	// 坏就坏在这里：授予执行权，却**不登记**执行意图——存储侧不留任何
	// 挡重试的东西。
	b.fence++
	lease := sideeffect.Lease{Resource: act.Resource, Fence: b.fence, Holder: "broken", TTL: ttl}
	return sideeffect.OutcomeApplied, lease, sideeffect.Result{}, nil
}

func (b *brokenMarkStore) MarkUncertain(context.Context, sideeffect.Action, sideeffect.Lease, string) error {
	return nil // 未知标记也不写：失败形态下存储侧干干净净
}

func (b *brokenMarkStore) Release(context.Context, sideeffect.Action, sideeffect.Lease) error {
	return nil
}

// TestMarkFailureScenarioRejectsBrokenStore 负对照：失败路径上存储侧既没有
// 未知标记、也没有挡着的执行意图的实现，必须被场景判红。
func TestMarkFailureScenarioRejectsBrokenStore(t *testing.T) {
	broken := newBrokenMarkStore()

	if err := checkMarkFailureBlocksAcquire(broken); err == nil {
		t.Fatal("坏实现（授予执行权不留意图、标记也不写）应被场景判红，套件却判它合规")
	}
}

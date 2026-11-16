package pii

// #296 判据先于实现：并发换策略下，订阅者观察到的代次**单调不回退**。
//
// issue #296 的待坐实项：`Add → 读 CurrentPolicy → [写侧 CAS+notify] → Invoke`
// 的窗口里，并发写侧的变更通知与订阅注册时的立即回调可能交错，订阅者是否会把
// 「更新的一代」收在「更旧的一代」之前。本文件把这条判据钉成压测：
//
//	订阅者回调先做一段真实的派生工作（sleep 模拟重建视图）再记录快照——
//	记录顺序即订阅者的观察顺序；
//	多位写侧并发换策略；
//	断言观察序列**严格递增**（乱序旧帧不得进入观察序列）。
//
// 修复前的实现（注册表直接裸调回调）下本用例可稳定复现回退：写侧 A 的
// Invoke(gen2) 还在回调里做派生工作时，写侧 B 的 Invoke(gen3) 已并发进入，
// B 先记录 ⇒ 观察到 gen3 → gen2 的回退。

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowRecorder 模拟真实订阅者：先重建派生视图（sleep），再落一份快照记录。
// 记录在锁内完成，顺序即观察顺序（配 -race 有效）。
type slowRecorder struct {
	mu    sync.Mutex
	gens  []uint64
	delay time.Duration
}

func (r *slowRecorder) fn(p Policy) {
	time.Sleep(r.delay) // 派生视图重建：一次投递未返回时，下一帧不得并发进入
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gens = append(r.gens, p.gen)
}

func (r *slowRecorder) all() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint64(nil), r.gens...)
}

// TestSubscribeGenMonotonicUnderConcurrentWrites 并发换策略压测：
// 订阅者观察到的代次单调不回退，且最终必见到最新一代（不漏）。
func TestSubscribeGenMonotonicUnderConcurrentWrites(t *testing.T) {
	restore(t)

	var r slowRecorder
	r.delay = 50 * time.Microsecond
	h, err := Subscribe(r.fn)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, h)

	const writers = 3
	const writesPerWriter = 300
	strategies := []StrategyKind{StrategyMask, StrategyRedact, StrategyBlock, StrategyHash}

	var wg sync.WaitGroup
	var done atomic.Bool
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter && !done.Load(); i++ {
				_ = SetStrategy(strategies[(w+i)%len(strategies)])
			}
		}(w)
	}
	wg.Wait()
	done.Store(true)

	// 收尾一次变更：写侧全部结束后，订阅者必须见到这一代（不漏）。
	if err := SetStrategy(StrategyMask); err != nil {
		t.Fatal(err)
	}

	gens := r.all()
	if len(gens) < 2 {
		t.Fatalf("压测应收到多帧快照，实得 %d 帧", len(gens))
	}
	last := uint64(0)
	for i, g := range gens {
		if g <= last {
			t.Fatalf("第 %d 份快照的代次回退：%d → %d（订阅者把更新的一代收在了更旧一代之前）",
				i, last, g)
		}
		last = g
	}
	if final := r.all()[len(gens)-1]; final != Gen() {
		t.Fatalf("订阅者最终应见到最新一代：快照 %d，全局 %d（有变更帧被漏掉）", final, Gen())
	}
}

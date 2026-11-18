// effect_test.go —— 副作用面的契约判据。
//
//   - sidetest.Run：副作用闸共享契约套件（33 条），与 pgstore 同一段判据；
//   - 本文件的手写用例：落库形态特有的持久化、租户作用域、跨实例恢复。
package sqlitestore

import (
	"context"
	"testing"
	"time"

	oerrors "github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/sidetest"
)

// TestSideEffectContract 跑副作用闸共享契约套件（与 pgstore 同一段判据）。
//
// 套件是**硬失败**语义：实现缺一项能力（比如 OwnedResolveStore）即红，
// 不静默跳过——跳过等于把「没测过」伪装成「通过了」。
func TestSideEffectContract(t *testing.T) {
	sidetest.Run(t, func(t *testing.T, clock *sidetest.FakeClock) sidetest.ClockedStore {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s.Effects()
	})
}

// effAct 构造一条测试动作（合成数据，不含任何领域词面）。
func effAct(key, resource string) sideeffect.Action {
	return sideeffect.Action{Key: sideeffect.Key(key), Resource: resource, Payload: []byte("p")}
}

// TestEffectPersistsAcrossInstances 重启后副作用闸状态还在（令牌、意图、
// 栅栏水位都是持久化事实，不是进程内缓存）。
func TestEffectPersistsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s1.Close() })
	act := effAct("k-persist", "res-persist")
	outcome, lease, _, err := s1.Effects().Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("首次 Acquire 应授予执行权，实得 %v", outcome)
	}

	// 第二个实例（模拟重启后的新进程）重放日志。
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	// 未裁决的意图必须还在 ⇒ 同一 Key 不得被重新授予执行权。
	outcome, _, _, err = s2.Effects().Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeContended {
		t.Fatalf("重启后未裁决的意图必须仍挡住重授，实得 %v", outcome)
	}
	// 旧持有者的凭据仍然有效（凭据是持久化事实）。
	if _, err := s2.Effects().Complete(ctx, act, lease, sideeffect.Result{Payload: []byte("done")}); err != nil {
		t.Fatalf("重启后原持有者应仍能完成：%v", err)
	}
	// 完成后重放既有结果。
	outcome, _, result, err := s2.Effects().Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeReplayed || string(result.Payload) != "done" {
		t.Fatalf("完成后必须重放既有结果：outcome=%v result=%q", outcome, string(result.Payload))
	}
}

// TestEffectTenantScope 租户是身份第二维：跨租户同名动作互不命中。
func TestEffectTenantScope(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ev := s.Effects()

	a := effAct("same-key", "same-res")
	a.Tenant = "tenant-a"
	b := effAct("same-key", "same-res")
	b.Tenant = "tenant-b"

	if _, _, _, err := ev.Acquire(ctx, a, time.Minute); err != nil {
		t.Fatal(err)
	}
	// 另一个租户的同名动作既不受「已完成」影响，也不受「资源被占」影响。
	outcome, _, _, err := ev.Acquire(ctx, b, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("跨租户同名动作不得互相命中，实得 %v", outcome)
	}
}

// TestEffectStaleHolderRejected 过期持有者的迟到写入被拒（栅栏 + 持有者
// 身份三项联查）。
func TestEffectStaleHolderRejected(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ev := s.Effects()
	act := effAct("k-stale", "res-stale")

	_, lease, _, err := ev.Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// 释放后令牌**没有变**（栅栏只在接管时递增）⇒ 只比令牌会漏掉这一条。
	if err := ev.Release(ctx, act, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Complete(ctx, act, lease, sideeffect.Result{}); !oerrors.Is(err, sideeffect.ErrFenceStale) {
		t.Fatalf("释放后的租约不得再记账，实得 %v", err)
	}
	// 拿别人的令牌冒充持有者（持有者身份不是本次授予铸造的那份）。
	//
	// 换一个键：上一个键的**执行意图还在**（Release 只放闸、不撤销意图）⇒
	// 同一个键不得被重新授予执行权，夹具必须换键而不是换租约。
	other := effAct("k-forge", "res-forge")
	_, lease2, _, err := ev.Acquire(ctx, other, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	forged := lease2
	forged.Holder = "someone-else"
	if _, err := ev.Complete(ctx, other, forged, sideeffect.Result{}); !oerrors.Is(err, sideeffect.ErrNotHolder) {
		t.Fatalf("持有者不匹配必须被拒（ErrNotHolder），实得 %v", err)
	}
	// A 资源的租约用于 B 资源 ⇒ ErrResourceMismatch。
	cross := lease2
	cross.Resource = "res-other"
	if _, err := ev.Complete(ctx, other, cross, sideeffect.Result{}); !oerrors.Is(err, sideeffect.ErrResourceMismatch) {
		t.Fatalf("跨资源凭据必须被拒（ErrResourceMismatch），实得 %v", err)
	}
}

package sideeffect

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// 本文件钉住 kernel#533：施加段被取消时，失败路径的收尾调用（Abandon /
// MarkUncertain / Release）必须仍送达、且送达的 ctx 不带取消——收尾的语义
// 是「无论本次操作成败，把账收干净」，它不该随操作一起死；否则意图撤销与
// 闸释放静默失败，接管被抬到 SideEffectTTL（宿主 Close/停机封口是常态路径）。
//
// fakeStore 是不模拟传输层的内存替身（方法一律忽略 ctx）⇒ 这里用
// ctxGuardStore 包一层模拟**真实存储**的行为：ctx 已取消则拒收。不修
// runner 时收尾撞已取消的 ctx 被拒 ⇒ 计数为 0，断言当场红（变异反证做过）。

// ctxGuardStore 拒收已取消 ctx 的 Store 包装，并记录三个收尾方法送达的 ctx。
type ctxGuardStore struct {
	Store
	self *fakeStore

	mu               sync.Mutex
	abandonCtx       context.Context
	releaseCtx       context.Context
	markUncertainCtx context.Context
}

func newCtxGuardStore() (*ctxGuardStore, *fakeStore) {
	f := newFakeStore()
	return &ctxGuardStore{Store: f, self: f}, f
}

// guard 拒收已取消的 ctx（真实存储如此），通过则记录送达的 ctx。
func (g *ctxGuardStore) guard(ctx context.Context, dst *context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	*dst = ctx
	return nil
}

func (g *ctxGuardStore) Abandon(ctx context.Context, act Action, lease Lease, reason string) error {
	if err := g.guard(ctx, &g.abandonCtx); err != nil {
		return err
	}
	return g.Store.Abandon(ctx, act, lease, reason)
}

func (g *ctxGuardStore) Release(ctx context.Context, act Action, lease Lease) error {
	if err := g.guard(ctx, &g.releaseCtx); err != nil {
		return err
	}
	return g.Store.Release(ctx, act, lease)
}

func (g *ctxGuardStore) MarkUncertain(ctx context.Context, act Action, lease Lease, reason string) error {
	if err := g.guard(ctx, &g.markUncertainCtx); err != nil {
		return err
	}
	return g.Store.MarkUncertain(ctx, act, lease, reason)
}

func TestFailureCleanupSurvivesCanceledContext(t *testing.T) {
	type ctxKey string
	const k ctxKey = "kernel533"

	t.Run("Safe 档施加失败：Abandon 脱离操作 ctx", func(t *testing.T) {
		st, fs := newCtxGuardStore()
		r := NewRunner(st, 30*time.Second)
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), k, "v"))
		defer cancel()
		a := act("k533-safe", "res-533-safe")
		a.RetrySafety = RetrySafetySafe

		_, _, err := r.Execute(ctx, a, func(context.Context, Lease) (Result, error) {
			cancel() // 宿主在施加段封口（Close/停机在途）
			return Result{}, errors.New("apply canceled")
		})
		if err == nil {
			t.Fatal("施加失败应透出错误")
		}
		if fs.abandonCalls != 1 {
			t.Fatalf("Abandon 应送达 1 次（收尾不得因 ctx 取消而静默失败），实得 %d", fs.abandonCalls)
		}
		if got := st.abandonCtx.Err(); got != nil {
			t.Fatalf("收尾 ctx 不得已取消（kernel#533），实得 %v", got)
		}
		if got := st.abandonCtx.Value(k); got != "v" {
			t.Fatalf("收尾 ctx 应保留值（WithoutCancel 保值），实得 %v", got)
		}
	})

	t.Run("未知档施加失败：MarkUncertain 与 Release 脱离操作 ctx", func(t *testing.T) {
		st, fs := newCtxGuardStore()
		r := NewRunner(st, 30*time.Second)
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), k, "v"))
		defer cancel()
		a := act("k533-uncertain", "res-533-uncertain")

		outcome, _, err := r.Execute(ctx, a, func(context.Context, Lease) (Result, error) {
			cancel()
			return Result{}, errors.New("apply canceled")
		})
		if !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("结果未知必须可判（ErrOutcomeUnknown），实得 %v", err)
		}
		if outcome != OutcomeUncertain {
			t.Fatalf("应判 uncertain，实得 %v", outcome)
		}
		if fs.markUncertainCalls != 1 {
			t.Fatalf("MarkUncertain 应送达 1 次，实得 %d", fs.markUncertainCalls)
		}
		if fs.releaseCalls != 1 {
			t.Fatalf("标记成功后 Release 应送达 1 次，实得 %d", fs.releaseCalls)
		}
		if got := st.markUncertainCtx.Err(); got != nil {
			t.Fatalf("MarkUncertain 收尾 ctx 不得已取消，实得 %v", got)
		}
		if got := st.releaseCtx.Err(); got != nil {
			t.Fatalf("Release 收尾 ctx 不得已取消，实得 %v", got)
		}
		if got := st.releaseCtx.Value(k); got != "v" {
			t.Fatalf("Release 收尾 ctx 应保留值，实得 %v", got)
		}
	})
}

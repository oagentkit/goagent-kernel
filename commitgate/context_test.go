// context_test.go —— issue #185 判据：commitgate 全链透传 context.Context。
//
// 网络 DB 实现在 API 层拿不到 deadline/cancel 时，上游超时后连接仍占着、
// 重试无法停止。修复后 Store 全部方法收 ctx、Guard 逐层透传；判据三件事：
//  1. ctx 已取消 ⇒ Acquire 返回可被 errors.Is 判定为 context.Canceled 的
//     错误，且**不进入存储**（存储实现不配合 ctx 也成立）；
//  2. 等待期间取消 ⇒ 取消信号真的穿到了存储（由实现中断等待）；
//  3. 五个存储方法都拿到调用方那个 ctx（值可观测），证明逐层透传。

package commitgate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// ctxKey 测试用 ctx 键（验证调用方 ctx 原样到达存储）。
type ctxKey struct{}

// recordingStore 记录五个方法的 ctx（委托给内存实现；ctx 健康时行为不变）。
type recordingStore struct {
	inner  *MemoryStore
	calls  []string
	seen   []any
	nonNil []bool
}

func newRecordingStore() *recordingStore { return &recordingStore{inner: NewMemoryStore()} }

func (s *recordingStore) record(ctx context.Context, name string) {
	s.calls = append(s.calls, name)
	s.seen = append(s.seen, ctx.Value(ctxKey{}))
	s.nonNil = append(s.nonNil, ctx != nil)
}

func (s *recordingStore) Claim(ctx context.Context, ref Ref, rec Record) (Record, bool, error) {
	s.record(ctx, "Claim")
	return s.inner.Claim(ctx, ref, rec)
}

func (s *recordingStore) ReleaseToken(ctx context.Context, ref Ref, token uint64) (bool, error) {
	s.record(ctx, "ReleaseToken")
	return s.inner.ReleaseToken(ctx, ref, token)
}

func (s *recordingStore) SeedIfAbsent(ctx context.Context, ref Ref, rec Record) error {
	s.record(ctx, "SeedIfAbsent")
	return s.inner.SeedIfAbsent(ctx, ref, rec)
}

func (s *recordingStore) Get(ctx context.Context, ref Ref) (Record, bool, error) {
	s.record(ctx, "Get")
	return s.inner.Get(ctx, ref)
}

func (s *recordingStore) Count(ctx context.Context) (int, error) {
	s.record(ctx, "Count")
	return s.inner.Count(ctx)
}

// TestAcquireCanceledContextDoesNotTouchStore ctx 已取消时 Acquire 必须立即
// 返回 context.Canceled，且**不把调用交给存储**（不进入等待）。
func TestAcquireCanceledContextDoesNotTouchStore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ctx   func() (context.Context, context.CancelFunc)
		want  error
		cause string
	}{
		{
			name: "显式取消",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "已超时",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			want: context.DeadlineExceeded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newRecordingStore()
			g := NewGuardWithStore(st, isTerminal)
			ctx, cancel := tc.ctx()
			defer cancel()

			_, err := g.Acquire(ctx, Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1})
			if !errors.Is(err, tc.want) {
				t.Fatalf("应归口 %v（可 errors.Is 判定），实得 %v", tc.want, err)
			}
			if len(st.calls) != 0 {
				t.Fatalf("ctx 已取消时不得进入存储（实得调用 %v）", st.calls)
			}
			// 错误仍带定位上下文（宿主日志能直接找到环节）。
			if !strings.Contains(err.Error(), "run-1") || !strings.Contains(err.Error(), "s1") {
				t.Fatalf("错误应带 run_id/step 上下文: %v", err)
			}
		})
	}
}

// blockingStore Claim 等 ctx 结束才返回（模拟网络存储的等待）。
type blockingStore struct {
	entered chan struct{}
}

func (s *blockingStore) Claim(ctx context.Context, _ Ref, _ Record) (Record, bool, error) {
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return Record{}, false, ctx.Err()
}

func (s *blockingStore) ReleaseToken(context.Context, Ref, uint64) (bool, error) {
	return false, nil
}

func (s *blockingStore) SeedIfAbsent(context.Context, Ref, Record) error { return nil }
func (s *blockingStore) Get(context.Context, Ref) (Record, bool, error) {
	return Record{}, false, nil
}
func (s *blockingStore) Count(context.Context) (int, error) { return 0, nil }

// TestAcquirePropagatesCancellationWhileWaiting 等待期间取消必须中断等待：
// 取消信号真的穿到了存储（不是 Guard 自己造了一个不可取消的 ctx）。
func TestAcquirePropagatesCancellationWhileWaiting(t *testing.T) {
	st := &blockingStore{entered: make(chan struct{}, 1)}
	g := NewGuardWithStore(st, isTerminal)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := g.Acquire(ctx, Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("等待期间超时必须返回 context.DeadlineExceeded，实得 %v", err)
	}
	select {
	case <-st.entered:
	default:
		t.Fatal("Claim 未被调用：取消没有穿到存储")
	}
}

// TestGuardPassesCallerContextToEveryStoreMethod 五个存储方法都必须拿到
// 调用方那个 ctx（按值可观测）——逐层透传的正面判据。
func TestGuardPassesCallerContextToEveryStoreMethod(t *testing.T) {
	st := newRecordingStore()
	g := NewGuardWithStore(st, isTerminal)
	ctx := context.WithValue(context.Background(), ctxKey{}, "caller-ctx")

	lease, err := g.Acquire(ctx, Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1})
	if err != nil {
		t.Fatalf("健康 ctx 上占用应成功: %v", err)
	}
	g.ReleaseLease(ctx, lease)
	if err := g.SeedE(ctx, Commit{Ref: ref("s2"), Status: "succeeded", Branch: "b", Seq: 2}); err != nil {
		t.Fatalf("健康 ctx 上回灌应成功: %v", err)
	}
	g.Status(ctx, ref("s1"))
	g.Committed(ctx)

	want := []string{"Claim", "ReleaseToken", "SeedIfAbsent", "Get", "Count"}
	if len(st.calls) != len(want) {
		t.Fatalf("五个存储方法都应经 ctx 调用（要 %v，实得 %v）", want, st.calls)
	}
	for i, name := range want {
		if st.calls[i] != name {
			t.Fatalf("第 %d 个调用应为 %s，实得 %s（全部：%v）", i+1, name, st.calls[i], st.calls)
		}
		if st.seen[i] != "caller-ctx" {
			t.Fatalf("%s 未拿到调用方 ctx（实得 %v）", name, st.seen[i])
		}
		if !st.nonNil[i] {
			t.Fatalf("%s 收到的 ctx 不得为 nil", name)
		}
	}
}

// TestMemoryStoreHonorsCanceledContext 参考实现自身也守 ctx：取消即返回，
// 且**不留半条状态**（取消的 Claim 不得占住闸，取消的 Seed 不得记上）。
func TestMemoryStoreHonorsCanceledContext(t *testing.T) {
	st := NewMemoryStore()
	ref := Ref{RunID: "r", Step: "s"}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := st.Claim(canceled, ref, Record{Status: "done"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Claim 应返回 context.Canceled，实得 %v", err)
	}
	// 取消的 Claim 不得留下占用：同一个 Ref 仍能被正常占用。
	if _, ok, err := st.Claim(context.Background(), ref, Record{Status: "done"}); err != nil || !ok {
		t.Fatalf("取消的 Claim 不得留下占用（claimed=%v err=%v）", ok, err)
	}

	if _, err := st.ReleaseToken(canceled, ref, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReleaseToken 应返回 context.Canceled，实得 %v", err)
	}
	// 取消的释放不得删掉记录。
	if _, ok, _ := st.Get(context.Background(), ref); !ok {
		t.Fatal("取消的 ReleaseToken 不得删除记录")
	}

	other := Ref{RunID: "r", Step: "s2"}
	if err := st.SeedIfAbsent(canceled, other, Record{Status: "done"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SeedIfAbsent 应返回 context.Canceled，实得 %v", err)
	}
	if _, ok, _ := st.Get(context.Background(), other); ok {
		t.Fatal("取消的 SeedIfAbsent 不得记上记录")
	}

	if _, _, err := st.Get(canceled, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get 应返回 context.Canceled，实得 %v", err)
	}
	if _, err := st.Count(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Count 应返回 context.Canceled，实得 %v", err)
	}
}

// TestObservationMethodsFailSoftOnCanceledContext 观测面保持既有口径：
// Status/Committed 在 ctx 取消时退「未提交 / 0」，不把自检打成假红。
func TestObservationMethodsFailSoftOnCanceledContext(t *testing.T) {
	g := NewGuard(isTerminal)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, committed := g.Status(ctx, ref("s1")); committed {
		t.Fatal("ctx 取消时 Status 应按未提交处理")
	}
	if n := g.Committed(ctx); n != 0 {
		t.Fatalf("ctx 取消时 Committed 应退 0，实得 %d", n)
	}
}

// TestNilContextTreatedAsBackground 迁移期防御：nil ctx 在 Guard 边界归一为
// context.Background()——旧调用点仓促迁移时不会把 nil 带进网络驱动
// （database/sql 的 QueryContext(nil) 是 panic，不是「无取消」）。
func TestNilContextTreatedAsBackground(t *testing.T) {
	st := newRecordingStore()
	g := NewGuardWithStore(st, isTerminal)
	var nilCtx context.Context // 显式构造 nil 接口值：判「边界归一」而非编译期常量
	if _, err := g.Acquire(nilCtx, Commit{Ref: ref("s1"), Status: "succeeded", Branch: "b", Seq: 1}); err != nil {
		t.Fatalf("nil ctx 应按无取消信号处理，实得 %v", err)
	}
	if len(st.calls) != 1 || !st.nonNil[0] {
		t.Fatalf("nil ctx 必须在 Guard 边界归一为非 nil（调用=%v nonNil=%v）", st.calls, st.nonNil)
	}
}

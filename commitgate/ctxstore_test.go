package commitgate

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/oekit/pkg/errors"
)

// 本文件是 CtxStore 接缝（issue #185）的判据：
//   - ctx 取消时 Claim 返回 context.Canceled 而不是继续等待存储（issue 验收
//     原句），且**状态零变更**（不占用、不铸令牌）；
//   - Guard 的 *Context 方法族把调用方 ctx **逐层透传**到存储（换 ctx 键可查）；
//   - 注入旧 Store（无 ctx 形态）时，ctx 形态仍在入口承接取消——两条注入
//     路径都有取消闸。

// slowCtxStore 存储替身：每个方法阻塞在 ctx 上直到取消/释放，
// 用来证明「取消让等待中的存储调用提前返回」。
type slowCtxStore struct {
	mu      sync.Mutex
	release chan struct{} // 关闭即放行
	seen    []context.Context
}

func newSlowCtxStore() *slowCtxStore {
	return &slowCtxStore{release: make(chan struct{})}
}

func (s *slowCtxStore) wait(ctx context.Context) error {
	s.mu.Lock()
	s.seen = append(s.seen, ctx)
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return nil
	}
}

func (s *slowCtxStore) Claim(ctx context.Context, _ Ref, _ Record) (Record, bool, error) {
	if err := s.wait(ctx); err != nil {
		return Record{}, false, err
	}
	return Record{Token: 1}, true, nil
}
func (s *slowCtxStore) ReleaseToken(ctx context.Context, _ Ref, _ uint64) (bool, error) {
	return false, s.wait(ctx)
}
func (s *slowCtxStore) SeedIfAbsent(ctx context.Context, _ Ref, _ Record) error {
	return s.wait(ctx)
}
func (s *slowCtxStore) Get(ctx context.Context, _ Ref) (Record, bool, error) {
	return Record{}, false, s.wait(ctx)
}
func (s *slowCtxStore) Count(ctx context.Context) (int, error) {
	return 0, s.wait(ctx)
}

type ctxKey string

// TestCtx取消时Claim返回Canceled且不占用 —— issue #185 验收原句：
// 「ctx 取消时 Claim 返回 context.Canceled 而不是继续等待存储」。
// 存储慢（等在 ctx 上），上游先取消：AcquireContext 立即以 context.Canceled
// 失败，且闸状态零变更（换新 ctx 重试必须能正常占用）。
func TestCtx取消时Claim返回Canceled且不占用(t *testing.T) {
	st := newSlowCtxStore()
	g := NewGuardWithCtxStore(st, nil)
	c := Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "done", Branch: "a", Seq: 1}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := g.AcquireContext(ctx, c)
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后的 AcquireContext 必须 errors.Is context.Canceled，实得 %v", err)
	}

	// 闸状态零变更：取消的那次调用没占用、没铸令牌。
	close(st.release)
	lease, err := g.AcquireContext(context.Background(), c)
	if err != nil {
		t.Fatalf("取消调用残留状态：新 ctx 重试占用失败 %v", err)
	}
	if lease.Token == 0 {
		t.Fatal("令牌必须由存储铸造")
	}
}

// TestCtx逐层透传到存储 —— Guard 调用链必须把**调用方的** ctx 带进存储层
// （接口债的核心：实现拿不到 ctx 就承接不了取消）。用自定义 ctx 键查证。
func TestCtx逐层透传到存储(t *testing.T) {
	st := newSlowCtxStore()
	g := NewGuardWithCtxStore(st, nil)
	close(st.release)

	mark := context.WithValue(context.Background(), ctxKey("lane"), "fin")
	c := Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "done", Branch: "a", Seq: 1}
	lease, err := g.AcquireContext(mark, c)
	if err != nil {
		t.Fatalf("AcquireContext: %v", err)
	}
	if err := g.SeedContext(mark, Commit{Ref: Ref{RunID: "r1", Step: "s0"}, Status: "done", Branch: "a"}); err != nil {
		t.Fatalf("SeedContext: %v", err)
	}
	_, _, _ = g.StatusContext(mark, Ref{RunID: "r1", Step: "s1"})
	_, _ = g.CommittedContext(mark)
	g.ReleaseLeaseContext(mark, lease)

	if len(st.seen) != 5 {
		t.Fatalf("五个 ctx 形态方法都应触达存储，实得 %d 次", len(st.seen))
	}
	for i, seen := range st.seen {
		if seen.Value(ctxKey("lane")) != "fin" {
			t.Fatalf("第 %d 次存储调用拿到的不是调用方 ctx（透传断了）", i)
		}
	}
}

// TestCtx已取消的入口不触达存储 —— 取消的调用不应把请求发出去
// （「上游超时后连接仍占着」就是这条没拦住的后果）。
func TestCtx已取消的入口不触达存储(t *testing.T) {
	st := newSlowCtxStore()
	g := NewGuardWithCtxStore(st, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "done", Branch: "a", Seq: 1}

	if _, err := g.AcquireContext(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireContext: %v", err)
	}
	if err := g.SeedContext(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("SeedContext: %v", err)
	}
	g.ReleaseLeaseContext(ctx, Lease{Ref: c.Ref, Token: 1})
	if _, _, ok := g.StatusContext(ctx, c.Ref); ok {
		t.Fatal("取消的 StatusContext 不得报已提交")
	}
	if _, err := g.CommittedContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommittedContext 必须 error 上抛（观测面也有 deadline），实得 %v", err)
	}
	if len(st.seen) != 0 {
		t.Fatalf("取消的调用不得触达存储，实触达 %d 次", len(st.seen))
	}
}

// TestCtx形态与SeedE判定同份 —— SeedContext 与 SeedE 的词表判定/空引用判定
// 必须同一份：空 Ref 响亮失败、非终态行跳过（issue #154 的判据不许分叉）。
func TestCtx形态与SeedE判定同份(t *testing.T) {
	st := NewMemoryCtxStore()
	isTerminal := func(s string) bool { return s == "done" }
	g := NewGuardWithCtxStore(st, isTerminal)

	if err := g.SeedContext(context.Background(), Commit{Ref: Ref{RunID: "r1"}, Status: "done"}); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("空 Ref 必须 ErrEmptyRef，实得 %v", err)
	}
	if err := g.SeedContext(context.Background(), Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "running"}); err != nil {
		t.Fatalf("非终态行应跳过不报错，实得 %v", err)
	}
	if err := g.SeedContext(context.Background(), Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "done"}); err != nil {
		t.Fatalf("SeedContext: %v", err)
	}
	// 非终态行没记上（判定面与回灌面同词表）。
	if _, _, ok := g.StatusContext(context.Background(), Ref{RunID: "r1", Step: "s1"}); !ok {
		t.Fatal("终态行应已回灌")
	}
	if n, _ := g.CommittedContext(context.Background()); n != 1 {
		t.Fatalf("已提交环节数应为 1，实得 %d", n)
	}
	// 空 Ref 的 AcquireContext 同侧响亮。
	if _, err := g.AcquireContext(context.Background(), Commit{Ref: Ref{RunID: "r1"}, Status: "done"}); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("AcquireContext 空 Ref 必须 ErrEmptyRef，实得 %v", err)
	}
}

// Test旧Store注入时ctx形态仍有取消闸 —— 已有的 Store 实现（无 ctx 形态）
// 走 NewGuardWithStore 注入后，*Context 方法族在入口承接取消，
// 存储零改动继续可用（迁移说明的「已有实现不受影响」）。
func Test旧Store注入时ctx形态仍有取消闸(t *testing.T) {
	st := NewMemoryStore()
	g := NewGuardWithStore(st, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "done", Branch: "a", Seq: 1}

	if _, err := g.AcquireContext(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("旧 Store 注入下取消仍须入口拦截，实得 %v", err)
	}
	// 无 ctx 方法族行为不变（Stable 兼容面）。
	lease, err := g.Acquire(c)
	if err != nil {
		t.Fatalf("无 ctx Acquire: %v", err)
	}
	if lease.Token == 0 {
		t.Fatal("令牌必须由存储铸造")
	}
}

// TestMemoryCtxStore取消不动状态 —— 参考实现的契约演示：入口判取消，
// 不动 map、不铸令牌。
func TestMemoryCtxStore取消不动状态(t *testing.T) {
	st := NewMemoryCtxStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ref := Ref{RunID: "r1", Step: "s1"}

	if _, _, err := st.Claim(ctx, ref, Record{Status: "done"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Claim: %v", err)
	}
	if err := st.SeedIfAbsent(ctx, ref, Record{Status: "done"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SeedIfAbsent: %v", err)
	}
	if _, _, err := st.Get(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get: %v", err)
	}
	if _, err := st.Count(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Count: %v", err)
	}
	if _, err := st.ReleaseToken(ctx, ref, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReleaseToken: %v", err)
	}
	if n, err := st.Count(context.Background()); err != nil || n != 0 {
		t.Fatalf("取消的调用不得改状态：count=%d err=%v", n, err)
	}
}

// TestCtxStore判定与Acquire同份 —— 注入 CtxStore 的闸，无 ctx 方法族走同一份
// 存储（AlreadyCommitted 判定不分叉）；并发占用恰好一个赢家（原子性契约）。
func TestCtxStore判定与Acquire同份(t *testing.T) {
	st := NewMemoryCtxStore()
	g := NewGuardWithCtxStore(st, nil)
	c := Commit{Ref: Ref{RunID: "r1", Step: "s1"}, Status: "done", Branch: "a", Seq: 1}

	if _, err := g.Acquire(c); err != nil {
		t.Fatalf("无 ctx 方法族应走同一份 CtxStore： %v", err)
	}
	_, err := g.AcquireContext(context.Background(), c)
	var dup *AlreadyCommittedError
	if !errors.As(err, &dup) {
		t.Fatalf("二次提交必须报 AlreadyCommittedError，实得 %v", err)
	}
	if !strings.Contains(dup.Error(), "s1") {
		t.Fatalf("错误信息应带环节引用，实得 %q", dup.Error())
	}
}

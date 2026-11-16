// ctxstore.go —— 带取消的存储接缝（issue #185）。
//
// # 为什么 Store 之外还要有第二个接缝
//
// Store 接缝（issue #145）的五个方法都没有 context.Context——网络数据库实现
// 在接口层拿不到 deadline/cancel：上游超时后连接仍占着、无法停止无意义的重试。
// 直接给 Store 的方法加 ctx 是**改已有导出符号的签名**——本包 Stable（v0.33.0
// 定级），breaking 不允许，故按 docs/api-compat.md 的 additive 路径新增本接缝。
// 多副本宿主的**新**实现请实现 [CtxStore] 并经 [NewGuardWithCtxStore] 注入；
// 已有的 Store 实现继续可用（零改动），迁移说明见文件尾。
//
// # CtxStore 的硬契约（跨进程实现必须给）
//
//   - **逐调用承接取消**：ctx 已取消 ⇒ 立即返回 ctx.Err()（errors.Is 可判到
//     context.Canceled / context.DeadlineExceeded），**不得先动状态再报错**；
//     请求在途时实现内部的等待（连接、行锁、重试）必须挂在同一个 ctx 上。
//   - 其余契约与 [Store] 逐字相同（Claim 原子、令牌由存储铸造、ReleaseToken
//     条件删除）——两个接缝共享同一份原子性契约，见 store.go 文件头。
//
// # 进程内参考实现
//
// NewMemoryCtxStore：单进程成立，**不得上生产**（与 NewMemoryStore 同口径）。
// 它演示的是「取消在动状态之前生效」这条契约：ctx 已取消的调用不动 map、
// 不铸令牌，原样返回 ctx.Err()。
package commitgate

import (
	"context"
	"sync"

	"github.com/oekit/pkg/errors"
)

// CtxStore 带 context.Context 的终态记录存储接缝（issue #185）。
//
// 方法语义与 [Store] 一一对应（判定逻辑仍在 Guard，状态与原子占用归存储），
// 差别只有两处：每个方法**第一参是 ctx**，且实现必须逐调用承接取消（见
// 文件头硬契约）。两个接缝是**同一契约的两种形态**：Store 是 Stable 兼容
// 保留的无 ctx 形态，CtxStore 是多副本新实现的目标形态。
type CtxStore interface {
	// Claim 原子占用（语义同 [Store.Claim]）。ctx 已取消 ⇒ 返回 ctx.Err()
	// 且不占用、不铸令牌。
	Claim(ctx context.Context, ref Ref, rec Record) (out Record, claimed bool, err error)
	// ReleaseToken 凭令牌精确释放（语义同 [Store.ReleaseToken]）。
	ReleaseToken(ctx context.Context, ref Ref, token uint64) (released bool, err error)
	// SeedIfAbsent 回灌已持久化的终态（语义同 [Store.SeedIfAbsent]）。
	SeedIfAbsent(ctx context.Context, ref Ref, rec Record) error
	// Get 查记录（语义同 [Store.Get]）。
	Get(ctx context.Context, ref Ref) (Record, bool, error)
	// Count 已终态提交的环节数（语义同 [Store.Count]；DB 计数也会慢，
	// 观测面同样要能被取消）。
	Count(ctx context.Context) (int, error)
}

// NewMemoryCtxStore 构造进程内参考实现（CtxStore 形态）。
func NewMemoryCtxStore() CtxStore {
	return &memoryCtxStore{done: map[Ref]Record{}}
}

// memoryCtxStore CtxStore 的进程内参考实现。
//
// 判-占-铸令牌与 memoryStore 同构；差别是每个方法入口先承接取消——
// 「ctx 已取消 ⇒ 什么都不动、返回 ctx.Err()」是本接缝区别于 Store 的全部语义。
type memoryCtxStore struct {
	mu       sync.Mutex
	done     map[Ref]Record
	tokenSeq uint64
}

// Claim 实现 CtxStore（取消先行：动了 map 再报错 = 取消没接住）。
func (s *memoryCtxStore) Claim(ctx context.Context, ref Ref, rec Record) (Record, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return Record{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.done[ref]; ok {
		return prev, false, nil
	}
	s.tokenSeq++
	rec.Token = s.tokenSeq
	s.done[ref] = rec
	return rec, true, nil
}

// ReleaseToken 实现 CtxStore。
func (s *memoryCtxStore) ReleaseToken(ctx context.Context, ref Ref, token uint64) (bool, error) {
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.done[ref]
	if !ok || prev.Token != token {
		return false, nil
	}
	delete(s.done, ref)
	return true, nil
}

// SeedIfAbsent 实现 CtxStore。
func (s *memoryCtxStore) SeedIfAbsent(ctx context.Context, ref Ref, rec Record) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.done[ref]; ok {
		return nil
	}
	s.done[ref] = rec
	return nil
}

// Get 实现 CtxStore。
func (s *memoryCtxStore) Get(ctx context.Context, ref Ref) (Record, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return Record{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.done[ref]
	return rec, ok, nil
}

// Count 实现 CtxStore。
func (s *memoryCtxStore) Count(ctx context.Context) (int, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.done), nil
}

// ctxErr 取消判定收敛在一处（五个方法同一把尺）。
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("commitgate: CtxStore 收到 nil ctx（调用方必须传有效 context）")
	}
	return ctx.Err()
}

// NewGuardWithCtxStore 构造闸并注入**带取消**的存储接缝（issue #185）。
//
// 多副本宿主的**新**实现实现 [CtxStore] 经本构造注入：Guard 调用链逐层透传
// 调用方 ctx（超时/取消在存储那层就生效，上游超时后不再占着连接重试）。
// s 为 nil ⇒ 退化为进程内参考存储。isTerminal 语义同 [NewGuardWithStore]。
//
// 本构造与 [NewGuardWithStore] 二选一：注入 CtxStore 后，无 ctx 的旧方法
// （Acquire 等）以 context.Background() 走同一份存储——判定逻辑不分叉。
func NewGuardWithCtxStore(s CtxStore, isTerminal func(status string) bool) *Guard {
	if s == nil {
		return NewGuard(isTerminal)
	}
	return &Guard{store: nil, ctxStore: s, isTerminal: isTerminal}
}

// ── Guard 的 ctx 形态方法（issue #185：Guard 调用链逐层透传）──────────────
//
// 与无 ctx 版本一一对应（database/sql 的 QueryContext 同款命名），判定逻辑
// 与无 ctx 版本共用同一份——ctx 形态不是第二套判据，只是把调用方的
// deadline/cancel 带进存储层。注入的是旧 Store（无 ctx 形态）时，ctx 形态
// 仍先承接取消（入口判 ctx.Err()），只是存储调用本身拿不到 ctx。

// AcquireContext 是 [Guard.Acquire] 的 ctx 形态：ctx 已取消 ⇒ 返回
// ctx.Err()（errors.Is 可判 context.Canceled），不占用、不铸令牌。
func (g *Guard) AcquireContext(ctx context.Context, c Commit) (Lease, error) {
	if err := ctxErr(ctx); err != nil {
		return Lease{}, err
	}
	if c.Ref.RunID == "" || c.Ref.Step == "" {
		return Lease{}, errors.Wrap(ErrEmptyRef, "提交缺环节引用", "run_id", c.Ref.RunID, "step", c.Ref.Step)
	}

	terminal := g.isTerminal == nil || g.isTerminal(c.Status)
	if !terminal {
		return Lease{}, nil
	}
	out, claimed, err := g.claimCtx(ctx, c.Ref, Record{Status: c.Status, Branch: c.Branch, Seq: c.Seq})
	if err != nil {
		return Lease{}, errors.Wrap(err, "占用终态失败", "run_id", c.Ref.RunID, "step", c.Ref.Step)
	}
	if !claimed {
		prev := record{status: out.Status, branch: out.Branch, seq: out.Seq, token: out.Token}
		return Lease{}, &AlreadyCommittedError{
			Ref:       c.Ref,
			Prev:      prev,
			PrevSeq:   prev.seq,
			Status:    prev.status,
			Branch:    prev.branch,
			NewStatus: c.Status,
			NewBranch: c.Branch,
		}
	}
	return Lease{Ref: c.Ref, Token: out.Token}, nil
}

// SeedContext 是 [Guard.SeedE] 的 ctx 形态（响亮形态 + ctx）：判定与 SeedE
// 完全同一份（isTerminal 词表、不覆盖幂等、空 Ref 报 ErrEmptyRef），仅多带
// 调用方 ctx 供存储层承接取消。
func (g *Guard) SeedContext(ctx context.Context, c Commit) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if c.Ref.RunID == "" || c.Ref.Step == "" {
		return errors.Wrap(ErrEmptyRef, "回灌缺环节引用", "run_id", c.Ref.RunID, "step", c.Ref.Step)
	}
	if g.isTerminal != nil && !g.isTerminal(c.Status) {
		return nil
	}
	if err := g.seedIfAbsentCtx(ctx, c.Ref, Record{Status: c.Status, Branch: c.Branch, Seq: c.Seq}); err != nil {
		return errors.Wrap(err, "回灌终态失败（闸未记上，重复终态提交将重新敞开）",
			"run_id", c.Ref.RunID, "step", c.Ref.Step)
	}
	return nil
}

// ReleaseLeaseContext 是 [Guard.ReleaseLease] 的 ctx 形态：ctx 供存储层承接
// 取消；凭证对不上仍静默返回（交还闸不是错误路径，口径同 ReleaseLease）。
func (g *Guard) ReleaseLeaseContext(ctx context.Context, l Lease) {
	if l.Token == 0 || (g.store == nil && g.ctxStore == nil) {
		return
	}
	if err := ctxErr(ctx); err != nil {
		return // 取消的释放不值得等：与「凭证对不上静默」同一侧
	}
	switch {
	case g.ctxStore != nil:
		_, _ = g.ctxStore.ReleaseToken(ctx, l.Ref, l.Token)
	default:
		_, _ = g.store.ReleaseToken(l.Ref, l.Token)
	}
}

// StatusContext 是 [Guard.Status] 的 ctx 形态。
// 零值 Guard / Store 故障 / ctx 已取消 ⇒ 未提交（观测面口径同 Status）。
func (g *Guard) StatusContext(ctx context.Context, ref Ref) (status string, branch string, committed bool) {
	if g.store == nil && g.ctxStore == nil {
		return "", "", false
	}
	if err := ctxErr(ctx); err != nil {
		return "", "", false
	}
	r, ok, err := g.getCtx(ctx, ref)
	if err != nil || !ok {
		return "", "", false
	}
	return r.Status, r.Branch, true
}

// CommittedContext 是 [Guard.Committed] 的 ctx 形态。与无 ctx 版本的差别：
// error **上抛**（新 API 响亮口径）——网络计数也会慢/会失败，调用方有 ctx
// 就该能区分「0 个」与「没数成」。
func (g *Guard) CommittedContext(ctx context.Context) (int, error) {
	if g.store == nil && g.ctxStore == nil {
		return 0, nil
	}
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	switch {
	case g.ctxStore != nil:
		return g.ctxStore.Count(ctx)
	default:
		return g.store.Count()
	}
}

// claimCtx 存储调用的 ctx 路由（CtxStore 注入时透传；旧 Store 先承接取消）。
func (g *Guard) claimCtx(ctx context.Context, ref Ref, rec Record) (Record, bool, error) {
	switch {
	case g.ctxStore != nil:
		return g.ctxStore.Claim(ctx, ref, rec)
	default:
		return g.store.Claim(ref, rec)
	}
}

// seedIfAbsentCtx 同 claimCtx 的回灌路由。
func (g *Guard) seedIfAbsentCtx(ctx context.Context, ref Ref, rec Record) error {
	switch {
	case g.ctxStore != nil:
		return g.ctxStore.SeedIfAbsent(ctx, ref, rec)
	default:
		return g.store.SeedIfAbsent(ref, rec)
	}
}

// getCtx 同 claimCtx 的读取路由。
func (g *Guard) getCtx(ctx context.Context, ref Ref) (Record, bool, error) {
	switch {
	case g.ctxStore != nil:
		return g.ctxStore.Get(ctx, ref)
	default:
		return g.store.Get(ref)
	}
}

// ── 迁移说明（issue #185 验收的「给迁移说明」）────────────────────────────
//
//   - 已实现的 Store（含内存参考实现与宿主的 DB 实现）：零改动继续可用，
//     走 NewGuardWithStore 与无 ctx 方法族；
//   - 多副本宿主的**新**实现：实现 CtxStore（同一份原子性契约 + 逐调用承接
//     取消），走 NewGuardWithCtxStore 与 *Context 方法族；
//   - 两个接缝不要求二选一实现（方法名冲突也使同一类型无法双实现）；
//     Store 无 ctx 形态保留为 Stable 兼容面，拆除（若有）只发生在 minor
//     边界并按 docs/api-compat.md 五步流程走 Deprecated 过渡。

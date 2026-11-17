// effect.go —— sideeffect.Store 的 PostgreSQL 参考实现：类型、事务骨架与行读写。
//
// # 三条判据在库里的落法（与内存版 effectStore 逐条对应）
//
//  1. **三项联查**：Complete / Release / Abandon / Renew / MarkUncertain 都在
//     同一个事务里校验「令牌仍是最新代 + 持有者身份一致 + 租约与动作的资源
//     一致」。校验与写入在同一事务 ⇒ 任何一步失败都发生在**任何状态被改之前**
//     ——这正是 MarkUncertain「标记写失败必须保住执行意图」的落库形态。
//  2. **执行意图**：Acquire 的授予（令牌 + 持有者身份）与意图登记写在
//     同一事务里；意图过期未裁决 ⇒ 同一 Key 转入未知态而非重新授予。
//  3. **对账占用**：BeginReconcile 与裁决分表字段，占用过期可重入。
//
// # 原子性来自哪里
//
// 每个方法 = 一个事务 = 若干条 `SELECT … FOR UPDATE` + 条件写。行锁把
// 「读状态 → 判 → 写」钉成跨进程原子：第二个进程在第一个提交前读到的
// 永远是旧状态，且会在行锁上等待。这与「一条带条件的 UPDATE」等价，
// 是契约点名认可的两种形态之一。
//
// 锁序恒为「资源行 → 动作行」：所有方法按同一顺序加锁，两个并发事务
// 不可能交叉持锁（交叉死锁在锁序一致时不存在）。
package pgstore

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// EffectStore sideeffect.Store 的 PostgreSQL 参考实现。
//
// 一个实例绑定一个 schema（全部表在其中）；并发安全。
type EffectStore struct {
	pool *pgxpool.Pool
	// tenantScoped 装配期声明（issue #275）：true ⇒ 每个入口从 ctx 取租户，
	// 缺租户拒绝（fail-closed）。false ⇒ 旧布局，租户取 Action.Tenant
	// （空 = ''），不强制上下文。
	tenantScoped bool

	// mu 保护 now：UseClock 与各方法的时钟读取互斥（套件在测试里注入时钟）。
	mu  sync.Mutex
	now func() time.Time
}

// NewEffectStore 构造副作用闸存储。pool 归调用方管理（Close 由它负责）。
//
// schema 必须已建好表（EnsureSchema）；search_path 已被连接池指到该 schema
// （见 NewEffectStores），故本包的 SQL 不写 schema 前缀。
func NewEffectStore(pool *pgxpool.Pool) *EffectStore {
	// 默认（未注入）：**不使用进程本地时钟**——now 为 nil，nowIn 读数据库
	// 的 clock_timestamp。租约是跨节点共享的事实，判定时刻必须来自同
	// 一个权威时钟源：A 节点时钟慢 10s、B 节点快 10s 时，用各自 time.Now
	// 判对方写的租约，15s 的 TTL 会「从一开始就被判过期」⇒ B 提前接管
	// A 仍在执行的资源（评审第三轮 P0）。注入（sidetest 契约套件）时
	// 用注入值——套件的过期推进语义不变。
	return &EffectStore{pool: pool, now: nil}
}

// NewTenantEffectStore 构造**租户作用域**的副作用闸存储（issue #275）。
//
// 与 coord.NewTenantBoardAdapter 同款装配期声明：所有方法从 ctx 取租户
// （tenant.Require），缺租户 ⇒ tenant.ErrNoContext，写入被拒；Action.Tenant
// 可与 ctx 一致（推荐显式带上，便于排障），不一致 ⇒ ErrTenantMismatch。
//
// 存储键是 (tenant, key) / (tenant, resource)：跨租户同 Key 互不命中。
func NewTenantEffectStore(pool *pgxpool.Pool) *EffectStore {
	return &EffectStore{pool: pool, tenantScoped: true}
}

// TenantScoped 报告本实例是否声明了租户作用域（装配期自检用）。
func (s *EffectStore) TenantScoped() bool { return s.tenantScoped }

// prepareAction 入口处的租户解析：把作用域结论落回 Action.Tenant，后续
// 全部 SQL 用 act.Tenant 做联合定位（解析只在入口发生一次）。
func (s *EffectStore) prepareAction(ctx context.Context, act sideeffect.Action) (sideeffect.Action, error) {
	t, err := resolveTenant(ctx, s.tenantScoped, act.Tenant)
	if err != nil {
		return act, err
	}
	act.Tenant = t
	return act, nil
}

// UseClock 注入时钟（sidetest 契约套件用）。
//
// 过期判定必须读它，否则过期场景恒不触发（套件的硬要求）。
// 注入值代表**系统时间**的测试替身（整个判定链的唯一时间源），不是
// 「某个 worker 的本地时钟」——修复后的实现里 worker 根本不提供时间，
// 这正是「租约统一数据库时钟」要达成的状态。
func (s *EffectStore) UseClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}

// nowIn 事务内的当前时刻：注入了测试时钟（sidetest 契约套件）→ 用注入值；
// 否则读**数据库时间**（clock_timestamp——跨节点判定的权威时钟源）。
//
// 调用位置有硬要求：必须在**取得行锁之后**。CURRENT_TIMESTAMP 固定在事务
// 开始时刻，事务若在行锁上等了几秒，用它（或事务开头取的任何时间）判过期
// 就会把「等锁时长」算进租约寿命里——判定基准必须锚在「状态已锁定」那一刻。
func (s *EffectStore) nowIn(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	s.mu.Lock()
	injected := s.now
	s.mu.Unlock()
	if injected != nil {
		return injected(), nil
	}
	var t time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t); err != nil {
		return time.Time{}, errors.Wrap(err, "pgstore: 读数据库时间失败")
	}
	return t, nil
}

// runTx 事务骨架：fn 报错即整体回滚（不落任何一行），成功才提交。
func runTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.Wrap(err, "pgstore: 开事务失败")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Wrap(err, "pgstore: 提交事务失败")
	}
	return nil
}

// withinTx / withinTx2 / withinTx3 事务骨架的按返回值个数分的变体
// （Go 泛型变参表达不了「N 个返回值 + error」，拆具名变体最直白）。
//
// 「事务」是本实现全部原子性的来源——契约允许「一条语句**或**一个事务」，
// 本实现选事务：Acquire 的三判合一读多写多，一条语句表达不了。
func withinTx[T any](ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) (T, error)) (T, error) {
	var zero T
	err := runTx(ctx, pool, func(tx pgx.Tx) error {
		out, err := fn(tx)
		if err != nil {
			return err
		}
		zero = out
		return nil
	})
	return zero, err
}

func withinTx2[T1, T2 any](ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) (T1, T2, error)) (T1, T2, error) {
	var zero1 T1
	var zero2 T2
	err := runTx(ctx, pool, func(tx pgx.Tx) error {
		out1, out2, err := fn(tx)
		if err != nil {
			return err
		}
		zero1, zero2 = out1, out2
		return nil
	})
	return zero1, zero2, err
}

func withinTx3[T1, T2, T3 any](ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) (T1, T2, T3, error)) (T1, T2, T3, error) {
	var zero1 T1
	var zero2 T2
	var zero3 T3
	err := runTx(ctx, pool, func(tx pgx.Tx) error {
		out1, out2, out3, err := fn(tx)
		if err != nil {
			return err
		}
		zero1, zero2, zero3 = out1, out2, out3
		return nil
	})
	return zero1, zero2, zero3, err
}

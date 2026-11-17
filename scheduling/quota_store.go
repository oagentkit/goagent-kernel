// quota_store.go —— 多副本共享的**原子配额原语**（issue #189）。
//
// # 它补的是哪一段
//
// 本包的 Quota 是进程内账本（判据面不做锁，注释里写明并发使用方自担
// 同步）。单副本时够用；多副本时每个副本各算一份「已占用」，真实并发
// 上限 = 副本数 × limit——配额看起来生效了，实际超卖 N 倍。
//
// QuotaStore 把「占用/释放」做成存储中立原语：扣减与归还在**存储侧的
// 一次原子操作**里完成，多个副本共享同一份账。调度判据（什么时候该占、
// 占多少）仍在调用方；本原语只回答「这次占用能不能成立、账上还剩多少」。
//
// # 词表对齐
//
// budget 包的计量词表是上下文 token（Segment / Meter / Report），**没有
// Budget 这一类型**；因此这里不硬造第二套预算字段，而是复用本包 Quota
// 的账本语义：Limit（上限）/ Used（已占用）/ Remaining（余额）、整单占用
// 不做部分占用、超释放显式拒（ErrQuotaExhausted / ErrOverRelease）。
// 与 Quota 的唯一区别是账本放在存储侧、可跨副本共享。
//
// # 为什么释放要凭租约
//
// 与 taskboard「凭令牌认人」同一条纪律：释放只认 Reserve 铸造的租约，
// 不认「资源名 + 数量」。否则重复释放（或迟到的释放）会把额度重复归还，
// 余额虚增，后续占用全部「看起来有余量」，而真实资源早就被占满了。
// 租约里的 Resource/Amount 是**预占时的快照**，Release 以 Token 查到的
// 存储记录为准——带错字段的租约同样只归还那笔原始预占。
//
// # 回收协议（issue #267）
//
// 只有 Reserve/Release 时，Worker 被 SIGKILL 后没人再调 Release ⇒ 额度
// 永久占用 ⇒ 随时间归零 ⇒ 调度停摆。故租约带**归属与有效期**，并给出
// 回收协议：
//
//   - 正常完成 ⇒ Release（凭 Token，立即归还）；
//   - Worker 失联 ⇒ Reclaim：**必须同时确认预占已过期、且关联任务的租约
//     已失效**（TaskLeaseOracle）。任务租约仍有效时拒绝回收——旧任务可能
//     还在真跑，回收额度会让别的调度者超额占用同一份资源；
//   - Reconciler 定期扫描 ⇒ SweepExpired（对全部过期预占逐条走 Reclaim
//     判据）。
//
// 回收与迟到 Release 的竞争由 Token 保护：回收即删除存储记录，迟到的旧
// Release 查不到记录 ⇒ ErrOverRelease 且不扣账（不会二次归还/负余额）。
//
// # 边界
//
// 接口是存储中立的；MemQuotaStore 是进程内参考实现（互斥锁原子，只在
// 单进程内成立，不得当多副本账本上生产）。跨进程实现把各方法落到
// Redis Lua / PostgreSQL 条件 UPDATE / etcd txn 上，判据不变。
package scheduling

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"
)

// QuotaLease 一次预占的凭证（由存储铸造）。
//
// Token 是归还/回收的唯一凭据；Resource 与 Amount 是预占时的快照（便于
// 调用方记账与排查），Release/Reclaim **不**以它们判定——重复释放/伪造
// 字段都绕不过 Token 的存储记录。
type QuotaLease struct {
	// Resource 资源账本键（宿主词表：模型、工具、并发槽位等）。
	Resource string
	// Amount 本次预占的数量（>0）。
	Amount int64
	// Token 存储铸造的预占令牌（归还/回收凭据）。
	Token string
	// TaskID 关联的任务标识（与 taskboard.TaskID 的字符串形态一致；
	// 空 = 未关联任务，只能按 TTL 回收）。回收前经 TaskLeaseOracle 确认
	// 该任务的租约已失效（issue #267）。
	TaskID string
	// Owner 预占者标识（观测与排查；回收判据不凭它认人）。
	Owner string
	// Until 预占有效期（零值 = 不限期，不得回收）。
	//
	// 到期只说明「可能失联」，**不**等于可以回收：关联任务租约仍有效时
	// 旧任务可能还在真跑（见 QuotaStore.Reclaim 契约）。
	Until time.Time
}

// QuotaRequest 一次预占请求：要多少、归谁、关联哪个任务、有效期多长。
type QuotaRequest struct {
	// Resource 资源账本键（必填）。
	Resource string
	// Amount 预占数量（>0，整单占用）。
	Amount int64
	// TaskID 关联任务（可选；空 = 未关联，回收只能按 TTL）。
	TaskID string
	// Owner 预占者（可选；留痕/排查用）。
	Owner string
	// TTL 预占有效期（0 = 不限期；不限期租约不得回收）。
	TTL time.Duration
}

// TaskLeaseOracle 任务租约存活判定（回收判据依赖任务租约闭环，#265）。
//
// 实现由宿主提供（通常包装 taskboard.Board 的租约/续期状态）；返回
// alive=true 表示任务仍被**活着**持有，此时不得回收配额。
type TaskLeaseOracle interface {
	TaskLeaseAlive(ctx context.Context, taskID string) (bool, error)
}

// QuotaUsage 一个资源账本的当前账目（观测口径，字段语义与 Quota 一致）。
type QuotaUsage struct {
	// Resource 资源账本键。
	Resource string
	// Limit 上限（未登记的资源按 0 处理——fail-closed）。
	Limit int64
	// Used 已占用。
	Used int64
}

// Remaining 余额 = Limit − Used，钳位到 0（超限不是负余额，是事实）。
func (u QuotaUsage) Remaining() int64 { return max(0, u.Limit-u.Used) }

// 回收协议的失败哨兵（判定方按 errors.Is 认人）。
var (
	// ErrLeaseNotExpired 预占尚未到期：不得回收（Worker 可能还在真跑）。
	ErrLeaseNotExpired = errors.New("scheduling: 配额预占尚未到期，不得回收")
	// ErrLeaseStillHeld 关联任务的租约仍有效：不得回收（旧任务可能还在真跑）。
	ErrLeaseStillHeld = errors.New("scheduling: 关联任务租约仍有效，不得回收配额")
	// ErrNoLeaseOracle 未注入任务租约判定面：无法确认失联，回收 fail-closed。
	ErrNoLeaseOracle = errors.New("scheduling: 未注入任务租约判定面（TaskLeaseOracle），拒绝回收")
)

// QuotaStore 多副本共享的配额原语。
//
// 实现方必须保证：
//
//   - Reserve / ReserveLease **原子**：余额判定与扣减不可分割（并发预留
//     只能有一个赢家越过上限；余额不足时**一个额度都不占**）；
//   - Release **原子且凭 Token**：同一租约至多归还一次；未知/已归还的
//     令牌报 ErrOverRelease（账不平必须显式，不静默吞）；
//   - Reclaim **凭 Token + 归属 + 有效期**：预占未到期
//     （ErrLeaseNotExpired）、关联任务租约仍有效（ErrLeaseStillHeld）、
//     或无判定面（ErrNoLeaseOracle）时**不回收**；成功回收后迟到的
//     Release 必须被 ErrOverRelease 拒收且不改账；
//   - SweepExpired 逐条走同一 Reclaim 判据，返回成功回收的条数；
//   - 上限与用量由**存储侧**持有（副本不各自记一份）。
type QuotaStore interface {
	// Reserve 预占 n 个额度（等价 ReserveLease 的无归属/无期限形态）。
	//
	// n <= 0 或 resource 为空 ⇒ ErrInvalidConfig；余额不足 ⇒
	// ErrQuotaExhausted（整单拒绝，不做部分占用）。
	Reserve(ctx context.Context, resource string, n int64) (QuotaLease, error)
	// ReserveLease 带归属与有效期的预占（推荐入口；issue #267）。
	ReserveLease(ctx context.Context, req QuotaRequest) (QuotaLease, error)
	// Release 凭租约全额归还。未知/重复的令牌 ⇒ ErrOverRelease。
	Release(ctx context.Context, lease QuotaLease) error
	// Reclaim 回收悬挂预占（凭 Token；契约见类型级说明）。
	Reclaim(ctx context.Context, lease QuotaLease) error
	// SweepExpired 扫描全部到期预占并逐条按 Reclaim 判据回收，
	// 返回实际回收条数（租约仍有效/未到期/已被归还的不计）。
	SweepExpired(ctx context.Context) (int, error)
}

// QuotaStoreOption MemQuotaStore 的装配项。
type QuotaStoreOption func(*MemQuotaStore)

// WithQuotaClock 注入存储时钟（到期判定按它；缺省 time.Now）。
// # 接缝契约（issue #369）
// @seam: no-reentry no-blocking panic-caller
//
// 不得重入（回调内不再触发读时钟的写路径；只读回查安全——时钟在 m.mu 外调用，issue #366）、
// 不得阻塞（每次到期判定前调用，必须立即返回）、panic 由调用方兜（内核不 recover）。
func WithQuotaClock(clock func() time.Time) QuotaStoreOption {
	return func(m *MemQuotaStore) {
		if clock != nil {
			m.clock = clock
		}
	}
}

// WithQuotaLeaseOracle 注入任务租约判定面（Reclaim 的归属判据）。
func WithQuotaLeaseOracle(o TaskLeaseOracle) QuotaStoreOption {
	return func(m *MemQuotaStore) {
		if o != nil {
			m.oracle = o
		}
	}
}

// MemQuotaStore QuotaStore 的进程内参考实现。经 NewMemQuotaStore 构造；
// 并发安全；只在单进程内成立（见包注释的边界声明）。
type MemQuotaStore struct {
	mu sync.Mutex

	clock  func() time.Time
	oracle TaskLeaseOracle

	limits map[string]int64
	used   map[string]int64
	leases map[string]QuotaLease // token → 预占记录（归还的真相）
	seq    uint64                // 令牌铸造序号（参考实现不承诺随机性）
}

// NewMemQuotaStore 构造参考实现：limits 是资源键 → 上限。
//
// 上限为负、资源键为空 ⇒ ErrInvalidConfig（装配期错误，不静默接受）。
// 未登记的资源按 0 上限处理（fail-closed：没有预算是拒绝，不是不限）。
func NewMemQuotaStore(limits map[string]int64, opts ...QuotaStoreOption) (*MemQuotaStore, error) {
	cp := make(map[string]int64, len(limits))
	for resource, limit := range limits {
		if resource == "" {
			return nil, errors.Wrap(ErrInvalidConfig, "scheduling: 配额资源键不能为空")
		}
		if limit < 0 {
			return nil, errors.Wrap(ErrInvalidConfig,
				"scheduling: 配额上限必须 ≥ 0（资源 "+resource+" 实得 "+strconv.FormatInt(limit, 10)+"）")
		}
		cp[resource] = limit
	}
	m := &MemQuotaStore{
		clock:  time.Now,
		limits: cp,
		used:   make(map[string]int64, len(cp)),
		leases: make(map[string]QuotaLease),
	}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// now 存储侧当前时刻。**必须在 m.mu 之外调用**：clock 是宿主回调，持锁调用
// 会让回调回查本包时二次抢锁（sync.Mutex 不可重入）⇒ 永久死锁（issue #366）。
func (m *MemQuotaStore) now() time.Time { return m.clock() }

// Reserve 原子预占（整单：余额不足一个都不占）。
func (m *MemQuotaStore) Reserve(ctx context.Context, resource string, n int64) (QuotaLease, error) {
	return m.ReserveLease(ctx, QuotaRequest{Resource: resource, Amount: n})
}

// ReserveLease 带归属与有效期的原子预占。
func (m *MemQuotaStore) ReserveLease(_ context.Context, req QuotaRequest) (QuotaLease, error) {
	if req.Resource == "" {
		return QuotaLease{}, errors.Wrap(ErrInvalidConfig, "scheduling: 配额资源键不能为空")
	}
	if req.Amount <= 0 {
		return QuotaLease{}, errors.Wrap(ErrInvalidConfig,
			"scheduling: 占用量必须为正（实得 "+strconv.FormatInt(req.Amount, 10)+"）")
	}
	if req.TTL < 0 {
		return QuotaLease{}, errors.Wrap(ErrInvalidConfig, "scheduling: 预占有效期不能为负")
	}
	var until time.Time
	if req.TTL > 0 {
		until = m.now().Add(req.TTL)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := m.limits[req.Resource] // 未登记 = 0（fail-closed）
	used := m.used[req.Resource]
	if used+req.Amount > limit {
		return QuotaLease{}, errors.Wrap(ErrQuotaExhausted,
			"scheduling: 资源 "+req.Resource+" 需要 "+strconv.FormatInt(req.Amount, 10)+
				"，剩余 "+strconv.FormatInt(max(0, limit-used), 10)+
				"，上限 "+strconv.FormatInt(limit, 10))
	}
	m.seq++
	lease := QuotaLease{
		Resource: req.Resource,
		Amount:   req.Amount,
		Token:    "quota-" + strconv.FormatUint(m.seq, 10),
		TaskID:   req.TaskID,
		Owner:    req.Owner,
		Until:    until,
	}
	m.used[req.Resource] = used + req.Amount
	m.leases[lease.Token] = lease
	return lease, nil
}

// Release 凭令牌全额归还（字段以存储记录为准；未知/重复令牌报
// ErrOverRelease，且不改账）。
func (m *MemQuotaStore) Release(_ context.Context, lease QuotaLease) error {
	if lease.Token == "" {
		return errors.Wrap(ErrInvalidConfig, "scheduling: 配额释放缺租约令牌")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.leases[lease.Token]
	if !ok {
		return errors.Wrap(ErrOverRelease,
			"scheduling: 租约令牌未知或已归还/已回收（账不平：重复释放会把余额虚增）")
	}
	m.dropLocked(stored)
	return nil
}

// Reclaim 回收悬挂预占（issue #267）。判据三条，缺一不可：
//
//   - 预占已到期（Until 非零且不晚于存储当前时刻）；
//   - 关联任务（TaskID 非空时）经 TaskLeaseOracle 确认租约**已失效**；
//     任务租约仍有效 ⇒ ErrLeaseStillHeld（旧任务可能还在真跑）；
//   - 未注入 Oracle 且租约带 TaskID ⇒ ErrNoLeaseOracle（判不了就拒绝）。
//
// 宿主判定面（Oracle）与存储时钟都在存储锁**外**调用（issue #316/#366）。
func (m *MemQuotaStore) Reclaim(ctx context.Context, lease QuotaLease) error {
	if lease.Token == "" {
		return errors.Wrap(ErrInvalidConfig, "scheduling: 配额回收缺租约令牌")
	}
	m.mu.Lock()
	stored, ok := m.leases[lease.Token]
	oracle := m.oracle
	m.mu.Unlock()
	if !ok {
		return errors.Wrap(ErrOverRelease, "scheduling: 租约令牌未知或已归还/已回收")
	}
	now := m.now()
	if stored.Until.IsZero() || now.Before(stored.Until) {
		return errors.Wrap(ErrLeaseNotExpired,
			"scheduling: 预占尚未到期，不得回收（Worker 可能还在真跑）")
	}
	taskID := stored.TaskID

	if taskID != "" {
		if oracle == nil {
			return errors.Wrap(ErrNoLeaseOracle,
				"scheduling: 预占关联任务 "+taskID+" 但未注入任务租约判定面")
		}
		alive, err := oracle.TaskLeaseAlive(ctx, taskID)
		if err != nil {
			return errors.Wrap(err, "scheduling: 任务租约存活判定失败（不回收）")
		}
		if alive {
			return errors.Wrap(ErrLeaseStillHeld,
				"scheduling: 任务 "+taskID+" 的租约仍有效，不得回收配额")
		}
	}

	// 判定期间别人可能已 Release/Reclaim：以存储记录为准，二次确认。
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.leases[lease.Token]
	if !ok {
		return errors.Wrap(ErrOverRelease, "scheduling: 租约在判定期间已被归还或回收")
	}
	m.dropLocked(cur)
	return nil
}

// SweepExpired 扫描全部到期预占并逐条按 Reclaim 判据回收。
//
// 租约仍有效/未到期/已被归还的条目跳过（不计数）；判定面故障向上传播
// （Reconciler 需要知道这轮没扫干净）。
func (m *MemQuotaStore) SweepExpired(ctx context.Context) (int, error) {
	now := m.now() // 锁外取时钟（issue #366）
	m.mu.Lock()
	candidates := make([]QuotaLease, 0, len(m.leases))
	for _, lease := range m.leases {
		if !lease.Until.IsZero() && !now.Before(lease.Until) {
			candidates = append(candidates, lease)
		}
	}
	m.mu.Unlock()

	reclaimed := 0
	for _, lease := range candidates {
		err := m.Reclaim(ctx, lease)
		switch {
		case err == nil:
			reclaimed++
		case errors.Is(err, ErrLeaseStillHeld),
			errors.Is(err, ErrLeaseNotExpired),
			errors.Is(err, ErrOverRelease):
			// 不满足回收条件或已被别的路径处理：不是扫描失败。
		default:
			return reclaimed, err
		}
	}
	return reclaimed, nil
}

// dropLocked 删除预占记录并归还用量（持锁调用）。
func (m *MemQuotaStore) dropLocked(stored QuotaLease) {
	delete(m.leases, stored.Token)
	m.used[stored.Resource] -= stored.Amount
	if m.used[stored.Resource] < 0 {
		// 防御性归零：参考实现里不可能发生（只有本锁内的 Reserve 加账），
		// 真发生了说明代码被改坏——不让它静默变成负余额。
		m.used[stored.Resource] = 0
	}
}

// Usage 取一个资源账本的当前账目（观测与测试用；未登记资源返回
// Limit=0, Used=0）。无锁外借 map，快照值与 Reserve/Release 同刻一致。
func (m *MemQuotaStore) Usage(resource string) QuotaUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return QuotaUsage{Resource: resource, Limit: m.limits[resource], Used: m.used[resource]}
}

// 编译期契约断言。
var _ QuotaStore = (*MemQuotaStore)(nil)

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
// # 边界
//
// 接口是存储中立的；MemQuotaStore 是进程内参考实现（互斥锁原子，只在
// 单进程内成立，不得当多副本账本上生产）。跨进程实现把两个方法落到
// Redis Lua / PostgreSQL 条件 UPDATE / etcd txn 上，判据不变。
package scheduling

import (
	"context"
	"strconv"
	"sync"

	"github.com/oekit/pkg/errors"
)

// QuotaLease 一次预占的凭证（由存储铸造）。
//
// Token 是归还的唯一凭据；Resource 与 Amount 是预占时的快照（便于调用方
// 记账与排查），Release **不**以它们判定——重复释放/伪造字段都绕不过
// Token 的存储记录。
type QuotaLease struct {
	// Resource 资源账本键（宿主词表：模型、工具、并发槽位等）。
	Resource string
	// Amount 本次预占的数量（>0）。
	Amount int64
	// Token 存储铸造的预占令牌（归还凭据）。
	Token string
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

// QuotaStore 多副本共享的配额原语。
//
// 实现方必须保证：
//
//   - Reserve **原子**：余额判定与扣减不可分割（并发预留只能有一个赢家
//     越过上限；余额不足时**一个额度都不占**）；
//   - Release **原子且凭 Token**：同一租约至多归还一次；未知/已归还的
//     令牌报 ErrOverRelease（账不平必须显式，不静默吞）；
//   - 上限与用量由**存储侧**持有（副本不各自记一份）。
type QuotaStore interface {
	// Reserve 预占 n 个额度，返回存储铸造的租约。
	//
	// n <= 0 或 resource 为空 ⇒ ErrInvalidConfig；余额不足 ⇒
	// ErrQuotaExhausted（整单拒绝，不做部分占用）。
	Reserve(ctx context.Context, resource string, n int64) (QuotaLease, error)
	// Release 凭租约全额归还。未知/重复的令牌 ⇒ ErrOverRelease。
	Release(ctx context.Context, lease QuotaLease) error
}

// MemQuotaStore QuotaStore 的进程内参考实现。经 NewMemQuotaStore 构造；
// 并发安全；只在单进程内成立（见包注释的边界声明）。
type MemQuotaStore struct {
	mu sync.Mutex

	limits map[string]int64
	used   map[string]int64
	leases map[string]QuotaLease // token → 预占记录（归还的真相）
	seq    uint64                // 令牌铸造序号（参考实现不承诺随机性）
}

// NewMemQuotaStore 构造参考实现：limits 是资源键 → 上限。
//
// 上限为负、资源键为空 ⇒ ErrInvalidConfig（装配期错误，不静默接受）。
// 未登记的资源按 0 上限处理（fail-closed：没有预算是拒绝，不是不限）。
func NewMemQuotaStore(limits map[string]int64) (*MemQuotaStore, error) {
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
	return &MemQuotaStore{
		limits: cp,
		used:   make(map[string]int64, len(cp)),
		leases: make(map[string]QuotaLease),
	}, nil
}

// Reserve 原子预占（整单：余额不足一个都不占）。
func (m *MemQuotaStore) Reserve(_ context.Context, resource string, n int64) (QuotaLease, error) {
	if resource == "" {
		return QuotaLease{}, errors.Wrap(ErrInvalidConfig, "scheduling: 配额资源键不能为空")
	}
	if n <= 0 {
		return QuotaLease{}, errors.Wrap(ErrInvalidConfig,
			"scheduling: 占用量必须为正（实得 "+strconv.FormatInt(n, 10)+"）")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := m.limits[resource] // 未登记 = 0（fail-closed）
	used := m.used[resource]
	if used+n > limit {
		return QuotaLease{}, errors.Wrap(ErrQuotaExhausted,
			"scheduling: 资源 "+resource+" 需要 "+strconv.FormatInt(n, 10)+
				"，剩余 "+strconv.FormatInt(max(0, limit-used), 10)+
				"，上限 "+strconv.FormatInt(limit, 10))
	}
	m.seq++
	lease := QuotaLease{
		Resource: resource,
		Amount:   n,
		Token:    "quota-" + strconv.FormatUint(m.seq, 10),
	}
	m.used[resource] = used + n
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
			"scheduling: 租约令牌未知或已归还（账不平：重复释放会把余额虚增）")
	}
	delete(m.leases, lease.Token)
	m.used[stored.Resource] -= stored.Amount
	if m.used[stored.Resource] < 0 {
		// 防御性归零：参考实现里不可能发生（只有本锁内的 Reserve 加账），
		// 真发生了说明代码被改坏——不让它静默变成负余额。
		m.used[stored.Resource] = 0
	}
	return nil
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

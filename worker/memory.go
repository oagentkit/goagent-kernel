// memory.go —— Registry 的进程内参考实现（记忆版，**单进程演示用**）。
//
// # 边界刻在类型上
//
// MemRegistry 的原子性来自**进程内互斥锁**：Register 的「顶替 + 铸令牌」、
// Heartbeat 的「资格校验 + 推后有效期」都在同一把锁内完成，但那只对同一
// 进程成立。多副本（多进程）共享注册表时，宿主要把同一套判据落到自己的
// 跨进程存储上（Redis SET NX PX / PostgreSQL 条件 UPDATE / etcd txn）——
// 拿 MemRegistry 当多副本注册表，两个进程各持一把各自的锁，
// 「同 ID 顶替」「旧令牌心跳被拒」这些判据会显示为绿，而身份页实际上
// 已经不再一致（AGENTS.md「参考实现是可被误用的资产」那条纪律的原话场景）。
//
// 它在本仓的用途只有两个：①给 Loop 的契约用例当**测试保底**；
// ②给宿主当**形态示范**——照它的四个方法写跨进程版本（契约见 Registry）。
//
// # 语义逐条
//
//   - Register：同 ID 顶替并铸新令牌（崩溃重启是正常路径，先到者从此
//     被 ErrNotRegistered 拒在门外——它由此得知自己已被顶替，而不是靠
//     幽灵状态猜）；
//   - Heartbeat：令牌不匹配、记录不在、或有效期已过 ⇒ ErrNotRegistered
//     （**不静默成功**）；TTL <= 0 ⇒ ErrInvalidConfig；
//   - Deregister：凭令牌；令牌不匹配 ⇒ ErrNotRegistered。过期的注册仍可
//     凭原令牌注销（收尾路径不该因为「刚好过期」而失败）；
//   - List：Alive 用**注册表自己的时钟**判（判据归存储侧，不归调用方）；
//     过期记录保留在原地并显式 Alive=false——与 Registry.Status 的契约
//     一致（实现也可以清理，清理与否不是契约条款）。
package worker

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"
)

// RegistryOption MemRegistry 的装配选项。
type RegistryOption func(*registryConfig)

// registryConfig MemRegistry 的装配期决策。
type registryConfig struct {
	// now 注入时钟（契约用例的过期场景要能确定性推进）。
	// nil = time.Now。
	now func() time.Time
}

// WithRegistryClock 注入注册表读时钟的方式（测试与演练用）。
//
// 生产装配不必注入：到期判定走注册表自己的时钟是契约要求，
// 多副本下这个「自己的时钟」应当是存储侧时钟（见 Registry 的说明）。
//
// 注入的回调**不会在持有 MemRegistry 内部锁时被调用**（issue #317）：回调里
// 重入本包的 Register/List/Deregister 不会与不可重入的互斥锁相撞。
//
// # 接缝契约（issue #369）
//
// @seam: no-reentry no-blocking panic-caller
//
//   - **不得重入**：回调内不得再次触发会读取注册表时钟的写路径（会递归）；
//     只读回查是安全的（时钟在 m.mu 之外被调用）。
//   - **不得阻塞**：回调在每次到期判定前被调用，必须立即返回。
//   - **panic 由调用方兜**（panic-caller）：内核不 recover 该回调的 panic。
func WithRegistryClock(now func() time.Time) RegistryOption {
	return func(c *registryConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// MemRegistry Registry 的进程内参考实现。经 NewMemRegistry 构造；
// 零值可用（内部状态惰性初始化）。并发安全。
type MemRegistry struct {
	mu sync.Mutex

	now  func() time.Time // nil = time.Now
	seq  uint64           // 令牌铸造序号（参考实现不承诺随机性）
	recs map[string]*memRegistration
}

// memRegistration 一条注册记录（令牌由注册表铸造——凭据出自判据面）。
type memRegistration struct {
	token     string
	labels    map[string]string
	expiresAt time.Time
}

// NewMemRegistry 构造参考实现。opts 见 RegistryOption。
func NewMemRegistry(opts ...RegistryOption) *MemRegistry {
	c := registryConfig{}
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	return &MemRegistry{now: c.now, recs: make(map[string]*memRegistration)}
}

// 编译期契约断言：参考实现必须满足 Registry（缺一个方法就红在这里）。
var _ Registry = (*MemRegistry)(nil)

// nowValue 读注册表时钟（nil 时钟退回 time.Now）。
//
// **必须在 m.mu 之外调用**（issue #317）：now 是宿主注入的回调，持锁调用它
// 会让「回调里重入本包」变成永久死锁。now 字段只在构造期写入，无并发写者，
// 故读取本身不需要锁。
func (m *MemRegistry) nowValue() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Register 注册一个 worker（同 ID 顶替旧注册并铸新令牌）。
func (m *MemRegistry) Register(_ context.Context, spec Spec) (Registration, error) {
	if err := spec.Validate(); err != nil {
		return Registration{}, err
	}
	now := m.nowValue() // 锁外取时钟（issue #317）
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recs == nil {
		m.recs = make(map[string]*memRegistration)
	}
	m.seq++
	rec := &memRegistration{
		token:     "reg-" + spec.ID + "-" + strconv.FormatUint(m.seq, 10),
		labels:    maps.Clone(spec.Labels),
		expiresAt: now.Add(spec.TTL),
	}
	// 同 ID 直接覆盖：先到者的令牌随旧记录一起作废（顶替语义）。
	m.recs[spec.ID] = rec
	return Registration{ID: spec.ID, Token: rec.token, ExpiresAt: rec.expiresAt}, nil
}

// Heartbeat 心跳 = 资格检查 + 延长有效期（同一把锁内完成——原子）。
func (m *MemRegistry) Heartbeat(_ context.Context, id, token string, ttl time.Duration) (time.Time, error) {
	if ttl <= 0 {
		return time.Time{}, errors.Wrap(ErrInvalidConfig,
			"worker: 心跳 TTL 必须为正——不设限的注册会让幽灵永远在线", "id", id)
	}
	now := m.nowValue() // 锁外取时钟；一次读取同时用于资格判定与续期（issue #317）
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok || r.token != token || !now.Before(r.expiresAt) {
		return time.Time{}, errors.Wrap(ErrNotRegistered,
			"worker: 心跳被拒：注册已失效、令牌不匹配或有效期已过", "id", id)
	}
	r.expiresAt = now.Add(ttl)
	return r.expiresAt, nil
}

// Deregister 注销（凭令牌；过期的注册仍可凭原令牌收尾）。
func (m *MemRegistry) Deregister(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok || r.token != token {
		return errors.Wrap(ErrNotRegistered,
			"worker: 注销被拒：注册已失效或令牌不匹配", "id", id)
	}
	delete(m.recs, id)
	return nil
}

// List 全量快照（Alive 用注册表自己的时钟判——判据归存储侧）。
// 过期记录保留并显式 Alive=false（契约允许实现自行清理；这里选择让
// 幽灵可见：运维看到「有一条过期注册」比「它凭空消失」更容易解释）。
func (m *MemRegistry) List(_ context.Context) ([]Status, error) {
	now := m.nowValue() // 锁外取时钟（issue #317）
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.recs))
	for id, r := range m.recs {
		out = append(out, Status{
			ID:        id,
			Labels:    maps.Clone(r.labels),
			ExpiresAt: r.expiresAt,
			Alive:     now.Before(r.expiresAt),
		})
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

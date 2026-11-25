// memplane.go —— 控制面的进程内参考实现。
//
// # MemPlane 同时是两面
//
//   - **控制面**：实现 Plane 契约（查询 + 三类介入）。判定逻辑由 StorePlane
//     提供（与持久化 Plane **同一份**状态机）；MemPlane 只是把 Store 固定
//     成进程内的 MemStore——单进程演示与测试的最小运行时面；
//   - **最小运行时面**：SeedRun / SeedEffect / Claim / Complete / Release
//     驱动场景（runtime.go）——控制面的介入判据必须对着真实的租约与代次
//     演示（强释之后旧令牌提交被拒、下一次认领代次严格递增）。
//
// 多副本共享控制面时用 NewStorePlane + 宿主的持久化 Store；MemStore 的
// 互斥锁只在单进程内成立（边界见 memstore.go）。
//
// 参考实现用单调计数器铸令牌（可读、可断言），**不承诺随机性**：宿主
// 实现存储时按 taskboard 的契约自行铸造（不可预测性是宿主的安全需求，
// 参考实现只演示判据）。
package controlplane

import (
	"context"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
)

// ErrAlreadyExists 目标已存在（Seed* 的重复登记；介入类动作不会报它——
// 介入对已存在目标的语义是执行，不是登记）。
var ErrAlreadyExists = errors.New("controlplane: 目标已存在")

// MemOption MemPlane / StorePlane 的装配选项。
type MemOption func(*memConfig)

// memConfig 控制面参考实现的装配期决策（只经 MemOption 显式给，零值 =
// 全部 fail-closed）。
type memConfig struct {
	// anonRead 读路径（TaskView/RunView）跳过租户 fail-closed 判定。
	// 缺省 false：读与写同一道闸（issue #197）。
	anonRead bool
}

// AllowAnonymousRead 显式放开读路径的匿名访问（TaskView/RunView 不再做
// tenant.Require）。**缺省不放行**——控制面读路径与写路径共用同一道
// fail-closed 闸（issue #197）：宿主 Authorizer 实现有 bug（对任何请求
// 返回零值 tenant.Context + nil）时，写路径被 tenant.Require 拦下、
// 读路径若不设闸就成了「同样的鉴权钩子、同样的错误，只有读放行」的口子。
//
// 这是给「健康检查式只读」场景的**显式**出口：装配时写明，装配期可见，
// 不许静默缺省开。开了它之后读路径的可归责性由宿主的 Authorizer 独自
// 承担——内核不再有第二道闸。
func AllowAnonymousRead(v bool) MemOption {
	return func(c *memConfig) { c.anonRead = v }
}

// newMemConfig 收集装配选项（nil 选项忽略）。
func newMemConfig(opts []MemOption) memConfig {
	c := memConfig{}
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	return c
}

// MemPlane 进程内参考实现。经 NewMemPlane 构造；并发安全。
//
// 判定逻辑来自 StorePlane（与持久化 Plane 同一份），状态在 MemStore。
type MemPlane struct {
	*StorePlane

	store *MemStore
}

// NewMemPlane 构造参考实现。aud 可为 nil——留痕面缺位时介入一律拒绝
// （ErrNoAuditor），这正是契约要演示的 fail-closed 形态。
// opts 可变参装配选项（见 MemOption；缺省全部 fail-closed）。
func NewMemPlane(aud Auditor, opts ...MemOption) *MemPlane {
	st := NewMemStore(aud)
	return &MemPlane{
		StorePlane: newStorePlane(st, aud, newMemConfig(opts)),
		store:      st,
	}
}

// 编译期契约断言：参考实现必须满足 Plane。
var _ Plane = (*MemPlane)(nil)

// ── 最小运行时面（委托给 MemStore；语义见 runtime.go）──

// SeedRun 建一个运行及其任务。
func (m *MemPlane) SeedRun(id RunID, status string, tasks []taskboard.Task) error {
	return m.store.SeedRun(id, status, tasks)
}

// SeedEffect 登记一个处于「结果未知」态的副作用键。
func (m *MemPlane) SeedEffect(key string, unsafe bool) error {
	return m.store.SeedEffect(key, unsafe)
}

// Claim 运行时侧认领（铸造令牌与接管代次）。
func (m *MemPlane) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, ttl time.Duration) (taskboard.Task, error) {
	return m.store.Claim(ctx, id, agent, ttl)
}

// Complete 凭 token 完成任务。
func (m *MemPlane) Complete(ctx context.Context, id taskboard.TaskID, token string) error {
	return m.store.Complete(ctx, id, token)
}

// Release 凭 token 归还认领。
func (m *MemPlane) Release(ctx context.Context, id taskboard.TaskID, token string) error {
	return m.store.Release(ctx, id, token)
}

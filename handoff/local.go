// local.go —— 进程内交接台：Desk + MemoryLedger 的薄包装。
//
// 边界与 desk.go 的注释同源：LocalDesk 只证契约可满足，对跨进程不做承诺。
// 需要跨实例/持久化形态时用 NewDesk(ledger)：两个 Desk 共用一个 Ledger
// 就是两个客户端的形状；真实落库由宿主实现 Ledger（接口见 ledger.go）。
//
// 默认行为与旧版逐条一致（现有用例是判据）：重放、终态开新代、懒式过期、
// 回执凭令牌、收回只归源方、快照按 Key 升序。
package handoff

import (
	"context"
)

// LocalDesk 进程内交接台（Store 的进程内参考实现）。
type LocalDesk struct {
	desk *Desk
}

// NewLocalDesk 构造进程内交接台（缺省行为：进程内互斥 + time.Now）。
func NewLocalDesk(opts ...DeskOption) *LocalDesk {
	return &LocalDesk{desk: NewDesk(NewMemoryLedger(), opts...)}
}

// Offer 挂出交接单（原子 + 幂等）。
func (d *LocalDesk) Offer(ctx context.Context, t Ticket) (Ticket, error) {
	return d.desk.Offer(ctx, t)
}

// Take 领取交接单：负责人在一次条件写里完成转移。
func (d *LocalDesk) Take(ctx context.Context, key Key, to Agent) (Handle, error) {
	return d.desk.Take(ctx, key, to)
}

// TakeAt 领取并校验所有权代次（旧代 → ErrStaleEpoch）。
func (d *LocalDesk) TakeAt(ctx context.Context, key Key, to Agent, expected Epoch) (Handle, error) {
	return d.desk.TakeAt(ctx, key, to, expected)
}

// Reply 交回执（凭领取令牌；迟到与令牌不匹配都拒收）。
func (d *LocalDesk) Reply(ctx context.Context, key Key, token string, r Receipt) error {
	return d.desk.Reply(ctx, key, token, r)
}

// Cancel 源方收回（仅 StateOffered；凭身份不凭内容）。
func (d *LocalDesk) Cancel(ctx context.Context, key Key, by Agent) error {
	return d.desk.Cancel(ctx, key, by)
}

// Snapshot 当前代的全部交接单（按 Key 升序，确定性）。
func (d *LocalDesk) Snapshot(ctx context.Context) ([]Ticket, error) {
	return d.desk.Snapshot(ctx)
}

// Deliveries 取回发往 addr 的回执（回执寻址的取件半边）。
func (d *LocalDesk) Deliveries(ctx context.Context, addr string) ([]AddressedReceipt, error) {
	return d.desk.Deliveries(ctx, addr)
}

// 编译期断言：LocalDesk 仍然是 Store（默认路径零回归）。
var _ Store = (*LocalDesk)(nil)

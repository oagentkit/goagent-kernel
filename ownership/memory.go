// memory.go —— Authority 的共享参考实现（issue #273）。
//
// 边界与 handoff.MemoryLedger / taskboard 的进程内参考实现同源：它证明
// 契约**可满足**，不承诺跨进程原子性。真实共享权威的表结构、事务边界与
// 选主归宿主——两个进程各持一个 MemoryAuthority 不是「共享权威」，而是
// 两台各说各话的权威机（正是本包要封掉的双主形态）。
package ownership

import (
	"context"
	"strings"
	"sync"

	"github.com/oekit/pkg/errors"
)

// MemoryAuthority 共享权威的进程内参考实现。
//
// 一把互斥锁把「判据 + 换代 + 令牌铸造」收成一次原子操作：参考实现在
// 单进程内满足 Authority 的全部实现义务；跨进程形态必须由宿主把同样的
// 义务落到共享存储的条件写里。
//
// 零值不可用，请用 @@NewMemoryAuthority@@。
type MemoryAuthority struct {
	mu      sync.Mutex
	records map[Domain]*authorityRecord
}

// authorityRecord 一个任务域的当前授权与提案状态。
type authorityRecord struct {
	// grant 当前授权（唯一权威事实）。
	grant Grant
	// pending 待确认的转移提案（nil = 无提案；提案不是授权）。
	pending *Transfer
	// last 最近一次成功确认的回执（用于回执重放幂等）。
	last *confirmedTransfer
}

// confirmedTransfer 一次已确认转移的回执（只保留最近一次）。
type confirmedTransfer struct {
	id    TransferID
	token string
	epoch Epoch
}

// NewMemoryAuthority 构造共享权威参考实现：按 mapping 初始化每个任务域
// 的当前授权（epoch 1，令牌由权威铸造）。
//
// mapping 不合法（空映射 / 空白键值）⇒ @@ErrBadMapping@@，装配期失败。
func NewMemoryAuthority(mapping RegionMap) (*MemoryAuthority, error) {
	if err := mapping.Validate(); err != nil {
		return nil, err
	}
	a := &MemoryAuthority{records: make(map[Domain]*authorityRecord, len(mapping))}
	for domain, region := range mapping {
		a.records[domain] = &authorityRecord{grant: Grant{
			Domain: domain,
			Region: region,
			Epoch:  1,
			Token:  mintToken(),
		}}
	}
	return a, nil
}

// Owner 返回任务域当前的权威授权（一致快照；未知域 ⇒ ErrUnknownDomain）。
func (a *MemoryAuthority) Owner(_ context.Context, domain Domain) (Grant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.records[domain]
	if !ok {
		return Grant{}, unknownDomain(domain)
	}
	return rec.grant, nil
}

// Mapping 返回当前区域权威映射的一致快照（转移确认后即反映新区域）。
func (a *MemoryAuthority) Mapping(context.Context) (RegionMap, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(RegionMap, len(a.records))
	for domain, rec := range a.records {
		out[domain] = rec.grant.Region
	}
	return out, nil
}

// BeginTransfer 发起转移（第一阶段）：当前权威**不变**，只记录候选代次
// 与回执令牌。同持有者的新提案取代旧提案（旧回执从此 ErrNoTransfer）。
func (a *MemoryAuthority) BeginTransfer(_ context.Context, req TransferRequest) (Transfer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	rec, ok := a.records[req.Domain]
	if !ok {
		return Transfer{}, unknownDomain(req.Domain)
	}
	if req.From != rec.grant.Region {
		return Transfer{}, errors.Wrap(ErrNotOwner, "发起方不是当前权威区域",
			"domain", string(req.Domain), "from", string(req.From), "owner", string(rec.grant.Region))
	}
	if req.Token == "" || req.Token != rec.grant.Token {
		return Transfer{}, errors.Wrap(ErrTokenMismatch, "发起方未持有当前授权令牌",
			"domain", string(req.Domain), "region", string(req.From))
	}
	if strings.TrimSpace(string(req.To)) == "" || req.To == rec.grant.Region {
		return Transfer{}, errors.Wrap(ErrBadTransfer, "目标区域不得为空或等于当前权威区域",
			"domain", string(req.Domain), "to", string(req.To))
	}

	tr := Transfer{
		ID:        mintTransferID(),
		Domain:    req.Domain,
		From:      req.From,
		To:        req.To,
		Candidate: rec.grant.Epoch + 1,
		Token:     mintReceiptToken(),
	}
	rec.pending = &tr
	return tr, nil
}

// ConfirmTransfer 确认转移（第二阶段）：目标区域凭回执令牌确认。
//
// 成功是一次原子换代：代次提升到候选值、区域切到目标、授权令牌重铸。
// 对**最近一次已确认提案**的重放幂等（返回同一授权）；未知或被取代的
// 提案一律 ErrNoTransfer，不得凭令牌猜测复活。
func (a *MemoryAuthority) ConfirmTransfer(_ context.Context, req ConfirmRequest) (Grant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 待确认提案：换代。
	for _, rec := range a.records {
		if rec.pending == nil || rec.pending.ID != req.ID {
			continue
		}
		if req.Token == "" || req.Token != rec.pending.Token {
			return Grant{}, errors.Wrap(ErrTokenMismatch, "确认令牌不是该提案的回执令牌",
				"domain", string(rec.grant.Domain), "transfer", string(req.ID))
		}
		if req.By != rec.pending.To {
			return Grant{}, errors.Wrap(ErrNotTarget, "确认方不是提案的目标区域",
				"domain", string(rec.grant.Domain), "by", string(req.By), "target", string(rec.pending.To))
		}
		if rec.pending.Candidate != rec.grant.Epoch+1 {
			// 防御性判据：提案与当前代次不接续时绝不换代（候选代次只能紧接当前代）。
			return Grant{}, errors.Wrap(ErrNoTransfer, "提案的候选代次与当前代次不接续",
				"domain", string(rec.grant.Domain), "candidate", rec.pending.Candidate, "current", rec.grant.Epoch)
		}
		grant := Grant{
			Domain: rec.grant.Domain,
			Region: rec.pending.To,
			Epoch:  rec.pending.Candidate,
			Token:  mintToken(),
		}
		rec.last = &confirmedTransfer{id: req.ID, token: req.Token, epoch: grant.Epoch}
		rec.grant = grant
		rec.pending = nil
		return grant, nil
	}

	// 已确认提案的重放：只有最近一次回执可重放，且代次必须仍是当时的代次。
	for _, rec := range a.records {
		if rec.last == nil || rec.last.id != req.ID {
			continue
		}
		if req.Token == "" || req.Token != rec.last.token {
			return Grant{}, errors.Wrap(ErrTokenMismatch, "重放令牌不是该回执的令牌",
				"domain", string(rec.grant.Domain), "transfer", string(req.ID))
		}
		if rec.grant.Epoch != rec.last.epoch {
			return Grant{}, errors.Wrap(ErrNoTransfer, "回执所属代次已被后续转移取代",
				"domain", string(rec.grant.Domain), "receiptEpoch", rec.last.epoch, "current", rec.grant.Epoch)
		}
		return rec.grant, nil
	}

	return Grant{}, errors.Wrap(ErrNoTransfer, "转移提案不存在或已被新提案取代",
		"transfer", string(req.ID))
}

// Fence 校验一次提交（判定与读取在同一把锁内；判定逻辑复用 Judge）。
func (a *MemoryAuthority) Fence(_ context.Context, c Commit) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.records[c.Domain]
	if !ok {
		return unknownDomain(c.Domain)
	}
	return Judge(rec.grant, c)
}

// unknownDomain 统一的未知任务域错误（带机器可读的域键）。
func unknownDomain(d Domain) error {
	return errors.Wrap(ErrUnknownDomain, "任务域没有权威映射", "domain", string(d))
}

// 编译期断言：参考实现满足权威契约。
var _ Authority = (*MemoryAuthority)(nil)

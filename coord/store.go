// store.go —— sideeffect.Store 的**通用适配器**：判定逻辑落在 Backend 原语之上。
//
// # 判定逻辑只有这一份
//
// Store 的 29 条契约语义（三判合一、判定顺序「已完成 > 结果未知 > 执行中 >
// 资源被占 > 授予」、三项联查、执行意图、对账占用、Unsafe 显式放行）全部
// 在本适配器里实现。宿主的后端只负责让四个原语真的原子；判定与提交之间
// 的窗口由断言护住（判定读过的键全部列进断言，被人动过就重判）。
//
// # 两份状态、两个键族
//
//   - act/<key>  Key 级：这个动作做过没有 / 结果未知没有 / 有没有一次
//     尚未裁决的尝试（执行意图）/ 有没有人在对账；
//   - res/<resource> 资源级：这个资源谁占着 / 发第几代令牌。
//
// 两条出口分开（Abandon 动 Key 级 + 资源级，Release 只动资源级）在键族上
// 直接可见。栅栏令牌住在资源记录里：它只在授予（接管）时递增，释放不清零
// ——「释放后的租约」凭旧令牌回来时，令牌仍是那一代但持有者已空，
// 三项联查的 ErrFenceStale 照样拦下。
//
// # 标记失败必须保住执行意图
//
// MarkUncertain 的契约硬要求（标记写失败时意图不得被撤）在本形态下由
// **断言事务天然成立**：标记与「撤销意图」是同一事务里的写入，
// 事务失败 = 什么都没发生，意图原样挡着。
//
// # 文件分工（结构阈值按判据的一侧切）
//
// 本文件：装配 + 施加侧（Acquire——判定最重的一个入口）；store_lease.go：
// 持有期收尾（Complete/Release/Abandon/Renew/MarkUncertain——凭凭据收尾）；
// store_reconcile.go：对账裁决（凭下游事实不凭持有权）。
package coord

import (
	"context"
	"sync"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

const (
	actKeyPrefix = "coord/act/" // Key 级状态
	resKeyPrefix = "coord/res/" // 资源级状态
)

// StoreAdapter 把任意 Backend 适配成 sideeffect.Store。
type StoreAdapter struct {
	b Backend

	// now 测试注入的时钟（UseClock；nil = 未注入）。缺省 nil 是刻意的：
	// 判定时刻一律走 resolveNow 的时间权威次序，绝不静默落回节点本地时钟
	//（与 BoardAdapter 同一条纪律，issue #194）。
	mu  sync.Mutex
	now func() time.Time
}

// NewStoreAdapter 构造 Store 适配器。
//
// 装配期检查时间权威（issue #194）：后端既不实现 ClockSource 也不声明
// SingleMachine 时返回 @@ErrNoTimeAuthority@@——占用/意图/对账的到期判定
// 同样是跨进程共享的事实，拒绝装配，而不是静默退化成节点本地时钟。
func NewStoreAdapter(b Backend) (*StoreAdapter, error) {
	if !hasTimeAuthority(b) {
		return nil, errors.Wrap(ErrNoTimeAuthority,
			"coord: StoreAdapter 拒绝装配（到期判定必须由存储时钟算，或显式声明仅单机）")
	}
	return &StoreAdapter{b: b}, nil
}

// UseClock 注入时钟（sidetest 契约套件要求：过期判定必须读可注入的时钟）。
//
// 仅限测试：契约套件的过期场景用它做确定性推进。生产路径**不得**注入本地
// 时钟——占用到期是跨进程共享的事实，时间真相在存储（issue #194）。
func (s *StoreAdapter) UseClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}

// nowAt 判定用当前时刻（时间权威次序见 resolveNow）。
func (s *StoreAdapter) nowAt(ctx context.Context) (time.Time, error) {
	s.mu.Lock()
	inj := s.now
	s.mu.Unlock()
	return resolveNow(s.b, inj, ctx)
}

// ── Store 的八个入口 ─────────────────────────────────────────────────────

// Acquire 一次原子判定。判定顺序是契约里最要紧的一行：
// **已完成 > 结果未知 > 执行中 > 资源被占 > 授予**。
// 顺序写反的代价是重复施加：把「结果未知」排到「资源被占」之后，
// 调用方会拿到 contended（以为「晚点重试就行」），重试于是发生。
func (s *StoreAdapter) Acquire(ctx context.Context, act sideeffect.Action, ttl time.Duration) (sideeffect.Outcome, sideeffect.Lease, sideeffect.Result, error) {
	ak, rk := actKeyPrefix+string(act.Key), resKeyPrefix+act.Resource
	type acq struct {
		out   sideeffect.Outcome
		lease sideeffect.Lease
		res   sideeffect.Result
	}
	r, err := casLoop(ctx, s.b, []string{ak, rk},
		func(snap map[string]Entry) (acq, []Assert, []Mutation, error) {
			a, aerr := decodeAct(snap[ak])
			if aerr != nil {
				return acq{}, nil, nil, aerr
			}
			rr, rerr := decodeRes(snap[rk])
			if rerr != nil {
				return acq{}, nil, nil, rerr
			}
			// 判定时刻取存储时钟（issue #194，时间权威次序见 resolveNow）。
			now, nerr := s.nowAt(ctx)
			if nerr != nil {
				return acq{}, nil, nil, errors.Wrap(nerr, "coord: Acquire 取存储时钟失败")
			}
			// dirty：快照判定过程中攒下的待提交改动（身份登记、过期占用清理等）。
			// 纯结论（无改动）直接返回，不走一次空事务；有改动则连同
			// 本次结论一起原子落地。
			dirty := registerIdentity(&a, act)
			commitAct := func() ([]Mutation, error) {
				am, merr := actMutation(act.Key, a)
				if merr != nil {
					return nil, merr
				}
				return []Mutation{am}, nil
			}
			// 0. 动作身份不可变（先于一切状态判定——含「已完成 → 重放」）：
			//    同 Key 不同身份不是重试，是另一个动作。
			if err := identityErr(a, act); err != nil {
				return acq{}, nil, nil, err
			}
			// 1. 已完成 → 重放（已完成的动作不再需要令牌）。
			if a.Done {
				if !dirty {
					return acq{out: sideeffect.OutcomeReplayed, res: sideeffect.Result{Payload: a.Result}}, nil, nil, nil
				}
				muts, err := commitAct()
				if err != nil {
					return acq{}, nil, nil, err
				}
				return acq{out: sideeffect.OutcomeReplayed, res: sideeffect.Result{Payload: a.Result}}, assertsFrom(snap), muts, nil
			}
			// 3. 结果未知（先于资源占用：未知态是这个 Key 头上的硬阻塞，
			//    占用只是「等一等」）。
			if a.Uncertain {
				if !dirty {
					return acq{out: sideeffect.OutcomeUncertain}, nil, nil, nil
				}
				muts, err := commitAct()
				if err != nil {
					return acq{}, nil, nil, err
				}
				return acq{out: sideeffect.OutcomeUncertain}, assertsFrom(snap), muts, nil
			}
			// 对账占用未过期 ⇒ 对账中（归入未知态一族：对账中 ≠ 已裁决，
			// 更不等于可以重做）；过期的占用在此清掉（可重入——
			// 「过期的占用被下一次 Acquire 清掉」由此成立，含后续结论路径）。
			if a.Recon != nil {
				if now.Before(a.Recon.Expires) {
					return acq{out: sideeffect.OutcomeUncertain}, nil, nil, nil
				}
				a.Recon = nil
				dirty = true
			}
			// 2. 执行意图：这次尝试还没裁决 ⇒ 不得重新授予执行权。
			if a.Intent != nil && now.Before(a.Intent.Expires) {
				if !dirty {
					return acq{out: sideeffect.OutcomeContended}, nil, nil, nil
				}
				muts, err := commitAct()
				if err != nil {
					return acq{}, nil, nil, err
				}
				return acq{out: sideeffect.OutcomeContended}, assertsFrom(snap), muts, nil
			}
			// 租约过期且未裁决 ⇒ 这次尝试的结果未知（可能已施加、账没记上）。
			if a.Intent != nil {
				stale := *a.Intent
				a.Intent = nil
				dirty = true
				if act.RetrySafety != sideeffect.RetrySafetySafe {
					a.Uncertain = true
					a.Reason = "上一次尝试的租约已过期，结果未知"
					muts, err := commitAct()
					if err != nil {
						return acq{}, nil, nil, err
					}
					// 两份投影同刻到期：资源闸若仍属本次尝试，一并作废——
					// 不清的话，没人接管时令牌没变，原持有者拿同一枚令牌仍能续期。
					if stale.Holder != "" && rr.Holder == stale.Holder && rr.Fence == stale.Fence {
						rr.Holder = ""
						rm, rerr := resMutation(act.Resource, rr)
						if rerr != nil {
							return acq{}, nil, nil, rerr
						}
						muts = append(muts, rm)
					}
					return acq{out: sideeffect.OutcomeUncertain}, assertsFrom(snap), muts, nil
				}
				// RetrySafetySafe：下游按幂等键去重，重做不会重复施加——
				// 清除该意图并继续走授予判定（落进下面的授予事务里一并写）。
			}
			// 4. 资源正被他人持有且租约未过期 → contended。
			if rr.Holder != "" && rr.Until.After(now) {
				if !dirty {
					return acq{out: sideeffect.OutcomeContended}, nil, nil, nil
				}
				muts, err := commitAct()
				if err != nil {
					return acq{}, nil, nil, err
				}
				return acq{out: sideeffect.OutcomeContended}, assertsFrom(snap), muts, nil
			}
			// 5. 授予：令牌 = 资源上已见过的最大值 + 1；意图、令牌、持有者
			// 身份在同一次事务里登记（分两次写，中间死掉就等于没写）。
			fence := rr.Fence + 1
			holder := sideeffect.MintHolder()
			rr.Fence = fence
			rr.Holder = holder
			rr.Until = now.Add(ttl)
			a.Intent = &intentRec{Resource: act.Resource, Fence: fence, Holder: holder, Expires: now.Add(ttl)}
			am, merr := actMutation(act.Key, a)
			if merr != nil {
				return acq{}, nil, nil, merr
			}
			rm, rerr := resMutation(act.Resource, rr)
			if rerr != nil {
				return acq{}, nil, nil, rerr
			}
			return acq{
				out:   sideeffect.OutcomeApplied,
				lease: sideeffect.Lease{Resource: act.Resource, Fence: sideeffect.Fence(fence), Holder: holder, TTL: ttl},
			}, assertsFrom(snap), []Mutation{am, rm}, nil
		})
	if err != nil {
		return sideeffect.OutcomeContended, sideeffect.Lease{}, sideeffect.Result{}, err
	}
	return r.out, r.lease, r.res, nil
}

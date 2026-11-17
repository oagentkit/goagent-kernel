package dispatch

import (
	"context"
	"fmt"
)

// CandidatesFunc 改派候选接缝（issue #169 原文签名）：
// FailoverCandidates(slug, tried, sameDomain)。
//
// sameDomain=true 限同域（同 route_group 一类的分组），false=跨域全集；
// 两次调用都必须排除 tried 与 slug 本身。**候选怎么来是宿主的事**——
// 注册表、能力画像、启用开关全在宿主侧，本包只消费有序列表，
// 与宿主的注册表/能力画像无关。
type CandidatesFunc func(slug string, tried map[string]bool, sameDomain bool) []string

// AttemptFunc 执行步接缝：把候选 slug 当作执行者跑一次。
//
// 返回 nil = 改派成功（宿主在此拿到产出）；返回非 nil = 该候选也失败。
// 本包不解释错误内容，也不在环内重新判档（纪律①）。
type AttemptFunc func(ctx context.Context, slug string) error

// 改派事件类别（进宿主事件面与最终消息，不进判定面）。
const (
	// EventSameDomain 同域改派（逐候选，含尝试的候选名）。
	EventSameDomain = "same_domain_failover"
	// EventCrossDomain 瞬态跨域兜底（一次）。
	EventCrossDomain = "cross_domain_failover"
)

// Event 一次改派尝试的留痕。
//
// Err 是**到目前为止的累计错误**（含 `%w（已同域改派 X 仍失败）` 说明）——
// 它是给人看的，不得回喂 Classify（见包头纪律①）。
type Event struct {
	Kind string
	// From 首次失败的执行者。
	From string
	// To 本次改派尝试的候选。
	To string
	// Err 累计错误（含改派说明）。
	Err error
}

// Request 一次改派环的输入。
type Request struct {
	// Slug 首次失败的执行者（也是候选枚举的起点）。
	Slug string
	// FirstErr **首次失败的原始错误**——判定面只读它（纪律①）。
	// 累计错误、改派说明一律不得放进这个字段。
	FirstErr error
	// SameDomain 同域改派次数上限（来自 Resolve；0=禁用同域改派）。
	SameDomain int
	// CrossDomain 是否允许瞬态跨域兜底一次（来自 Resolve）。
	CrossDomain bool
	// FailoverCandidates 候选枚举接缝（nil = 视为无候选）。
	FailoverCandidates CandidatesFunc
	// Attempt 执行步接缝（nil = 不执行任何改派，只给出档位与原错误）。
	Attempt AttemptFunc
	// OnEvent 每次改派尝试前的通知（nil = 不通知）。只在此处产出改派说明。
	//
	// 接缝契约（宿主实现，内核在改派环里同步调用）：
	//
	// @seam: no-reentry no-blocking panic-isolated
	//
	//   - no-reentry：不得回查本包（不得在通知里再调 StickyFailover）——
	//     通知是「改派说明」的产出点，重入会让一次改派嵌套在另一次里，
	//     累计错误与尝试次数的口径互相污染。
	//   - no-blocking：通知**同步**发生在改派环内，必须廉价返回，不得做
	//     I/O（写日志、落库、发网络请求都另走旁路）——慢通知会拖住整条
	//     改派路径。
	//   - panic-isolated：宿主侧兑现——本包**不做 recover**；通知回调 panic
	//     会掀翻整个改派环。
	//
	// （同结构体的 FailoverCandidates / Attempt 是同型接缝，约束相同。）
	OnEvent func(Event)
}

// Result 改派环的结论。
type Result struct {
	// Slug 改派**成功**的候选；空 = 未改派或全部仍失败。
	Slug string
	// Err 累计错误：无改派时原样返回 FirstErr，有改派时按
	// `%w（已同域改派 X 仍失败）` / `%w（已跨域兜底 X 仍失败）` 累加（给人看）。
	// 改派成功时为 nil。
	Err error
	// Class 首次失败的档位（进事件载荷：宿主据此解释「为什么没继续改派」）。
	Class Class
	// Attempts 实际执行的改派次数（同域 + 跨域）。
	Attempts int
}

// StickyFailover 改派环骨架（issue #169）：同域 N 次 → 瞬态跨域一次 → fatal 直接终态。
//
// # 次序与档位的关系
//
//  1. **进环前判一次档**（Classify(FirstErr)），环内不再判——环会改写 Err，
//     二次判定读到的文本含改派说明（纪律①）。
//  2. fatal：**不进任何改派**，原样返回（换执行者也是白跑，直接终态）。
//  3. 同域：按候选序最多 SameDomain 次，任一次成功即返回。
//  4. 跨域：仅当 CrossDomain 且档位是 transient，且只兜底**一次**。
//
// # 与宿主的分工
//
// 本包不发事件、不落库、不排序候选（能力画像排序是宿主的领域资产）：
// 事件走 OnEvent，候选走 FailoverCandidates，执行走 Attempt。
// 首次失败的环节状态落库（running→failed 之类）也在宿主侧——那是展示面，
// 不是「能不能再派」的判据。
func StickyFailover(ctx context.Context, req Request) Result {
	if req.FirstErr == nil {
		// 没有失败可处置：不判档、不改派。
		return Result{Class: ClassOther}
	}
	// 判定面只读首次失败的原始错误（纪律①）。
	class := Classify(req.FirstErr)
	res := Result{Err: req.FirstErr, Class: class}
	if class == ClassFatal {
		// 换执行者也是白跑：同域兄弟与跨域候选共享同一套凭证与权限面。
		return res
	}
	if req.Attempt == nil {
		return res
	}
	tried := map[string]bool{req.Slug: true}
	var sameCands []string
	if req.FailoverCandidates != nil {
		sameCands = req.FailoverCandidates(req.Slug, tried, true)
	}
	runErr := req.FirstErr
	for i := 0; i < req.SameDomain && i < len(sameCands); i++ {
		sib := sameCands[i]
		tried[sib] = true
		res.Attempts++
		req.notify(Event{Kind: EventSameDomain, From: req.Slug, To: sib, Err: runErr})
		if err := req.Attempt(ctx, sib); err == nil {
			res.Slug, res.Err = sib, nil
			return res
		}
		// 改派说明只进累计错误（事件/最终消息），**不回喂 Classify**（纪律①）。
		runErr = fmt.Errorf("%w（已同域改派 %s 仍失败）", runErr, sib)
	}
	if req.CrossDomain && class == ClassTransient && req.FailoverCandidates != nil {
		if cands := req.FailoverCandidates(req.Slug, tried, false); len(cands) > 0 {
			sib := cands[0]
			tried[sib] = true
			res.Attempts++
			req.notify(Event{Kind: EventCrossDomain, From: req.Slug, To: sib, Err: runErr})
			if err := req.Attempt(ctx, sib); err == nil {
				res.Slug, res.Err = sib, nil
				return res
			}
			runErr = fmt.Errorf("%w（已跨域兜底 %s 仍失败）", runErr, sib)
		}
	}
	res.Err = runErr
	return res
}

// notify 发改派事件；OnEvent 为 nil 时是空操作。
func (r Request) notify(ev Event) {
	if r.OnEvent != nil {
		r.OnEvent(ev)
	}
}

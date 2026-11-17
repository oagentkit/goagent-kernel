package steer

import (
	"context"

	"github.com/oekit/logx"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
)

// log 接管收尾留痕（logx.Delegating 懒解析宿主后端）。
var log = logx.Delegating("steer")

// Store 接管收尾的存储面（宿主实现——gorm/model 不下沉）。
//
// 四个方法就是判据的全部输入：完成环节清单、队列非空与否、报告覆写、
// 条件迁移——本包不认识任何领域字段。
//
// 接缝契约（宿主实现的存储面；一条接缝覆盖其全部方法）：
//
// @seam: no-reentry no-blocking panic-isolated
//
// no-reentry：实现内不得回查本包（不得再走本包的接管收尾路径）。内核侧兑现
// 「调用点不持锁」：本包无锁，存储调用在锁外同步发生。
// no-blocking：存储调用走**脱离 ctx**（context.WithoutCancel）——宿主不能靠
// 取消打断自己，故实现必须**有界**：不得无界等待（等锁、等别的回调）。
// panic-isolated：**宿主侧兑现**——本包不做 recover：实现 panic 会穿到调用方
// （宿主实现须自行保证不 panic）。
type Store interface {
	// ReadSteps 读环节清单（含状态；BuildSummary 只计成功环节）。
	// 读失败不中断收尾：Run 用降级正文继续（保产摘要的失败面不得扩大）。
	ReadSteps(ctx context.Context) ([]DoneStep, error)
	// HasQueue 队列是否仍有排队条目。读失败按「无排队」判定（保守侧：
	// 不开空轮）并留痕——排队消息伪装成不存在的代价是少续跑一轮，
	// 伪装成存在开空轮的代价是无人认领的僵轮，前者可补、后者是死轮。
	HasQueue(ctx context.Context) (bool, error)
	// SetReport 覆写终报（覆写前折叠旧结论留宿主；文案拼装也在宿主）。
	// 只在 Migrate 命中后由本包调用（issue #205）：终报只属于赢下仲裁的
	// 那一路，输家不得落任何报告写。
	SetReport(ctx context.Context, text string) error
	// Migrate 一次原子条件迁移：现状态 ∈ from 时迁移到 to；ok=false = 0 行
	//（他路已接手）。原子性由宿主存储保证，本包只判结果——并发投喂时
	// 恰有一个赢家，输家零行静默。
	Migrate(ctx context.Context, from []string, to string) (ok bool, err error)
}

// Spec 一次接管收尾的注入事实（文案与上限留宿主——零领域词）。
type Spec struct {
	// Header 摘要文案头（宿主注入，如标题行；空 = 只落正文）。
	Header string
	// FailReport 队列空失败收敛的报告文本（宿主注入；空 = 不写报告）。
	FailReport string
	// Caps 摘要截断上限（零值取缺省）。
	Caps Caps
}

// Outcome 接管收尾结果。
type Outcome struct {
	// Decision 走到的臂（队列感知裁决结论）。
	Decision Decision
	// Status 实际迁移到的状态；0 行（他路已接手）为空——不谎报。
	Status string
	// Hit 条件迁移是否命中（并发投喂恰有一个赢家）。
	Hit bool
}

// Run 执行接管收尾——**条件迁移只由本函数执行**（调用方不得另起协程迁移，
// 无双跑窗口；并发投喂时条件迁移恰有一个赢家，输家零行静默）。
//
// # 并发纪律：脱离 ctx
//
// 一切存储调用走 context.WithoutCancel 派生的脱离 ctx：编排 ctx 已被接管
// 信号取消，存储直查会直接中止 ⇒ 摘要与回迁全部写不进去，会话滞留 running。
//
// # 流程
//
//  1. 队列感知裁决（Decide）——队列读失败按无排队（不开空轮的保守侧）；
//  2. 失败臂（队列空）：迁 running→failed（仲裁）→ **命中才**写宿主注入的
//     失败报告，零行 = 他路已接手静默；
//  3. 回迁臂：读环节清单（失败降级正文，不中断）→ 机械拼摘要（不调 LLM）
//     → 迁 running→created（仲裁）→ **命中才**写报告，零行 = 他路已接手
//     静默（不发事件、不写报告——事件与终报都只属于已接手的那一路，
//     双发是谎报第二次，覆写是把赢家结论换成输家的）。
//
// # 写序纪律：仲裁先于终报（issue #205）
//
// 并发投喂恰有一个赢家（Migrate 条件迁移保证），而失败臂与回迁臂的报告文本
// 完全不同（FailReport vs 保产摘要）——两路裁决不一致时（队列读失败按
// fail-closed、或队列状态在两次读之间变化），报告若写在迁移之前，输家会把
// 赢家的终报覆写成相反结论。故报告一律排在 Migrate 命中之后：先仲裁、后落笔。
func Run(ctx context.Context, store Store, spec Spec) (Outcome, error) {
	// detached 写纪律（与 finalize 同款）：切断取消传播，保留父 ctx 的值。
	wctx := context.WithoutCancel(ctx)

	hasQueue, qerr := store.HasQueue(wctx)
	if qerr != nil {
		log.CtxWarn(wctx, "接管收尾队列读取失败，按无排队判定", "err", qerr)
		hasQueue = false
	}
	dec := Decide(hasQueue)
	if dec == DecisionFailConverge {
		// 接管条目已被本轮消费：按失败收敛，不开空轮。先抢仲裁，命中才落
		// 失败报告（issue #205 写序：报告早于仲裁会让输家覆写赢家终报）。
		out, err := migrate(wctx, store, dec, contract.StatusFailed)
		if err != nil || !out.Hit {
			return out, err
		}
		if spec.FailReport != "" {
			if rerr := store.SetReport(wctx, spec.FailReport); rerr != nil {
				log.CtxWarn(wctx, "接管收尾失败报告写入失败（迁移已命中）", "err", rerr)
			}
		}
		return out, nil
	}

	// 回迁臂：保产摘要（机械拼接，不调 LLM）。清单先读后迁——读是只读面，
	// 输家读了对事实无影响；写必须等仲裁命中。
	body := DegradedReport()
	if steps, serr := store.ReadSteps(wctx); serr != nil {
		log.CtxWarn(wctx, "接管摘要环节读取失败，用降级正文", "err", serr)
	} else {
		body = BuildSummary(steps, spec.Caps)
	}
	out, err := migrate(wctx, store, dec, contract.StatusCreated)
	if err != nil || !out.Hit {
		return out, err
	}
	if rerr := store.SetReport(wctx, compose(spec.Header, body)); rerr != nil {
		// 摘要写失败不阻断回迁：回迁丢的是展示面，不回迁丢的是整轮续跑。
		log.CtxWarn(wctx, "接管摘要写入失败（回迁已命中）", "err", rerr)
	}
	return out, nil
}

// migrate 一次条件迁移并按命中与否折叠 Outcome：
// 零行 = 他路已接手——Status 留空（不谎报）、不发任何后续写。
func migrate(ctx context.Context, store Store, dec Decision, to string) (Outcome, error) {
	hit, err := store.Migrate(ctx, []string{contract.StatusRunning}, to)
	if err != nil {
		return Outcome{Decision: dec}, errors.Wrap(err, "接管条件迁移")
	}
	if !hit {
		return Outcome{Decision: dec}, nil
	}
	return Outcome{Decision: dec, Status: to, Hit: true}, nil
}

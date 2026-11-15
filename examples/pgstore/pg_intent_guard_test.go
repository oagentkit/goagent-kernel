package pgstore

// pg_intent_guard_test.go —— 「部分填充」意图行在 Acquire 上的处置（issue #223）。
//
// 四个意图列（intent_resource / intent_fence / intent_holder / intent_expires）
// 各自独立可空，写入按指针写（nil → NULL）。包内写入路径总是四列同写同清，
// 但人工订正、schema 迁移、仓外写入方只写部分列都可能留下「部分填充」的行
// ——ensureIdentity 为老数据行留兼容路径，正说明列可空是预期形态。
//
// 旧实现 hasIntent() 只判 intentFence 一列，而 Acquire 的判定路径解引用
// intentExpires / intentResource：「intent_fence 有值、intent_resource 为 NULL」
// 的行会让 Acquire 在**行锁持有、事务开着**的路径上 panic——库层 panic 直接
// 杀宿主进程，连接与锁的处置完全交给上层 recover，「校验失败即整体回滚、
// 不落任何一行」的包纪律根本没走到判定就被打断。
//
// 修法后的判据（fail-closed，不 panic）：
//  1. 部分填充的行 → Acquire 显式报 ErrIntentRowIncomplete，事务整体回滚；
//  2. 残缺行不得被当成「没有在途尝试」放行执行权（错误不是放行的别名）；
//  3. 订正数据（四列补齐或全部置 NULL）后同一 Key 恢复可用——报错是
//     「先修数据」的指令，不是死刑。
//
// # 为什么必须在真实 PG 上钉
//
// 「部分填充」是**列式存储里的数据形态**，内存替身造不出来——它没有「列」，
// 也就没有「只写部分列」。判据是：真实库里存在残缺行时，入口必须显式变红
// 而不是崩。
import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

func TestPGPartialIntentRowFailsClosedNotPanic(t *testing.T) {
	dsn := requireDSN(t)
	eff, pool := freshEffectEnv(t, dsn)
	ctx := context.Background()

	act := sideeffect.Action{
		Key:      sideeffect.Key("pg-intent-guard-key"),
		Resource: "pg-intent-guard-res",
	}

	// 造行：先正常授予一次（动作行、身份登记、意图四列齐备），再把
	// intent_resource 单列置 NULL——「fence 有值、resource 为 NULL」的部分
	// 填充形态。这一步模拟的是仓外写入方，不是包内路径。
	if _, _, _, err := eff.Acquire(ctx, act, time.Minute); err != nil {
		t.Fatalf("前置授予失败: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE pgref_effect_action SET intent_resource = NULL WHERE key = $1`,
		string(act.Key)); err != nil {
		t.Fatalf("注入部分填充行失败: %v", err)
	}

	// Acquire 必须显式报错（fail-closed），不是 panic，更不是放行。
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("部分填充的意图行不得让 Acquire panic（旧实现在这里必崩）: %v", r)
		}
	}()
	// Acquire 必须显式报错（fail-closed），不是 panic，更不是放行。
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("部分填充的意图行不得让 Acquire panic（旧实现在这里必崩）: %v", r)
		}
	}()
	// 出错时返回值里的 Outcome 是事务骨架的零值（无意义），凭据以错误为准。
	_, lease, _, err := eff.Acquire(ctx, act, time.Minute)
	if err == nil {
		t.Fatal("部分填充的意图行必须显式报错（fail-closed），不得静默返回")
	}
	if !errors.Is(err, ErrIntentRowIncomplete) {
		t.Fatalf("应报 ErrIntentRowIncomplete，实得 %v", err)
	}
	if lease.Fence != 0 {
		t.Fatalf("被拒的授予不得发放令牌，实得 Fence=%d", lease.Fence)
	}

	// 事务整体回滚的实证：残缺行原样（intent_fence 仍有值、resource 仍 NULL），
	// 动作行既没被记成完成、也没被置成未知态——「校验失败即整体回滚、
	// 不落任何一行」发生在判定之前，库内没有任何行被这次失败的 Acquire 改动。
	var fence *int64
	var res *string
	var done, uncertain bool
	if err := pool.QueryRow(ctx,
		`SELECT intent_fence, intent_resource, done, uncertain
		   FROM pgref_effect_action WHERE key = $1`, string(act.Key)).
		Scan(&fence, &res, &done, &uncertain); err != nil {
		t.Fatalf("读回动作行失败: %v", err)
	}
	if fence == nil || res != nil {
		t.Fatalf("失败的 Acquire 不得改动残缺行（事务须整体回滚）：fence=%v resource=%v", fence, res)
	}
	if done || uncertain {
		t.Fatalf("失败的 Acquire 不得写任何状态行：done=%v uncertain=%v", done, uncertain)
	}

	// 订正数据（意图四列全部置 NULL = 「没有在途尝试」；当初那次授予遗留的
	// 资源持有随订正一并清掉——它是同一次作废尝试的另一个投影）后同一 Key
	// 恢复可用：报错是「先修数据」的指令，不是死刑。
	if _, err := pool.Exec(ctx,
		`UPDATE pgref_effect_action
		    SET intent_resource = NULL, intent_fence = NULL,
		        intent_holder = NULL, intent_expires = NULL
		  WHERE key = $1`, string(act.Key)); err != nil {
		t.Fatalf("订正数据行失败: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE pgref_effect_resource SET holder = NULL, until = NULL WHERE resource = $1`,
		act.Resource); err != nil {
		t.Fatalf("订正资源行失败: %v", err)
	}
	outcome, _, _, err := eff.Acquire(ctx, act, time.Minute)
	if err != nil {
		t.Fatalf("订正后的 Acquire 应恢复可用: %v", err)
	}
	if outcome != sideeffect.OutcomeApplied {
		t.Fatalf("订正后应重新授予执行权，实得 %v", outcome)
	}
}

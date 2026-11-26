package sidetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
)

// 本文件是「标记失败」一族的契约场景（v0.33.0 新增）。
//
// # 这一族判的是什么
//
// MarkUncertain **写失败**之后，存储侧必须还剩下什么。
//
// 失效形态：标记没写进去 ⇒ 存储侧既没有「完成」也没有「未知」⇒ 另一个
// worker（或重启后的自己）再来一次 Acquire，看到的是「未完成、未占用」
// ⇒ 授予执行权 ⇒ 重放一个可能已生效的动作。第一次调用的返回值只是
// 栈上的错误——跨进程什么都没留下，「失败形态是变红」退化成了
// 「红只在这一次调用的栈上」。
//
// 契约要求（Store.MarkUncertain 第 5 条）：标记写失败时，该 Key 的执行
// 意图必须原样留在存储里——它是唯一还在挡着重试的东西。本场景用一个
// 套件内部的**故障注入装饰器**模拟「存储侧此刻不可用」。
//
// # 注入点必须在宿主写入之前（issue #224）
//
// 「让宿主实现跑完、再把返回值换成失败」造不出这条失效形态：参考实现把
// MarkUncertain 的写放进一个事务，宿主跑完即已提交——随后的 Acquire 走的是
// **uncertain 分支**（标记已落地），不是场景点名的「意图还在挡着」。
// 真正的写失败（语句报错 ⇒ 事务回滚 ⇒ 存储回到标记前）在这条路径上从未
// 被制造，一个「失败路径顺手丢意图」的实现在这里也是绿的。
// 故装饰器对被注入的那次调用**什么都不写**、只返回错误——这正是
// 「写失败」的语义：什么都没进去。断言随之收紧为 OutcomeContended
// （由 Acquire 登记的执行意图挡住），而不是宽到「不是 Applied 就行」的
// 任意分支。真库上的回滚语义（一个事务里既有撤销、又有置未知）由
// examples/pgstore 的「标记写失败」用例在真实 PostgreSQL 上另行钉住。

// failingMarkStore 套件内部的故障注入装饰器：让 MarkUncertain **以写失败
// 形态失败**——什么都不写、对调用方返回错误，模拟「存储侧此刻不可用」。
// 语义拒绝（三项联查不过）由 stale_fence_cannot_mark 一族覆盖，这里注入的
// 是写失败。
type failingMarkStore struct {
	sideeffect.Store

	fired bool
}

// failMark 让下一次 MarkUncertain 以写失败形态失败（只失败一次）。
func (f *failingMarkStore) failMark() {
	f.fired = true
}

func (f *failingMarkStore) MarkUncertain(ctx context.Context, act sideeffect.Action, lease sideeffect.Lease, reason string) error {
	if f.fired {
		// 写失败的语义：存储什么都没收到。宿主实现完全不被调用——
		// 「调用方拿到错误」与「存储状态被动过」从此是两件事。
		return errors.New("注入的存储故障：标记未写入")
	}
	return f.Store.MarkUncertain(ctx, act, lease, reason)
}

// checkMarkFailureBlocksAcquire 钉住：标记写失败后，同一 Key 的下一次 Acquire
// 必须被**执行意图**挡住（issue #224：判据支点是意图分支，不是任意非 Applied）。
//
// 标记没写成 ⇒ 存储侧既没有「完成」也没有「未知」⇒ 唯一还在的东西是
// 首次 Acquire 登记的执行意图 ⇒ Acquire 必须返回 contended（未裁决的尝试
// 还在，租约未过期）。判据宽到「不是 Applied」的话，一个靠意外落入
// uncertain 分支（或任何其它分支）而通过的实现会掩盖「失败路径丢意图」。
func checkMarkFailureBlocksAcquire(st sideeffect.Store) error {
	ctx := context.Background()
	a := act("k-markfail", "res-markfail")

	wrapped := &failingMarkStore{Store: st}

	_, lease, _, err := st.Acquire(ctx, a, time.Minute)
	if err != nil {
		return fmt.Errorf("首次 Acquire 失败: %w", err)
	}
	wrapped.failMark()
	if err := wrapped.MarkUncertain(ctx, a, lease, "注入的失败"); err == nil {
		return fmt.Errorf("故障注入的标记本应失败（场景自身的注入装置失效）")
	}

	outcome, _, _, err := st.Acquire(ctx, a, time.Minute)
	if err != nil {
		return fmt.Errorf("标记失败后的 Acquire 失败: %w", err)
	}
	if outcome != sideeffect.OutcomeContended {
		return fmt.Errorf("标记写失败后必须由执行意图挡住（contended），实得 %v——"+
			"意图被丢或被换成了别的分支，重复施加的入口已打开", outcome)
	}
	return nil
}

// ── 场景入口（供 Run 调用）────────────────────────────────────────────

func runMarkFailureBlocksAcquire(t *testing.T, factory Factory) {
	t.Helper()
	st, _ := newStore(t, factory)
	if err := checkMarkFailureBlocksAcquire(st); err != nil {
		t.Fatalf("违反「标记失败后不得授予执行权」契约: %v", err)
	}
}

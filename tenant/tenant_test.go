package tenant

import (
	"context"
	"math"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/cost"
)

// 示例 scope 词表（合成数据：词面是机制侧通用表述，词表本体归宿主）。
const (
	scopeRead    = "task.read"
	scopeOperate = "task.operate"
)

func TestPresent(t *testing.T) {
	if (Context{}).Present() {
		t.Fatal("零值上下文应判定为未注入")
	}
	if !(Context{Tenant: "acme"}).Present() {
		t.Fatal("Tenant 非空应判定为已注入")
	}
	// Subject/Scopes 不参与感知判据：判据只有一个真相（Tenant 非空）
	if !(Context{Tenant: "acme", Scopes: nil}).Present() {
		t.Fatal("无 Scopes 的上下文只要 Tenant 非空就是已注入")
	}
}

func TestCanSubsetSemantics(t *testing.T) {
	c := Context{Tenant: "acme", Scopes: []string{scopeRead}}
	if !c.Can() {
		t.Fatal("required 为空 = 无要求，应恒真")
	}
	if !c.Can(scopeRead) {
		t.Fatal("授权面内的 scope 应判真")
	}
	if c.Can(scopeOperate) {
		t.Fatal("授权面外的 scope 应判假")
	}
	if c.Can(scopeRead, scopeOperate) {
		t.Fatal("多项 required 缺一即假（子集判定，不是任一命中）")
	}
	// 重复/乱序的授权面不参与规范化：判定只认「有没有」
	dup := Context{Tenant: "acme", Scopes: []string{scopeRead, scopeRead}}
	if !dup.Can(scopeRead) {
		t.Fatal("重复项不应影响包含判定")
	}
}

func TestContextPassing(t *testing.T) {
	c := Context{Tenant: "acme", Subject: "op-1", Scopes: []string{scopeRead}}
	ctx := WithContext(context.Background(), c)
	if got := From(ctx); got.Tenant != "acme" || got.Subject != "op-1" {
		t.Fatalf("注入/提取往返漂移： %+v", got)
	}
	// 不可变值：改写提取出的副本**元素**不牵动已注入的上下文。
	//
	// 必须用下标赋值而不是 append：Scopes 为 len==cap==1 时 append 必然
	// 重新分配，共享与克隆两种实现下都绿——它证明不了「不共享底层数组」。
	got := From(ctx)
	got.Scopes[0] = scopeOperate
	got.Tenant = "改写"
	if again := From(ctx); again.Tenant != "acme" || again.Scopes[0] != scopeRead {
		t.Fatalf("上下文被改写： %+v", again)
	}
	// 未注入：读路径宽容（零值，不报错）
	if (From(context.Background())).Present() {
		t.Fatal("未注入应返回零值")
	}
}

// TestImmutableScopesCannotEscalate 钉住 #376：Context 的「不可变值」承诺
// 必须对**引用字段的元素**成立——改写副本的 Scopes[0] 不得改穿回 ctx，
// 更不得把授权判定从「拒绝」变成「放行」。
func TestImmutableScopesCannotEscalate(t *testing.T) {
	scopes := []string{scopeRead}
	ctx := WithContext(context.Background(), Context{Tenant: "acme", Scopes: scopes})

	// 入口侧：注入后宿主改写自己的原切片，机制面不得看到变化。
	scopes[0] = "admin"
	if got := From(ctx); got.Scopes[0] != scopeRead {
		t.Fatalf("注入后改写宿主原切片改穿了上下文： %+v", got.Scopes)
	}
	if _, err := Require(ctx, "admin"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("宿主原切片改穿后 Require(admin) 必须仍拒绝，got %v", err)
	}

	// 出口侧（issue 的触发路径）：改写提取副本的元素（下标赋值，不是 append）。
	got := From(ctx)
	got.Scopes[0] = "admin"
	if again := From(ctx); again.Scopes[0] != scopeRead {
		t.Fatalf("改写 From 副本的元素改穿了上下文： %+v", again.Scopes)
	}
	if _, err := Require(ctx, "admin"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("改写副本后 Require(admin) 必须仍拒绝（越权放行），got %v", err)
	}

	// Require 返回的副本同样不得与 ctx 共享底层数组。
	c, err := Require(ctx, scopeRead)
	if err != nil {
		t.Fatalf("正常授权应通过： %v", err)
	}
	c.Scopes[0] = "admin"
	if _, err := Require(ctx, "admin"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("改写 Require 返回值改穿了上下文（越权放行），got %v", err)
	}
}

func TestRequireFailClosed(t *testing.T) {
	// 未注入 → ErrNoContext（写路径不静默放行）
	if _, err := Require(context.Background(), scopeRead); !errors.Is(err, ErrNoContext) {
		t.Fatalf("未注入应拒绝 ErrNoContext，got %v", err)
	}
	// 注入但授权不足 → ErrUnauthorized
	ctx := WithContext(context.Background(), Context{Tenant: "acme", Scopes: []string{scopeRead}})
	if _, err := Require(ctx, scopeOperate); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("授权不足应拒绝 ErrUnauthorized，got %v", err)
	}
	// required 为空：只判注入，不判授权
	c, err := Require(ctx)
	if err != nil || c.Tenant != "acme" {
		t.Fatalf("空 required 应只判注入： %v %+v", err, c)
	}
	// 正常通过：返回注入的副本
	c, err = Require(ctx, scopeRead)
	if err != nil || c.Subject != "" {
		t.Fatalf("正常通过应返回副本： %v %+v", err, c)
	}
}

func TestJudgeBoundaries(t *testing.T) {
	q := Quota{Max: map[cost.Kind]int64{
		cost.KindInputTokens: 100,
		cost.KindToolCalls:   0, // 归零声明
	}}
	used := func(amounts map[cost.Kind]int64) cost.Total {
		return cost.Total{Amounts: amounts}
	}
	// used == limit 不超限（上限是允许值）
	if got := q.Judge(used(map[cost.Kind]int64{cost.KindInputTokens: 100})); len(got) != 0 {
		t.Fatalf("用量等于上限不应超限： %+v", got)
	}
	// used == limit+1 超限
	got := q.Judge(used(map[cost.Kind]int64{cost.KindInputTokens: 101, cost.KindToolCalls: 1}))
	if len(got) != 2 {
		t.Fatalf("应报 2 条超限，got %d： %+v", len(got), got)
	}
	if got[0].Kind != cost.KindInputTokens || got[0].Used != 101 || got[0].Max != 100 {
		t.Fatalf("排序或明细漂移： %+v", got[0])
	}
	if got[1].Kind != cost.KindToolCalls || got[1].Max != 0 {
		t.Fatalf("归零声明应判超限： %+v", got[1])
	}
	// 未声明上限的 Kind 不设限
	if got := q.Judge(used(map[cost.Kind]int64{cost.KindOutputTokens: 1 << 40})); len(got) != 0 {
		t.Fatalf("未声明的 Kind 不设限： %+v", got)
	}
	// 归零声明 + 零用量：不超限
	if got := q.Judge(used(map[cost.Kind]int64{cost.KindToolCalls: 0})); len(got) != 0 {
		t.Fatalf("归零声明在零用量下不超限： %+v", got)
	}
	// 确定性：两次判定同一份顺序
	again := q.Judge(used(map[cost.Kind]int64{cost.KindInputTokens: 101, cost.KindToolCalls: 1}))
	if len(again) != 2 || again[0].Kind != got[0].Kind || again[1].Kind != got[1].Kind {
		t.Fatalf("判定顺序非确定： %+v vs %+v", again, got)
	}
}

func TestRemaining(t *testing.T) {
	q := Quota{Max: map[cost.Kind]int64{
		cost.KindInputTokens: 100,
		cost.KindToolCalls:   0, // 归零声明：剩余额度恒 0
	}}
	rem := q.Remaining(cost.Total{Amounts: map[cost.Kind]int64{
		cost.KindInputTokens:  30,
		cost.KindToolCalls:    1,
		cost.KindOutputTokens: 999, // 未声明的 Kind 不进结果
	}})
	if len(rem) != 2 {
		t.Fatalf("Remaining 应只含声明过的 Kind，got %+v", rem)
	}
	if rem[cost.KindInputTokens] != 70 {
		t.Fatalf("剩余额度漂移： %d", rem[cost.KindInputTokens])
	}
	if rem[cost.KindToolCalls] != 0 {
		t.Fatalf("归零声明的剩余额度应为 0，got %d", rem[cost.KindToolCalls])
	}
	// 超限 → 剩余额度钳位为 0，不为负
	rem = q.Remaining(cost.Total{Amounts: map[cost.Kind]int64{cost.KindInputTokens: 150}})
	if rem[cost.KindInputTokens] != 0 {
		t.Fatalf("超限的剩余额度应钳位为 0，got %d", rem[cost.KindInputTokens])
	}
}

// fakeSource 测试夹具：内存声明源。
type fakeSource struct {
	limits map[string]Quota
	err    error
}

func (f fakeSource) QuotaFor(tenant string) (Quota, error) {
	if f.err != nil {
		return Quota{}, f.err
	}
	return f.limits[tenant], nil // 未配置的租户返回零值 Quota（不设限）
}

func TestEnforce(t *testing.T) {
	src := fakeSource{limits: map[string]Quota{
		"acme": {Max: map[cost.Kind]int64{cost.KindInputTokens: 100}},
	}}
	used := cost.Total{Amounts: map[cost.Kind]int64{cost.KindInputTokens: 120}}

	// 未注入上下文 → ErrNoContext
	if err := Enforce(src, Context{}, used); !errors.Is(err, ErrNoContext) {
		t.Fatalf("未注入应拒绝 ErrNoContext，got %v", err)
	}
	// 超限 → ErrQuotaExceeded，明细在包装链消息里
	err := Enforce(src, Context{Tenant: "acme"}, used)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("超限应报 ErrQuotaExceeded，got %v", err)
	}
	// 未配置的租户 = 零值 Quota = 不设限
	if err := Enforce(src, Context{Tenant: "glob"}, used); err != nil {
		t.Fatalf("未配置的租户不设限，got %v", err)
	}
	// 未超限 → nil
	ok := cost.Total{Amounts: map[cost.Kind]int64{cost.KindInputTokens: 100}}
	if err := Enforce(src, Context{Tenant: "acme"}, ok); err != nil {
		t.Fatalf("用量在上限内不应报错，got %v", err)
	}
	// 声明源自身故障原样上抛（不被吞成「超限」或「放行」）
	boom := errors.New("source down")
	if err := Enforce(fakeSource{err: boom}, Context{Tenant: "acme"}, used); !errors.Is(err, boom) {
		t.Fatalf("声明源故障应原样上抛，got %v", err)
	}
}

// TestNilContextDoesNotPanic nil ctx 不得 panic（issue #215）：From 的契约是
// 「读路径不因缺上下文而崩」，Require 是 fail-closed 入口——两者在 nil ctx
// 上得到的应是零值 / ErrNoContext，而不是进程级崩溃。
func TestNilContextDoesNotPanic(t *testing.T) {
	if got := From(nil); got.Present() { //nolint:staticcheck // SA1012: nil ctx is the contract under test
		t.Fatalf("From(nil) 应返回零值，实得 %+v", got)
	}
	if _, err := Require(nil); !errors.Is(err, ErrNoContext) { //nolint:staticcheck // SA1012: 同上
		t.Fatalf("Require(nil) 应报 ErrNoContext，实得 %v", err)
	}
}

// TestJudgeFailClosedOnSaturatedAndNegativeTotals 配额判据对「溢出饱和」与
// 「负总额」必须显式判超限：负总额只可能是聚合回绕/坏账，不能当成合法余量。
func TestJudgeFailClosedOnSaturatedAndNegativeTotals(t *testing.T) {
	q := Quota{Max: map[cost.Kind]int64{cost.KindInputTokens: 1 << 60}}

	saturated := cost.Total{
		Amounts:   map[cost.Kind]int64{cost.KindInputTokens: math.MaxInt64},
		Saturated: map[cost.Kind]bool{cost.KindInputTokens: true},
	}
	got := q.Judge(saturated)
	if len(got) != 1 {
		t.Fatalf("饱和总额必须判超限，实得 %+v", got)
	}
	if got[0].Kind != cost.KindInputTokens || !got[0].Saturated {
		t.Fatalf("超限明细应带饱和标记： %+v", got[0])
	}
	if rem := q.Remaining(saturated); rem[cost.KindInputTokens] != 0 {
		t.Fatalf("饱和总额的剩余额度必须钳位为 0，实得 %d", rem[cost.KindInputTokens])
	}

	negative := cost.Total{Amounts: map[cost.Kind]int64{cost.KindInputTokens: -1}}
	if got := q.Judge(negative); len(got) != 1 {
		t.Fatalf("负总额必须判超限（负总额只能是溢出/坏账），实得 %+v", got)
	}
	if rem := q.Remaining(negative); rem[cost.KindInputTokens] != 0 {
		t.Fatalf("负总额的剩余额度必须钳位为 0（不得反向虚报余额），实得 %d", rem[cost.KindInputTokens])
	}
}

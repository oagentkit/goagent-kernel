// tenant.go —— 租户作用域的契约场景（issue #195）。
//
// # 它补的是哪一段
//
// 多租户此前只进了控制面的 fail-closed 判据：数据面的键空间与领取条件
// 没有任何租户维度，跨租户认领/覆盖在契约层不可判定。本文件的场景把
// 「租户进了存储键与领取条件」变成可跑断言：
//
//   - 跨租户任务**不可见、不可领**（List 里没有，Claim 报 ErrNotFound）；
//   - 同 id 不同租户是两条互不相干的任务（互不覆盖）；
//   - 跨租户并发认领互不干扰（各租户恰好一个赢家，他租户一个都领不到）；
//   - 他租户令牌不能收场（Release/Complete/Renew 一律 ErrNotFound）；
//   - 缺租户 fail-closed（与写路径 tenant.Require 同口径：ErrNoContext）。
//
// # 接入方式
//
// 租户作用域是**显式声明**的能力：实现构造一个「所有操作从 ctx 取租户、
// 缺失即拒」的板，再用 RunTenant 跑本套件。单租户实现不接本套件不受影响
// （默认路径零回归）；声明支持却不隔离的实现会在下面每一条上红。
package boardtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// TenantFactory 构造**租户作用域**的被测实现。
//
// 与 Factory 同形：每个子测试一份全新实现 + 套件假时钟。区别是构造出的
// 板必须从 ctx 取租户（tenant.Context），缺失时对所有方法 fail-closed。
type TenantFactory func(t *testing.T, clock *FakeClock) ClockedBoard

// RunTenant 跑租户隔离契约判据。
//
// 调用本函数 = 声明「本实现支持租户作用域」。套件对「声明了但不隔离」
// 硬失败，不降级、不跳过（与 Run 的纪律一致）。
func RunTenant(t *testing.T, factory TenantFactory) {
	t.Helper()
	t.Run("跨租户任务不可见且不可领", func(t *testing.T) {
		b, _ := newTenantBoard(t, factory)
		if err := checkTenantInvisible(tenantCtx("tenant-a"), tenantCtx("tenant-b"), b); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("同 id 不同租户互不覆盖", func(t *testing.T) {
		b, _ := newTenantBoard(t, factory)
		if err := checkTenantSameIDCoexists(tenantCtx("tenant-a"), tenantCtx("tenant-b"), b); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("跨租户并发认领互不干扰", func(t *testing.T) {
		b, _ := newTenantBoard(t, factory)
		if err := checkTenantConcurrentClaims(tenantCtx("tenant-a"), tenantCtx("tenant-b"), b); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("跨租户令牌不能收场", func(t *testing.T) {
		b, _ := newTenantBoard(t, factory)
		if err := checkTenantTokenConfined(tenantCtx("tenant-a"), tenantCtx("tenant-b"), b); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("缺租户 fail-closed", func(t *testing.T) {
		b, _ := newTenantBoard(t, factory)
		if err := checkTenantMissingFailClosed(tenantCtx("tenant-a"), b); err != nil {
			t.Fatal(err)
		}
	})
}

// tenantCtx 注入租户上下文（机制间传递的唯一通道）。
func tenantCtx(id string) context.Context {
	return tenant.WithContext(context.Background(), tenant.Context{Tenant: id})
}

// newTenantBoard 构造被测实现并注入假时钟。
func newTenantBoard(t *testing.T, factory TenantFactory) (taskboard.Board, *FakeClock) {
	t.Helper()
	clock := NewFakeClock()
	b := factory(t, clock)
	if b == nil {
		t.Fatalf("factory 返回了 nil 实现")
	}
	b.UseClock(clock.Now)
	return b, clock
}

// ── 判据核心（返回 error；变异验证直接调用）─────────────────────────────

// checkTenantInvisible A 租户的任务对 B 租户不可见、不可领。
func checkTenantInvisible(ctxA, ctxB context.Context, b taskboard.Board) error {
	if err := seedBoard(b, taskboard.Task{Tenant: "tenant-a", ID: "t1", State: taskboard.StateOpen}); err != nil {
		return err
	}
	tasks, err := b.List(ctxB)
	if err != nil {
		return errors.Wrap(err, "boardtest: B 租户 List 应当成功（空板不是错误）")
	}
	if len(tasks) != 0 {
		return fmt.Errorf("boardtest: B 租户 List 看到了 %d 条任务（A 租户的任务必须不可见）", len(tasks))
	}
	if _, err := b.Claim(ctxB, "t1", "worker-b", taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, taskboard.ErrNotFound) {
		return fmt.Errorf("boardtest: B 租户认领 A 租户的任务必须报 ErrNotFound（实得 %v）——"+
			"跨租户认领放行就是越权施加", err)
	}
	claimed, err := b.Claim(ctxA, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户自己的认领应当成功（拒绝必须只打在 B 租户上）")
	}
	if claimed.Tenant != "tenant-a" {
		return fmt.Errorf("boardtest: 认领返回的任务租户 = %q，契约要求 tenant-a（租户归属必须可观测）", claimed.Tenant)
	}
	tasks, err = b.List(ctxA)
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户 List 应当成功")
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		return fmt.Errorf("boardtest: A 租户 List 应当恰好看到自己的 1 条任务（实得 %d 条）", len(tasks))
	}
	return nil
}

// checkTenantSameIDCoexists 同 id 不同租户互不覆盖。
func checkTenantSameIDCoexists(ctxA, ctxB context.Context, b taskboard.Board) error {
	if err := seedBoard(b,
		taskboard.Task{Tenant: "tenant-a", ID: "t1", Title: "title-a", State: taskboard.StateOpen},
		taskboard.Task{Tenant: "tenant-b", ID: "t1", Title: "title-b", State: taskboard.StateOpen},
	); err != nil {
		return err
	}
	a, err := b.Claim(ctxA, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户认领应当成功")
	}
	if a.Title != "title-a" || a.Tenant != "tenant-a" {
		return fmt.Errorf("boardtest: A 租户认领到的是 (title=%q tenant=%q)，说明同 id 记录被 B 租户覆盖", a.Title, a.Tenant)
	}
	bb, err := b.Claim(ctxB, "t1", "worker-b", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: B 租户认领应当成功（同 id 是两个租户的两条任务）")
	}
	if bb.Title != "title-b" || bb.Tenant != "tenant-b" {
		return fmt.Errorf("boardtest: B 租户认领到的是 (title=%q tenant=%q)，说明同 id 记录被 A 租户覆盖", bb.Title, bb.Tenant)
	}
	// B 收场不影响 A：两条记录必须各自独立。
	if err := b.Complete(ctxB, "t1", bb.Lease.Token); err != nil {
		return errors.Wrap(err, "boardtest: B 租户完成应当成功")
	}
	tasksA, err := b.List(ctxA)
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户 List 应当成功")
	}
	if len(tasksA) != 1 || tasksA[0].State != taskboard.StateClaimed || tasksA[0].Owner != "worker-a" {
		return fmt.Errorf("boardtest: B 租户完成后 A 的同 id 任务必须原样（实得 %+v）", tasksA)
	}
	if tasksA[0].Tenant != "tenant-a" {
		return fmt.Errorf("boardtest: A 租户 List 返回的任务租户 = %q，契约要求 tenant-a", tasksA[0].Tenant)
	}
	tasksB, err := b.List(ctxB)
	if err != nil {
		return errors.Wrap(err, "boardtest: B 租户 List 应当成功")
	}
	if len(tasksB) != 1 || tasksB[0].State != taskboard.StateDone {
		return fmt.Errorf("boardtest: B 租户自己的任务应当为 done（实得 %+v）", tasksB)
	}
	if tasksB[0].Tenant != "tenant-b" {
		return fmt.Errorf("boardtest: B 租户 List 返回的任务租户 = %q，契约要求 tenant-b", tasksB[0].Tenant)
	}
	return nil
}

// checkTenantConcurrentClaims 跨租户并发认领互不干扰。
//
// 两半：① 同一 id 在两个租户上被同时并发认领——各租户内部恰好一个赢家，
// 两个租户都成功（键空间隔离，压不到对方的断言）；② 他租户 N 个 agent
// 并发抢 A 租户的任务——一个都不许成功，且失败必须是 ErrNotFound。
func checkTenantConcurrentClaims(ctxA, ctxB context.Context, b taskboard.Board) error {
	if err := seedBoard(b,
		taskboard.Task{Tenant: "tenant-a", ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{Tenant: "tenant-b", ID: "t1", State: taskboard.StateOpen},
	); err != nil {
		return err
	}
	const workers = 10
	type race struct {
		name   string
		ctx    context.Context
		agents []taskboard.AgentID
	}
	races := []race{
		{"tenant-a", ctxA, makeAgents("a", workers)},
		{"tenant-b", ctxB, makeAgents("b", workers)},
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		won   = map[string]int{}
		fails []error
	)
	start := make(chan struct{})
	for _, r := range races {
		r := r
		wg.Add(1)
		// 走 async.GoSafe（本仓禁止裸 goroutine）。
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			n, err := taskboard.ConcurrentClaim(r.ctx, b, "t1", r.agents, time.Minute)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, errors.Wrap(err, "boardtest: 租户并发认领判据无法给出结论", "tenant", r.name))
				return
			}
			won[r.name] = n
		})
	}
	close(start)
	wg.Wait()
	if len(fails) > 0 {
		return fails[0]
	}
	for _, r := range races {
		if won[r.name] != 1 {
			return fmt.Errorf("boardtest: 租户 %s 的 %d 个 agent 并发认领，成功者 = %d，契约要求恰好 1 个",
				r.name, workers, won[r.name])
		}
	}
	// 同一 id 在两个租户上各自有赢家：跨租户的键空间不得互相压断言。
	if err := seedBoard(b, taskboard.Task{Tenant: "tenant-c", ID: "t2", State: taskboard.StateOpen}); err != nil {
		return err
	}
	return tenantClaimsAllRejected(tenantCtx("tenant-d"), b, "t2", 16, taskboard.ErrNotFound)
}

// checkTenantTokenConfined 他租户的令牌不能在别的租户上收场。
func checkTenantTokenConfined(ctxA, ctxB context.Context, b taskboard.Board) error {
	if err := seedBoard(b, taskboard.Task{Tenant: "tenant-a", ID: "t1", State: taskboard.StateOpen}); err != nil {
		return err
	}
	held, err := b.Claim(ctxA, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户认领应当成功")
	}
	if err := b.Release(ctxB, "t1", held.Lease.Token); !errors.Is(err, taskboard.ErrNotFound) {
		return fmt.Errorf("boardtest: B 租户拿 A 的令牌释放必须报 ErrNotFound（实得 %v）——"+
			"放行会让跨租户的迟到释放放掉别人的闸", err)
	}
	if err := b.Complete(ctxB, "t1", held.Lease.Token); !errors.Is(err, taskboard.ErrNotFound) {
		return fmt.Errorf("boardtest: B 租户拿 A 的令牌完成必须报 ErrNotFound（实得 %v）", err)
	}
	r, err := asRenewing(b)
	if err != nil {
		return err
	}
	if _, err := r.Renew(ctxB, "t1", held.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, taskboard.ErrNotFound) {
		return fmt.Errorf("boardtest: B 租户拿 A 的令牌续期必须报 ErrNotFound（实得 %v）", err)
	}
	// A 的持有必须原样：跨租户拒绝不是「碰巧令牌不匹配」。
	renewed, err := r.Renew(ctxA, "t1", held.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户自己的续期应当成功（拒绝必须只打在 B 租户上）")
	}
	if renewed.Tenant != "tenant-a" {
		return fmt.Errorf("boardtest: 续期返回的任务租户 = %q，契约要求 tenant-a", renewed.Tenant)
	}
	return nil
}

// checkTenantMissingFailClosed 缺租户 fail-closed（与写路径同一口径）。
func checkTenantMissingFailClosed(ctxA context.Context, b taskboard.Board) error {
	if err := seedBoard(b, taskboard.Task{Tenant: "tenant-a", ID: "t1", State: taskboard.StateOpen}); err != nil {
		return err
	}
	held, err := b.Claim(ctxA, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		return errors.Wrap(err, "boardtest: A 租户认领应当成功")
	}
	r, err := asRenewing(b)
	if err != nil {
		return err
	}
	missing := []struct {
		name string
		ctx  context.Context
	}{
		{"未注入", context.Background()},
		{"注入了零值", tenant.WithContext(context.Background(), tenant.Context{})},
	}
	for _, m := range missing {
		if _, err := b.List(m.ctx); !errors.Is(err, tenant.ErrNoContext) {
			return fmt.Errorf("boardtest: %s 的上下文 List 必须报 tenant.ErrNoContext（实得 %v）", m.name, err)
		}
		if _, err := b.Claim(m.ctx, "t1", "worker-x", taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, tenant.ErrNoContext) {
			return fmt.Errorf("boardtest: %s 的上下文 Claim 必须报 tenant.ErrNoContext（实得 %v）", m.name, err)
		}
		if err := b.Release(m.ctx, "t1", held.Lease.Token); !errors.Is(err, tenant.ErrNoContext) {
			return fmt.Errorf("boardtest: %s 的上下文 Release 必须报 tenant.ErrNoContext（实得 %v）", m.name, err)
		}
		if err := b.Complete(m.ctx, "t1", held.Lease.Token); !errors.Is(err, tenant.ErrNoContext) {
			return fmt.Errorf("boardtest: %s 的上下文 Complete 必须报 tenant.ErrNoContext（实得 %v）", m.name, err)
		}
		if _, err := r.Renew(m.ctx, "t1", held.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, tenant.ErrNoContext) {
			return fmt.Errorf("boardtest: %s 的上下文 Renew 必须报 tenant.ErrNoContext（实得 %v）", m.name, err)
		}
	}
	// fail-closed 不得有副作用：A 的持有原样。
	if _, err := r.Renew(ctxA, "t1", held.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); err != nil {
		return errors.Wrap(err, "boardtest: 缺租户拒绝后 A 的持有必须原样（续期应当成功）")
	}
	return nil
}

// tenantClaimsAllRejected 让 n 个 agent 并发认领，成功者必须为 0 且失败全部是 want。
func tenantClaimsAllRejected(ctx context.Context, b taskboard.Board, id taskboard.TaskID, n int, want error) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		winners  int
		unexpect []error
	)
	start := make(chan struct{})
	for _, agent := range makeAgents("x", n) {
		agent := agent
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			_, err := b.Claim(ctx, id, agent, taskboard.ClaimRequest{TTL: time.Minute})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
			case !errors.Is(err, want):
				unexpect = append(unexpect, err)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(unexpect) > 0 {
		return errors.Wrap(unexpect[0], "boardtest: 跨租户并发认领出现非预期错误（判据无法给出结论）")
	}
	if winners != 0 {
		return fmt.Errorf("boardtest: %d 个他租户 agent 并发认领 %s，成功者 = %d，契约要求 0",
			n, id, winners)
	}
	return nil
}

// makeAgents 生成 n 个不同 agent 标识。
func makeAgents(prefix string, n int) []taskboard.AgentID {
	out := make([]taskboard.AgentID, 0, n)
	for i := range n {
		out = append(out, taskboard.AgentID(fmt.Sprintf("%s-%03d", prefix, i)))
	}
	return out
}

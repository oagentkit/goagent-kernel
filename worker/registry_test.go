// registry_test.go —— Registry 的内存参考实现（MemRegistry）的契约用例。
//
// 三条硬契约各有一组用例：多副本共享同一注册表时的**同 ID 顶替**、过期
// 幽灵的**资格拒绝**（心跳/注销凭令牌，过期即失效）、以及**旧令牌心跳
// 被拒**（顶替者的心跳延长的是新持有者的注册，不是被顶替者的）。
// 并发场景走 async.GoSafe（本仓禁止裸 goroutine）。
package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"
)

// registerAll 并发注册同一 ID，返回各次的令牌（模拟多个副本同时重启）。
func registerAll(t *testing.T, reg Registry, id string, n int) []string {
	t.Helper()
	tokens := make([]string, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		i := i
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			registration, err := reg.Register(context.Background(), Spec{ID: id, TTL: time.Minute})
			if err != nil {
				t.Errorf("Register: %v", err)
				return
			}
			tokens[i] = registration.Token
		})
	}
	close(start)
	wg.Wait()
	return tokens
}

// TestMemRegistryTakeoverAcrossReplicas 钉「多副本共享同一注册表 + 同 ID
// 顶替」：后到者铸新令牌，先到者的心跳与注销立即失效，List 只有一条记录。
func TestMemRegistryTakeoverAcrossReplicas(t *testing.T) {
	clock := newTestClock()
	reg := NewMemRegistry(WithRegistryClock(clock.Now))
	ctx := context.Background()

	first, err := reg.Register(ctx, Spec{ID: "w-1", TTL: time.Minute, Labels: map[string]string{"pool": "a"}})
	if err != nil {
		t.Fatalf("首次注册: %v", err)
	}
	second, err := reg.Register(ctx, Spec{ID: "w-1", TTL: 2 * time.Minute, Labels: map[string]string{"pool": "b"}})
	if err != nil {
		t.Fatalf("顶替注册: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("同 ID 顶替必须铸造新令牌")
	}

	// 被顶替者的心跳被拒（它由此得知身份已失，而不是靠幽灵状态猜）。
	if _, err := reg.Heartbeat(ctx, "w-1", first.Token, time.Minute); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("旧令牌心跳必须 ErrNotRegistered（实得 %v）", err)
	}
	// 新持有者的心跳成功，并推后有效期。
	until, err := reg.Heartbeat(ctx, "w-1", second.Token, 3*time.Minute)
	if err != nil {
		t.Fatalf("新令牌心跳: %v", err)
	}
	want := clock.Now().Add(3 * time.Minute)
	if !until.Equal(want) {
		t.Fatalf("心跳终点 = %v，want %v（存储时钟口径）", until, want)
	}

	// List 只有一条记录，标签是顶替者的快照。
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("顶替后注册表只应有一条记录（实得 %d）", len(list))
	}
	if list[0].ID != "w-1" || !list[0].Alive || list[0].Labels["pool"] != "b" {
		t.Fatalf("顶替后的快照漂移： %+v", list[0])
	}
	if !list[0].ExpiresAt.Equal(until) {
		t.Fatalf("快照终点 = %v，want %v", list[0].ExpiresAt, until)
	}

	// 被顶替者不能注销新持有者的注册。
	if err := reg.Deregister(ctx, "w-1", first.Token); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("旧令牌注销必须 ErrNotRegistered（实得 %v）", err)
	}
	if err := reg.Deregister(ctx, "w-1", second.Token); err != nil {
		t.Fatalf("新令牌注销: %v", err)
	}
	if list, _ := reg.List(ctx); len(list) != 0 {
		t.Fatalf("注销后注册表应为空（实得 %d 条）", len(list))
	}
}

// TestMemRegistryGhostCleanup 钉幽灵清理语义：有效期过后心跳一律被拒
// （不能靠迟到心跳复活），List 把幽灵显式标成 Alive=false；同 ID 重新
// 注册是正常路径，顶替幽灵后恢复在线。
func TestMemRegistryGhostCleanup(t *testing.T) {
	clock := newTestClock()
	reg := NewMemRegistry(WithRegistryClock(clock.Now))
	ctx := context.Background()

	ghost, err := reg.Register(ctx, Spec{ID: "w-ghost", TTL: time.Minute})
	if err != nil {
		t.Fatalf("注册: %v", err)
	}
	clock.Advance(2 * time.Minute) // 跨过有效期（没有心跳）

	if _, err := reg.Heartbeat(ctx, "w-ghost", ghost.Token, time.Minute); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("过期幽灵的心跳必须被拒（实得 %v）", err)
	}
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Alive {
		t.Fatalf("过期记录必须显式 Alive=false（实得 %+v）", list)
	}

	// 同 ID 重新注册取代幽灵（崩溃重启的正常路径）。
	fresh, err := reg.Register(ctx, Spec{ID: "w-ghost", TTL: time.Minute})
	if err != nil {
		t.Fatalf("重新注册: %v", err)
	}
	if fresh.Token == ghost.Token {
		t.Fatal("重新注册必须铸造新令牌")
	}
	if _, err := reg.Heartbeat(ctx, "w-ghost", fresh.Token, time.Minute); err != nil {
		t.Fatalf("重新注册后的心跳: %v", err)
	}
	list, _ = reg.List(ctx)
	if len(list) != 1 || !list[0].Alive {
		t.Fatalf("重新注册后应只有一条在线记录（实得 %+v）", list)
	}
}

// TestMemRegistryConcurrentTakeoverSingleWinner 钉并发顶替下的唯一性：
// 多个副本同时注册同一 ID，注册表最终只有一条记录，且**恰好一个**令牌
// 还能心跳（不是「都以为自己在线」）。
func TestMemRegistryConcurrentTakeoverSingleWinner(t *testing.T) {
	reg := NewMemRegistry(WithRegistryClock(newTestClock().Now))
	ctx := context.Background()

	const replicas = 16
	tokens := registerAll(t, reg, "w-shared", replicas)
	for _, tok := range tokens {
		if tok == "" {
			t.Fatal("并发注册出现失败（令牌为空）")
		}
	}

	winners := 0
	for _, tok := range tokens {
		if _, err := reg.Heartbeat(ctx, "w-shared", tok, time.Minute); err == nil {
			winners++
		} else if !errors.Is(err, ErrNotRegistered) {
			t.Fatalf("并发顶替后心跳只允许 ErrNotRegistered（实得 %v）", err)
		}
	}
	if winners != 1 {
		t.Fatalf("并发顶替后应恰好一个令牌可心跳（实得 %d）", winners)
	}
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "w-shared" {
		t.Fatalf("并发顶替后注册表只应有一条记录（实得 %+v）", list)
	}
}

// TestMemRegistryHeartbeatRejectsBadInput 心跳的入参判据：TTL 非正、
// 未知 ID、空令牌都不得静默成功。
func TestMemRegistryHeartbeatRejectsBadInput(t *testing.T) {
	reg := NewMemRegistry(WithRegistryClock(newTestClock().Now))
	ctx := context.Background()
	reg0, err := reg.Register(ctx, Spec{ID: "w-1", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Heartbeat(ctx, "w-1", reg0.Token, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("零 TTL 心跳必须 ErrInvalidConfig（实得 %v）", err)
	}
	if _, err := reg.Heartbeat(ctx, "w-1", "", time.Minute); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("空令牌心跳必须 ErrNotRegistered（实得 %v）", err)
	}
	if _, err := reg.Heartbeat(ctx, "w-none", reg0.Token, time.Minute); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("未知 ID 心跳必须 ErrNotRegistered（实得 %v）", err)
	}
}

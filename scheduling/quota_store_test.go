// quota_store_test.go —— 原子配额原语的契约用例（issue #189）。
//
// 三组判据：并发扣减不超卖（多副本共享同一账本）、失败不留半截
// （整单拒绝 / 非法入参不改账）、租约释放归账（凭令牌、重复释放显式拒）。
// 并发场景走 async.GoSafe（本仓禁止裸 goroutine）。
package scheduling

import (
	"context"
	"sync"
	"testing"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"
)

// TestMemQuotaStoreNoOversell 钉并发扣减：100 个调用方抢 10 个额度，
// 恰好 10 个成功，账上 Used=10、余额 0——多副本共享同一账本时上限
// 不再是「副本数 × limit」。
func TestMemQuotaStoreNoOversell(t *testing.T) {
	store, err := NewMemQuotaStore(map[string]int64{"model-x": 10})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const callers = 100
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		won     int
		refused int
		fatal   []error
		start   = make(chan struct{})
	)
	for range callers {
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			_, rerr := store.Reserve(ctx, "model-x", 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case rerr == nil:
				won++
			case errors.Is(rerr, ErrQuotaExhausted):
				refused++
			default:
				fatal = append(fatal, rerr)
			}
		})
	}
	close(start)
	wg.Wait()

	if len(fatal) > 0 {
		t.Fatalf("并发预留出现非配额类错误（判据无法给出结论）：%v", fatal[0])
	}
	if won != 10 {
		t.Fatalf("恰好 10 个预留能成功（实得 %d）", won)
	}
	if refused != callers-won {
		t.Fatalf("其余调用应报 ErrQuotaExhausted（实得 %d）", refused)
	}
	if u := store.Usage("model-x"); u.Used != 10 || u.Limit != 10 || u.Remaining() != 0 {
		t.Fatalf("账目不符： %+v（剩余 %d）", u, u.Remaining())
	}
}

// TestMemQuotaStoreFailureLeavesNoResidue 钉失败回滚：余额不足整单拒绝
// （一个额度都不占），非法入参不改账。
func TestMemQuotaStoreFailureLeavesNoResidue(t *testing.T) {
	store, err := NewMemQuotaStore(map[string]int64{"slot": 10})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := store.Reserve(ctx, "slot", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reserve(ctx, "slot", 5); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("余额不足必须 ErrQuotaExhausted（实得 %v）", err)
	}
	if u := store.Usage("slot"); u.Used != 8 {
		t.Fatalf("被拒的预留不得部分占用（实得 Used=%d）", u.Used)
	}

	// 非法量/未知资源同一口径：拒绝且不改账。
	if _, err := store.Reserve(ctx, "slot", 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("零占用量必须 ErrInvalidConfig（实得 %v）", err)
	}
	if _, err := store.Reserve(ctx, "", 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("空资源键必须 ErrInvalidConfig（实得 %v）", err)
	}
	if _, err := store.Reserve(ctx, "unregistered", 1); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("未登记资源必须 fail-closed（实得 %v）", err)
	}
	if u := store.Usage("slot"); u.Used != 8 {
		t.Fatalf("非法请求不得改账（实得 Used=%d）", u.Used)
	}

	// 余额仍够的下一笔可以成功（失败没有把账本带偏）。
	if _, err := store.Reserve(ctx, "slot", 2); err != nil {
		t.Fatalf("余额足够的预留应成功（实得 %v）", err)
	}
	if u := store.Usage("slot"); u.Used != 10 || u.Remaining() != 0 {
		t.Fatalf("最终账目应为满额： %+v", u)
	}
}

// TestMemQuotaStoreReleaseByLease 钉租约释放归账：只归还该租约的额度、
// 重复释放显式拒且不改账、字段被改写的租约仍以存储记录为准。
func TestMemQuotaStoreReleaseByLease(t *testing.T) {
	store, err := NewMemQuotaStore(map[string]int64{"slot": 10})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	leaseA, err := store.Reserve(ctx, "slot", 3)
	if err != nil {
		t.Fatal(err)
	}
	leaseB, err := store.Reserve(ctx, "slot", 4)
	if err != nil {
		t.Fatal(err)
	}
	if u := store.Usage("slot"); u.Used != 7 {
		t.Fatalf("两笔预占后 Used=7（实得 %d）", u.Used)
	}

	if err := store.Release(ctx, leaseA); err != nil {
		t.Fatalf("Release A: %v", err)
	}
	if u := store.Usage("slot"); u.Used != 4 {
		t.Fatalf("归还 A 后只应剩 B 的 4（实得 %d）", u.Used)
	}
	// 重复释放：账不平必须显式拒，且余额不被二次归还。
	if err := store.Release(ctx, leaseA); !errors.Is(err, ErrOverRelease) {
		t.Fatalf("重复释放必须 ErrOverRelease（实得 %v）", err)
	}
	if u := store.Usage("slot"); u.Used != 4 {
		t.Fatalf("被拒的重复释放不得改账（实得 Used=%d）", u.Used)
	}

	// 字段被改写但令牌是真的：以存储记录为准（凭 Token 认账）。
	tampered := leaseB
	tampered.Amount = 1
	tampered.Resource = "another"
	if err := store.Release(ctx, tampered); err != nil {
		t.Fatalf("合法令牌的释放不应因字段快照不同而失败（实得 %v）", err)
	}
	if u := store.Usage("slot"); u.Used != 0 {
		t.Fatalf("释放应归还原始预占的 4（实得 Used=%d）", u.Used)
	}
	if err := store.Release(ctx, QuotaLease{Token: "forged"}); !errors.Is(err, ErrOverRelease) {
		t.Fatalf("伪造令牌必须 ErrOverRelease（实得 %v）", err)
	}
	if err := store.Release(ctx, QuotaLease{Token: ""}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("空令牌必须 ErrInvalidConfig（实得 %v）", err)
	}

	// 归零后可以重新占满。
	if _, err := store.Reserve(ctx, "slot", 10); err != nil {
		t.Fatalf("释放归账后应能重新占满（实得 %v）", err)
	}
}

// TestNewMemQuotaStoreValidates 装配期判据：负上限/空资源键拒绝；
// 入参 map 在构造时拷贝（调用方事后改写不影响账本）。
func TestNewMemQuotaStoreValidates(t *testing.T) {
	if _, err := NewMemQuotaStore(map[string]int64{"slot": -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("负上限必须 ErrInvalidConfig（实得 %v）", err)
	}
	if _, err := NewMemQuotaStore(map[string]int64{"": 1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("空资源键必须 ErrInvalidConfig（实得 %v）", err)
	}

	limits := map[string]int64{"slot": 1}
	store, err := NewMemQuotaStore(limits)
	if err != nil {
		t.Fatal(err)
	}
	limits["slot"] = 100 // 构造后改写调用方 map
	if u := store.Usage("slot"); u.Limit != 1 {
		t.Fatalf("构造应拷贝上限（实得 Limit=%d）", u.Limit)
	}
}

// authority_test.go —— 共享权威参考实现的转移协议：
// 缺确认不可用 / 确认后换代 / 旧代拒绝 / 提案取代 / 并发确认唯一赢家。
package ownership

import (
	"context"
	"fmt"
	"sync"
	"testing"

	kerrors "github.com/oekit/pkg/errors"
)

// newAuthority 构造两份域映射的共享权威参考实现。
func newAuthority(t *testing.T) *MemoryAuthority {
	t.Helper()
	a, err := NewMemoryAuthority(RegionMap{"d-1": "region-a", "d-2": "region-c"})
	if err != nil {
		t.Fatalf("构造权威失败: %v", err)
	}
	return a
}

// TestMemoryAuthorityInitialFacts 映射是**可查事实**：每个域恰好一个权威区域、
// 代次从 1 起、令牌由权威铸造；未知域显式拒绝。
func TestMemoryAuthorityInitialFacts(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)

	g1, err := a.Owner(ctx, "d-1")
	if err != nil {
		t.Fatal(err)
	}
	if g1.Region != "region-a" || g1.Epoch != 1 || g1.Token == "" {
		t.Fatalf("初始授权应为 region-a/epoch-1/非空令牌，实得 %+v", g1)
	}
	g2, err := a.Owner(ctx, "d-2")
	if err != nil {
		t.Fatal(err)
	}
	if g2.Region != "region-c" || g2.Epoch != 1 || g2.Token == "" {
		t.Fatalf("初始授权应为 region-c/epoch-1/非空令牌，实得 %+v", g2)
	}
	if g1.Token == g2.Token {
		t.Fatal("不同域的授权令牌不得重合（凭令牌认人）")
	}

	m, err := a.Mapping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m["d-1"] != "region-a" || m["d-2"] != "region-c" || len(m) != 2 {
		t.Fatalf("映射快照与配置不一致: %v", m)
	}
	// 快照是拷贝：调用方改它不得改到权威内部。
	m["d-1"] = "region-z"
	again, _ := a.Mapping(ctx)
	if again["d-1"] != "region-a" {
		t.Fatalf("Mapping 必须返回快照拷贝，实得 %v", again)
	}

	if _, err := a.Owner(ctx, "d-9"); !kerrors.Is(err, ErrUnknownDomain) {
		t.Fatalf("未知域必须 ErrUnknownDomain，实得 %v", err)
	}
	if _, err := NewMemoryAuthority(RegionMap{}); !kerrors.Is(err, ErrBadMapping) {
		t.Fatalf("空映射必须构造失败（ErrBadMapping），实得 %v", err)
	}
}

// TestAuthorityRejectsUnknownAndForgedReceipt 未知域与伪造/重放令牌的边界。
func TestAuthorityRejectsUnknownAndForgedReceipt(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)

	if err := a.Fence(ctx, Commit{Domain: "d-9", Region: "region-a", Epoch: 1, Token: "x"}); !kerrors.Is(err, ErrUnknownDomain) {
		t.Fatalf("未登记域的提交必须 ErrUnknownDomain，实得 %v", err)
	}
	if _, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-9", From: "region-a", Token: "x", To: "region-b"}); !kerrors.Is(err, ErrUnknownDomain) {
		t.Fatalf("未登记域不得发起转移，实得 %v", err)
	}

	old, _ := a.Owner(ctx, "d-1")
	tr, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-a", Token: old.Token, To: "region-b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: "forged", By: "region-b"}); !kerrors.Is(err, ErrTokenMismatch) {
		t.Fatalf("伪造回执令牌必须 ErrTokenMismatch，实得 %v", err)
	}
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-b"}); err != nil {
		t.Fatal(err)
	}
	// 重放时令牌仍必须是该回执的令牌（幂等不等于免凭据）。
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: "forged", By: "region-b"}); !kerrors.Is(err, ErrTokenMismatch) {
		t.Fatalf("重放伪造令牌必须 ErrTokenMismatch，实得 %v", err)
	}
}

// TestTransferRequiresConfirmation 两阶段的核心判据：
// BeginTransfer 之后当前权威**不变**；候选代次在确认前一律不可提交；
// 确认必须由目标区域凭回执令牌完成；确认成功后新代次生效、旧代次被拒。
func TestTransferRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)

	old, err := a.Owner(ctx, "d-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Fence(ctx, old.Commit()); err != nil {
		t.Fatalf("当前持有者的提交应通过: %v", err)
	}

	tr, err := a.BeginTransfer(ctx, TransferRequest{
		Domain: "d-1", From: "region-a", Token: old.Token, To: "region-b",
	})
	if err != nil {
		t.Fatalf("当前持有者发起转移应成功: %v", err)
	}
	if tr.ID == "" || tr.Token == "" {
		t.Fatalf("转移必须带提案标识与确认令牌: %+v", tr)
	}
	if tr.From != "region-a" || tr.To != "region-b" || tr.Domain != "d-1" {
		t.Fatalf("转移提案字段不符: %+v", tr)
	}
	if tr.Candidate != old.Epoch+1 {
		t.Fatalf("候选代次必须是当前代次 + 1（%d），实得 %d", old.Epoch+1, tr.Candidate)
	}

	// ── 确认前：权威不变（提案不是授权）──
	cur, err := a.Owner(ctx, "d-1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Region != "region-a" || cur.Epoch != old.Epoch || cur.Token != old.Token {
		t.Fatalf("未确认的提案不得改变权威: %+v", cur)
	}
	if m, _ := a.Mapping(ctx); m["d-1"] != "region-a" {
		t.Fatalf("未确认的提案不得改变映射: %v", m)
	}
	// 目标区域拿候选代次 + 回执令牌提交：必须报「未确认」。
	if err := a.Fence(ctx, Grant{Domain: "d-1", Region: "region-b", Epoch: tr.Candidate, Token: tr.Token}.Commit()); !kerrors.Is(err, ErrEpochUnconfirmed) {
		t.Fatalf("确认前的候选代次必须 ErrEpochUnconfirmed，实得 %v", err)
	}
	// 目标区域拿当前代次 + 旧令牌提交：区域不是权威区域。
	if err := a.Fence(ctx, Grant{Domain: "d-1", Region: "region-b", Epoch: old.Epoch, Token: old.Token}.Commit()); !kerrors.Is(err, ErrNotOwner) {
		t.Fatalf("确认前目标区域不得凭旧代次提交，实得 %v", err)
	}
	// 旧持有者仍可提交（确认前没有第二个权威）。
	if err := a.Fence(ctx, old.Commit()); err != nil {
		t.Fatalf("确认前旧持有者仍是唯一权威: %v", err)
	}

	// ── 确认回执必须自洽：错令牌 / 错确认方都不得推进代次 ──
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: "forged", By: "region-b"}); !kerrors.Is(err, ErrTokenMismatch) {
		t.Fatalf("伪造确认令牌必须 ErrTokenMismatch，实得 %v", err)
	}
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-c"}); !kerrors.Is(err, ErrNotTarget) {
		t.Fatalf("非目标区域的确认必须 ErrNotTarget，实得 %v", err)
	}
	if cur, _ := a.Owner(ctx, "d-1"); cur.Epoch != old.Epoch || cur.Region != "region-a" {
		t.Fatalf("失败的确认不得推进代次: %+v", cur)
	}

	// ── 正确确认：代次提升、新区域成为唯一权威 ──
	grant, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-b"})
	if err != nil {
		t.Fatalf("目标区域凭回执令牌确认应成功: %v", err)
	}
	if grant.Region != "region-b" || grant.Epoch != tr.Candidate || grant.Token == "" {
		t.Fatalf("确认后的授权不符: %+v", grant)
	}
	if grant.Token == old.Token || grant.Token == tr.Token {
		t.Fatal("新授权必须铸造新令牌（不得复用旧授权令牌或转移回执令牌）")
	}
	if err := a.Fence(ctx, grant.Commit()); err != nil {
		t.Fatalf("新持有者的提交应通过: %v", err)
	}
	if err := a.Fence(ctx, old.Commit()); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("旧代提交必须 ErrStaleEpoch，实得 %v", err)
	}
	// 旧区域 + 新代次 + 旧令牌：代次与区域都对不上，报区域不符（先判代次）。
	if err := a.Fence(ctx, Commit{Domain: "d-1", Region: "region-b", Epoch: grant.Epoch, Token: old.Token}); !kerrors.Is(err, ErrTokenMismatch) {
		t.Fatalf("新代次 + 旧令牌必须 ErrTokenMismatch，实得 %v", err)
	}

	// ── 回执重放幂等：同一提案再次确认返回同一授权，不推进代次 ──
	replay, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-b"})
	if err != nil {
		t.Fatalf("已确认提案的重放必须幂等成功: %v", err)
	}
	if replay.Epoch != grant.Epoch || replay.Token != grant.Token || replay.Region != grant.Region {
		t.Fatalf("重放不得铸造新授权: first=%+v replay=%+v", grant, replay)
	}

	// 确认后再次发起新生：必须用新授权令牌（旧令牌不再有发起权）。
	if _, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-a", Token: old.Token, To: "region-c"}); !kerrors.Is(err, ErrNotOwner) {
		t.Fatalf("旧区域不得再发起转移（ErrNotOwner），实得 %v", err)
	}
	if _, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-b", Token: old.Token, To: "region-c"}); !kerrors.Is(err, ErrTokenMismatch) {
		t.Fatalf("新区域持旧令牌不得发起转移（ErrTokenMismatch），实得 %v", err)
	}
}

// TestTransferSupersede 提案被同持有者的新提案取代：旧回执作废，
// 新提案仍以「当前代次 + 1」为候选（未确认的提案不消耗代次）。
func TestTransferSupersede(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)
	old, _ := a.Owner(ctx, "d-1")

	first, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-a", Token: old.Token, To: "region-b"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-a", Token: old.Token, To: "region-c"})
	if err != nil {
		t.Fatalf("同持有者的新提案应取代旧提案: %v", err)
	}
	if second.Candidate != first.Candidate {
		t.Fatalf("未确认的提案不得消耗代次：first=%d second=%d", first.Candidate, second.Candidate)
	}
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: first.ID, Token: first.Token, By: "region-b"}); !kerrors.Is(err, ErrNoTransfer) {
		t.Fatalf("被取代的提案必须 ErrNoTransfer，实得 %v", err)
	}
	grant, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: second.ID, Token: second.Token, By: "region-c"})
	if err != nil {
		t.Fatalf("新提案确认应成功: %v", err)
	}
	if grant.Region != "region-c" || grant.Epoch != second.Candidate {
		t.Fatalf("确认结果不符: %+v", grant)
	}
	// 已被取代的旧回执在换代后仍不得复活。
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: first.ID, Token: first.Token, By: "region-b"}); !kerrors.Is(err, ErrNoTransfer) {
		t.Fatalf("被取代的旧回执不得复活，实得 %v", err)
	}
	// 发起转移的请求自检：空目标 / 目标等于当前权威区域。
	if _, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-c", Token: grant.Token, To: ""}); !kerrors.Is(err, ErrBadTransfer) {
		t.Fatalf("空目标必须 ErrBadTransfer，实得 %v", err)
	}
	if _, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-c", Token: grant.Token, To: "region-c"}); !kerrors.Is(err, ErrBadTransfer) {
		t.Fatalf("目标等于当前权威区域必须 ErrBadTransfer，实得 %v", err)
	}
}

// TestEpochRegressionRejected 代次只增不回退：换代后更小代次被拒，
// 更大但未确认的代次也被拒（「未来代次」不能靠猜赢）。
func TestEpochRegressionRejected(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)
	old, _ := a.Owner(ctx, "d-1")
	tr, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-a", Token: old.Token, To: "region-b"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-b"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ep   Epoch
		want error
	}{
		{"回退到上一代", grant.Epoch - 1, ErrStaleEpoch},
		{"回退到初始代", 1, ErrStaleEpoch},
		{"越到下一代（未确认）", grant.Epoch + 1, ErrEpochUnconfirmed},
		{"越到很远（未确认）", grant.Epoch + 100, ErrEpochUnconfirmed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := grant.Commit()
			c.Epoch = tc.ep
			if err := a.Fence(ctx, c); !kerrors.Is(err, tc.want) {
				t.Fatalf("期望 %v，实得 %v", tc.want, err)
			}
		})
	}
}

// TestConcurrentTransferSingleWinner 并发发起转移 + 逐一提案确认：
// 成功确认的提案必须**恰好一个**（CAS 语义；提案取代不是「两个都算」）。
func TestConcurrentTransferSingleWinner(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)
	old, _ := a.Owner(ctx, "d-1")

	const n = 16
	transfers := make([]Transfer, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			transfers[i], errs[i] = a.BeginTransfer(ctx, TransferRequest{
				Domain: "d-1", From: "region-a", Token: old.Token,
				To: Region(fmt.Sprintf("region-%d", i)),
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发提案 %d 失败: %v", i, err)
		}
		if transfers[i].Candidate != old.Epoch+1 {
			t.Fatalf("提案 %d 的候选代次必须仍是 %d（提案不消耗代次），实得 %d",
				i, old.Epoch+1, transfers[i].Candidate)
		}
	}

	winners := 0
	for _, tr := range transfers {
		if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: tr.To}); err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("并发提案确认必须恰好一个赢家（CAS），实得 %d", winners)
	}
	cur, err := a.Owner(ctx, "d-1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Epoch != old.Epoch+1 {
		t.Fatalf("只允许一次确认推进代次（%d），实得 %+v", old.Epoch+1, cur)
	}
}

// local_test.go —— 单区域退化路径：不接共享权威、不做转移确认，
// 本区域即权威；判定口径与共享权威逐条一致（同一条 Judge）。
package ownership

import (
	"context"
	"testing"

	kerrors "github.com/oekit/pkg/errors"
)

// TestLocalSingleRegionDegradation 单区域部署：任何任务域都归本区域，
// 提交走同一条栅栏判据（epoch 1 + 权威铸造的令牌）。
func TestLocalSingleRegionDegradation(t *testing.T) {
	ctx := context.Background()
	local, err := NewLocal("region-a")
	if err != nil {
		t.Fatalf("单区域权威构造应成功: %v", err)
	}

	g, err := local.Owner(ctx, "d-any")
	if err != nil {
		t.Fatalf("开放域集合下任意任务域都应有本地权威: %v", err)
	}
	if g.Domain != "d-any" || g.Region != "region-a" || g.Epoch != 1 || g.Token == "" {
		t.Fatalf("本地授权不符: %+v", g)
	}
	// 同一域重复查询返回同一授权（令牌不得每次重铸，否则「凭令牌认人」失效）。
	again, _ := local.Owner(ctx, "d-any")
	if again.Token != g.Token || again.Epoch != g.Epoch {
		t.Fatalf("重复查询不得重铸授权: first=%+v again=%+v", g, again)
	}
	// 开放域集合没有可预枚举的映射；已查询过的域会成为可查事实。
	if m, _ := local.Mapping(ctx); m["d-any"] != "region-a" {
		t.Fatalf("已授予的开放域应出现在映射快照里，实得 %v", m)
	}
	// Fence 只读：未查询过的开放域不会因一笔提交而「顺手」获得授权。
	if err := local.Fence(ctx, Commit{Domain: "d-never", Region: "region-a", Epoch: 1, Token: "x"}); !kerrors.Is(err, ErrUnknownDomain) {
		t.Fatalf("未授予的开放域提交必须 ErrUnknownDomain，实得 %v", err)
	}
	if m, _ := local.Mapping(ctx); len(m) != 1 {
		t.Fatalf("Fence 不得产生授权副作用（映射应仍只有 d-any），实得 %v", m)
	}

	if err := local.Fence(ctx, g.Commit()); err != nil {
		t.Fatalf("本地提交应通过: %v", err)
	}
	if err := local.Fence(ctx, Commit{Domain: "d-any", Region: "region-b", Epoch: 1, Token: g.Token}); !kerrors.Is(err, ErrNotOwner) {
		t.Fatalf("其他区域的提交必须 ErrNotOwner，实得 %v", err)
	}
	if err := local.Fence(ctx, Commit{Domain: "d-any", Region: "region-a", Epoch: 0, Token: g.Token}); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("epoch 回退必须 ErrStaleEpoch，实得 %v", err)
	}
	if err := local.Fence(ctx, Commit{Domain: "d-any", Region: "region-a", Epoch: 2, Token: g.Token}); !kerrors.Is(err, ErrEpochUnconfirmed) {
		t.Fatalf("未确认的下一代必须 ErrEpochUnconfirmed，实得 %v", err)
	}
	if err := local.Fence(ctx, Commit{Domain: "d-any", Region: "region-a", Epoch: 1, Token: "forged"}); !kerrors.Is(err, ErrTokenMismatch) {
		t.Fatalf("伪造令牌必须 ErrTokenMismatch，实得 %v", err)
	}

	// 单区域没有跨区域转移：请求显式拒绝，不静默不做。
	if _, err := local.BeginTransfer(ctx, TransferRequest{Domain: "d-any", From: "region-a", Token: g.Token, To: "region-b"}); !kerrors.Is(err, ErrSingleRegion) {
		t.Fatalf("单区域权威必须拒绝转移（ErrSingleRegion），实得 %v", err)
	}
	if _, err := local.ConfirmTransfer(ctx, ConfirmRequest{ID: "x", Token: "y", By: "region-b"}); !kerrors.Is(err, ErrSingleRegion) {
		t.Fatalf("单区域权威必须拒绝确认（ErrSingleRegion），实得 %v", err)
	}

	if _, err := NewLocal(""); !kerrors.Is(err, ErrBadMapping) {
		t.Fatalf("空区域必须构造失败（ErrBadMapping），实得 %v", err)
	}
}

// TestLocalRestrictedDomains 显式域集合：映射可枚举、域外不可提交。
func TestLocalRestrictedDomains(t *testing.T) {
	ctx := context.Background()
	local, err := NewLocalFor("region-a", "d-1", "d-2")
	if err != nil {
		t.Fatalf("限定域集合的本地权威构造应成功: %v", err)
	}
	m, err := local.Mapping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["d-1"] != "region-a" || m["d-2"] != "region-a" {
		t.Fatalf("限定域集合的映射应可枚举，实得 %v", m)
	}
	g, err := local.Owner(ctx, "d-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Fence(ctx, g.Commit()); err != nil {
		t.Fatalf("域内提交应通过: %v", err)
	}
	if _, err := local.Owner(ctx, "d-3"); !kerrors.Is(err, ErrUnknownDomain) {
		t.Fatalf("域外任务域必须 ErrUnknownDomain，实得 %v", err)
	}
	if err := local.Fence(ctx, Commit{Domain: "d-3", Region: "region-a", Epoch: 1, Token: g.Token}); !kerrors.Is(err, ErrUnknownDomain) {
		t.Fatalf("域外提交必须 ErrUnknownDomain，实得 %v", err)
	}

	if _, err := NewLocalFor("region-a"); !kerrors.Is(err, ErrBadMapping) {
		t.Fatalf("限定域集合为空时必须 ErrBadMapping，实得 %v", err)
	}
	if _, err := NewLocalFor("region-a", ""); !kerrors.Is(err, ErrBadMapping) {
		t.Fatalf("空白任务域必须 ErrBadMapping，实得 %v", err)
	}
}

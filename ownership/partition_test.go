// partition_test.go —— 分区场景的验收判据（issue #273）：
// 旧区域在分区期间保留旧代次；共享权威确认新代次后，旧区域的一切提交被**拒绝**，
// 不是「尽量拒绝」。
//
// 场景顺序（与 issue 的「转移确认」条款对应）：
//  1. region-a 是 d-1 的唯一权威，并已把确认回执交给 region-b；
//  2. 分区发生：region-a 继续以为自己持有当前代次；
//  3. region-b 经共享权威完成确认，成为新权威；
//  4. region-a 恢复后的提交凭旧代次被 fence 拒绝；region-b 的提交通过。
package ownership

import (
	"context"
	"testing"

	kerrors "github.com/oekit/pkg/errors"
)

// TestPartitionStaleOwnerCannotCommit 分区双主：两个区域都认为自己拥有 d-1。
func TestPartitionStaleOwnerCannotCommit(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)

	// 1. region-a 当前权威；转移提议已交付 region-b（回执令牌在 b 手里）。
	grantA, err := a.Owner(ctx, "d-1")
	if err != nil {
		t.Fatal(err)
	}
	tr, err := a.BeginTransfer(ctx, TransferRequest{
		Domain: "d-1", From: "region-a", Token: grantA.Token, To: "region-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 分区：region-a 的记忆停在 grantA —— 它不知道确认已经发生。
	//    这里不模拟「权威不可达」的传输层失败（那是宿主接缝的失败面）；
	//    本用例钉住的是：即使权威可达，旧代次也**不合法**。

	// 3. region-b 到共享权威完成确认。
	grantB, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-b"})
	if err != nil {
		t.Fatalf("region-b 确认应成功（权威可达）: %v", err)
	}

	// 4a. region-b 的提交通过；region-a 的旧代次提交必须被拒。
	if err := a.Fence(ctx, grantB.Commit()); err != nil {
		t.Fatalf("新权威的提交应通过: %v", err)
	}
	if err := a.Fence(ctx, grantA.Commit()); !kerrors.Is(err, ErrStaleEpoch) {
		t.Fatalf("分区期间旧区域的提交必须 ErrStaleEpoch，实得 %v", err)
	}

	// 4b. 旧区域不能凭旧令牌/旧区域名重新发起转移或「抢回」权威。
	if _, err := a.BeginTransfer(ctx, TransferRequest{
		Domain: "d-1", From: "region-a", Token: grantA.Token, To: "region-a",
	}); !kerrors.Is(err, ErrNotOwner) {
		t.Fatalf("旧区域不得发起转移，实得 %v", err)
	}

	// 4c. 即使旧区域伪造更高代次，也只是「未确认」而不是新权威。
	forged := grantA.Commit()
	forged.Epoch = grantB.Epoch + 1
	if err := a.Fence(ctx, forged); !kerrors.Is(err, ErrEpochUnconfirmed) {
		t.Fatalf("伪造的未来代次不得通过，实得 %v", err)
	}

	// 5. 权威映射的事实也已切到 region-b（可查，不是注释里的承诺）。
	m, err := a.Mapping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m["d-1"] != "region-b" {
		t.Fatalf("确认后映射必须指向新区域，实得 %v", m)
	}
}

// TestPartitionOtherDomainUnaffected 分区/转移只作用于目标任务域：
// 另一个域的权威不得被顺带改写（域间隔离是映射的粒度）。
func TestPartitionOtherDomainUnaffected(t *testing.T) {
	ctx := context.Background()
	a := newAuthority(t)
	g1, _ := a.Owner(ctx, "d-1")
	g2, _ := a.Owner(ctx, "d-2")
	tr, err := a.BeginTransfer(ctx, TransferRequest{Domain: "d-1", From: "region-a", Token: g1.Token, To: "region-b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTransfer(ctx, ConfirmRequest{ID: tr.ID, Token: tr.Token, By: "region-b"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Fence(ctx, g2.Commit()); err != nil {
		t.Fatalf("其他域的权威不得被转移波及: %v", err)
	}
	m, _ := a.Mapping(ctx)
	if m["d-2"] != "region-c" {
		t.Fatalf("d-2 的映射不得变化，实得 %v", m)
	}
}

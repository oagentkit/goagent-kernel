// clock_test.go —— 多 Worker 时钟偏移（矩阵行四，Standalone 可选 / Cluster）。
package faultmatrix

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// runClockSkew 节点 B 的本地墙钟比存储快两小时。租约到期是**存储时钟**
// 回答的问题（issue #194）：活租约不得因为某个节点的本地时钟「已经到了
// 未来」被抢；存储时钟真正越过 TTL 后，接管才成立且代次严格递增。
//
// 反面对照：按节点本地时钟判到期的实现会在第一次「B 认领」处成功——本用例红。
func runClockSkew(t *testing.T) {
	ctx := context.Background()
	storageClock := newFakeClock()
	backend := newStorageBackend(storageClock)
	board, err := coord.NewBoardAdapter(backend) // 刻意不 UseClock：时间权威是存储
	if err != nil {
		t.Fatalf("存储时钟后端必须可装配: %v", err)
	}
	board.Seed(taskboard.Task{ID: "task-clock", State: taskboard.StateOpen})

	const ttl = time.Minute
	first, err := board.Claim(ctx, "task-clock", "worker-a", taskboard.ClaimRequest{TTL: ttl})
	if err != nil {
		t.Fatalf("首个认领应成功: %v", err)
	}

	// 节点 B 的本地墙钟（若实现拿它判到期，这里早已「过期」）。
	nodeBLocal := func() time.Time { return storageClock.Now().Add(2 * time.Hour) }
	if _, err := board.Claim(ctx, "task-clock", "worker-b", taskboard.ClaimRequest{TTL: ttl}); !errors.Is(err, taskboard.ErrTaken) {
		t.Fatalf("节点 B 本地时钟 %v 不得让活租约被抢（实得 %v）", nodeBLocal(), err)
	}

	storageClock.Advance(ttl / 2) // 存储时钟仍未到期
	if _, err := board.Claim(ctx, "task-clock", "worker-b", taskboard.ClaimRequest{TTL: ttl}); !errors.Is(err, taskboard.ErrTaken) {
		t.Fatalf("存储时钟未越过 TTL 时不得接管（实得 %v）", err)
	}

	storageClock.Advance(ttl) // 存储时钟越过 TTL
	second, err := board.Claim(ctx, "task-clock", "worker-b", taskboard.ClaimRequest{TTL: ttl})
	if err != nil {
		t.Fatalf("存储时钟越过 TTL 后必须可接管: %v", err)
	}
	if second.Lease.Fence <= first.Lease.Fence {
		t.Fatalf("接管代次必须严格递增（前 %d，后 %d）", first.Lease.Fence, second.Lease.Fence)
	}

	// 装配期底线：拿不出存储时钟、又不声明仅单机的后端必须被拒绝，
	// 不得静默退化成节点本地时钟。
	if _, err := coord.NewBoardAdapter(&bareBackend{inner: coord.NewMemoryBackend()}); !errors.Is(err, coord.ErrNoTimeAuthority) {
		t.Fatalf("无时间权威的后端必须在装配期被拒（实得 %v）", err)
	}
}

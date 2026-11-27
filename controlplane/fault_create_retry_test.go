// fault_create_retry_test.go —— 插点 G1/F3（创建 → 领取）的响应丢失注入
// （issue #523）：存储提交成功但回执没回来，客户端重试创建 ⇒ 不得出现
// 重复任务。
//
// 落点说明：taskboard.Board 契约没有创建入口（任务拓扑是宿主资产，
// coord.BoardAdapter.Seed 只是套件接缝、不是生产 API）；内核内带重复判定的
// 真实创建入口是本包的 SeedRun——重复 run / 重复任务一律 ErrAlreadyExists，
// 且先整单校验再落账（被拒的重试不留下半截数据）。
package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestFaultCreateReceiptLostRetryNoDuplicate 创建回执丢失后的重试不重复：
// 首次创建落库（回执丢失）→ 重试 → 第二次报「已存在」且板上只有一行 →
// 创建到领取的链路完好。
//
// 期望失败形态（坏实现必红的对照）：
//   - 重试静默成功再插一行 ⇒ List 行数断言红；
//   - 重试写入半截 run（先写后判）⇒ 被拒的 run 可见断言红；
//   - 重试破坏原行 ⇒ 领取断言红。
func TestFaultCreateReceiptLostRetryNoDuplicate(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore(&MemAuditor{})
	tasks := []taskboard.Task{{ID: "t1", Title: "合成任务", State: taskboard.StateOpen}}

	// 首次创建提交成功——回执丢失：调用方没拿到结果，只当自己失败了。
	if err := m.SeedRun("r1", "", tasks); err != nil {
		t.Fatalf("首次创建应成功: %v", err)
	}

	// 客户端重试同一创建：第二次必须报「已存在」，而不是再插一行。
	if err := m.SeedRun("r1", "", tasks); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("回执丢失后的重试创建必须报 ErrAlreadyExists，实得 %v", err)
	}
	// 换一个 run id、同一任务 id 的重试同样不得产生第二行（任务身份全局唯一）。
	if err := m.SeedRun("r2", "", tasks); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("同一任务 id 的重复创建必须报 ErrAlreadyExists，实得 %v", err)
	}

	// 板上只有一行：两次被拒的重试没有留下任何重复任务。
	list, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != "t1" || list[0].State != taskboard.StateOpen {
		t.Fatalf("重试不得产生重复任务（板上只有一行）: %+v", list)
	}
	// 被拒的重试不得留下半截 run（先整单校验再落账）。
	snap, err := m.SnapshotRun(ctx, "r1")
	if err != nil {
		t.Fatalf("原 run 应完好可读: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("原 run 下应恰好一个任务: %+v", snap.Tasks)
	}
	if _, err := m.SnapshotRun(ctx, "r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("被拒的重试不得留下半截 run: %v", err)
	}

	// 创建面完好：任务仍可被正常领取（创建 → 领取链路没有被重试破坏）。
	claimed, err := m.Claim(ctx, "t1", "w-1", time.Minute)
	if err != nil {
		t.Fatalf("重试被拒后原任务应可正常领取: %v", err)
	}
	if claimed.State != taskboard.StateClaimed || claimed.Owner != "w-1" {
		t.Fatalf("领取结果应为认领态且归 w-1: %+v", claimed)
	}
	list, err = m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("领取后板上仍应恰好一行: %+v", list)
	}
}

package coord

import (
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// BoardAdapter 接入共享契约套件（硬失败不跳过——接入即声明满足契约）。
func TestBoardAdapterContractSuite(t *testing.T) {
	boardtest.Run(t, func(t *testing.T, clock *boardtest.FakeClock) boardtest.ClockedBoard {
		t.Helper()
		b, err := NewBoardAdapter(NewMemoryBackend())
		if err != nil {
			t.Fatal(err)
		}
		b.UseClock(clock.Now)
		return b
	})
}

func TestBoardAdapterListEmpty(t *testing.T) {
	b, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := b.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tasks != nil {
		t.Fatalf("空任务板必须返回 nil（实得 %v）", tasks)
	}
}

func TestBoardAdapterSeedSkipsEmptyID(t *testing.T) {
	b, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	b.Seed(taskboard.Task{ID: ""}, taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	tasks, err := b.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("空 id 任务必须被跳过（实得 %v）", tasks)
	}
}

func TestBoardAdapterConcurrentClaimJudgment(t *testing.T) {
	// 判据 taskboard.ConcurrentClaim 直接跑在适配器上（契约条款的可跑形态）。
	b, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	n, err := taskboard.ConcurrentClaim(t.Context(), b, "t1", []taskboard.AgentID{"a1", "a2", "a3"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("并发认领先成功者必须恰好 1 个（实得 %d）", n)
	}
}

// TestClaimRejectsUnknownState 写路径必须与体检面同向：词表外状态代表
// 「内核读不懂这个状态」，此时最该保守（拒绝），而旧 Claim 只看
// Terminal()（未知状态不是终态）⇒ 照认不误并铸出令牌。体检说「非法」、
// 执行面说「可领」，一条脏状态行就能引发一次真实副作用。
func TestClaimRejectsUnknownState(t *testing.T) {
	b, err := NewBoardAdapter(NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.State("weird_state")})
	ctx := t.Context()

	_, err = b.Claim(ctx, "t1", "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err == nil {
		t.Fatal("词表外状态不得被 Claim 认领")
	}
	if !errors.Is(err, taskboard.ErrUnknownState) {
		t.Fatalf("应报 taskboard.ErrUnknownState，实得 %v", err)
	}
	// 存储面不得出现租约 / owner：拒绝必须发生在任何写操作之前。
	tasks, lerr := b.List(ctx)
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(tasks) != 1 {
		t.Fatalf("任务条数漂移：%d", len(tasks))
	}
	if tasks[0].State != taskboard.State("weird_state") || tasks[0].Owner != "" || !tasks[0].Lease.IsZero() {
		t.Fatalf("被拒的 Claim 不得改动任务/铸出租约：%+v", tasks[0])
	}
	// 批量领取与单条 Claim 必须同结论：未知状态不是就绪候选。
	got, rerr := b.ClaimReady(ctx, "worker-b", 1, taskboard.ClaimRequest{TTL: time.Minute})
	if rerr != nil {
		t.Fatalf("ClaimReady 不应因跳过未知状态而报错：%v", rerr)
	}
	if len(got) != 0 {
		t.Fatalf("ClaimReady 不得认领词表外状态，实得 %+v", got)
	}
}

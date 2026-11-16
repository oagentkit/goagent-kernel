// claimready_test.go —— taskboard.ReadyBoard 的参考实现用例（issue #189）。
//
// 三组判据：批量领取不重复分配（含与单条 Claim 交错）、依赖/终态过滤、
// 过期租约接管（旧令牌当场作废、新代次严格递增）。并发场景走
// async.GoSafe（本仓禁止裸 goroutine）。
package coord

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// newReadyBoard 构造带存储时钟的适配器（批量领取的过期场景要能确定性触发）。
func newReadyBoard(t *testing.T) (*BoardAdapter, *clockedStorage) {
	t.Helper()
	st := newClockedStorage(time.Date(2032, 4, 1, 8, 0, 0, 0, time.UTC))
	b, err := NewBoardAdapter(st)
	if err != nil {
		t.Fatalf("NewBoardAdapter: %v", err)
	}
	return b, st
}

// taskIDs 取一批任务的 id（保持返回值顺序）。
func taskIDs(tasks []taskboard.Task) []taskboard.TaskID {
	out := make([]taskboard.TaskID, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

// boardByID 把 List 快照变成 id → 任务。
func boardByID(t *testing.T, b taskboard.Board) map[taskboard.TaskID]taskboard.Task {
	t.Helper()
	tasks, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	out := make(map[taskboard.TaskID]taskboard.Task, len(tasks))
	for _, task := range tasks {
		out[task.ID] = task
	}
	return out
}

// TestClaimReadyBatchDistinct 钉批量领取的基本形态：一次最多 n 个、批内
// 不重复、按 id 序确定、就绪任务不足时返回剩下的且不报错。
func TestClaimReadyBatchDistinct(t *testing.T) {
	b, _ := newReadyBoard(t)
	b.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen},
		taskboard.Task{ID: "t3", State: taskboard.StateOpen},
		taskboard.Task{ID: "t4", State: taskboard.StateOpen},
		taskboard.Task{ID: "t5", State: taskboard.StateOpen},
	)
	ctx := context.Background()

	first, err := b.ClaimReady(ctx, "agent-a", 3, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	if got := taskIDs(first); !slices.Equal(got, []taskboard.TaskID{"t1", "t2", "t3"}) {
		t.Fatalf("首批应按 id 序领到 t1..t3（实得 %v）", got)
	}
	for _, task := range first {
		if task.Owner != "agent-a" || task.Lease.Token == "" || task.Lease.Fence == 0 {
			t.Fatalf("批量认领必须铸造令牌与代次且 owner 正确： %+v", task)
		}
	}

	second, err := b.ClaimReady(ctx, "agent-a", 3, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("ClaimReady 第二批: %v", err)
	}
	if got := taskIDs(second); !slices.Equal(got, []taskboard.TaskID{"t4", "t5"}) {
		t.Fatalf("第二批应领到剩余 t4,t5（实得 %v）", got)
	}

	third, err := b.ClaimReady(ctx, "agent-a", 3, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("候选耗尽不是错误（实得 %v）", err)
	}
	if len(third) != 0 {
		t.Fatalf("候选耗尽应返回空列表（实得 %v）", taskIDs(third))
	}

	byID := boardByID(t, b)
	if len(byID) != 5 {
		t.Fatalf("任务数漂移： %d", len(byID))
	}
	for id, task := range byID {
		if task.State != taskboard.StateClaimed {
			t.Fatalf("%s 应已被批量领取（实得 %s）", id, task.State)
		}
	}

	if _, err := b.ClaimReady(ctx, "agent-a", 0, taskboard.ClaimRequest{}); !errors.Is(err, taskboard.ErrInvalidBatch) {
		t.Fatalf("n=0 必须 ErrInvalidBatch（实得 %v）", err)
	}
	if _, err := b.ClaimReady(ctx, "agent-a", -2, taskboard.ClaimRequest{}); !errors.Is(err, taskboard.ErrInvalidBatch) {
		t.Fatalf("n<0 必须 ErrInvalidBatch（实得 %v）", err)
	}
}

// TestClaimReadyFiltersBlockedAndTerminal 钉就绪判据：依赖未完成、未知
// 依赖、终态都不是候选；failed 可重做；依赖完成后下一批就能领到。
func TestClaimReadyFiltersBlockedAndTerminal(t *testing.T) {
	b, _ := newReadyBoard(t)
	b.Seed(
		taskboard.Task{ID: "t1", State: taskboard.StateOpen},
		taskboard.Task{ID: "t2", State: taskboard.StateOpen, Deps: []taskboard.TaskID{"t1"}},
		taskboard.Task{ID: "t3", State: taskboard.StateDone},
		taskboard.Task{ID: "t4", State: taskboard.StateFailed},
		taskboard.Task{ID: "t5", State: taskboard.StateOpen, Deps: []taskboard.TaskID{"t1"}},
		taskboard.Task{ID: "t6", State: taskboard.StateOpen, Deps: []taskboard.TaskID{"ghost"}},
	)
	ctx := context.Background()

	first, err := b.ClaimReady(ctx, "agent-a", 10, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	if got := taskIDs(first); !slices.Equal(got, []taskboard.TaskID{"t1", "t4"}) {
		t.Fatalf("首批只能领到就绪的 t1 与可重做的 t4（实得 %v）", got)
	}

	// t1 完成后 t2/t5 就绪；t3 是终态、t6 依赖不存在，永远不出现。
	var t1Token string
	for _, task := range first {
		if task.ID == "t1" {
			t1Token = task.Lease.Token
		}
	}
	if err := b.Complete(ctx, "t1", t1Token); err != nil {
		t.Fatalf("Complete t1: %v", err)
	}
	second, err := b.ClaimReady(ctx, "agent-a", 10, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("ClaimReady 第二批: %v", err)
	}
	if got := taskIDs(second); !slices.Equal(got, []taskboard.TaskID{"t2", "t5"}) {
		t.Fatalf("依赖完成后应领到 t2,t5（实得 %v）", got)
	}
}

// TestClaimReadyTakesOverExpiredLease 钉过期租约接管：活租约不是候选；
// 存储时钟越过到期时刻后，批量领取按 Claim 的接管语义铸新令牌/新代次，
// 旧持有者的迟到提交凭 ErrLeaseLost 被拒。
func TestClaimReadyTakesOverExpiredLease(t *testing.T) {
	b, st := newReadyBoard(t)
	b.Seed(taskboard.Task{ID: "t1", State: taskboard.StateOpen})
	ctx := context.Background()

	old, err := b.Claim(ctx, "t1", "worker-old", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("初次认领: %v", err)
	}
	// 活租约期间批量领取不得抢：返回空列表。
	none, err := b.ClaimReady(ctx, "worker-new", 5, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("活租约期间 ClaimReady 不应报错（实得 %v）", err)
	}
	if len(none) != 0 {
		t.Fatalf("活租约期间不得被批量领取抢走（实得 %v）", taskIDs(none))
	}

	st.advance(2 * time.Minute) // 越过租约到期时刻
	got, err := b.ClaimReady(ctx, "worker-new", 5, taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("过期后 ClaimReady: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("过期租约应被批量领取接管（实得 %v）", taskIDs(got))
	}
	if got[0].Owner != "worker-new" {
		t.Fatalf("接管者应为 worker-new（实得 %s）", got[0].Owner)
	}
	if got[0].Lease.Token == old.Lease.Token {
		t.Fatal("接管必须铸造新令牌")
	}
	if got[0].Lease.Fence <= old.Lease.Fence {
		t.Fatalf("接管代次未严格递增： %d -> %d", old.Lease.Fence, got[0].Lease.Fence)
	}

	if err := b.Complete(ctx, "t1", old.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("旧令牌迟到提交必须 ErrLeaseLost（实得 %v）", err)
	}
	if err := b.Complete(ctx, "t1", got[0].Lease.Token); err != nil {
		t.Fatalf("新持有者提交: %v", err)
	}
}

// TestClaimReadyHundredConcurrentUnique 对齐 issue #189 的验收口径：
// 100 个副本同时各领 1 个任务，100 个任务必须恰好各有一个持有者。
func TestClaimReadyHundredConcurrentUnique(t *testing.T) {
	b, _ := newReadyBoard(t)
	const n = 100
	tasks := make([]taskboard.Task, 0, n)
	for i := range n {
		tasks = append(tasks, taskboard.Task{
			ID:    taskboard.TaskID("hundred-" + strconv.Itoa(i)),
			State: taskboard.StateOpen,
		})
	}
	b.Seed(tasks...)
	ctx := context.Background()

	type result struct {
		agent string
		tasks []taskboard.Task
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []result
		start   = make(chan struct{})
	)
	for i := range n {
		agent := "replica-" + strconv.Itoa(i)
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			got, err := b.ClaimReady(ctx, taskboard.AgentID(agent), 1, taskboard.ClaimRequest{TTL: time.Minute})
			if err != nil {
				t.Errorf("ClaimReady(%s): %v", agent, err)
				return
			}
			mu.Lock()
			results = append(results, result{agent: agent, tasks: got})
			mu.Unlock()
		})
	}
	close(start)
	wg.Wait()

	seen := make(map[taskboard.TaskID]string, n)
	for _, r := range results {
		if len(r.tasks) != 1 {
			t.Fatalf("%s 应恰好领到 1 个任务（实得 %d）", r.agent, len(r.tasks))
		}
		task := r.tasks[0]
		if task.Owner != taskboard.AgentID(r.agent) {
			t.Fatalf("%s 领到的任务 owner 漂移： %s", r.agent, task.Owner)
		}
		if prev, ok := seen[task.ID]; ok {
			t.Fatalf("任务 %s 被重复分配：%s 与 %s", task.ID, prev, r.agent)
		}
		seen[task.ID] = r.agent
	}
	if len(seen) != n {
		t.Fatalf("100 个任务应全部被领走（实得 %d）", len(seen))
	}
}

// TestClaimReadyInterleavesWithSingleClaim 钉与单条 Claim 的交错：所有
// 领取路径共用存储的原子认领，任何任务在整场竞争里至多一个持有者。
func TestClaimReadyInterleavesWithSingleClaim(t *testing.T) {
	b, _ := newReadyBoard(t)
	ids := []taskboard.TaskID{"t00", "t01", "t02", "t03", "t04", "t05", "t06", "t07", "t08", "t09", "t10", "t11"}
	tasks := make([]taskboard.Task, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, taskboard.Task{ID: id, State: taskboard.StateOpen})
	}
	b.Seed(tasks...)
	ctx := context.Background()

	type claimed struct {
		agent string
		id    taskboard.TaskID
	}
	var (
		mu    sync.Mutex
		all   []claimed
		start = make(chan struct{})
		wg    sync.WaitGroup
	)
	record := func(agent string, got []taskboard.Task) {
		mu.Lock()
		defer mu.Unlock()
		for _, task := range got {
			all = append(all, claimed{agent: agent, id: task.ID})
		}
	}

	// 七个批量领取者（各要 2 个）+ 一个单条领取者（抢 t00）。
	for i := range 7 {
		agent := "batch-" + string(rune('a'+i))
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			<-start
			got, err := b.ClaimReady(ctx, taskboard.AgentID(agent), 2, taskboard.ClaimRequest{TTL: time.Minute})
			if err != nil {
				t.Errorf("ClaimReady(%s): %v", agent, err)
				return
			}
			record(agent, got)
		})
	}
	wg.Add(1)
	async.GoSafe(func() {
		defer wg.Done()
		<-start
		task, err := b.Claim(ctx, "t00", "single", taskboard.ClaimRequest{TTL: time.Minute})
		if err == nil {
			record("single", []taskboard.Task{task})
			return
		}
		if !errors.Is(err, taskboard.ErrTaken) && !errors.Is(err, taskboard.ErrLeaseLost) {
			t.Errorf("单条 Claim 只允许竞争性拒绝（实得 %v）", err)
		}
	})
	close(start)
	wg.Wait()

	seen := make(map[taskboard.TaskID]string, len(all))
	for _, c := range all {
		if prev, ok := seen[c.id]; ok {
			t.Fatalf("任务 %s 被重复分配：%s 与 %s", c.id, prev, c.agent)
		}
		seen[c.id] = c.agent
	}
	if len(seen) != len(ids) {
		t.Fatalf("所有任务都应恰好被领一次：want %d got %d (%v)", len(ids), len(seen), seen)
	}

	// 板上的 owner 必须与竞争结果一致（不是「记了但没落库」）。
	byID := boardByID(t, b)
	for id, owner := range seen {
		task := byID[id]
		if task.State != taskboard.StateClaimed || string(task.Owner) != owner {
			t.Fatalf("%s 落库状态与竞争结果不一致：state=%s owner=%s want owner=%s",
				id, task.State, task.Owner, owner)
		}
	}
}

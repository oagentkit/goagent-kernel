// authority_test.go —— 控制面与运行时/副作用闸的权威统一（issue #268）。
//
// 判据：
//   - 强制释放与 Worker Complete 并发 ⇒ 只有一次有效状态转换，另一侧得到
//     ErrLeaseLost（可观测）；
//   - 强释后旧 Worker 的 Renew/Complete 被同一份任务板拒收；
//   - 裁决落到副作用闸读取的同一份状态（不是控制面自己的投影）；
//   - 装配期同源声明不一致 ⇒ ErrAuthorityMismatch。
package controlplane

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/coord"
	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
)

// fakeEffects 副作用闸替身：states[key] = 是否已生效（闸自己的状态）。
type fakeEffects struct {
	mu      sync.Mutex
	states  map[string]bool
	calls   int
	lastRel bool
	nextErr error
}

func (f *fakeEffects) ResolveUncertain(_ context.Context, key string, applied, forceRetryable bool, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	if _, ok := f.states[key]; !ok {
		return errors.Wrap(ErrNotFound, "效果键不存在: "+key)
	}
	f.states[key] = applied
	f.lastRel = forceRetryable
	return nil
}

func (f *fakeEffects) state(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[key]
}

func newAuthorityFixture(t *testing.T) (*StorePlane, *coord.BoardAdapter, *MemAuditor, *fakeEffects) {
	t.Helper()
	board, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatalf("NewBoardAdapter: %v", err)
	}
	aud := &MemAuditor{}
	store := NewMemStore(aud)
	effects := &fakeEffects{states: map[string]bool{}}
	plane, err := NewAuthoritativeStorePlane(store, aud, board, effects)
	if err != nil {
		t.Fatalf("NewAuthoritativeStorePlane: %v", err)
	}
	return plane, board, aud, effects
}

// TestForceReleaseVsWorkerCompleteSingleWinner 并发介入只有一次有效转换。
func TestForceReleaseVsWorkerCompleteSingleWinner(t *testing.T) {
	plane, board, _, _ := newAuthorityFixture(t)
	ctx := tenantCtx()

	for i := 0; i < 20; i++ {
		id := taskboard.TaskID("t" + strconv.Itoa(i))
		board.Seed(taskboard.Task{ID: id, State: taskboard.StateOpen})
		claimedTask, err := board.Claim(ctx, id, "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			errs <- plane.ForceRelease(ctx, ForceRelease{TaskID: id, Operator: "op-1", Basis: "合成依据"})
		}()
		go func() {
			<-start
			errs <- board.Complete(ctx, id, claimedTask.Lease.Token)
		}()
		close(start)
		e1, e2 := <-errs, <-errs

		winners, losers := 0, 0
		for _, err := range []error{e1, e2} {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, taskboard.ErrLeaseLost):
				losers++
			default:
				t.Fatalf("第 %d 轮落败方必须是 ErrLeaseLost（实得 %v）", i, err)
			}
		}
		if winners != 1 || losers != 1 {
			t.Fatalf("第 %d 轮必须恰好一方生效（winners=%d losers=%d: %v / %v）",
				i, winners, losers, e1, e2)
		}
	}
}

// TestForceReleaseInvalidatesOldWorker 强释后旧令牌一切动作被同一份任务板拒收。
func TestForceReleaseInvalidatesOldWorker(t *testing.T) {
	plane, board, aud, _ := newAuthorityFixture(t)
	ctx := tenantCtx()
	const id taskboard.TaskID = "t1"
	board.Seed(taskboard.Task{ID: id, State: taskboard.StateOpen})
	first, err := board.Claim(ctx, id, "worker-a", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := plane.ForceRelease(ctx, ForceRelease{TaskID: id, Operator: "op-1", Basis: "失联"}); err != nil {
		t.Fatalf("ForceRelease: %v", err)
	}
	// 旧持有者迟到的续期/完成：由同一份任务板判 ErrLeaseLost。
	if _, err := board.Renew(ctx, id, first.Lease.Token, taskboard.ClaimRequest{TTL: time.Minute}); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("强释后旧令牌续期必须 ErrLeaseLost, got %v", err)
	}
	if err := board.Complete(ctx, id, first.Lease.Token); !errors.Is(err, taskboard.ErrLeaseLost) {
		t.Fatalf("强释后旧令牌完成必须 ErrLeaseLost, got %v", err)
	}
	// 下一次认领铸造新令牌 + 严格更大代次。
	second, err := board.Claim(ctx, id, "worker-b", taskboard.ClaimRequest{TTL: time.Minute})
	if err != nil {
		t.Fatalf("强释后应可重新认领: %v", err)
	}
	if second.Lease.Token == first.Lease.Token || second.Lease.Fence <= first.Lease.Fence {
		t.Fatalf("重新认领必须换令牌并推进代次（%+v → %+v）", first.Lease, second.Lease)
	}
	// 控制面视图读的是同一份任务板。
	view, err := plane.TaskView(ctx, id)
	if err != nil {
		t.Fatalf("TaskView: %v", err)
	}
	if view.Task.Owner != "worker-b" || view.Task.Lease.Token != second.Lease.Token {
		t.Fatalf("TaskView 必须反映权威任务板当前持有者: %+v", view.Task)
	}
	entries, err := aud.Entries(ctx, string(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != ActionForceRelease {
		t.Fatalf("强释必须留痕: %+v", entries)
	}
}

// TestAdjudicateWritesThroughEffectAuthority 裁决落到副作用闸的同一份状态。
func TestAdjudicateWritesThroughEffectAuthority(t *testing.T) {
	plane, _, aud, effects := newAuthorityFixture(t)
	ctx := tenantCtx()
	effects.states["e1"] = false // 闸里的未知态
	// 控制面自己的效果投影另有一份（用来证明权威模式不再写它）。
	store, ok := plane.store.(*MemStore)
	if !ok {
		t.Fatalf("夹具应为 MemStore，实得 %T", plane.store)
	}
	if err := store.SeedEffect("e1", false); err != nil {
		t.Fatalf("SeedEffect: %v", err)
	}

	err := plane.Adjudicate(ctx, Adjudication{
		EffectKey: "e1", Verdict: VerdictApplied, Operator: "op-1", Basis: "下游查得已生效",
	})
	if err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	if !effects.state("e1") {
		t.Fatal("裁决必须落到副作用闸的同一份状态（闸读到已生效）")
	}
	snap, err := store.SnapshotEffect(ctx, "e1")
	if err != nil {
		t.Fatalf("SnapshotEffect: %v", err)
	}
	if !snap.Effect.Uncertain {
		t.Fatal("权威模式不得改写控制面自己的效果投影（否则又是两份状态）")
	}

	// 先到者为准：闸报 ErrNotUncertain ⇒ 控制面如实返回并留痕。
	effects.nextErr = sideeffect.ErrNotUncertain
	err = plane.Adjudicate(ctx, Adjudication{
		EffectKey: "e1", Verdict: VerdictNotApplied, Operator: "op-2", Basis: "第二个介入者",
	})
	if !errors.Is(err, sideeffect.ErrNotUncertain) {
		t.Fatalf("已裁决后再裁决必须 ErrNotUncertain, got %v", err)
	}
	entries, err := aud.Entries(ctx, "e1")
	if err != nil {
		t.Fatal(err)
	}
	var settled bool
	for _, e := range entries {
		if e.Action == ActionAdjudicate && e.Operator == "op-2" {
			settled = true
		}
	}
	if !settled {
		t.Fatalf("被拒的裁决也必须留痕（可观测）: %+v", entries)
	}
}

// TestAuthoritativePlaneFailClosed 缺任务面/效果面时必须装配期或调用期拒绝。
func TestAuthoritativePlaneFailClosed(t *testing.T) {
	board, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	aud := &MemAuditor{}
	store := NewMemStore(aud)
	if _, err := NewAuthoritativeStorePlane(store, aud, nil, nil); !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("缺任务权威面必须装配失败, got %v", err)
	}
	if _, err := NewAuthoritativeStorePlane(nil, aud, board, nil); !errors.Is(err, ErrNoStore) {
		t.Fatalf("缺 Store 必须装配失败, got %v", err)
	}
	plane, err := NewAuthoritativeStorePlane(store, aud, board, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = plane.Adjudicate(tenantCtx(), Adjudication{
		EffectKey: "e1", Verdict: VerdictApplied, Operator: "op-1", Basis: "b",
	})
	if !errors.Is(err, ErrNoEffectAuthority) {
		t.Fatalf("缺副作用权威面时裁决必须 fail-closed, got %v", err)
	}
}

// sourcedStore / sourcedTasks 声明来源标识（同源装配判据）。
type sourcedStore struct {
	Store
	id string
}

func (s sourcedStore) TaskSourceID() string { return s.id }

type sourcedTasks struct {
	TaskAuthority
	id string
}

func (s sourcedTasks) TaskSourceID() string { return s.id }

// TestAuthoritativePlaneRejectsMismatchedSource 装配期同源声明不一致即拒绝。
func TestAuthoritativePlaneRejectsMismatchedSource(t *testing.T) {
	board, err := coord.NewBoardAdapter(coord.NewMemoryBackend())
	if err != nil {
		t.Fatal(err)
	}
	aud := &MemAuditor{}
	store := sourcedStore{Store: NewMemStore(aud), id: "table-a"}
	tasks := sourcedTasks{TaskAuthority: board, id: "table-b"}
	if _, err := NewAuthoritativeStorePlane(store, aud, tasks, nil); !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("来源标识不一致必须装配失败, got %v", err)
	}
	same := sourcedTasks{TaskAuthority: board, id: "table-a"}
	if _, err := NewAuthoritativeStorePlane(store, aud, same, nil); err != nil {
		t.Fatalf("来源标识一致应装配成功: %v", err)
	}
}

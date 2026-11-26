// partition_test.go —— Worker 网络分区与租约接管（矩阵行五，Cluster）。
package faultmatrix

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/coord"
	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/worker"
)

// partitionBoard 只切断 A 的**续期路径**（控制平面不可达）：存储本身仍
// 正常，B 用同一存储。这正是网络分区对租约的直接后果——A 续不上，
// 存储时钟越过 TTL 后 B 可接管。
type partitionBoard struct {
	*coord.BoardAdapter
	partitioned atomic.Bool
}

func (b *partitionBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	if b.partitioned.Load() {
		return taskboard.Task{}, errors.New("faultmatrix: 注入网络分区（续期不可达）")
	}
	return b.BoardAdapter.Renew(ctx, id, token, req)
}

// memRegistry 进程内注册表替身（判据归注册表：过期读注入时钟）。
type memRegistry struct {
	mu   sync.Mutex
	now  func() time.Time
	seq  int
	recs map[string]*memReg
}

type memReg struct {
	token   string
	expires time.Time
}

func newMemRegistry(now func() time.Time) *memRegistry {
	return &memRegistry{now: now, recs: map[string]*memReg{}}
}

func (r *memRegistry) Register(_ context.Context, spec worker.Spec) (worker.Registration, error) {
	if err := spec.Validate(); err != nil {
		return worker.Registration{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	r.recs[spec.ID] = &memReg{token: fmt.Sprintf("reg-%s-%d", spec.ID, r.seq), expires: r.now().Add(spec.TTL)}
	rec := r.recs[spec.ID]
	return worker.Registration{ID: spec.ID, Token: rec.token, ExpiresAt: rec.expires}, nil
}

func (r *memRegistry) Heartbeat(_ context.Context, id, token string, ttl time.Duration) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.recs[id]
	if !ok || rec.token != token || !r.now().Before(rec.expires) {
		return time.Time{}, errors.Wrap(worker.ErrNotRegistered, "faultmatrix: 注册替身拒绝心跳")
	}
	if ttl <= 0 {
		return time.Time{}, errors.Wrap(worker.ErrInvalidConfig, "faultmatrix: 注册替身要求正 TTL")
	}
	rec.expires = r.now().Add(ttl)
	return rec.expires, nil
}

func (r *memRegistry) Deregister(_ context.Context, id, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.recs[id]
	if !ok || rec.token != token {
		return errors.Wrap(worker.ErrNotRegistered, "faultmatrix: 注册替身拒绝注销")
	}
	delete(r.recs, id)
	return nil
}

func (r *memRegistry) List(context.Context) ([]worker.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]worker.Status, 0, len(r.recs))
	for id, rec := range r.recs {
		out = append(out, worker.Status{ID: id, ExpiresAt: rec.expires, Alive: r.now().Before(rec.expires)})
	}
	return out, nil
}

// runNetworkPartitionLeaseTakeover A 与存储的控制平面中断，租约到期后 B 接管：
//
//   - A 的续期路径不可达期间不会「确认持有」；存储时钟越过 TTL 后 B 可以接管；
//   - A 的分区恢复后的下一次续期报 ErrLeaseLost ⇒ 只取消 A 的在途执行，
//     且 A 的迟到收场被拒（结果作废、留痕）；
//   - B 恰好施加一次并完成；A 不得完成/不得施加（不产生双份副作用）；
//   - 接管代次严格递增。
func runNetworkPartitionLeaseTakeover(t *testing.T) {
	clock := newFakeClock()
	base := newBoardWithClock(t, clock)
	base.Seed(taskboard.Task{ID: "task-partition", State: taskboard.StateOpen})
	reg := newMemRegistry(newFakeClock().Now) // 注册表时钟不动：身份不参与本格
	partitioned := &partitionBoard{BoardAdapter: base}

	var (
		causesMu sync.Mutex
		causes   []worker.LeaseLostCause
	)
	aEntered := make(chan taskboard.Task, 1)
	aCanceled := make(chan struct{}, 1)
	var aApplied atomic.Int64
	var aFence taskboard.Fence
	aHandler := worker.HandlerFunc(func(ctx context.Context, task taskboard.Task) error {
		select {
		case aEntered <- task:
		default:
		}
		<-ctx.Done()
		select {
		case aCanceled <- struct{}{}:
		default:
		}
		if ctx.Err() == nil {
			aApplied.Add(1)
		}
		return ctx.Err()
	})
	aLoop, err := worker.NewLoop(partitioned, reg, aHandler, worker.Config{
		Agent: "worker-a", TTL: time.Minute, PollInterval: time.Millisecond,
		HeartbeatInterval: 5 * time.Millisecond,
		// issue #266 后续期按到期时刻调度：本格要在分区恢复后的短窗口内
		// 看到 A 的续期被判易主，故把安全余量调到 TTL 附近（每拍都到点）。
		RenewMargin:  time.Minute - time.Millisecond,
		RenewFailMax: 1_000_000, // 分区时长远小于阈值：本格要测接管而不是自检失败
		OnLeaseLost: func(_ taskboard.TaskID, cause worker.LeaseLostCause) {
			causesMu.Lock()
			causes = append(causes, cause)
			causesMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	aCtx, aCancel := context.WithCancel(context.Background())
	defer aCancel()
	aDone := make(chan error, 1)
	go func() { aDone <- aLoop.Run(aCtx) }()

	select {
	case task := <-aEntered:
		aFence = task.Lease.Fence
	case <-time.After(5 * time.Second):
		t.Fatal("等待 A 进入执行体超时")
	}

	// 分区 + 存储时钟越过 A 的租约。A 续不上，B 从同一存储接管。
	partitioned.partitioned.Store(true)
	clock.Advance(2 * time.Minute)

	// B 的接管入口是 worker.Recover（过期认领的恢复面）：普通领取环只挑
	// open/failed，claimed 的过期任务由 Recover 主动 Claim 接管并重跑。
	// 这正是分区/强删后新副本启动时的真实路径。
	took, err := worker.Recover(context.Background(), base, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("B 恢复（接管）失败: %v", err)
	}
	if len(took) != 1 || took[0] != "task-partition" {
		t.Fatalf("B 必须接管 A 的过期认领（实得 %v）", took)
	}
	tasks, err := base.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateClaimed || tasks[0].Owner != "worker-b" {
		t.Fatalf("接管后任务必须归 B 且处于认领态（实得 %+v）", tasks)
	}
	bFence := tasks[0].Lease.Fence
	bApplied := int64(1) // B 执行并施加一次
	if err := base.Complete(context.Background(), "task-partition", tasks[0].Lease.Token); err != nil {
		t.Fatalf("B 接管后完成任务失败: %v", err)
	}

	// 分区恢复：A 的下一次续期看到租约已易主/任务已终态 ⇒ 取消在途。
	partitioned.partitioned.Store(false)
	select {
	case <-aCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("分区恢复后 A 的续期必须报 ErrLeaseLost 并取消在途执行")
	}
	aCancel()
	select {
	case err := <-aDone:
		if err != nil {
			t.Fatalf("单任务资格丢失不构成驱动故障，应优雅退出（实得 %v）", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待 A 收刀超时")
	}
	if bApplied != 1 {
		t.Fatalf("B 必须恰好施加一次（实得 %d）", bApplied)
	}
	if got := aApplied.Load(); got != 0 {
		t.Fatalf("A 在取消后不得施加副作用（实得 %d）——否则与新持有者双份", got)
	}
	if bFence <= aFence {
		t.Fatalf("接管代次必须严格递增（A=%d，B=%d）", aFence, bFence)
	}
	causesMu.Lock()
	hasTakenOver := false
	for _, cause := range causes {
		if cause == worker.LeaseLostTakenOver {
			hasTakenOver = true
		}
	}
	causesMu.Unlock()
	if !hasTakenOver {
		t.Fatal("租约易主必须留痕 LeaseLostTakenOver——「本次结果作废」不许静默")
	}
}

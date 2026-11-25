// boardtest_test.go —— 用一份内存实现把套件跑绿（正例）；
// 变异实现与判据的牙齿验证在 mutation_test.go。
package boardtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// memBoard 一份最小的内存实现（仅供套件自证；生产实现归宿主）。
type memBoard struct {
	mu    sync.Mutex
	tasks map[taskboard.TaskID]*taskboard.Task

	// seq 令牌与接管代次的**共同铸造计数器**（同源：同一次认领的同一原子
	// 操作里铸造两者，见 Lease.Fence）。令牌由**存储**铸造是 v0.33.0 的要害：
	// 调用方自带令牌的话，「凭令牌认人」名存实亡。
	seq uint64

	// now 可注入时钟（过期判定必须读它，否则过期场景恒不触发）。
	now func() time.Time
}

func newMemBoard(_ *testing.T, _ *FakeClock) ClockedBoard {
	return rawMemBoard()
}

// rawMemBoard 供变异实现包一层用。
func rawMemBoard() *memBoard {
	return &memBoard{tasks: map[taskboard.TaskID]*taskboard.Task{}}
}

// UseClock 注入时钟（套件约定）。
func (m *memBoard) UseClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = now
}

// clockLocked 当前时刻。**只在持锁时调用**。
func (m *memBoard) clockLocked() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Seed 摆初始任务（套件约定：创建语义是宿主资产，故走这个可选接口）。
func (m *memBoard) Seed(tasks ...taskboard.Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tk := range tasks {
		// Task.Clone 收口「Deps 不外借」这条契约（issue #379）。
		cp := tk.Clone()
		m.tasks[tk.ID] = &cp
	}
}

func (m *memBoard) List(_ context.Context) ([]taskboard.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]taskboard.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.Clone())
	}
	return out, nil
}

func (m *memBoard) Claim(_ context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrNotFound, string(id))
	}
	// 词表外状态不得认领（issue #360 的契约义务）：与生产实现同一判据。
	if err := t.State.Claimable(); err != nil {
		return taskboard.Task{}, errors.Wrap(err, string(id))
	}
	if t.State == taskboard.StateClaimed && !leaseExpired(t.Lease, m.clockLocked()) {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrTaken, string(id))
	}
	for _, d := range t.Deps {
		dep, ok := m.tasks[d]
		if !ok || !dep.State.Terminal() {
			return taskboard.Task{}, errors.Wrap(taskboard.ErrBlocked, string(d))
		}
	}
	// 令牌与接管代次由存储铸造；到期时刻也由存储按**自己的**时钟算。
	m.seq++
	lease := taskboard.Lease{
		Token: fmt.Sprintf("tok-%d", m.seq),
		Fence: taskboard.Fence(m.seq),
	}
	if req.TTL > 0 {
		lease.Until = m.clockLocked().Add(req.TTL)
	}
	t.State = taskboard.StateClaimed
	t.Owner = agent
	t.Lease = lease
	t.Seq++
	return t.Clone(), nil
}

func (m *memBoard) Release(_ context.Context, id taskboard.TaskID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return taskboard.ErrNotFound
	}
	if t.State != taskboard.StateClaimed || t.Lease.Token != token {
		return taskboard.ErrLeaseLost
	}
	t.State = taskboard.StateOpen
	t.Owner = ""
	t.Lease = taskboard.Lease{}
	t.Seq++
	return nil
}

func (m *memBoard) Complete(_ context.Context, id taskboard.TaskID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return taskboard.ErrNotFound
	}
	if t.State != taskboard.StateClaimed || t.Lease.Token != token {
		return taskboard.ErrLeaseLost
	}
	// 资格在到期时刻终止（issue #497）：租约已过期时旧持有者不得提交终态
	// ——否则终态内容取决于「谁先提交」而不是「谁有资格提交」。
	if leaseExpired(t.Lease, m.clockLocked()) {
		return taskboard.ErrLeaseLost
	}
	t.State = taskboard.StateDone
	t.Owner = ""
	t.Lease = taskboard.Lease{}
	t.Seq++
	return nil
}

// Renew 续期并确认持有资格（taskboard.RenewingBoard 的参考语义）。
func (m *memBoard) Renew(_ context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrNotFound, string(id))
	}
	// 资格校验在前：令牌不是当前租约的、或租约已过期 ⇒ 拒且不改。
	if t.State != taskboard.StateClaimed || t.Lease.Token != token {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, string(id))
	}
	if leaseExpired(t.Lease, m.clockLocked()) {
		return taskboard.Task{}, errors.Wrap(taskboard.ErrLeaseLost, string(id))
	}
	// 校验与延长同一把锁内完成（原子）；TTL 语义与 Claim 一致。
	if req.TTL > 0 {
		t.Lease.Until = m.clockLocked().Add(req.TTL)
	} else {
		t.Lease.Until = time.Time{}
	}
	t.Seq++
	return *t, nil
}

// TestConformanceOnMemoryBoard 好实现必须全绿（缺省规模档：10 与 100）。
func TestConformanceOnMemoryBoard(t *testing.T) {
	Run(t, newMemBoard)
}

// TestConformanceWithCustomScale 宿主按自己的量级配置规模档也能跑。
func TestConformanceWithCustomScale(t *testing.T) {
	RunConfig(t, newMemBoard, Config{ClaimWorkers: 25})
}

// brokenBoard 故意把认领做成**读-判-写三段分离**：所有读者先读到 open，
// 再一起写入。这正是「没做到原子」的真实形态（先查后写，中间没有事务）。
type brokenBoard struct {
	*memBoard
	barrier *sync.WaitGroup
}

func (b *brokenBoard) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	// 第一段：读（拿完锁就放开）。
	b.mu.Lock()
	t, ok := b.tasks[id]
	state := taskboard.StateOpen
	if ok {
		state = t.State
	}
	b.mu.Unlock()
	if !ok {
		return taskboard.Task{}, taskboard.ErrNotFound
	}
	// 所有并发读者在这里对齐：保证「判」都发生在「写」之前。
	b.barrier.Done()
	b.barrier.Wait()
	// 第二段：判 + 第三段：写（各自凭自己读到的旧值决定）。
	if state.Terminal() {
		return taskboard.Task{}, taskboard.ErrTerminal
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	cur := b.tasks[id]
	cur.State = taskboard.StateClaimed
	cur.Owner = agent
	cur.Lease = taskboard.Lease{Token: fmt.Sprintf("tok-broken-%d", cur.Seq+1)}
	cur.Seq++
	return *cur, nil
}

// TestJudgeCatchesNonAtomicBoard ConcurrentClaim 判据的反向验证：
// 不原子的实现必须被抓住。没有这一条，ConcurrentClaim 可能无论实现对错
// 都返回 1，而套件照样全绿。
func TestJudgeCatchesNonAtomicBoard(t *testing.T) {
	const n = 4
	base := &memBoard{tasks: map[taskboard.TaskID]*taskboard.Task{
		"t1": {ID: "t1", State: taskboard.StateOpen},
	}}
	b := &brokenBoard{memBoard: base, barrier: &sync.WaitGroup{}}
	b.barrier.Add(n)

	agents := make([]taskboard.AgentID, 0, n)
	for i := range n {
		agents = append(agents, taskboard.AgentID(fmt.Sprintf("a%d", i+1)))
	}
	winners, err := taskboard.ConcurrentClaim(context.Background(), b, "t1", agents, time.Minute)
	if err != nil {
		t.Fatalf("判据本身不该报错: %v", err)
	}
	if winners == 1 {
		t.Fatalf("不原子的实现也判成 1 个赢家：判据没有测到争用（winners=%d）", winners)
	}
}

// tenantMemBoard 租户作用域的内存正例：每个租户一份独立 memBoard。
// 它就是「键空间按租户分区」的最小实现——用来证明租户套件可被正确实现跑绿。
type tenantMemBoard struct {
	mu     sync.Mutex
	boards map[string]*memBoard
	now    func() time.Time
}

func newTenantMemBoard(_ *testing.T, _ *FakeClock) ClockedBoard {
	return &tenantMemBoard{boards: map[string]*memBoard{}}
}

// UseClock 注入时钟（新租户子板沿用当前注入值）。
func (b *tenantMemBoard) UseClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
	for _, mb := range b.boards {
		mb.UseClock(now)
	}
}

// boardLocked 取（或建）租户子板。持 b.mu 调用。
func (b *tenantMemBoard) boardLocked(tenantID string) *memBoard {
	mb, ok := b.boards[tenantID]
	if !ok {
		mb = rawMemBoard()
		if b.now != nil {
			mb.UseClock(b.now)
		}
		b.boards[tenantID] = mb
	}
	return mb
}

// scoped 从 ctx 取租户（缺租户 fail-closed）。
func (b *tenantMemBoard) scoped(ctx context.Context) (*memBoard, error) {
	c, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.boardLocked(c.Tenant), nil
}

// Seed 按任务自带的租户落键（套件接缝）。
func (b *tenantMemBoard) Seed(tasks ...taskboard.Task) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tk := range tasks {
		if tk.Tenant == "" {
			panic("boardtest: 租户实现不得摆入空租户任务")
		}
		b.boardLocked(tk.Tenant).Seed(tk)
	}
}

func (b *tenantMemBoard) List(ctx context.Context) ([]taskboard.Task, error) {
	mb, err := b.scoped(ctx)
	if err != nil {
		return nil, err
	}
	return mb.List(ctx)
}

func (b *tenantMemBoard) Claim(ctx context.Context, id taskboard.TaskID, agent taskboard.AgentID, req taskboard.ClaimRequest) (taskboard.Task, error) {
	mb, err := b.scoped(ctx)
	if err != nil {
		return taskboard.Task{}, err
	}
	return mb.Claim(ctx, id, agent, req)
}

func (b *tenantMemBoard) Release(ctx context.Context, id taskboard.TaskID, token string) error {
	mb, err := b.scoped(ctx)
	if err != nil {
		return err
	}
	return mb.Release(ctx, id, token)
}

func (b *tenantMemBoard) Complete(ctx context.Context, id taskboard.TaskID, token string) error {
	mb, err := b.scoped(ctx)
	if err != nil {
		return err
	}
	return mb.Complete(ctx, id, token)
}

func (b *tenantMemBoard) Renew(ctx context.Context, id taskboard.TaskID, token string, req taskboard.ClaimRequest) (taskboard.Task, error) {
	mb, err := b.scoped(ctx)
	if err != nil {
		return taskboard.Task{}, err
	}
	return mb.Renew(ctx, id, token, req)
}

// tenantLeakBoard 忽略租户的**变异实现**：所有租户共用一份按 id 索引的板。
// 这正是「租户没进键与领取条件」的形态——套件必须抓住它。
type tenantLeakBoard struct {
	*memBoard
}

func newTenantLeakBoard(_ *testing.T, _ *FakeClock) ClockedBoard {
	return &tenantLeakBoard{memBoard: rawMemBoard()}
}

// TestTenantConformanceOnMemoryBoard 租户套件在正确实现上必须全绿。
func TestTenantConformanceOnMemoryBoard(t *testing.T) {
	RunTenant(t, newTenantMemBoard)
}

// TestTenantSuiteCatchesLeak 租户套件的牙齿：忽略租户的实现必红。
func TestTenantSuiteCatchesLeak(t *testing.T) {
	ctxA, ctxB := tenantCtx("tenant-a"), tenantCtx("tenant-b")
	cases := []struct {
		name string
		run  func(b taskboard.Board) error
	}{
		{"跨租户不可见", func(b taskboard.Board) error { return checkTenantInvisible(ctxA, ctxB, b) }},
		{"同 id 互不覆盖", func(b taskboard.Board) error { return checkTenantSameIDCoexists(ctxA, ctxB, b) }},
		{"跨租户并发认领", func(b taskboard.Board) error { return checkTenantConcurrentClaims(ctxA, ctxB, b) }},
		{"跨租户令牌", func(b taskboard.Board) error { return checkTenantTokenConfined(ctxA, ctxB, b) }},
		{"缺租户 fail-closed", func(b taskboard.Board) error { return checkTenantMissingFailClosed(ctxA, b) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, _ := newTenantBoard(t, newTenantLeakBoard)
			if err := c.run(b); err == nil {
				t.Fatalf("忽略租户的实现也通过了「%s」：判据没有牙齿", c.name)
			}
		})
	}
}

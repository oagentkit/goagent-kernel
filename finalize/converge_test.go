package finalize_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/finalize"
)

// fakeStore 合成存储面：条件迁移按「现状态 ∈ from」判，记录每次调用的
// 调用序 / 起点集 / ctx 健康度——三条并发纪律全靠这些记录断言。
type fakeStore struct {
	mu     sync.Mutex
	status string // 库内现状态

	calls    []string // 调用序
	lastFrom []string // 最近一次 Migrate 的起点集
	ctxErrs  []error  // 每次调用时刻的 ctx.Err()
	reports  []string
	notices  []finalize.Notice
	reads    int

	reportErr  error
	migrateErr error
	readErr    error
}

func (s *fakeStore) record(ctx context.Context, call string) {
	s.calls = append(s.calls, call)
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
}

func (s *fakeStore) Migrate(ctx context.Context, from []string, to string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx, "migrate")
	s.lastFrom = append([]string(nil), from...)
	if s.migrateErr != nil {
		return false, s.migrateErr
	}
	for _, f := range from {
		if s.status == f {
			s.status = to
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) SetReport(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx, "report")
	s.reports = append(s.reports, text)
	return s.reportErr
}

func (s *fakeStore) ReadStatus(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx, "read")
	s.reads++
	if s.readErr != nil {
		return "", s.readErr
	}
	return s.status, nil
}

func (s *fakeStore) Publish(ctx context.Context, n finalize.Notice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx, "publish")
	s.notices = append(s.notices, n)
}

func (s *fakeStore) hasCall(call string) bool {
	for _, c := range s.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (s *fakeStore) assertDetached(t *testing.T) {
	t.Helper()
	if len(s.ctxErrs) == 0 {
		t.Fatal("存储面一次都没被调用")
	}
	for i, err := range s.ctxErrs {
		if err != nil {
			t.Fatalf("第 %d 次存储调用的 ctx 已取消: %v——终态写必须走脱离 ctx", i, err)
		}
	}
}

// TestConvergeWritesOnDetachedCtx 纪律①：原编排 ctx 已取消，收尾写（报告 /
// 迁移 / 事件）仍全部执行，且每一次存储调用拿到的都是未取消的脱离 ctx。
func TestConvergeWritesOnDetachedCtx(t *testing.T) {
	st := &fakeStore{status: contract.StatusRunning}
	parent, cancel := context.WithCancel(context.Background())
	cancel() // 编排 ctx 已被取消（取消/接管/停机都先取消它）

	out, err := finalize.Converge(parent, st, finalize.Spec{
		From:   []string{contract.StatusRunning},
		To:     contract.StatusTimedOut,
		Report: "## 会话已超时",
	})
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !out.Hit || out.Status != contract.StatusTimedOut {
		t.Fatalf("Outcome = %+v, want 命中 timed_out", out)
	}
	st.assertDetached(t)
	if !st.hasCall("report") || !st.hasCall("migrate") || !st.hasCall("publish") {
		t.Fatalf("调用序缺件: %v", st.calls)
	}
}

// TestConvergeZeroRowSilent 纪律②：条件迁移零行 = 他路已接手——静默收手，
// 不回读、不补发事件、不写任何状态。
func TestConvergeZeroRowSilent(t *testing.T) {
	st := &fakeStore{status: contract.StatusSucceeded} // 他路已收敛到 succeeded
	out, err := finalize.Converge(context.Background(), st, finalize.Spec{
		From:    []string{contract.StatusRunning},
		To:      contract.StatusFailed,
		ZeroRow: finalize.ZeroRowSilent,
	})
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if out.Hit || out.Status != "" {
		t.Fatalf("Outcome = %+v, want 零行静默（不谎报状态）", out)
	}
	if st.reads != 0 {
		t.Fatalf("零行静默不得回读（reads=%d）", st.reads)
	}
	if len(st.notices) != 0 {
		t.Fatalf("零行静默不得补发事件: %+v", st.notices)
	}
	if st.status != contract.StatusSucceeded {
		t.Fatalf("他路的终态被改写成 %q——零行后不得再写任何状态", st.status)
	}
}

// TestConvergeZeroRowReadBackNotLie 纪律③（后半）：零行时回读库内现终态
// 补发事件，**按库内事实报、不按意图报**（不谎报 timed_out）。
func TestConvergeZeroRowReadBackNotLie(t *testing.T) {
	st := &fakeStore{status: contract.StatusCancelled} // 他路（取消 API）已先行收敛
	out, err := finalize.Converge(context.Background(), st, finalize.Spec{
		From: []string{contract.StatusRunning},
		To:   contract.StatusTimedOut,
	})
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if out.Hit {
		t.Fatal("零行不得判命中")
	}
	if out.Status != contract.StatusCancelled {
		t.Fatalf("Outcome.Status = %q, want 库内现状态 cancelled——按意图报 timed_out 就是谎报", out.Status)
	}
	if len(st.notices) != 1 {
		t.Fatalf("应补发恰好 1 条事件: %+v", st.notices)
	}
	n := st.notices[0]
	if !n.Raced || n.Status != contract.StatusCancelled {
		t.Fatalf("补发事件 = %+v, want {Status: cancelled, Raced: true}", n)
	}
}

// TestConvergeDualStartConvergence 纪律③（前半）：多起点收敛——会话滞留在
// waiting_approval（挂起期迁移写丢失）时，只认 running 起点的终态写零行、
// 会话永不入终态；双起点一次原子迁移必须命中。
func TestConvergeDualStartConvergence(t *testing.T) {
	st := &fakeStore{status: contract.StatusWaitingApproval}
	out, err := finalize.Converge(context.Background(), st, finalize.Spec{
		From: []string{contract.StatusRunning, contract.StatusWaitingApproval},
		To:   contract.StatusTimedOut,
	})
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if !out.Hit || out.Status != contract.StatusTimedOut {
		t.Fatalf("Outcome = %+v, want 双起点命中 timed_out", out)
	}
	got := strings.Join(st.lastFrom, ",")
	if !strings.Contains(got, contract.StatusRunning) || !strings.Contains(got, contract.StatusWaitingApproval) {
		t.Fatalf("Migrate 起点集 = %v, want 同时含 running 与 waiting_approval", st.lastFrom)
	}
}

// TestConvergeSingleStartMissesWaitingApproval 反向对照：只给 running 起点时
// 恰好命中上面那条缺口（零行 → 回读路径）——双起点用例的断言因此有区分度。
func TestConvergeSingleStartMissesWaitingApproval(t *testing.T) {
	st := &fakeStore{status: contract.StatusWaitingApproval}
	out, err := finalize.Converge(context.Background(), st, finalize.Spec{
		From: []string{contract.StatusRunning},
		To:   contract.StatusTimedOut,
	})
	if err != nil {
		t.Fatalf("Converge: %v", err)
	}
	if out.Hit || out.Status != contract.StatusWaitingApproval {
		t.Fatalf("Outcome = %+v, want 零行回读到 waiting_approval", out)
	}
	if st.status != contract.StatusWaitingApproval {
		t.Fatalf("状态被改写成 %q——零行后不得再写", st.status)
	}
}

// TestConvergeErrors 零行回读失败 / 迁移失败都要响亮报错且不补发事件
// （给不出结论就不发——发一条没核实过的事件就是谎报）。
func TestConvergeErrors(t *testing.T) {
	t.Run("回读失败", func(t *testing.T) {
		st := &fakeStore{status: contract.StatusSucceeded, readErr: errors.New("存储不可达")}
		_, err := finalize.Converge(context.Background(), st, finalize.Spec{
			From: []string{contract.StatusRunning}, To: contract.StatusTimedOut,
		})
		if err == nil {
			t.Fatal("回读失败必须报错")
		}
		if len(st.notices) != 0 {
			t.Fatalf("回读失败不得补发事件: %+v", st.notices)
		}
	})
	t.Run("迁移失败", func(t *testing.T) {
		st := &fakeStore{status: contract.StatusRunning, migrateErr: errors.New("存储不可达")}
		_, err := finalize.Converge(context.Background(), st, finalize.Spec{
			From: []string{contract.StatusRunning}, To: contract.StatusFailed,
		})
		if err == nil {
			t.Fatal("迁移失败必须报错")
		}
		if len(st.notices) != 0 {
			t.Fatalf("迁移失败不得发事件: %+v", st.notices)
		}
	})
	t.Run("空起点", func(t *testing.T) {
		st := &fakeStore{status: contract.StatusRunning}
		if _, err := finalize.Converge(context.Background(), st, finalize.Spec{
			To: contract.StatusFailed,
		}); err == nil {
			t.Fatal("空起点必须当场报错（恒零行的迁移不该静默走完零行分支）")
		}
		if len(st.calls) != 0 {
			t.Fatalf("报错前不得碰存储: %v", st.calls)
		}
	})
}

// TestConvergeReportErrorDoesNotBlockMigrate 终报是展示面、状态是数据面：
// 终报写失败不阻断状态收敛（留痕后继续）。
func TestConvergeReportErrorDoesNotBlockMigrate(t *testing.T) {
	st := &fakeStore{status: contract.StatusRunning, reportErr: errors.New("磁盘满")}
	out, err := finalize.Converge(context.Background(), st, finalize.Spec{
		From: []string{contract.StatusRunning}, To: contract.StatusFailed, Report: "## 失败",
	})
	if err != nil {
		t.Fatalf("终报失败不得阻断收敛: %v", err)
	}
	if !out.Hit || out.Status != contract.StatusFailed {
		t.Fatalf("Outcome = %+v, want 命中 failed", out)
	}
	if !st.hasCall("migrate") {
		t.Fatal("迁移未执行")
	}
}

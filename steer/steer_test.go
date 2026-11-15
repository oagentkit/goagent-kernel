package steer_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/contract"
	"github.com/yi-nology/goagent-kernel/finalize"
	"github.com/yi-nology/goagent-kernel/statemachine"
	"github.com/yi-nology/goagent-kernel/steer"
)

// TestSignalCauseDistinguishable 接管 Cause 与用户取消 / 停机三分可辨：
// steer.IsSteered 只认 ErrSteered，finalize.Route 把三者路由到不同臂
// （steer 产出 Cause、finalize 消费——单向依赖的端到端对拍）。
func TestSignalCauseDistinguishable(t *testing.T) {
	// 接管：Signal 发出的 Cause。
	sctx, scancel := steer.Signal(context.Background())
	scancel()
	if !steer.IsSteered(sctx) {
		t.Fatal("Signal 的 Cause 应判成接管")
	}
	if got := finalize.Route(sctx); got != finalize.ActionTakeover {
		t.Fatalf("Route = %v, want takeover", got)
	}

	// 用户取消：finalize 的哨兵。
	uctx, ucancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { ucancel(nil) })
	ucancel(finalize.ErrUserCancelled)
	if steer.IsSteered(uctx) {
		t.Fatal("用户取消不得判成接管")
	}
	if got := finalize.Route(uctx); got != finalize.ActionCancelled {
		t.Fatalf("Route = %v, want cancelled", got)
	}

	// 停机：裸 ctx 取消（无 Cause）。
	pctx, pcancel := context.WithCancel(context.Background())
	t.Cleanup(pcancel)
	pcancel()
	if steer.IsSteered(pctx) {
		t.Fatal("停机不得判成接管")
	}
	if got := finalize.Route(pctx); got != finalize.ActionShutdown {
		t.Fatalf("Route = %v, want shutdown", got)
	}
}

// TestDecideQueueAware 队列感知：有排队条目才回 created；零值是保守侧
// （不开空轮）——漏传队列事实时宁可失败收敛，不开无人认领的空轮。
func TestDecideQueueAware(t *testing.T) {
	if got := steer.Decide(true); got != steer.DecisionRequeue {
		t.Fatalf("Decide(true) = %v, want requeue", got)
	}
	if got := steer.Decide(false); got != steer.DecisionFailConverge {
		t.Fatalf("Decide(false) = %v, want fail_converge", got)
	}
	var zero steer.Decision
	if zero != steer.DecisionFailConverge {
		t.Fatalf("零值 = %v, want fail_converge（保守侧缺省）", zero)
	}
	if zero.String() == "" || steer.DecisionRequeue.String() == "" {
		t.Fatal("Decision.String 不得为空串")
	}
}

// TestBuildSummaryOnlyCountsSucceeded 只计成功环节：skipped / rolled_back /
// rejected / failed 无有效产出，留给下一轮重新评估——失败环节的摘要绝不能
// 混进保产面。
func TestBuildSummaryOnlyCountsSucceeded(t *testing.T) {
	steps := []steer.DoneStep{
		{Slug: "step-a", Summary: "合成产出 A", Status: statemachine.StatusSucceeded},
		{Slug: "step-b", Summary: "合成产出 B", Status: statemachine.StatusFailed},
		{Slug: "step-c", Summary: "合成产出 C", Status: statemachine.StatusSkipped},
		{Slug: "step-d", Summary: "合成产出 D", Status: statemachine.StatusRolledBack},
		{Slug: "step-e", Summary: "合成产出 E", Status: statemachine.StatusInterrupted},
	}
	out := steer.BuildSummary(steps, steer.Caps{})
	if !strings.Contains(out, "step-a：合成产出 A") {
		t.Fatalf("成功环节缺失:\n%s", out)
	}
	for _, bad := range []string{"step-b", "step-c", "step-d", "step-e", "合成产出 B"} {
		if strings.Contains(out, bad) {
			t.Fatalf("非成功环节 %q 泄进摘要:\n%s", bad, out)
		}
	}
}

// TestBuildSummaryEmptyAndDegraded 空清单与读取失败的降级正文必须分得开
// （「没有完成」与「读不到」是两件事）。
func TestBuildSummaryEmptyAndDegraded(t *testing.T) {
	out := steer.BuildSummary(nil, steer.Caps{})
	if !strings.Contains(out, "尚未完成任何环节") {
		t.Fatalf("空清单应明示尚未完成:\n%s", out)
	}
	deg := steer.DegradedReport()
	if deg == "" {
		t.Fatal("降级正文不得为空")
	}
	if strings.Contains(out, "读取失败") {
		t.Fatalf("正常摘要不得带读取失败字样:\n%s", out)
	}
}

// TestBuildSummaryCaps 截断：单条按 StepSummary、整份按 Report，按 rune
// 截断（按 byte 切会把多字节字符拦腰截断）；负值 = 不截断（显式语义不归一）。
func TestBuildSummaryCaps(t *testing.T) {
	long := strings.Repeat("字", 80)
	steps := []steer.DoneStep{{Slug: "step-a", Summary: long, Status: statemachine.StatusSucceeded}}

	one := steer.BuildSummary(steps, steer.Caps{StepSummary: 5, Report: 400})
	if !strings.Contains(one, "step-a：字字字字…") {
		t.Fatalf("单条摘要未按 5 字符截断（4 字 + …）:\n%s", one)
	}
	capped := steer.BuildSummary(steps, steer.Caps{Report: 30})
	if n := len([]rune(capped)); n > 30 {
		t.Fatalf("整份摘要 %d 字符超过上限 30:\n%s", n, capped)
	}
	uncapped := steer.BuildSummary(steps, steer.Caps{StepSummary: -1, Report: -1})
	if !strings.Contains(uncapped, long) {
		t.Fatal("负上限 = 不截断（显式语义，不得静默归一）")
	}
	// 零值取缺省上限。
	if steer.DefaultStepSummaryCap <= 0 || steer.DefaultReportCap <= 0 {
		t.Fatal("缺省上限必须为正")
	}
}

// fakeStore 合成存储面：队列事实、环节清单、报告覆写、条件迁移全部可注入，
// 记录每次调用时刻的 ctx 健康度与迁移目标。
type fakeStore struct {
	mu     sync.Mutex
	status string // 库内现状态
	queue  bool

	queueErr   error
	steps      []steer.DoneStep
	stepsErr   error
	reportErr  error
	migrateErr error

	ctxErrs    []error
	reports    []string
	migrateTos []string
}

func (s *fakeStore) record(ctx context.Context) { s.ctxErrs = append(s.ctxErrs, ctx.Err()) }

func (s *fakeStore) ReadSteps(ctx context.Context) ([]steer.DoneStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx)
	if s.stepsErr != nil {
		return nil, s.stepsErr
	}
	return append([]steer.DoneStep(nil), s.steps...), nil
}

func (s *fakeStore) HasQueue(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx)
	if s.queueErr != nil {
		return false, s.queueErr
	}
	return s.queue, nil
}

func (s *fakeStore) SetReport(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx)
	s.reports = append(s.reports, text)
	return s.reportErr
}

func (s *fakeStore) Migrate(ctx context.Context, from []string, to string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(ctx)
	s.migrateTos = append(s.migrateTos, to)
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

func (s *fakeStore) assertDetached(t *testing.T) {
	t.Helper()
	if len(s.ctxErrs) == 0 {
		t.Fatal("存储面一次都没被调用")
	}
	for i, err := range s.ctxErrs {
		if err != nil {
			t.Fatalf("第 %d 次存储调用的 ctx 已取消: %v——收尾写必须走脱离 ctx", i, err)
		}
	}
}

// steeredCtx 造一个已按接管信号中止的编排 ctx（收尾发生在信号之后）。
func steeredCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := steer.Signal(context.Background())
	t.Cleanup(cancel)
	cancel()
	return ctx
}

// TestRunRequeueHit 回迁臂：队列非空 → 摘要落库 + 迁 running→created；
// 编排 ctx 已被接管信号取消，收尾写仍全部走脱离 ctx 执行（纪律①）。
func TestRunRequeueHit(t *testing.T) {
	st := &fakeStore{
		status: contract.StatusRunning,
		queue:  true,
		steps: []steer.DoneStep{
			{Slug: "step-a", Summary: "合成产出 A", Status: statemachine.StatusSucceeded},
		},
	}
	out, err := steer.Run(steeredCtx(t), st, steer.Spec{
		Header:     "## 已按新指令接管",
		FailReport: "## 不该被写",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Decision != steer.DecisionRequeue || !out.Hit || out.Status != contract.StatusCreated {
		t.Fatalf("Outcome = %+v, want requeue 命中 created", out)
	}
	st.assertDetached(t)
	if st.status != contract.StatusCreated {
		t.Fatalf("库内状态 = %q, want created", st.status)
	}
	if len(st.reports) != 1 {
		t.Fatalf("应恰好写 1 份报告: %v", st.reports)
	}
	if !strings.Contains(st.reports[0], "已按新指令接管") || !strings.Contains(st.reports[0], "step-a：合成产出 A") {
		t.Fatalf("报告缺文案头或成功环节摘要:\n%s", st.reports[0])
	}
}

// TestRunQueueEmptyFailConverge 队列空按失败收敛**不开空轮**：迁
// running→failed，绝不回 created。
func TestRunQueueEmptyFailConverge(t *testing.T) {
	st := &fakeStore{status: contract.StatusRunning, queue: false}
	out, err := steer.Run(steeredCtx(t), st, steer.Spec{
		FailReport: "## 接管后中断",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Decision != steer.DecisionFailConverge || !out.Hit || out.Status != contract.StatusFailed {
		t.Fatalf("Outcome = %+v, want fail_converge 命中 failed", out)
	}
	if st.status == contract.StatusCreated {
		t.Fatal("队列空却回了 created——开了无人认领的空轮")
	}
	if st.status != contract.StatusFailed {
		t.Fatalf("库内状态 = %q, want failed", st.status)
	}
	if len(st.reports) != 1 || st.reports[0] != "## 接管后中断" {
		t.Fatalf("失败报告未按注入落库: %v", st.reports)
	}
}

// TestRunReadStepsDegrades 读取失败走降级正文，**不中断收尾**（保产摘要的
// 失败面不得扩大成整轮收尾失败）。
func TestRunReadStepsDegrades(t *testing.T) {
	st := &fakeStore{
		status:   contract.StatusRunning,
		queue:    true,
		stepsErr: errors.New("存储不可达"),
	}
	out, err := steer.Run(steeredCtx(t), st, steer.Spec{Header: "## 已按新指令接管"})
	if err != nil {
		t.Fatalf("读取失败不得中断收尾: %v", err)
	}
	if !out.Hit || out.Status != contract.StatusCreated {
		t.Fatalf("Outcome = %+v, want 命中 created", out)
	}
	if len(st.reports) != 1 || !strings.Contains(st.reports[0], "读取失败") {
		t.Fatalf("降级正文未落库: %v", st.reports)
	}
}

// TestRunZeroRowSilent 迁移零行 = 他路已接手：不报状态（不谎报）、不动他路终态。
func TestRunZeroRowSilent(t *testing.T) {
	st := &fakeStore{status: contract.StatusCancelled, queue: true}
	out, err := steer.Run(steeredCtx(t), st, steer.Spec{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Hit || out.Status != "" {
		t.Fatalf("Outcome = %+v, want 零行静默", out)
	}
	if st.status != contract.StatusCancelled {
		t.Fatalf("他路终态被改写成 %q", st.status)
	}
}

// TestRunHasQueueErrFailClosed 队列读失败按「无排队」判定（不开空轮的
// 保守侧）并留痕——死轮比少续跑一轮更贵。
func TestRunHasQueueErrFailClosed(t *testing.T) {
	st := &fakeStore{
		status:   contract.StatusRunning,
		queue:    true, // 事实有排队，但读不出来
		queueErr: errors.New("存储不可达"),
	}
	out, err := steer.Run(steeredCtx(t), st, steer.Spec{FailReport: "## 接管后中断"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Decision != steer.DecisionFailConverge {
		t.Fatalf("Outcome = %+v, want 读失败按无排队（fail_converge）", out)
	}
	if st.status == contract.StatusCreated {
		t.Fatal("队列读失败却回了 created——开了空轮")
	}
}

// TestRunMigrateErrorPropagates 迁移失败响亮报错（结论未知时不得静默）。
func TestRunMigrateErrorPropagates(t *testing.T) {
	st := &fakeStore{
		status:     contract.StatusRunning,
		queue:      true,
		migrateErr: errors.New("存储不可达"),
	}
	if _, err := steer.Run(steeredCtx(t), st, steer.Spec{}); err == nil {
		t.Fatal("迁移失败必须报错")
	}
}

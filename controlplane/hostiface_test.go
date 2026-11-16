package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oekit/pkg/errors"
)

// 本文件钉 issue #238：宿主自有的任务 ID / 运行 ID / 任务快照 / 问题类型 /
// 租户类型可以直接接入控制面窄契约，不必先转成 taskboard / tenant 的类型。
// 实现全部在**宿主侧**（测试替身），controlplane 只给声明。

type hostTaskID struct {
	Shard string
	Seq   uint32
}

type hostRunID struct {
	Epoch uint64
	Seq   uint32
}

type hostTask struct {
	ID    hostTaskID
	State string
}

type hostProblem struct {
	Task hostTaskID
	Code string
}

type hostTenant struct {
	Org   string
	Actor string
}

// hostPlane 宿主侧实现：只实现窄契约（不 import taskboard）。
type hostPlane struct {
	task        hostTask
	run         hostRunID
	released    []hostTaskID
	adjudicated []string
	terminated  []hostRunID
}

var _ HostPlane[hostTaskID, hostRunID, hostTask, hostProblem] = (*hostPlane)(nil)

func (p *hostPlane) TaskView(_ context.Context, id hostTaskID) (TaskViewOf[hostTask, hostProblem], error) {
	if id != p.task.ID {
		return TaskViewOf[hostTask, hostProblem]{}, ErrNotFound
	}
	return TaskViewOf[hostTask, hostProblem]{
		Task:     p.task,
		Problems: []hostProblem{{Task: id, Code: "synthetic_problem"}},
	}, nil
}

func (p *hostPlane) RunView(_ context.Context, id hostRunID) (RunViewOf[hostRunID, hostTask, hostProblem], error) {
	if id != p.run {
		return RunViewOf[hostRunID, hostTask, hostProblem]{}, ErrNotFound
	}
	return RunViewOf[hostRunID, hostTask, hostProblem]{
		RunID:  id,
		Status: "running",
		Gen:    7,
		Tasks:  []hostTask{p.task},
	}, nil
}

func (p *hostPlane) ForceRelease(_ context.Context, req ForceReleaseOf[hostTaskID]) error {
	p.released = append(p.released, req.TaskID)
	return nil
}

func (p *hostPlane) Adjudicate(_ context.Context, req Adjudication) error {
	p.adjudicated = append(p.adjudicated, req.EffectKey)
	return nil
}

func (p *hostPlane) Terminate(_ context.Context, req TerminationOf[hostRunID]) error {
	p.terminated = append(p.terminated, req.RunID)
	return nil
}

// TestHostPlaneAcceptsHostDefinedTypes 宿主自定义 ID / 快照 / 问题类型经窄契约
// 走完查询与三类介入，全部原样往返（无需转换层）。
func TestHostPlaneAcceptsHostDefinedTypes(t *testing.T) {
	p := &hostPlane{
		task: hostTask{ID: hostTaskID{Shard: "shard-a", Seq: 1}, State: "open"},
		run:  hostRunID{Epoch: 3, Seq: 2},
	}
	var plane HostPlane[hostTaskID, hostRunID, hostTask, hostProblem] = p
	ctx := context.Background()

	tv, err := plane.TaskView(ctx, p.task.ID)
	if err != nil {
		t.Fatalf("TaskView: %v", err)
	}
	if tv.Task.ID != p.task.ID || tv.Task.State != "open" {
		t.Fatalf("任务快照类型漂移: %+v", tv.Task)
	}
	if len(tv.Problems) != 1 || tv.Problems[0].Task != p.task.ID {
		t.Fatalf("问题类型漂移: %+v", tv.Problems)
	}
	if _, err := plane.TaskView(ctx, hostTaskID{Shard: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知任务应 ErrNotFound, got %v", err)
	}

	rv, err := plane.RunView(ctx, p.run)
	if err != nil {
		t.Fatalf("RunView: %v", err)
	}
	if rv.RunID != p.run || rv.Gen != 7 || len(rv.Tasks) != 1 {
		t.Fatalf("运行视图类型漂移: %+v", rv)
	}

	if err := plane.ForceRelease(ctx, ForceReleaseOf[hostTaskID]{
		TaskID: p.task.ID, Operator: "op-1", Basis: "synthetic basis",
	}); err != nil {
		t.Fatalf("ForceRelease: %v", err)
	}
	if err := plane.Adjudicate(ctx, Adjudication{
		EffectKey: "effect-1", Verdict: VerdictApplied, Operator: "op-1", Basis: "synthetic basis",
	}); err != nil {
		t.Fatalf("Adjudicate: %v", err)
	}
	if err := plane.Terminate(ctx, TerminationOf[hostRunID]{
		RunID: p.run, ExpectedGen: 7, Operator: "op-1", Basis: "synthetic basis",
	}); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	if len(p.released) != 1 || p.released[0] != p.task.ID {
		t.Fatalf("强制释放未拿到宿主任务 ID: %+v", p.released)
	}
	if len(p.adjudicated) != 1 || p.adjudicated[0] != "effect-1" {
		t.Fatalf("裁决未拿到效果键: %+v", p.adjudicated)
	}
	if len(p.terminated) != 1 || p.terminated[0] != p.run {
		t.Fatalf("终止未拿到宿主运行 ID: %+v", p.terminated)
	}
}

// TestHostRequestValidateUsesHostID 窄请求复用「无归责不介入」的留痕判据，
// 目标判空按宿主的 ID 类型做（零值可比较即可），不要求转成字符串。
func TestHostRequestValidateUsesHostID(t *testing.T) {
	if err := (ForceReleaseOf[hostTaskID]{Operator: "op", Basis: "b"}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("零值宿主任务 ID 应判请求不完整, got %v", err)
	}
	if err := (ForceReleaseOf[hostTaskID]{
		TaskID: hostTaskID{Shard: "s"}, Operator: "op", Basis: "b",
	}).Validate(); err != nil {
		t.Fatalf("完整宿主请求应通过: %v", err)
	}
	if err := (ForceReleaseOf[hostTaskID]{TaskID: hostTaskID{Shard: "s"}, Basis: "b"}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺 operator 应判请求不完整, got %v", err)
	}

	if err := (TerminationOf[hostRunID]{ExpectedGen: 1, Operator: "op", Basis: "b"}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("零值宿主运行 ID 应判请求不完整, got %v", err)
	}
	if err := (TerminationOf[hostRunID]{
		RunID: hostRunID{Epoch: 1}, ExpectedGen: 1, Operator: "op", Basis: "b",
	}).Validate(); err != nil {
		t.Fatalf("完整宿主终止请求应通过: %v", err)
	}
}

// hostAuthorizer 宿主自有租户类型：实现窄鉴权钩子，返回值不经过 tenant.Context。
type hostAuthorizer struct {
	tenant hostTenant
}

var _ AuthorizerOf[hostTenant] = hostAuthorizer{}

func (a hostAuthorizer) Authorize(*http.Request, Action, string) (hostTenant, error) {
	return a.tenant, nil
}

// TestHostAuthorizerCarriesHostTenantType 鉴权钩子的窄契约不解释租户类型：
// 宿主返回自己的 tenant 值，注入请求 context 的方式由宿主决定。
func TestHostAuthorizerCarriesHostTenantType(t *testing.T) {
	want := hostTenant{Org: "org-a", Actor: "actor-a"}
	var auth AuthorizerOf[hostTenant] = hostAuthorizer{tenant: want}
	req := httptest.NewRequest(http.MethodGet, "/host/task-view", nil)
	got, err := auth.Authorize(req, ActionQuery, "target-1")
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if got != want {
		t.Fatalf("租户类型往返漂移: %+v, want %+v", got, want)
	}
}

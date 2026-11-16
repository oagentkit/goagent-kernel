package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oekit/pkg/errors"
)

// 本文件是泛型 Plane 契约（issue #238）的验收判据：宿主用**自己的**
// 任务 ID / 任务快照 / 不变量项 / 租户上下文类型实现整套
// Plane + Authorizer + Handler——全程不出现 taskboard.TaskID /
// taskboard.Task / taskboard.Problem / tenant.Context。
// 泛型化之前这四型都是硬绑定，宿主自有类型接不进来。

// ── 宿主的四种自有类型（与 kernel 四型零关系）────────────────────────────

// hostTaskID 宿主自有任务标识。
type hostTaskID string

// hostTask 宿主自有任务快照形态（字段与 taskboard.Task 无同构关系）。
type hostTask struct {
	ID       hostTaskID
	State    string
	Assignee string
}

// hostProblem 宿主自有不变量项。
type hostProblem struct {
	Task hostTaskID
	Say  string
}

// hostTenant 宿主自有租户上下文。
type hostTenant struct {
	Realm string
	User  string
}

// hostTenantKey 宿主自己的 request context 键（不经 tenant.WithContext）。
type hostTenantKey struct{}

// hostPlane 宿主实现：查询 + 强释（其余介入动作对本验收不必要，报哨兵即可）。
// 租户 fail-closed 用**宿主自己的**判定：ctx 里没有 hostTenant ⇒ ErrNotFound。
type hostPlane struct {
	tasks  map[hostTaskID]hostTask
	leased map[hostTaskID]bool
}

func (p *hostPlane) TaskView(ctx context.Context, id hostTaskID) (TaskView[hostTask, hostProblem], error) {
	_, ok := ctx.Value(hostTenantKey{}).(hostTenant)
	if !ok {
		return TaskView[hostTask, hostProblem]{}, errors.New("host: no tenant")
	}
	t, ok := p.tasks[id]
	if !ok {
		return TaskView[hostTask, hostProblem]{}, ErrNotFound
	}
	var probs []hostProblem
	if t.State == "claimed" && t.Assignee == "" {
		probs = append(probs, hostProblem{Task: id, Say: "claimed without assignee"})
	}
	return TaskView[hostTask, hostProblem]{Task: t, Problems: probs}, nil
}

func (p *hostPlane) RunView(ctx context.Context, id RunID) (RunView[hostTask, hostProblem], error) {
	if _, ok := ctx.Value(hostTenantKey{}).(hostTenant); !ok {
		return RunView[hostTask, hostProblem]{}, errors.New("host: no tenant")
	}
	out := RunView[hostTask, hostProblem]{RunID: id, Status: "running", Gen: 1}
	for _, t := range p.tasks {
		out.Tasks = append(out.Tasks, t)
	}
	return out, nil
}

func (p *hostPlane) ForceRelease(ctx context.Context, req ForceRelease[hostTaskID]) error {
	tc, ok := ctx.Value(hostTenantKey{}).(hostTenant)
	if !ok {
		return errors.New("host: no tenant")
	}
	if req.Operator == "" || tc.Realm == "" {
		return ErrInvalidRequest
	}
	t, ok := p.tasks[req.TaskID]
	if !ok {
		return ErrNotFound
	}
	if t.State != "claimed" {
		return ErrStaleRun // 宿主哨兵语义此处仅作占位
	}
	t.State = "open"
	t.Assignee = ""
	p.tasks[req.TaskID] = t
	p.leased[req.TaskID] = false
	return nil
}

func (p *hostPlane) Adjudicate(_ context.Context, _ Adjudication) error { return nil }
func (p *hostPlane) Terminate(_ context.Context, _ Termination) error   { return nil }

// hostAuth 宿主鉴权钩子：从 X-Realm / X-User 头解析自有租户类型，
// Inject 用**宿主自己的** context 键注入。
type hostAuth struct{ deny bool }

func (a hostAuth) Authorize(_ *http.Request, _ Action, _ string) (hostTenant, error) {
	if a.deny {
		return hostTenant{}, errors.New("host: denied")
	}
	return hostTenant{Realm: "eng", User: "op-9"}, nil
}

func (a hostAuth) Inject(ctx context.Context, tc hostTenant) context.Context {
	return context.WithValue(ctx, hostTenantKey{}, tc)
}

// Test宿主自有类型接入整套控制面 —— issue #238 验收：泛型契约下宿主的
// ID / 快照 / 不变量 / 租户类型贯穿 HTTP 面 → 鉴权钩子 → Plane 实现全链路。
func Test宿主自有类型接入整套控制面(t *testing.T) {
	p := &hostPlane{
		tasks: map[hostTaskID]hostTask{
			"T-100": {ID: "T-100", State: "claimed", Assignee: "w1"},
		},
		leased: map[hostTaskID]bool{},
	}
	h := NewHandler[hostTaskID, hostTask, hostProblem, hostTenant](p, hostAuth{})

	// 查询：宿主快照形态原样出 JSON（不经 taskboard.Task 投影）。
	rec := h.serve(t, http.MethodGet, "/tasks/T-100", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	var tv TaskView[hostTask, hostProblem]
	if err := json.Unmarshal(rec.Body.Bytes(), &tv); err != nil {
		t.Fatalf("响应不是 TaskView: %v", err)
	}
	if tv.Task.ID != hostTaskID("T-100") || tv.Task.Assignee != "w1" {
		t.Fatalf("宿主快照形态漂移: %+v", tv.Task)
	}

	// 介入：路径 id 转成宿主 ID 类型；鉴权钩子的自有租户类型经宿主 Inject
	// 进 ctx，被宿主实现读到（tenant.Context 全程缺席）。
	body := bytes.NewReader([]byte(`{"operator":"op-9","basis":"worker失联"}`))
	rec = h.serveRaw(t, http.MethodPost, "/tasks/T-100/force-release", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body.String())
	}
	if got := p.tasks["T-100"]; got.State != "open" || got.Assignee != "" {
		t.Fatalf("强释未生效: %+v", got)
	}

	// 钩子拒绝 ⇒ 403 且不触达 Plane。
	denied := NewHandler[hostTaskID, hostTask, hostProblem, hostTenant](p, hostAuth{deny: true})
	rec = denied.serve(t, http.MethodGet, "/tasks/T-100", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("钩子拒绝应 403，实得 %d", rec.Code)
	}
}

// Test宿主租户缺位failclosed —— fail-closed 判据随宿主实现走：
// Authorizer 没注入租户（宿主 Inject 是 no-op）⇒ 宿主实现拒绝，读路径 500
// 投影（宿主错误哨兵未登记进 statusFor，宿主应包自己的映射——契约面只
// 承诺 kernel 哨兵的映射）。
func Test宿主租户缺位failclosed(t *testing.T) {
	p := &hostPlane{
		tasks: map[hostTaskID]hostTask{"T-7": {ID: "T-7", State: "open"}},
	}
	anon := NewHandler[hostTaskID, hostTask, hostProblem, hostTenant](p, anonHostAuth{})
	rec := anon.serve(t, http.MethodGet, "/tasks/T-7", nil)
	if rec.Code == http.StatusOK {
		t.Fatal("宿主租户缺位必须被宿主实现拒绝（fail-closed 随宿主走）")
	}
}

// anonHostAuth 注入零值租户的宿主钩子（模拟宿主 Authorizer 的 bug 形态）。
type anonHostAuth struct{}

func (anonHostAuth) Authorize(_ *http.Request, _ Action, _ string) (hostTenant, error) {
	return hostTenant{}, nil
}
func (anonHostAuth) Inject(ctx context.Context, tc hostTenant) context.Context {
	if tc.Realm == "" {
		return ctx // 零值不注入 ⇒ 宿主实现按「无租户」拒绝
	}
	return context.WithValue(ctx, hostTenantKey{}, tc)
}

// serve / serveRaw 测试便捷出口。
func (h *Handler[ID, Task, Problem, TC]) serve(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	return h.serveRaw(t, method, path, rd)
}

func (h *Handler[ID, Task, Problem, TC]) serveRaw(t *testing.T, method, path string, rd *bytes.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

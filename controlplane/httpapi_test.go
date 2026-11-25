package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/contract"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// fakeAuth 鉴权钩子夹具：注入固定的租户上下文，或按动词拒绝。
type fakeAuth struct {
	deny bool
	ctx  tenant.Context
}

func (f fakeAuth) Authorize(_ *http.Request, _ Action, _ string) (tenant.Context, error) {
	if f.deny {
		return tenant.Context{}, errors.New("fakeAuth: 钩子判定拒绝")
	}
	return f.ctx, nil
}

func setup(t *testing.T) (*Handler, *MemAuditor, *MemPlane) {
	t.Helper()
	m, aud := seededPlane(t)
	h := NewHandler(m, fakeAuth{ctx: tenant.Context{Tenant: "acme", Scopes: []string{"control.intervene"}}})
	return h, aud, m
}

func doJSON(h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json") // 介入面的 Content-Type 闸（issue #198）
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("错误响应不是 JSON： %s", rec.Body.String())
	}
	return out.Error
}

func TestHandlerNoAuthorizerFailsClosed(t *testing.T) {
	m, _ := seededPlane(t)
	h := NewHandler(m, nil) // 鉴权面缺位：控制面不开无鉴权的口子

	rec := doJSON(h, http.MethodGet, "/tasks/t1", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无鉴权面查询应 401，got %d", rec.Code)
	}
	rec = doJSON(h, http.MethodPost, "/tasks/t1/force-release", ForceRelease{Operator: "op", Basis: "b"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无鉴权面介入应 401，got %d", rec.Code)
	}
}

func TestHandlerQueryEndpoints(t *testing.T) {
	h, _, _ := setup(t)

	// 任务视图
	rec := doJSON(h, http.MethodGet, "/tasks/t1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tasks/t1 = %d: %s", rec.Code, rec.Body.String())
	}
	var tv TaskView
	if err := json.Unmarshal(rec.Body.Bytes(), &tv); err != nil {
		t.Fatalf("响应不是 TaskView： %v", err)
	}
	if tv.Task.ID != "t1" {
		t.Fatalf("任务视图漂移： %+v", tv.Task)
	}

	// 运行视图
	rec = doJSON(h, http.MethodGet, "/runs/r1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /runs/r1 = %d", rec.Code)
	}
	var rv RunView
	if err := json.Unmarshal(rec.Body.Bytes(), &rv); err != nil {
		t.Fatalf("响应不是 RunView： %v", err)
	}
	if rv.Status != contract.StatusRunning || rv.Gen != 1 || len(rv.Tasks) != 2 {
		t.Fatalf("运行视图漂移： %+v", rv)
	}

	// 目标不存在 → 404
	rec = doJSON(h, http.MethodGet, "/tasks/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知任务应 404，got %d", rec.Code)
	}
	if msg := errBody(t, rec); msg == "" {
		t.Fatal("404 响应应带 error 明细")
	}
}

func TestHandlerInterventionEndpoints(t *testing.T) {
	h, aud, m := setup(t)

	// 鉴权钩子拒绝 → 403，不触达 Plane
	denied := NewHandler(m, fakeAuth{deny: true})
	rec := doJSON(denied, http.MethodPost, "/tasks/t1/force-release", ForceRelease{Operator: "op", Basis: "b"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("钩子拒绝应 403，got %d", rec.Code)
	}

	// 强释：路径参数是目标唯一来源（body 里的 TaskID 被忽略）
	claimCtx := tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"})
	if _, err := m.Claim(claimCtx, "t1", "worker-a", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	rec = doJSON(h, http.MethodPost, "/tasks/t1/force-release",
		ForceRelease{TaskID: "body-声称的其他任务", Operator: "op-1", Basis: "持有者失联"})
	if rec.Code != http.StatusOK {
		t.Fatalf("强释应 200，got %d: %s", rec.Code, rec.Body.String())
	}
	entries, _ := aud.Entries(context.Background(), "t1")
	if len(entries) != 1 || entries[0].Operator != "op-1" {
		t.Fatalf("介入留痕漂移： %+v", entries)
	}
	// 被忽略的 body TaskID 没有误伤别的任务
	if _, err := m.TaskView(claimCtx, "body-声称的其他任务"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("body 里的目标不应被介入： %v", err)
	}
}

func TestHandlerErrorMapping(t *testing.T) {
	h, _, m := setup(t)

	// 坏 JSON → 400
	req := httptest.NewRequest(http.MethodPost, "/tasks/t1/force-release", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400，got %d", rec.Code)
	}

	// 介入请求缺归责字段 → 400
	rec = doJSON(h, http.MethodPost, "/tasks/t1/force-release", ForceRelease{Operator: "op"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 basis 应 400，got %d", rec.Code)
	}

	// 未注入租户上下文（钩子给了零值）→ Plane 拒收 → 401
	anon := NewHandler(m, fakeAuth{})
	rec = doJSON(anon, http.MethodPost, "/tasks/t1/force-release", ForceRelease{Operator: "op", Basis: "b"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("零值租户上下文应 401（Plane fail-closed），got %d", rec.Code)
	}

	// 终止：过期视图 → 409
	viewCtx := tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"})
	view, err := m.RunView(viewCtx, "r1")
	if err != nil {
		t.Fatalf("RunView: %v", err)
	}
	rec = doJSON(h, http.MethodPost, "/runs/r1/terminate",
		Termination{ExpectedGen: view.Gen + 5, Operator: "op", Basis: "b"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("过期视图应 409，got %d", rec.Code)
	}

	// 方法不匹配 → 405（mux 对已注册模式的方法不匹配返回 405）
	rec = doJSON(h, http.MethodGet, "/runs/r1/terminate", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("方法不匹配应 405，got %d", rec.Code)
	}
}

// TestHandlerContentTypeGate 介入面的 Content-Type 闸（issue #198）。
//
// 反面：浏览器的跨站「简单请求」不触发 CORS 预检——text/plain 的 fetch、
// urlencoded 的表单都能直接 POST 到 force-release 这类不可幂等介入；Go 的
// JSON 解码器本不看 Content-Type，没有闸它们会被当合法请求处理。两种形态
// 加上缺头一律 415，且不触达 Plane（介入未发生、留痕为空）。
// 正面：application/json（含 charset 参数）照常通过。
func TestHandlerContentTypeGate(t *testing.T) {
	h, aud, m := setup(t)
	claimCtx := tenant.WithContext(context.Background(), tenant.Context{Tenant: "acme"})
	if _, err := m.Claim(claimCtx, "t1", "worker-a", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	post := func(contentType, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/tasks/t1/force-release", strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	crossSite := `{"operator":"evil","basis":"跨站页面构造的强释"}`

	// 反面：跨站简单请求的两种形态 + 缺头
	for _, tc := range []struct {
		name string
		ct   string
	}{
		{"text/plain（跨站 fetch 形态）→ 415", "text/plain"},
		{"表单 urlencoded → 415", "application/x-www-form-urlencoded"},
		{"缺 Content-Type → 415", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(tc.ct, crossSite)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s 应 415，got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
			entries, _ := aud.Entries(context.Background(), "t1")
			if len(entries) != 0 {
				t.Fatalf("介入不得触达 Plane（留痕=%+v）", entries)
			}
			tv, err := m.TaskView(claimCtx, "t1")
			if err != nil {
				t.Fatalf("TaskView: %v", err)
			}
			if holder := tv.Task.Owner; holder != "worker-a" {
				t.Fatalf("任务租约不得被跨站请求动掉：owner=%q", holder)
			}
		})
	}

	// 正面：application/json 照常（含 charset 参数的归一形态）
	t.Run("application/json → 200", func(t *testing.T) {
		rec := post("application/json", `{"operator":"op-1","basis":"持有者失联"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("合法介入应 200，got %d: %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("application/json; charset=utf-8 → 200", func(t *testing.T) {
		if _, err := m.Claim(claimCtx, "t1", "worker-b", 0); err != nil {
			t.Fatalf("重认领（上一轮强释后任务回开放态）: %v", err)
		}
		rec := post("application/json; charset=utf-8", `{"operator":"op-2","basis":"b"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("charset 参数不影响判定，应 200，got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

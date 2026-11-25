// httpapi.go —— 控制面的 net/http JSON 适配面。
//
// # 鉴权边界：内核不鉴权
//
// scope 词表、租户体系、会话形态全是宿主资产。Handler 持有 Authorizer 钩子
// （宿主实现）：它从请求解析主体与租户、判定该动作是否可执行，返回的
// tenant.Context 被注入 request context 传给 Plane——Plane 侧的
// tenant.Require（读与写共用的 fail-closed 判据，issue #197）据此有据可判。
// 这是「HTTP 面 ↔ 机制面」的唯一接缝：宿主在钩子里做完自己的判定，
// 机制面拿到的已经是判定过的上下文。
//
// 两层 fail-closed，缺一即 401：钩子缺位 ⇒ 全部请求 401；钩子在但对任何
// 请求返回零值 tenant.Context + nil ⇒ 全部请求（**含两个只读端点**）
// 仍被 Plane 的 tenant.Require 拒成 401——读路径不做第二道闸的话，
// 「同样的鉴权钩子、同样的错误，只有读放行」就是口子（issue #197）。
//
// # CSRF 边界（issue #198）：Authorization 头形态，Cookie 会话不在此面
//
// 介入端点全部是 POST + JSON body，而浏览器的跨站「简单请求」（表单的
// urlencoded、fetch 的 text/plain）不触发 CORS 预检，可以直接打进来——
// force-release / terminate / adjudicate 又都是不可幂等的介入。防线两层，
// 都在本包写死：
//
//  1. Content-Type 闸（见 decode）：介入请求只收 application/json，缺省与
//     其它类型一律 415——两种跨站简单请求形态都挡在 JSON 解码之前（Go 的
//     JSON 解码器本不看 Content-Type，不设闸它们会被当合法请求处理）；
//  2. 鉴权只认 Authorization 头：宿主钩子（Authorizer）从请求头解析凭据，
//     本面**禁止 Cookie 会话**——跨站请求带不上 Authorization 头，无凭据
//     即 401/403。宿主若坚持 Cookie 形态，CSRF 防线（同源校验 /
//     SameSite=Strict / 双提交令牌）归宿主，内核不背书。
//
// # 路由（Go 1.22+ 模式路由）
//
//	GET  /tasks/{id}                 → TaskView
//	GET  /runs/{id}                  → RunView
//	POST /tasks/{id}/force-release   body: ForceRelease
//	POST /effects/{key}/adjudicate   body: Adjudication
//	POST /runs/{id}/terminate        body: Termination
//
// 路径参数是介入目标的**唯一来源**：请求体里同名字段被忽略——同一个目标
// 不允许两条来源（一个真相）。
package controlplane

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"

	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/goagent-kernel/sideeffect"
	"github.com/oagentkit/goagent-kernel/taskboard"
	"github.com/oagentkit/goagent-kernel/tenant"
)

// Authorizer 鉴权钩子（宿主实现）：解析请求 → 判定动作是否可执行 →
// 返回注入 request context 的租户/权限上下文。返回错误 ⇒ 403，不触达 Plane。
type Authorizer interface {
	Authorize(r *http.Request, act Action, target string) (tenant.Context, error)
}

// Handler 控制面的 JSON 适配面。经 NewHandler 构造，直接挂到任意 http.Server。
type Handler struct {
	plane Plane
	auth  Authorizer
	mux   *http.ServeMux
}

// 请求体上限：控制面的介入请求都很小，1 MiB 远超所需——
// 上限挡的是误用（把文档当 body 发进来），不是业务需求。
const maxBodyBytes = 1 << 20

// NewHandler 构造适配面。auth 可为 nil——此时全部请求 401（见包内
// httpapi 头注：控制面不开无鉴权的口子）。
func NewHandler(p Plane, a Authorizer) *Handler {
	h := &Handler{plane: p, auth: a, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /tasks/{id}", h.taskView)
	h.mux.HandleFunc("GET /runs/{id}", h.runView)
	h.mux.HandleFunc("POST /tasks/{id}/force-release", h.forceRelease)
	h.mux.HandleFunc("POST /effects/{key}/adjudicate", h.adjudicate)
	h.mux.HandleFunc("POST /runs/{id}/terminate", h.terminate)
	return h
}

// ServeHTTP 实现 http.Handler。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

// authorize 鉴权闸：通过返回注入租户上下文的 request context；
// 不通过时已写好 4xx 响应，返回 false。
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, act Action, target string) (context.Context, bool) {
	if h.auth == nil {
		writeErr(w, http.StatusUnauthorized, errors.New("controlplane: 鉴权面未装配"))
		return nil, false
	}
	tc, err := h.auth.Authorize(r, act, target)
	if err != nil {
		writeErr(w, http.StatusForbidden, err)
		return nil, false
	}
	return tenant.WithContext(r.Context(), tc), true
}

func (h *Handler) taskView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, ok := h.authorize(w, r, ActionQuery, id)
	if !ok {
		return
	}
	v, err := h.plane.TaskView(ctx, taskboard.TaskID(id))
	h.write(w, v, err)
}

func (h *Handler) runView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, ok := h.authorize(w, r, ActionQuery, id)
	if !ok {
		return
	}
	v, err := h.plane.RunView(ctx, RunID(id))
	h.write(w, v, err)
}

func (h *Handler) forceRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, ok := h.authorize(w, r, ActionForceRelease, id)
	if !ok {
		return
	}
	var req ForceRelease
	if !h.decode(w, r, &req) {
		return
	}
	req.TaskID = taskboard.TaskID(id) // 目标以路径为准（body 同名字段被忽略）
	h.write(w, okBody{Status: "ok"}, h.plane.ForceRelease(ctx, req))
}

func (h *Handler) adjudicate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	ctx, ok := h.authorize(w, r, ActionAdjudicate, key)
	if !ok {
		return
	}
	var req Adjudication
	if !h.decode(w, r, &req) {
		return
	}
	req.EffectKey = key // 目标以路径为准（body 同名字段被忽略）
	h.write(w, okBody{Status: "ok"}, h.plane.Adjudicate(ctx, req))
}

func (h *Handler) terminate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, ok := h.authorize(w, r, ActionTerminate, id)
	if !ok {
		return
	}
	var req Termination
	if !h.decode(w, r, &req) {
		return
	}
	req.RunID = RunID(id) // 目标以路径为准（body 同名字段被忽略）
	h.write(w, okBody{Status: "ok"}, h.plane.Terminate(ctx, req))
}

// okBody 介入成功的响应体（错误明细走 writeErr）。
type okBody struct {
	Status string `json:"status"`
}

// decode 解析 JSON 请求体（带上限）。失败时已写好 4xx 响应，返回 false。
//
// Content-Type 闸（issue #198）：只收 application/json（charset 等参数经
// mime.ParseMediaType 归一，不影响判定）；缺省与其它类型一律 415——判定
// 失败不回退到「当作 JSON 试一试」，那是把闸开成摆设。
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, errors.New(
			`controlplane: 介入请求 Content-Type 必须是 application/json（got "`+
				r.Header.Get("Content-Type")+`"）`))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// write 统一出口：错误按哨兵映射状态码，成功按 JSON 编码。
func (h *Handler) write(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 写 JSON 错误体 {"error": "<msg>"}。
func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// statusFor 错误哨兵 → HTTP 状态码（控制面契约的 HTTP 投影）。
// 判定走 errors.Is（包装链一路解到哨兵），宿主实现的包装形态不丢映射。
func statusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, tenant.ErrNoContext):
		return http.StatusUnauthorized
	case errors.Is(err, tenant.ErrUnauthorized):
		return http.StatusForbidden
	case errors.Is(err, ErrStaleRun),
		errors.Is(err, ErrTerminalRun),
		errors.Is(err, taskboard.ErrLeaseLost),
		errors.Is(err, taskboard.ErrTaken),
		errors.Is(err, taskboard.ErrTerminal),
		errors.Is(err, taskboard.ErrBlocked),
		errors.Is(err, sideeffect.ErrNotUncertain),
		errors.Is(err, sideeffect.ErrUnsafeNotRetryable):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// read_failclosed_test.go —— issue #197 回归：控制面读路径的租户 fail-closed。
//
// 判据：三类介入写 + 两类读共用同一组「无租户上下文」用例——宿主
// Authorizer 返回零值 tenant.Context + nil error 时，五个端点一律 401，
// 不留「同样的鉴权钩子、同样的错误，只有读放行」的口子。匿名只读必须
// 装配期显式 AllowAnonymousRead(true)。
package controlplane

import (
	"context"
	"net/http"
	"testing"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/tenant"
)

// TestMemPlaneReadsFailClosedWithoutTenant 参考实现的读路径直接拒无租户
// 上下文（ Mechanism 面的闸，HTTP 面只是它的投影）。
func TestMemPlaneReadsFailClosedWithoutTenant(t *testing.T) {
	m, _ := seededPlane(t)
	ctx := context.Background() // 未注入租户上下文

	if _, err := m.TaskView(ctx, "t1"); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("无租户上下文的 TaskView 必须被拒 tenant.ErrNoContext（实得 %v）", err)
	}
	if _, err := m.RunView(ctx, "r1"); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("无租户上下文的 RunView 必须被拒 tenant.ErrNoContext（实得 %v）", err)
	}

	// 注入了租户上下文 ⇒ 照常可读（闸只拦无据可判的请求）。
	if _, err := m.TaskView(tenantCtx(), "t1"); err != nil {
		t.Fatalf("带租户上下文的 TaskView 应通过（实得 %v）", err)
	}
	if _, err := m.RunView(tenantCtx(), "r1"); err != nil {
		t.Fatalf("带租户上下文的 RunView 应通过（实得 %v）", err)
	}

	// 注入了空租户名的 Context 同样拒（tenant.Require 判 Tenant 非空）。
	emptyTenant := tenant.WithContext(context.Background(), tenant.Context{})
	if _, err := m.TaskView(emptyTenant, "t1"); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("Tenant 为空的上下文必须被拒（实得 %v）", err)
	}
}

// TestAllowAnonymousReadIsExplicitOptIn 匿名只读是显式装配选项：缺省不放行，
// 显式声明后读路径才跳过闸（写路径永远不跳）。
func TestAllowAnonymousReadIsExplicitOptIn(t *testing.T) {
	m, _ := seededPlane(t, AllowAnonymousRead(true))
	ctx := context.Background()

	if _, err := m.TaskView(ctx, "t1"); err != nil {
		t.Fatalf("显式匿名只读的 TaskView 应放行（实得 %v）", err)
	}
	if _, err := m.RunView(ctx, "r1"); err != nil {
		t.Fatalf("显式匿名只读的 RunView 应放行（实得 %v）", err)
	}
	// 写路径不随匿名读放开：介入仍要可归责的租户上下文。
	if err := m.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op", Basis: "b"}); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("匿名只读不得放开写路径（实得 %v）", err)
	}

	// 对照组：缺省装配（无选项）读路径必须仍拦。
	def, _ := seededPlane(t)
	if _, err := def.TaskView(ctx, "t1"); !errors.Is(err, tenant.ErrNoContext) {
		t.Fatalf("缺省装配不得匿名读（实得 %v）", err)
	}
}

// TestHandlerZeroValueContextAllEndpoints401 HTTP 面判据（验收原案）：
// 宿主 Authorizer 对任何请求返回零值 tenant.Context + nil error ⇒
// 两类读 + 三类写一律 401（写路径由 tenant.Require 拦，读路径由 #197
// 补上的同一道闸拦）。
func TestHandlerZeroValueContextAllEndpoints401(t *testing.T) {
	h, _, _ := setup(t)
	// 复用 setup 的 fakeAuth，但换成零值 Context 注入者（模拟宿主钩子 bug）。
	anon := NewHandler(h.plane, fakeAuth{ctx: tenant.Context{}})

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"读·任务视图", http.MethodGet, "/tasks/t1", nil},
		{"读·运行视图", http.MethodGet, "/runs/r1", nil},
		{"写·强制释放", http.MethodPost, "/tasks/t1/force-release", ForceRelease{Operator: "op", Basis: "b"}},
		{"写·裁决", http.MethodPost, "/effects/effect-1/adjudicate", Adjudication{Verdict: VerdictApplied, Operator: "op", Basis: "b"}},
		{"写·终止", http.MethodPost, "/runs/r1/terminate", Termination{ExpectedGen: 1, Operator: "op", Basis: "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(anon, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("零值租户上下文必须 401（实得 %d: %s）", rec.Code, rec.Body.String())
			}
		})
	}
}

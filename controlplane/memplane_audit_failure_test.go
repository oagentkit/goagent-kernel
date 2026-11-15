// memplane_audit_failure_test.go —— 三类介入共用的「留痕失败即无副作用」契约用例。
//
// 留痕与状态变更必须在同一临界区、同一顺序：先算下一状态并留痕，留痕成功
// 才写回。失败的介入若已改变状态与代次，并发介入者的旧视图就被无声作废——
// 故三类介入（Terminate / ForceRelease / Adjudicate）共用同一组用例钉住
// 这个方向。拆成独立文件只为满足单文件行数阈值（见 architecture-policy.yaml）。
package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"

	"github.com/yi-nology/goagent-kernel/sideeffect"
	"github.com/yi-nology/goagent-kernel/taskboard"
)

// flakyAuditor 可注入失败节奏的留痕面：包装 MemAuditor，fail=true 时
// Append 一律失败。用于注入「留痕失败」路径——三类介入共用同一组
// 「失败即无副作用」契约用例钉住这个方向。
type flakyAuditor struct {
	*MemAuditor
	fail bool
}

// errInjectedAudit 注入的留痕失败（测试哨兵：断言介入的错误可归因到
// 留痕失败，而不是撞上相邻的偶然原因）。
var errInjectedAudit = errors.New("controlplane 测试: 注入的留痕失败")

func (a *flakyAuditor) Append(ctx context.Context, e AuditEntry) error {
	if a.fail {
		return errInjectedAudit
	}
	return a.MemAuditor.Append(ctx, e)
}

// TestInterventionAuditFailureHasNoSideEffect 三类介入（Terminate /
// ForceRelease / Adjudicate）共用的契约用例：注入 Auditor.Append 失败 ⇒
// 介入返回 error，且可观测状态与代次与调用前逐字相同。留痕失败即整体失败——
// 没有「先改状态后补痕迹」的窗口（失败的介入若已改变状态，并发介入者的
// 旧视图就被无声作废）。
//
// 断言骨架见 assertAuditFailureNoSideEffect：有读视图的介入直接断言视图
// 逐字相同；Adjudicate 无读视图，由「重试同一请求必须成功 + 随后撞
// ErrNotUncertain」承担观察。
func TestInterventionAuditFailureHasNoSideEffect(t *testing.T) {
	cases := []struct {
		name string
		exec func(t *testing.T)
	}{
		{
			name: "Terminate",
			exec: func(t *testing.T) {
				aud := &flakyAuditor{MemAuditor: &MemAuditor{}}
				m := seededPlaneWithAuditor(t, aud)
				ctx := tenantCtx()

				before, err := m.RunView(ctx, "r1")
				if err != nil {
					t.Fatalf("RunView: %v", err)
				}
				req := Termination{RunID: "r1", ExpectedGen: before.Gen, Operator: "op-1", Basis: "超时未产出"}
				act := func() error { return m.Terminate(ctx, req) }

				assertAuditFailureNoSideEffect(t, aud, act, func(t *testing.T) {
					after, err := m.RunView(ctx, "r1")
					if err != nil {
						t.Fatalf("RunView after: %v", err)
					}
					if after.Status != before.Status || after.Gen != before.Gen {
						t.Fatalf("留痕失败不得改变运行状态：Status %q -> %q，Gen %d -> %d",
							before.Status, after.Status, before.Gen, after.Gen)
					}
				}, ErrStaleRun)
			},
		},
		{
			name: "ForceRelease",
			exec: func(t *testing.T) {
				aud := &flakyAuditor{MemAuditor: &MemAuditor{}}
				m := seededPlaneWithAuditor(t, aud)
				ctx := tenantCtx()

				held, err := m.Claim(ctx, "t1", "w1", time.Minute)
				if err != nil {
					t.Fatalf("Claim: %v", err)
				}
				before, err := m.TaskView(ctx, "t1")
				if err != nil {
					t.Fatalf("TaskView: %v", err)
				}
				if before.Task.Lease.Token != held.Lease.Token {
					t.Fatalf("场景不自洽：TaskView 租约与认领结果不一致： %+v vs %+v", before.Task.Lease, held.Lease)
				}
				act := func() error {
					return m.ForceRelease(ctx, ForceRelease{TaskID: "t1", Operator: "op-1", Basis: "持有者失联"})
				}

				assertAuditFailureNoSideEffect(t, aud, act, func(t *testing.T) {
					after, err := m.TaskView(ctx, "t1")
					if err != nil {
						t.Fatalf("TaskView after: %v", err)
					}
					a := after.Task
					if a.State != taskboard.StateClaimed || a.Owner != before.Task.Owner ||
						a.Seq != before.Task.Seq || a.Lease.Token != before.Task.Lease.Token ||
						a.Lease.Fence != before.Task.Lease.Fence {
						t.Fatalf("留痕失败不得改变租约：before %+v after %+v", before.Task, a)
					}
				}, taskboard.ErrLeaseLost)
			},
		},
		{
			name: "Adjudicate",
			exec: func(t *testing.T) {
				aud := &flakyAuditor{MemAuditor: &MemAuditor{}}
				m := seededPlaneWithAuditor(t, aud)
				ctx := tenantCtx()

				act := func() error {
					return m.Adjudicate(ctx, Adjudication{
						EffectKey: "effect-1", Verdict: VerdictApplied, Operator: "op-1", Basis: "下游确认已写入",
					})
				}
				// 效果键的裁决状态没有查询面：无副作用由骨架的「重试同一请求
				// 必须成功 + 随后撞 ErrNotUncertain」承担观察。
				assertAuditFailureNoSideEffect(t, aud, act, nil, sideeffect.ErrNotUncertain)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.exec)
	}
}

// assertAuditFailureNoSideEffect 三类介入共用的「失败即无副作用」断言骨架：
//
//  1. 注入留痕失败 ⇒ 介入报错且可归因到留痕失败；
//  2. observe 断言视图与调用前逐字相同（可为 nil：无读视图的介入由第 3 步观察）；
//  3. 修复留痕面后重试同一请求必须成功——失败那次若已生效，重试必撞各自的
//     「已生效」哨兵；重试成功后再执行同一请求撞上 sentinel，证明第 3 步
//     真的生效了（骨架自身不空转）。
//
// 对「先改状态后补留痕」的实现，第 2 步或第 3 步必红。
func assertAuditFailureNoSideEffect(t *testing.T, aud *flakyAuditor, act func() error, observe func(t *testing.T), sentinel error) {
	t.Helper()

	aud.fail = true
	if err := act(); !errors.Is(err, errInjectedAudit) {
		t.Fatalf("留痕失败应使介入失败并归因到留痕失败，got %v", err)
	}
	if observe != nil {
		observe(t)
	}

	aud.fail = false
	if err := act(); err != nil {
		t.Fatalf("留痕失败后重试同一介入应成功（失败那一次不得已生效），got %v", err)
	}
	if err := act(); !errors.Is(err, sentinel) {
		t.Fatalf("介入生效后重复同一请求应撞哨兵 %v（证明重试真的生效），got %v", sentinel, err)
	}
}

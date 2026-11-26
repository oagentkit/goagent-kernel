// managed_egress_test.go —— issue #212 回归：受管后端的出口策略维度。
//
// 判据：Managed.Check 必须透传请求级 Egress（此前整个丢掉，放行理由却宣称
// 「出口」已同意）；装配钉死的 Managed.Egress 在 Check 与 Exec 两个入口都判
// 且不被调用放宽；放行理由只陈述真正判过的维度。
package gatecheck

import (
	"context"
	"strings"
	"testing"

	"github.com/oagentkit/guardx/sandbox"
	"github.com/oagentkit/toolgate/policy"

	"github.com/yi-nology/goagent-kernel/egress"
)

// TestManagedCheckRequestEgressNotDropped（验收原案）：Managed + 请求级
// Egress ModeDenied + 不声明出口能力的后端 ⇒ 不得 Proceed。
// 修复前：Egress 被静默丢弃，结论 OutcomeProceed、理由含「出口」。
func TestManagedCheckRequestEgressNotDropped(t *testing.T) {
	m := NewManaged(fakeProvider{level: sandbox.LevelProcess}, gateOf(policy.VerdictAuto),
		registryOf(sandbox.LevelProcess), sandbox.LevelNone)

	d := m.Check(context.Background(), Request{
		Op:      policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)},
		Egress:  egress.Spec{Mode: egress.ModeDenied},
		Program: "print(1)",
	})
	if d.Outcome == OutcomeProceed {
		t.Fatalf("ModeDenied 的出口要求不得被静默吞掉后放行（Outcome=%v Reason=%q）", d.Outcome, d.Reason)
	}
	if d.Outcome != OutcomeEgressShort {
		t.Fatalf("出口策略兑现不了应判 egress_short（实得 %v：%s）", d.Outcome, d.Reason)
	}
}

// TestManagedCheckEgressSilentProviderShort 后端对出口能力沉默（没实现
// Enforcer/Verifier）⇒ 沉默按最弱处理 ⇒ egress_short。
func TestManagedCheckEgressSilentProviderShort(t *testing.T) {
	m := NewManaged(fakeProvider{level: sandbox.LevelProcess}, gateOf(policy.VerdictAuto),
		registryWith(fakeProvider{level: sandbox.LevelProcess}), sandbox.LevelNone)

	d := m.Check(context.Background(), Request{
		Op:     policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)},
		Egress: egress.Spec{Mode: egress.ModeProxy, Proxy: "http://127.0.0.1:3128"},
	})
	if d.Outcome != OutcomeEgressShort {
		t.Fatalf("沉默的后端兑现不了出口要求（实得 %v：%s）", d.Outcome, d.Reason)
	}
}

// TestManagedPinnedEgressJudgedOnBothEntries 装配钉死的 Managed.Egress：
// Check（请求不带出口要求）与 Exec（sandbox.Request 没有出口通道）两个
// 入口都按钉死策略判；Exec 判不过 ⇒ 不委派（内层一次都不跑）。
func TestManagedPinnedEgressJudgedOnBothEntries(t *testing.T) {
	calls := 0
	inner := countingProvider{level: sandbox.LevelProcess, calls: &calls}
	m := NewManaged(inner, gateOf(policy.VerdictAuto),
		registryWith(fakeProvider{level: sandbox.LevelProcess}), sandbox.LevelNone)
	m.Egress = egress.Spec{Mode: egress.ModeDenied}
	// 审批层给个确定性 auto（变更标记显式声明），让判定走到出口那一维。
	m.OpFunc = func(string, sandbox.Budget) policy.Op {
		return policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)}
	}

	d := m.Check(context.Background(), Request{Op: policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)}})
	if d.Outcome != OutcomeEgressShort {
		t.Fatalf("钉死的出口策略必须在 Check 生效（实得 %v：%s）", d.Outcome, d.Reason)
	}

	_, err := m.Exec(context.Background(), sandbox.Request{Program: "print(1)"})
	if err == nil {
		t.Fatal("钉死的出口策略判不过时 Exec 不得委派执行")
	}
	if calls != 0 {
		t.Fatalf("判据不通过 ⇒ 内层后端一次都不跑（实得 %d 次）", calls)
	}
}

// TestManagedPinnedEgressNotRelaxedByRequest 钉死的档位不被调用放宽：
// 装配钉 ModeDenied、请求什么都不要求 ⇒ 仍按 ModeDenied 判（不放行）。
func TestManagedPinnedEgressNotRelaxedByRequest(t *testing.T) {
	m := NewManaged(fakeProvider{level: sandbox.LevelProcess}, gateOf(policy.VerdictAuto),
		registryWith(fakeProvider{level: sandbox.LevelProcess}), sandbox.LevelNone)
	m.Egress = egress.Spec{Mode: egress.ModeDenied}

	d := m.Check(context.Background(), Request{
		Op:      policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)},
		Egress:  egress.Spec{Mode: egress.ModeUnrestricted}, // 请求想完全不设防
		Program: "print(1)",
	})
	if d.Outcome != OutcomeEgressShort {
		t.Fatalf("钉死的 ModeDenied 不得被请求放宽成 Unrestricted（实得 %v：%s）", d.Outcome, d.Reason)
	}
}

// TestManagedPinnedEgressRequestStricterAllowed 请求比钉死策略更严 ⇒ 允许：
// 钉 Proxy、请求 Denied ⇒ 按 Denied 判，声明了该档能力的后端放行。
func TestManagedPinnedEgressRequestStricterAllowed(t *testing.T) {
	m := NewManaged(egressProvider{level: sandbox.LevelOS, mode: egress.ModeDenied},
		gateOf(policy.VerdictAuto),
		registryWith(egressProvider{level: sandbox.LevelOS, mode: egress.ModeDenied}),
		sandbox.LevelNone)
	m.Egress = egress.Spec{Mode: egress.ModeProxy, Proxy: "http://127.0.0.1:3128"}

	d := m.Check(context.Background(), Request{
		Op:     policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)},
		Egress: egress.Spec{Mode: egress.ModeDenied}, // 比钉死的 Proxy 更严
	})
	if d.Outcome != OutcomeProceed {
		t.Fatalf("更严的请求要求必须被受理（实得 %v：%s）", d.Outcome, d.Reason)
	}
	if !strings.Contains(d.Reason, "出口") {
		t.Fatalf("判过出口的放行理由应提及出口（实得 %q）", d.Reason)
	}
}

// TestProceedReasonStatesJudgedDimensionsOnly（验收原案）：放行理由只陈述
// 真正判过的维度——没判出口，「出口」二字不得出现（包级 Check 与受管
// Check 同判据）。
func TestProceedReasonStatesJudgedDimensionsOnly(t *testing.T) {
	// 包级 Check：只要求档位（无出口要求）⇒ 理由不得宣称「出口」同意。
	d := Check(context.Background(), gateOf(policy.VerdictAuto),
		registryOf(sandbox.LevelProcess), Request{
			Op:       policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)},
			MinLevel: sandbox.LevelProcess,
		})
	if d.Outcome != OutcomeProceed {
		t.Fatalf("档位满足应放行（实得 %v：%s）", d.Outcome, d.Reason)
	}
	if strings.Contains(d.Reason, "出口") {
		t.Fatalf("未判出口时放行理由不得宣称出口已同意（实得 %q）", d.Reason)
	}
	if !strings.Contains(d.Reason, "档位") {
		t.Fatalf("判过档位的放行理由应提及档位（实得 %q）", d.Reason)
	}

	// 受管 Check：同判据（无出口要求时理由同样不得提「出口」）。
	m := NewManaged(fakeProvider{level: sandbox.LevelProcess}, gateOf(policy.VerdictAuto),
		registryOf(sandbox.LevelProcess), sandbox.LevelNone)
	dm := m.Check(context.Background(), Request{Op: policy.Op{Type: policy.OpToolCall, Mutating: policy.Mutating(false)}})
	if dm.Outcome != OutcomeProceed {
		t.Fatalf("受管判据应放行（实得 %v：%s）", dm.Outcome, dm.Reason)
	}
	if strings.Contains(dm.Reason, "出口") {
		t.Fatalf("受管判据未判出口时理由不得宣称出口已同意（实得 %q）", dm.Reason)
	}
}

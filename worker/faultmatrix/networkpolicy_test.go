// networkpolicy_test.go —— 矩阵行十四：真实 NetworkPolicy 验证（Cluster）。
//
// 进程内可判的是**声明面门禁**（issue #262）：禁网档只在「策略装配开启 +
// CNI 能力事实为真 + 出站探测已装配」三者齐备时自报 EgressDenied；缺任何
// 一半都必须降级为 EgressUnrestricted（fail-closed，不静默假报）。
//
// 真集群格（requiresEnv）验证的是数据面：apiserver 接受 NetworkPolicy
// 不等于 CNI 真的阻断出站；该格用实际出站探测断言。
package faultmatrix

import (
	"testing"

	"github.com/oagentkit/guardx/sandbox"

	"github.com/yi-nology/goagent-kernel/examples/containerbackend"
)

// staticCNI 固定答案的 CNI 能力探测替身。
type staticCNI struct {
	enforced bool
	err      error
}

func (c staticCNI) NetworkPolicyEnforced() (bool, error) { return c.enforced, c.err }

func newDeclarationJob(opts ...containerbackend.K8sJobOption) *containerbackend.K8sJob {
	base := []containerbackend.K8sJobOption{
		containerbackend.WithK8sEndpoint("http://127.0.0.1:1"),
		containerbackend.WithK8sNamespace("faultmatrix"),
		containerbackend.WithK8sNetworkPolicy(true),
	}
	return containerbackend.NewK8sJob(append(base, opts...)...)
}

func probeSpec() containerbackend.K8sEgressProbe {
	return containerbackend.K8sEgressProbe{
		Image:   "registry.example/probe@sha256:feedface",
		Command: []string{"/probe", "-expect-blocked"},
	}
}

// runNetworkPolicyDeclaration 声明面三条全齐才报禁网；缺一条就降级。
// 这是「缺环境时仍可判」的进程内事实，真集群格也会先跑它。
func runNetworkPolicyDeclaration(t *testing.T) {
	// 缺 CNI 能力事实：答不上来 ⇒ 不得自报禁网。
	if got := newDeclarationJob(containerbackend.WithK8sEgressProbe(probeSpec())).Egress(); got != sandbox.EgressUnrestricted {
		t.Fatalf("缺 CNI 能力事实时必须降级为 unrestricted（实得 %v）", got)
	}
	// CNI 明确不实现 NetworkPolicy：不得自报禁网。
	if got := newDeclarationJob(
		containerbackend.WithK8sCNICapability(staticCNI{enforced: false}),
		containerbackend.WithK8sEgressProbe(probeSpec()),
	).Egress(); got != sandbox.EgressUnrestricted {
		t.Fatalf("CNI 不实现 NetworkPolicy 时必须降级（实得 %v）", got)
	}
	// CNI 能力探测报错：不可判 ⇒ 不得自报禁网。
	if got := newDeclarationJob(
		containerbackend.WithK8sCNICapability(staticCNI{err: errInjectedResponseLost}),
		containerbackend.WithK8sEgressProbe(probeSpec()),
	).Egress(); got != sandbox.EgressUnrestricted {
		t.Fatalf("CNI 能力不可判时必须降级（实得 %v）", got)
	}
	// 有 CNI 但没装配出站探测：没有实测面 ⇒ 不得自报禁网。
	if got := newDeclarationJob(containerbackend.WithK8sCNICapability(staticCNI{enforced: true})).Egress(); got != sandbox.EgressUnrestricted {
		t.Fatalf("缺出站探测时必须降级（实得 %v）", got)
	}
	// 策略装配被显式关闭：如实降级。
	if got := containerbackend.NewK8sJob(
		containerbackend.WithK8sEndpoint("http://127.0.0.1:1"),
		containerbackend.WithK8sNamespace("faultmatrix"),
		containerbackend.WithK8sNetworkPolicy(false),
		containerbackend.WithK8sCNICapability(staticCNI{enforced: true}),
		containerbackend.WithK8sEgressProbe(probeSpec()),
	).Egress(); got != sandbox.EgressUnrestricted {
		t.Fatalf("策略装配关闭时必须如实降级（实得 %v）", got)
	}
	// 三条齐备：声明禁网档。
	if got := newDeclarationJob(
		containerbackend.WithK8sCNICapability(staticCNI{enforced: true}),
		containerbackend.WithK8sEgressProbe(probeSpec()),
	).Egress(); got != sandbox.EgressDenied {
		t.Fatalf("策略 + CNI 能力 + 出站探测齐备时必须自报禁网档（实得 %v）", got)
	}
}

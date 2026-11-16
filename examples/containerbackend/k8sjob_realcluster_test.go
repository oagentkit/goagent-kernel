// k8sjob_realcluster_test.go —— issue #262 的真集群验收（验收标准 1 / 4）。
//
// # 跳过是显式声明，不是「观察到但跳过」
//
// 本文件的用例只能在真集群上兑现判定：数据面（CNI 是否真的实现
// NetworkPolicy）没有任何 httptest 替身可以证明。环境不齐时用例显式
// Skipf 并打印所需环境（与 worker/faultmatrix 的 matrixCase.requiresEnv
// 同一条纪律），缺什么写什么，绝不静默跳过。
//
// 所需环境（全部必填，缺一即 Skipf）：
//
//	GAK_K8S_E2E_ENDPOINT     apiserver 端点（https://…）
//	GAK_K8S_E2E_TOKEN_FILE   bearer token 文件路径（每请求重读）
//	GAK_K8S_E2E_CA_FILE      apiserver CA 文件路径
//	GAK_K8S_E2E_NAMESPACE    专用执行命名空间（须已存在且可建 Job/NetworkPolicy）
//	GAK_K8S_E2E_PROBE_IMAGE  出站探测镜像（探测约定：尝试访问外部地址，
//	                         连上=exit 1，被阻断=exit 0）
//	GAK_K8S_E2E_JOB_IMAGE    业务镜像（须含 sh 与 wget/curl，本用例用它复核探测）
package containerbackend

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	kerrors "github.com/oekit/pkg/errors"

	"github.com/oagentkit/guardx/sandbox"
	"github.com/yi-nology/goagent-kernel/backend"
)

const (
	envK8sE2EEndpoint   = "GAK_K8S_E2E_ENDPOINT"
	envK8sE2ETokenFile  = "GAK_K8S_E2E_TOKEN_FILE"
	envK8sE2ECAFile     = "GAK_K8S_E2E_CA_FILE"
	envK8sE2ENamespace  = "GAK_K8S_E2E_NAMESPACE"
	envK8sE2EProbeImage = "GAK_K8S_E2E_PROBE_IMAGE"
	envK8sE2EJobImage   = "GAK_K8S_E2E_JOB_IMAGE"
)

// k8sE2EEnvNames 环境清单：Skipf 文案与逐项检查共用，缺什么打印什么。
var k8sE2EEnvNames = []string{
	envK8sE2EEndpoint, envK8sE2ETokenFile, envK8sE2ECAFile,
	envK8sE2ENamespace, envK8sE2EProbeImage, envK8sE2EJobImage,
}

func k8sE2EEnv(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{}
	var missing []string
	for _, name := range k8sE2EEnvNames {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
			continue
		}
		env[name] = v
	}
	if len(missing) > 0 {
		t.Skipf("本用例只能真集群验证（数据面行为没有替身可证），需要环境：%s；缺：%s",
			strings.Join(k8sE2EEnvNames, ", "), strings.Join(missing, ", "))
	}
	return env
}

// TestK8sRealClusterEgressDenied 验收标准 1：真集群上禁网 Job 启动即尝试访问
// 外部地址，**必须失败**（探测被阻断 ⇒ 作业放行，作业内的出站尝试也必须失败）。
//
// 作业命令用「连上打 OPEN / 被阻断打 BLOCKED」的形态，让断言落在可读的
// 实测证据上，而不是落在含混的退出码上。
func TestK8sRealClusterEgressDenied(t *testing.T) {
	env := k8sE2EEnv(t)
	k := NewK8sJob(
		WithK8sEndpoint(env[envK8sE2EEndpoint]),
		WithK8sNamespace(env[envK8sE2ENamespace]),
		WithK8sExecutionNamespace(env[envK8sE2ENamespace]),
		WithK8sTokenFile(env[envK8sE2ETokenFile]),
		WithK8sCAFile(env[envK8sE2ECAFile]),
		WithK8sCNICapability(k8sE2ECapability{}),
		WithK8sEgressProbe(K8sEgressProbe{
			Image:   env[envK8sE2EProbeImage],
			Command: []string{"/bin/sh", "-ec", "if wget -T 5 -q -O /dev/null https://kubernetes.default.svc/healthz; then exit 1; else exit 0; fi"},
		}),
		WithK8sPollInterval(2*time.Second),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := k.Exec(ctx, backend.Job{
		Image:  env[envK8sE2EJobImage],
		Args:   []string{"-ec", "if wget -T 5 -q -O /dev/null https://kubernetes.default.svc/healthz; then echo EGRESS_OPEN; else echo EGRESS_BLOCKED; fi"},
		Budget: sandbox.Budget{MaxMemory: 1 << 26},
	})
	if err != nil {
		t.Fatalf("真集群 Exec 基础设施故障：%v", err)
	}
	if !res.OK {
		t.Fatalf("探测被阻断后业务容器应能跑完自身判定：res=%+v", res)
	}
	if !strings.Contains(res.Stdout, "EGRESS_BLOCKED") || strings.Contains(res.Stdout, "EGRESS_OPEN") {
		t.Fatalf("禁网 Job 的出站尝试必须失败：stdout=%q detail=%+v", res.Stdout, res.Detail)
	}
	if d, ok := res.Detail.(map[string]any); ok && d["egress_verified"] != "probe_exit_0" {
		t.Fatalf("实测面必须记录探测被阻断：%+v", d)
	}
}

// TestK8sRealClusterEgressStacking 验收标准 2：同命名空间内先放一条允许出站的
// 策略 ⇒ 禁网 Job 必须被拒绝创建（叠加语义被处理掉），而不是带病执行。
func TestK8sRealClusterEgressStacking(t *testing.T) {
	env := k8sE2EEnv(t)
	k := NewK8sJob(
		WithK8sEndpoint(env[envK8sE2EEndpoint]),
		WithK8sNamespace(env[envK8sE2ENamespace]),
		WithK8sExecutionNamespace(env[envK8sE2ENamespace]),
		WithK8sTokenFile(env[envK8sE2ETokenFile]),
		WithK8sCAFile(env[envK8sE2ECAFile]),
		WithK8sCNICapability(k8sE2ECapability{}),
		WithK8sEgressProbe(K8sEgressProbe{
			Image:   env[envK8sE2EProbeImage],
			Command: []string{"/bin/sh", "-ec", "exit 0"},
		}),
		WithK8sPollInterval(2*time.Second),
	)
	// 同一后端的传输面允许用例自建/自删叠加策略（真集群，不引第三方依赖）。
	cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	allow := `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy",` +
		`"metadata":{"name":"e2e-allow-all-stacking"},` +
		`"spec":{"podSelector":{},"policyTypes":["Egress"],"egress":[{}]}}`
	if err := k8sE2ECreateRawPolicy(cctx, k, env[envK8sE2ENamespace], allow); err != nil {
		t.Fatalf("准备叠加策略失败（检查 RBAC：create/get/delete networkpolicies）：%v", err)
	}
	defer k8sE2EDeleteRawPolicy(cctx, k, env[envK8sE2ENamespace], "e2e-allow-all-stacking")

	_, err := k.Exec(cctx, backend.Job{Image: env[envK8sE2EJobImage]})
	if !kerrors.Is(err, ErrNetworkPolicy) {
		t.Fatalf("存在允许出站的叠加策略时必须拒绝创建禁网作业：%v", err)
	}
}

// k8sE2ECapability 真集群用例的能力事实：跑在已确认实现 NetworkPolicy 的
// 集群上（e2e 集群的选型前提），事实由装配方显式给出。
type k8sE2ECapability struct{}

func (k8sE2ECapability) NetworkPolicyEnforced() (bool, error) { return true, nil }

// k8sE2ECreateRawPolicy / k8sE2EDeleteRawPolicy 用例自管叠加策略（走本后端
// 的同一条传输管道；同包测试可及，不引第三方依赖）。
func k8sE2ECreateRawPolicy(ctx context.Context, k *K8sJob, ns, payload string) error {
	resp, err := k.call(ctx, http.MethodPost,
		"/apis/networking.k8s.io/v1/namespaces/"+ns+"/networkpolicies",
		json.RawMessage(payload), nil)
	if err != nil {
		return err
	}
	return k8sAPIError("创建叠加策略", resp)
}

func k8sE2EDeleteRawPolicy(ctx context.Context, k *K8sJob, ns, name string) {
	_, _ = k.call(ctx, http.MethodDelete,
		"/apis/networking.k8s.io/v1/namespaces/"+ns+"/networkpolicies/"+name, nil, nil)
}

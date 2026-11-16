// k8sjob.go —— Kubernetes Job 执行后端（issue #192）：契约要求的四件在
// batch/v1 上兑现，**零第三方依赖**。
//
// # 零依赖的接法
//
// 与 docker.go 走 Docker Engine HTTP API 是同一手法：stdlib net/http 直连
// kube-apiserver。in-cluster 时从 service account 目录读 token / CA /
// namespace（token 每请求重读，它会轮换），也可以显式装配端点与凭据。
// **不引 client-go**：它的依赖图追着 K8s 版本跑，还会把宿主的集群凭据形态
// 带进本仓依赖面。
//
// # 四件兑现
//
//   - Namespace / ServiceAccount 可配（WithK8sNamespace / WithK8sServiceAccount）；
//     SA 只做名字注入，最小权限 Role/RoleBinding 由宿主在集群侧准备。
//     禁网任务可另落独立执行命名空间（WithK8sExecutionNamespace）。
//   - Egress=denied 以「逐作业 NetworkPolicy + 严格策略装配 + 实际出站探测」
//     兑现，声明面与验证面分开（issue #262，机制详见 k8sjobegress.go）：
//     Egress() 只在策略装配 + CNI 能力事实 + 探测装配三者齐备时自报禁网档；
//     策略建不出来 / 策略面对账不了 / 叠加语义可能放行 ⇒ 拒绝创建作业
//     （ErrNetworkPolicy，fail-closed）——兑现不了的禁网不许静默降级。
//   - TTL：spec.ttlSecondsAfterFinished（缺省 300s）在进程崩溃/对账中断时由
//     控制面兜底清理；正常路径 Exec 结束还会显式删 Job（与 Docker remove 对齐）。
//   - 状态对账：轮询 Job 的 conditions / succeeded / failed / active，映射
//     backend.Result；Job 消失、Pod 被强删（active>0 但无 Pod）、Pod phase
//     Unknown、有 Job 无 Pod 超过宽限，一律给「未知」失败结论，不谎报成功。
//
// # 宿主装配期的声明校验（guardx ExecPolicy）
//
// guardx v0.27.0 起导出了 sandbox.ExecPolicy / Judge / Spec；宿主应在装配期
// 用它校验 Job 声明（镜像 / 挂载 / 环境 / 出站要求与作业档位是否一致），再把
// 满足者登记进 backend.Registry。本后端的 MountPolicy 只是「挂载白名单」一条
// 在参考实现里的落地，不冒充完整 ExecPolicy。
//
// # Pod 安全约束
//
// runAsNonRoot、readOnlyRootFilesystem、allowPrivilegeEscalation=false、
// capabilities.drop=[ALL]、seccompProfile=RuntimeDefault、
// automountServiceAccountToken=false。镜像必须以非 root 运行且不写 rootfs；
// 不满足的镜像由 kubelet 拒绝启动（失败而不是降级）。出站探测 init 容器与
// 业务容器同等受约束。
//
// 日志走 Pod log 端点，stdout/stderr 是**合并流**（K8s API 不拆），全部放进
// Result.Stdout；Detail 标注 log_stream=combined。
package containerbackend

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/oekit/concurrency/async"
	"github.com/oekit/pkg/errors"

	"github.com/oagentkit/guardx/sandbox"
	"github.com/yi-nology/goagent-kernel/backend"
)

var (
	// ErrAPICall kube-apiserver 调用失败（创建 / 轮询 / 读日志 / 清理任一步）。
	// 作业没跑起来属基础设施故障，修复对象是集群侧（凭据 / RBAC / 网络）。
	ErrAPICall = errors.New("containerbackend: kube-apiserver 调用失败")

	// ErrNetworkPolicy NetworkPolicy 未能建立或策略面叠加工况会放行出站——
	// Egress=denied 无法兑现，拒绝创建作业（fail-closed）。修集群能力
	// （CNI / RBAC / 执行命名空间的策略卫生），或显式关闭策略并接受 Egress
	// 自报降级；不得跳过策略继续执行。
	ErrNetworkPolicy = errors.New("containerbackend: NetworkPolicy 未能建立，Egress=denied 无法兑现（拒绝创建作业）")
)

// K8sJob Kubernetes Job 参考实现。
//
// 自报四条（判据按此判，实现按此兑现）：
//
//   - Isolation() = IsolationContainer：Job/Pod 落 namespace + cgroup 边界；
//   - Egress() = EgressDenied（默认）：每个作业先建 Egress 全拒策略；
//     显式关闭策略时如实降为 EgressUnrestricted；
//   - Capable：内存经 Pod resources.limits 兜住；输出截断与逐调用计数
//     兜不住（如实 false）；
//   - 墙钟经 activeDeadlineSeconds（服务端强制）+ context 截止（客户端收场）
//     两道兑现，到点删 Job 并返回 Result{OK:false}。
type K8sJob struct {
	id string

	endpoint       string
	namespace      string
	execNamespace  string
	serviceAccount string
	tokenFile      string
	tokenExplicit  bool
	caFile         string
	caExplicit     bool
	pollInterval   time.Duration
	orphanGrace    time.Duration
	ttlSeconds     int32
	networkPolicy  bool
	probe          K8sEgressProbe
	cniCap         K8sCNICapability
	cniOnce        sync.Once
	cniOK          bool
	cniErr         error
	mounts         MountPolicy

	client *http.Client
	cfgErr error
}

// ID 后端标识。
func (k *K8sJob) ID() string { return k.id }

// Isolation 容器档（Job/Pod 由 kubelet 落 namespace + cgroup）。
func (k *K8sJob) Isolation() backend.Isolation { return backend.IsolationContainer }

// Egress 声明面（issue #262）：禁网档只在「策略装配开启 + CNI 能力事实为真 +
// 出站探测已装配」三者齐备时自报；缺任何一半降级为 EgressUnrestricted
// （fail-closed）——没有实测面的禁网声明，正是本后端不许再犯的洞。
func (k *K8sJob) Egress() sandbox.EgressMode {
	if !k.networkPolicy {
		return sandbox.EgressUnrestricted
	}
	if !k.k8sEgressProbeRequired() {
		return sandbox.EgressUnrestricted // 没有实测面，不许声明禁网
	}
	ok, err := k.cniNetworkPolicyEnforced()
	if err != nil || !ok {
		return sandbox.EgressUnrestricted // 答不上来 / CNI 不实现 ⇒ 按兜不住处理
	}
	return sandbox.EgressDenied
}

// Capable 内存经 limits 兜住；输出截断与逐调用计数兜不住。
func (k *K8sJob) Capable(b sandbox.Budget) bool {
	return b.MaxMemory > 0 && b.MaxBytes == 0 && b.MaxCalls == 0
}

// execNS 本作业的执行命名空间：禁网路径（策略装配开启）优先落独立执行
// 命名空间，未装配时退回作业命名空间。
func (k *K8sJob) execNS() string {
	if k.networkPolicy && k.execNamespace != "" {
		return k.execNamespace
	}
	return k.namespace
}

// Exec 执行一次作业：策略面对账 → NetworkPolicy → Job → 轮询 → 取日志/退出码
// → 清理。
//
// 步骤契约：
//
//   - 挂载先过 MountPolicy（缺省拒绝一切），不满足即创建阶段拒绝（ErrMountDenied）；
//   - 禁网路径先对账执行命名空间的策略面（叠加语义 ⇒ ErrNetworkPolicy），
//     Egress 策略建不出来 ⇒ ErrNetworkPolicy，Job 根本不创建（fail-closed）；
//   - 禁网作业携带出站探测 init 容器：探测未通过（连上外部或不可判）⇒
//     作业被拒；实测结果进 Detail["egress_verified"]；
//   - Budget.MaxMemory ⇒ Pod limits.memory；Budget.WallClock ⇒
//     activeDeadlineSeconds + 截止 watcher（到点删 Job，返回作业失败，不是引擎故障）；
//   - 终态以 Job conditions 为准（Complete / Failed）；对账给不出结论时
//     Result{OK:false, Detail.state=unknown}，绝不猜成功；
//   - 清理失败不掩盖主结果（TTL 是兜底）。
func (k *K8sJob) Exec(ctx context.Context, j backend.Job) (backend.Result, error) {
	if err := j.Validate(); err != nil {
		return backend.Result{}, errors.New("containerbackend: " + err.Error())
	}
	if k.cfgErr != nil {
		return backend.Result{}, k.cfgErr
	}
	if k.client == nil || k.endpoint == "" {
		return backend.Result{}, errors.New("containerbackend: K8sJob 未装配（用 NewK8sJob 构造）")
	}
	if k.ttlSeconds < 0 {
		return backend.Result{}, errors.New("containerbackend: k8s TTL 不得为负（装配期声明错误）")
	}
	if err := k.checkMounts(j); err != nil {
		return backend.Result{}, err
	}

	ns := k.execNS()
	name := newK8sJobName()
	if k.networkPolicy {
		// 严格策略装配：先对账策略面，叠加语义可能放行出站即拒绝（先于任何创建）。
		if err := k8sCheckForeignPolicies(ctx, k, ns, name); err != nil {
			return backend.Result{}, err
		}
		if err := k.createNetworkPolicy(ctx, ns, name); err != nil {
			return backend.Result{}, errors.Wrap(ErrNetworkPolicy, err.Error())
		}
		defer k.cleanup(ctx, ns, k.deleteNetworkPolicy, name)
	}
	if err := k.createJob(ctx, ns, name, j); err != nil {
		return backend.Result{}, err
	}
	defer k.cleanup(ctx, ns, k.deleteJob, name)

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if j.Budget.WallClock > 0 {
		watcherDone := make(chan struct{})
		wall := j.Budget.WallClock
		// Job 状态轮询是同步循环，墙钟到点由 watcher 删 Job 并收场
		// （走 GoSafe：本仓禁止裸 goroutine）。
		async.GoSafe(func() {
			defer close(watcherDone)
			t := time.NewTimer(wall)
			defer t.Stop()
			select {
			case <-t.C:
				k.cleanup(ctx, ns, k.deleteJob, name)
				cancel(errWallClock)
			case <-runCtx.Done():
			}
		})
		defer func() { cancel(errWallClock); <-watcherDone }() // 收口：确保 watcher 停下
	}

	res, err := k.await(runCtx, ns, name)
	if err != nil {
		if errors.Is(context.Cause(runCtx), errWallClock) {
			return k8sAttachEgressFacts(k, backend.Result{
				OK:     false,
				Stderr: errWallClock.Error(),
				Detail: map[string]any{"budget_exceeded": "wall_clock"},
			}, k.verifiedOrNone()), nil
		}
		return backend.Result{}, err
	}
	// 服务端 activeDeadlineSeconds 先于 watcher 生效时，同样归入墙钟预算。
	if !res.OK && j.Budget.WallClock > 0 {
		if d, ok := res.Detail.(map[string]any); ok && d["reason"] == "DeadlineExceeded" {
			d["budget_exceeded"] = "wall_clock"
			res.Stderr = errWallClock.Error()
		}
	}
	return k8sAttachEgressFacts(k, res, k.verifiedOrNone()), nil
}

// verifiedOrNone 实测面的兜底值：await 未能从 Pod 状态读出探测事实时（Job 消失、
// Pod 被强删等 unknown 路径），按探测是否装配记 unseen / none，不猜。
func (k *K8sJob) verifiedOrNone() string {
	if k.k8sEgressProbeRequired() {
		return "unseen"
	}
	return "none"
}

// checkMounts 挂载判据闸：任何一条不满足即在创建阶段拒绝（先于 apiserver 调用）。
func (k *K8sJob) checkMounts(j backend.Job) error {
	for _, m := range j.Mounts {
		if err := k.mounts.check(m); err != nil {
			return errors.Wrap(ErrMountDenied, err.Error())
		}
	}
	return nil
}

// cleanup 用脱离调用方取消的短超时执行清理（清理失败不掩盖主结果）。
func (k *K8sJob) cleanup(ctx context.Context, ns string, del func(context.Context, string, string) error, name string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = del(cctx, ns, name)
}

// k8sWaitError 把等待被取消转成基础设施错误（墙钟路径由 Exec 先行分流）。
func k8sWaitError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if cause == nil {
		cause = ctx.Err()
	}
	if cause == nil {
		cause = errors.New("context canceled")
	}
	return errors.New("containerbackend: k8s 作业等待被取消: " + cause.Error())
}

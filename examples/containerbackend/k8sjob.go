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
//   - Egress=denied 以**逐作业 NetworkPolicy** 兑现：policyTypes: [Egress] +
//     egress: [] ⇒ 该 Job 的 Pod 默认拒绝全部出站。策略建不出来就拒绝创建
//     Job（ErrNetworkPolicy，fail-closed）——兑现不了的禁网不许静默降级。
//   - TTL：spec.ttlSecondsAfterFinished（缺省 300s）在进程崩溃/对账中断时由
//     控制面兜底清理；正常路径 Exec 结束还会显式删 Job（与 Docker remove 对齐）。
//   - 状态对账：轮询 Job 的 conditions / succeeded / failed / active，映射
//     backend.Result；Job 消失、Pod 被强删（active>0 但无 Pod）、Pod phase
//     Unknown、有 Job 无 Pod 超过宽限，一律给「未知」失败结论，不谎报成功。
//
// # Egress 的边界（如实声明）
//
// NetworkPolicy 的**强制**依赖实现它的 CNI；apiserver 接受策略不等于数据面
// 生效。本后端保证的是「策略提交失败 / RBAC 不允许就绝不创建作业」。集群
// CNI 不实现 NetworkPolicy 时，宿主必须显式 WithK8sNetworkPolicy(false)：
// 此时 Egress() 自报 EgressUnrestricted，要求禁网的 Spec 不会选中它。
//
// # 宿主装配期的声明校验（guardx ExecPolicy）
//
// 本仓 go.mod 钉的 oagentkit/guardx v0.25.1 尚未导出 sandbox.ExecPolicy /
// Judge / Spec（v0.27.0 才提供），故本后端**不为此升级依赖**、也不私造第二套
// 判据。宿主应在装配期先用 guardx 的 sandbox.ExecPolicy 校验 Job 声明
// （镜像 / 挂载 / 环境 / 出站要求与作业档位是否一致），再把满足者登记进
// backend.Registry。本后端的 MountPolicy 只是「挂载白名单」一条在参考实现里
// 的落地，不冒充完整 ExecPolicy。
//
// # Pod 安全约束
//
// runAsNonRoot、readOnlyRootFilesystem、allowPrivilegeEscalation=false、
// capabilities.drop=[ALL]、seccompProfile=RuntimeDefault、
// automountServiceAccountToken=false。镜像必须以非 root 运行且不写 rootfs；
// 不满足的镜像由 kubelet 拒绝启动（失败而不是降级）。
//
// 日志走 Pod log 端点，stdout/stderr 是**合并流**（K8s API 不拆），全部放进
// Result.Stdout；Detail 标注 log_stream=combined。
package containerbackend

import (
	"context"
	"net/http"
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

	// ErrNetworkPolicy NetworkPolicy 未能建立——Egress=denied 无法兑现，拒绝
	// 创建作业（fail-closed）。修集群能力（CNI / RBAC），或显式关闭策略并
	// 接受 Egress 自报降级；不得跳过策略继续执行。
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
	serviceAccount string
	tokenFile      string
	tokenExplicit  bool
	caFile         string
	caExplicit     bool
	pollInterval   time.Duration
	orphanGrace    time.Duration
	ttlSeconds     int32
	networkPolicy  bool
	mounts         MountPolicy

	client *http.Client
	cfgErr error
}

// ID 后端标识。
func (k *K8sJob) ID() string { return k.id }

// Isolation 容器档（Job/Pod 由 kubelet 落 namespace + cgroup）。
func (k *K8sJob) Isolation() backend.Isolation { return backend.IsolationContainer }

// Egress 默认逐作业 NetworkPolicy 全拒出站；策略被显式关闭时如实降级。
func (k *K8sJob) Egress() sandbox.EgressMode {
	if k.networkPolicy {
		return sandbox.EgressDenied
	}
	return sandbox.EgressUnrestricted
}

// Capable 内存经 limits 兜住；输出截断与逐调用计数兜不住。
func (k *K8sJob) Capable(b sandbox.Budget) bool {
	return b.MaxMemory > 0 && b.MaxBytes == 0 && b.MaxCalls == 0
}

// Exec 执行一次作业：NetworkPolicy → Job → 轮询 → 取日志/退出码 → 清理。
//
// 步骤契约：
//
//   - 挂载先过 MountPolicy（缺省拒绝一切），不满足即创建阶段拒绝（ErrMountDenied）；
//   - Egress 策略建不出来 ⇒ ErrNetworkPolicy，Job 根本不创建（fail-closed）；
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

	name := newK8sJobName()
	if k.networkPolicy {
		if err := k.createNetworkPolicy(ctx, name); err != nil {
			return backend.Result{}, errors.Wrap(ErrNetworkPolicy, err.Error())
		}
		defer k.cleanup(ctx, k.deleteNetworkPolicy, name)
	}
	if err := k.createJob(ctx, name, j); err != nil {
		return backend.Result{}, err
	}
	defer k.cleanup(ctx, k.deleteJob, name)

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
				k.cleanup(ctx, k.deleteJob, name)
				cancel(errWallClock)
			case <-runCtx.Done():
			}
		})
		defer func() { cancel(errWallClock); <-watcherDone }() // 收口：确保 watcher 停下
	}

	res, err := k.await(runCtx, name)
	if err != nil {
		if errors.Is(context.Cause(runCtx), errWallClock) {
			return backend.Result{
				OK:     false,
				Stderr: errWallClock.Error(),
				Detail: map[string]any{"budget_exceeded": "wall_clock"},
			}, nil
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
	return res, nil
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

// await 轮询 Job 直到终态或对账给不出结论。
func (k *K8sJob) await(ctx context.Context, name string) (backend.Result, error) {
	var pendingSince time.Time
	for {
		job, found, err := k.getJob(ctx, name)
		if err != nil {
			return backend.Result{}, err
		}
		if !found {
			return k.unknown(name, "job_not_found"), nil
		}
		if job.Metadata.DeletionTimestamp != nil {
			return k.unknown(name, "job_deleting"), nil
		}
		if done, res, reason := classifyK8sJob(job.Status); done {
			return k.collect(ctx, name, res, reason), nil
		}
		pods, err := k.listPods(ctx, name)
		if err != nil {
			return backend.Result{}, err
		}
		switch {
		case job.Status.Active > 0 && len(pods) == 0:
			// Job 认为有活着的 Pod、集群里却一个都没有：Pod 被强删/节点失联。
			return k.unknown(name, "active_without_pods"), nil
		case anyPodPhaseUnknown(pods):
			return k.unknown(name, "pod_phase_unknown"), nil
		case len(pods) == 0 && job.Status.Succeeded == 0 && job.Status.Failed == 0:
			if pendingSince.IsZero() {
				pendingSince = time.Now()
			} else if time.Since(pendingSince) >= k.orphanGrace {
				return k.unknown(name, "no_pods"), nil
			}
		default:
			pendingSince = time.Time{}
		}
		t := time.NewTimer(k.pollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return backend.Result{}, k8sWaitError(ctx)
		case <-t.C:
		}
	}
}

// classifyK8sJob 把 Job 状态映射成终态结论（false = 还没到终态）。
//
// 判定只认 conditions 与计数：Complete=True / succeeded>0 ⇒ 成功；
// Failed=True 或 failed>0 且无 active ⇒ 失败。条件缺失不是成功。
func classifyK8sJob(st k8sJobStatus) (bool, backend.Result, string) {
	for _, c := range st.Conditions {
		if c.Status != "True" {
			continue
		}
		switch c.Type {
		case "Failed":
			reason := c.Reason
			if reason == "" {
				reason = "failed_condition"
			}
			return true, backend.Result{OK: false}, reason
		case "Complete":
			return true, backend.Result{OK: true}, "complete"
		}
	}
	if st.Succeeded > 0 {
		return true, backend.Result{OK: true}, "succeeded"
	}
	if st.Failed > 0 && st.Active == 0 {
		return true, backend.Result{OK: false}, "failed"
	}
	return false, backend.Result{}, ""
}

// collect 终态后取 Pod 退出码与日志（读取失败只进明细，不改写终态）。
func (k *K8sJob) collect(ctx context.Context, name string, res backend.Result, reason string) backend.Result {
	detail := map[string]any{
		"job": name, "namespace": k.namespace, "reason": reason, "log_stream": "combined",
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	pods, err := k.listPods(cctx, name)
	if err != nil {
		detail["pods_error"] = err.Error()
		res.Detail = detail
		return res
	}
	pod := pickK8sPod(pods)
	if pod == nil {
		if !res.OK {
			res.ExitCode = 1 // 失败结论已定，但没有 Pod 退出码：给非零值，不伪装成「没跑」
		}
		res.Detail = detail
		return res
	}
	detail["pod"] = pod.Metadata.Name
	if code, ok := terminatedExitCode(*pod); ok {
		res.ExitCode = code
	}
	if !res.OK && res.ExitCode == 0 {
		res.ExitCode = 1
	}
	logs, lerr := k.podLogs(cctx, pod.Metadata.Name)
	if lerr != nil {
		detail["log_error"] = lerr.Error()
	} else {
		res.Stdout = logs
	}
	res.Detail = detail
	return res
}

// unknown 对账给不出结论：Result 层失败 + 明确 unknown 明细（不返回 error——
// 「不知道」不是「后端坏了」，但绝不能猜成成功）。
func (k *K8sJob) unknown(name, reason string) backend.Result {
	return backend.Result{OK: false, Detail: map[string]any{
		"job": name, "namespace": k.namespace, "state": "unknown", "reason": reason,
	}}
}

// cleanup 用脱离调用方取消的短超时执行清理（清理失败不掩盖主结果）。
func (k *K8sJob) cleanup(ctx context.Context, del func(context.Context, string) error, name string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = del(cctx, name)
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

// pickK8sPod 优先取有终止状态的 Pod（失败作业可能留下多个 Pod）。
func pickK8sPod(pods []k8sPod) *k8sPod {
	for i := range pods {
		if _, ok := terminatedExitCode(pods[i]); ok {
			return &pods[i]
		}
	}
	if len(pods) > 0 {
		return &pods[0]
	}
	return nil
}

// terminatedExitCode 取容器终止退出码。
func terminatedExitCode(p k8sPod) (int, bool) {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			return cs.State.Terminated.ExitCode, true
		}
	}
	return 0, false
}

// anyPodPhaseUnknown 节点失联的 Pod 会被标记为 Unknown——此时给结论是猜测，
// 一律按未知失败收场。
func anyPodPhaseUnknown(pods []k8sPod) bool {
	for _, p := range pods {
		if p.Status.Phase == "Unknown" {
			return true
		}
	}
	return false
}

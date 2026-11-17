// Package faultmatrix 跨部署模式的**故障注入矩阵**（issue #193，issue #274 扩列）。
//
// # 为什么是一张可执行的表
//
// 分布式正确性此前靠零散用例与文档声明，没有「哪种故障在哪种部署模式必须被
// 验证」的单一清单，也没有接进门禁。散文清单会漂移；这里把矩阵写成
// `deploymentMatrix` + `requiredMatrixCells`：前者声明故障类、适用模式、
// 执行函数与真环境依赖，后者登记**不可删除的格与最小用例数**。
// TestFaultDeploymentMatrix 先自检（格数不足/漏类/形状坏即红），再逐格执行。
// 漏一类 = 门禁红；删一条新用例 = 计数不足 = 门禁红。
//
//	故障类                         Embedded  Standalone  Cluster
//	并发认领唯一性                    ✓          ✓          ✓
//	写库成功但事件发布失败             —          ✓          ✓
//	数据库故障及恢复                  —          ✓          ✓
//	多 Worker 时钟偏移                —         ✓(可选)      ✓
//	Worker 网络分区与租约接管          —          —          ✓
//	Pod 强制删除与副作用对账           —          —          ✓
//	K8s API 响应丢失                  —          —        ✓(+真集群)
//	跨租户任务碰撞                    —          ✓          ✓
//	长任务租约失效                    —          —          ✓
//	数据库主备切换                    —        ✓(+真 PG)     —
//	事件乱序提交                      —          ✓          ✓
//	配额悬挂                         —          —          ✓
//	控制面并发介入                    —          —          ✓
//	真实 NetworkPolicy 验证           —          —        ✓(+真集群)
//
// # 落在哪一层跑
//
// 全部用例走 `make fault-injection`（`go test ./... -run '^TestFault'`）。
// 用例用进程内后端/可注入时钟/可注入故障模拟**时序与判定**：矩阵验证的是
// 内核契约（认领原子性判据、落库先于发布、存储故障不伪装成资格丢失、
// 时间权威归存储、租约接管、副作用对账、租户键作用域、水位线无空洞、
// 配额回收判据、控制面权威唯一），不是某个存储驱动的跨进程原子性。
// 后者由 `taskboard/boardtest`、`sidetest` 契约套件与 `examples/pgstore`
// 的 PG 集成面覆盖（`make integration`）——两层各判一段，不互相冒充。
//
// # 需要真环境时不许静默跳过
//
// matrixCase.requiresEnv 非空时用例显式 Skipf 并打印所需环境与触发条件。
// 这类格**必须同时给 localFact**：Skip 之前先跑一条进程内可判事实（声明面
// 门禁、同标识对账、主备切换语义等），否则「跳过」会把整格伪装成绿的
// （自检对此红）。真环境执行入口由宿主/CI 按 requiresEnv 文案提供。
package faultmatrix

import (
	"fmt"
	"testing"
)

// deploymentMode 部署模式（与 issue #193 矩阵的三列同名）。
type deploymentMode string

const (
	// ModeEmbedded 进程内嵌（单进程、无外部存储）。
	ModeEmbedded deploymentMode = "embedded"
	// ModeStandalone 单副本 + 外部存储（一个 Worker 进程，DB/协调存储独立）。
	ModeStandalone deploymentMode = "standalone"
	// ModeCluster 多副本（Worker 之间共享存储与租约，存在接管/分区/强删）。
	ModeCluster deploymentMode = "cluster"
)

// faultClass 故障类（矩阵行）。
type faultClass string

const (
	// ClassConcurrentClaim 并发认领唯一性：同一任务同时被多执行者认领。
	ClassConcurrentClaim faultClass = "concurrent_claim_uniqueness"
	// ClassDurableWritePublishFail 写库成功但实时事件发布失败。
	ClassDurableWritePublishFail faultClass = "durable_write_publish_failure"
	// ClassDBOutageRecovery 数据库故障及恢复。
	ClassDBOutageRecovery faultClass = "db_outage_recovery"
	// ClassClockSkew 多 Worker 节点时钟偏移。
	ClassClockSkew faultClass = "clock_skew"
	// ClassNetworkPartition Worker 网络分区与租约接管。
	ClassNetworkPartition faultClass = "network_partition_lease_takeover"
	// ClassPodForceDelete Pod 强制删除与副作用对账。
	ClassPodForceDelete faultClass = "pod_force_delete_side_effect_reconcile"

	// ── issue #274：外部评审点名的 8 类真实分布式场景 ──
	//
	// ClassK8sAPIResponseLoss K8s API 响应丢失（Job 已建、Worker 超时）。
	ClassK8sAPIResponseLoss faultClass = "k8s_api_response_loss"
	// ClassCrossTenantCollision 跨租户任务碰撞（同 TaskID 不同租户）。
	ClassCrossTenantCollision faultClass = "cross_tenant_task_collision"
	// ClassLongLeaseExpiry 长任务租约失效（执行超过 TTL 后易主）。
	ClassLongLeaseExpiry faultClass = "long_task_lease_expiry"
	// ClassDBFailover 数据库主备切换（提交状态在备库可见、未提交不半写）。
	ClassDBFailover faultClass = "db_primary_standby_failover"
	// ClassEventReorder 事件乱序提交（先提交大 ID 不推进水位线）。
	ClassEventReorder faultClass = "event_out_of_order_commit"
	// ClassQuotaDangling 配额悬挂（Worker 失联后预占回收，任务租约仍活则拒绝）。
	ClassQuotaDangling faultClass = "quota_dangling_reclaim"
	// ClassControlPlaneRace 控制面并发介入（强释与 Worker 完成只有一次有效转换）。
	ClassControlPlaneRace faultClass = "controlplane_concurrent_intervention"
	// ClassNetworkPolicy 真实 NetworkPolicy 验证（声明面 fail-closed + 数据面实测）。
	ClassNetworkPolicy faultClass = "network_policy_enforcement"
)

// matrixCase 矩阵的一格（一行 × 一个适用模式）。
type matrixCase struct {
	class faultClass
	name  string
	modes []deploymentMode
	// requiresEnv 非空 = 本格只能真环境验证；用例显式 Skip 并打印该环境。
	// 本格必须同时给 localFact（缺环境也至少判一条可判事实，防「跳过即假绿」）。
	requiresEnv string
	// localFact 在 Skip 之前无条件执行的进程内判据：真环境格也必须留下
	// 一条可判事实（声明面/装配期门禁/同标识对账等），不能靠 Skip 冒充已覆盖。
	localFact func(t *testing.T)
	// run 进程内可执行的用例；requiresEnv 非空的格可以为 nil（只剩声明与
	// localFact——真环境执行入口由宿主/CI 提供）。
	run func(t *testing.T)
}

// requiredCell 一个**不可删除**的矩阵格：故障类 × 模式 × 最少用例数。
//
// 自检从 deploymentMatrix 统计实际格数，低于 min 即红。这样「删掉任一新
// 用例」会直接让对应格计数不足——矩阵只准扩，不准删（issue #274）。
type requiredCell struct {
	class faultClass
	mode  deploymentMode
	min   int
}

// requiredMatrixCells 门禁的**必有余量**清单（既有六类 + 新增八类）。
//
// 既有六类保留原口径（每类在适用模式至少一条）；新增八类按 issue #274
// 的落位表登记。min=2 的格表示「进程内模拟 + 真环境声明」两条都要在：
// 删掉任何一条都会红。
var requiredMatrixCells = []requiredCell{
	// 既有六类（#193 的矩阵不得缩水）。
	{ClassConcurrentClaim, ModeEmbedded, 1},
	{ClassConcurrentClaim, ModeStandalone, 1},
	{ClassConcurrentClaim, ModeCluster, 1},
	{ClassDurableWritePublishFail, ModeStandalone, 1},
	{ClassDurableWritePublishFail, ModeCluster, 1},
	{ClassDBOutageRecovery, ModeStandalone, 1},
	{ClassDBOutageRecovery, ModeCluster, 1},
	{ClassClockSkew, ModeStandalone, 1},
	{ClassClockSkew, ModeCluster, 1},
	{ClassNetworkPartition, ModeCluster, 1},
	{ClassPodForceDelete, ModeCluster, 1},

	// issue #274 新增八类。
	{ClassK8sAPIResponseLoss, ModeCluster, 2}, // 进程内幂等 + 真集群
	{ClassCrossTenantCollision, ModeStandalone, 1},
	{ClassCrossTenantCollision, ModeCluster, 1},
	{ClassLongLeaseExpiry, ModeCluster, 1},
	{ClassDBFailover, ModeStandalone, 2}, // 进程内主备切换 + 真 PG
	{ClassEventReorder, ModeStandalone, 1},
	{ClassEventReorder, ModeCluster, 1},
	{ClassQuotaDangling, ModeCluster, 1},
	{ClassControlPlaneRace, ModeCluster, 1},
	{ClassNetworkPolicy, ModeCluster, 2}, // 声明面 fail-closed + 真集群实测
}

// deploymentMatrix 矩阵的**唯一声明面**（执行前自检完整性）。
var deploymentMatrix = []matrixCase{
	{
		class: ClassConcurrentClaim,
		name:  "concurrent-claim-uniqueness",
		modes: []deploymentMode{ModeEmbedded, ModeStandalone, ModeCluster},
		run:   runConcurrentClaimUniqueness,
	},
	{
		class: ClassDurableWritePublishFail,
		name:  "durable-write-then-publish-failure",
		modes: []deploymentMode{ModeStandalone, ModeCluster},
		run:   runDurableWritePublishFailure,
	},
	{
		class: ClassDBOutageRecovery,
		name:  "db-outage-recovery",
		modes: []deploymentMode{ModeStandalone, ModeCluster},
		run:   runDBOutageRecovery,
	},
	{
		class: ClassClockSkew,
		name:  "clock-skew-storage-authority",
		modes: []deploymentMode{ModeStandalone, ModeCluster},
		run:   runClockSkew,
	},
	{
		class: ClassNetworkPartition,
		name:  "network-partition-lease-takeover",
		modes: []deploymentMode{ModeCluster},
		run:   runNetworkPartitionLeaseTakeover,
	},
	{
		class: ClassPodForceDelete,
		name:  "pod-force-delete-side-effect-reconcile",
		modes: []deploymentMode{ModeCluster},
		run:   runPodForceDeleteReconcile,
	},

	// ── issue #274：8 类真实分布式场景（扩行扩列，既有格一条不删）──
	{
		class: ClassK8sAPIResponseLoss,
		name:  "k8s-api-response-loss-idempotent-execution",
		modes: []deploymentMode{ModeCluster},
		run:   runK8sAPIResponseLossIdempotent,
	},
	{
		class: ClassK8sAPIResponseLoss,
		name:  "k8s-api-response-loss-real-cluster",
		modes: []deploymentMode{ModeCluster},
		requiresEnv: "Kubernetes 1.29+ 集群（可创建 Job/Pod/NetworkPolicy 的 kubeconfig）" +
			"+ 可在 apiserver 与 kubelet 之间注入响应丢失/超时（toxiproxy 或等价故障注入）" +
			"+ 专用执行命名空间；触发条件：CI 提供上述环境后由宿主测试入口执行本格",
		localFact: runK8sAPIResponseLossIdempotent,
	},
	{
		class: ClassCrossTenantCollision,
		name:  "cross-tenant-task-collision",
		modes: []deploymentMode{ModeStandalone, ModeCluster},
		run:   runCrossTenantTaskCollision,
	},
	{
		class: ClassLongLeaseExpiry,
		name:  "long-task-lease-expiry-takeover",
		modes: []deploymentMode{ModeCluster},
		run:   runLongTaskLeaseExpiry,
	},
	{
		class: ClassDBFailover,
		name:  "db-primary-standby-failover",
		modes: []deploymentMode{ModeStandalone},
		run:   runDBFailover,
	},
	{
		class: ClassDBFailover,
		name:  "db-primary-standby-failover-real-pg",
		modes: []deploymentMode{ModeStandalone},
		requiresEnv: "PostgreSQL 主备（流复制，同步或准同步）+ 可触发 promote/restart 的编排面" +
			"+ PGREF_DSN_PRIMARY / PGREF_DSN_STANDBY 指向两端；触发条件：CI 的 PG HA 环境就绪后执行本格",
		localFact: runDBFailover,
	},
	{
		class: ClassEventReorder,
		name:  "event-out-of-order-commit",
		modes: []deploymentMode{ModeStandalone, ModeCluster},
		run:   runEventOutOfOrderCommit,
	},
	{
		class: ClassQuotaDangling,
		name:  "quota-dangling-lease-reclaim",
		modes: []deploymentMode{ModeCluster},
		run:   runQuotaDanglingReclaim,
	},
	{
		class: ClassControlPlaneRace,
		name:  "controlplane-concurrent-intervention",
		modes: []deploymentMode{ModeCluster},
		run:   runControlPlaneConcurrentIntervention,
	},
	{
		class: ClassNetworkPolicy,
		name:  "network-policy-declaration-fail-closed",
		modes: []deploymentMode{ModeCluster},
		run:   runNetworkPolicyDeclaration,
	},
	{
		class: ClassNetworkPolicy,
		name:  "network-policy-real-dataplane",
		modes: []deploymentMode{ModeCluster},
		requiresEnv: "Kubernetes 1.29+ 集群 + 实现 NetworkPolicy 数据面的 CNI（Calico/Cilium 等）" +
			"+ 允许创建执行命名空间、NetworkPolicy 与探测镜像；触发条件：真集群就绪后用实际出站探测断言禁网",
		localFact: runNetworkPolicyDeclaration,
	},
}

// TestFaultDeploymentMatrix 故障矩阵门禁：先判矩阵自身完整，再逐格执行。
//
// 自检（checkMatrix）任一不满足即红，不进入执行：
//   - 每个登记故障类在每种登记模式下的用例数 ≥ requiredMatrixCells 的下限；
//   - 每格至少声明一个合法模式、名字非空、执行函数非空（真环境格可只留
//     localFact）；
//   - requiresEnv 非空时**必须**同时给 localFact——缺环境也要判一条进程内
//     事实，不许用 Skip 冒充已覆盖；
//   - 矩阵里出现未在 requiredMatrixCells 登记的故障类 ⇒ 红（扩行必须显式
//     登记，防「新增一类但没人记得补格」）。
func TestFaultDeploymentMatrix(t *testing.T) {
	if err := checkMatrix(deploymentMatrix, requiredMatrixCells); err != nil {
		t.Fatal(err)
	}
	for _, c := range deploymentMatrix {
		c := c
		for _, mode := range c.modes {
			mode := mode
			t.Run(c.name+"/"+string(mode), func(t *testing.T) {
				// 真环境格的「可判事实」先跑：Skip 不代表本格什么都没验。
				if c.localFact != nil {
					c.localFact(t)
				}
				if c.requiresEnv != "" {
					t.Skipf("本格只能真环境验证，需要环境：%s", c.requiresEnv)
				}
				c.run(t)
			})
		}
	}
}

// cellKey 故障类 × 模式（requiredCell 的统计键）。
type cellKey struct {
	class faultClass
	mode  deploymentMode
}

// checkMatrix 矩阵自检的**唯一实现**（TestFaultDeploymentMatrix 与反向用例
// 共用同一份判定，避免门禁与自检各写一套）。
func checkMatrix(matrix []matrixCase, required []requiredCell) error {
	validMode := func(m deploymentMode) bool {
		return m == ModeEmbedded || m == ModeStandalone || m == ModeCluster
	}
	requiredClasses := map[faultClass]bool{}
	for _, r := range required {
		if !validMode(r.mode) {
			return fmt.Errorf("requiredMatrixCells 登记了未知模式 %q", r.mode)
		}
		if r.min <= 0 {
			return fmt.Errorf("requiredMatrixCells 的 %q/%s 下限必须为正（实得 %d）", r.class, r.mode, r.min)
		}
		requiredClasses[r.class] = true
	}

	names := map[string]bool{}
	cells := map[cellKey]int{}
	classes := map[faultClass]bool{}
	for _, c := range matrix {
		if c.name == "" {
			return fmt.Errorf("故障矩阵用例缺名字：%+v", c)
		}
		if names[c.name] {
			return fmt.Errorf("故障矩阵用例名重复：%s（名字是门禁报错的定位键）", c.name)
		}
		names[c.name] = true
		if len(c.modes) == 0 {
			return fmt.Errorf("故障矩阵用例 %s 未声明适用部署模式", c.name)
		}
		if c.run == nil && c.requiresEnv == "" {
			return fmt.Errorf("故障矩阵用例 %s 缺执行函数（非真环境格必须可执行）", c.name)
		}
		if c.requiresEnv != "" && c.localFact == nil {
			return fmt.Errorf("真环境格 %s 缺 localFact——Skip 之前必须判一条可判事实，否则跳过即假绿", c.name)
		}
		for _, m := range c.modes {
			if !validMode(m) {
				return fmt.Errorf("故障矩阵用例 %s 声明了未知部署模式 %q", c.name, m)
			}
			cells[cellKey{class: c.class, mode: m}]++
		}
		classes[c.class] = true
	}

	for class := range classes {
		if !requiredClasses[class] {
			return fmt.Errorf("故障矩阵出现未登记的故障类 %q——扩行必须先在 requiredMatrixCells 登记", class)
		}
	}
	for _, r := range required {
		if got := cells[cellKey{class: r.class, mode: r.mode}]; got < r.min {
			return fmt.Errorf("故障矩阵缺格：%q/%s 需要至少 %d 条用例，实得 %d——删格/漏格即门禁红",
				r.class, r.mode, r.min, got)
		}
	}
	return nil
}

// newFaultClasses issue #274 新增的 8 类（反向用例的删除集合）。
var newFaultClasses = map[faultClass]bool{
	ClassK8sAPIResponseLoss:   true,
	ClassCrossTenantCollision: true,
	ClassLongLeaseExpiry:      true,
	ClassDBFailover:           true,
	ClassEventReorder:         true,
	ClassQuotaDangling:        true,
	ClassControlPlaneRace:     true,
	ClassNetworkPolicy:        true,
}

// TestMatrixNewCasesNotDeletable 「删掉任一新用例 ⇒ 门禁红」的反向验证：
// 逐个删掉新增类的每一条用例，checkMatrix 都必须报错。
func TestMatrixNewCasesNotDeletable(t *testing.T) {
	removed := 0
	for i, c := range deploymentMatrix {
		if !newFaultClasses[c.class] {
			continue
		}
		reduced := make([]matrixCase, 0, len(deploymentMatrix)-1)
		reduced = append(reduced, deploymentMatrix[:i]...)
		reduced = append(reduced, deploymentMatrix[i+1:]...)
		if err := checkMatrix(reduced, requiredMatrixCells); err == nil {
			t.Fatalf("删除新用例 %q 未被矩阵自检拦截——「矩阵只准扩，不准删」没有执行体", c.name)
		}
		removed++
	}
	if removed == 0 {
		t.Fatal("矩阵里没有任何新增类用例——反向验证没有作用对象")
	}
}

// TestMatrixSelfCheckRejectsBadShape 自检逻辑本身的反向验证：形状坏了
// （缺 localFact / 真环境格缺 run 以外的必需面 / 重复名 / 未知模式 /
// 缺必需格）都必须红。
func TestMatrixSelfCheckRejectsBadShape(t *testing.T) {
	ok := func(*testing.T) {}
	required := []requiredCell{{class: ClassK8sAPIResponseLoss, mode: ModeCluster, min: 1}}
	good := []matrixCase{{
		class: ClassK8sAPIResponseLoss,
		name:  "good",
		modes: []deploymentMode{ModeCluster},
		run:   ok,
	}}
	if err := checkMatrix(good, required); err != nil {
		t.Fatalf("合法矩阵被误判: %v", err)
	}

	cases := []struct {
		name   string
		matrix []matrixCase
	}{
		{"删除必需格", nil},
		{"真环境格缺 localFact", []matrixCase{{
			class: ClassK8sAPIResponseLoss, name: "env-only",
			modes: []deploymentMode{ModeCluster}, requiresEnv: "某真集群",
		}}},
		{"非真环境格缺执行函数", []matrixCase{{
			class: ClassK8sAPIResponseLoss, name: "no-run",
			modes: []deploymentMode{ModeCluster},
		}}},
		{"重复用例名", []matrixCase{
			{class: ClassK8sAPIResponseLoss, name: "dup", modes: []deploymentMode{ModeCluster}, run: ok},
			{class: ClassK8sAPIResponseLoss, name: "dup", modes: []deploymentMode{ModeCluster}, run: ok},
		}},
		{"未知部署模式", []matrixCase{{
			class: ClassK8sAPIResponseLoss, name: "bad-mode",
			modes: []deploymentMode{"nomad"}, run: ok,
		}}},
		{"未登记的故障类", []matrixCase{{
			class: ClassEventReorder, name: "unregistered",
			modes: []deploymentMode{ModeCluster}, run: ok,
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkMatrix(tc.matrix, required); err == nil {
				t.Fatalf("坏矩阵未被自检拦截: %s", tc.name)
			}
		})
	}
}

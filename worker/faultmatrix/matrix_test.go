// Package faultmatrix 跨部署模式的**故障注入矩阵**（issue #193）。
//
// # 为什么是一张可执行的表
//
// 分布式正确性此前靠零散用例与文档声明，没有「哪种故障在哪种部署模式必须被
// 验证」的单一清单，也没有接进门禁。散文清单会漂移；这里把矩阵写成
// `deploymentMatrix`：每条声明故障类、适用部署模式与执行函数，
// TestFaultDeploymentMatrix 先自检「六类故障每类至少一条用例、模式合法、
// 执行函数非空」，再逐格执行。漏一类 = 门禁红；删一条 = 门禁红。
//
//	故障类                         Embedded  Standalone  Cluster
//	并发认领唯一性                    ✓          ✓          ✓
//	写库成功但事件发布失败             —          ✓          ✓
//	数据库故障及恢复                  —          ✓          ✓
//	多 Worker 时钟偏移                —         ✓(可选)      ✓
//	Worker 网络分区与租约接管          —          —          ✓
//	Pod 强制删除与副作用对账           —          —          ✓
//
// # 落在哪一层跑
//
// 全部用例走 `make fault-injection`（`go test ./... -run '^TestFault'`）。
// 用例用进程内后端/可注入时钟/可注入故障模拟**时序与判定**：矩阵验证的是
// 内核契约（认领原子性判据、落库先于发布、存储故障不伪装成资格丢失、
// 时间权威归存储、租约接管、副作用对账），不是某个存储驱动的跨进程原子性。
// 后者由 `taskboard/boardtest`、`sidetest` 契约套件与 `examples/pgstore`
// 的 PG 集成面覆盖（`make integration`）——两层各判一段，不互相冒充。
//
// # 需要真集群时不许静默跳过
//
// matrixCase.requiresEnv 非空时用例会显式 Skipf 并打印所需环境。当前六类
// 都能在进程内复现，故无一格使用它；将来若某格只能在真集群验证，必须填
// 这个字段（跳过是显式声明，不是「观察到但跳过」的软判据）。
package faultmatrix

import (
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
)

// matrixCase 矩阵的一格（一行 × 一个适用模式）。
type matrixCase struct {
	class faultClass
	name  string
	modes []deploymentMode
	// requiresEnv 非空 = 本格只能真集群验证；用例显式 Skip 并打印该环境。
	requiresEnv string
	run         func(t *testing.T)
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
}

// TestFaultDeploymentMatrix 故障矩阵门禁：先判矩阵自身完整，再逐格执行。
//
// 自检三件事（任一不满足即红，不进入执行）：
//   - 六类故障每类至少一条用例；
//   - 每格至少声明一个合法模式、执行函数非空、名字非空；
//   - requiresEnv 只作为显式跳过通道，跳过原因必须带得出去。
func TestFaultDeploymentMatrix(t *testing.T) {
	required := []faultClass{
		ClassConcurrentClaim,
		ClassDurableWritePublishFail,
		ClassDBOutageRecovery,
		ClassClockSkew,
		ClassNetworkPartition,
		ClassPodForceDelete,
	}
	seen := map[faultClass]int{}
	validMode := func(m deploymentMode) bool {
		return m == ModeEmbedded || m == ModeStandalone || m == ModeCluster
	}
	for _, c := range deploymentMatrix {
		if c.name == "" {
			t.Fatalf("故障矩阵用例缺名字：%+v", c)
		}
		if c.run == nil {
			t.Fatalf("故障矩阵用例 %s 缺执行函数", c.name)
		}
		if len(c.modes) == 0 {
			t.Fatalf("故障矩阵用例 %s 未声明适用部署模式", c.name)
		}
		for _, m := range c.modes {
			if !validMode(m) {
				t.Fatalf("故障矩阵用例 %s 声明了未知部署模式 %q", c.name, m)
			}
		}
		seen[c.class]++
	}
	for _, class := range required {
		if seen[class] == 0 {
			t.Fatalf("故障矩阵缺故障类 %q 的用例——新增分布式能力必须补它所在的格子", class)
		}
	}

	for _, c := range deploymentMatrix {
		c := c
		for _, mode := range c.modes {
			mode := mode
			t.Run(c.name+"/"+string(mode), func(t *testing.T) {
				if c.requiresEnv != "" {
					t.Skipf("本格只能真集群验证，需要环境：%s", c.requiresEnv)
				}
				c.run(t)
			})
		}
	}
}

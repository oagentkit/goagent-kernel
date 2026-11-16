package backend

import (
	"github.com/oagentkit/guardx/sandbox"
)

// 本文件是「作业声明 → 执行硬约束判据」的接缝（guardx/sandbox.ExecPolicy）。
//
// # 为什么需要它：Validate 只管自洽，管不了策略
//
// Job.Validate 判的是**自洽**——镜像非空、Env 是 K=V、挂载两端齐、预算非负。
// 这些与策略无关，任何宿主都该过。但它看不见两类真实缺口：
//
//   - **镜像没钉 digest**（app:latest 这类浮动 tag）：同名 tag 重推之后
//     「钉住什么就跑什么」不再成立，作业级可复现性当场失效；
//   - **挂载了控制面 socket**（/var/run/docker.sock 之类）：作业能在里面
//     创建任意特权容器，network=none、只读 rootfs、cap_drop 全部被绕开。
//
// 这两条都不是「声明写错了」，而是「声明落在策略外」——判据在上游
// guardx/sandbox.ExecPolicy（v0.27.0 下沉），本包只负责把 Job 映射成它的
// 声明面 sandbox.Spec。
//
// # 为什么映射归机制面
//
// 字段对应关系一旦各宿主各写一份，同一个 Job 在不同宿主上会判出不同结论：
// 漏映射一个字段，那条约束就在那个宿主上**静默失效**。**策略值**（允许哪些
// digest、能不能挂宿主目录）是宿主的资产；「声明 → Spec」的对应是机制。
// 与 ExecPolicy 自身「本包不 import 任何执行后端，接法在宿主」的分工互补：
// 上游管判据，本仓管本仓声明面的那份映射（上游文档点名的就是这种接法）。

// ExecSpec 把作业请求映射成上游执行硬约束判据的声明面。
//
// 未映射的项（RootfsReadOnly / NonRoot / CapDrop / SeccompProfile）在本包的
// 作业声明里**没有对应字段**：任务式后端的这几项是后端**无条件兑现**的
// （只读 rootfs、非 root、allowPrivilegeEscalation=false 写在各实现里），
// 不是作业声明的自由度。映射里留零值即「未声明」——策略若要求它们，
// 会如实判不过（fail-closed），不会假装满足。
func (j Job) ExecSpec() sandbox.Spec {
	mounts := make([]sandbox.Mount, 0, len(j.Mounts))
	for _, m := range j.Mounts {
		mounts = append(mounts, sandbox.Mount{
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	return sandbox.Spec{
		Image:  j.Image,
		Mounts: mounts,
		Limits: j.Budget, // 同一个 sandbox.Budget 类型：资源词表只有一份
	}
}

// JudgeExec 判作业声明是否落在执行硬约束策略内（违反即返回对应哨兵）。
//
// 与 Validate 的分工是**两层判据**，不要合并：
//
//	Validate   自洽（与策略无关，任何宿主都该过）
//	JudgeExec  落在策略内（策略是宿主的资产，必须显式传入）
//
// 零值 ExecPolicy 即**最严**（上游取向，不是本包加的）：镜像必须带 digest，
// 任何挂载都拒——漏配策略拿到的是「挂载全拒」，不是「挂载全放」。
// 调用点应在**装配期**（把请求交给任何后端之前），判不过就不该交给后端。
func (j Job) JudgeExec(policy sandbox.ExecPolicy) error {
	return policy.Judge(j.ExecSpec())
}

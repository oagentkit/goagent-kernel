# 执行不变量与插点矩阵

> **这是什么**：评审（2026-11-27 §3.3 问题二 / §3.4 方向一）要的「执行正确性证明体系」的**声明面单源**。
> 机制正确不代表系统正确——任务链条（创建 → 领取 → 工具执行 → 副作用提交 → 事件发布 →
> Checkpoint 保存 → 终态提交 → 终态事件）每个环节单独正确，不代表组合之后没有竞态。
> 本文档把**七类不变量**（谁在守、守得全不全）与**七个步骤间隙**（哪个插点有场景、哪个是静默空隙）
> 登记成两张可机判的表。
>
> **判据**：`make invariantcheck`（执行体 `cmd/invariantcheck`，判据本体
> `internal/invariantledger`，已接进 `make unit` 与 `make check` ⇒ CI 会跑）。
> 登记的测试指针指向不存在的测试 ⇒ 红并点名；某个间隙既无场景也无书面豁免 ⇒ 红。
>
> **关联**：issue #523（本文件的开出单）、#529（阶段总纲）、`docs/acceptance-paths.yaml`
> （真环境路径的「被执行过」证据，与本文档互补：那边管跑没跑，这边管守没守）。

updated: 2026-11-27

## 一、七类不变量登记表

每行四要素齐全：**陈述**（一句话可判）、**判定机制**（哪个包哪个函数）、**成文处**（契约在哪份文档）、
**必红判据**（登记的测试指针，`invariantcheck` 核其真实存在）、**缺口**（显式声明，没有就写「无」——
留白即红，不许「应该没问题」）。

| id | 陈述 | 判定机制 | 成文处 | 必红判据 | 缺口 |
|---|---|---|---|---|---|
| state-transition | 状态只按迁移表走；词表外状态被三态化（过程 / 终态 / 读不懂），不猜 | `statemachine.CanTransition` / `contract.ClassifyStatus`（状态词表单源） | `statemachine` 包注释；`docs/error-semantics.md`（#363 收口：checkpoint / controlplane 各自的分类器删除改调统一判定面） | `statemachine.TestLegalTransitions`<br>`statemachine.TestIllegalTransitions`<br>`statemachine.TestTerminalMatchesTransitionTable`<br>`statemachine.TestRequiresApproval` | 新增状态消费方仍可自带分类器——#363 收口了存量两处，「不许再长出第二份」目前无门禁（同形面靠 review） |
| task-ownership | 租约期内唯一持有者；过期 / 接管后旧持有者的提交被存储端条件拒绝——拒绝不是「尽量」 | `worker.LeaseSession`（续期 + 丢失通知）、`taskboard` 租约 / 认领令牌、`ownership` Epoch（跨区域两阶段 + fence） | `docs/DISTRIBUTED-EVOLUTION-20261117.md` L1/L3 出口条件；`docs/SEAM-CONTRACT.md` | `worker.TestManagedExecutorRenewalPreventsTakeover`<br>`worker.TestManagedExecutorFencedCommitRejectsLateHolder`<br>`taskboard.TestConcurrentClaimExactlyOneWinner`<br>`ownership.TestTransferRequiresConfirmation` | 单机租约面与跨区域 Epoch 面分属两包，**合并视角**的对抗只在 faultmatrix 场景级（见插点矩阵），没有一行级用例同时打两层 |
| sideeffect-once | 一个动作至多施加一次：幂等键 / 租约 / 栅栏是**一份状态的三个投影**；未知结果不猜、不重复施加 | `sideeffect`（ActionKey + Fence + 结果未知态）、`sidetest` 共享契约套件、`retryplan` 三档路径（无幂等键不许猜着重试） | `docs/HARNESS-BENCHMARK-20261021.md` §五（**明确不承诺 exactly-once**——上界是至多一次）；ADR-0007 执行意图 | `sidetest.TestContract`<br>`sideeffect.TestInFlightBlocksSameKey`<br>`sideeffect.TestExpiredIntentRequiresReconcile` | 「下游接受幂等键」的接线责任在宿主（设计边界而非缺口）；仓内无下游侧契约套件——宿主接错只会在宿主侧显形 |
| checkpoint-monotonic | 快照序号单调：同序号唯一胜者、旧序号 / 旧持有者的写被闸拒绝、恢复不回退 | `checkpoint` 闸（SaveIfNewerFenced / CommitFenced）、Seq CAS | `docs/CHECKPOINT.md` 三条不变量与跨进程闸 | `checkpoint.TestConcurrentCommitsSerialize`<br>`checkpoint.TestStaleWriteRejectedAndGateRolledBack`<br>`checkpoint.TestFaultConcurrentSameSeqExactlyOneWinner` | 五条 `TestFault*` 覆盖写失败 / 恢复中断 / 双 Worker，**Seq 溢出与负数**等边界由存储实现各自兜（PG 面曾开 #425），仓内无统一溢出契约测试 |
| terminal-uniqueness | 终态只被提交一次：终态写入与终态事件同事务（Outbox 补做）；强制释放与 Worker 完成落到同一份权威任务表 | `commitgate` 终态独占闸、`finalize` Intent + Outbox、`controlplane` 权威同账（TaskSourceID 同源判定） | `docs/DISTRIBUTED-EVOLUTION-20261117.md` L2 出口条件；ADR-0007 | `commitgate.TestLeaseConcurrentAcquireHasExactlyOneWinner`<br>`controlplane.TestForceReleaseVsWorkerCompleteSingleWinner`<br>`reconcile.TestFinalizeAtomicFailureNoSplit` | 「双终态」的证明集中在权威同账与闸两处；**经由第三条写路径**（宿主绕过权威面直写）不在仓内可达，靠契约文档约束宿主 |
| event-recoverability | 事件不丢帧：已提交水位线（非裸 max(id)）推进；乱序 / 崩溃后恢复不丢事件、不重放已提交 | `eventbus` 水位线 + tail/outbox、`reconcile` 启动孤儿回收与 Outbox 补做 | `docs/DISTRIBUTED-EVOLUTION-20261117.md` L2 出口条件（#264 / #272） | `eventbus.TestOutboxWatermarkAdvancesOnlyWithCommit`<br>`reconcile.TestFinalizeOutboxNoLossNoDuplicate`<br>`reconcile.TestBootOrphanSkipsEventsAfterAnchorAndForeignOwner` | 水位线判据目前有事件面与孤儿回收两组用例；**跨存储实现**（Mem vs PG）的对齐依赖 pgstore 迁出后的 runtime 仓侧验收（acceptance-paths `owner` 字段如实登记） |
| multi-replica-race | 多副本同刻竞争有唯一胜者，且败者路径可观测（被拒有哨兵、有留痕）——场景级证明 | `worker/faultmatrix` 跨部署模式故障矩阵（15 场景）、`controlplane` 并发介入 | `docs/DISTRIBUTED-EVOLUTION-20261117.md` §场景表（controlrace / 集群类） | `worker/faultmatrix.TestFaultDeploymentMatrix`<br>`checkpoint.TestFaultTwoWorkersResumeSameTask`<br>`controlplane.TestForceReleaseVsWorkerCompleteSingleWinner` | 矩阵是**点名式**（人工挑高危场景），不按间隙×故障类型对账——完备性由下节插点矩阵核，缺口格逐格显形 |

## 二、插点矩阵（7 间隙 × 6 故障类型）

链条插点：**G1** 创建→领取、**G2** 领取→执行、**G3** 执行→副作用、**G4** 副作用→事件、
**G5** 事件→checkpoint、**G6** checkpoint→终态、**G7** 终态→终态事件。

格内只许三种内容：**测试指针**（`dir/TestName`，`invariantcheck` 核存在）、`—`（已判无此形态）、
`缺口`（已知没场景，行级仍须有场景或豁免兜底）。行级规则：**至少一条场景指针，或非空书面豁免**
（「—」不算理由）——这就是「零静默空隙」。

| 间隙 | F1 崩溃 | F2 双副本 | F3 响应丢失 | F4 租约过期+旧恢复 | F5 事件丢失 | F6 双终态 | 豁免/备注 |
|---|---|---|---|---|---|---|---|
| G1 创建→领取 | `reconcile.TestBootOrphanReclaimedBeforeAnchor` | `taskboard.TestConcurrentClaimExactlyOneWinner` | 缺口 | — | — | — | 创建回执丢失后重试不重复的用例待补（矩阵缺口，见 §三） |
| G2 领取→执行 | 缺口 | — | — | `worker.TestManagedExecutorRenewalPreventsTakeover` | — | — | 死在领取后、执行前的正向崩溃注入待补（矩阵缺口，见 §三）；租约过期+旧恢复由 F4 列覆盖 |
| G3 执行→副作用 | `sideeffect.TestExpiredIntentRequiresReconcile` | `sideeffect.TestInFlightBlocksSameKey` | `sideeffect.TestUnknownOutcomeFailsLoudly` | `worker.TestManagedExecutorLateCheckpointRejectedAfterTakeover` | — | — | 已知偶发红 `ptc.TestStressSubprocessKillMidRunReaps`（#465）不作判据登记——挂红的测试守不住任何东西 |
| G4 副作用→事件 | `eventbus.TestDurablePublishInsertPanicIsolated` | `worker/faultmatrix.TestFaultDeploymentMatrix` | `worker/faultmatrix.TestFaultDeploymentMatrix` | — | `worker/faultmatrix.TestFaultDeploymentMatrix` | — | faultmatrix 场景 `durable-write-then-publish-failure`（F1/F5 同源：写成而发布失败） |
| G5 事件→checkpoint | `checkpoint.TestFaultResumeInterruptedBeforeSnapshot` | `worker/faultmatrix.TestFaultDeploymentMatrix` | — | 缺口 | `eventbus.TestOutboxWatermarkAdvancesOnlyWithCommit` | — | faultmatrix 场景 `event-out-of-order-commit`（乱序提交不丢帧）；租约过期+旧恢复插在本间隙的正向用例待补（矩阵缺口，见 §三） |
| G6 checkpoint→终态 | `checkpoint.TestFaultResumeInterruptedThenResumes` | `checkpoint.TestFaultTwoWorkersResumeSameTask` | `checkpoint.TestFaultSaveFailsGateRollsBackSameSeqRetryable` | `worker.TestManagedExecutorFencedCommitRejectsLateHolder` | — | `checkpoint.TestFaultConcurrentSameSeqExactlyOneWinner`<br>`commitgate.TestLeaseConcurrentAcquireHasExactlyOneWinner` | — |
| G7 终态→终态事件 | `reconcile.TestFinalizeOutboxMarkFailureReplays` | `reconcile.TestFinalizeAtomicFailureNoSplit` | — | — | `reconcile.TestFinalizeOutboxNoLossNoDuplicate` | `controlplane.TestForceReleaseVsWorkerCompleteSingleWinner` | — |

## 三、缺口与补测计划（三个矩阵缺口 = 下一批三条插点用例的靶子）

矩阵里标 `缺口` 的三格是**已知零覆盖**（行级有场景兜底，故门禁绿——绿的是
「没有静默空隙」，不是「每一格都有场景」）。三条靶子按评审点名的故障形态选：

1. **G1/F3 创建回执丢失**：存储提交成功但响应没回来，客户端重试创建 ⇒ 不得出现
   重复任务。评审点名「数据库提交成功但响应丢失」在创建段的形态——仓内现有
   判据只到领取段（`taskboard.TestConcurrentClaimExactlyOneWinner`）。
2. **G2/F1 死在领取后、执行前**：正向崩溃注入（领取完成 → 进程死 → 重启后经
   租约过期重新可领）。现由 F4 列的租约过期接管**侧写**覆盖，缺正向注入——
   checkpoint `TestFault*` 的形态往链条前推一格即是。
3. **G5/F4 租约过期 + 旧恢复插在事件与 checkpoint 之间**：旧持有者在事件已发布、
   快照未保存时恢复执行 ⇒ 快照不得回退、事件不得重放。G6/F4 有
   `TestManagedExecutorFencedCommitRejectsLateHolder`，但插在 G5 的形态无正向用例。

补测纪律：新用例先在本矩阵落指针（`make invariantcheck` 当场核存在），再进
`fault-injection` 层或对应包——**指针先行，测试随后**会让门禁红，这是故意的：
登记了没补上比不登记更坏。

## 四、判据边界（如实登记）

- `invariantcheck` 判得了「**登记的测试存在**」，判不了「它真的在 CI 跑 / 没被 t.Skip」——后者由
  `make unit`（全量 `go test ./...`）与 fault-injection 分层口径兑底；被 Skip 的测试守不住东西，
  由测试质量面（review、#465 这类偶发红单）管。
- 七类不变量与七个间隙的 **id 集合硬编码在判据里**（`internal/invariantledger` 的
  `RequiredInvariants` / `RequiredGaps`）：验收口径本体不能从登记面删行——从清单里删掉一行就变绿
  的门禁守不住任何东西。新增第八类 ⇒ 改判据代码（code review 可见）+ 本文档补行。
- 本文档是**声明面**；「真环境跑过没跑过」的证据在 `docs/acceptance-paths.yaml`，两份登记面各管一事，
  不互判。

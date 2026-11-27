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
| state-transition | 状态只按迁移表走；词表外状态被三态化（过程 / 终态 / 读不懂），不猜 | `statemachine.CanTransition` / `contract.ClassifyStatus`（状态词表单源） | `statemachine` 包注释；`docs/error-semantics.md`（#363 收口：checkpoint / controlplane 各自的分类器删除改调统一判定面） | `statemachine.TestLegalTransitions`<br>`statemachine.TestIllegalTransitions`<br>`statemachine.TestTerminalFollowsTransitionTable`<br>`contract.TestStatusClassifiersDoNotEnumerateKnownStates` | 转移表无机械全表覆盖：TestLegalTransitions 手写 10 条，`running→interrupted` 与 `waiting_approval→cancelled` 两条合法边无正用例、负例亦是手写补集（#363 的 AST 判据只管分类器不管边） |
| task-ownership | 租约期内唯一持有者；过期 / 接管后旧持有者的提交被存储端条件拒绝——拒绝不是「尽量」 | `worker.LeaseSession`（续期 + 丢失通知）、`taskboard` 租约 / 认领令牌、`ownership` Epoch（跨区域两阶段 + fence） | `docs/DISTRIBUTED-EVOLUTION-20261117.md` L1/L3 出口条件；`docs/SEAM-CONTRACT.md` | `taskboard/boardtest.TestConformanceOnMemoryBoard`<br>`worker.TestManagedExecutorRenewalPreventsTakeover`<br>`worker.TestManagedExecutorFencedCommitRejectsLateHolder`<br>`ownership.TestPartitionStaleOwnerCannotCommit` | ownership 侧无共享契约套件（boardtest / sidetest / invocationtest 都有「宿主实现接进来跑」的套件，Authority/Fencer 只有内存自测）——宿主的持久化权威实现无套件可接 |
| sideeffect-once | 一个动作至多施加一次：幂等键 / 租约 / 栅栏是**一份状态的三个投影**；未知结果不猜、不重复施加 | `sideeffect`（ActionKey + Fence + 结果未知态）、`sidetest` 共享契约套件、`retryplan` 三档路径（无幂等键不许猜着重试） | `docs/HARNESS-BENCHMARK-20261021.md` §五（**明确不承诺 exactly-once**——上界是至多一次）；ADR-0007 执行意图 | `sidetest.TestContract`<br>`sideeffect.TestSinkContract`<br>`sideeffect.TestInFlightBlocksSameKey`<br>`sideeffect.TestExpiredIntentRequiresReconcile` | 真 PG 跨进程原子性判据随 pgstore 迁 goagent-runtime：本仓 sidetest 只跑内存替身，「Acquire 一次原子操作」这条头号硬要求在本仓**无跨进程反例**（宿主接错只在宿主侧显形） |
| checkpoint-monotonic | 快照序号单调：同序号唯一胜者、旧序号 / 旧持有者的写被闸拒绝、恢复不回退 | `checkpoint` 闸（SaveIfNewerFenced / CommitFenced）、Seq CAS | `docs/CHECKPOINT.md` 三条不变量与跨进程闸 | `checkpoint.TestSeqGateStrictlyMonotonic`<br>`checkpoint.TestSaveIfNewerFencedContract`<br>`checkpoint.TestFaultConcurrentSameSeqExactlyOneWinner`<br>`checkpoint.TestE2EResumeAfterCrashDoesNotReplay` | fence.go 自述的残余窗口（新持有者首提前，旧代仍可能命中「等于」分支）**无钉边界的用例**——有意如实登记的窗口，但窗口边界本身没人守；真存储 CAS（PG `WHERE seq < ?`）在对侧仓 |
| terminal-uniqueness | 终态只被提交一次：终态写入与终态事件同事务（Outbox 补做）；强制释放与 Worker 完成落到同一份权威任务表 | `commitgate` 终态独占闸、`finalize` Intent + Outbox、`controlplane` 权威同账（TaskSourceID 同源判定） | `docs/DISTRIBUTED-EVOLUTION-20261117.md` L2 出口条件；ADR-0007 | `commitgate.TestConcurrentTerminalCommitHasExactlyOneWinner`<br>`commitgate.TestLeaseConcurrentAcquireHasExactlyOneWinner`<br>`controlplane.TestForceReleaseVsWorkerCompleteSingleWinner`<br>`reconcile.TestFinalizeAtomicFailureNoSplit` | 七类里**登记制覆盖最薄**：不在 ROUTE-LEDGER 任何出口条件、DEPLOY-MATRIX 无 commitgate/finalize 能力行、faultmatrix 无双终态故障类；跨进程终态独占依赖宿主 store.Claim 原子性，本仓仅 MemoryStore 自测 |
| event-recoverability | 事件不丢帧：已提交水位线（非裸 max(id)）推进；乱序 / 崩溃后恢复不丢事件、不重放已提交 | `eventbus` 水位线 + tail/outbox、`reconcile` 启动孤儿回收与 Outbox 补做 | `docs/DISTRIBUTED-EVOLUTION-20261117.md` L2 出口条件（#264 / #272） | `eventbus.TestTailerOutOfOrderCommitNotSkipped`<br>`eventbus.TestNaiveAutoIncrementFormLosesFrames`<br>`eventbus.TestOutboxWatermarkAdvancesOnlyWithCommit`<br>`reconcile.TestFinalizeOutboxNoLossNoDuplicate` | ① `PublishInTx` 判据体随 pgstore 迁出——「事件行与业务状态同事务」这条 L2 出口条件本仓**零测试**；② eventbus 6 处 50ms 缺席断言落在 wallclock 门禁的豁免形态里且不在 `ruler_classifications` ⇒ 门禁与登记面**双重看不见**（#443 同形残余） |
| multi-replica-race | 多副本同刻竞争有唯一胜者，且败者路径可观测（被拒有哨兵、有留痕）——场景级证明 | `worker/faultmatrix` 跨部署模式故障矩阵（15 场景）、`controlplane` 并发介入 | `docs/DISTRIBUTED-EVOLUTION-20261117.md` §场景表（controlrace / 集群类） | `worker/faultmatrix.TestFaultDeploymentMatrix`<br>`checkpoint.TestFaultTwoWorkersResumeSameTask`<br>`controlplane.TestForceReleaseVsWorkerCompleteSingleWinner` | 矩阵是**点名式**（人工挑高危场景），不按间隙×故障类型对账——完备性由下节插点矩阵核，缺口格逐格显形；controlrace 只有「介入 vs 完成」一对，无双介入 / 介入 vs 续期变体；场景实现是 `run*` 非 `Test*` ⇒ 格级只能指到父级 `TestFaultDeploymentMatrix` |

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
| G5 事件→checkpoint | `checkpoint.TestFaultResumeInterruptedBeforeSnapshot` | `worker/faultmatrix.TestFaultDeploymentMatrix`<br>`eventbus.TestTailerOutOfOrderCommitNotSkipped` | — | 缺口 | `eventbus.TestOutboxWatermarkAdvancesOnlyWithCommit` | — | faultmatrix 场景 `event-out-of-order-commit`（乱序提交不丢帧）；租约过期+旧恢复插在本间隙的正向用例待补（矩阵缺口，见 §三） |
| G6 checkpoint→终态 | `checkpoint.TestFaultResumeInterruptedThenResumes` | `checkpoint.TestFaultTwoWorkersResumeSameTask` | `checkpoint.TestFaultSaveFailsGateRollsBackSameSeqRetryable` | `worker.TestManagedExecutorFencedCommitRejectsLateHolder` | — | `checkpoint.TestFaultConcurrentSameSeqExactlyOneWinner`<br>`commitgate.TestConcurrentTerminalCommitHasExactlyOneWinner` | — |
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

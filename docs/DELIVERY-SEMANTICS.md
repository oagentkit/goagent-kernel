# 交付语义分级（DELIVERY-SEMANTICS）

> **这是什么**：评审（2026-11-27 §3.3 问题三）要求把「Exactly Once」拆开——
> **不要把所有能力统一包装成 Exactly Once**。本文件是交付语义的**单源声明面**：
> 四档词表定义 + 逐能力面承诺表 + 失真声明白名单。
>
> **判据**：`make semanticscheck`（执行体 `cmd/semanticscheck`，已接进 `make unit`
> 与 `make check` ⇒ CI 会跑）：能力面表覆盖全部 Stable 包、交付行 ≥6 且档位在四档
> 值域内、登记的判据测试真实存在；生产文档与包注释里出现 `exactly-once` 字样而
> **既不在白名单也不在拒绝语境**（±2 行内有拒绝标记）⇒ 红——把上界说成 exactly-once
> 是本仓明确拒绝的失真（`docs/HARNESS-BENCHMARK-20261021.md` §五）。
>
> **与相邻登记面的分工**：`docs/INVARIANTS.md` 管**执行正确性**（谁在守）；
> 本文档管**承诺口径**（每一面承诺哪一档）。两份各管一事，不互判。

updated: 2026-11-28

## 一、四档词表

| 档位 | 一句可判定义 | 仓内机制指针 |
|---|---|---|
| **at-most-once** | 同一动作至多生效一次——宁可少做，绝不重做；不确定时进入「结果未知」而不是重试 | `sideeffect`（Acquire 一次原子操作 + 结果未知态）、`commitgate`（终态独占闸）、`taskboard`（认领恰好一赢家） |
| **at-least-once** | 同一交付至少到达一次——可能重复，消费侧必须能吸收重复 | `eventbus`（落库取游标→广播、失败不广播 + 已提交水位线）、`reconcile`（Finalize Outbox 补做） |
| **effectively-once** | 对外观察效果恰好一次 = at-least-once 交付 + 消费侧幂等吸收；**只在系统边界内成立，不向下游转嫁承诺** | `reconcile`（终态写与终态事件同事务 + Outbox） |
| **idempotent-execution** | 同一幂等键的重试命中「已做过」直接返回原结果——重放安全由键保证，不由「只做一次」保证 | `sideeffect`（幂等键重放 PathReplayed）、`retryplan`（无幂等键不许猜着重试）、`checkpoint`（同序号唯一胜者） |

**裁决口径**：档位是**承诺**，不是描述。对外（宿主文档、README、包注释）只许按上表
措辞承诺；把 at-most-once 的上界说成 exactly-once = 失真（§三白名单外即红）。
**exactly-once 一词在本仓只出现在拒绝与对照语境里**——这是设计，不是巧合。

## 二、逐能力面语义表

交付行 **7 行**（≥6 达标）+ 非交付行 8 行（Stable 契约面零遗漏——判据面本身不移动
任何交付，如实标「不适用」而不是硬安一个档位）。包列覆盖全部 15 个 Stable 包。

| 能力面 | 包 | 承诺档位 | 判定机制 | 现有判据 |
|---|---|---|---|---|
| 事件发布 | eventbus | at-least-once（水位线不丢帧；重复帧由订阅游标吸收） | DurablePublisher 落库取游标→广播、失败不广播；WatermarkTailStore 无空洞推进 | `eventbus.TestTailerOutOfOrderCommitNotSkipped`<br>`eventbus.TestOutboxWatermarkAdvancesOnlyWithCommit`<br>`eventbus.TestTailerDuplicateFrameAbsorbedBySubscriberCursor` |
| 副作用施加 | sideeffect、sidetest | at-most-once（**明确拒绝 exactly-once**；带幂等键的重放 = idempotent-execution） | `Store.Acquire` 一次原子操作三判据（幂等键/租约/栅栏）；执行意图 + 结果未知态；sidetest 契约套件 | `sidetest.TestContract`<br>`sideeffect.TestInFlightBlocksSameKey`<br>`sideeffect.TestRetryWithSameKeyReplays` |
| 环节终态写 | commitgate | at-most-once（终态独占闸，第二次提交必拒） | `Guard.Acquire` 凭令牌放闸，令牌由存储铸造 | `commitgate.TestFirstTerminalWins`<br>`commitgate.TestSecondTerminalRejected`<br>`commitgate.TestConcurrentTerminalCommitHasExactlyOneWinner` |
| 快照持久化 | checkpoint | idempotent-execution（同序号唯一胜者；写失败回滚后同序号可重试至成功；旧序号拒） | `StoreCAS.SaveIfNewer` 条件写 + SeqGate 单调 | `checkpoint.TestFaultConcurrentSameSeqExactlyOneWinner`<br>`checkpoint.TestFaultSaveFailsGateRollsBackSameSeqRetryable`<br>`checkpoint.TestStaleWriteRejectedAndGateRolledBack` |
| 任务认领与令牌提交 | taskboard | at-most-once（并发认领恰好一赢家；令牌恰好一提交） | Claim 条件认领 + 令牌由存储铸造 | `taskboard.TestConcurrentClaimExactlyOneWinner`<br>`taskboard.TestDuplicateTokenSubmitExactlyOneWinner` |
| 配额扣减与回收 | scheduling、tenant | at-most-once（整单占用不部分占用、超释放显式拒；凭租约全额归账不虚增） | `QuotaStore.Reserve/Release` 存储侧原子扣减 | `scheduling.TestMemQuotaStoreNoOversell`<br>`scheduling.TestMemQuotaStoreReleaseByLease`<br>`scheduling.TestQuotaReclaimAfterWorkerLost` |
| 重试路径裁决 | retryplan | idempotent-execution（幂等失败直连重试；非幂等必须走幂等键重放；无键不许猜着重试） | 三档路径（PathReplayed / PathNone / 原样重发） | `retryplan.TestIdempotentFailureRetriesDirectly`<br>`retryplan.TestNonIdempotentFailureNeedsReplay` |
| 编排运行契约 | contract | 不适用（非交付面） | 接口框架本身——交付语义由各面实现兑现，本包不移动任何交付 | — |
| 状态迁移判定 | statemachine | 不适用（非交付面） | 合法迁移表是纯函数判定面，不移动交付 | — |
| 报告契约 | protocol | 不适用（非交付面） | 报告解析是纯函数判定面，产物送达归事件面 | — |
| 超时看守 | watchdog | 不适用（非交付面） | 超时/无进展是判定面，不移动交付 | — |
| 工具拒绝合成 | guardset | 不适用（非交付面） | 多条拒绝合成一条裁决是判定面 | — |
| 工具披露 | tooldisclose | 不适用（非交付面） | 模型可见面派生是判定面 | — |
| 流式增量 | delta | 不适用（非交付面） | 输出块的送达归事件面，delta 只做渲染 | — |
| 优雅停机 | drain | 不适用（非交付面） | 停机收敛面，不承诺输出送达语义 | — |

## 三、失真声明白名单（文件级，理由必填）

白名单外的 `exactly[- ]once`（大小写不敏感）出现在**根目录 md / docs/*.md / 生产
`.go` 注释**即红。白名单只收「拒绝与对照语境的家」——每行理由非空，缺理由的白名单
条目本身判红（豁免必须写理由，与 policy 例外同纪律）：

| 文件 | 理由 |
|---|---|
| `docs/DELIVERY-SEMANTICS.md` | 本表：四档定义与对照的家 |
| `docs/HARNESS-BENCHMARK-20261021.md` | 对标对照文档——§五是「明确拒绝 exactly-once」的原始出处 |
| `sideeffect/sideeffect.go` | 拒绝语境的包注释本体（HARNESS §五的代码侧镜像） |
| `CHANGELOG.md` | 历史快照，按定义记录过去版本的表述 |

**白名单外的通行规则——拒绝语境窗口**：命中行**或其 ±2 行窗口**内含拒绝标记
（`不承诺` / `不提供` / `不构成` / `拒绝` / `而非` / `尚未` / `不是` / `接线责任` /
`要求下游` / `说成` / `失真`）⇒ 放行。这条规则让 README / AGENTS / USAGE /
INVARIANTS 里的**拒绝句**合法存在，而**正向承诺**（「本包保证 exactly-once」）无处
可藏——它附近没有任何拒绝标记。

**判定面边界（如实登记）**：`site/`（文档站源）是镜像面，不进扫描（与 docsync 同
口径），文档站表述由发布前抽查兑底；CHANGELOG 按历史快照排除。机器判「字面量 +
窗口」，判不了「拒绝句写得诚不诚实」——那归 review。

## 四、判据边界（如实登记）

- 机器判：表结构 / Stable 全覆盖 / 档位值域 / 交付行 ≥6 / 判据测试存在 / 失真扫描。
  判不了：「这一档归得对不对」（某面该 at-least-once 还是 effectively-once 是语义
  判断，归 review）——档位改动人维护表，门禁照旧核覆盖与值域。
- 新增能力面 ⇒ 表里加行（不加 = 若它是 Stable 包，覆盖判据当场红；若非 Stable，
  行不加不影响判据但属登记遗漏，review 兜）。

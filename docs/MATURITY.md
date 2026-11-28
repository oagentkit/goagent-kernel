# 机制成熟度盘点（MATURITY）

> **这是什么**：评审（2026-11-27 §3.3 问题一 / §3.4 方向三「机制成熟度管理」）要求的
> **Experimental 全量盘点面**——不是所有包都该同时进 Stable；「维持演进」不能变成
> 「永远没人盘」。本文档回答两个问题：**每个 Experimental 包现在处于什么成熟度阶段、
> 依据是什么**；**晋级要拿什么证据**。
>
> **判据**：`make apilifecheck` 第五类判据（执行体 `cmd/apilifecheck/maturity.go`）：
> 盘点行 ↔ 清单 Experimental 集合**双向覆盖**、五分类值域、依据非空；标「晋级候选」
> 的行**三维证据**（breaking 窗口 / 宿主接入 / 故障测试）缺一格即红。
>
> **与相邻登记面的分工**：`api-lifecycle.yaml` 管**稳定性等级**（改了算不算 breaking）；
> 本文档管**演进阶段**（离 Stable 还差什么、还该不该活着）。等级由清单定，阶段在
> 这里盘；晋级动作发生在清单，前提先落在这里。

updated: 2026-11-28

## 一、晋级三维判据（Experimental → Stable）

2026-11-28 起从「一个 minor 无 breaking」一维升为三维（全文见 `docs/api-compat.md`）：

| 维度 | 要求 | 为什么 |
|---|---|---|
| ① 无 breaking 窗口 | 一个 minor 版本内无 breaking，判定语义有配套契约套件或负对照 | 稳定承诺的本体 |
| ② 真实宿主接入证据 | 有宿主（goagent-runtime 或下游）在真实装配里 import 并跑通过 | 「有套件」≠「有宿主在用」——没人接过的 Stable 是纸面 Stable |
| ③ 真实故障测试证据 | 有 fault-injection / faultmatrix / acceptance-path 指针覆盖其关键判据 | 「没红过」≠「红过给你看」——没被故障打过的判据不知道守不守得住 |

机器可判半边：晋级候选行的三格证据非空（`apilifecheck` 判）；三维的**内容真伪**
归 review（门禁判「填了没有」，不判「填得对不对」——与本仓全部登记面同一边界）。

## 二、五分类定义

| 分类 | 含义 | 处置去向 |
|---|---|---|
| **晋级候选** | 三维证据齐，下一版可显式晋级 | 晋级时撤盘点行（行与级别同生命周期） |
| **维持演进** | 机制还在动（近期有 breaking 或判据面未定型），或门禁件/套件按设计不晋级 | 每个 minor 版本边界复盘 |
| **待合并** | 与仓内另一包高度重叠，该合 | 开合并单，合并后撤行 |
| **待降级** | 三问命中「只在特定调用顺序下有意义」，应降 `internal/` 或并入调用方 | 开降级单（破坏性 minor），处置后撤行 |
| **待废弃** | 无消费方、无宿主价值、或已被上游替代 | 开废弃单（Deprecated → 删除路径） |

**调用顺序依赖三问**（待降级的判定口径，评审 §3.3 问题一原文）：

1. 单独 import 能否构成可观察的输入→输出行为？
2. 它的导出面是否只被某个特定调用方按固定顺序驱动？
3. 宿主直接用它是否必然搭配另一个包的前置装配？

三问命中 ≠ 自动降级——命中后逐个给处置（降级 / 并入 / **书面保留**），保留必须
写理由；处置结论落盘点表「依据」格，零悬空。

## 三、盘点表（Experimental 全量，五分类）

每行六列；「breaking 窗口 / 宿主接入 / 故障测试」三格只有晋级候选必填（其余填 `—`），
「依据」全行必填（没有依据的分类无从复核）。

| pkg | 分类 | breaking 窗口 | 宿主接入 | 故障测试 | 依据 |
|---|---|---|---|---|---|
| invocation | 晋级候选 | 一个 minor 内仅加法（v0.75.1 词表 7→8，明文零符号消失零签名变更） | runtime/pgstore 真实 import（HOST-SURFACE 地面真相 15 包之一） | invocationtest 变异负对照 + 恢复族 + 并发单赢家（入 make contract） | 三维齐；reason 的「还会反向调整」指词表/场景扩容（additive），非接口 breaking——晋级动作时复核该句是否已过期 |
| invocation/invocationtest | 晋级候选 | 场景只增不删（套件自身无 breaking 面） | runtime 测试侧真实 import（pgstore 跑套件） | 本体即负对照套件（mutation + 14 场景，坏实现必红） | 与 invocation 同批晋级；套件的 Stable 承诺 = 场景只增不删 |
| taskboard/boardtest | 晋级候选 | 场景只增不删（taskboard 本体是 Stable，套件吸收其历史变更） | runtime 测试侧真实 import（pgstore/reference-runtime） | 本体即竞争负对照套件（mutation + 认领/令牌/租约接管） | 与 taskboard 同批；晋级动作前复核 v0.33 TTL 口径已全部吸收进场景 |
| trajectory | 维持演进 | — | — | — | 轮次模型明文未冻结、投影字段随上游事件流扩充（11-24 仍在改） |
| ptc | 维持演进 | 两轮 BREAKING 落本包（v0.31/v0.33 缺省即拒绝、内存上限） | — | SIGKILL/OOM 注入、预算耗尽回收（非 TestFault 名但为真注入） | 71 条提交高频仍在动，VM 编排面未定型 |
| egress | 维持演进 | — | — | — | 兑现强度判据两端仍空、P0 未关（11-26 仍在对齐强度） |
| gatecheck | 维持演进 | — | — | — | 审批×沙箱联合口径随上游两层各自演进，未定型 |
| preset | 维持演进 | — | — | — | 档位×审批×出口三件其一未冻结，组合面随动 |
| skilldisclose | 维持演进 | — | — | — | 三信号触发权重校准中、检索面未冻结；有 USAGE 教学面故不到待废弃 |
| hookengine | 维持演进 | — | — | — | 传输形态上游分叉未收敛，匹配语义可能随调 |
| delegate | 维持演进 | — | — | — | ACP 线格式与能力语义仍在动，契约随上游 |
| worktree | 维持演进 | — | — | — | 冲突判据刚落，挂载/隔离的宿主侧语义未补全 |
| metrics | 维持演进 | — | — | — | 五类运营量口径刚立（名称/维度随看板增补）；零 Go 消费方但 USAGE 有埋点教学——最接近待废弃者，复盘时重点看 |
| cost | 维持演进 | — | — | — | 刚立包（11-13），事件种类与维度是开放键未收口；有 tenant 生产消费方 |
| tenant | 维持演进 | — | runtime 真实 import | 弱（声明源故障上抛 1 条，无 TestFault） | 存储键/领取条件仍在扩面（11-16 改过键面），故障证据薄 |
| controlplane | 维持演进 | — | runtime 真实 import | TestFaultCreateReceiptLostRetryNoDuplicate + brokenAuditor 故障替身 | 宿主+故障证据俱强（最接近晋级），但 11-22/28 仍在改权威原子接缝与绑定面——接口未定型 |
| archcheck | 维持演进 | — | — | — | 门禁执行体：规则随 policy 增删（55 条提交），判据面天生开放不晋级 |
| internal/ledgerstatus | 维持演进 | — | — | — | 内部件（Go 可见性挡外），随台账门禁演进 |
| internal/acceptancepath | 维持演进 | — | — | — | 内部件，验收路径门禁专用 |
| internal/routeledger | 维持演进 | — | — | — | 内部件，路线对账门禁专用 |
| internal/invariantledger | 维持演进 | — | — | — | 内部件，#523 对账门禁专用（11-28 立包） |
| internal/hostsurface | 维持演进 | — | — | — | 内部件，#524 接入面门禁专用（11-28 立包） |
| backend | 维持演进 | — | runtime 真实 import | 弱（基础设施故障与作业失败分层 1 条） | 执行后端契约刚立，Docker/K8s 接入形态待真实引擎磨合，故障证据薄 |
| handoff | 维持演进 | — | — | — | 四问契约草案刚落，跨进程存储形态未定 |
| ownership | 维持演进 | — | — | — | 单提交新件（11-17），落库形态与真实多区域接入未验证 |
| eval | 维持演进 | — | — | — | 录制/重放判项词表开放未收；零消费方，叠在 trajectory 之上非重叠 |
| coord | 维持演进 | — | runtime 真实 import | 无本包 TestFault（降级窗口单测 + faultmatrix 间接压） | Backend 三原语形状待 Redis/etcd 真实接入反馈；宿主证据强但本包无独立故障套件 |
| worker | 维持演进 | 窗口内无接口 breaking（近期 30 条为缺陷修复与测试补齐） | runtime 必接 import（HOST-SURFACE 必须接） | 全仓最厚：faultmatrix 14 类 + TestFaultClaimedWorkerDies + TestFaultLeaseExpired | 三维客观证据全仓最齐，但本包 reason 自述「Registry 接口与 Loop 六段边界还要随宿主反馈调整」= 判据面未定型——边界收敛后**首批转晋级候选** |
| scheduling | 维持演进 | — | — | — | v0.43.0 唯一编译期 breaking 就在此包（QuotaStore 扩三方法），刚 breaking 即晋级不成立 |
| dispatch | 维持演进 | — | — | — | 三档词表与改派环刻度未定型；与上游 agent/dispatch 是分工非重叠 |
| reconcile | 维持演进 | — | — | TestFinalizeOutboxNoLossNoDuplicate + boot 孤儿回收族（INVARIANTS 指针） | 适配器未接线、三阈值未定型；故障/不变量证据真实 |
| deadline | 维持演进 | — | — | — | **书面保留（三问 C2）**：包内两半——墙钟锚点机制宿主直用（finalize 完全不碰）、Route 单源被 finalize 只做投影（#355，route_cross_test 钉一致非双表）；`import_test.go` 已把「至少一个非测试 import」钉成机器判据；USAGE 无节是教学欠账 |
| finalize | 维持演进 | — | — | — | 链顶收尾入口，宿主直接调用（4 测试独立可观察），仓内无消费方属预期；接缝形状待首个宿主打磨；与 deadline 是投影关系非重叠 |
| steer | 维持演进 | — | — | — | **书面保留（三问 C3）**：唯一仓内方 deadline 只读 `ErrSteered` 哨兵（不驱动导出面），五件套由宿主驱动；`finalize → deadline → steer` 单向依赖是 policy 明文；USAGE 0 提及是 45 包最极端教学欠账（非降级理由） |
| raceprobe | 维持演进 | — | — | — | **书面保留（三问 C5）+ 口径已消解**：纯函数判定层留公开路径是为跨仓复用（Go internal 会挡外仓 import）——HOST-SURFACE 已同步改「可选」，两份文档不再打架 |

<!-- 待三问审计裁决的疑点行（5）：deadline / finalize / steer / internal 五件是否
     转「待合并 / 待降级」——裁决后覆盖上方对应行。 -->

## 四、调用顺序依赖三问审计处置记录（2026-11-28）

45 个公开包实审（internal/* 五件按内部件口径免审），**严格三问全中（Q1 难 + Q2 单一 + Q3 必配）= 0 个包**——本仓是宿主面内核库，22 个包仓内生产消费方为 0（消费方在宿主侧），「仓内单一消费方」本身不构成降级理由。Q2/Q3 组合命中的 5 项全部**书面保留**（依据落盘点表「依据」格）：

| 项 | 命中 | 保留理由（摘要） |
|---|---|---|
| C1 cost | 仓内唯一消费方 tenant，USAGE 无节 | 口径面主场景是宿主出账单/配额，降 internal 会堵死宿主直接记账 |
| C2 deadline | Route 半边只被 finalize 投影 | 两半分治：墙钟锚点宿主直用 + Route 单源投影（#355）；机器判据已钉非测试消费方 |
| C3 steer | 唯一仓内方只读哨兵 | 五件套由宿主驱动；依赖方向 policy 明文；USAGE 欠账另记 |
| C4 handoff | USAGE 无节 | 契约 + LocalDesk 参考实现独立可跑，宿主跨进程交接直连面 |
| C5 raceprobe | 唯一消费方是自家 cmd | **口径冲突已消解**：HOST-SURFACE 内部件 → 可选（与 api-lifecycle「跨仓复用」对齐，与 archcheck 同物种） |

**反例锚点**（不算调用顺序依赖）：`taskboard/boardtest` / `sidetest` / `invocation/invocationtest`
是宿主要接的契约套件——套件被固定顺序调用恰恰是它的工作方式。

**教学面欠账**（与 HOST-SURFACE §二六包合并记）：`steer`（曾 0 提及，45 包最极端）、
`cost`、`handoff`、`eval`、`reconcile`、`finalize`、`metrics` 七包 USAGE 接入节
已补清（issue #549）——**本轮欠账清单清零**。HOST-SURFACE §二地面真相节另记的六包
（`backend` / `controlplane` / `invocation` / `tenant` / `worker` / `coord`，
宿主真在用但 USAGE 无接入节）仍待补，不属本轮范围。

**待合并观察（不转分类）**：internal/* 五件结构同构（登记面解析 + 纯判据 + cmd），
但归并会耦合五个门禁的演进节奏（改一个门禁的形状连坐四个）——本轮判「维持演进 +
观察在案」；若门禁件继续增殖，启动归并复盘。

## 五、复盘节奏与判据边界

- **复盘节奏**：分类为「维持演进」的包在**每个 minor 版本边界**复盘一次（发版
  prewrite 时顺手过一遍表）；晋级 / 合并 / 降级 / 废弃任一处置发生 ⇒ 同步改表
  （改清单不改表 = apilifecheck 当场红：行与级别同生命周期）。
- **判据边界（如实登记）**：机器判「覆盖 / 值域 / 依据非空 / 晋级三格非空」，判不了
  「依据写得对不对」「三维证据真伪」——那归 review 与证据本身（故障测试指针在
  `docs/INVARIANTS.md` / faultmatrix，宿主接入指针在 `docs/HOST-SURFACE.md` 地面真相节）。

# goagent-kernel · 多智能体编排通用内核

多智能体编排通用内核 —— oagentkit 模块矩阵 **tier 4**（编排判据层，判据单源 ADR-0010）。

本仓依赖的矩阵内仓：`guardx` `runtime` `toolgate` `toolname`。

> 一句话：**编排是代码，LLM 只做单环节**——本仓只提供确定性编排的**机制面**，
> **不认识任何领域概念**。

从上游私有宿主仓（代号见内部文档）拆出的内核层，宿主业务不公开。定位对标同族的
`oagentkit`（Agent/LLM 编排工具链矩阵）与 `oekit`（基础库）：**独立发版、版本化引入、
零 replace**（go.mod 含 replace 即门禁红；本地并行开发走 `make devlink` 的 workspace，见下）。

> **本文档导航**
> - [README](README.md)（本文）= 是什么、为什么这么设计、边界纪律
> - **[docs/USAGE.md](docs/USAGE.md) = 怎么接、接错了会怎样** ← 宿主开发者从这儿开始
> - **[docs/HOST-SURFACE.md](docs/HOST-SURFACE.md) = 宿主接入面清单**：六步主链「宿主提供 / kernel 兑现」对照 + 全量包三分类（机器判定，`make hostsurfacecheck`）
> - **[docs/AI-GUIDE.md](docs/AI-GUIDE.md) = 怎么改、会被哪条门禁拦** ← 要动这个仓的 AI 代理从这儿开始
> - [AGENTS.md](AGENTS.md) = 在本仓干活的工作纪律
> - **[文档站](https://yi-nology.github.io/goagent-kernel/)** = 介绍 / 设计初衷 / 怎么用 / 能力 / 参考（VuePress，源码在 `site/`）

> **全部文档一览**（`docs/` 下标着**历史快照**的那些记录当时的判断与踩坑，
> 不是当前事实；当前事实一律以本文 + 门禁 + 源码为准）：
>
> | 文档 | 回答什么 | 性质 |
> |---|---|---|
> | [README](README.md) | 是什么、为什么这么设计、边界纪律、下游升级须知 | 活文档 |
> | [docs/USAGE.md](docs/USAGE.md) | 宿主开发者：怎么把 harness 接进自己产品 | 活文档 |
> | [docs/AI-GUIDE.md](docs/AI-GUIDE.md) | 改本仓的 AI 代理：任务 → 改哪些文件 → 哪条门禁拦 → 怎么验收 | 活文档 |
> | [AGENTS.md](AGENTS.md) | 在本仓干活的工作纪律（每条纪律的事故来源） | 活文档 |
> | [architecture-policy.yaml](architecture-policy.yaml) | 门禁**声明面**：模块拓扑 / 规则 / 阈值 / 例外 | 活文档（机器判定）|
> | [CHANGELOG.md](CHANGELOG.md) | 版本历史与破坏性变更 | 活文档（按 tag 切段生成）|
> | [api-lifecycle.yaml](api-lifecycle.yaml) | **API 生命周期清单**：每个生产包的 Experimental/Stable/Deprecated 定级 + 理由（机器判定） | 活文档（机器判定）|
> | [docs/api-compat.md](docs/api-compat.md) | API 兼容策略：分级判定规则（什么算 breaking）/ 破坏性变更流程 / 宿主升级须知 | 活文档 |
> | [docs/error-semantics.md](docs/error-semantics.md) | 全仓错误哨兵语义总表：哪个包哪个哨兵、触发条件、调用方处置（机器判定） | 活文档（机器判定）|
> | [docs/SEAM-CONTRACT.md](docs/SEAM-CONTRACT.md) | 宿主接缝契约：三标记模板 + 门禁三形态（A 只读 / B 阻断 / C 阻断）+ 各自漏判与取舍（issue #369/#370） | 活文档（机器判定）|
> | [docs/adr/README.md](docs/adr/README.md) | 决策记录（ADR）索引：每条设计取舍的编号 / 一句话结论 / 状态 | **决策记录**（ADR）｜设计推导与取舍的真相源 |
> | [docs/OAGENTKIT-OVERLAP.md](docs/OAGENTKIT-OVERLAP.md) | 本仓与上游 `oagentkit` 的能力边界：哪些该做、哪些上游已有 | 活文档 |
> | [docs/SANDBOX-DESIGN-20261021.md](docs/SANDBOX-DESIGN-20261021.md) | 执行沙箱的下一层：跨域执行与资源兜底（发现「内存无兜底」真缺陷 + 与上游 guardx 的分工）| 活文档 |
> | [docs/HARNESS-BENCHMARK-20261021.md](docs/HARNESS-BENCHMARK-20261021.md) | 对标 Codex / DSH / Octop / Coze 五家：借鉴了什么、什么归宿主、什么本轮没做 | 活文档 |
> | [docs/GAP-ANALYSIS-20261021.md](docs/GAP-ANALYSIS-20261021.md) | 与 Codex / DSH / Octop / Coze 的**逐项实测对齐**：差距在哪个维度、哪些该补、哪些不该补 | 活文档 |
> | [docs/GAP-ANALYSIS-20261106.md](docs/GAP-ANALYSIS-20261106.md) | 同一对标的**刷新**（2026-11-06 重拉五家）：Coze 已停更、DSH 219→400 叶子包、缺口从「可撤销性」移到「判定面的宽度」| 活文档 |
> | [docs/GAP-ANALYSIS-20261107.md](docs/GAP-ANALYSIS-20261107.md) | 第三次刷新（2026-11-07 重拉五家）：**参照系这轮没长出新机制面**；唯一开着的 P0 是出口强制件的**两端都空**（无代理 + 无 `Enforcer` 实现）| 活文档 |
> | [docs/DISTRIBUTED-EVOLUTION-20261117.md](docs/DISTRIBUTED-EVOLUTION-20261117.md) | **分布式演进方向**：外部评审逐条核验台账 + L1/L2/L3 三阶段路线 + 场景化部署配方的当前缺口 | 活文档（评审核验，2026-11-17）|
> | [docs/INVARIANTS.md](docs/INVARIANTS.md) | **执行不变量与插点矩阵（机器可查）**：七类不变量的判定机制 / 成文处 / 必红判据绑定 + 7 步骤间隙 × 6 故障类型覆盖（零静默空隙）；`make invariantcheck` 判指针存在与行级兜底（issue #523） | 活文档（机器判定）|
> | [docs/HOST-SURFACE.md](docs/HOST-SURFACE.md) | **宿主接入面清单（机器可查）**：六步主链「宿主提供 / kernel 兑现」对照 + 全量包三分类（必须接 / 可选 / 内部件）+ 文档 import 白名单；`make hostsurfacecheck` 判覆盖全集与白名单（issue #524） | 活文档（机器判定）|
> | [docs/MATURITY.md](docs/MATURITY.md) | **机制成熟度盘点面（机器可查）**：Experimental 全量五分类（晋级候选/维持演进/待合并/待降级/待废弃）+ 晋级三维证据 + 三问审计处置记录；`make apilifecheck` 第五类判据（issue #525） | 活文档（机器判定）|
> | [docs/DELIVERY-SEMANTICS.md](docs/DELIVERY-SEMANTICS.md) | **交付语义分级（机器可查）**：四档词表（at-most-once / at-least-once / effectively-once / idempotent-execution）+ 逐能力面承诺表（Stable 全覆盖）+ 失真声明白名单；`make semanticscheck` 扫「把上界说成 exactly-once」（issue #526） | 活文档（机器判定）|
> | [docs/PLANES.md](docs/PLANES.md) | **三平面划分（机器可查）**：控制/执行/恢复/横切定义 + 50 包平面映射 + 跨面依赖规则（观测方向必须登记、禁止方向命中即红）；`make planecheck`（issue #527） | 活文档（机器判定）|
> | [docs/DEPLOY-MATRIX.yaml](docs/DEPLOY-MATRIX.yaml) | **部署配方能力清单（机器可查）**：场景 → 必需能力 → 证据（契约符号 / 门禁 / issue）→ 当前状态；`make deploycheck` 判证据可解析、状态由证据推导、与 USAGE 口径一致（issue #277） | 活文档（机器判定）|
> | [docs/STORAGE-SELECTION.yaml](docs/STORAGE-SELECTION.yaml) | **存储选型声明面（机器可查）**：什么时候用 SQLite / 什么时候用 PostgreSQL——允许集 / 能力归属 / 落点 / 选型口径 / 已知不对称；`make storagecheck` 判场景选型、能力齐备、落点可解析、写者数不越界（issue #424） | 活文档（机器判定）|
> | [docs/K8S-EGRESS-E2E-20261119.md](docs/K8S-EGRESS-E2E-20261119.md) | **K8s 出站禁网的真集群实测结论**：环境 / 复跑方式（`make k8s-e2e`）/ 四条验收标准的实测 / **结论边界**（issue #418）| 活文档（实测结论，2026-11-19）|
> | [docs/CROSS-ENV-VERIFY-20261021.md](docs/CROSS-ENV-VERIFY-20261021.md) | 跨环境验证：平台相关的机制怎么在「CI 绿但其实跳过」的情况下真验（实测环境矩阵 + 交叉编译流程）| 活文档 |
> | [docs/CHECKPOINT.md](docs/CHECKPOINT.md) | 续跑契约的设计推导、三条不变量与跨进程闸 | 活文档 |
> | [docs/BENCH-BASELINE-20261113.md](docs/BENCH-BASELINE-20261113.md) | **性能基线数字的唯一口径来源**：五个基准包的口径 / 采集方法 / 演进纪律（`make bench` 两处引用） | 活文档（机器判定）|
| [docs/ENGINEERING-HISTORY.md](docs/ENGINEERING-HISTORY.md) | 「工程底座」表**九轮校准**的过程与账：为什么它漂过、后来怎么接进门禁 | **历史快照**（从 README 下沉，2026-11-11）|
> | [docs/SPLIT_PLAN.md](docs/SPLIT_PLAN.md) | 从宿主仓拆分的分层依据（L1/L2/L3）| **历史快照**（2026-10-17 写于 10 个包时）|
> | [docs/MIGRATION_OAGENTKIT_20261018.md](docs/MIGRATION_OAGENTKIT_20261018.md) | 门面迁移与 errors 链路改写的执行记录 | **历史快照**（已于 2026-10-18 执行完毕）|
> | [docs/SELF-AUDIT-20261018.md](docs/SELF-AUDIT-20261018.md) | 独立复核发现并修复的缺陷 | **历史快照**（2026-10-18 收尾）|

## 边界：本仓有什么、没有什么

```
✓ 有（机制，与领域无关）        ✗ 没有（资产，属于宿主/领域包）
─────────────────────────      ──────────────────────────────
protocol   统一报告协议          专家包 / 专家清单 / 技能
           宽容解析 + 结构化校验  诊断链 / 编排图（faultChain 等）
           白名单校验            运维词表（巡检域、K8s 词面…）
           Markdown 渲染         目标类型（主机 / 集群 / Pod）
contract   编排运行契约（唯一边界） 宿主状态的落库方式与并发约束
statemachine 环节状态机          审批风险分级（restart/delete 四档）
eventbus   进程内有损广播        拓扑内容（谁允许派发谁）
tooldisclose 渐进式工具披露 + 调用门禁    工具实现（MCP server）
guardset   守卫装配与拒绝合成      具体守卫实现（披露门/出站门/审计门）
ptc        程序化工具编排沙箱    环节语义（收口/幽灵结果的具体判据属宿主）
watchdog   工具调用配对监视      工具描述文案
checkpoint 编排续跑契约          快照的存储介质（SQLite / PG / 文件）
commitgate 环节终态独占提交闸    并行分支的冲突裁决（该归宿主还是机制）
delta      增量节流 + 标签分流   模型供应商路由
sideeffect 副作用至多一次契约   幂等键的存储实现（Redis / PG / etcd）
sidetest  副作用闸契约测试套件   存储实现的并发正确性（原子性 / 跨进程）
retryplan 重试处置决策          幂等键的具体存储（内核只给路径，不给实现）
```

判定口诀：**「这个包需要知道自己在给谁干活吗？」需要 → 属于宿主。**

<div align="center">
<svg width="760" viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="12" y="12" width="736" height="276" rx="10" fill="#1c2330" stroke="#262e3d"/>
<rect x="30" y="36" width="300" height="200" rx="8" fill="#161b26" stroke="#58a6ff"/>
<text x="48" y="64" font-size="14" fill="#58a6ff">内核（本仓）＝机制面</text>
<text x="48" y="98" font-size="12" fill="#e6edf3">判定语义：状态词表 / 迁移 / 档位</text>
<text x="48" y="124" font-size="12" fill="#e6edf3">契约面：执行 / 交接 / 所有权</text>
<text x="48" y="150" font-size="12" fill="#e6edf3">门禁：依赖方向 / 结构阈值</text>
<text x="48" y="176" font-size="12" fill="#e6edf3">工具判定环：披露 / 合成 / 分类</text>
<text x="48" y="208" font-size="12" fill="#9aa7b8">不认识：这套机制在给谁干活</text>
<rect x="430" y="36" width="300" height="200" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="448" y="64" font-size="14" fill="#a371f7">宿主 ＝ 资产面</text>
<text x="448" y="98" font-size="12" fill="#e6edf3">拓扑内容：谁允许派发谁</text>
<text x="448" y="124" font-size="12" fill="#e6edf3">存储实现：快照 / 幂等键落哪</text>
<text x="448" y="150" font-size="12" fill="#e6edf3">展示文案：报告长什么样</text>
<text x="448" y="176" font-size="12" fill="#e6edf3">领域判据：巡检 / 审批 / 成本</text>
<text x="448" y="208" font-size="12" fill="#9aa7b8">领域词表：进内核即判红</text>
<line x1="380" y1="36" x2="380" y2="236" stroke="#262e3d" stroke-dasharray="4 4"/>
<rect x="30" y="250" width="700" height="28" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="380" y="269" font-size="12.5" text-anchor="middle" fill="#e6edf3">判定口诀：这个包需要知道自己在给谁干活吗？需要 → 属于宿主</text>
</svg>
</div>
<div align="center"><em>图 1 · 内核只放机制面：判定语义归内核，判定依据与展示文案归宿主。</em></div>

## 包清单

| 包 | 生产行数 | 外部依赖 | 职责 |
|---|---|---|---|
| `protocol` | 2341 | oekit/pkg、jsonschema | Report 契约 + 五级容错解析 + 白名单校验 + Markdown 渲染 |
| `scheduling` | 793 | oekit/pkg | Agent 调度的**判据面**（优先级 / 加权公平 / 资源配额 / 背压，issue #108）：归属结论见 ADR-0009——上游 `oagentkit/agent/dispatch` 的全部判定面是 allow 矩阵 + 深度 + 自派发三条（证据见 ADR 的「证据」节），四样判据上游确实没有，落本仓**纯函数面**：优先级排序**平局有名字层裁决且不依赖输入次序**（一次无关的配置重排不得换掉执行次序）、加权公平按 `served_i/w_i` 欠账最小者先服务（同状态重建选择序列逐字相同）、配额账本**整单占用不做部分占用 + 超释放显式拒**（钳位会把重复释放吞成静默）、背压裁决**拒绝必须带机器可判的原因**且在途满优先于队列满（容量问题的处置是扩容/收敛，拥塞问题的处置是退避，先报哪条决定宿主去找谁）。**不做队列不做执行**（宿主的活）、**不判可达性**（上游 `oagentkit/agent/dispatch` 的活）——不与上游并存第二份实现；上游若收编某样，对应判据交回上游（ADR-0009 写明后续动作）。**多副本全局配额原语 `QuotaStore`**（issue #189）：Reserve/Release 在存储侧原子扣减、凭租约全额归账（重复释放显式拒，余额不虚增）；`MemQuotaStore` 是进程内参考实现，跨进程落库归宿主 |
| `eventbus` | 1424 | oekit/concurrency、oekit/logx、oekit/pkg | 进程内有损广播（缓冲 chan）；可选 durable 发布面 `DurablePublisher` 收口「落库取游标→广播、失败不广播」（#160，Store 由宿主实现，包零存储依赖）；可选 event-tail 补灌面 `Tailer` 收口「初始化 MaxID → 轮询 id>游标 → 回灌 Hub → 推进」（#166，读侧对偶，TailStore 契约含「ID 序 == 提交可见序」的**无空洞推进硬要求**（#183，DB 序列下迟到提交的小 ID 不得被跳过）、读失败游标不动幂等补读、可选 `CursorStore` 游标持久化接缝（重启从上次停点续随，停机窗口内事件仍被补灌）与 `WithTailOnGap` 空洞哨兵（跳号从静默丢帧变响亮告警）；**scope:replica 每副本一条，不套 leader 门**——套了非 leader 副本就收不到他副本事件；重复帧不归它去重，归订阅侧游标）。跨实例面在 `Relay` 接缝之后（#145）：缺省无 Relay = 进程内限定，装配后 Publish 本地先/Relay 后/解锁后调用，别处事件经 `Inject` 回注（不回流防环） |
可选 durable 发布面 `DurablePublisher` 收口「落库取游标→广播、失败不广播」（#160，Store 由宿主实现，包零存储依赖）；可选 event-tail 补灌面 `Tailer` 收口「初始化 MaxID → 轮询 id>游标 → 回灌 Hub → 推进」（#166，读侧对偶，TailStore 契约含「ID 序 == 提交可见序」的**无空洞推进硬要求**（#183，DB 序列下迟到提交的小 ID 不得被跳过）、读失败游标不动幂等补读、可选 `CursorStore` 游标持久化接缝（重启从上次停点续随，停机窗口内事件仍被补灌）与 `WithTailOnGap` 空洞哨兵（跳号从静默丢帧变响亮告警）；**scope:replica 每副本一条，不套 leader 门**——套了非 leader 副本就收不到他副本事件；重复帧不归它去重，归订阅侧游标）。跨实例面在 `Relay` 接缝之后（#145）：缺省无 Relay = 进程内限定，装配后 Publish 本地先/Relay 后/解锁后调用，别处事件经 `Inject` 回注（不回流防环） |
| `dispatch` | 380 | oagentkit/guardx、oagentkit/toolgate | 改派失败面（issue #169）：三档分类词表（transient 可跨域兜底 / fatal 换谁白跑直接终态 / other 仅同域重试）+ **两级判定**（第一级消费上游 `toolgate/policy.Disposition` 单源——toolfail 链 → MCP 协议层，零文本匹配；链与协议层都 Unknown 才落文本兜底，状态码判定带 `\b` 词边界：「省略中段 4032 字节」不得被裸 `Contains("403")` 命中）+ 改派环骨架（同域 N 次 → 瞬态跨域兜底一次 → fatal 直接终态；候选枚举 `FailoverCandidates(slug, tried, sameDomain)` 与执行步 `Attempt` 经回调注入，与宿主注册表/能力画像无关）+ 四级数据化策略解析（条目 > 表级 > conf > 代码缺省，指针区分「未声明」与「显式 0/false」）。**判定面只读首次失败的原始错误**——改派说明 `%w（已同域改派 X 仍失败）` 只进事件与最终消息。回答的是第四问「失败后还能不能再派、派给谁」：不碰可达性（上游 allow 矩阵/深度/自派发）、不做队列（`scheduling`）、不答「要不要原样重发」（`toolfail`） |
| `statemachine` | 305 | oekit/pkg | 环节状态机：条件迁移的纯函数件（错误三态：非法迁移 / 状态不在词表；`StepType.Valid` + 词表外审批保守侧）；环节状态与类别各有**枚举出口**（`AllStepStatuses()` / `AllStepTypes()`），出口与 `Valid()` 读同一张词表 |
| `contract` | 362 | oekit/pkg | 编排运行契约：编排引擎 ↔ 宿主状态的**唯一边界**（纯接口面，零实现）；状态词表**单源**：`AllStatuses()` 枚举、`IsKnownStatus()` 判成员、`ClassifyStatus()` 给三态（过程 / 终态 / 读不懂），会话轴与环节轴共用一份判据 |
| `checkpoint` | 1268 | oekit/pkg | 编排续跑契约：进度快照存取边界 + **「续跑而非重放」**的恢复判定（空洞不采信 / 终态不自动重开 / 序号单调闸）+ **跨进程 CAS 存取契约**（`StoreCAS.SaveIfNewer`；**v0.33.0 起序号为 0 的快照一律拒收**——它不是一次真实提交，而放它落库等于给了它唯一能覆盖既有快照的写）与提交序列固化（`Committer`：抢序号→落库→**仅失败时**回滚，宿主无法漏掉）+ **依赖声明**（`StepDef.DependsOn` / `Isolated`）：并行 fan-out 的合法乱序完成按各自前驱采信，不再被「连续前缀」整批作废；零值 = 旧口径逐字不变|
| `commitgate` | 484 | oekit/logx、oekit/pkg | 环节终态独占提交闸：同一 (run, 环节) 的终态只允许被提交一次（`statemachine` 是纯谓词，`SeqGate` 只排快照序号，两者都排不出「两条分支都赢了」）。终态词表由宿主注入。**放闸靠凭证**：`Acquire` 返回唯一令牌、`ReleaseLease` 凭令牌精确释放——靠 (状态,分支,序号) 认人的旧 `Release` 分辨不出两次同名同序的占用，迟到的释放会放掉别人的闸。状态在 `Store` 接缝之后（#145）：判定留 Guard、**令牌由存储铸造**（跨副本不撞），进程内参考实现 `NewMemoryStore` 不得上生产。回灌面 fail-loud（#204）：`SeedE` 对 Store 故障响亮上抛（吞掉 = 闸为空、重复终态提交重新敞开），Deprecated `Seed` 走 `SeedWarn` 钩子/logx warn 留痕 |
| `tooldisclose` | 959 | oagentkit/guardx、oagentkit/toolname、oekit/pkg | 渐进式工具披露（确定性打分检索换完整 schema）+ 执行门禁（**未加载可重试 / 不在目录禁止绕过** 两类严格二分，**拒绝不激活**——门禁只读已加载态，`Catalog.Load` 是唯一写入口；schema 未取回前原样重试不会成功（issue #286））+ 接成 `invoke` 链的一环（`Stage`，链上 Reason 直接回面向模型的指引）|
| `sideeffect` | 1360 | oekit/pkg | 副作用「至多一次」的跨进程契约：**幂等键**（同一逻辑动作重试不重复施加）+ **租约**（同一资源独占，带 TTL）+ **栅栏令牌**（过期持有者的迟到写入被下游拒绝）+ **下游拒收契约**（`FencedSink`：比较与写入须同一次原子操作，否则两个持有者会同时读到同一个旧值、双双通过检查，更旧的那次写入反而后落覆盖新的）。上游三个判据共用**一次原子操作**（`Store.Acquire`）——拆成三份存储就会出现「租约过了但幂等没过」的夹缝，允许同一动作被施加两次。执行失败**不记账**（没做成的不能记成做成了），但**释放闸**（否则失败残留的闸会把后续重试全挡成「别人占着」）。**v0.32.0 起施加失败且无法判定远端是否已生效 ⇒ 结果未知**（`OutcomeUncertain`）：该 Key **禁止重试**，必须先对账（`Reconcile`）——「响应丢了」与「远端没做」在调用方看来完全一样，而前者重试就是**重复施加**；执行器同时拿到**栅栏令牌**（`lease.Fence`）好把陈旧写拒在下游。**施加前有资格检查**（`ExecuteWith` 两段式：准备 → 检查 → 施加）——闸已易主时**根本不施加**，因为 Complete 被拒只说明没记上账，**副作用早已生效，事后补救不了**。承诺的是**至多一次**而非 exactly-once——后者要求下游也接受幂等键。**v0.33.0 起补上「执行意图」**：`Acquire` 原子地登记「本次尝试还在」（Key 级 IN_FLIGHT），于是**进程死在施加成功之后、记账之前**这一形态在存储侧留有痕迹——租约过期且未裁决时，同一 Key 转入**需对账态**（`OutcomeUncertain`）而**不是**重新授予执行权（声明了 `RetrySafetySafe` 的除外：下游按幂等键去重，重做安全）。配套把两条出口拆开：`Abandon`（确定没生效 ⇒ 撤销意图 + 放闸）与 `Release`（**只**放闸，不动意图）——「可能已生效」那条路若顺手撤销意图，重复施加的入口就重新打开 |
| `invocation` | 402 | oekit/pkg | 工具调用账本（Tool Invocation Journal）的**跨进程契约**：调用身份（callID + 工具名 + 参数摘要）**一经落账不可变** + 状态机（planned → approved → executing → applied → result_recorded，另有 rejected / failed / uncertain）+ **「未完成是合法形态」**的恢复处置判据（executing ⇒ 对账、applied ⇒ 回填结果而不是重做）。补的是 sideeffect 补不了的那一半：闸能保「同一逻辑动作至多施加一次」，但前提是调用方**记得住那个键**——进程崩溃后模型会重新生成调用 ⇒ 键变 ⇒ 同一业务动作再施加一遍。账本把调用身份在**执行外部工具之前**持久化，恢复时按账本续跑，**不再问模型要那次调用**。两者合成才闭合崩溃窗口：账本保身份、闸保至多一次；只有闸会重复施加，只有账本会盲目重做。零内部依赖（叶子层）——宿主实现 Store 接口落库，判据归内核；存储侧原子推进的地基是 `Predecessors`（能合法转移到某状态的前驱集）——宿主拿它拼「一条带条件的 UPDATE：WHERE state IN (前驱集)」，不必自己反推转移表（反推就是第二份真相）；共享契约套件在 `invocation/invocationtest` |
| `sidetest` | 1911 | oekit/concurrency、oekit/pkg | sideeffect.Store 的**共享契约测试套件**：宿主写完存储实现后跑一遍，违反哪条契约就红在哪一条——免得「各写一遍必然各判一套，漂移到没人发现为止」。**34 条子测试**覆盖「只做一次 / 已完成优先于占用 / 未过期时争用 / 过期后接管 / 令牌单调 / 过期持有者不得记账 / 过期持有者不得放闸 / 释放不等于完成 / 续期延长且不换令牌 / 闸已易主后续期被拒 / **闸已释放后续期被拒**（只比令牌会漏掉的那条）/ **过期且无人接管后续期被拒**（过期即资格终止，与 taskboard 同口径）」，外加「结果未知」一族五条（未知态禁止再执行 / 裁决未生效允许重试 / 裁决已生效记结果 / 裁决先到者为准 / 过期栅栏不得转未知）与「**执行意图**」一族六条（未裁决不得重新授予执行权 / 过期未裁决转入需对账态 / 声明可安全重试则过期后可重授 / 显式放弃后重新可执行 / **只放闸不撤销意图** / 放弃只认本次尝试的令牌）、「**持有者身份**」一族三条（身份由存储铸造 / **偷到令牌的组件冒充持有者被拒** / 跨资源凭据被拒）、「**标记失败**」一族一条（**标记写失败后不得授予执行权**，套件内置故障注入装饰器）、「**重试安全三档**」一族两条（**Unsafe 裁决未生效仍阻塞** / 显式决策才放行）与「**对账占用**」一族四条（占用挡住第二个对账者 / 占用过期可重入且不放行 / **带凭据的裁决只认当前有效占用**（迟到的对账者提交被拒，OwnedResolveStore）/ **认领 TTL 必须为正**）以及「**动作身份**」一族一条（四项断言：改 RetrySafety / 改 Resource / 改 Payload 被拒为 `ErrActionConflict` / 已完成后改身份不重放旧结果——身份首次登记后不可变）。范式沿用上游 `cachetest` / `singleflighttest`：**硬失败不静默跳过**（显式接入套件＝声明要满足契约，跳过等于把「没测过」伪装成「通过了」）。时钟可注入（`ClockedStore`），否则过期场景恒不触发，套件会变成一组空断言。另有**下游栅栏**一族六条在 `RunSinkContract`（v0.33.0 起，issue #84：新令牌接受 / **等于也接受** / 陈旧拒收且零写入 / 新先旧后保最大 / 并发陈旧被拒 / **写入体失败不部分写**）——判据收编自 sideeffect 包内对内存替身的用例（只有一份），内存替身与 PostgreSQL 参考实现跑同一段断言（参考实现自 v0.67.0 起在 goagent-runtime，同跑这段判据）；并发场景走 `async.GoSafe`（本仓禁止裸 goroutine） |
| `egress` | 504 | oagentkit/guardx、oekit/pkg | 出口策略：**三档**（`ModeUnrestricted` < `ModeProxy` < `ModeDenied`，单调可比、零值即最松）+ 声明自检（`Validate`）+ 兑现判据（`Env` / `Verify`）+ 后端能力问询（`SatisfiedBy`）+ **兑现强度**（`VerifyVerdict` / `SatisfiedVerdict`：**环境变量兑现出来的永远是建议型**；**v0.33.0 起自报与实测分开**——后端只是**声明**能力（`Enforcer`）⇒ 强度是 `declared`，**不是** `enforced`；只有后端能**实测**（`EgressVerifier`）并给出证据时才判 `enforced`——「代理档已兑现」不等于「出站已被强制隔离」，而后端自报恰恰是最容易被读成事实的那种声明）。本包**不含代理本体**（那是上游 `guardx` 与宿主的活），只回答此前没人回答的三件事：这次执行要求哪一档出口 / 宿主给的环境是否**真的**兑现了那一档 / 某个沙箱后端能不能兑现它。两条底线都是「沉默与伪造都落在安全的那一侧」：**后端不声明就按最弱处理**（不是「没说不支持」＝满足，否则从没实现过出口控制的后端会被选中，声明与实际之间的落差没有信号）；**禁网档拒绝伪造**（`ModeDenied` 的 `Env`/`Verify` 直接报错，不做「尽力而为」——假装禁网比明确拒绝更危险，报告会显示「已兑现」而网络其实通着） |
| `gatecheck` | 599 | oagentkit/guardx、oagentkit/toolgate、oekit/pkg | 审批层与沙箱层的**联合判据**：Codex 的两层安全模型是「沙箱强制 + 审批策略，**两层都同意才放行**」。上游 `guardx/sandbox`（档位×预算）与 `toolgate/policy`（审批裁决）各自完整但**互不知情**——「两层是否都同意」这个判定没有单点，落在各宿主手里必然漂移。本包只做**合成**（不重新实现任何一层，那会造成与上游并存的第二份真相）。判据顺序有意为之：**审批已否 ⇒ 直接否**（档位再高也不该执行）；**需人审 ⇒ 优先于档位不足**（人该在场，而不是让宿主去找后端——否则这次调用既没执行也没人管，**静默丢失**）；**计划模式单列**（只审不执行 ≠ 拒绝）。`Managed` 是**受管后端**（把真后端包起来）：判据不通过就**不委派**（副作用不会发生）——判据与执行**不可分**，否则「判过的后端 ≠ 实际跑的后端」又有了信号落差 |
| `preset` | 230 | oagentkit/guardx、oagentkit/toolgate、oekit/pkg | 命名权限预设：把「隔离档位 × 审批模式 × 出口策略」三件散参数收成一份**具名、只读、加载期自检**的组合（`Preset{Name,Level,Mode,Egress,Unsafe,Justification}`）。散参数的病不是难写，是**改一处漏两处**——档位调到 OS 级而出口忘开代理、审批放到 auto 而沙箱仍是进程内，组合出来的权限谁都没声明过。自检两条最关键：**三面全松必须显式 `Unsafe` 且写理由**（「全都放开」最容易在调试时临时出现、然后留下来）；**语义相同的两个名字即报错**——两个名字一份组合在配置里看起来是「两个选择」，真正的伤害在留痕：报告里出现两个名字，读者会以为执行过两种权限。`Resolve` 遇未登记名字**报错而不降级**（降级方向不可预测，可能更松，而「打错一个字就用上另一档权限」没有任何信号）。方向是 preset → gatecheck（高层组装件认识低层判据），gatecheck 不认识 preset |
| `retryplan` | 262 | oagentkit/guardx | 重试处置决策：把「该不该重试」（`toolfail` 判）与「重试会不会重复施加」（`sideeffect` 判）合成**一条可执行结论**——三档路径（不重试 / 原样重发 / 走幂等键重放）。与 `guardset` 同族：**组合逻辑归内核，判定归各自机制**（各宿主各写一遍组合逻辑，必然各判一套）。关键一档是「可重试但原样重发不安全」——`toolfail` 只说「先确认上一次是否已生效」，**没说什么怎么做到**，而这正是幂等键的活；故该档判为 `PathReplayed`（走幂等键）。关键取舍：**没有幂等键就不许「猜着重试」**（降级为不重试，而非原样重发赌一次）。分类器可注入（`Options.Classifier`，缺省 = 现有 `toolfail` 口径，行为零变化；issue #240） |
| `delta` | 253 | oagentkit/runtime | 增量节流：分片合并 + 权威整段优先 + 确定性收口扫序 |
| `drain` | 320 | oekit/logx、oekit/pkg | 优雅排水：轮 ctx 取消脱钩 + 等在途收敛 + 预算耗尽强制收刀（经 `Turns` 接口）|
| `deadline` | 478 | oekit/pkg | 会话级墙钟时限（issue #170）：锚点三态（未写/有效/过期）判定纯函数 + **shared 锚点首轮认领后写一次、续跑轮沿用**（总时限不因续跑轮重置——每次续跑都新开一段时限的话会话可以无限续跑）+ 轮 ctx 经 `WithTimeoutCause` 携带专属 `ErrSessionDeadline`，与用户取消/进程停机**三分**（`Route` 的 **timed_out 臂必须先于停机臂**：墙钟到点的 ctx 同样 `ctx.Err() != nil`，次序反了每次超时都被判成停机、不落终态，会话永久滞留 running）+ 过期锚点的续跑轮在认领迁移后**立即 timed_out 收敛、不进编排**。锚点持久化经注入 `Store`（首轮写一次、续跑轮读；未注入 fail-closed——静默跳过会让总时限被静默重置）。词表缺省消费 `contract.StatusTimedOut`（超时的产出不可信 = deny-by-default），宿主经 `StatusOf`/`StatusWith` 注入自有状态词表（缺省 = contract 词表，行为零变化；issue #237）；与 `drain.ErrDeadline` 是两个 Cause、两根轴 |
| `reconcile` | 780 | oekit/pkg | 启动孤儿对账与心跳租约死主回收（issue #165）：**三路判定 + 一套收敛体**——boot-anchor（进程启动即替代同 instanceID 的上一具尸体，名下 running 且最后事件早于锚点定义性是孤儿，**无须等事件静默**）/ event-silence（本副本 10m 静默；本副本不适用心跳免检，自副本走静默恰是重启孤儿的安全网）/ heartbeat-lease（他副本租约过期**不等静默立即收**、心跳新鲜免检、无心跳行回落 30m 静默），30s 宽限窗；收敛 deny-by-default 落 failed（不重放任何步骤）+ closed_by 四路归因（boot-reconcile/recovery/dead-owner/dead-lease）。**与 drain 成对**（drain 两处「留启动对账」的空缺即本包：计划内 vs 非计划死）；状态词表复用 contract、收尾事件走 eventbus，存储事实全经 Store 接缝（`ListRunning`/`LastActivity`/`ListBeats`/`ConvergeCAS`/`CloseBracket`），包零 DB 依赖；时钟可注入（租约/静默边界可确定性测）。**终态事件与结算同事务（#272）**：可选 `FinalizeStore` 把终态写入与终态事件 Outbox 行放在同一事务，`FlushFinalizeOutbox` 投递成功才标记、失败保留并跨重启幂等补做（稳定 Event.ID）；旧 Store 路径由 `FinalizeGuarantee` 显式报告非原子/未确认投递（先复现用例钉住旧路径的分裂）。**误收面如实声明**：双进程重叠窗口的静默长步骤有误收面（常规路有 10m 缓冲；单副本杀旧起新无重叠不受影响），`BootGrace` 正值加宽、负值整体关闭 |
| `finalize` | 344 | oekit/logx、oekit/pkg | 收尾漏斗（issue #167）：一轮编排中止时「该落哪个终态/该不落终态」的唯一收口——`Route` Cause→终态**四分路由**（接管不落终态回 created / 用户取消 cancelled / 会话超时 timed_out **先于**停机分支 / 进程停机不落终态留给恢复循环，其余 failed）+ `Converge` **多起点条件迁移**（running/waiting_approval 双起点一次原子迁移——挂起期写丢失时只认 running 起点会永不入终态）+ `AdjudicateRetry` 失败臂排队重试裁决。三条并发纪律是包级不变量：① 终态写一律走 `Detached`（编排 ctx 已取消时直查中止 ⇒ 会话滞留 running 而事件谎报）；② 条件迁移零行 = 他路已接手，不再写任何状态；③ 零行回读库内现终态补发事件**不谎报**。gorm/model 不下沉：条件迁移/回读/发事件全走 `Store` 注入接口；报告文案、needs_review、排队结算留宿主回调。接管臂载荷在 `steer`（单向依赖 finalize → steer，不许环）|
| `steer` | 336 | oekit/logx、oekit/pkg | 运行中接管（issue #168）：用户运行中注入新输入的协作中止与保产续跑五件套——① `ErrSteered` + `Signal`/`IsSteered` 协作中止 Cause（与用户取消/停机三分可辨）；② `BuildSummary` 保产摘要**机械拼接不调 LLM**（只计成功环节，读取失败走降级正文——收尾在脱离 ctx 里跑，须快且不引入新失败面）；③ 不落终态回可认领态；④ `Decide` 队列感知（有排队条目才回 created，否则按失败收敛**不开空轮**）；⑤ `Run` 条件迁移**单协程执行**（无双跑窗口，并发投喂恰有一个赢家、输家零行静默）。判据只依赖注入事实（环节清单/队列非空/迁移结果），零领域词；与 handoff（跨进程负责人改写）/ taskboard（租约过期被动接管）/ drain（停机排水）同词不同域，包头互指 |
| `ptc` | 3116 | dop251/goja、oagentkit/guardx、oagentkit/runtime、oagentkit/toolname、oekit/concurrency、oekit/logx、oekit/pkg | 程序化工具编排：模型写程序 → 沙箱 VM 执行 → 经 `bindings` 调宿主工具；三重预算（墙钟/调用数/字节数）替代 OS 沙盘。**N 轮 ReAct 往返塌缩成 1 轮，原始数据不过模型面**；绑定可带 Signature 让模型按位置传参（可选形参以 `?` 标记、未传则不出现在 JSON 里；形态里的函数名必须是**净化后**的名字，规则见 oagentkit 的 `toolname`（已归位））；**程序内的 host.<tool>() 与普通工具调用走同一套 Stage 判定环**（不做这一步，本包就成了整条链上唯一绕过所有门禁的路径）；**v0.32.0 起缺省即拒绝**：装了工具绑定却没装判定环 ⇒ 拒绝执行（`ErrorKind=unmanaged`），裸执行必须显式声明逃逸口（生产代码里由门禁拦下）；绑定名净化后撞名即装配期拒（否则后绑者顶替前绑者，副作用错位无信号）。**新增子进程后端**（`ptc-subprocess`）：把执行搬出宿主进程，用 **cgroup v2 的 `memory.max`** 兑现 `MaxMemory`（限**物理内存**；`RLIMIT_AS` 限的是虚拟地址空间，会把 Go runtime 一起掐死——已实测否掉）——进程内形态兜不住内存（Go 运行时没有「只给一个 VM 限内存」的开关，goja 公开 API 也没有内存限制），故子进程形态**如实声明**自己兑现这一项（`sandbox.Capable`），**建不出即拒绝执行**（fail closed），兜不住的后端在登记簿里不会被选中 |
| `backend` | 579 | oagentkit/guardx、oekit/pkg | 任务式执行后端的**契约面**（issue #109）：隔离轴（none/process/container/vm——上游 `Level` 词表的枚举值即强弱序，往中间插容器档等于跨仓破坏性变更，故本包立自己的轴，与 `sandbox.Level` 的对应关系**只写在注释里、不写进代码**——代码级映射是第二真相）。三条判据与既有执行面同源：能力**自报**（`EgressEnforcer` / `Capable` 与 sandbox 同构同词表，**沉默按最弱**——未声明出站能力面的后端连「我能禁网」都说不出口）、要求对照 **fail-closed**（`Spec.SatisfiedBy`：词表外档位不是「更强」而是声明缺陷；要求内存必须实现 `Capable` 且自报兜得住）、登记簿 **无满足者即拒绝不降级 / 满足者取最弱、同档按 ID 字典序**（与登记顺序无关，一次无关的配置重排不会换掉执行方）。进程内兜底 `Inline` **显式自报 IsolationNone**（零值=最弱档是刻意的：漏声明的后端拿到的是最弱选择，不是最强豁免）。Docker Engine 与 Kubernetes Job 两份参考实现已迁至 goagent-runtime 仓的 containerbackend（宿主抄它、不 import 它） |
| `watchdog` | 351 | 无| 被动配对不变量监视：INV1/INV2 工具调用级（**配对键与 trajectory 同源：`eventbus.ToolCallID` 的全局 call_id，不含 step**——结果可跨环节到达、同号声明可跨环节重试；INV1 的判定时刻是会话收口或 TTL 清扫，不是环节收口；缺口按 declared_not_executed / unpaired_result / duplicate_result / duplicate_call 告警，无 call_id 不记缺口），INV3 轮级括号（`turn_started`/`turn_ended` 配对 + `closed_by` 归因词表 + contract 终态 status + 开放超 TTL 泄漏；停机留开放=合法态）；**只告警不裁决**（热路径裁决在守卫侧）|
| `guardset` | 1027 | oagentkit/guardx、oekit/pkg | 守卫装配契约：一组**相互独立**的判定一并求值，多条拒绝合成一条（按 Rank 定序、**不可重试优先**的保守裁决、指引指向真正卡住的那一条）。接成 `invoke` 链上单独一环——链在第一个拒绝处就收口，后面的守卫永远没机会说话，模型一次只听得到一个理由，这正是本包补的那一面；链本身的顺序/留痕/panic 兜底沿用上游，不重复实现。**装配面 `Assembly` 提供「撤销」的机械路径**：撤销 = **换整份**，不是改其中一条——改其中一条会让「当前有哪些守卫」出现两个真相；换份走 **CAS + mutate(旧值)**（并发撤销**不丢更新**，否则「撤销被撤销」）、代次住在被换的那份里（全局代次是投影）、链不用重接、旧份不被修改、在途判定跑在旧份上；「全部撤销」不是降级是失守（换上去的那份永远非空） |
| `archcheck` | 13216 | gopkg.in/yaml.v3、x/tools | 架构门禁执行体：`architecture-policy.yaml` 的**可执行面**（规则判定 / **门包规则（清单已清零、规则保留）** / 依赖方向判定 / policy 自校验 / 结构阈值）。**可导入包**——宿主仓复用同一套判据，不必各写一遍（各写一遍必然各判一套，漂移到没人发现为止）。判据与执行体分离：结构性判定一律走 `go/ast`（import 声明有六种以上书写形态，逐行正则必然漏）。依赖方向判三条：越界内部依赖 / 越界跨仓依赖（`ext:` 登记）/ 未登记包目录。**执行沙箱后端的档位登记**（`sandbox_providers` 段 ↔ 代码事实，走 `go/ast` 按方法指纹认出后端实现，核对档位漂移与未登记）。**租户过滤漏拼门**（`tenant_gate` 段）：服务层「授权上下文形参 + 租户域返回」必须有隔离证据（共享判定面 / 过滤列 / `tenant:iso` 声明），登记在册的共享判定面必须真实存在；形状全由 policy 声明，租户类型由代码推导。**陈旧包根当天即红**——root 指向空目录即「声明与事实已偏离」，而没有执行体时这句话没人读 |
| `raceprobe` | 226 | 无 | race 门禁失败形态的**判定层**（台账 §26 建议 1 下沉）：六种形态分类（`none`/`data-race`/`test-hang`/`startup-wedge`/`test-failure`/`unknown`）+ 结论结构 `Verdict`，**纯函数**——输入一段输出文本、输出一个结论，故「哪种输出算哪种形态」能用构造出来的文本逐条钉死，不必真跑一场 11 分钟的挂死。判据是「**栈在不在**」而不是「超时了没有」：用例挂死 ⇒ Go 运行时活到过 ⇒ 栈打得出来；启动期楔死 ⇒ 运行时压根没起来 ⇒ 没栈（2026-11-18 那次 11m0s 就是被「超时」症状归错的）。**从 `cmd/raceprobe` 下沉为可导入包**——判定面与执行面分离，本仓别的门禁（fault-injection 等也可能撞上「会挂死」的形态）与宿主复用同一份判据（本仓老教训：机制面若要跨仓复用，就不能锁在 `package main` 里） |
| `trajectory` | 1535 | 无 | 轨迹投影器：**事件流 → 一轮 turn 的步骤序列**（增量投影：喂新事件继续更新既有行）。行含 kind / 起止时刻 / **实测耗时**（起止相减现算）/ 工具调用**配对状态**（paired / missing_result / duplicate_result / unpaired_result / duplicate_call / no_call_id）/ 摘要；**无 call_id 的调用/结果按到达顺序归并、不记缺口**。⭐ **结构上不设时长字段**——时长唯一来源是起止相减，**估算值无处安放**（没有实测就是没有实测，不拿 payload 体积之类糊过去）；配对缺口语义分两层：`missing_result`/`unpaired_result` 是**当前状态**（等到了会被清掉），`duplicate_*` 是**发生过**（永久留在清单里）。事件倒挂（结果先于调用）按「就地补起点」处理且此时判为无实测值。与 `watchdog` 分工：watchdog 判**不变量**（只告警），本包判**这一轮的流水**（给宿主渲染）。另有 **OTel 映射契约**（`otelmap.go`）：每个属性写明来源字段 / 取值域（含 Go 类型）/ 基数上界 / 能否进 metric label，`Span.ParentID` 声明父子，**trace 边界定在轮**（`Row.ID` 的唯一性范围恰好就是轮内）；无界看似维度的字段（`session_id`/`agent_slug`）列进禁区清单。**v0.33.0 起覆盖 `sideeffect` / `checkpoint` 行**（副作用阶段与快照序号，属性命名与既有类目同构；点事件落孤儿行——没有实测时长就不写时长） |
| `eval` | 491 | oekit/pkg | 轨迹的**录制 → 重放 → 差异比对**与**确定性评估**（issue #111）：锚点是 `trajectory` 的确定性行 ID（同一条事件流投出的行 ID 必然相同，「同一条轨迹」由此可谈）；**时刻是数据不是时钟**——起止时刻取自事件时刻、随录制定格，重放即复读，没有 sleep 也没有 now()。`Record`/`Load` 是朴素 JSONL（同一事件流两次录制逐字节相同，可 diff）；`Verify` 判**录制适航**（头部字段往返保真 + 载荷往返幂等：整数落 float64 是 JSON 固有形变，一轮即稳即可录，两轮还在变就显式报出）；`Diff` 纯函数、按 (ID, Field) 确定排序，空切片 = 复现（验收线：回放能复现已录制轨迹，差异为零）；`Assess` 是**纯函数评估判据**（closure / pairing / no_duplicates / expected / forbidden 五判项；checkpoint 点行的孤儿形态是投影设计事实，不误报；零判项显式报错——「没有检查」与「检查通过」必须分得开）。不跑模型不看像不像：**「像不像」要再跑一次模型，那是抽签不是评估** |

| `taskboard` | 795 | oekit/concurrency、oekit/pkg | 多 agent 协调面的**存储中立契约 + 一致性判据**：`dispatch` 只回答「能不能派」，派出去之后「谁负责哪个子任务、依赖谁、怎么还回去」此前没有任何机制面——各宿主自己拿一张表兜着，于是「同一子任务被两个 agent 同时认领」在报告里读起来完全正常（两条分支都干完了活，各自都以为自己是负责人）。本包**不给存储实现**（表结构/事务边界/持久化是宿主资产，给一份参考实现宿主就会照着改它，最后两份真相并存——与 `checkpoint.StoreCAS` 同一分工）。三条口径：① 认领必须**原子**由存储保证，本包只判结果（`ConcurrentClaim` 并发跑一遍，成功者必须恰好 1 个；`DuplicateTokenSubmit` 让同一令牌的并发提交者正面相撞，成功者同样必须恰好 1 个）；② 释放/完成**凭令牌不凭内容**（同名 agent 与「失败→释放→重试」都会让内容比对认错人）——**v0.33.0 起令牌由存储铸造**（`Claim` 改收「要多长的租约」的 `ClaimRequest`，令牌随返回的 `Task` 给出）：调用方自带令牌时「只认 token」名存实亡，它能造出任意 token，输家的迟到释放照样放掉赢家的闸；③ **failed 不是终态**（一次失败不能把子任务永久钉死，否则补救路径静默失效）。**v0.33.0 起带续期与接管代次**：`RenewingBoard.Renew` 是资格检查不是心跳（校验令牌仍是最新代、租约未过期，才把到期时刻推后）；`Lease.Fence` 与令牌同一次铸造、同一任务上严格递增——旧持有者迟到的提交凭「令牌不匹配 ⇒ ErrLeaseLost」必然被拒。**v0.76.0 起 `Complete` 与 `Renew` 同一口径**（issue #497）：资格在到期时刻终止——租约过期后、尚未被接管的窗口里旧持有者不得提交终态（终态内容不该取决于谁先提交），过期即 `ErrLeaseLost` 且不改状态；`Release` 不判有效期（归还是自愿放弃、不写终态）。`Check` 判快照不变量：id 唯一、依赖存在且**无环**、认领态与租约必须同时成立、依赖未完成不得认领、终态不带 owner、非认领态不残留租约/代次。**多租户（#195）**：`Task.Tenant` 让任务身份变成 `(Tenant, ID)`（空租户 = 默认命名空间，旧行为逐字不变），`Check` 按租户判重、依赖只在同租户内解析——租户进了身份，不再只是控制面判据。**可选批量领取面 `ReadyBoard.ClaimReady`**（issue #189）：一次至多领 n 个就绪任务，批内不重复、过期租约可接管、与单条 Claim 共用同一套原子认领判据；单条 Claim 仍是默认路径，语义不变 |
| `coord` | 2641 | oekit/concurrency、oekit/pkg | 分布式协调后端的**适配形态约定**（issue #107）：协调契约（`sideeffect.Store` / `taskboard.Board`）此前只有 PostgreSQL 一种参考适配（examples/pgstore）——「后端不止一种，且形态有约定」那一半是空的。本包把「一次原子操作 / 条件更新 / CAS 循环」收成**代码级约定**（`Backend` 四原语：一致快照读 / 断言全过才提交的条件事务 / **前缀扫描 `ScanSnapshot`**（#182，一次原子操作返回前缀下全部键与值——全量视图类操作的读面，`Keys`+`Snapshot` 两步拼是跨两个时刻的拼接不是快照）/ 前缀列键；版本断言取「版本相等」不取「值相等」——ABA 场景下值断言会误判「没动过」），`BoardAdapter` / `StoreAdapter` 把契约的全部判定逻辑（三判合一、三项联查、执行意图、对账占用、接管代次）落成原语之上的**单份适配器**：宿主接新后端的工作量从「重写 29 条契约语义」缩成「实现三个原语 + 让它们真的原子」。各后端的原子性边界写在包文档（Redis 单 Lua 内原子但脚本间无隔离、etcd txn 原子但吞吐低、PG 事务、内存单进程）。**内存后端是测试保底不是生产存储**（单进程互斥锁，跨进程原子性不存在——边界刻在类型文档里）；boardtest / sidetest 共享契约套件在适配器上真跑。**租户进键与领取条件（#195）**：`NewTenantBoardAdapter` 从 ctx 取租户生成 `coord/t/<escaped-tenant>/task/<id>`（独立根，不与旧 id 撞键），List/Claim/Release/Complete/Renew 全按租户过滤，缺租户 `tenant.ErrNoContext` fail-closed，`boardtest.RunTenant` 覆盖跨租户不可见/不可领（含并发）、同 id 互不覆盖与跨租户令牌拒收。**`BoardAdapter` 同时实现 `taskboard.ReadyBoard`**（issue #189）：ClaimReady 按快照挑就绪候选、逐笔复用 Claim 的原子判据——竞争性拒绝跳过、批量不重复分配。**租约判定—提交窗口（#271）**：可选 `AtomicDecision` 让后端在一次事务内取存储时刻+快照+判定+提交（窗口 0）；`LeasePrecision()` 报承诺精度/来源，`RenewWithVerdict()` 对非原子后端按 250ms 存储时钟预算给出窗口内过期/超预算的可解释事实（`LeaseVerdict` + Warning），装配期时间权威检查不变 |
| `skilldisclose` | 485 | oekit/pkg | 技能披露：**确定性**挑出该给模型看的那几个技能。与 `tooldisclose` 不是重复——那边的检索键是「工具调用需求」，而技能的触发信号有三种且**强度不同**：点名 > 资源命中 > 描述相关。混成一个分数会让「相关」压过「点名」，而那正是用户已经明确表达过的意图（故点名权重 1000，任何相关度信号之和都够不到）。判据三条：点名压过一切；同分按名字（不依赖目录遍历顺序与 map 顺序）；预算裁剪**显式**（`Truncated`，静默截断会让宿主以为「就这几个相关」）。空结果必须写明原因——「没有检查」与「检查通过」在调用方看起来一样，而前者是「模型不知道有这个能力」 |
| `hookengine` | 296 | oekit/pkg | Hooks 的**判定面**（不是传输面）：Codex 是「外部进程协议 + 规则文件」，DSH 是「动态插件包」，形态**已经分叉**——由内核选边等于把一种传输焊进契约，故只定三件跨形态恒定的事：**匹配**（事件 + 工具前缀）/ **裁决**（allow / deny）/ **顺序与收口**（按名字确定序，第一个拒绝即停）。传输与加载归宿主（`Runner` 是唯一接缝）。两条 fail-closed：阻塞 hook **执行失败 ⇒ deny**（跑不起来不等于没问题，否则一个写错的检查会静默变成空检查）；**超时裁决必须显式声明**（`OnTimeout`，缺省 deny）——没有缺省值时超时会被 ctx 取消掩盖成「这次没拒绝」。非阻塞 hook 不参与裁决但失败必须留痕：它一旦能影响裁决就只是「慢的阻塞 hook」，而宿主会以为它是旁路的 |
| `delegate` | 176 | oekit/pkg | 异构委托的**契约面**：ACP 一类东西同时是协议 + 传输，线格式归上游；本包只定跨传输恒定的三条。① 能力必须**子集**（做不到就拒，不许「尽力而为」——尽力而为的结果无法判定「没做到」与「本来就没要求」）；② **最小能力原则**选执行方：多个都能接时取能力**最少**者，同数量按名字——按顺序取第一个是错的，顺序通常来自配置文件书写次序，一次无关重排就会换掉执行方；③ 回执状态**三值**（completed / failed / refused），且 completed 不得带 Failure、failed 与 refused 必须带原因——把「被拒绝执行」混进「执行失败」会让委托方去修一个不存在的 bug |
| `handoff` | 1342 | oekit/concurrency、oekit/pkg | 跨进程任务交接的**契约面**（issue #110），交接四问各一条契约条款：**交接点**（`Store`/`Ticket`——交接是把负责人字段改写一次，不是把任务推过一条管道）；**原子性**（负责人转移发生在 `Take` 的一次原子操作里，判据 `ConcurrentTake`：赢家必须恰好 1 个、输家必须是 `ErrTaken`——把「存储连不上」算成输家，判据会在存储坏掉时变绿）；**幂等键**（同 Key 重复 `Offer` 是**重放**、返回原单；超时/收回后重新交接 = **新一代**，Generation 同 Key 严格递增、与领取令牌同一次原子操作铸造——taskboard 的 Fence 口径）；**回执寻址**（`ReplyTo` 挂出时必填——不假设「谁交的回给谁」，跨进程时源方可能崩溃重启；只有 To 能领取；回执凭存储铸造的令牌，不凭内容）；**超时**（一条时限线覆盖领取与回执两段，截止时刻归存储按自己的时钟推算；无时限的交接是契约禁止的；迟到的领取与回执一律拒收）。回执三值 done/failed/refused 与 delegate 同纪律。`Check` 是快照不变量纯函数（时钟注入，逾期残留显形）；`LocalDesk` 进程内参考实现只证契约可满足。**持久化面（#190）**：`Ledger` 的 Save/Load/Delete 条件写（Revision CAS + 代次水位）、`MemoryLedger` 内存参考实现、`Desk` 跨实例客户端（两个 Desk 共用一个 Ledger 即两个进程客户端）；Ownership Epoch 跨终态与 Delete 不复用（`TakeAt` 旧代拒绝、`ErrStaleEpoch`），回执按 `ReplyTo` 索引留存（`Deliveries`，Delete 后仍可取值） |
| `ownership` | 739 | oekit/pkg | 跨区域任务所有权权威的**契约面**（issue #273）：`handoff` 的 Epoch 管交接单代次、`coord` 的时间权威管租约到期，都不回答「分区后哪个区域有权提交这个任务域」。本包把**任务域 → 权威区域**收成可配置、可校验、可查的事实（`RegionMap` / `Authority.Mapping` / `Owner`），所有权带区域与代次（`Epoch`）。跨区域转移走**两阶段**：`BeginTransfer` 只记录候选代次（= 当前 + 1）与回执令牌、**不改变**当前权威；`ConfirmTransfer` 由目标区域凭回执令牌确认，换代是一次原子操作（代次提升 + 区域切换 + 令牌重铸）。分区期间旧区域的旧代提交一律 `ErrStaleEpoch`，确认前的候选代次一律 `ErrEpochUnconfirmed`——拒绝不是「尽量」。提交路径只依赖 `Fencer` 接缝；任何错误（含权威不可达）都必须拒绝（fail-closed）；判据 `Judge` 是纯函数，参考实现与单区域档共用同一份。**不造第二份租约**：没有 TTL/续期，任务租约仍归 taskboard/handoff。单区域部署退化为 `NewLocal`（本区域即权威，转移显式 `ErrSingleRegion`）；`MemoryAuthority` 只证契约可满足，不承诺跨进程原子性 |
| `worker` | 2188 | oekit/concurrency、oekit/pkg | Worker Runtime 的**生命周期契约与驱动**（注册 → 心跳 → 领取 → 执行 → 续期/恢复 → 优雅退出，issue #106）：`delegate` / `taskboard` 都不含「执行子任务的 worker 自己怎么活」——此前各宿主各写一套注册/心跳/领取循环，各写必然各漏一样（漏心跳 ⇒ 别人以为它死了；漏续期 ⇒ 长任务必被接管；漏注销 ⇒ 注册表里全是幽灵）。`Registry` 是注册/心跳/注销的存储中立契约（凭据由注册表铸造；同 ID 顶替并铸新令牌——崩溃重启是正常路径，先到者的心跳从此被拒）；`Loop` 是六段驱动：**心跳接 `taskboard.RenewingBoard.Renew`**（续期是资格检查不是摆设——租约易主即取消在途执行，危险窗口在施加之前封口）、任务板不可用驱动退出（宿主的重启策略兜）、优雅退出 = 停止领取 + 在途收敛（终态写入走 WithoutCancel）+ 注销不留幽灵。`Recover` 判资格不判时钟：对过期认领直接 `Claim`，活租约被 ErrTaken 拒——**过期判定归任务板侧**，不建第二套时钟判据。**受管执行器（#191）**：`ManagedExecutor` 把 Claim → `sideeffect.Runner.ExecuteWith`（执行意图/栅栏/结果未知）→ `checkpoint.Committer`（单调提交）→ Complete（凭令牌）收成唯一编排路径；UNCERTAIN 不落任务板状态（不 Complete + 释放租约，由副作用闸继续挡重试），对账走 `Resolve` 转发 Runner.Reconcile。**`MemRegistry` 进程内参考实现**在 worker/memory.go（Register 的顶替+铸令牌、Heartbeat 的资格校验+推后有效期都是同锁原子；只演示判据，多副本跨进程落库归宿主，issue #189）；端到端演示已迁至 goagent-runtime 仓的 examples/worker-runtime |
| `worktree` | 232 | oekit/pkg | 工作区隔离的**声明面与冲突判据**（**不碰真实文件系统**——建目录/挂载/chroot 是宿主与执行后端的活）。并行工作区会不会互相踩此前没人判：两个 agent 各自「在自己的目录里工作」，可写区完全重叠时任何一边的写入都会让另一边的结论失效，而报告里只有两份「各自成功」。判三件：声明自检（同一工作区不得既只读又可写）、**可写区重叠即冲突**（只读重叠不算——多个人读同一份文件没有危害，**写**没有第二份真相）、无命中即不可读写（fail-closed：默认允许会让漏写的一行声明变成全域可写）。**不支持通配**（写明的局限）：`*` 与 `**` 的语义各实现不同，进契约就会出现「按 A 的理解写、按 B 的理解判」，需要更强表达力时宿主自己展开成多条精确声明 |
| `metrics` | 804 | oekit/pkg | 内核指标契约面（issue #100）：指标名 / 类型 / 维度 / 基数上界的**单一来源**——business 选指标、transport 负责编码与投递（OTel / Prometheus / 自建看板由宿主接）；执行耗时 / 重试次数 / 租约竞争 / 恢复成功率 / 未知结果积压五个口径定名，基数上界在契约面封顶，看板后端先爆之前契约先拦。宿主自有指标经 `DefaultSpecs().Register/Merge` 复用同一套 Validate / 基数门（Clone 语义：包级表不被篡改；issue #239） |
| `cost` | 312 | oekit/pkg | 运行成本治理的**口径面**（issue #112）：任务级成本的计量单位/归集键/预算档单一来源——business 记账、transport 投递，与 metrics 同一分工；成本数字进看板之前先在契约面定口径，避免各宿主各算一套 |
| `tenant` | 272 | oekit/pkg | 多租户的**判据面**（issue #114）：租户隔离/配额/权限上下文的声明与判定纯函数——判据与执行分离（谁执行隔离由宿主与执行后端定，本包只判「这条声明成立吗」），零值=无租户上下文按单租户放行的口径写死在 godoc。**数据面落点（#195）**：任务身份 `(Tenant, ID)` 与 coord 的租户键/领取条件已进内核（显式装配 `NewTenantBoardAdapter`），副作用幂等键、事件流、运行留痕/审计、数据库 RLS 仍归宿主 |
| `controlplane` | 2341 | oekit/pkg | 控制平面的**接口面**（issue #115）：任务查看/运行状态/失败诊断/人工介入四类操作的契约——判定逻辑只有一份（`StorePlane`），宿主按自己的存储与鉴权落地；人工介入操作全部走令牌授权（介入是特权面，不走「谁都能点」的默认）。自有任务 ID/快照/租户类型的宿主实现 `HostPlane`/`AuthorizerOf` 窄契约（泛型 ID；与 taskboard/tenant 类型解耦，issue #238）。**持久化 Plane（issue #189）**：`Store` 契约（任务/运行/效果键的一致快照 + 带版本锚的原子提交）+ `StorePlane`（介入 = 状态变更 + 留痕同事务，留痕失败即整体失败；跨实例可见、代次单调）+ 进程内 `MemStore` / `MemPlane` 参考实现 |

> 「生产行数」口径：`wc -l` 统计 `*.go` 且**不含** `_test.go`——包体量看的是要交付的实现，
> 不是它的测试。依赖列只列**仓外** import；「无」表示该包生产代码只用 stdlib。
>
> 本表由 `make check` 末步的 `docsync` 机器校验（`cmd/docsync`），判定分两类：
> **依赖列逐项相等**——加依赖是显式动作，必须同步；**行数按 25% 相对容差判定**
> ——容差内的逐行涨落不拦，改一行代码不必顺手改文档，而跨量级的误述
> （某包声称 ~86 行、实际 412 行那种）仍然拦下。行数取较大者为分母，
> 「少写」与「夸大」同等对待。职责列与依赖列非空也在判定内。
>
> **`internal/` 下的包刻意不在本表**：它们是内部件，不是对外承诺（`docsync` 的
> 反向收录判定只看一级目录，故 `internal/subhub` 不会要求一行）。但它们**不受
> 「未登记包」豁免**——`architecture-policy.yaml` 的 `modules` 段照样要给
> `{id, roots, requires, owner}`，否则依赖方向门禁判它「对本门禁隐形」。

## 特性

本节只给判据形状与取舍理由；逐包接入顺序见 [docs/USAGE.md](docs/USAGE.md)。

### 边界不是文档，是门禁

**问题**：边界只写在散文里，一年后就会长出第二份实现——两份真相并存时，没有任何一方会收到信号。
**机制**：规则与结构判据全部挂在 `make check` 上，CI 只执行这一条；判定面在 `archcheck`（可导入包，宿主可直接复用），声明面在 `architecture-policy.yaml`。
**什么时候用**：新增任何包之前先判「机制还是资产」，再在 policy 的 `modules` 段登记依赖方向——先改声明面，再写代码。

<div align="center">
<svg width="760" viewBox="0 0 760 350" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="12" y="12" width="736" height="326" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="380" y="36" font-size="13" text-anchor="middle" fill="#9aa7b8">依赖方向：上层依赖下层；本仓只加机制面，不重写上游已有的那份</text>
<rect x="30" y="52" width="700" height="52" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="48" y="74" font-size="13" fill="#e6edf3">oekit（基础库）</text>
<text x="48" y="94" font-size="11" fill="#9aa7b8">oekit/pkg · oekit/logx · oekit/concurrency</text>
<rect x="30" y="128" width="700" height="52" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="48" y="150" font-size="13" fill="#e6edf3">oagentkit（Agent 原语矩阵）</text>
<text x="48" y="170" font-size="11" fill="#9aa7b8">guardx（invoke / sandbox / toolfail）· toolgate（policy）· runtime</text>
<rect x="30" y="204" width="700" height="52" rx="8" fill="#161b26" stroke="#58a6ff" stroke-width="2"/>
<text x="48" y="226" font-size="13" fill="#58a6ff">goagent-kernel（本仓）</text>
<text x="48" y="246" font-size="11" fill="#9aa7b8">判定环 · 续跑与终态闸 · 上下文三段 · 交接与权威 · 门禁执行体</text>
<rect x="30" y="280" width="700" height="52" rx="8" fill="#161b26" stroke="#3fb950"/>
<text x="48" y="302" font-size="13" fill="#3fb950">宿主（产品）</text>
<text x="48" y="322" font-size="11" fill="#9aa7b8">拓扑内容 · 存储实现 · 展示文案 · 领域判据</text>
<path d="M380,104 V120" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M380,128 l-4,-8 h8 z" fill="#9aa7b8"/>
<path d="M380,180 V196" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M380,204 l-4,-8 h8 z" fill="#9aa7b8"/>
<text x="400" y="196" font-size="10.5" fill="#9aa7b8">本仓依赖上游</text>
<path d="M380,280 V264" stroke="#3fb950" stroke-width="1.5" fill="none"/>
<path d="M380,256 l-4,8 h8 z" fill="#3fb950"/>
<text x="400" y="276" font-size="10.5" fill="#9aa7b8">宿主依赖本仓（方向相反）</text>
</svg>
</div>
<div align="center"><em>图 2 · 本仓在 oekit → oagentkit → goagent-kernel → 宿主这条链上的位置：判定面留上游，编排机制归本仓。</em></div>

> 设计推导见 [ADR-0001](docs/adr/ADR-0001-领域知识红线.md)；与上游的分工见 [ADR-0003](docs/adr/ADR-0003-与上游矩阵的分层.md)

### 工具调用只有一条路

**问题**：判定环少接一段，那个位置就成了整条链上唯一绕过所有门禁的路径。
**机制**：渐进披露、披露门、多守卫合成、失败分类、程序内调用全部接成同一条 `invoke.Stage` 链；`guardset` 把多条拒绝合成一条（不可重试优先的保守侧，并点出该先处理哪一条），`tooldisclose` 只放行已取回 schema 的工具。
**什么时候用**：任何会触发工具执行的路径——包括 `ptc` 里模型自己写的 `host.<tool>()`。

<div align="center">
<svg width="760" viewBox="0 0 760 330" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="12" y="12" width="736" height="306" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="380" y="36" font-size="13" text-anchor="middle" fill="#9aa7b8">普通工具调用与 ptc 程序内调用，走的是同一条判定环</text>
<rect x="40" y="48" width="220" height="38" rx="8" fill="#161b26" stroke="#58a6ff"/>
<text x="150" y="72" font-size="13" text-anchor="middle" fill="#e6edf3">普通工具调用</text>
<rect x="500" y="48" width="220" height="38" rx="8" fill="#161b26" stroke="#58a6ff"/>
<text x="610" y="72" font-size="13" text-anchor="middle" fill="#e6edf3">ptc 程序内 host.&lt;tool&gt;()</text>
<path d="M150,86 V100" stroke="#58a6ff" stroke-width="1.5" fill="none"/>
<path d="M610,86 V98 H150 V100" stroke="#58a6ff" stroke-width="1.5" fill="none"/>
<path d="M150,106 l-4,-8 h8 z" fill="#58a6ff"/>
<rect x="30" y="106" width="700" height="96" rx="10" fill="#161b26" stroke="#262e3d"/>
<rect x="44" y="128" width="152" height="52" rx="8" fill="#1c2330" stroke="#58a6ff"/>
<text x="120" y="150" font-size="12.5" text-anchor="middle" fill="#e6edf3">invoke.Stage 链</text>
<text x="120" y="168" font-size="10.5" text-anchor="middle" fill="#9aa7b8">装配顺序＝判定顺序</text>
<rect x="210" y="128" width="152" height="52" rx="8" fill="#1c2330" stroke="#a371f7"/>
<text x="286" y="150" font-size="12.5" text-anchor="middle" fill="#e6edf3">guardset</text>
<text x="286" y="168" font-size="10.5" text-anchor="middle" fill="#9aa7b8">并求值·合成一条拒绝</text>
<rect x="376" y="128" width="152" height="52" rx="8" fill="#1c2330" stroke="#a371f7"/>
<text x="452" y="150" font-size="12.5" text-anchor="middle" fill="#e6edf3">tooldisclose</text>
<text x="452" y="168" font-size="10.5" text-anchor="middle" fill="#9aa7b8">披露门·未取回即拒</text>
<rect x="542" y="128" width="152" height="52" rx="8" fill="#1c2330" stroke="#a371f7"/>
<text x="618" y="150" font-size="12.5" text-anchor="middle" fill="#e6edf3">toolgate 审计门</text>
<text x="618" y="168" font-size="10.5" text-anchor="middle" fill="#9aa7b8">上游策略裁决</text>
<path d="M196,154 H206" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M212,154 l-8,-4 v8 z" fill="#9aa7b8"/>
<path d="M362,154 H372" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M378,154 l-8,-4 v8 z" fill="#9aa7b8"/>
<path d="M528,154 H538" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M544,154 l-8,-4 v8 z" fill="#9aa7b8"/>
<rect x="30" y="222" width="340" height="56" rx="8" fill="#161b26" stroke="#3fb950"/>
<text x="200" y="246" font-size="12.5" text-anchor="middle" fill="#3fb950">放行 ⇒ 执行 ⇒ 结果回模型</text>
<text x="200" y="266" font-size="11" text-anchor="middle" fill="#9aa7b8">要不要原样重发 ⇒ 上游 guardx/toolfail</text>
<rect x="390" y="222" width="340" height="56" rx="8" fill="#161b26" stroke="#f85149"/>
<text x="560" y="246" font-size="12.5" text-anchor="middle" fill="#f85149">拒绝 ⇒ 一条指引回模型</text>
<text x="560" y="266" font-size="11" text-anchor="middle" fill="#9aa7b8">披露门被拒不激活：先取回 schema 再调用</text>
<path d="M200,202 V218" stroke="#3fb950" stroke-width="1.5" fill="none"/>
<path d="M200,222 l-4,-8 h8 z" fill="#3fb950"/>
<path d="M560,202 V218" stroke="#f85149" stroke-width="1.5" fill="none"/>
<path d="M560,222 l-4,-8 h8 z" fill="#f85149"/>
</svg>
</div>
<div align="center"><em>图 3 · 工具调用只有一条判定环：拒绝也走同一条环，并回一条指引；披露门被拒不激活，重试不是恢复路径。</em></div>

### 崩了能接着跑，而且不会「两条分支都赢了」

**问题**：崩溃后怎么恢复，临场决定必然选最坏的那个（盲目全量重跑或盲目跳过）；并发分支还会两条都以为自己赢了。
**机制**：`checkpoint` 把恢复判定做成纯函数（空洞不采信 / 终态不自动重开 / 序号单调闸）；`commitgate` 用独占提交闸保证终态只被提交一次——`statemachine` 是纯谓词、`SeqGate` 只排快照序号，都排不出「两条分支都赢了」。
**什么时候用**：任何会跨进程、跨崩溃恢复的长编排；续跑判定与终态独占必须成对出现，且 **Acquire / ReleaseLease 成对**（凭占用凭证精确释放，旧三元组认人的 Commit/Release 已删）。

<div align="center">
<svg width="760" viewBox="0 0 760 350" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="12" y="12" width="736" height="326" rx="10" fill="#1c2330" stroke="#262e3d"/>
<rect x="30" y="32" width="350" height="286" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="48" y="58" font-size="13.5" fill="#58a6ff">checkpoint：续跑，不是重放</text>
<rect x="48" y="74" width="60" height="34" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="78" y="96" font-size="12" text-anchor="middle" fill="#3fb950">S1 完成</text>
<rect x="126" y="74" width="60" height="34" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="156" y="96" font-size="12" text-anchor="middle" fill="#3fb950">S2 完成</text>
<rect x="204" y="74" width="60" height="34" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="234" y="96" font-size="12" text-anchor="middle" fill="#f85149">S3 崩</text>
<rect x="282" y="74" width="60" height="34" rx="8" fill="#1c2330" stroke="#9aa7b8"/>
<text x="312" y="96" font-size="12" text-anchor="middle" fill="#9aa7b8">S4 ？</text>
<path d="M110,91 H125" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M125,91 l-8,-4 v8 z" fill="#9aa7b8"/>
<path d="M188,91 H203" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M203,91 l-8,-4 v8 z" fill="#9aa7b8"/>
<path d="M266,91 H281" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M281,91 l-8,-4 v8 z" fill="#9aa7b8"/>
<text x="48" y="140" font-size="12" fill="#e6edf3">空洞不采信：前驱没完成就不算过</text>
<text x="48" y="164" font-size="12" fill="#e6edf3">终态不自动重开：终态就是终态</text>
<text x="48" y="188" font-size="12" fill="#e6edf3">序号单调闸：旧快照覆盖不了新的</text>
<rect x="48" y="206" width="314" height="44" rx="8" fill="#1c2330" stroke="#58a6ff"/>
<text x="60" y="226" font-size="11" fill="#e6edf3">SaveIfNewer：序号为 0 的快照一律拒收</text>
<text x="60" y="242" font-size="11" fill="#9aa7b8">（序号 0 不是一次真实提交）</text>
<rect x="390" y="32" width="340" height="286" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="408" y="58" font-size="13.5" fill="#a371f7">commitgate：终态只提交一次</text>
<rect x="408" y="74" width="130" height="34" rx="8" fill="#1c2330" stroke="#9aa7b8"/>
<text x="473" y="96" font-size="12" text-anchor="middle" fill="#e6edf3">分支 A</text>
<rect x="582" y="74" width="130" height="34" rx="8" fill="#1c2330" stroke="#9aa7b8"/>
<text x="647" y="96" font-size="12" text-anchor="middle" fill="#e6edf3">分支 B</text>
<path d="M473,108 L516,130" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M520,134 l-4,-8 h8 z" fill="#9aa7b8"/>
<path d="M647,108 L584,130" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M580,134 l-4,-8 h8 z" fill="#9aa7b8"/>
<rect x="490" y="134" width="120" height="40" rx="8" fill="#1c2330" stroke="#a371f7"/>
<text x="550" y="159" font-size="12.5" text-anchor="middle" fill="#e6edf3">独占提交闸</text>
<path d="M520,174 L482,192" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M478,196 l-4,-8 h8 z" fill="#9aa7b8"/>
<path d="M580,174 L646,192" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M650,196 l-4,-8 h8 z" fill="#9aa7b8"/>
<rect x="408" y="196" width="140" height="40" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="478" y="221" font-size="12" text-anchor="middle" fill="#3fb950">A 提交成功</text>
<rect x="582" y="196" width="140" height="40" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="652" y="221" font-size="12" text-anchor="middle" fill="#f85149">B 被拒</text>
<text x="408" y="262" font-size="11" fill="#9aa7b8">Acquire 铸造令牌 · ReleaseLease 凭令牌放闸</text>
<text x="408" y="280" font-size="11" fill="#9aa7b8">旧三元组认不出两次同名同序的占用</text>
<text x="408" y="298" font-size="11" fill="#f85149">迟到的释放会放掉别人的闸</text>
</svg>
</div>
<div align="center"><em>图 4 · 左：checkpoint 的续跑判定（空洞不采信 / 终态不重开 / 序号单调）；右：commitgate 的终态独占闸。</em></div>

### 上下文是有限预算，不是无限历史

**问题**：只给一个总数字，分不出「工具面膨胀」与「历史膨胀」，而两者处置完全不同。
**机制**：上下文工程三件套已按概念域分派上抛上游（2026-11-26 归属评测，v0.77.0）——
输入侧 `textx/budget`、输出侧 `textx/spill`、内联大对象 `textx/offload`
（github.com/oagentkit/textx，API 逐字未变）；本仓不再维护第二份。

### 失败是数据表，不是散落的 if

**问题**：散落的 `if` 让「能不能重试」「要不要人工」「能不能原样重发」各判一套，跨宿主必然漂移到没人发现为止。
**机制**：失败分类住在上游 `github.com/oagentkit/guardx/toolfail`（类别 → 可重试 / 退避 / 需人工 / 重发安全 / 模型指引，不可归类一律不可重试）；本仓 `dispatch` 是**失败面**包——三档 transient / fatal / other 加同域与跨域改派环，回答「失败后还能不能再派、派给谁」，不碰可达性（那是上游 `oagentkit/agent/dispatch` 的活）；`retryplan` 再把「该不该重试」与「重试会不会重复施加」合成一条可执行结论。
**什么时候用**：工具调用失败后的处置决策——先查表拿档位，再决定是否原样重发：**重发安全与重试价值是两个正交维度**（超时值得重试，但服务端可能已执行，原样重发就是重复施加副作用）。

分类结果落在同一条判定环上（见「工具调用只有一条路」的图）：它是环上的一环输入，不是散落在调用点的分支。

### N 轮 ReAct 往返塌缩成 1 轮

**问题**：N 轮「工具结果回模型 → 模型再决策」往返，原始数据每轮都过一遍模型面，预算与延迟随轮数线性放大。
**机制**：`ptc` 让模型写程序、进程内 goja VM 执行、程序内经 bindings 调宿主工具，只把归并后的小结果带回上下文（**原始数据不过模型面**）；三重预算（墙钟 / 调用数 / 字节数）替代 OS 沙盘，纯 Go 零 CGO、无 Node 分发面。
**什么时候用**：数据量大、步骤确定的批处理（多文件解析、批量查询）——一次调用比多轮对话便宜一个量级；程序内调用与普通调用走同一条判定环（见「工具调用只有一条路」的图）。

### 执行可以出进程，但档位表必须诚实

**问题**：后端自报的能力与实际执行强度之间没有信号，「已兑现」可能只是一句声明。
**机制**：`backend` 给任务式执行（镜像 + 参数 + 挂载 + 预算）定契约：隔离轴、能力自报、要求对照、登记簿选择，与上游 `github.com/oagentkit/guardx/sandbox` 同一条纪律（无满足者即拒绝，绝不降级）；出口与预算判据复用上游，本仓只做映射与选择，不私造第二套判据。
**什么时候用**：要跑不可信代码或需要强隔离时——先登记后端能力，再按 Spec 解析；登记簿里没有满足者就让调用失败，不要降级到进程内。

Docker Engine 与 Kubernetes Job 两份参考实现已迁至 goagent-runtime 仓的 containerbackend，接线三行：

```go
var reg backend.Registry
_ = reg.Register(containerbackend.NewDocker()) // 容器隔离档（network=none 兑现禁网）
_ = reg.Register(backend.NewInline())          // 进程内兜底档（显式 IsolationNone）
b, err := reg.Resolve(backend.Spec{
    MinIsolation: backend.IsolationContainer,
    Egress:       sandbox.EgressDenied,
    Budget:       sandbox.Budget{MaxMemory: 256 << 20},
})
res, err := b.Exec(ctx, backend.Job{Image: "registry.example/app@sha256:...", Args: []string{"run"}})
```

为什么隔离轴在本包重立而出口词表复用上游 `guardx/sandbox`：上游 `Level` 的枚举值即强弱序，往中间插容器档是跨仓破坏性变更；对应关系只写在注释里。
为什么 K8s 后端以逐作业 NetworkPolicy（`policyTypes: [Egress]` + 空 `egress`）兑现禁网、策略建不出来就拒绝创建作业：兑现不了的禁网静默降级成「普通作业」，等于把「要求」与「实际」拆成两份真相。NetworkPolicy 的强制依赖实现它的 CNI——集群不支持时宿主必须显式关闭策略，后端随即把 `Egress()` 自报降为 unrestricted，要求禁网的 Spec 不会选中它。
Job 声明的完整校验不写进本示例，但**不是缺判据**：上游 `sandbox.ExecPolicy` / `Judge` / `Spec` 是导出的，`backend.Job.ExecSpec()` 与 `JudgeExec(policy)` 已把 Job 声明映射成 `sandbox.Spec` 交给它判定（两层判据：`Validate` 管自洽，`JudgeExec` 管落在策略内）——装配期把 `JudgeExec` 挂上即可，示例为求短只写了登记与解析两行。

### 交接就是改写负责人，且只有一次

**问题**：跨进程交接若靠「推过一条管道」或「谁交的回给谁」，源方崩溃重启后就没有可寻址的对方——两种错在报告里读起来完全正常。
**机制**：`handoff` 把交接的每一问各收成一条契约条款（交接点 / 原子性 / 幂等键 / 回执寻址 / 超时）；判据 `ConcurrentTake` / `Check` 是纯函数，宿主写完存储实现跑一遍就算验过契约。
**什么时候用**：任务要跨进程转手时——交接是**负责人字段的原子改写**，不是消息投递：回执地址必填（不假设「谁交的回给谁」，跨进程时源方可能崩溃重启），无时限的交接被契约禁止（没有截止时刻的交接只会留下一把谁都不敢动的锁）。终态独占与负责人改写是同一族纪律：都要一道独占闸加一个可核对的凭证（见「崩了能接着跑」的图）。

### 跨区域只有一个权威

**问题**：分区期间，「这个任务域归哪个区域」若只是各宿主的配置约定，就会出现双主——两边的提交都看起来合法。
**机制**：`ownership` 把「任务域 → 权威区域」从配置约定收成可校验、可查的事实（`RegionMap` / `Owner` / `Mapping`），并用**代次 + 确认**把跨区域转移做成两阶段：提案只记录候选代次，确认成功才换代。
**什么时候用**：跨区域部署时——候选代次未确认一律拒（未经确认的候选代次只是「将要接管」，把它当授权就是双主），旧代提交也一律拒（分区期间旧区域的 owner 身份从未失效，唯一能挡住它的是提交路径上那条 `Fence`；权威不可达同样按拒绝处理）；且不给所有权加 TTL（那会变成第二份租约，任务租约仍归 `taskboard` / `handoff`，所有权只回答「谁是权威」）。单区域部署退化为 `NewLocal`，判据形状不变，将来接共享权威不用改调用方。

### 轨迹可以复读，评估是纯函数

**问题**：「结果像不像」要再跑一次模型看运气，那不是评估；而要复现一段历史，又不能靠时钟和 sleep。
**机制**：`trajectory` 把事件流投成确定性行（行 ID 确定、时刻随录制定格——时刻是数据不是时钟）；`eval` 把「录制 → 重放 → 差异比对」收成机制面：`Diff` 为空即复现，`Assess` 给出可判定的结构结论（收口 / 配对 / 重复 / 期望 / 禁止），零判项显式报错。
**什么时候用**：回归验收与线上问题复盘——能复读同一段轨迹，结论才可回归；不能复读的历史拿来做评估等于抽签。

<div align="center">
<svg width="760" viewBox="0 0 760 310" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="12" y="12" width="736" height="286" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="380" y="38" font-size="13" text-anchor="middle" fill="#9aa7b8">同一条事件流：投成轨迹行 → 录制 → 重放 → 比对 → 评估</text>
<rect x="40" y="52" width="220" height="52" rx="8" fill="#161b26" stroke="#9aa7b8"/>
<text x="150" y="74" font-size="12.5" text-anchor="middle" fill="#e6edf3">事件流</text>
<text x="150" y="93" font-size="10.5" text-anchor="middle" fill="#9aa7b8">事件时刻随录制定格</text>
<path d="M260,78 H326" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M334,78 l-8,-4 v8 z" fill="#9aa7b8"/>
<rect x="340" y="52" width="300" height="52" rx="8" fill="#161b26" stroke="#58a6ff"/>
<text x="490" y="74" font-size="12.5" text-anchor="middle" fill="#e6edf3">trajectory 行</text>
<text x="490" y="93" font-size="10.5" text-anchor="middle" fill="#9aa7b8">行 ID 确定 · 时刻是数据不是时钟</text>
<path d="M490,104 V120 H110 V132" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M110,140 l-4,-8 h8 z" fill="#9aa7b8"/>
<rect x="30" y="140" width="160" height="54" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="110" y="162" font-size="12.5" text-anchor="middle" fill="#e6edf3">Record 录制</text>
<text x="110" y="181" font-size="10.5" text-anchor="middle" fill="#9aa7b8">JSONL 可逐字节 diff</text>
<rect x="210" y="140" width="160" height="54" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="290" y="162" font-size="12.5" text-anchor="middle" fill="#e6edf3">Replay 重放</text>
<text x="290" y="181" font-size="10.5" text-anchor="middle" fill="#9aa7b8">不 sleep，不取 now()</text>
<rect x="390" y="140" width="160" height="54" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="470" y="162" font-size="12.5" text-anchor="middle" fill="#e6edf3">Diff 比对</text>
<text x="470" y="181" font-size="10.5" text-anchor="middle" fill="#9aa7b8">空切片 = 复现</text>
<rect x="570" y="140" width="160" height="54" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="650" y="162" font-size="12.5" text-anchor="middle" fill="#e6edf3">Assess 评估</text>
<text x="650" y="181" font-size="10.5" text-anchor="middle" fill="#9aa7b8">五判项 · 纯函数</text>
<path d="M190,167 H202" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M210,167 l-8,-4 v8 z" fill="#9aa7b8"/>
<path d="M370,167 H382" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M390,167 l-8,-4 v8 z" fill="#9aa7b8"/>
<path d="M550,167 H562" stroke="#9aa7b8" stroke-width="1.5" fill="none"/>
<path d="M570,167 l-8,-4 v8 z" fill="#9aa7b8"/>
<rect x="30" y="216" width="700" height="46" rx="8" fill="#161b26" stroke="#3fb950"/>
<text x="380" y="236" font-size="12.5" text-anchor="middle" fill="#3fb950">Diff 为空 = 复现；Assess 只判结构事实</text>
<text x="380" y="254" font-size="11" text-anchor="middle" fill="#9aa7b8">不跑模型看「像不像」——那要再抽一次签，不是评估</text>
</svg>
</div>
<div align="center"><em>图 6 · 同一段历史可逐字节复读，评估判据是纯函数：结构事实可测可回归。</em></div>

### 工程底座

| 指标 | 实测 |
|---|---|
| 生产代码 | 68,201 行 / 357 文件（不含 `_test.go`）|
| 测试 | 420 个测试文件 |
| 覆盖率 | 门槛：全局 60% + **逐包下限 70%**（两条判据判两件事，见下）。**实测值不抄在这里**，以 `make cover-gate` 的输出为准 |
| 结构阈值 | `max_file_lines: 400`，`max_methods_per_type: 25`（按**实质方法数**判，见下），`enforce: true` |
| 门禁 | 12 条规则（`rules` 段，以 `make architecture-check` 的输出为准）+ **`go/ast` 结构规则**（条数随 `archcheck/*.go` 增减而变，**不抄在这里**——抄进来就是给自己造一份会静默过期的真相；判据面清单：门包不变 / 依赖方向 / 沙箱档位登记 / 执行路径 / **子进程出口** / **未受管逃逸** / **租户隔离证据三条** / **接缝契约三形态三条：#369/#370** / **用例内 goroutine 收尾（#428：测试函数起了 goroutine 而测试侧无等待点即红）** / **例外与实际命中面对账（#447：例外命中 0 条违反且未标 `status: stale` 即红，标了 `stale` 却仍命中也判矛盾红，报告逐条点名不许静默）** / **测试面不得直调包级可变入口（#449：`check_kind: test_no_direct_call`，被禁符号与受管辅助全由 policy 声明，执行体零本仓专名）** / **判定词表登记七条：词表须有枚举出口 + 消费方不得手抄（判定词表 / 输入归一词表两类，判据精确到那一处字面量）+ 判定谓词须与表同源 + 输入归一词表须有导出的归一出口 + 词表须覆盖它那一族常量的全部取值（反向派生：常量加了、表没加当天即红）+ 同形撞车须显式声明关系（同概念 / 不同概念——决定手抄指路往哪引，错误指路比不指路更坏）+ 同形关系的锚点须仍指向真实常量（改名 / 被删 / 取值漂了 / 说谎了，当天即红）**）+ **门面包已清零**（v0.35.0 删 `dispatch`/`policy`/`reminder`/`toolfail`/`sandbox` 五门面、消费方直连上游；门面规则保留、清单置空）+ 54 包依赖方向 + README 包表一致性 + **USAGE 包数口径** + **API 生命周期与错误登记口径（`apilifecheck`，#113/#104）** + **部署配方能力清单口径（`deploycheck`：证据可解析 / state 由证据推导 / USAGE 逐行一致，#277；issue 证据只在单号仍开着时才算缺口）** + **存储选型判据（`storagecheck`：场景 storage 在允许集内 / storage 提供场景必需的能力 / 能力断言在代码里找得到落点 / 多写者场景不得选单写者档位 / 选型口径与已知不对称必须看得见，#424）** + **演进台账纪律（`ledgercheck`：同一单号状态一致 / 已合入必给关闭 PR 号 / 关闭 PR 号形态 / 无主单号 / **台账 ↔ GitHub issue 状态双向对账（#455：已合入而 issue 仍开、issue 已关而台账没登记关闭 PR 号，两方向都判；判据面与数据源分离——判据是纯函数，数据源**两级**：实时 `gh` 为主、拉不到退回 `docs/issue-state.yaml` 快照（刷新走 `make issue-state`），两级都拿不到才 exit 2）**）** + **竞态（`-race`）+ 漏洞扫描（`govulncheck`）+ 交叉构建矩阵** + **发版纪律（`releasecheck`：annotated tag / CHANGELOG 含本版段 / 无 U+FFFD / 区间内无混装提交；非 tag 提交显式跳过）** + **仓库卫生（`hygiene`：被跟踪的编译产物二进制不得入库（按文件头魔数判），且被跟踪文件不得命中 `.gitignore`（`git add -f` 强加的文本产物 / 陈旧跟踪），#413 / §16 建议 3）** |
| 例外登记 | **0** |

> **上表分两类：前两行有机器判定，其余仍是散文**（issue #31）。
>
> ✅ **已判**：生产代码行数 / 文件数 / 测试文件数——三项由 `docsync` **判相等，不留容差**。
> 包表那 25% 容差是给「逐行涨落」留的噪声滤波，而这张表的历史漂移量恰好落在容差里
> （895 / 578 / 573 行相对 1.9 万行都在 3% 上下），给它容差等于让静默过期继续成立。
> 口径就是本段末尾那三条命令，门禁报错时也会原样给出来。
>
> ⚠️ **未判**：覆盖率与其余四行。
>
>   - **覆盖率不判是刻意的**：它随「跑的是哪个用例集合」变——2026-11-06 同一批用例、
>     生产代码一行未改，量出 85.5 / 85.0 / 87.0 三个值。让散文去复述一个由门禁**实时**
>     给出的数字，只会制造两处不一致；故这一格只写门槛（门槛由 Makefile 判，不会漂），
>     实测值看 `make cover-gate` 的输出。
>   - 结构阈值 / 门禁条数 / 例外登记数是**描述**，各自有执行体（policy + 各条门禁），
>     写错不会让门禁失效、只是描述不准——判定成本高于收益，故不判。
>
> 于是「改这张表」的规矩变成两半：**前两行照门禁的报错改**（它会告诉你实测值），
> **其余行改前请手算**，别信上一版。
> v0.6.4 那次修「工程底座」三处漂移（生产代码 11,491 行/66 文件 → 13,336 行/77 文件、
> 测试 84 → 99 个文件、覆盖率 80.4% → 86.6%）时，这三处已经过期了**三个月**——
> 它们是 `archcheck` 从 `cmd/` 下沉成独立包（2026-10-19）之前的旧值，此后没人更新。
> 这正是本仓「散文不是门禁」那条纪律的反面案例：**机器只管得着被写成规则的那部分**。
> 改这张表时请手动重算，**别信上一版**。
>
> **重算口径（照抄这三条，别手算）**——「生产代码」= 全仓非 `_test.go` 的 `.go`，
> 排除隐藏目录与 `vendor`：
>
> ```bash
> find . -name '*.go' -not -name '*_test.go' -not -path './.*' -not -path './vendor/*' | wc -l
> find . -name '*.go' -not -name '*_test.go' -not -path './.*' -not -path './vendor/*' -print0 | xargs -0 cat | wc -l
> find . -name '*_test.go' -not -path './.*' -not -path './vendor/*' | wc -l
> ```
>
> 覆盖率取 `make cover` 的最后一行（`go tool cover -func=coverage.out | tail -1`）。
>
> 2026-11-06 第八轮校准（**生产代码一行没动**）：19,624 行 / 113 文件未变，测试文件
> 139 → **141**（`sandbox/forwarders_test.go`、`cmd/docsync/run_test.go`），覆盖率
> 85.5% → **87.0%**。这 +1.5pp 全部来自补测，不是新写的生产代码：`sandbox` 60.0% →
> **100%**、`cmd/docsync` 23.6% → **88.8%**（`run()` 端到端，用 `t.Chdir` 造夹具）。
>
> 中间有一次「只补了 `sandbox`」的测量是 **85.0%**——比上一版**低** 0.5pp，而生产代码
> 没变、只多了一个测试文件。这件事本身值得记下来：**这张表的数字会随测量口径与批次
> 漂**（同一批用例在不同轮次里给出 85.5 / 85.0 / 87.0 三个值）。
>
> 2026-11-06 第九轮（**这一轮把表接进了门禁**，issue #31）：19,624 → **20,397** 行 /
> 113 → **118** 文件 / 141 → **146** 个测试文件（+773 行）。前两行从此由 `docsync`
> **判相等**，报错时会把实测值与三条重算命令一并给出，不需要再靠「记得回来手算」；
> 覆盖率那一格**删掉了手抄的百分数**，只留门槛（门槛由 Makefile 判）。
> 这一轮记下的教训是：**「会漂的数」与「不会漂的数」要分开对待**——覆盖率属于前者，
> 让它出现在散文里，就等于承认「这里迟早会说谎」；行数/文件数属于后者，那就判相等，
> 连容差都不该给。
>
> 同日补的第四条门禁口径：`cover-gate` 从「只判全局」变成「全局 + 逐包下限 70%」。
> 起因是全局 85.5% 同时掩盖着 `cmd/docsync` 23.6% 与 `sandbox` 60.0%——单个包的
> 塌方在大盘里完全看不见。`sandbox` 的 60% 已补到 **100%**（门面包的每条语句就是
> 一句转发，转发目标或参数顺序写错编译得过、语义却错，故对门面包而言语句覆盖率
> 恰好是恰当的度量）；`cmd/docsync` 当时先登记豁免（判定逻辑仍在 `package main`、
> 没有可注入的接缝），**同日即拆除**——改用 `t.Chdir` 造夹具后 `run()` 端到端可测，
> 23.6% → **88.8%**，`PKG_COV_EXEMPT` 现已为空。
>
> 2026-11-06 第九轮校准（差距台账落地轮）：上一版记的 19,624 行/113 文件、141 个测试
> 文件、87.0%，这一轮按 `docs/GAP-ANALYSIS-20261106.md` 落了 P0-1/P0-2、P1-1..P1-5 与
> 五项 P2（`cmdpolicy` / `egress` / `preset` / `sessionref` / `taskboard` /
> `skilldisclose` / `hookengine` / `memorypipe` / `delegate` / `worktree`，外加
> `archcheck` 的样例自检与子进程出口判据），实测 **23,281 行 / 130 文件、156 个测试
> 文件、87.6%**；门禁表里「依赖方向」那一格也从 34 包改为 **42 包**（`archcheck` 实测）。
> 这一轮值得记一笔的**不是**行数涨了 3,657——而是十条门禁里有两条是**这一轮才有的
> 判定面**（规则样例自检、子进程出口），它们此前都是「有声明、没人判」。


### 平台支持矩阵（2026-11-06 补齐）

此前本仓**从未声明支持哪些平台**，也没有任何东西验过——本机 darwin 全绿，而本仓是
「被宿主编进二进制」的库，真正的交付面是 linux。2026-11-06 实测才发现 Windows
根本编不过，而这个事实在仓里**一个字都没有**。

| 平台 | `go build ./...` | 依据 |
|---|---|---|
| linux/amd64 | ✅ 支持 | `make crossbuild` 每次 `make check` 都验 |
| linux/arm64 | ✅ 支持 | 同上 |
| darwin/arm64 | ✅ 支持 | 同上（本机开发面） |
| **windows/amd64** | ✅ **支持（语义降级，见下）** | 2026-11-07 回归：上游 `oagentkit/runtime` procx 落地 Windows 进程树路径（Job Object 建组 + TerminateJobObject 整树硬杀，runtime v0.11.0 起，oagentkit/runtime#24）——`make crossbuild` 恢复 windows/amd64，CI 增加 windows job 真跑测试。**语义降级**：Windows 无进程组信号语义，TERM 优雅退出降级为整树硬终止；这是接口层写明的差异，不是对等实现 |

处置方式：**显式声明不支持**，而不是留着「看起来支持、其实编不过」。`make crossbuild`
只判 `CROSS_TARGETS` 里列出的平台——为一条永远编不过的目标留门禁不是严谨，是把红线
泡软：它会常年红，然后被人忽略，连带把别的红一起忽略掉。

要在 Windows 上使用，前提是上游 `procx` 先给出可移植的进程组实现；在那之前本仓
`ptc` 的子进程后端不适用于 Windows。这条限制**不在本仓能力范围内**，故不做兼容层。

### 三条「目标存在但没人跑」的门禁（2026-11-06 补齐）

2026-11-06 的仓库审计里有三条门禁的共同形态：**目标/文档里有，但没有任何东西执行它**——这和 `lint` 当年一模一样（见 Makefile 的 lint 注释）。
处置方式也一样：接进 `make check`，不挂到 `ci.yml` 上变回「推送后门禁」。逐条实测漏项如下。

| 门禁 | 此前状态 | 它漏掉的东西（实测） |
|---|---|---|
| `race` | `race` 目标存在，但 `check` 依赖链与 `ci.yml` **都没有它** | 本仓的并发面（策略代次、提交闸、共享 map）从没被竞态检测过。补上时踩到一个硬事实：linux 上 `-race requires cgo`，而 `GOENV := CGO_ENABLED=0` ⇒ race 目标**不能**跟着 GOENV，否则在 CI 上必红 |
| `vuln` | Makefile 里没有这个目标，`govulncheck` **从未跑过** | 首次跑出 **6 条漏洞，全部在标准库**（`crypto/x509` / `net/http` 系列 / `crypto/tls`），修法是抬 `go` 指令到 1.27.3。载体正是「下游用本仓编出来的那个二进制」 |
| `crossbuild` | 没有任何交叉构建门禁 | 见上一节：Windows 编不过这件事从来没被记录过 |

顺带补上的第四条：**覆盖率此前只判全局**。全局 85.5% 同时掩盖着 `cmd/docsync` 23.6% 与 `sandbox` 60.0%——大盘涨 1% 就能抵消一个包从 70 掉到 20 的塌方，而报告里只有一行「85.5%，绿」。
故 `cover-gate` 现在跑**两条**判据（逐包判据的执行体是 `cmd/pkgcov`——原先是 `scripts/pkgcov.awk`，issue #36 收编为 Go 并带上自己的用例），且逐包下限的**普通豁免**（`PKG_COV_EXEMPT`）**失效即红**（豁免包一旦自己达标，门禁会要求当天删掉那条豁免——本轮就是这样把它拆掉的：`cmd/docsync` 补测到 88.8% 后第一次跑门禁，报的正是「豁免失效，请从 `PKG_COV_EXEMPT` 移除」，而不是默默放行）。
> ⚠️ 豁免分**两类**，「失效」的判据不同（2026-11-20）：普通豁免（`PKG_COV_EXEMPT`）的失效是「自己达标了」（还缺用例这件事没了）；**结构性豁免**（`PKG_COV_STRUCTURAL_EXEMPT`）是「包的**性质**决定通用覆盖率判据不适用」（环境依赖型），它自己达标**不**判红（理由仍在），但**没命中任何包**仍判红（包没了/改名了 ⇒ 条目该拆），**命中的包形态不适用**（零测试文件 / 无语句）同样判红（豁免对它压根不适用 ⇒ 换表或拆除；两种形态分开报，否则维护者会照着「没有包命中」去找一个**就在日志里**的包）。**同一个包同时登记在两张表里 ⇒ 判口径冲突并报红**（报告**两个表项都点名** ⇒ 照着提示拆掉错的那条即可）：两张表的语义互斥（一个说「还缺用例」，一个说「性质决定通用覆盖率判据不适用」），默默取一条等于判据自己藏起一处矛盾 ⇒ 下一个人读到的是一份自相矛盾的声明面，而门禁此时是绿的。曾登记的 `examples/pgstore` 属后者（2026-11-22 随参考实现迁 goagent-runtime，条目已删）——它原先登记在 `PKG_COV_EXEMPT` 里，于是同一个包、同一份代码，本机有 PostgreSQL 时覆盖 80.7% ⇒ 判红「豁免失效」，而豁免的理由一个字都没变。

## 定位：Agent Execution Kernel，不是 Agent Framework

评审（2026-11-27 §3.3 问题五）定调的边界——**五项属于宿主**，内核只需要知道
它们在执行过程中产生了什么可判定的状态和操作：

| 属于宿主 | 内核对应的判定面 |
|---|---|
| 专家角色 | 开放键状态值（内核不知「角色」；`# Role:` 类叙事由 prompt 门禁挡在生产代码外） |
| Prompt | `harness-no-prompt-template` 门禁：模板特征（You are a… / # Role: / <|im_start|>）不进内核生产代码 |
| 工具实现 | `ptc` bindings / `backend` 注册接缝——只定「要求 vs 实际」判定口径，不长工具 |
| 领域判断 | ADR-0001 领域知识红线 + `contract` 开放键（内核不解释不校验） |
| 模型调用 | `harness-no-model-client-import` 门禁：模型 client 不进依赖面（roundguard 类判据只判结局，不调模型） |

核心竞争力不是能调用多少模型，而是**长任务、多任务、多副本、异常恢复下的执行
正确性**。五项对照与判据落地见 [ADR-0011](docs/adr/ADR-0011-Kernel不等于Framework五项边界.md)。

## 头号红线：不得长出领域知识

拆分做完后最现实的风险不是拆不动，而是**业务代码一年年渗回内核**，等发现时内核已经退化成「换了个名字的 monolith」。
这条红线的规则清单、推导与事故来源已下沉成 ADR 集——README 只留结论，入口是 [ADR 索引](docs/adr/README.md)：

| ADR | 一句话结论 |
|---|---|
| [ADR-0001](docs/adr/ADR-0001-领域知识红线.md) | 领域知识只能住在宿主／领域包，本仓只留机制面——这条边界靠机器判，不靠自觉。 |
| [ADR-0002](docs/adr/ADR-0002-门禁执行体可被宿主复用.md) | 机制面若要跨仓复用，就不能锁在 `package main` 里——判据与执行体分离，宿主只写声明面。 |
| [ADR-0003](docs/adr/ADR-0003-与上游矩阵的分层.md) | 本仓不是孤岛——家规只写在别处的文档里等于没有规矩，必须落成本仓自己执行的门禁。 |
| [ADR-0004](docs/adr/ADR-0004-执行沙箱入口唯一.md) | 执行只允许经 `sandbox` 登记簿，档位声明必须有人签字——否则声明强度与实际执行强度之间没有信号。 |
| [ADR-0005](docs/adr/ADR-0005-门面包不可变.md) | 门包只许转发、不许长胖——门面一旦自己长出实现，就成了与上游并存的第二份真相。 |
| [ADR-0006](docs/adr/ADR-0006-通用化判据.md) | 内核只给**机制**——判定语义归内核、判定依据与展示文案归宿主。 |
| [ADR-0007](docs/adr/ADR-0007-执行意图.md) | 意图的存在本身就是一条判据——没结论不是「没做过」，而是一次**尚未裁决**的尝试。 |
| [ADR-0008](docs/adr/ADR-0008-跨进程任务交接契约.md) | 跨进程交接的契约不是队列，是**负责人字段的原子改写**——交接点、幂等键、回执地址、时限四问各有一条可机械判定的答案。 |
| [ADR-0009](docs/adr/ADR-0009-调度判据归属.md) | 调度判据的归属靠证据：上游 dispatch 的判定面只有可达性/深度/自派发三条，优先级/公平/配额/背压落本仓 `scheduling`，可达性仍归门面。 |
| [ADR-0010](docs/adr/ADR-0010-对话域上抛与org归属.md) | 概念域分派第三轮：对话域七包（roundguard/budget/spill/offload/pending/sessionref/memorypipe）判据主语不是编排，全部上抛；kernel/runtime 归 oagentkit org。 |
| [ADR-0011](docs/adr/ADR-0011-Kernel不等于Framework五项边界.md) | Kernel ≠ Framework 五项边界：角色/Prompt/工具/领域/模型调用归宿主；prompt 模板与模型 client 两条门禁（注入即红）。 |

## 通用化判据

内核每加一个能力，先过这一问：**换个宿主，这段代码要不要改？** 要 → 属于宿主，不该在内核。
落到三条可执行约定：判定语义归内核、判定依据归宿主；展示文案归宿主；测试夹具用合成数据。
> 设计推导见 [ADR-0006](docs/adr/ADR-0006-通用化判据.md)

## 文档站（GitHub Pages · VuePress）

**[yi-nology.github.io/goagent-kernel](https://yi-nology.github.io/goagent-kernel/)**

**VuePress 文档站**：介绍、设计初衷、怎么用（接入路线图 + 最小示例）、能力详解（九组）、
门禁体系、坑速查、包清单与机器事实。

**站点源码在本仓 [`site/`](site/)**（`docs/` 内容 + `gen.mjs` + `.vuepress/config.js`），
发布走 [`.github/workflows/pages.yml`](.github/workflows/pages.yml)（`build_type: workflow`）：
push 到 main 且站点内容或其**事实输入**（`README.md` / `go.mod` / `Makefile` /
`architecture-policy.yaml` / `docs/USAGE.md`）有变更时，CI 自动构建并部署。

本地构建：

```bash
make site     # = cd site && npm ci && npm run docs:build（开发预览：npm run docs:dev）
```

**站点上的每个数字都不手抄。** 生成器 `site/gen.mjs` 在每次构建前把 README（包表 /
工程底座）、`go.mod`（依赖版本）、`architecture-policy.yaml`（规则清单）、`Makefile`
（覆盖率门槛）、`docs/USAGE.md`（坑速查表）抽成站点页——**解析不到即构建失败**，
不静默填默认值。生成页不入库（入库就会没人重新生成，等于把「自动」改回「手抄」）。
理由与本仓其它纪律同源：**手抄一遍就等于多一份会静默失真的真相**。

> **迁移记录（2026-11-11）**：站点原先住在 `gh-pages` 独立分支（`build_type: legacy`），
> 生成器与源码不在 main 里——恰好违反本仓「单一真相」的纪律。当日迁回 `site/`
> 并升级为 VuePress 文档站；`gh-pages` 分支**冻结为历史归档**（迁走前的最后一版，
> 只作对照，不再接收产物）。

## 快速开始

**要接入本仓**：直接看 **[docs/USAGE.md](docs/USAGE.md)**，那是按「宿主接入顺序」
组织的完整指南——九组能力逐个讲清「职责 → 最小集成片段 → 关键符号 → 坑」，
末尾有一张按症状索引的坑速查表。

**要改本仓**：

```bash
make build          # 编译 archcheck
make test           # 全量单测
make check          # 全量门禁（versioncheck/cijobname/fmt/vet/lint/test/cover-gate/vuln/race/crossbuild/archcheck/docsync/seamcheck/apilifecheck/deploycheck/storagecheck/ledgercheck/releasecheck/modreplace/modwork/hygiene）
make architecture-check --report   # 看全量违规清单（默认前 10 条）
```

> macOS：本机 go build/test 一律带 `CGO_ENABLED=0`（新 SDK 27.0 与 CLT linker
> 不兼容 + 许可未接受），Makefile 已内置。
>
> `make check` 需要 `golangci-lint`（CI 钉 v2.14.0）；未安装时该目标**明确报错退出**，
> 不静默跳过——没人跑的门禁等于没有门禁。

### 提交与合并

`main` 有分支保护：**必须走 PR**，且必须通过六条检查——

- `unit（fmt+vet+lint+test+archcheck+docsync+卫生）`（Linux 综合门禁）
- `race（竞态检测，CGO_ENABLED=1）`
- `contract（sidetest+boardtest 契约套件）`
- `fault-injection（故障注入矩阵）`
- `gates（cover-gate+crossbuild+vuln+versioncheck）`
- `windows (真跑测试)`（Windows 上真执行，不是只编译）

六条都是 strict（需与 `main` 同步）。**job 名与 required context 必须逐字相同**——
context 等不到就永远 BLOCKED（2026-11-22 实录：integration 层迁走后保护规则
没同步，所有 PR 卡死，直到把这条过期 context 摘掉）。管理员保留 bypass 通道（应急直推），
但**门禁本体全在 `make check` 里**——本地能过，CI 就该过；CI 红了先看日志分清
「真违规」与「基建抖动」，不要绕过。

> 这条保护是 2026-11-11 开的（issue #77）。此前 `main` 完全无保护，直推即合，
> CI 通过与否与能不能合无关——于是「CI 是绿的」不再等于「能合」，
> 门禁的失败形态又变回了「变绿」。

## 本地多仓并行开发

本仓处在 `oagentkit` / `oekit` 矩阵里，改本仓时经常要同时改上游仓（如 guardx）。
这里有个必须知道的陷阱：

> **Go 的 `replace` 只在主模块生效，依赖模块的 replace 不级联。**

在本仓 `go.mod` 里写 `replace github.com/oagentkit/guardx => ./.guardx-work/guardx`，
本仓构建全绿（自己就是主模块）；但下游宿主把本仓当依赖时，这条 replace 被**静默忽略**，
guardx 按 MVS 回落到已发布的旧版本，于是报
`no required module provides package github.com/oagentkit/guardx/injectband`——
错指 guardx，真正原因（本仓依赖了未发布的新包）不出现，一行本地验证代码断掉所有下游。

正确姿势是 workspace（`use` 跨仓生效，且不碰任何 go.mod）：

```bash
git clone https://github.com/oagentkit/guardx.git .guardx-work/guardx   # 未跟踪
make devlink     # 生成本地 go.work，把 guardx 工作副本接入
# ...改 guardx，本仓构建即时生效...
make devunlink   # 断开，构建回到 go.mod 声明的已发布版本
```

配套纪律：

1. **go.mod 零 replace。** `go.mod 不得含 replace` / `go.work 不得入库` 两条门禁在
   `make check` 里（本地与 CI 同一条路径），本地 `go.work` 由 `.gitignore` 兜底。
2. **go.mod 里 require 的版本必须是已发布的 tag。** 若确需依赖未发布的新包，
   用 `make devlink`，不要动 go.mod。CI 在干净环境构建，未发布版本解析不到直接红——
   这是发版顺序纪律的第二道防线。
3. **workspace 期间别跑 `go work sync`。** 它按 workspace 的构建列表**回写**各模块的
   `go.mod` / `go.sum`：仓不全时用不到的依赖会被直接删掉（实测把 oagentkit 的四个依赖
   整片剥掉）。要整合同步，先 `make devunlink`，再在单模块下 `go mod tidy`。
4. **`make devlink` 可重复执行。** 已有 `go.work` 时走 `go work use -add` 追加而不是
   重建，所以往 workspace 里加第二个仓不会抹掉别人已加的条目。
5. **验下游而不是只验本仓。** 本仓作为主模块绿，不代表作为依赖也绿（replace 的失效就发生在这个差别里）。

## 下游升级须知（破坏性变更）

本仓的公开 API 里有一部分**类型来自上游矩阵**（`invoke` 判定环原语）。上游把它换了包，
本仓跟随，**下游的 import 路径就要跟着换**——这类变更记在这里，不记在 tag 注解里
（tag 注解会沉到 git 历史里，README 不会）。

| 本仓版本 | 变更 | 下游要做什么 |
|---|---|---|
| **v0.76.0**（已发版，tag `v0.76.0`） | **`taskboard.Board.Complete` 的凭据面补上「租约未过期」**（issue #497，**破坏性 minor**——`taskboard` 是 Stable 包，走 api-compat.md 五步流程）：旧口径只判「状态 == claimed && 令牌匹配」，与 `Renew` 的「租约已过期 ⇒ `ErrLeaseLost`，**资格在到期时刻终止**」不一致 ⇒ 存在一个真实窗口：**租约已过期、但还没有别的持有者接管**时，旧持有者仍能把任务标记完成 ⇒ 终态内容取决于**谁先提交**（竞态），而不是取决于谁有资格提交。新口径：凭据面 = **令牌 + 租约未过期**（判据走 `taskboard.LeaseHeld`，不各写一份）；过期 ⇒ `ErrLeaseLost` 且**不做任何修改**（拒绝后任务仍是认领态，可被接管）。`Release` **不判有效期**（归还是自愿放弃、不写终态）⇒ 行为不变。共享契约套件新增子判据「租约过期后完成被拒」（拒绝 + 不改状态 + 接管者能接手，三件齐）并配变异实现做反向验证；参考实现（`coord.BoardAdapter.Complete` / `controlplane.MemStore.Complete`）同批按新契约实现。**零既有导出符号消失、零既有签名变更、依赖地板零变化** | **① 实现 `taskboard.Board` 的宿主**：在 `Complete` 的判定里补一条「租约未过期」，且**在存储侧同一次原子操作里判完**（别拆成「读 → 判 → 写」三步——那三步之间正是本单要封的窗口）；判据走 `taskboard.LeaseHeld`，不要各写一份。`Release` 不需要改。<br>**② 接 `taskboard/boardtest` 共享套件的宿主**：升级内核后跑一遍即知是否合规——新增子判据会把未跟上的实现判红（不是「下次再说」的红，是 CI 立刻红）。<br>**③ 只调用内核判据、不自己实现 Board 的宿主**：零行为变化。<br>**④ 下游无需 `go mod tidy`**（依赖地板零变化）。 |
| **v0.75.1**（已发版，tag `v0.75.1`） | **补 `invocation` 的 `failed` 终态**——执行失败（副作用**确定未施加**）与审批拒绝（`rejected`）**分属两个决策来源**：宿主接线时暴露的缺口是「获批了但执行体报错」原先端点无处安放，宿主只能在「记成 rejected」（读者分不清「没获批」与「获批但跑失败」，两者补偿口径不同）与「停在 executing」（谎称还在执行）之间二选一。状态词表 7 值 → **8 值**，`executing → failed` 是合法转移，`failed` 为终态（`Recovery(failed) = none`：确定没做，不需要恢复动作），共享契约套件补一条判据（可从 executing 到达且为终态），并显式声明与 `roundguard` / `taskboard` 的「failed」为 `distinct`（同形撞车须显式声明关系）。**零既有导出符号消失、零既有签名变更、依赖地板零变化** | **① 下游无需任何动作**：新增取值不影响既有代码 ⇒ **无需 `go mod tidy`**（除非开始用 `invocation.StateFailed`）。<br>**② 想用新取值的宿主**：执行面判失败（执行体报错 / 闸判失败 / 工具未注册）时把账目推进到 `failed`——它是终态（确定没做，不需要恢复动作），与 `rejected`（审批闸判的「没获批」）分开记账，读者才不会把两个决策混成一个。 |
| **v0.75.0**（已发版，tag `v0.75.0`） | **新增一个机制包 `invocation`（工具调用账本）+ 共享契约套件**：① 契约面——调用身份（callID + 工具名 + 参数摘要）**一经落账不可变**、状态机（planned → approved → executing → applied → result_recorded，另有 rejected / uncertain）、**恢复处置判据**（executing ⇒ 对账、applied ⇒ 回填结果而不是重做）、存储中立的 `Store` 接口 + 三个哨兵（`ErrNotFound` / `ErrIdentityConflict` / `ErrIllegalTransition`）。② 两张**判定词表**（状态 7 值 / 恢复处置 4 值）登记进 policy 的 `word_tables` 段 ⇒ 消费方必须引用常量而不是手抄字面量；与 contract / statemachine / protocol / roundguard 的同形取值显式声明为 `distinct`（同形不同义）。③ `Predecessors(to)` 前驱集出口——宿主拿它拼「一条带条件的 UPDATE」实现存储侧**原子推进**，不必自己反推转移表（反推就是第二份真相）。④ 共享契约套件 `invocation/invocationtest`（14 条子测试）接入 `make contract` ⇒ CI 的 contract job 会跑。**本版零既有导出符号消失、零既有签名变更、依赖地板零变化** | **① 下游无需任何动作**：新增包不影响既有代码 ⇒ **无需 `go mod tidy`**（除非开始用新包）。<br>**② 想用账本面的宿主**：实现 `invocation.Store`（`Plan` / `Get` / `List` / `Advance`）后，把 `invocation/invocationtest` 接进自己的测试跑一遍（违反哪条契约就红在哪一条）；**推进必须在存储侧原子判定**（一次 CAS / 一条带条件的 UPDATE），不能拆成「读 → 判 → 写」三步——那三步之间正是本包要封的窗口。参考实现落在 goagent-runtime 的 `pgstore` / `sqliteadapter`（`pgref_invocation` / `gak_invocation` 两张表）。<br>**③ 本版把 v0.74.0 的版本身份补翻成「已发版」**（它的 tag 早已存在，此前漏了回执 —— 快照自称未发版，下游从模块缓存里读到的发布说明会误判）。 |
| **v0.74.0**（已发版，tag `v0.74.0`） | **三条判据面缺口单同批清偿 + 总纲单判据形态兑现**：① #481 墙钟阈值判据——`harness-test-wallclock-ruler` 判定面扩到「固定秒数断言 / 纯计时器窗口」，policy 新增 `ruler_classifications` 做三方向对账（漏登记 / 登记成尺子类 / 改完不删登记 各自即红）；实测推翻 issue 估计：`time.After` 的 104 处 select 全部带事实分支（纯计时器窗口 0 处），固定秒数断言 5 处 ⇒ 改写 2 处、登记 3 处。② #482 外部依赖接缝清单——新增 `docs/EXTERNAL-DEPS.yaml`（骨架机器生成、判断字段人维护）+ `harness-external-dep-seam` 三方向对账；实测 139 处外部触达 ⇒ 110 个函数；接缝核验改为对**任何**声明了接缝的条目都跑（原先只跑 decide ⇒ 本仓接缝多在 stamp ⇒ 一次都没执行）。③ #483 验收路径「被执行过」证据——新增 `internal/acceptancepath`（判据纯函数，时钟与文件读取均可注入）+ `cmd/acceptancecheck` + `docs/acceptance-paths.yaml` + `make acceptancecheck`（接进 `unit`/`check`）；五条口径：从未跑过即红并点名 / 时效只回显不判红 / 入口必须真的存在 / 反向对账 / 证据与登记逐字对账。④ 总纲单 #279 的判据形态兑现——路线达成对账落地（声明面 `docs/ROUTE-LEDGER.yaml` + 判据体 `internal/routeledger` + 门禁 `make routecheck`），3 阶段 / 18 个成员单号全部已合入。同批撤掉本仓悬空的 `make k8s-e2e`（判据体已随边界迁到 goagent-runtime），可复跑入口迁至 runtime 仓。本版**零对外导出面变化**（新增的两个 internal 包不对外承诺、无既有签名变更、依赖地板零变化） | **① 下游无需任何动作**：零导出符号消失、零既有签名变更、依赖地板零变化 ⇒ **无需 `go mod tidy`**。<br>**② 新增两个 internal 件（非对外面）**：`internal/routeledger` 与 `internal/acceptancepath`——Go 的 internal 可见性规则已挡住外部引用，宿主**不可 import**；它们只服务本仓的门禁与台账，api-lifecycle 定 Experimental 即为此。
| **v0.73.0**（已发版，tag `v0.73.0`） | **边界归位：三个包上抛——`netguard` → `guardx`、`ticket` → `guardx`、`cmdpolicy` → `tools`**（PR #486）：按「概念域分派，不按实现依赖」的口径，① `netguard`（出站 URL 守卫 SSRF）属**执行安全域**，与 `egress` 同族同层——原先守卫住在编排内核、而它的 IP 策略依据在 guardx ⇒ 机制与判据依据分家（分层倒挂）；② `ticket`（一次性短时授权票据）属**执行安全域**（准入凭据），与 `permgate`/`rbac`/`fence` 同族；③ `cmdpolicy`（命令级放行裁决）与它的**事实面**（bashguard 真 shell AST）本是同一件事的两半，分家在不同仓 ⇒ 依赖只能向上够（且 `toolgate` 与 tools **同为 tier 2**，迁过去会构成同层依赖，矩阵纪律禁止）。三包的判据与导出面**逐字未变**（包内零 kernel 内部依赖）；同批清理仓内 18 处「同仓/本仓的 netguard」注释残留（纯注释、零行为变化） | **① 若你 import 过这三个包**：改指 `github.com/oagentkit/guardx/netguard`、`github.com/oagentkit/guardx/ticket`、`github.com/oagentkit/tools/cmdpolicy`（API 逐字未变；上游 guardx ≥ v0.29.0 / tools ≥ v0.16.0 起提供）。<br>**② 必须 `go mod tidy`**：依赖面变化（`oagentkit/tools` 从本仓依赖图移除）。<br>**③ 只用其余内核包的宿主**：既有包导出面逐字未变 ⇒ 行为零变化。 |
| **v0.72.2**（已发版，tag `v0.72.2`） | **台账清账 + 两条门禁判据**（§42，PR #484）：① `fault-injection` 统一取证——`cmd/raceprobe` 参数化（`-race=false`/`-run`/`-v`/`-label`），与 race 共用同一套形态判据与取证（失败时给形态结论 + 落盘原始输出），cgo 随开关注入；② `hygiene` 补第二条判据——被跟踪**且命中 .gitignore** 的文件即红（`git add -f` 强加的文本产物 / 陈旧跟踪，魔数判不到这一类）；③ 台账 §42 把承接链末端的建议逐条处置（2 落地 / 2 结论 / 1 定案 / 3 开单 #481–#483）| **① 下游无需任何动作**：`raceprobe` 导出包与内核包公开契约面**逐字未变**（改动全在 `package main` 的 CLI 与 Makefile 接线），依赖地板零变化 ⇒ **无需 `go mod tidy`**。<br>**② 若你复用本仓 Makefile**：`make fault-injection` 现在走 raceprobe，失败时多出取证目录与形态结论（行为增强，退出码语义不变）；`hygiene` 多判一类文件。 |
| **v0.72.1**（已发版，tag `v0.72.1`） | **全量文档与现状对齐**——33 处过期表述更正（PR #478）：CI 分层七→六作业（integration 层迁 goagent-runtime）、README 分支保护「两条检查」→ 实际六条 required contexts、口径数字按实测更新（52 包 / 54 module id / 12 规则 / 463 测试）、已迁包引用改指新家（`toolname`→oagentkit、`pii`→oekit/pkg）、AGENTS 与 USAGE「CI 只执行 make check」自相矛盾统一 | **① 纯文档，零行为变化**：内核包公开契约面逐字未变、依赖地板未动 ⇒ 无需 `go mod tidy`。<br>**② 若你读本仓文档**：CI 作业数、required context 数、包数等口径已按实测刷新。 |
| **v0.72.0**（已发版，tag `v0.72.0`） | **raceprobe 判定层下沉为可导入包**（§26 建议 1 清偿）：race 门禁的六种失败形态判定（data-race / test-hang / startup-wedge / test-failure / unknown / none）此前锁在 `cmd/raceprobe`（`package main`）里，别的门禁与宿主 import 不到、各写一遍必然各判一套 ⇒ 判定层下沉为新包 `raceprobe`（`FailureKind` + 6 常量、`Verdict`、`Classify`、包级筛选函数全部导出，只依赖 stdlib），`cmd/raceprobe` 改 import 消费，夹具同源；policy 登记 + api-lifecycle 定 **Experimental**（与 archcheck / roundguard 同款理由：判定面随口径演进会增删） | **① 下游无需任何动作**：零导出符号消失、零既有签名变更、依赖地板零变化 ⇒ **无需 `go mod tidy`**。<br>**② 新增可复用面**：宿主可 import `raceprobe.Classify` 复用六种形态判定，不必自写第二份。 |
| **v0.71.0**（已发版，tag `v0.71.0`） | **废弃原语清零**：上游 oekit/pkg v0.59.0 删除 v0.58.2 标记 Deprecated 的 toolname 包（强制更换生效）；kernel 自 v0.70.0 起已用 github.com/oagentkit/toolname，本版仅抬依赖面（oekit/pkg v0.58.2 → v0.59.0），依赖图不再含任何已删除包 | **① 零行为变化，下游无需任何动作**。<br>**② 若你仍在 import `github.com/oekit/pkg/toolname`（≤v0.58.x）**：改指 `github.com/oagentkit/toolname`（API 逐字一致）。 |
| **v0.70.0**（已发版，tag `v0.70.0`） | **toolname 归位 oagentkit（概念归属纠偏）**：v0.69.0 把 toolname 上移到 oekit/pkg——归错位了：实现虽纯 stdlib，概念域是工具治理（与 toolgate/guardx 同族），oekit 只收纯基础设施。本版仓内消费方（ptc / tooldisclose）import 再改指 github.com/oagentkit/toolname（v0.1.0 独立仓）；oekit/pkg v0.58.2 的同名包标 Deprecated。**分层口径确立**：纯基础设施→oekit；工具/沙箱/执行安全→oagentkit；编排判据→kernel；装配/参考实现→runtime | **① 若你跟着 v0.69.0 改指过 `github.com/oekit/pkg/toolname`**：再改指 `github.com/oagentkit/toolname`（oekit 那份 v0.59.0 已删）。<br>**② 必须 `go mod tidy`**：依赖面变化。 |
| **v0.69.0**（已发版，tag `v0.69.0`） | **toolname 上移 oekit/pkg——方向已被 v0.70.0 纠偏**：kernel 的 toolname 包删除（破坏性 minor），按「机制与宿主无关」上移 oekit/pkg v0.58.0（API 逐字未变）；同批对账结论：netguard 维持现状、watchdog 留 kernel（深耦合编排判据）、ticket 留待下批 | **① 该版是中间态，直接用 v0.70.0+**——若曾按本版改指 `github.com/oekit/pkg/toolname`，最终归宿是 `github.com/oagentkit/toolname`。<br>**② 必须 `go mod tidy`**：依赖地板 oekit/pkg v0.57.0 → v0.58.0。 |
| **v0.68.0**（已发版，tag `v0.68.0`） | **三个演示迁往 goagent-runtime，examples/ 清空**（-3842 行 / 19 文件）：编排演示与容器后端参考实现归装配层；faultmatrix 行十四（真实 NetworkPolicy 验证）判据随迁 runtime（声明面改名 TestNetworkPolicyDeclarationFailClosed，判据逐字不变）；taskboard 指纹判据 2→1；backend 契约面 / faultmatrix 矩阵本体 / worker 生命周期契约留 kernel 逐字未变 | **① 若 import 过这三个演示**：改指 github.com/yi-nology/goagent-runtime 的 examples/reference-runtime、examples/worker-runtime、containerbackend（API 逐字未变）。 |
| **v0.67.0**（已发版，tag `v0.67.0`） | **PG/SQLite 参考实现迁往 goagent-runtime，撤驱动破例**：examples/{pgstore,sqliteadapter} 删除（-7969 行 / 38 文件），主 module 依赖图不再含任何数据库驱动（pgx/modernc 撤除）——「内核不实现存储」从门禁破例变结构事实；门禁跨仓降级（deploycheck 支持 https 跨仓引用、storagecheck 支持 external: 门面，判不了的明说判不了）；CI integration job 随迁 runtime | **① 若 import 过这两个包**：改指 github.com/yi-nology/goagent-runtime 的 pgstore / sqliteadapter（API 逐字未变，判据套件同源）。<br>**② 必须 `go mod tidy`**：依赖地板变化。 |
| **v0.66.1**（已发版，tag `v0.66.1`） | **stress 测试的 PID 文件隔离到测试专属 TMPDIR**（#465 根因）：PID 文件此前写在固定系统路径 `os.TempDir()/gak_ptc_child_pid`，而 `os.TempDir` 是**所有进程共享**的 ⇒ 两个测试进程并发时（多 worktree / 并行会话同时跑测试）互相读写——后启动的 `readChildPID` 会**删掉对方写的文件**、读到**对方子进程的 PID**，于是 SIGKILL 杀掉的是别人的子进程而自己的没人杀（父侧 20s 超时「挂死」），或读到对方**已退出**的 PID（「应存活却已死」）。**两种失败形态、两次实录同源**。对照实验：4 路 CPU 负载 `-count=10` 全绿（**CPU 不是触发条件**）、两进程并发**必失败**（**并发才是**）⇒ 此前单跑/负载下绿是运气。修法：`t.Setenv("TMPDIR", t.TempDir())`，子进程源码**不用改**（`TMPDIR` 在 `procx.ChildEnv` 白名单里） | **① 零影响**：只改**测试文件内容**（未增删文件、未动生产代码），内核包公开契约面**逐字未变**，依赖地板未动 ⇒ **无需 `go mod tidy`**。<br>**② README 工程底座三数不变**。<br>**③ 若你在多进程并发跑本仓测试**：本版起 stress 测试不再互踩。 |
| **v0.66.0**（已发版，tag `v0.66.0`） | **破坏性变更**：`pii` 与 `internal/subhub` **上移 `oekit/pkg`**（oekit/pkg#571，v0.57.0）——边界审计结论是 pii 属**文本安全原语、不认识编排**且在本仓零消费方，subhub 是四包共用的通用并发数据结构，按「机制与宿主无关 → 归上游基础库」上移。本仓 `github.com/yi-nology/goagent-kernel/pii` 与 `internal/subhub` **删除**，Stable 级 pii 同步退役；配套 policy 摘模块登记/scan_roots/requires/wrappers、`harness-test-no-direct-global-setter` 规则随 pii 退役（`check_kind` 判定面留在 archcheck 供宿主复用）、api-lifecycle 62→60、README/USAGE 口径重算（61→59 module id、53→52 包）。依赖地板 `oekit/pkg` v0.52.0 → **v0.57.0** | **① 外部若 import 这两个包**：改指 `github.com/oekit/pkg/pii` 与 `github.com/oekit/pkg/subhub`（**API 逐字未变**）。<br>**② 必须 `go mod tidy`**：依赖地板抬升（跨 5 版），不跑就报 `updates to go.mod needed` 且不提真正原因。<br>**③ 仓内四个消费方**（eventbus/guardset/netguard/apilifecheck）的 import 已重指，零行为变化。 |
| **v0.65.1**（已发版，tag `v0.65.1`） | **状态词的同行归属歧义修复**（#411 / #412 / #413 不再被误判）：parse 此前对**整行**算一个状态、行里所有单号**无条件继承** ⇒ 「那条建议仍开着」被安到 #411 头上（它 2026-11-17 就关了），判据 5 拿它对账 GitHub 判出「关了没登记」。两道机制：① **按出现次数判归属**——同一行非 PR 单号出现多于一次 ⇒ 不猜（用不去重的计数：那行 #411 只有一个编号却出现在两处语境，去重后长度是 1、照样误判）；② **围栏代码块不进单号视图**——三反引号围栏内的行由主循环跳过（代码块是示例与引用，不是台账叙述；这条是被**实录三次**逼出来的：每次修完，说明这个歧义的文档自己又把原文引进来）。**代价如实登记**：多单号正文行的辅助声明、代码块内的单号都不再产生出现点 ⇒ 判据 1/3 对这两类更宽松；**权威面（登记表状态列）完全不受影响** | **① 零影响**：`internal/ledgerstatus` 是 **internal 包**，下游本就不可 import；内核包公开契约面**逐字未变**，依赖地板未动 ⇒ **无需 `go mod tidy`**。<br>**② 只影响本仓自己的门禁**（`ledgercheck` 判据 1/5 与 `deploymatrix`）如何解析台账正文。<br>**③ 顺带开 #465**：ptc 的 `TestStressSubprocessKillMidRunReaps` 负载下偶发红（已实录两次），按「同形面要系统审」开单。 |
| **v0.65.0**（已发版，tag `v0.65.0`） | **判据 5 的第二级数据源（#455 离线兜底）+ #447 的后半句（例外「已失效待删」标记）**：① **#455 兜底**——判据本体已随 **v0.64.2** 出货（实时 `gh` 拉取 + 纯函数判定），本版补的是**第二级**：实时拉不到（没装 gh / 离线 / 没 token）时**退回仓内快照** `docs/issue-state.yaml`——「跑不起来的门禁」与「没有门禁」是同一件事，而开发者机离线跑 `make check` 是常态。**判定仍走同一条 `checkIssueStates`**（换的只是喂给它的状态表；本版还**删掉了第一版自带的第二套 `Reconcile` 判定**——两套判定各判一套必然漂移）。三级处置：实时成功 ⇒ **不带任何快照标签**（兜底只在主路径失败时出现，否则「实时为主」名存实亡）／ 实时失败 + 快照在 ⇒ 退回并**大声回显**（`Tag` 进成功行、`Warn` 进 stderr，两处都点明「数据可能不是当下事实」）／ 两级都拿不到 ⇒ **exit 2「给不出结论」**。快照解析层一并收紧（未知取值 / **非数字键** / **空快照** 三种形态都报错——空结果与「查不到」不可分，拿它对账等于凭空判「全部一致」）。**时效不做红判据**（会让门禁红在与被判事实无关的原因上），只回显日期与天数，刷新走 `make issue-state`。② **#447 后半句**——v0.64.0 落地的是「僵尸例外当天即红」，而登记行处方里还有**「要么被显式标记为已失效待删」**这一半没落地 ⇒ 新增 `Exception.status`（`stale`，**可选**，缺省行为逐字不变）：取值域**封闭**（拼错当天自校验红）、标了 `stale` 却**仍命中**违反判**矛盾红**（否则 stale 成了「写一次永久免检」的免死金牌）、已标记的例外不参与僵尸判定但**报告逐条点名**。③ **合流裁决**——`archcheck/policy.go` 在基线上已 **395/400** 行（与 #448 判的 398/400 同病），加字段即 413 越界 ⇒ 按关注点拆出 `policy_decl.go`（声明面 348 行）+ `policy.go`（装载面 78 行），**零行为变更**。④ **台账两处声明缺陷订正**（新判据上线当场抓到：§16 用英文 `closed` 不是标记词 / §18 建议的「仍开着」被按行解析错挂到单号头上）+ **文档面同步**（USAGE/AI-GUIDE/AGENTS 判据面与条数，三处「10 条规则」实为 13）。**判定面新增**（新可选 policy 字段 + 判据 5 的第二级数据源） | **① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零既有接口加方法 / 零既有导出函数签名变更）。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。<br>**③ 若你复用 `archcheck` 的例外机制**：本版新增**可选**字段 `Exception.status`（省略 = 在用，行为与旧版**逐字相同**）；**老 archcheck 加载带 `status` 的 policy 会因严格 YAML 解析失败（退出码 2）** ⇒ 升级 archcheck，或删掉那行。<br>**④ 若你在 Go 代码里构造 `archcheck.Rule`**：本版**未动** `Rule`（无字段变化）。<br>**⑤ 若你在自己仓跑 `ledgercheck`**：判据 5 现在是**两级数据源**——有 `gh` 就实时查；没有则读仓内快照，**快照缺失且实时也失败才 exit 2**。要让离线兜底可用，把 `docs/issue-state.yaml` 一起带走（或跑 `make issue-state` 生成）。 |
| **v0.64.3**（已发版，tag `v0.64.3`） | **P0-A 任务侧修复**：权威强释的原子接缝——`forceReleaseAuthoritative` 默认是两步（先 `Release` 后 `appendAudit`），两步之间死掉或留痕失败 ⇒ **租约已释放、方法返回错误、本次介入缺成功审计**。新增 **`ForceReleaseAtomic` 可选接缝**（实现方把「校验并释放 + 写审计行」收敛进同一事务，审计失败则释放一并回滚），`StorePlane` 探测到即走原子路径；同版修两个同族缺口（探测点必须在分步 Release **之前**；`ErrNoAuditor` 检查移到任何状态变更之前）。参考实现：新表 `pgref_control_audit`（留账与释放同事务、`operator` 列 `CHECK (operator <> '')`） | **① 未实现 `ForceReleaseAtomic` 的存储保留两步形态**（调用点如实声明窗口），实现即自动走原子路径。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**。<br>**③ 导出面**：新增 `ForceReleaseAtomic` 可选接口（源码级 additive），既有签名不变。 |
| **v0.64.2**（已发版，tag `v0.64.2`） | **台账 ↔ GitHub issue 状态双向对账**（判据 5，#455）——补的是「台账说已合入而 issue 还开着**没有任何判据会红**」这个缺口（#427/#428/#430 就是这么关漏的）：① 判据**双向**（漏关 / 关了没登记 / 单号查不到都判红；台账没表态不判），只判登记面单号；② **判据面与数据源分离**——判据是纯函数（喂构造状态表即可离线钉住），数据源可注入（生产走 `gh issue list`，测试走构造数据），**拉不到 ⇒ exit 2**（给不出结论，不是「没有违反」）；③ **CI 接线**：`ledgercheck` 本就在 `make unit`，`ci.yml` 补 `permissions: issues: read` + `GH_TOKEN`；④ 子进程走 `procx.ChildEnv`（不继承全量 environ）+ `egress_paths` 登记。**首跑即抓到 #411 的状态标记词歧义归属**（已改台账措辞 + 登记为已知边界） | **① 零影响**：`cmd/ledgercheck` 是 `package main`（不可 import），内核包公开契约面**逐字未变**，依赖地板未动 ⇒ **无需 `go mod tidy`**。<br>**② 若你复用本仓的 Makefile 门禁**：`make ledgercheck` 现在需要 GitHub 访问（拿不到 ⇒ exit 2 而非跳过）——需要 `gh` 已安装并登录，或给 `GH_TOKEN`。<br>**③ 本版是门禁工具的能力增强**，不影响任何运行时行为。 |
| **v0.64.1**（已发版，tag `v0.64.1`） | **P0-B 修复**：持续存储故障下租约会话的本地资格截止——`LeaseSession.renewLoop` 此前只在**明确收到** `ErrLeaseLost` 时才失格，续期持续超时则无限重试，而权威存储侧租约早已过期、任务可能已被新 worker 接管（**双执行者窗口**）；注释里「下一拍必然收到 `ErrLeaseLost`」的隐含前提在持续网络分区下不成立 ⇒ 改为**保守资格截止** `T_last_confirmed + TTL − TTL/10`，超时未确认即主动失格并取消执行 ctx（**不等存储回包**；本地截止只负责提前撤销资格，授予权仍归权威存储）。`LeaseSessionConfig` 新增 `Now` 时钟注入点（零值 = `time.Now`，`@seam` 三约束） | **① 使用 `worker.NewLeaseSession` 的宿主无需改动**：注入过自定义 `RenewInterval` 的照旧，默认形态自动获得持续故障失格保护。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 在 v0.64.0..v0.64.1 区间内未动）。<br>**③ 多副本部署建议**：在故障注入矩阵补「worker↔DB 持续分区 > TTL」场景，断言旧 worker 在截止后不再发出新的业务动作。 |
| **v0.64.0**（已发版，tag `v0.64.0`） | **清偿 v0.63.0 收尾时登记的三条残余边界**（#447 / #448 / #449）：① **#447 僵尸例外**——policy 的 `exceptions` 段里**命中不到任何违反**的例外此前被静默忽略（站点已修复 / 文件已拆 / 规则口径变了之后，那条例外再也命中不到东西而门禁照样绿 ⇒ 已不需要的豁免可以**永久留在声明面上**）⇒ 按 `rule + path + line` **三元组对账**，命中不到 ⇒ **阻断级**且点名到那一处（例外指向的东西本身有问题时由自校验点名，不叠第二条）；② **#448 `eventbus/tail.go` 398/400 行**按关注点拆成 `tail.go`(230) + `tailstore.go`(108) + `tailoption.go`(74)，同包 `tail_cursor_test.go` 384/400 一并拆成 gap(208) + form(195)——**导出面与测试名逐字相同**；③ **#449 新增 `check_kind: test_no_direct_call`**（+ `Rule.ForbidCalls` 导出字段）：测试面不得直调包级全局态 setter，判**调用点**（`go/ast`）不判文本，被禁符号与受管辅助声明在 policy 侧。**判定面新增**（新 `check_kind` 取值 + 新增导出字段 + 僵尸例外判据） | **① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零接口加方法 / 零签名变更）。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。<br>**③ 若你复用 `archcheck` 的例外机制**：本版起**命中不到任何违反的例外会判红**——你若曾登记过已失效的例外，升级后门禁会转红，**删掉那条例外即可**。<br>**④ 若你启用 `test_no_direct_call`**：policy 里必须声明 `forbid_calls`（被禁符号清单），否则自校验当天即红。 |
| **v0.63.0**（已发版，tag `v0.63.0`） | **承接 v0.62.2 未落地的部分**（v0.62.2 出货的是两条纪律的**规则本体** + 9 处同形面处置 + 8 条例外登记；本版把那 8 条例外里**形态为真**的 6 处真违规改成代码，并清偿另外两条登记行）：① **#442 `pii` 全局态真缺陷**——包内**两套相反**的还原约定（`SetEnabled(false)` vs `SetEnabled(true)`）+ `restore(t)` **只在 cleanup 还原、用例开始时不重置** + 还原在 goroutine 里跑且**超时静默** ⇒ `-count>1` 概率红（基线 `-count=20` **红 19 次**，main 上逐字复现 ⇒ 预存在）⇒ 收进**一个**包级 helper（setup **同步**重置 + cleanup **同步**还原 + **还原超时必须让用例失败**），包内 **105 处**直接调 `Set*` 的写法全部改走 helper；② **#443 `eventbus` 拆分与缺席断言可观测化**——`tail_test.go` 379/400 行 ⇒ 按关注点拆成 5 个（**最大 181 行**、余量 ≥219）+ 两处「50ms 内不该有事件」改成「**先等机制跑过 N 读拍**（可观测事实）⇒ 再判缺席」（旧写法在同一个坏实现下**是绿的** ⇒ 白测坐实）；③ **判据精度修正**——`defer_cleanup` 把**非阻塞 drain**（带 `default` 的 select，永不挂起 ⇒ 不等任何东西，是断言不是 join）误判成等待点 ⇒ 给出「把 drain 塞进 defer」的**错误处方** ⇒ 收窄口径 + **双向**用例钉住；④ **6 处 eventbus tailer goroutine 补 defer 收尾 join**（上一版挂例外的真违规，改代码而非继续挂账）；⑤ **例外从 8 条降到 2 条**（删掉已改代码的 `eventbus/**` 6 条；剩 2 条都是真债，各带 `until` 与拆除条件）——**例外不许无声失效**（僵尸例外已登记为 #447）。**判定面未新增**（不新增字段、不新增 `check_kind` 取值）⇒ 宿主 policy 无需改动 | **① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零接口加方法 / 零签名变更）。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。<br>**③ 若你复用 `archcheck` 并已启用 `defer_cleanup`**：本版**收窄**了它的判定口径——**带 `default` 的 select 接收不再算等待点**（此前会被判红并给出「塞进 defer」的错误处方）⇒ 你的 policy **无需改动**，但若此前为这类站点登记过例外，那条例外会变成命中不到任何违反的**僵尸例外**（建议删掉）。<br>**④ `examples/*` 一律 Experimental**：本版**未改**它们任何既有导出签名。 |
| **v0.62.2**（已发版，tag `v0.62.2`） | **两条纪律从「散文」变成「门禁判定」**（#440 收尾等待点必须写成 `defer` / #441 断言的尺子必须是可以观测事实）：① **新增规则 `harness-test-defer-cleanup`**（`check_kind: defer_cleanup`）——用例内起的 goroutine，其**收尾等待点**必须写在 `defer` 里（等待点写在普通语句里、且它之前有 `t.Fatal` 族 / `runtime.Goexit` 的直接调用 ⇒ 失败路径上 Goexit 会跳过这一步 ⇒ goroutine 实际未被 join，而兄弟规则判它合规）；② **新增规则 `harness-test-wallclock-ruler`**（`check_kind: wallclock_ruler`）——测试面不许拿裸 `time.Sleep` 当尺子（窗口长度单独决定结论 ⇒ 机器一忙就假红 / 机制抢跑就白测；**轮询退避与 goroutine 体里的 sleep 显式豁免**）；③ **新增「执行上下文」作用域模型**（`archcheck/testscope.go`）——`t.Run` 子用例闭包里的 `t.Fatal` 终止的是子用例、不是父用例（不区分就会把「子用例失败」误当成「父用例被提前终止」⇒ 假红）；④ **`archcheck.Rule` 新增字段 `ManagedHelpers`**（源码级 additive）——policy 侧登记「受管辅助」，其函数体内部的 sleep 不判；⑤ **9 处同形面已处置**（5 处收尾等待改成 `defer` 形态 / 4 处墙钟窗口改成「等可观测事实」——其中 `drain` 那处**原写法睡 50ms 而轮询间隔是 200ms ⇒ 窗口落空 ⇒ 一直在覆盖「入口即 Done」分支（白测）**）；⑥ **8 条例外登记**（带 `until` 与拆除条件）：`eventbus/**` 6 处与 `pii/**` 1 处（并行工作线在改）、`examples/pgstore` 1 处（需 PG 侧可观测事实，本机无 PG 验不了）。**判定语义零变更**（内核包导出面除 `archcheck.Rule` 新增一个字段外逐字未变） | **① 只用内核包的宿主不受影响**：内核包的公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零既有接口加方法 / 零既有导出函数签名变更）。<br>**② 若你在自己仓里复用 `archcheck`**：想用新规则需要 `archcheck` ≥ v0.62.2，并在 policy 里写 `check_kind: defer_cleanup` 或 `check_kind: wallclock_ruler`（**取值域封闭** ⇒ 旧版 archcheck 加载这两行会因严格 YAML 解析失败、退出码 2 ⇒ 升级 archcheck，或删掉那行）；`managed_helpers` 字段同理（写了但旧版不认 ⇒ 同样解析失败）。<br>**③ 若你在 Go 代码里构造 `archcheck.Rule`**：新增了 `ManagedHelpers` 字段 ⇒ **具名字段字面量逐字兼容**；**位置参数结构体字面量会编译不过**（需补一个零值字段）。<br>**④ 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。<br>**⑤ 若你在本仓跑 `make check`**：新规则会扫本仓测试文件 ⇒ 收尾等待点必须写成 `defer` 形态、裸 `time.Sleep` 必须换成「等可观测事实」（例外按 `rule + path + line` 精确登记）。 |
| **v0.62.1**（已发版，tag `v0.62.1`） | **三条台账登记行一次清偿**（#431 用例收尾形态 / #432 断言的尺子 / #433 门禁命令的扫描成本）：① **9 处尾随收尾点改成 `defer` 形态**（#431）——原先是「用例末尾的普通语句」⇒ 用例中途 `t.Fatalf` 触发的 `runtime.Goexit` 会**跳过**它们 ⇒ 失败路径上 tailer 实际未被 join（7 处末尾改 `defer`；2 处**中途**改闭包内 `defer`——不能一刀切 defer ⇒ 会把 join 拖到用例结束、**语义改变**）；② **8 处「拿墙钟窗口当尺子」的断言改成「等可观测事实」**（#432）——固定 `sleep` + 计数断言在负载下会**假红**（`worker` 的续期用例实测红）或**白测**（负向断言 + 窗口 ⇒ 机制压根没跑起来也绿）；③ **`cmd/seamcheck` 的扫描成本与依赖规模脱钩**（#433）——去掉 `NeedDeps`（依赖走导出数据）+ 扫描器给可注入接缝（用例扫合成夹具，真仓扫描仍由 `make seamcheck` 门禁承担）⇒ 该包用例从 **5.384s** 降到 **0.394s**（⚠ 台账那条 489s 基线在本机复现不出来，如实登记）。**判定语义零变更** | **① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零接口加方法 / 零签名变更）。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。<br>**③ `examples/*` 一律 Experimental**：本版**未改**它们任何既有导出签名。<br>**④ 若你复用 `cmd/seamcheck`**：它是命令（`package main`，不可被 import）⇒ 产出**逐字不变**（接缝声明 93 / 调用点 439 / 三条约束写齐 5）；但**首次在冷缓存机器上跑会多花几秒**（导出数据要先编译依赖）。 |
| **v0.62.0**（已发版，tag `v0.62.0`） | **终审留后续的 5 条一次清偿**（roundguard 判据面 / 导出面口径 + 文档表述）：① **散文判据腿形态无关 + 覆盖全部 `### ` 小节**（F2）——原先只认**全角**冒号、且只覆盖「矩阵要钉的那 14 个判据面小节」⇒ 在 §9.x 这类小节里新加一条判据时门禁**一声不响**；登记面从 11 小节 / 27 条扩到 **13 小节 / 29 条**；② **导出面计数器把「导出别名暴露出去的那一面」计入**（M2）——`type Alias = inner` 时 inner 的字段与方法原先**完全不可见**（本包零别名 ⇒ 实测值不变）；③ **警告回灌移到被评判回合之后**（M1）——原先模型先看到警告、后看到触发警告的那一回合（**语义倒置**，且配对不破 ⇒ 没有编译错误、没有断言会红）；④ **policy 登记数的措辞改成 module id**（M3）——**61 个 module id ≠ 63 个 root 目录**，此前 USAGE 写「61 个包目录」是把错误的口径写死成契约；⑤ **AI-GUIDE §5.3 门包表述改成现状**（M4）——本仓**已无门包**（`facades: []`），且门禁表里不再写死规则条数。**依赖地板无变化** | **① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零接口加方法 / 零签名变更）；`roundguard` 的导出面实测值仍是 **104** / 天花板 136。<br>**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 在 v0.61.0..v0.62.0 区间内**逐字未动**）。<br>**③ `examples/*` 一律 Experimental**：本版**未改**它们任何既有导出签名。<br>**④ 若你复用 `cmd/docsync`**：它是命令（`package main`，不可被 import），判据数的仍是同一个数（`modules` 段的 `- id:` 条目数）⇒ 实测值未变；但**用旧措辞「N 个包目录」写 USAGE 口径块**现在会被判红（那是本版故意收紧的措辞）。 |
| **v0.61.0**（已发版，tag `v0.61.0`） | **「用例内 goroutine 必须在用例返回前退出」从纪律变成门禁（#428）+ 门面判据文件按关注点拆分（#427）**：① **新增规则 `harness-test-goroutine-joined`**（测试面，`severity: error`）——测试函数（`Test*`/`Benchmark*`/`Fuzz*`）里起了 goroutine，而**该函数或它调用的包内函数（深度 ≤2）里没有等待点**（接收表达式 `<-ch` 或 `X.Wait()`）⇒ 红；豁免必须**helper 感知**（等待点常常写在 `stopTailer` 这类助手里，只看测试函数体 ⇒ 大量误报 ⇒ 真阳性被淹没）；② **policy 新增 `check_kind` 字段**——结构化执行体的选择（当前取值域 `goroutine_join`），**取值域封闭**（未知取值当天即红；写错的类别名若被当成「不是已知类别」，这条登记就静默降级成「不判」，失败形态是门禁**变绿**）；③ **`archcheck.Rule` 新增字段 `CheckKind`**（源码级 additive）；④ **`cmd/storagecheck` 门面判据文件按关注点拆成三个**（`facade.go` / `facade_derive.go` / `facade_coverage.go`）——**零行为变更**（判定语义与报错文案逐字未变）；⑤ **门禁运行口径**：测试阶段显式给单包超时（`TEST_GO_TIMEOUT := 15m`）——`cmd/seamcheck` 的全仓类型检查单包实测 489.346s，Go 默认 10m 无余量 ⇒ 负载下 `cover-gate` 必红（**只改本仓门禁怎么跑，不改任何判定**）。**依赖地板无变化** | **① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零接口加方法 / 零签名变更）。<br>**② 若你在自己仓里复用 `archcheck`**：想用新规则需要 `archcheck` ≥ v0.61.0，并在 policy 里写 `check_kind: goroutine_join`；**用旧版 archcheck 加载带这行的 policy 会因严格 YAML 解析失败（退出码 2）** ⇒ 升级 archcheck，或删掉那行（`check_kind` 不写时行为与旧版逐字相同）。<br>**③ 若你在 Go 代码里构造 `archcheck.Rule`**：新增了 `CheckKind` 字段 ⇒ **具名字段字面量逐字兼容**；**位置参数结构体字面量会编译不过**（需补一个零值字段）。<br>**④ 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 在 v0.60.0..v0.61.0 区间内**逐字未动**）。<br>**⑤ `examples/*` 一律 Experimental**：本版**未改**它们任何既有导出签名。<br>**⑥ 若你在本仓跑 `make check`**：单包超时改为显式 15m（此前吃 Go 默认 10m）⇒ **真挂死仍然红**，只是不再被机器负载放大出来的排队时间误判。 |
| **v0.60.0**（已发版，tag `v0.60.0`） | **新增 `roundguard`（回合守卫）——模型侧循环的回合结局分类 / 次数预算 / 无进展检测 / 组合结论 `Decide`**：① **回合结局分类**（`Reason` 10 值 + `Classify`）——把「这一回合的事实」映射成一个结局取值，并区分**失败型收敛**与**温和叫停**（`Reason.IsFailure()`）：「被上限温和收停」与「真的坏了」在宿主侧必须可分；② **回合次数预算**（`Budget` / `Used` / `Meter` + 三个 `Check*`）——额度维度（事实）与处置口径（策略）分成两张词表，额度一律**调用前**判；③ **无进展检测**（`Detect`，五形态 × 三档处置）——首档 Warn（回灌给模型一次自我修正机会）/ 停止档 Stalled（温和叫停，产物仍可用）；④ **组合结论 `Decide`**——结局终局优先 → 额度按策略分流 → 无进展命中 ⇒ stalled ⇒ 否则继续。本包定级 **Experimental**（v0.60.0 定级），**零内部依赖**（只依赖 `github.com/oekit/pkg/errors`）。**依赖地板无变化**（`go.mod` / `go.sum` 在 v0.51.0..v0.60.0 区间内逐字未动）；**既有包公开契约面逐字未变**（导出面审计三问全清：零导出符号消失 / 零既有接口加方法 / 零既有导出函数签名变更） | **① 只用既有内核包的宿主什么都不用做**：既有包导出面逐字未变、依赖地板零变化 ⇒ **无需 `go mod tidy`**。<br>**② 若你管的是模型侧循环**：可**可选**接入 `roundguard`——`go get github.com/yi-nology/goagent-kernel@v0.60.0` 后 `import "github.com/yi-nology/goagent-kernel/roundguard"`。本仓此前**没有任何一维**管「模型这一轮说了什么、干了什么、还要不要再干」（`MaxTurns` / `maxSteps` / `MaxRounds` 全仓零命中），`roundguard` 补的就是这一维。<br>**③ 本包只给判据面，不调模型**：判定语义归内核、判定依据归宿主、展示文案归宿主——接线形态见 [docs/USAGE.md](docs/USAGE.md) §3.18。<br>**④ 本包定级 Experimental**：会改、删、拆，不承诺兼容；升级风险自负。 |
| **v0.51.0**（已发版，tag `v0.51.0`） | **存储选型判据收紧 + 门面「同款集合」相等性机判 + #425 快照序号溢出守卫**：① **#425 真缺陷修复**——`examples/pgstore` 快照面此前**缺**序号溢出守卫（`Seq > math.MaxInt64` 被静默接受 ⇒ 高位被截成负数落库 ⇒ 单调比较整个反向 ⇒ 恢复点可被任意覆盖），现与 SQLite 侧**同款**当场拒绝，并把失败登记进共享失败槽（`failBatch`）⇒ 调用方拿得到失败，不静默落库；② **门面判据 F1–F5**——两侧门面的「同款集合」相等性从**散文断言**变成**机判事实**（声明面 ↔ 代码派生集**双向**相等 / 清单形状 / 各门面访问器集合相等 / 每个访问器有落点或已登记 / 有方法级落点必须登记 `facade:` 段）；③ **`kind` 封闭值域**（不再是自由标签：拼错当场红）+ **写者数交叉校验 W1/W2**（`scenario_writers` 与 `max_writers` 两张表互相钉住，改弱任一张当场红）；④ **「N 个面」窄义措辞清理 21 处**（PG 7 + SQLite 14）。**依赖地板无变化** | **① 若你直接用了 `examples/pgstore`**：⚠ **行为面变更**——溢出序号现在**当场拒绝**（此前是静默接受并落库）。拒绝时返回的是**裸 `errors.New`**（与 SQLite 侧**逐字一致**）⇒ 按 `errors.Is(err, checkpoint.ErrStaleSeq)` 分流的调用方**判不到**该错误（**已知边界**，不是本版的缺陷）。<br>**② 若你把本仓的 `docs/DEPLOY-MATRIX.yaml` 复制到了自己仓**：新增 `requires_multi_writer` 字段（**缺省即红**）⇒ 每条能力都要显式写 `true` / `false`（它表达「这条能力是否要求多写者」）。`cmd/deploymatrix` 与 `cmd/storagecheck` 都是**命令**（`package main`，不可被 import），只在**本仓**跑。<br>**③ 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号消失 / 零接口加方法 / 零签名变更）。<br>**④ 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 在 v0.50.2..v0.51.0 区间内**逐字未动**）⇒ 按 Go 1.17+ 的模块图裁剪，连这条间接依赖都看不到。<br>**⑤ `examples/*` 一律 Experimental**：本版**未改**它们任何既有导出签名。 |
| **v0.50.0**（已发版，tag `v0.50.0`） | **真 SQLite 适配器 + PG 跨面事务 + 存储选型判据机判化（#424）**：① **删掉 `examples/sqlitestore`**——它名为 SQLite、实为 **flock + JSONL 的文件后端**（22 个 .go / 3358 行）；② **新增 `examples/sqliteadapter`**——真 SQLite（`modernc.org/sqlite` v1.61.0，**纯 Go 无 cgo**），**11 个面访问器**共用一份事务域（同一个写句柄 + 同一个库文件，一次提交），契约面与 PG 侧对齐；③ **`examples/pgstore` 新增 `Store.Apply`**——把各面收进**同一次提交** + 共享失败槽（源码级 additive，既有接口逐字未变）；④ **新增 `cmd/storagecheck` + `docs/STORAGE-SELECTION.yaml`**——「何时用 SQLite / 何时用 PostgreSQL」从散文变成**门禁可判**的规则（单机多进程共享写 ⇒ 硬边界判 PostgreSQL）。**依赖地板变化**：主模块新增 `modernc.org/sqlite` v1.61.0（纯 Go，无 cgo） | **① 若你直接复用了 `examples/sqlitestore` 的文件后端**：该包**已删除** ⇒ 改用 `examples/sqliteadapter`（接口形态不同：SQLite 侧 `Apply(ctx, fn func(tx *Tx) error)`，事务句柄显式传递）；旧数据目录**打不开**（JSONL ≠ SQLite 文件）⇒ 数据需自行迁移。<br>**② 若你 import 了 `examples/sqlitestore`**：编译期即红（包已删）⇒ 换 `examples/sqliteadapter`。<br>**③ 只有 import `examples/sqliteadapter` 的宿主需要 `go mod tidy`**：那类宿主的 go.mod 会多出 `modernc.org/sqlite` v1.61.0（间接依赖，纯 Go 无 cgo）；**只用内核包的宿主连这条依赖都看不到**——模块图裁剪下它不进你的 go.mod / go.sum（实测：空模块 `go get` v0.50.0 后，两个文件里 `modernc.org/sqlite` **零命中**）。<br>**④ 只用内核包的宿主不受影响**：内核包的公开契约面**逐字未变**（`sideeffect.Store` / `checkpoint.StoreCAS` / `eventbus.TailStore` 等一个字都没改），本版改动全在 `examples/` 与一个新命令里；`examples/*` 一律 **Experimental**，无兼容承诺。 |
| **v0.48.0**（已发版，tag `v0.48.0`） | **单机落库参考实现扩到四个面（#419）**：① `examples/sqlitestore` 从只覆盖任务板扩为覆盖 **任务板 / 副作用闸 / 进度快照 / 事件面** 四个面，**共用一份事务域**（同一把锁、同一个日志、一次提交）；② **日志格式升级为 v2**（一行 = 一个事务批次，跨面变更同批落盘 ⇒ 跨面不会「一半已提交」），v1 旧格式（一行 = 一条任务记录）**仍可读**，旧目录不必迁移；③ 新增导出符号 `Store.Apply` / `Tx` / `TaskStore` / `EffectStore` / `SnapshotStore` / `EventStore`（本包定级 **Experimental**，无对外兼容承诺）；④ 示例现在实现了 `sideeffect.OwnedResolveStore`（带对账占用凭据的裁决入口）。**契约本身逐字未变**（`sideeffect.Store` / `checkpoint.StoreCAS` / `eventbus.Store` 一个字都没改），**依赖地板无变化** | **① 若你直接复用了本包的文件后端**：旧数据目录可直接打开（v1 仍可读）；但**新写入的日志是 v2 批次格式**，用旧解析器读会失败 ⇒ 跟着本仓版本走。<br>**② 若你在自己仓里实现了 `sideeffect.Store` / `checkpoint.StoreCAS` / `eventbus.TailStore`**：本包现在多了一份**落库形态参考**（三项联查 / 执行意图 / 对账占用 / 水位线游标的完整落法），可对照抄；**接口逐字未变 ⇒ 既有实现无需改动**。<br>**③ 无需 `go mod tidy`**（本版无依赖地板变化）。 |
| **v0.47.0**（已发版，tag `v0.47.0`） | **判定词表登记门禁第十四阶段（all 锚点 + 族归属派生）+ 第十五阶段（单号状态派生）**：① **关系锚点支持「锚住两端共有取值的全集」**——`archcheck` 的 `word_tables.relations[].anchors` 新增 `all` 形态（yaml：`anchors: {a: [{all: true}], b: [{all: true}]}`），此前只能逐个写锚点常量；② **`family_exclude` 字段废弃**——族归属改由**表自己声明**（`not_registered` 段写 `pkg` + `prefixes`），写了 `family_exclude` 的 policy 会被 `harness-policy-self-check` **阻断报红**（字段仍留在结构体里，只为给出迁移提示——删字段会让宿主 policy 静默失效）；③ **新增导出字段** `archcheck.WordTableAnchor.All` 与 `archcheck.WordTableNotRegistered.Pkg/Prefixes`（源码级 additive）；④ **新增 `internal/ledgerstatus` 与 `make ledgercheck`**：台账的单号状态从此机器可查（已开单 vs 待清偿），部署清单的 issue 证据**只有在对应单号仍开着时**才算缺口。**依赖地板无变化** | **① 若你在自己仓里复用 `archcheck` 且 policy 里写过 `word_tables.relations[].anchors`**：逐个锚点仍逐字可用；想锚住「两端共有取值的全集」改写成 `all: true` 形态即可（语义是「共有取值的集合」，不是「全部取值」）。<br>**② 若你的 policy 写过 `family_exclude`**：**会红**（自校验阻断，不是警告）——把豁免改成让**归属表自己声明前缀**：在 `not_registered` 段加 `{pkg: <你的包>, prefixes: [<族前缀>], reason: ...}`，并从 `family_exclude` 里删掉那条（同一条族里常含另一张表的成员 ⇒ 给按前缀的豁免口时**必须写理由**）。<br>**③ 只读台账 / 只看部署矩阵的宿主不受影响**：`internal/ledgerstatus` 是 internal 包（Go 可见性规则挡住外部引用），`make ledgercheck` 是本仓自用门禁，不改任何导出面。<br>**④ 无需 `go mod tidy`**（本版无依赖地板变化）。 |
| **v0.45.0**（已发版，tag `v0.45.0`） | **判定词表登记门禁第十二阶段：形状拆分 + 多前缀族 + 同形撞车关系**（`archcheck` 定级 Experimental，无对外兼容承诺，但**源码级变更须告知**）：① **载体形状拆分**——此前执行体把「切片」一律当成分组表，于是「一张扁平表写成常量切片」（元素是**常量名**）的判定表**登记不进来**；现在按**元素形状**分：常量名 ⇒ 扁平表（取值可派生，可登记成判定词表），复合字面量 ⇒ 分组表（仍只能写 `kind: input`）。② **族前缀支持多个**——`family` 接受字符串或列表（`family: [Status, Phase]`）。③ **新增 `harness-word-table-value-collision`**——两张判定词表有同形取值却没在 `word_tables.relations` 里声明关系 ⇒ 红；声明了却没撞车 ⇒ 红（**声明失真**：今天没关系，将来一旦同形会无声生效）；手抄文案**按关系改口**（`same` 列全部候选常量 / `distinct` 要求在本包另起一个名字）。④ **`archcheck.WordTable.Family` 字段类型由 `string` 改为 `FamilyPrefixes`**（源码级）。**依赖地板无变化** | **① 若你在自己仓里复用 `archcheck` 且在 Go 代码里构造 `WordTable`**（典型是宿主侧的 policy 用例）：`Family: "Status"` 改写为 `Family: archcheck.FamilyPrefixes{"Status"}`（编译期即红）。**只写 policy YAML 的宿主不受影响**——YAML 侧 `family: Status` 逐字不变。<br>**② 若你的宿主登记了两张有同形取值的判定词表**：新规则会红 ⇒ 在 `word_tables.relations` 里声明这对表的关系（`same` = 同一概念 / `distinct` = 不同概念），并按新文案改手抄处——`distinct` 时必须**在本包另起一个名字**，不能引用别张表的常量（那是**语义损坏**：把任务失败写成会话失败）。<br>**③ 宿主若也用切片承载词表**：元素是常量名 ⇒ 现在可登记成判定词表；元素是复合字面量 ⇒ 仍只能写 `kind: input`。<br>**④ 无需 `go mod tidy`**（本版无依赖地板变化）。 |
| **v0.43.0**（已发版，tag `v0.43.0`） | **一处编译期 BREAKING + 一处行为面收口 + 哨兵身份统一**：① **`scheduling.QuotaStore` 接口新增三个方法**——`ReserveLease`（带归属与有效期的预占）、`Reclaim`（凭令牌回收悬挂预占）、`SweepExpired`（扫描并逐条回收到期预占，返回实际回收条数）；② **`finalize.Route` 改为委托 `deadline.Route`**（唯一判据单源，#355）——**宿主自有取消哨兵须显式传入**才判 `cancelled`；③ **错误哨兵身份统一**——`finalize.ErrUserCancelled` / `finalize.ErrSessionDeadline` / `deadline.ErrSessionDeadline` 现在都是 `contract.ErrUserCancelled` / `contract.ErrSessionDeadline` 的别名（跨包 `errors.Is` 互相命中）。**依赖地板无变化**（区间内无 go.mod/go.sum 改动） | **① 实现 `scheduling.QuotaStore` 的宿主要补三个方法**（编译期即红）：`ReserveLease(ctx, QuotaRequest) (QuotaLease, error)`、`Reclaim(ctx, QuotaLease) error`（凭 Token 回收悬挂预占）、`SweepExpired(ctx) (int, error)`（返回实际回收条数；租约仍有效 / 未到期 / 已归还的不计）。**只调用不实现的宿主不受影响**。<br>**② 用 `finalize.Route` 判终态的宿主**：若会话有**宿主自有**的取消哨兵（不是 `contract.ErrUserCancelled`），须改调 `finalize.Route(ctx, mySentinel)`，否则仍判成 `ActionShutdown`（不落终态、留给恢复循环）而不是 `ActionCancelled`。沿用 `contract.ErrUserCancelled` 作 cancel cause 的写法**无需改动**。<br>**③ 用 `==` 严格比较错误哨兵的调用点要复核**：跨包哨兵现在同一实例（此前是不同实例）；`errors.Is` 写法不受影响，只是命中面变宽。<br>**④ 无需 `go mod tidy`**（本版无依赖地板变化）。<br>**⑤ 可选接入的新能力**（不接也不红）：`ownership` 跨区域所有权权威、`sideeffect.ActionKeyBuilder` 动作身份作用域、`netguard.CheckURLWithResolver` / `CheckChainWithResolver` 解析器接缝、`scheduling.NewMemQuotaStore(..., opts...)` 装配项 |
| **v0.33.0**（已发版，tag `v0.33.0`） | **两处 BREAKING + 两处修复 + 一处判据收紧**：① `sideeffect` 引入**执行意图**——`Acquire` 原子登记「本次尝试还在」（Key 级 IN_FLIGHT），租约过期且**未裁决** ⇒ 转入需对账态而**不再**重新授予执行权；`Store` 接口**新增 `Abandon`**，`Release` 收窄为「只放资源闸、**不动**意图」；② `taskboard` 认领令牌改由**存储铸造**（`Claim` 改收 `ClaimRequest{TTL}`，`ConcurrentClaim` 末参改 TTL）；③ 参考存储补**持有者校验**（释放后的租约不得再续期/记账）；④ `checkpoint.StoreCAS.SaveIfNewer` **拒收 `Seq == 0`**（它不是一次真实提交，放它落库等于给了它唯一能覆盖既有快照的写）；⑤ `egress` 自报与实测分离（新增 `StrengthDeclared` 与 `EgressVerifier`，自报不再判 `enforced`）。**依赖地板无变化** | **① 实现 `sideeffect.Store` 的宿主要补一个方法并改一条语义**（编译期即红）：新增 `Abandon(ctx, act, lease, reason)`——撤销该 Key 的执行意图并释放资源持有；`Release` 改成**只放资源闸、不动意图**（旧语义会连意图一起清掉 ⇒ 「可能已生效」那条路的重复施加入口重新打开）。`Acquire` 的判定顺序改为「已完成 > 结果未知 > **执行中** > 资源被占 > 授予」：执行中且未过期 ⇒ `contended`；执行中且**已过期** ⇒ **uncertain**（须先对账，`RetrySafetySafe` 除外）。<br>**② 实现 `taskboard.Board` 的宿主要改 `Claim` 签名**（编译期即红）：收 `ClaimRequest{TTL}` 而非自带 `Lease`，**令牌由存储铸造**并随返回的 `Task` 给出——调用方自带令牌会让「释放/完成只认 token」名存实亡（它能造出任意 token）。`ConcurrentClaim` 最后一个参数改为 TTL。<br>**②′（本轮补齐，#87）长任务要能续期**：实现 `taskboard.RenewingBoard`（新增 `Renew(ctx, id, token, ClaimRequest{TTL}) (Task, error)`——**资格检查不是心跳**：令牌不是当前租约的或租约已过期 ⇒ `ErrLeaseLost` 且不改任何状态；通过才推后到期时刻，校验与延长须在同一原子操作里）。`Lease` 新增 `Fence` 字段（接管代次，与令牌同一次铸造，同一任务上严格递增；续期不递增）——下游资源可凭它拒收过期持有者的迟到写入。不实现 `Renew` 的存储编译不破（单独接口），但 `boardtest` 套件对缺失**硬失败**。<br>**③ 契约套件升级**：`sidetest` 22 条（须实现 `ClockedStore`，过期判定读注入时钟）、`boardtest` 16 条（须实现 `ClockedBoard` + `Seed` + `RenewingBoard`；并发认领规模档可配置，缺省跑 10 与 100 两档——4 并发撞不出「读-判-写」窗口）——宿主实现跑一遍即知是否合规。<br>**④ `checkpoint` 宿主**：`SaveIfNewer` 须拒收 `Seq == 0`（返回 `ErrStaleSeq` 且**不写入**）。<br>**⑤ `egress` 宿主**：后端只是**声明**能力（实现 `Enforcer`）时，兑现强度是 `declared` 而**不是** `enforced`；要判 `enforced`，后端须实现 `egress.EgressVerifier`（`VerifyEgress() (Mode, error)`）给出**实测证据**。<br>**⑥ 无需 `go mod tidy`**（本版无依赖地板变化）；**⑦ 文档**：`docs/adr/` 新增 ADR-0007《执行意图》，推导与取舍从 CHANGELOG 下沉到决策记录 |
| **v0.32.1**（已发版，tag `v0.32.1`） | **修复轮，无破坏性变更**：PTC 未受管逃逸口的**豁免登记路径**此前是「照着指引做就红」——自校验的「规则存在」口径只看 policy `rules` 段里的规则 id，而 `harness-ptc-unmanaged-escape` 是**恒开**的、不在 `rules` 段里 ⇒ 宿主按 v0.32.0 的违规文案去登记豁免时，自校验反而报「指向不存在的规则」，逃逸违规也没被豁免。**无 API 变更、无依赖地板变化** | **① 无需 `go mod tidy`**，调用面逐字不变。<br>**② 若你的宿主用了 PTC 的 `AllowUnmanaged` 逃逸口**（典型形态：PTC 只是本层工具的批量调用面，而每个绑定工具**已由别处逐笔审计**）：v0.32.0 上你**照着内核指引登记豁免会被红**；升到 v0.32.1 后这条路径才走得通——在 `exceptions` 段登记 `{rule: harness-ptc-unmanaged-escape, path, line, reason, until, removal}`（`path`+`line` 精确指向那处字面量），豁免**真的生效**（违反计入 excused、不再阻断），`until` 到期即失效。<br>**③ 三条收紧**（可能影响既有登记）：`rule: harness-policy-self-check` 的例外登记**直接报出**（自校验不可豁免）；例外指向**当前不在判**的内置规则（该段被 `enabled: false` 关掉）**报出**；policy 规则 id 与内置规则**重名**也报出。<br>**④ 既有 `exceptions` 登记若指向内置规则**，在 v0.32.0 上它们是**静默不生效**的（当时自校验会红，但没跑门禁就没人知道）——升上来后它们**开始真的生效**，请逐条复核 `until` 与 `removal` 是否仍成立 |
| **v0.32.0**（已发版，tag `v0.32.0`） | **两处 BREAKING + 三处判据增强 + 参考运行时 + 文档体系重构**：① `sideeffect` 引入「结果未知」一等状态——`Store` 接口**新增两个方法**（`MarkUncertain` / `ResolveUncertain`）、`Effect.Apply` 与 `Runner.Execute` 的执行体**签名新增 `lease Lease` 参数**、`Action` 新增 `RetrySafety` 字段（零值 = 未声明 = 按最弱处理）、`Outcome` 新增 `OutcomeUncertain`；② `ptc` **缺省即拒绝**——装配了工具绑定却没装判定环 ⇒ 拒绝执行（`ErrorKind=unmanaged`），裸执行须显式声明 `AllowUnmanaged`；③ `egress` 判据返回**兑现强度**（新增 `VerifyVerdict` / `SatisfiedVerdict`，环境变量兑现恒为建议型）；④ 新增 `examples/reference-runtime`（纯 stdlib 内存存储，**零新依赖**）；⑤ 文档：README 收敛 + `docs/adr/` 6 篇 ADR + USAGE 数字纳入 `make docsync` 机器判；⑥ `main` 开启分支保护（必须 PR + 必过检查，管理员可 bypass）。**依赖地板无变化** | **① 实现 `sideeffect.Store` 的宿主要补两个方法**（编译期即红，接口强制新语义、不给可选兼容面）：`MarkUncertain(ctx, act, lease, reason)` 把 Key 转入未知态、`ResolveUncertain(ctx, act, res)` 由宿主对账后裁决（已生效 → 记为完成；未生效 → 清除未知标记、允许重试）。裁决**先到者为准**（幂等）、**不校验栅栏**（依据是下游事实，不是持有权）。<br>**② 执行体签名变了**：`Effect.Apply` / `Runner.Execute` 的函数要加一个 `sideeffect.Lease` 参数——下游据此拒收过期持有者的迟到写。<br>**③ 动作要声明重试安全性**：不声明（零值）即按**最弱**处理——施加失败判「结果未知」并**禁止重试**，必须先对账；只有确认下游按幂等键去重，才显式写 `RetrySafety: sideeffect.RetrySafetySafe`。<br>**④ `ptc` 调用方**：装了工具绑定就必须装判定环（`Stages`），否则现在直接拒绝执行；纯计算程序（不装任何绑定）不受影响；确需裸执行的宿主显式声明 `AllowUnmanaged`（本仓生产代码里由新门禁 `harness-ptc-unmanaged-escape` 拦下）。<br>**⑤ `egress`**：原 `Verify` / `SatisfiedBy` 逐字不变，只是多了一对返回 `Verdict` 的入口——若你的宿主要区分「代理档已兑现」与「出站已被强制隔离」，改用新入口读 `Strength`（环境变量兑现恒为 `advisory`）。<br>**⑥ 无需 `go mod tidy`**（本版无依赖地板变化）；**⑦ 文档**：`docs/USAGE.md` 的包数口径已改为机器判（包数 / policy 登记数 / 安装行版本三处），改文档数字后跑一次 `make docsync` 即知对错 |
| **v0.31.0**（已发版，tag `v0.31.0`） | **BREAKING：项目整体更名 `agent-harness` → `goagent-kernel`**——旧名在语境里与「模型外壳 agent harness」「评测 harness」「harness.io」三头撞车（实测搜索前排全是本仓明确不做的东西），名字把读者引向错误预期。GitHub 仓、Go module path、文档站 URL 全部换名；仓内 94 个文件的 import 与活文档已全部替换。**历史快照刻意保留旧名**（CHANGELOG 与 docs/ 标注「历史快照」的文档不改写历史）；门禁规则 ID（`harness-*`）是内部标识符，不在本次更名范围。**API 逐字不变**——本次只是身份换名 | **① 机械替换 import**：`github.com/yi-nology/agent-harness/…` → `github.com/yi-nology/goagent-kernel/…`（全局替换即可，调用面零改动）。<br>**② `go mod tidy`**——module path 是编译期身份，不跑 tidy 会直接报找不到模块。<br>**③ 旧 GitHub URL 自动 301**（仓页面与 git 协议均然，旧 remote 不用急着改）；**文档站 URL 不重定向**：`yi-nology.github.io/agent-harness/` → [`yi-nology.github.io/goagent-kernel/`](https://yi-nology.github.io/goagent-kernel/)。<br>**④ `go` 指令地板 1.27.3 → 1.27.4**（随 Go 2026-11-10 安全发布抬升，标准库 CVE-2026-97033…97054；govulncheck 当天报本仓命中 1 条）：`go mod tidy` 后 GOTOOLCHAIN 自动对齐——地板抬升对下游是隐式 breaking，不跑 tidy 会报工具链不符 |
| **v0.29.0**（已发版，tag `v0.29.0`） | **判据面补强：cmdpolicy 事实面收编上游 bashguard**：① `DecideCommand` 从「只比 argv 前缀」升级为**全调用 AST 判定**（事实面收编 `oagentkit/tools/bashguard`，mvdan.cc/sh 真 AST）——前缀判据的三类典型绕过在此闭合：**多段**（`git status; rm -rf /` 的 rm 段）、**命令替换**（`echo $(rm -rf /)` 里的嵌套 rm）、**写重定向**（`git status > /etc/passwd` 的 `>` 是语句级事实、不在 argv 里）；② 新增**事实面下限**：含写重定向或动态展开（`$()`/反引号/`$VAR`）⇒ 裁决**不低于 prompt**（这两类语义前缀规则结构上看不见，静默 allow 即「判据比声明宽」）；③ 新增哨兵 `ErrUnjudgeable`（AST 超长/解析失败 ⇒ 判不了就不放行）；`Verdict` 新增 `Wrote`/`Dynamic` 事实字段，`Explain` 追加信号说明。**依赖**：新增 `github.com/oagentkit/tools` v0.14.3（带入 `mvdan.cc/sh` v3.14.1）；其余第一方地板与 v0.28.0 相同（该轮已抬到 guardx v0.24.1 / runtime v0.12.6 / toolgate v0.17.0 / pkg v0.42.0 等，以 `go.mod` 为准） | **① `go mod tidy`**——引入新依赖 `oagentkit/tools`（含 `mvdan.cc/sh` 传递依赖），不跑就报 `updates to go.mod needed`。<br>**② `DecideCommand` 的判定结果会变严**（这是修复不是回归）：此前被绕过的三类命令现在会被拦/升 prompt——`git status > x` 由 allow 变 **prompt**、`git status; rm -rf /` 由 allow 变 **forbidden**（全调用面取最严）。若你的宿主依赖旧行为放行这类命令：要么按新口径补规则，要么改用 **`Decide(argv)`**（纯前缀、不做事实面判定，调用面一字未动）。<br>**③ `Verdict` 新增 `Wrote`/`Dynamic` 两个字段**：键名构造不受影响；**无键名字面量**构造会编译不过，改键名即可。<br>**④ 命令解析失败/超长从「放行」变报错**（`ErrUnjudgeable`）——判不了就不放行，与 `ErrUnclosedQuote` 同一处置口径 |
| **v0.28.0**（已发版，tag `v0.28.0`） | **两处上游能力收编 + 一处下游可见的行为修正 + 第四轮 issue 清零**：① `cmdpolicy` 的**分词面收编上游 `oekit/pkg/shellx`**（本仓删掉手写分词器与 `mergeSeparators`，约 100 行）——收编**顺带修掉三处 POSIX 错误**：双引号内的反斜杠不再被一律丢弃（`"a\b"` 由 `ab` 变回 `a\b`，bash 实测即此）、分隔符只在**字符相邻**时合并（`a & & b` 不再被误并成 `a && b`）、CR 归空白（CRLF 命令行的行尾 CR 不再粘进最后一个 token）；② `cmdpolicy.SplitCommands` 的段文本改用上游 `shellx.QuoteArgv` 拼装，**段变得可往返**（段再分词逐项还原原 token）；③ `ptc` 的子进程 stderr 落进 `ExecutionAborted.Stderr` 前**过上游 `guardx/logredact` 脱敏**（凭据形态打码：URL 内嵌凭据、Bearer 头、JWT、厂商 token 前缀、长 hex、键值对）——子进程常把连接串与 API key 打进 stderr（MCP server 启动回显配置是最高频形态），而这段文本原先原样进错误消息与诊断报告；④ **第四轮 issue 清零（#62-#67）**：`egress` 代理变量判据收严为「一个不缺、一个不歪」（#62）、绕过名单收口为 NO_PROXY/no_proxy 并集单判定（#63）、`policy` 门面补 `Mutating(b)` 转发（#64，`toolgate` v0.17.0：`Op.Mutating` 三态化，**nil=未知 ⇒ 审计门 fail-safe 升人审**）、`ptc` 子进程后端三处契约修复（#65-#67，判定环归因复用 `stageOutcome` 等） | **① `cmdpolicy` 的分词结果在三处形态上会变**（只影响含反斜杠 / 空格分隔的 `& ` / CRLF 行尾的命令行）：本仓旧实现给 `"a\b"` → `ab`，现在给 `a\b`——**这是修复不是回归**（bash 实测逐字一致），但若你的宿主按旧行为做过前缀匹配，需按新口径核对。② `SplitCommands` 返回的段文本形态变了（含空格的段现在带引号）——**段仍是合法的 shell 命令行**，可再分词、可回放，但**不要再按字面比对段文本**（改判段再分词后的 token）。③ `ptc` 的 `ExecutionAborted.Stderr` 现在是**脱敏后**的文本：崩溃原因（fatal error / panic 栈）原样保留，凭据被打码——若你的宿主把 stderr 全文写进自己的日志系统，脱敏后是安全方向（凭据不再二次扩散）；若你的宿主**依赖**从 stderr 里读到某个明文 key（不当用法），请改从别的通道取。<br>**④ `go mod tidy`**：本轮无新增依赖（`shellx` 与 `logredact` 都在已声明的 `oekit/pkg` 与 `oagentkit/guardx` 模块内），故依赖地板不变。<br>**⑤ 若你的宿主依赖「不标变更=非变更=放行」**：`toolgate` v0.17.0 起 `Op.Mutating` 三态化（`*bool`），nil=未知 ⇒ 审计门升人审——`gatecheck`/`tooldisclose`/`ptc` 不再替宿主猜变更标记，**需显式声明**。<br>**⑥ `egress.Verify` 收严**：六个代理变量「一个不缺、一个不歪」才绿、`Bypass` 判定对 NO_PROXY/no_proxy 取并集——此前靠缺省通过的配置会开始报违约（修复假绿，#62/#63） |
| **v0.27.1**（已发版，tag `v0.27.1`） | **修复轮，无破坏性变更**：`ptc` 的拒绝分类载体化（issue #53-#59）——分类随抛出对象一起走，收口处据此把「守卫判定拒绝」与「守卫自己崩了」分成两类，不再一律映射成 transient；另修 `protocol` 的报告校验、`drain` 的预算归一、`pending` 的列值解析。**依赖地板不变** | **无需 `go mod tidy`**，调用面逐字不变。**若你的宿主按「拒绝一律可重试」处理 ptc 的拒绝**，请改按 `toolfail` 的分类分流（`Kind` 随拒绝一起给出）——本版起「权限拒绝被当成瞬时错」这条误判已修正 |
| **v0.27.0**（已发版，tag `v0.27.0`） | **第一方依赖全量抬到最新 + 一处下游可见的解析口径变化**：① 依赖地板抬升——`oekit/concurrency` v0.28.4→**v0.31.0**、`oekit/pkg` v0.37.4→**v0.40.0**、`oekit/logx` v0.8.10→**v0.8.12**、`oagentkit/agent` v0.14.3→**v0.14.5**、`guardx` v0.23.2→**v0.23.4**、`runtime` v0.12.2→**v0.12.4**、`toolgate` v0.15.2→**v0.15.3**、`mcpkit` v0.12.1→**v0.12.4**；② **`protocol` 的解析通道边界随之扩宽**——`oekit/pkg` v0.38 起 `jsonrepair.Repair` 的输出契约明写「`:` 后缺值补 null」（pkg#268），故形如 `{"agent_name":"X","confidence":}` 的**非法 JSON 从「拒收」变成「被修复通道救回」**；救回点落在本仓 `markRepaired` 的既有设计内（修复产物**必降置信 low**，下游 verify/needs_review 承接），本仓代码**零改动**、只把测试的不变量从「失败即拒收」改写为「凡经修复通道放行必为 low」，并补了成对的反向判据（缺冒号、无 JSON 结构仍拒收） | **① `go mod tidy`**——地板抬升对下游是隐式 breaking，不跑就报 `updates to go.mod needed`，且报错不提真正原因。<br>**② 若你的宿主直接调 `protocol.ParseReport` / `ParseLLMReport`**：缺值形态的模型输出现在**不再返回 `ErrNotReport`**，而是返回一份 `Confidence="low"` 的报告。这不是回归——上游修复通道的扩宽是刻意行为，且本仓既有设计（`markRepaired` 降置信）本就是为这类形态准备的。**若你把「解析失败」当拒收信号用**，请改判 `Confidence == protocol.ConfidenceLow`。（结构不可修复的形态——缺冒号、无 JSON 结构——仍照常返回 `ErrNotReport`，本仓补了成对的反向用例钉住这条边界。）<br>**③ 上游 `oekit/concurrency` v0.28.2 起 `async.PanicHandler` 签名改为 `func(ctx context.Context, r any, stack []byte)`**——本仓**未定义**该 handler（故编译未破），但**你的宿主若自行实现了它，需要改签名**。这是本轮唯一一处**真正传导到下游的 BREAKING**，请务必核对。 |
| **v0.22.0**（已发版，tag `v0.22.0`） | **依赖地板抬升 + 三处零行为重构**：`guardx` v0.21.0→v0.22.0、`toolgate` v0.13.0→v0.14.0、`mcpkit` v0.11.0→v0.12.0、`logx` v0.8.4→v0.8.6；本仓代码只做等价收口（`archcheck` 删掉两个**同体异名**的证据判定包装、`protocol` 的手写 nil 判空换 `ptrx.Deref`、`taskboard` 的循环二次拼接换 `strings.Join`），**公开 API 零变更、零行为差异** | **只需 `go mod tidy`**——地板抬升对下游是隐式 breaking：不跑就报 `updates to go.mod needed`，且报错不提真正原因。三处重构的调用面逐字不变。**一处刻意不采用**：`mcpkit` v0.12.0 新增的 `DisclosureGate`（模型面三态披露契约）本仓不接——它绑 `mcpkit/mcp` 的 `Catalog`，而那个包依赖 eino + mcp-go，引入等于拿一个三态判定换一整条 mcp SDK 依赖；且本仓 `tooldisclose.Gate` 的哨兵二分 + `Retryable` 自报 + `Activated` 副作用仍要留（`toolfail` 靠 `Retryable` 分流，不解析文案） |
| **v0.21.0**（已发版，tag `v0.21.0`；**本行由 v0.22.0 轮补录**，该版当时未补行） | `trajectory` 的 OTel 映射长成**契约表**：新增 `AttrSpecs` / `AttrSpecOf` / `ForbiddenLabels` 与 `TraceGranularity` / `Domain` / `Cardinality`——每个属性有主、只有有界枚举才许当 label、父子关系落 `Span.ParentID`。**无 API 删除、无依赖地板变化** | **无需 `go mod tidy`**、无需改调用面。补录依据是 `git log v0.20.1..v0.21.0`（一个 feat + 两个 docs 提交）；**若你依赖 `trajectory` 的 label 面**，请自行对照 `AttrSpecs` 自查——本次补录未逐条核对这版对 label 的收窄 |
| **v0.20.1**（已发版，tag `v0.20.1`） | **`egress.Spec.Allow` 的语义修正（安全方向）**：`Allow` 是「**允许经代理访问**的目标」，此前被写进 `NO_PROXY`——那等于让允许名单里的目标**绕过代理直连**：代理的拒绝名单 / 端口白名单 / 只读档对它全部失效，而声明在报告里仍显示「已兑现」。现 `Allow` 不再产生 `NO_PROXY`；新增 **`Spec.Bypass []string`** 表达「不经代理直连」，`Verify` 增判绕过名单（**语义等价**比较：顺序与空白漂移不算违约，判字面就是假红）。另：`egress` 补齐端到端验收用例——真强制代理 + 真请求（GAP 台账 P0-1 遗留的那一条）。 | **① 若你把 `Spec.Allow` 当「直连豁免」用**（即依赖它产生的 `NO_PROXY`）：改用 `Spec.Bypass`。名字与语义都变了，但这一版是**把危险的默认关掉**——旧行为让「允许」变成「豁免」。<br>**② 若你把 `Allow` 当「允许目标」用**（正确用法）：变得更严格，不破坏调用面——`Allow` 现在只是交给兑现者的声明（代理按自己的名单判），env 里不再出现 `NO_PROXY`。<br>**③ `Verify` 收紧了**：`Bypass` 非空而 `NO_PROXY` 缺失或不一致即判违约。<br>**④ 不需要 `go mod tidy`**——本版无依赖地板变化。 |
| **v0.20.0**（已发版，tag `v0.20.0`） | **出口声明的兑现面收口（行为收紧）**：① `egress.Mode` / `egress.Enforcer` 的**真相上移**到上游 `guardx/sandbox`（`EgressMode` / `EgressEnforcer`），本仓这两个名字改为 **type alias**（编译期同一类型，调用面逐字兼容）——词表留在本仓时，上游任何执行后端要声明「我能兑现」都得 import 本仓 ⇒ 成环 ⇒ 那条接口在结构上只可能由宿主实现，且编译期一声不响；② `ptc.SubprocessOptions` 新增 **`EgressBackend egress.Enforcer`**，**spawn 之前**先问后端「你能强制这一档吗」，问不到就**不起子进程**；③ `archcheck` 的 `egress_paths` 判据补上**第二半证据**（`proxy` 档除 env 生成外还须看得见能力问询 `SatisfiedBy`），policy 新增可选字段 `capability`。另：`guardx` v0.19.0→v0.21.0、`oekit/pkg` v0.36.0→v0.37.1 | **① 若你用了 `ptc.SubprocessOptions.Egress` 且档位不是 `unrestricted`：必须同时填 `EgressBackend`**（一个实现 `sandbox.EgressEnforcer` 的对象，例如宿主起的强制代理；上游 `guardx/egressproxy` 就是现成实现，它的 `Egress()` 只声称 `proxy` 档）。不填 ⇒ 现在**拒绝执行**（v0.18.0 时会照常起子进程、只生成 env）——这是**修复不是回归**：环境变量只是建议，「生成了 env」当成「已兑现」正是这条链此前空转的原因。<br>**② `go mod tidy`**——地板抬升对下游是隐式 breaking，不跑就报 `updates to go.mod needed`，且报错不提真正原因。<br>**③ 若你在自己仓里复用 `archcheck`**：`egress_paths` 的 `proxy` 档现在要求**两半证据**（env 生成 + 能力问询），只做前一半的启动点会红。这条是刻意的（旧判据对「只生成 env」静默放行）；确需过渡就在该条登记的 `capability` 字段指明你们的能力问询方法名。<br>**④ 本仓 `sandbox` 门面新增 `EgressMode` / `EgressEnforcer` / 三个档位常量别名**——宿主实现 Provider 时可在门面里拿到这两个名字，不必直接 import 上游。<br>**⑤ 上游 `guardx` v0.20.0 收紧了 `egress.Check` / `CheckWith` / `CheckResolving` 的判定面**：新增 **scheme 白名单（只认 `http` / `https`）**，`file:` / `gopher:` / `ftp:` / `ws(s):` / 空 scheme（相对 URL）**一律拒绝**；`Policy.NewClient` 同时补上 `DialTLS` 这条此前被绕过的拨号路径。若你（或你的宿主）把这两个判定用于**非 http(s) 出站**，升级后从「通过」变成 `BlockedError`——这是**修复不是回归**（判定面本来就只该覆盖 LLM 可控的 fetch 类 URL）；按 host 判定的 `CheckHost` / `CheckHostWith` 没有 scheme 概念，行为不变。 |
| **v0.18.0**（已发版，tag `v0.18.0`） | **纯新增：十个机制包 + 两条新判定面**——`cmdpolicy`（命令级判据）、`egress`（出口策略）、`preset`（权限预设）、`sessionref`（跨会话引用）、`taskboard` + `boardtest`（协调面契约与并发判据）、`skilldisclose`、`hookengine`、`memorypipe`、`delegate`、`worktree`；`ptc.SubprocessOptions` 新增 `Egress egress.Spec`；`archcheck` 新增两条 `go/ast` 判定面（`require_rule_examples` 规则样例自检、`egress_paths` 子进程出口登记）。详见 [docs/GAP-ANALYSIS-20261106.md](docs/GAP-ANALYSIS-20261106.md) 的第六节 | **无需改动**——全部是新增 API 与新增 policy 字段，不写新字段即按旧行为生效。三点可选留意：<br>**① `ptc.SubprocessOptions.Egress` 零值 = 不限制**，与旧版逐字一致；一旦你开始用 `ModeProxy`，子进程会拿到注入的 `HTTP(S)_PROXY` 等变量（不会自动发生，必须显式声明）<br>**② 若你在自己仓里复用 `archcheck`**：可把 `egress_paths` 段抄过去（判定执行体是同一份可导入包），把「spawn 点必须登记 + `proxy` 必须看得见兑现证据」这条判据一起带走<br>**③ `preset` 的方向是 preset → gatecheck**，别反向接（低层判据认识高层组装件会立刻成环）|
| **v0.17.0** | **三处破坏性收窄 + 依赖地板抬升**：① `drain.Budget` 返回**命名类型** `DrainBudget`（`Drain` / `DrainSession` / `DetachCtx` 只收它），于是 `context.WithTimeout(ctx, drain.Budget(x))` **编译不过**——那正是「优雅停机静默变硬切」的唯一入口；收尾预算一律 `drain.ShutdownBudget`（返回普通 `time.Duration`）。`drain` 包同时把 `Drain` / `DrainSession` 两份收敛循环合并成一份。② 删 `netguard.CheckURL` / `netguard.CheckChain`（双 bool 包装），放行面唯一形态是 `Allow{...}` + `CheckURLWith` / `CheckChainWith`。③ 删 `pii.Redact` / `pii.RedactValue`（面无关门面），唯一入口是 `pii.Apply` / `ApplyValue`（**必须显式指定面**）。另：`ptc` 三处预算归一收进 `ptc/limits.go`、三份快照式订阅表收编到 `internal/subhub`（均为内部实现，无 API 变化）。**依赖**：oekit / oagentkit 家族抬到本轮最新（逐项见 [CHANGELOG.md](CHANGELOG.md) 的 v0.17.0 段）；根目录 `tools.go` 版本钉子删除 ⇒ `oagentkit/{llm,obs,textx}` **不再是本仓依赖** | **① `go mod tidy`**——地板抬升对下游是隐式 breaking，不跑就报 `updates to go.mod needed`，且报错不提真正原因。<br>**② `drain` 接线改两个入口**：`Drain` / `DrainSession` / `DetachCtx` 的实参包一层 `drain.Budget(cfg.X)`；喂 `context.WithTimeout` 的那一个数改走 `drain.ShutdownBudget(cfg.X)`。机械改法、**语义不变**（`Budget` 对正值与负值都原样，只有 0 补缺省），但**别把 `Budget` 的负值归一**——`<0` 是「关闭排水」的配置语义，归一会把「少做」静默翻成「多做 60 秒」<br>**③ `netguard` 改调用形态**：`CheckURL(u)` → `CheckURLWith(u, Allow{...})`、`CheckChain(c)` → `CheckChainWith(c, Allow{...})`——两个 bool 的位置不再需要记（写反不报错，只是静默改变放行面）<br>**④ `pii` 改调用形态**：`Redact` / `RedactValue` → `Apply` / `ApplyValue` 并显式传面；面无关门面拿不到 `blocked` 信号，策略配成 `block` 时它只会**静默打码**<br>**⑤ 若你的 `go.mod` 靠本仓间接拉起过 `oagentkit/{llm,obs,textx}` 的版本地板**，请自己在 `go.mod` 里显式钉——本仓不再声明它们 |
| **v0.14.0** | **依赖地板抬高**（`oekit/pkg` v0.28.0→v0.30.0、`oekit/logx` v0.7.1→v0.8.0、`oekit/concurrency` v0.22.0→v0.23.0、`oekit/core` v0.12.0→v0.12.1、`oagentkit/guardx` v0.15.0→v0.17.0、`oagentkit/llm` v0.17.1→v0.18.1、`oagentkit/textx` v0.6.2→v0.7.1、`oagentkit/mcpkit` v0.8.6→v0.9.0）；**本仓自身代码零改动** | **① `go mod tidy`**——地板抬高对下游是隐式 breaking，不跑就报 `updates to go.mod needed`，且报错不提真正原因。<br>**② 若你直接 import 过 `github.com/oagentkit/guardx/audit` 或 `github.com/oagentkit/guardx/bashguard`：这两个包自 guardx v0.17.0 起**不再属于 guardx 模块**，要改 import 路径**——audit → `github.com/oagentkit/obs/audit`；bashguard → `github.com/oagentkit/tools/bashguard`。本仓自身零命中这两个路径（用的是新路径 `obs/audit`），但你的宿主代码若直接依赖过，需跟着换这一行 import。<br>**其余全是等价改写与纯新增，无公开 API 变更**：`oekit/pkg` v0.30.0 补 `convert.AnyToStrOr` / `OptStr` / `OptBool` / `OptInt32` / `OptInt64` / `AnyToInt32` 六个 nil 安全入口（`AnyToStr(nil)` 此前返回 字符串 `"<nil>"` 这种**长得像值**的哨兵），并修 `cache.HashKey` 的参数边界歧义（`("a","b")` 与 `("a:b")` 曾算出同一个 key ⇒ 缓存串数据）；`oekit/logx` v0.8.0 补 `Nop()` 静默 logger 与 `LevelOff` 档；`oekit/concurrency` v0.23.0 修 `budget.Ledger` 的 int64 溢出绕过（一次 `n ≈ MaxInt64` 的消耗曾让该维度**永久**变成「无限制」）与 `singleflight.DoChan` 的清理漂移（`Forget` 后同 key 曾并发跑两次 fn），并补 `Ledger.Used` 观测口；`oagentkit/mcpkit` v0.9.0 补 `Pool.Tool(server, name)` 与 `ToolsResult.ByServer`（纯新增，`Tools` 的扁平列表保留） |
| **v0.13.0** | **`spill` 路径逃逸修复（fail-closed 收口）**：会话标识与定位符的判定改委托 `oekit/pkg/pathx.IsSafeFileName`——旧实现用 `filepath.Base` 判等，而 `filepath.Base("..") == ".."`，于是 `sessionID=".."` 能让落点指到 spill 根目录的**父目录**（三重守卫两道一起失效，实测可读出根外文件）；**检索条数钳制**改用 `oekit/pkg/pagination.Policy` 口径（等价）；**schema 编译缓存**收编 `oekit/pkg/cache`（内部实现，契约不变）；**依赖地板抬高**（`oekit/pkg` v0.22.0→v0.28.0、`oekit/concurrency` v0.20.1→v0.22.0、`oagentkit/agent` v0.10.6→v0.11.0、`guardx` v0.14.0→v0.15.0、`llm` v0.16.0→v0.17.1、`obs` v0.9.6→v0.9.7、`toolgate` v0.12.4→v0.12.5、`mcpkit` v0.8.5→v0.8.6） | **① `spill` 的判定口径收紧了**：会话标识/定位符含路径形态（`.` / `..` / 含 `/` `\` / 含 Windows 盘符相对路径）此前多数**静默通过**，现在一律**显式拒绝**（返回 `ErrBadName` / 「会话标识非法」）。若你传的是用户可控的会话标识，非法形态从「悄悄读到别的目录」变成「拿到错误」——**这是修复，不是回归**。<br>**② `go mod tidy`**——地板抬高对下游是隐式破坏性变更，不跑就报 `updates to go.mod needed`，且报错不提真正原因。<br>**③ 两处上游 BREAKING 会传导到你**：`oekit/concurrency` v0.21.0 起 `ratelimit.Limiter` 接口**启用了 `Wait(ctx context.Context) error`**——**自行实现该接口的宿主需补上这个方法**（旧实现不再满足接口）；`oekit/pkg` v0.26.0 起 `convert.ResolveClientIP` **去掉了 `xri` 入参**（确需采信改用 `ResolveClientIPWithXRI`）。本仓自身两处都没直接用到，但你的宿主代码可能用了。<br>**其余全是等价改写，无公开 API 变更**：`pagination.Policy` 口径与旧 `clampLimit` 逐条等价（n≤0→默认、超限→钳到上限、区间内原样）；schema 缓存与单边界钳位均为内部实现替换 |
| **v0.10.0** | **`guardset.Assembly` 对齐 `netguard` 的「整份原子交换」**：换份走 **CAS + `mutate(旧值)`**（新增 `SwapFunc`）；新增**代次**（`Assembly.Generation()` / `Snapshot()` / `Outcome.Generation`）与**换份订阅**（`Subscribe`/`Unsubscribe`）；**修两处静默失效**：① 并发换分**丢更新**⇒「撤销被撤销」（被撤掉的守卫悄悄复活）；② 通知期回调 panic 把**已落地**的换份说成失败 | **若你按 v0.9.0 的示例用 `Swap(next)` 做读改写，请改用 `SwapFunc(ctx, mutate)`**——`Swap` 的语义是「整份替换为这一份」，**不基于当前那一份**；两个并发撤销者会互相覆盖，被撤掉的守卫悄悄复活且**没有任何提示**。`SwapFunc` 基于当前份做读改写（CAS 循环），并发下不丢更新。**其余全是新增 API，无接口变更**（`SwapRecord`/`Outcome` 只**新增**字段）；`Set` 仍只读，`guardset.Stage(set)` 仍可用 |
| **v0.9.0** | **新增 `gatecheck` 包**（审批层 × 沙箱层的**联合判据**：两层都同意才放行，含 `ExecuteAndRun` 与受管后端 `Managed`——判据与执行不可分）；**新增 `guardset.Assembly`**（守卫集合的装配面：撤销 = **换整份**，链不用重接）；**`policy` 门面新增 `Stage`**（审批门接进判定环的唯一入口）；**`archcheck` 新增 `import_exempt`**（import 路径级**精确**例外，可放行被禁依赖树里的某个具体子包）；**`ptc` 修复「子进程不回结果时父侧永久挂死」**（上游 `guardx/wire` 的 `Starter.Start` 收了 ctx 却不用它，本仓改为取 ctx deadline 与墙钟预算较早者）+ 内存兜底改用 cgroup v2（原 RLIMIT_AS 会把 Go runtime 一起掐死）+ 执行异常终止的**死因分类**；**依赖地板追平**（`oagentkit/agent` v0.10.2→v0.10.4、`llm` v0.15.0→v0.15.2、`obs` v0.9.4→v0.9.5、`toolgate` v0.12.1→v0.12.4、`mcpkit` v0.8.4→v0.8.5） | **`go mod tidy`**——地板抬高对下游是隐式破坏性变更，不跑就报 `updates to go.mod needed`，且报错不提真正原因。**其余全是新增 API，无接口变更**：不接 `gatecheck` / `guardset.Assembly` / `policy.Stage` / `archcheck` 的 `import_exempt` 的宿主代码**逐字不变**（policy 不写新字段即按旧行为生效）。**一处行为变化需知**：`ptc` 现在尊重 ctx deadline——此前父侧可能在子进程不回结果时**永久挂死**，现在到点收刀并报错；若你依赖「父侧一直等」请显式给足 deadline |
| **v0.8.4** | **依赖地板追平矩阵本轮新版本**（`oagentkit/agent` v0.10.1→v0.10.2、`obs` v0.9.3→v0.9.4、`runtime` v0.7.1→v0.7.2、`textx` v0.6.1→v0.6.2、`toolgate` v0.12.0→v0.12.1、`mcpkit` v0.8.3→v0.8.4）；**无代码变更** | **`go mod tidy`**——地板抬高对下游是隐式破坏性变更：不跑就报 `updates to go.mod needed`，且报错不提真正原因。**无行为差异**：这 6 个上游版本**全部是纯依赖地板追平**（各仓自身代码零改动，只抬 `oekit/pkg` / `oekit/concurrency` / `guardx` / `llm` 四类地板之一），逐仓核对见 [CHANGELOG.md](CHANGELOG.md) 的 v0.8.4 段 |
| **v0.8.3** | **依赖地板追平上游本轮新版本**（`oagentkit/llm` v0.14.2→v0.15.0、`oekit/concurrency` v0.18.0→v0.19.0）；**无代码变更** | **`go mod tidy`**——地板抬高对下游是隐式 breaking：不跑就报 `updates to go.mod needed`，且报错不提真正原因。**无行为差异**：llm v0.15.0 只新增了一个**可选**字段 `OpenAIProviderConfig.HTTPClient`（缺省 nil 时行为逐字不变），本仓只用 llm 的空导入（`tools.go` 的工具依赖登记），不碰其 API。**顺带值得知道**：llm v0.15.0 起宿主可注入自己的 `*http.Client` → 配合本仓 `netguard.NewClient` 把出站守卫挂到 `Transport.DialContext`（写面校验拦不住 DNS rebinding，守卫必须在拨号层）——接线形态见 [llm v0.15.0](https://github.com/oagentkit/llm/releases/tag/v0.15.0) |
| **v0.8.2** | **依赖地板追平上游本轮新版本**（`oekit/pkg` v0.18.0→v0.20.0、`oagentkit/guardx` v0.13.0→v0.13.1）；**无代码变更** | **`go mod tidy`**——地板抬高对下游是隐式 breaking：不跑就报 `updates to go.mod needed`，且报错不提真正原因。两处抬升**均无行为差异**（`guardx` v0.13.1 只改 `go.mod`/`go.sum`；`oekit/pkg` v0.20.0 相对 v0.18.0 是纯增量 + 一处退避溢出修复），逐项核对见 [CHANGELOG.md](CHANGELOG.md) 的 v0.8.2 段 |
| **v0.8.0** | **`sideeffect.Store` 新增 `Renew` 方法**（续期 + 校验持有资格），并新增 `sideeffect.FencedSink` 契约（下游拒收陈旧写入）；新增 `retryplan` 包（重试处置决策） | **接口层面的破坏性变更**：宿主若已实现 `sideeffect.Store`，需补上 `Renew(ctx, act, lease, ttl) (Lease, error)` 方法——语义见包头注释（**续期必须同时校验令牌仍是最新代**，否则是「假续期」）。宿主若已实现下游栅栏拒收，可改用 `FencedSink` 契约对齐比较口径与原子性要求。`FencedSink` 与 `retryplan` 是**新增**的，不影响既有代码 |
| **v0.7.0** | **`toolfail` 与 `sandbox` 下沉上游 `oagentkit/guardx`（v0.9.0）**，本仓转**门面**（类型别名 + 常量别名 + 单条上游转发）；依赖地板抬到上游本轮新版本（`guardx` v0.8.2→v0.9.0、`llm` v0.14.1→v0.14.2、`agent` v0.10.0→v0.10.1）| **调用面逐字兼容，宿主代码不用改**——类型别名是编译期同一类型，宿主现有的 `errors.As` / `errors.Is` 判定链继续有效。**但必须 `go mod tidy`**：地板抬高对下游是隐式 breaking，不跑就报 `updates to go.mod needed`，且报错不提真正原因。下游若直接 import 过 `github.com/yi-nology/goagent-kernel/toolfail` 或 `.../sandbox` 的**具体实现**（如自造 `*Failure`、注册自定义 Provider），请改 import 上游路径 `github.com/oagentkit/guardx/toolfail` / `.../sandbox`——门面只保转发，不提供实现。逐版差异见 [CHANGELOG.md](CHANGELOG.md) 的 v0.7.0 段 |
| **v0.6.5** | **依赖地板追平上游本轮新版本**（`oekit/pkg` v0.17.0→v0.18.0、`oagentkit/obs` v0.9.2→v0.9.3、`oagentkit/mcpkit` v0.8.2→v0.8.3）；**`archcheck` 的 god object 判据改比「实质方法数」**（方法总数 − 转发壳数），policy 新增可选字段 `forwarding_shell_max_chars` | **`go mod tidy`**——地板抬高对下游是隐式 breaking：不跑就报 `updates to go.mod needed`，且报错不提真正原因。**判据变更对下游无感**：policy 不写新字段即按缺省 140 生效；若宿主要保持旧语义（方法总数），在自己 policy 里显式写 `forwarding_shell_max_chars: 0` 即可逐字退回旧行为。逐模块版本差异与判据推导见 [CHANGELOG.md](CHANGELOG.md) 的 v0.6.5 段 |
| **v0.6.4** | 纯文档：新增 `docs/AI-GUIDE.md`（面向 AI 代理的操作手册），并校准 README「工程底座」表的三处漂移 | 无（无 API 变更、无地板抬高）|
| **v0.6.3** | **依赖地板追平上游本轮新版本**（llm / obs / mcpkit / concurrency / core 共 5 个模块）| **`go mod tidy`**——地板抬高对下游是隐式 breaking：不跑就报 `updates to go.mod needed`，且报错不提真正原因。逐模块版本差异与「是否影响本仓」已逐个核对，写在 [CHANGELOG.md](CHANGELOG.md) 的 v0.6.3 段 |
| **v0.6.2** | 纯文档：发版纪律补上「发 GitHub Release」这一步，并补齐本表此前缺的 v0.6.1 与 v0.3.0–v0.3.2 两行 | 无（纯文档）。本仓发布页此前 v0.4.0–v0.6.1 四版是空的，已按各 tag 注解补发 |
| **v0.6.1** | 修 archcheck 的静默假红（import 规则不认 `negative_follow` 豁免）；补 `CHANGELOG.md` 与发版纪律 | 若你的 policy 在 import 规则上写过 `negative_follow`：**此前它一条都没生效**，现在生效了，构建可能从红变绿；若写过 `forbid_call_regex` / `allow_paths`：那两字段一直被 YAML 静默丢弃，自校验现在会点名 |
| **v0.6.0** | `netguard` 的放行面改为具名类型（`Allow`），校验层与拨号层共用同一个类型；新增接线件 `NewClient` / `NewTransport`；依赖地板抬到上游最新一轮（7 个模块）| `netguard.DialOptions.AllowPrivate` / `AllowLoopback` 两字段合并为 `Allow`（**宿主若已接线，改字段名**）。其余为新增 API，旧签名保留但已废弃。**地板抬高对下游是隐式 breaking**：`go mod tidy` 不跑会报 `updates to go.mod needed`，且报错不提真正原因——迁移说明见 [CHANGELOG.md](CHANGELOG.md) |
| **v0.5.0** | 上游把判定环原语 `invoke` 从 `oagentkit/toolgate` 迁到 `oagentkit/guardx`（框架无关的原语不该被 eino 重仓的地板拖住），本仓跟随；依赖地板抬到上游最新一轮 | import 路径换成 `github.com/oagentkit/guardx/invoke`；require 升到 `toolgate v0.12.0` + `guardx v0.8.0`。**编译期报错**（类型身份变了），不是静默行为变化 |
| **v0.3.0 – v0.3.2** | 依赖地板连续抬到上游当时最新（oagentkit 八模块 + oekit 四模块）| **`go mod tidy`**——地板抬高对下游是隐式 breaking：不改代码就编不过，而报错只说 `updates to go.mod needed`，不提真正原因。逐版抬了哪些地板见 [CHANGELOG.md](CHANGELOG.md) 与对应 tag 的 release 说明 |
| v0.4.0 | 门禁执行体从 `package main` 下沉为可导入包 `archcheck/` | 无（新增包，不改既有 API）|

受影响的本仓 API（签名里带 `invoke` 类型）：

```go
guardset.Guard          // Check func(ctx, c invoke.Call) invoke.Verdict
guardset.Set.Evaluate   // (ctx, c invoke.Call) Outcome
guardset.Stage          // invoke.Stage
tooldisclose.Stage      // invoke.Stage
ptc.Options.Stages      // []invoke.Stage
```

> 依赖面变轻了：判定环从 eino 重仓挪到了零依赖底座。下游若自己 import 过
> `toolgate/invoke`，那条依赖曾把 eino 拖进下游的模块图，现在没有了。

## 宿主接入

接线前先看 **[docs/HOST-SURFACE.md](docs/HOST-SURFACE.md)**：六步主链（创建 → 领取 → 执行 →
提交 → 恢复 → 介入）每步宿主提供什么、kernel 兑现什么，以及全部 50 个包的三分类
（必须接 / 可选 / 内部件）——`make hostsurfacecheck` 机器判定本文与 USAGE 教的 import
不落在内部件里。

```bash
go get github.com/yi-nology/goagent-kernel@latest
```

```go
require github.com/yi-nology/goagent-kernel v0.65.0   // 零 replace，直接版本化引入
```

> 升级前用 `git ls-remote --tags origin` 核候选版本真的存在——
> `go list -m -versions` 报的是 proxy 的缓存目录，不是远端现实。
> 当前已发布 tag 以远端为准（写稿时为 v0.62.0）。
>
> ⚠️ **require 里写的是「最低版本」，不是「锁定版本」**：下游 `go mod tidy`
> 只保证不低于这个版本，不会自动升到最新版。要用新版本必须显式
> `go get ...@<新版本>`——这也是为什么这里写当前版本号而不是 `v0.0.0`。

```go
import "github.com/yi-nology/goagent-kernel/dispatch"

// 失败面：先判档位，再决定要不要换人再派
switch dispatch.Classify(err) {
case dispatch.ClassTransient: // 瞬态：可跨域兜底改派
case dispatch.ClassFatal:     // 终态类：换执行者也是白跑
default:                       // 不确定：仅同域重试
}

// 改派环：候选枚举与执行步由宿主注入（与宿主注册表 / 能力画像无关）
res := dispatch.StickyFailover(ctx, dispatch.Request{
    Slug:       slug,
    FirstErr:   err, // 判定面只读首次失败的原始错误
    SameDomain: 1,
    CrossDomain: true,
    FailoverCandidates: func(slug string, tried map[string]bool, sameDomain bool) []string {
        // ...宿主给出候选序...
        return nil
    },
    Attempt: func(ctx context.Context, slug string) error {
        // ...宿主按候选 slug 执行一次...
        return nil
    },
})
```

> 「这次派发**能不能**发生」不在本仓：allow 矩阵 / 深度上限 / 自派发拒绝由
> 上游 `github.com/oagentkit/agent/dispatch` 判，本仓 `dispatch` 只管失败面
> （分类 + 改派），不提供可达性判定。

> ⚠️ 上面只是九组能力之一的片段。**完整接入路线图见 [docs/USAGE.md](docs/USAGE.md)**：
> 契约面（`contract` + `statemachine`）、工具判定环（`tooldisclose` + `guardset` +
> 上游 `github.com/oagentkit/guardx/toolfail`）、报告协议（`protocol`）、续跑契约
> （`checkpoint` + `commitgate`）、实时事件、流式与退出、安全面（上游 `netguard` + `pii`）、
> 程序化编排（`ptc`）；上下文三件套已上抛上游 `github.com/oagentkit/textx`（v0.77.0）。

宿主侧的安全不变量（如「全拓扑只有一条边能到达执行器」）的**断言测试应留在宿主仓**——
那是产品级承诺，不是内核机制属性。本仓的机制用例用合成的候选与执行步测改派环
（`dispatch/failover_test.go`），可复用于任何宿主。

## 演进纪律

- 新增能力前先问：**这是机制还是资产？** 资产进宿主/领域包，机制才进本仓。
- 新依赖方向先改 policy 再写代码——声明面是门禁的判据源，先写代码再补声明只会被当场判红；例外带注释写明拆除条件，否则它会退化成永久豁免。
- **改了包或门禁，同步本 README 的包表**：新增/删除包、改外部依赖、改生产行数跨过 25% 容差——三者任一发生就要改表，`make docsync` 会拦。
- 内核包规模远小于宿主，`max_file_lines: 400` 直接阻断（宿主仓是报告口径）。
  2026-10-18 起 `thresholds.enforce` 由 false 切 true：此前 3 处超长文件
  （`protocol/report_types.go` 660、`protocol/report_test.go` 782、
  `cmd/archcheck/main.go` 490）已拆分归零，2026-10-19 起执行体整体下沉为 `archcheck` 包，切换条件才成立。**拆分只为满足
  机械阈值，单文件读不动时该模块应重新分组，而不是继续往上加。**
- 拆自宿主仓的代码历史见 [`docs/SPLIT_PLAN.md`](docs/SPLIT_PLAN.md)——那是 2026-10-17
  写于 10 个包时的**分层依据快照**，其中「L2/L3 待拆」一节描述的是当时的待办，
  **不是当前包清单**（当前清单见本文「包清单」表）。

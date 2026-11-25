# ADR-0010 · 对话域上抛与 org 归属：概念域分派的第三轮应用

| 项 | 内容 |
|---|---|
| **状态** | 已采纳（Accepted） |
| **日期** | 2026-11-26 |
| **背景** | 跨 org 归属深度评测（2026-11-26，四路深挖：kernel 52 包逐包判定 / runtime 接口双源 / 上游现状 / 消费面）复核 v0.70 确立的分层口径——**纯基础设施→oekit；工具/沙箱/执行安全→oagentkit；编排判据→kernel；装配/参考实现→runtime**。该口径按「概念域」分派，不按「实现依赖」分派；本轮把它应用到两个 v0.70 没有枚举的面上：对话域（上下文工程 / 对话状态）与仓本身（org 归属）。 |
| **决策** | ① **七包上抛**（导出面逐字未变，v0.77.0）：`roundguard`→`oagentkit/agent/roundguard`（单 agent 循环判据——循环本体已收敛、差异在循环周边，与多智能体编排无关）；`budget`/`spill`/`offload`→`oagentkit/textx`（对话域上下文工程：输入分段计量 / 输出溢出存档 / 大对象外置）；`pending`/`sessionref`/`memorypipe`→`oagentkit/promptx`（对话域状态与注入：反问挂起态 / 跨会话引用 / 记忆注入）。② **仓归属**：本仓与 goagent-runtime 迁入 `oagentkit` org（module path `github.com/oagentkit/goagent-kernel`）——本仓是框架层（编排判据），真实消费者是框架产品（argus-service），建仓时「宿主业务不公开」的理由已过期（两仓 public、bianque 零引用）；依赖方向早已指向 oagentkit/* + oekit/*。③ **不动面**：`tooldisclose`/`skilldisclose`（有在案裁定，待复核再议）；`trajectory`/`eval`（吃本仓 eventbus 类型，移动成环）；`eventbus`/`worker`/`hookengine` 与上游的邻接面（双源挂账另行裁决）。 |
| **后果** | kernel 收敛为 **43 包纯编排判据层**（契约面 / 状态机 / 副作用闸 / 介入面 / 执行安全合成）；消费方同 wave 迁移（goagent-runtime v0.7.0 / argus-service / code-review-service）。module path 变更后旧 tag 永久可解析（GitHub 重定向），新版本要求消费方改 import。七包上抛后本仓对它们的判据**只剩引用没有实现**——后续若上游判据漂移，本仓的 README 表会过期，由 docsync/门禁兜一致性。 |
| **依据** | 下文「证据」节；`docs/OAGENTKIT-OVERLAP.md`（2026-10-21 快照中「budget 不下沉」的结论在本轮被复核推翻——该结论判的是「不与 textx/compact 合并」，本轮上抛是**平移为姊妹包**而非合并，职责面不重叠的原判定依然成立）；v0.70 的 toolname 两跳先例（先 oekit 后 oagentkit——概念域复核纠偏的既定模式）。 |

> **一句话结论**：分层口径按概念域分派，不按实现依赖——对话域七包（循环判据 +
> 上下文工程 + 对话状态）判据主语不是编排，全部上抛；仓归属与依赖方向对齐，
> kernel/runtime 归 oagentkit org；kernel 收敛为纯编排判据层。

---

## 证据

### 1. 七包的判据主语（逐包核对）

| 包 | 判据主语 | 内部依赖（实测 go list） | 编排词出现处 |
|---|---|---|---|
| `roundguard` | 单 agent 模型侧循环（结局 / 额度 / 无进展） | 仅 oekit/pkg/errors | 无——`Reason`/`Limit`/`StopPolicy`/`Shape` 四张判定词表全部围绕「一次模型往返」 |
| `budget` | 会话上下文窗口（分段计量 / 钳位 / 压实计划） | 仅 oekit/pkg/errors | 无 |
| `spill` | 输出溢出存档（提示契约 / 分页取回 / 按龄清理） | oekit/pkg errors+pathx | 无 |
| `offload` | 内联大对象外置（sha 去重 / 两阶段 / fail-soft） | oekit/pkg errors+stringx+encoding | 无 |
| `pending` | 反问挂起态载体 | 零依赖 | 无 |
| `sessionref` | 跨会话引用注入（有界 / 带来源 / 带失效点） | guardx/injectband + oekit/pkg | 无 |
| `memorypipe` | 记忆注入-引用-打标三段 | oekit/pkg errors+stringx | 无 |

七包在 kernel 内**零生产消费**（除 examples 演示外无 import 方）——「契约面先行」的
建仓哲学让它们的迁移成本恰好最低。

### 2. 消费面实测（2026-11-26）

- goagent-runtime：sideeffect 45 / eventbus 41 / taskboard 35 / checkpoint 31 / … / roundguard 4
- argus-service（GitHub 侧 go.mod 实抓）：kernel v0.75.1 + runtime v0.6.4，18+5 包
- bianque：**零引用**（历史下沉证据链在 eventbus durable/tail 注释，非当前依赖）
- kernel 近 14 天 8686 clones / 1043 uniques——迁移后旧路径经 GitHub 重定向可解析

### 3. org 归属的三条判据

1. **分层事实**：四层栈 oekit（基础机制）→ oagentkit 矩阵（agent 能力）→ kernel（编排判据）→ runtime（装配）。前三层在框架 org，第四层在产品 org——分层连续而 org 断裂。
2. **依赖方向**：kernel go.mod 依赖 oagentkit/{guardx,runtime,toolgate,toolname} + oekit/*；framework 依赖框架 org 而住在产品 org，归属与依赖方向倒挂。
3. **建仓理由过期**：2026-10-18 拆分时「宿主业务不公开」——现两仓 public，宿主（bianque）零引用，理由失效。

### 4. 上抛承接仓的落位

| 包 | 承接 | 层级合规 |
|---|---|---|
| `roundguard` | agent/roundguard | agent(t3) 只依赖 oekit/pkg——tier-0 依赖不变 |
| `budget`/`spill`/`offload` | textx/{budget,spill,offload} | textx(t0) 零矩阵内依赖不变 |
| `pending`/`memorypipe` | promptx/{pending,memorypipe} | promptx(t1) |
| `sessionref` | promptx/sessionref | promptx(t1)→guardx(t0) 向下依赖，合规 |

## 后续动作（挂账）

- `tooldisclose` / `skilldisclose` 的归属复核（本轮裁定不动：前者有 2026-10-21 的
  模型面/server 面区分裁定，后者与 skillkit/skill/decision.go 的边界待实测）
- `eventbus.Bus` vs `oagentkit/runtime/progress.Bus`、`worker` vs `queue/worker.Pool`、
  `hookengine` vs `hookx`、`taskboard` 租约 vs `queue.LeaseStore`——四个邻接面各开
  issue 裁决，不在迁移 wave 里夹带

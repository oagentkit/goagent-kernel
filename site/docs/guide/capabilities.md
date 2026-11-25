---
title: 能力详解
---

# 能力详解：九组能力

> 🤖 本页要点已收录进 [llms.txt](https://oagentkit.github.io/goagent-kernel/llms.txt)——AI 代理可直接读取。

本章按「解决什么问题」分组，**先看图、再看字**。逐包的集成片段与坑在仓内
[docs/USAGE.md](https://github.com/oagentkit/goagent-kernel/blob/main/docs/USAGE.md)，
包级事实（行数、依赖）在[包清单与机器事实](../reference/packages.md)。

<div class="diagram">
<svg viewBox="0 0 760 396" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="ar1" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/>
    </marker>
  </defs>
  <rect x="140" y="8" width="200" height="36" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
  <text x="240" y="31" text-anchor="middle" fill="var(--k-diagram-text)" font-size="13">LLM 模型 / 程序</text>
  <rect x="420" y="8" width="200" height="36" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
  <text x="520" y="31" text-anchor="middle" fill="var(--k-diagram-text)" font-size="13">宿主工具（MCP 等）</text>
  <line x1="240" y1="44" x2="240" y2="60" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar1)"/>
  <line x1="520" y1="44" x2="520" y2="60" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar1)"/>
  <g font-size="12.5" fill="var(--k-diagram-text)">
    <rect x="20" y="68" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="130" y="94" text-anchor="middle">① 报告与协议</text>
    <text x="130" y="118" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">protocol · 容错解析 · 白名单校验 · 渲染</text>
    <rect x="270" y="68" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="380" y="94" text-anchor="middle">② 工具判定环</text>
    <text x="380" y="118" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">tooldisclose · guardset · toolfail</text>
    <rect x="520" y="68" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="630" y="94" text-anchor="middle">③ 编排契约与续跑</text>
    <text x="630" y="118" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">contract · statemachine · checkpoint · commitgate</text>
    <rect x="20" y="156" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="130" y="182" text-anchor="middle">④ 上下文三件套</text>
    <text x="130" y="206" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">budget · spill · offload</text>
    <rect x="270" y="156" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="380" y="182" text-anchor="middle">⑤ 程序化编排</text>
    <text x="380" y="206" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">ptc · sandbox · egress</text>
    <rect x="520" y="156" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="630" y="182" text-anchor="middle">⑥ 实时与流式</text>
    <text x="630" y="206" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">eventbus · watchdog · delta · drain · pending</text>
    <rect x="20" y="244" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="130" y="270" text-anchor="middle">⑦ 安全面</text>
    <text x="130" y="294" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">netguard · pii（已上移上游）</text>
    <rect x="270" y="244" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="380" y="270" text-anchor="middle">⑧ 副作用只做一次</text>
    <text x="380" y="294" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">retryplan · sideeffect · sidetest</text>
    <rect x="520" y="244" width="220" height="72" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="630" y="270" text-anchor="middle">⑨ 协作与治理</text>
    <text x="630" y="294" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">taskboard · skilldisclose · hookengine · memorypipe…</text>
  </g>
  <line x1="380" y1="316" x2="380" y2="338" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar1)"/>
  <rect x="200" y="344" width="360" height="38" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
  <text x="380" y="368" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="12">宿主设施：存储 · 通道 · 拓扑 · 文案（经接口注入，内核只判定）</text>
</svg>
</div>
<p class="diagram-cap">能力全景：模型与工具的每一次交互都被中间三层机制管住，落点是宿主自己的设施。</p>

## 1 · 报告与协议：`protocol`

**解决**：模型输出的 JSON 不可信——字段漂移、置信虚高、越权字段。
**形态**：五级容错解析 → 白名单校验 → 注入式渲染。

- 解析分信任级：`ParseLLMReport`（剥引擎侧字段，解析 LLM 产出用这个）与
  `ParseReport`（落库往返入口）；
- 校验分两级：warn 留痕不拦截，critical 才 `ErrViolation` 整份拦；
- 渲染三档，标题与文案走 `RenderOptions` 注入；
- 证据面：`AssignEvidenceIDs` / `CheckEvidenceRefs`（编号唯一、引用完整）。

**什么时候用**：凡是「LLM 产出 → 结构化下游消费」的出口。

## 2 · 工具判定环：`tooldisclose` + `guardset` + `toolfail` + `policy` + `dispatch`

**解决**：工具调用的「披露、守卫、失败、审批、拓扑」五件事若各走各的路，必有绕过点。

<div class="diagram">
<svg viewBox="0 0 760 360" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="chk1" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
    <marker id="chk2" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-green)"/></marker>
    <marker id="chk3" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-red)"/></marker>
  </defs>
  <g font-size="11" fill="var(--k-diagram-text)">
    <rect x="14" y="76" width="150" height="36" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="89" y="99" text-anchor="middle">普通工具调用</text>
    <rect x="14" y="124" width="150" height="36" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="89" y="147" text-anchor="middle">ptc 程序内调用</text>
    <rect x="14" y="172" width="150" height="36" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="89" y="195" text-anchor="middle">子进程侧调用</text>
    <rect x="200" y="40" width="256" height="200" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="328" y="64" text-anchor="middle" font-size="12">invoke.Chain（逐环短路 · 顺序即语义）</text>
    <rect x="214" y="78" width="228" height="40" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="328" y="95" text-anchor="middle">policy.Stage · 审批门</text>
    <text x="328" y="111" text-anchor="middle" font-size="9" fill="var(--k-diagram-text-mute)">上游 toolgate/policy</text>
    <rect x="214" y="124" width="228" height="40" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="328" y="141" text-anchor="middle">tooldisclose.Stage · 披露门</text>
    <text x="328" y="157" text-anchor="middle" font-size="9" fill="var(--k-diagram-text-mute)">目录内 + schema 已取回</text>
    <rect x="214" y="170" width="228" height="40" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="328" y="187" text-anchor="middle">guardset.Stage · 一并求值</text>
    <text x="328" y="203" text-anchor="middle" font-size="9" fill="var(--k-diagram-text-mute)">多条拒绝合成一条</text>
    <text x="328" y="230" text-anchor="middle" font-size="9" fill="var(--k-diagram-text-mute)">每环都是链上的一环 · 先后由宿主管线决定</text>
    <rect x="490" y="60" width="254" height="56" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="617" y="82" text-anchor="middle" fill="var(--k-diagram-green)">通过 → 宿主执行工具</text>
    <text x="617" y="102" text-anchor="middle" font-size="9.5" fill="var(--k-diagram-text-mute)">结果回灌模型；判定环不执行、不解释工具语义</text>
    <rect x="490" y="140" width="254" height="88" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="617" y="162" text-anchor="middle" fill="var(--k-diagram-red)">拒绝 → 结构化 Verdict（不是 error）</text>
    <text x="617" y="184" text-anchor="middle" font-size="9.5" fill="var(--k-diagram-text-mute)">Reason：给模型看的是 Guidance（能照着做）</text>
    <text x="617" y="202" text-anchor="middle" font-size="9.5" fill="var(--k-diagram-text-mute)">留痕另取 Reason()（全且准，给人看）</text>
    <text x="617" y="220" text-anchor="middle" font-size="9.5" fill="var(--k-diagram-text-mute)">链上抛 error = 这一环自己坏了</text>
  </g>
  <line x1="164" y1="94" x2="196" y2="86" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#chk1)"/>
  <line x1="164" y1="142" x2="196" y2="134" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#chk1)"/>
  <line x1="164" y1="190" x2="196" y2="182" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#chk1)"/>
  <line x1="456" y1="110" x2="486" y2="88" stroke="var(--k-diagram-green)" stroke-width="1.5" marker-end="url(#chk2)"/>
  <line x1="456" y1="170" x2="486" y2="180" stroke="var(--k-diagram-red)" stroke-width="1.5" marker-end="url(#chk3)"/>
  <g font-size="10.5" fill="var(--k-diagram-text-mute)">
    <text x="16" y="272">少接一段，那里就是整条链上唯一绕过所有门禁的路径——模型写的程序里的调用也不例外。</text>
    <text x="16" y="294">guardset 把自己接成链上的**一环**：链照旧短路，但这一环内部已经把该说的都说了。</text>
    <text x="16" y="316" fill="var(--k-diagram-red)">披露门也可以直接作为 guardset 的一条 guard 一并求值——两种装配都成立，关键是三处入口走同一条链。</text>
    <text x="16" y="342">拒绝是「这一环不通过」，不是「程序出错」——模型据此改参数或换工具，不该看到异常。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">三处入口汇入同一条 invoke 链：链上每一环都是独立判定，顺序即语义；拒绝是一个 Verdict，不是一个异常。</p>

- `tooldisclose` **渐进式披露**：工具面按需展开，真 schema 要用检索工具取回；
  **被拒不激活**——「schema 未取回」可重试但原样重试无效，「不在目录」不可重试；
- `guardset` **多守卫合成**：相互独立的判定**一并求值**，多条拒绝合成一条，
  可重试取「全部可重试才算」的保守侧——不要用逐个过、第一个拒绝就收口的链；
- `toolfail` **失败是数据表**：类别 → 可重试性 / 退避 / 需人工 / **重发安全** /
  模型指引；只看 `Retryable` 不看 `SafeToResubmit`，重试会把一次副作用变成两次；
- `policy.Stage` 把**审批门接进判定环**：工具调用、程序内调用、子进程侧调用
  走同一条 invoke 链，审批规则只装配一次。

**什么时候用**：只要你放一个工具给模型。**全部接进同一条 `invoke.Stage` 链**。

## 3 · 编排契约与续跑：`contract` + `statemachine` + `checkpoint` + `commitgate`

**解决**：状态存哪、怎么流转、崩了怎么接着跑、终态怎么独占。

<div class="diagram">
<svg viewBox="0 0 720 226" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="ar3" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/>
    </marker>
  </defs>
  <text x="20" y="22" font-size="13" fill="var(--k-diagram-text)">checkpoint：续跑而非重放</text>
  <text x="392" y="22" font-size="13" fill="var(--k-diagram-text)">commitgate：终态独占</text>
  <line x1="360" y1="34" x2="360" y2="210" stroke="var(--k-diagram-stroke)"/>
  <g font-size="12" fill="var(--k-diagram-text)">
    <rect x="20" y="42" width="130" height="34" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="85" y="63" text-anchor="middle">崩溃前快照</text>
    <rect x="196" y="42" width="130" height="34" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="261" y="63" text-anchor="middle">BuildPlan（纯函数）</text>
    <rect x="20" y="106" width="306" height="34" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="173" y="127" text-anchor="middle">跳过已终态 · 重跑缺口 · 并行按前驱采信</text>
  </g>
  <line x1="150" y1="59" x2="192" y2="59" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar3)"/>
  <line x1="261" y1="76" x2="261" y2="102" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar3)"/>
  <text x="20" y="172" font-size="11" fill="var(--k-diagram-text-mute)">可信边界按依赖判定（失序宁可多跑）· 终态不自动重开 · 序号单调闸</text>
  <g font-size="12" fill="var(--k-diagram-text)">
    <rect x="392" y="42" width="112" height="34" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="448" y="63" text-anchor="middle">Acquire 令牌</text>
    <rect x="528" y="42" width="92" height="34" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="574" y="63" text-anchor="middle">提交终态</text>
    <rect x="644" y="42" width="66" height="34" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="677" y="63" text-anchor="middle">Release</text>
    <rect x="392" y="106" width="318" height="34" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="551" y="127" text-anchor="middle">放闸凭令牌，不比对内容 —— 同名/同序也放不错</text>
  </g>
  <line x1="504" y1="59" x2="524" y2="59" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar3)"/>
  <line x1="620" y1="59" x2="640" y2="59" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar3)"/>
  <text x="392" y="172" font-size="11" fill="var(--k-diagram-text-mute)">同一 (run, 环节) 的终态只允许提交一次 —— 「两条分支都赢了」不可能</text>
</svg>
</div>
<p class="diagram-cap">左：恢复判定是纯函数，不是宿主的临场决定；右：终态独占权凭令牌，不靠内容比对认人。</p>

- `contract`：`RunContext` / `StepSink` / `ToolBinder` / `Clock`——
  编排引擎与宿主状态的**唯一边界**（纯接口面，零实现）；
- `statemachine`：环节状态机纯谓词（含 `interrupted` 终态：中止 ≠ 失败）；
- `checkpoint`：`StoreCAS` 跨进程 CAS 存取、并行 fan-out 按各自前驱采信；
- `commitgate`：`Acquire` / `ReleaseLease` 凭令牌放闸。

**什么时候用**：编排超过一个环节、或「崩了不能全量重跑」的时候。

## 4 · 上下文三件套：`budget` + `spill` + `offload`

**解决**：上下文窗口是有限预算，不是无限历史。

<div class="diagram">
<svg viewBox="0 0 720 242" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="ar4" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/>
    </marker>
  </defs>
  <rect x="270" y="92" width="200" height="52" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
  <text x="370" y="115" text-anchor="middle" fill="var(--k-diagram-text)" font-size="13">上下文窗口</text>
  <text x="370" y="133" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">有限预算，不是无限历史</text>
  <g font-size="12" fill="var(--k-diagram-text)">
    <rect x="20" y="38" width="200" height="50" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="120" y="59" text-anchor="middle">budget 输入侧</text>
    <text x="120" y="77" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">分段计量 · 压实计划 · 毛/净分列</text>
    <rect x="500" y="38" width="200" height="50" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="600" y="59" text-anchor="middle">spill 输出侧</text>
    <text x="600" y="77" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">溢出存档 · 分页取回 · 按龄清理</text>
    <rect x="270" y="182" width="200" height="50" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="370" y="203" text-anchor="middle">offload 大对象</text>
    <text x="370" y="221" text-anchor="middle" fill="var(--k-diagram-text-mute)" font-size="10.5">两阶段外置：首次只标记不替换</text>
  </g>
  <line x1="220" y1="72" x2="280" y2="100" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar4)"/>
  <line x1="500" y1="72" x2="452" y2="100" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar4)"/>
  <line x1="370" y1="182" x2="370" y2="148" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar4)"/>
</svg>
</div>
<p class="diagram-cap">三件套各管一段：进来的计量与压实、出去的溢出与取回、躺在线里的大对象。</p>

- `budget`：区分「工具面膨胀」与「历史膨胀」；要减账用 `NetRemovedTokens`（净）；
- `spill`：截断 + 分页取回，提示契约定死；
- `offload`：**首次只标记不替换**——直接替换会让模型引用一个自己没看过的东西。

**什么时候用**：工具面大、历史长、或产出含大对象（图片、日志、报告全文）。

## 5 · 程序化编排：`ptc` + `sandbox` + `egress` + `preset`

**解决**：N 轮 ReAct 往返的上下文成本与原始数据过模型面的问题。

<div class="diagram">
<svg viewBox="0 0 720 270" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="ar5" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/>
    </marker>
    <marker id="ar5b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-red)"/>
    </marker>
  </defs>
  <text x="20" y="22" font-size="13" fill="var(--k-diagram-text-mute)">N 轮 ReAct 往返 —— 每轮原始数据都过模型面</text>
  <g font-size="12" fill="var(--k-diagram-text)">
    <rect x="30" y="36" width="110" height="34" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="85" y="57" text-anchor="middle">模型</text>
    <rect x="260" y="36" width="110" height="34" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="315" y="57" text-anchor="middle">工具</text>
  </g>
  <line x1="140" y1="46" x2="256" y2="46" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar5)"/>
  <line x1="260" y1="62" x2="144" y2="62" stroke="var(--k-diagram-red)" stroke-width="1.5" marker-end="url(#ar5b)"/>
  <text x="410" y="58" font-size="12" fill="var(--k-diagram-text-mute)">× N 轮，上下文被撑爆</text>
  <line x1="20" y1="96" x2="700" y2="96" stroke="var(--k-diagram-stroke)"/>
  <text x="20" y="126" font-size="13" fill="var(--k-diagram-text)">ptc —— 塌缩成 1 轮，原始数据不过模型面</text>
  <g font-size="12" fill="var(--k-diagram-text)">
    <rect x="20" y="142" width="120" height="36" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="80" y="164" text-anchor="middle">模型写程序</text>
    <rect x="182" y="142" width="110" height="36" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="237" y="164" text-anchor="middle">沙箱执行</text>
    <rect x="334" y="142" width="130" height="36" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="399" y="164" text-anchor="middle">bindings 调工具 ×N</text>
    <rect x="506" y="142" width="120" height="36" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="566" y="164" text-anchor="middle">归并小结果</text>
  </g>
  <line x1="140" y1="160" x2="178" y2="160" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar5)"/>
  <line x1="292" y1="160" x2="330" y2="160" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar5)"/>
  <line x1="464" y1="160" x2="502" y2="160" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar5)"/>
  <path d="M566,142 C566,118 120,118 88,138" fill="none" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar5)"/>
  <text x="20" y="218" font-size="11" fill="var(--k-diagram-text-mute)">三重预算（墙钟 / 调用数 / 字节数）替代 OS 沙盘 · 纯 Go 零 CGO</text>
  <text x="20" y="242" font-size="11" fill="var(--k-diagram-text-mute)">程序内 host.&lt;tool&gt;() 走同一套判定环 · egress 管子进程出口 · 命令级判据在上游 tools</text>
</svg>
</div>
<p class="diagram-cap">同一个任务：上面每一轮的原始数据都要挤进上下文；下面只有一次往返，原始数据留在沙箱里。</p>

- `ptc`：绑定可带 Signature（净化名，撞名装配期拒）；
- `sandbox`：档位声明与兑现（`sandbox.Capable`）——子进程后端用 cgroup v2 `memory.max`
  兑现 `MaxMemory`，**建不出即拒绝执行**；
- `cmdpolicy`（**上游 `oagentkit/tools/cmdpolicy`**）：命令级判据（前缀 token 规则 + 三态裁决，规则自带样例、加载期自检）；
- `preset`：把「档位 × 审批 × 出口」收成一份具名组合。

**什么时候用**：一次任务要聚合多次工具调用（多表查询、批量巡检、逐机采样）。

## 6 · 实时事件与流式：`eventbus` + `watchdog` + `delta` + `drain` + `pending`

- `eventbus`：进程内有损广播——**先落库再广播**，SSE 断了靠 DB 回放游标补达；
- `watchdog`：工具调用配对监视（收口/幽灵调用的判据）；
- `delta`：增量节流（`Pacer` **每环节新建**，长生命周期会内存涨、收口卡住）；
- `drain`：优雅排水（用 `DrainSession` 而非全局 `Drain`）；
- `pending`：多轮挂起态载体（落列必须走 `pending.ColumnValue`）。

## 7 · 安全面：出站守卫与脱敏（**两包已上移上游**）

<div class="diagram">
<svg viewBox="0 0 760 280" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="ar7" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
  </defs>
  <text x="16" y="24" font-size="12.5" fill="var(--k-diagram-text)">netguard（上游 guardx）：出站请求的四道放行判据，逐层收紧</text>
  <g font-size="11" fill="var(--k-diagram-text)">
    <rect x="16" y="40" width="164" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="98" y="62" text-anchor="middle">① 名字层</text>
    <text x="98" y="80" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">指向内网 / 元数据的域名</text>
    <rect x="206" y="40" width="164" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="288" y="62" text-anchor="middle">② 逐 IP 过出口策略</text>
    <text x="288" y="80" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">解析出的每个 IP 都要判</text>
    <rect x="396" y="40" width="164" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="478" y="62" text-anchor="middle">③ 重定向逐跳重验</text>
    <text x="478" y="80" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">下一跳重新走一遍①②③</text>
    <rect x="586" y="40" width="158" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="665" y="62" text-anchor="middle">④ 建连后核对</text>
    <text x="665" y="80" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">核对真实 RemoteAddr</text>
  </g>
  <line x1="180" y1="69" x2="202" y2="69" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar7)"/>
  <line x1="370" y1="69" x2="392" y2="69" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar7)"/>
  <line x1="560" y1="69" x2="582" y2="69" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar7)"/>
  <text x="16" y="120" font-size="10.5" fill="var(--k-diagram-text-mute)">④ 闭合的是 rebinding 窗口：名字判过之后，域名解析结果可能在建连前被换掉。</text>
  <line x1="16" y1="140" x2="744" y2="140" stroke="var(--k-diagram-stroke)"/>
  <text x="16" y="164" font-size="12.5" fill="var(--k-diagram-text)">pii：内核只回报信号，阻断动作由宿主执行</text>
  <g font-size="11" fill="var(--k-diagram-text)">
    <rect x="16" y="180" width="196" height="48" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="114" y="200" text-anchor="middle">待检文本</text>
    <text x="114" y="218" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">上下文 / 报告 / 日志</text>
    <rect x="248" y="180" width="196" height="48" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="346" y="200" text-anchor="middle">pii.Apply 检出</text>
    <text x="346" y="218" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">手机号 / 身份证四重校验</text>
    <rect x="480" y="180" width="264" height="48" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="612" y="200" text-anchor="middle">返回 (文本, true)</text>
    <text x="612" y="218" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">true = 宿主应当阻断，动作由你执行</text>
  </g>
  <line x1="212" y1="204" x2="244" y2="204" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar7)"/>
  <line x1="444" y1="204" x2="476" y2="204" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#ar7)"/>
  <text x="16" y="258" font-size="10.5" fill="var(--k-diagram-red)">放行面具名且零值即严：不配置 = 全拒绝（静默放行是最贵的失效形态）。</text>
  <text x="16" y="276" font-size="10.5" fill="var(--k-diagram-text-mute)">内核不抛错、不中断控制流——它只说「该阻断」，怎么处置是你的事。</text>
</svg>
</div>
<p class="diagram-cap">出站四道判据逐层收紧，最后一道在建连之后核对真实地址；脱敏只回报信号，处置在宿主。</p>

- `netguard`（**上游 `oagentkit/guardx/netguard`**）：出站 SSRF 守卫——名字层 + 逐 IP 过 egress + 重定向逐跳重验 +
  `DialFunc` 建连后核对真实 RemoteAddr（闭合 rebinding 窗口）；
- `pii`（**上游 `oekit/pkg/pii`**）：个人信息脱敏（手机号/身份证四重校验），内核只回报信号，阻断由宿主执行。

**什么时候用**：模型/工具能碰外网，或报告/上下文里可能进个人信息。

## 8 · 副作用只做一次：`retryplan` + `sideeffect` + `sidetest`

<div class="diagram">
<svg viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="ar8" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
  </defs>
  <text x="16" y="24" font-size="12.5" fill="var(--k-diagram-text)">retryplan：两个正交维度决定处置，四个格子四种处置</text>
  <text x="16" y="46" font-size="10.5" fill="var(--k-diagram-text-mute)">纵轴＝可重发安全（重发会不会重复施加副作用）　横轴＝值得重试</text>
  <g font-size="11" fill="var(--k-diagram-text)">
    <rect x="16" y="58" width="186" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="109" y="80" text-anchor="middle">可重试 + 可重发</text>
    <text x="109" y="100" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">放心重试</text>
    <rect x="212" y="58" width="186" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="305" y="80" text-anchor="middle">可重试 + 不可重发</text>
    <text x="305" y="100" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">先确认上次是否已生效</text>
    <rect x="16" y="130" width="186" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="109" y="152" text-anchor="middle">不可重试 + 可重发</text>
    <text x="109" y="172" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">重发也解决不了，别重试</text>
    <rect x="212" y="130" width="186" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="305" y="152" text-anchor="middle">不可重试 + 不可重发</text>
    <text x="305" y="172" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">终止，交人工</text>
  </g>
  <text x="16" y="212" font-size="10.5" fill="var(--k-diagram-red)">只看「可重试」不看「可重发」——超时类最典型：值得重试，但服务端可能已执行。</text>
  <line x1="424" y1="40" x2="424" y2="200" stroke="var(--k-diagram-stroke)"/>
  <text x="444" y="60" font-size="12.5" fill="var(--k-diagram-text)">sideeffect：一次原子操作顶三件事</text>
  <g font-size="11" fill="var(--k-diagram-text)">
    <rect x="444" y="72" width="300" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="594" y="97" text-anchor="middle">幂等键 · 同一逻辑动作重试不重复施加</text>
    <rect x="444" y="120" width="300" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="594" y="145" text-anchor="middle">租约 · 同一资源独占，带 TTL</text>
    <rect x="444" y="168" width="300" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="594" y="193" text-anchor="middle">栅栏令牌 · 过期持有者的迟到写入被拒</text>
  </g>
  <line x1="16" y1="226" x2="744" y2="226" stroke="var(--k-diagram-stroke)"/>
  <g font-size="10.5" fill="var(--k-diagram-text-mute)">
    <text x="16" y="250">拆成三份存储就会出现「租约过了但幂等没过」的夹缝，允许同一动作被施加两次。</text>
    <text x="16" y="270">承诺的是**至多一次**而非 exactly-once——后者要求下游也接受幂等键。</text>
    <text x="16" y="290" fill="var(--k-diagram-red)">施加失败且无法判定远端是否已生效 ⇒ 结果未知：该键禁止重试，必须先对账。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">左：先分「值不值得重试」与「能不能原样重发」；右：三件事共用一次原子操作，别拆成三份存储。</p>

**接线范式**：`retryplan` 判策（`Retryable` × `SafeToResubmit` 两个正交维度）→
`sideeffect` 执行（幂等键 + 租约 + 栅栏）。`sidetest` 给你的存储实现一份**验收标准**。

## 9 · 协作与治理面

`taskboard`（多 agent 认领与依赖）· `skilldisclose`（技能渐进披露）·
`hookengine`（钩子装配）· `memorypipe`（记忆管道）· `trajectory`（轨迹）·
`toolname`（工具名净化）· `gatecheck`（审批 × 档位联合判据）·
`sessionref` / `worktree` / `delegate`（会话引用 / 工作树 / 委派）。
跨宿主必须一致的判定语义**只在上游有一份**：失败分类 `guardx/toolfail`、
隔离档位 `guardx/sandbox`、审批门 `toolgate/policy`、出站守卫 `guardx/netguard`、
一次性票据 `guardx/ticket`、命令级裁决 `tools/cmdpolicy`、
派发可达性 `oagentkit/agent/dispatch`——本仓不保留转发门面（`facades` 段为空）。
本仓的 `dispatch` 是**派发失败面**（transient / fatal / other 三档 + 兜底改派环），
不是可达性门面。
**模型侧回合循环的判据在本仓 `roundguard`**（结局分类 / 额度 / 无进展，Experimental）——
它与 `dispatch` 同属「失败与循环怎么判」这一族（USAGE.md 的 ⑩ 判定与改派），
**不属协作治理面**，故不进上面的清单；详见 [模型接入](./llm-guide.md) 的「Agent Loop」一节。

## 怎么选：三个判断

1. **这判定换个宿主要不要改？** 要 → 写在宿主适配层；不要 → 可以内核机制化。
2. **这语义跨宿主必须一致吗？**（失败分类/隔离档位/终态独占）→ 用内核的机制包，别自研。
3. **这条路径绕过判定环了吗？** 任何工具到达模型的执行面都要过同一条 `invoke.Stage` 链；
   发现第二条路 = 缺陷。

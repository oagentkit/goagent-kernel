---
title: 模型接入
---

# 给大模型的接入指南

> 🤖 本页要点已收录进 [llms.txt](https://oagentkit.github.io/goagent-kernel/llms.txt)——AI 代理可直接读取。

这一页只回答一个问题：**把模型接进来之后，模型到底看到什么、要吐出什么、被拒了会怎样、
上下文从哪来。**

内核的核心命题是「编排是代码，LLM 只做单环节」。这句话落到接线层面是三条硬边界：
**模型只在一个环节里发挥；内核只判定、不处置；处置与领域资产归宿主。**
下面每一节都按这三条边界展开——先给图，再给能照着做的口径。

## 0 · 三方分工：模型做什么，内核做什么，宿主做什么

<div class="diagram">
<svg viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg1a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
    <marker id="lg1b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-purple)"/></marker>
  </defs>
  <g font-size="12.5" fill="var(--k-diagram-text)">
    <rect x="250" y="12" width="230" height="76" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-purple)"/>
    <text x="365" y="38" text-anchor="middle" font-size="13.5">模型（大模型）</text>
    <text x="365" y="58" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">产出结构化结果 · 选择工具</text>
    <text x="365" y="75" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">环节内自由发挥，出了环节交回契约</text>
    <rect x="250" y="132" width="230" height="76" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="365" y="158" text-anchor="middle" font-size="13.5">内核（机制面）</text>
    <text x="365" y="178" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">解析校验产出 · 判定工具准入</text>
    <text x="365" y="195" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">失败分类 · 回合额度 · 续跑终态</text>
    <rect x="250" y="252" width="230" height="76" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="365" y="278" text-anchor="middle" font-size="13.5">宿主（你的产品）</text>
    <text x="365" y="298" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">装上下文 · 执行工具 · 落库</text>
    <text x="365" y="315" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">阻断 / 重试 / 审批 · 领域资产与文案</text>
  </g>
  <line x1="352" y1="88" x2="352" y2="126" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg1a)"/>
  <line x1="398" y1="132" x2="398" y2="94" stroke="var(--k-diagram-purple)" stroke-width="1.6" marker-end="url(#lg1b)"/>
  <line x1="352" y1="208" x2="352" y2="246" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg1a)"/>
  <line x1="398" y1="252" x2="398" y2="214" stroke="var(--k-diagram-purple)" stroke-width="1.6" marker-end="url(#lg1b)"/>
  <text x="358" y="112" font-size="9.5" fill="var(--k-diagram-text-mute)">产出</text>
  <text x="404" y="112" font-size="9.5" fill="var(--k-diagram-text-mute)">判定结论 / 回灌</text>
  <text x="358" y="232" font-size="9.5" fill="var(--k-diagram-text-mute)">判定</text>
  <text x="404" y="232" font-size="9.5" fill="var(--k-diagram-text-mute)">处置动作</text>
  <g font-size="10" fill="var(--k-diagram-text-mute)">
    <text x="20" y="46">模型侧只有两件事：</text>
    <text x="20" y="64">① 产出结构化结果</text>
    <text x="20" y="82">② 在工具面里选择</text>
    <text x="20" y="108" fill="var(--k-diagram-red)">流程顺序、失败口径、</text>
    <text x="20" y="126" fill="var(--k-diagram-red)">隔离档位都不该由模型决定</text>
    <text x="500" y="152">内核只判定不处置：</text>
    <text x="500" y="170">回报「该阻断 / 该重试 /</text>
    <text x="500" y="188">不可归类」，动作由宿主执行</text>
    <text x="500" y="272">宿主的活儿：装上下文、执行工具、</text>
    <text x="500" y="290">落库、审批，外加全部领域资产</text>
  </g>
</svg>
</div>
<p class="diagram-cap">三方分工：模型在一个环节里发挥，内核把「不信任」变成「可判定」，宿主执行动作并持有领域知识。</p>

判定口诀一句就够：**「这个包需要知道自己在给谁干活吗？」需要 ⇒ 属于宿主。**
换宿主要改的代码不该在内核里——这条不是风格偏好，而是本仓的门禁
（`harness-no-host-dependency`）逐条在判。

## 1 · 模型要交什么：产出契约

模型产出的 JSON 不可信：字段会漂、置信会虚高、越权字段会混进来。
内核给出的不是「解析器」，而是**一条带信任级的流水线**——能修就修，但该拦的拦死。

<div class="diagram">
<svg viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg2a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
    <marker id="lg2b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-purple)"/></marker>
  </defs>
  <g font-size="12" fill="var(--k-diagram-text)">
    <rect x="16" y="128" width="132" height="56" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="82" y="152" text-anchor="middle">模型产出</text>
    <text x="82" y="170" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">一段 JSON 文本</text>
    <rect x="196" y="36" width="212" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="302" y="60" text-anchor="middle">ParseLLMReport</text>
    <text x="302" y="80" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">剥掉 5 个引擎侧字段</text>
    <rect x="196" y="128" width="212" height="56" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="302" y="152" text-anchor="middle">ParseReport</text>
    <text x="302" y="170" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">保留引擎字段 · 落库往返</text>
    <rect x="452" y="84" width="140" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="522" y="108" text-anchor="middle">Validate</text>
    <text x="522" y="126" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">warn 留痕 / critical 拦死</text>
    <rect x="624" y="84" width="120" height="58" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="684" y="108" text-anchor="middle">Render</text>
    <text x="684" y="126" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">标题走注入</text>
  </g>
  <line x1="148" y1="140" x2="192" y2="70" stroke="var(--k-diagram-purple)" stroke-width="1.6" marker-end="url(#lg2b)"/>
  <line x1="148" y1="168" x2="192" y2="156" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg2a)"/>
  <line x1="408" y1="65" x2="448" y2="104" stroke="var(--k-diagram-purple)" stroke-width="1.6" marker-end="url(#lg2b)"/>
  <line x1="408" y1="156" x2="448" y2="122" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg2a)"/>
  <line x1="592" y1="113" x2="620" y2="113" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg2a)"/>
  <text x="164" y="62" font-size="9.5" fill="var(--k-diagram-purple)">模型产出走这条</text>
  <text x="164" y="188" font-size="9.5" fill="var(--k-diagram-text-mute)">落库往返走这条</text>
  <line x1="16" y1="212" x2="744" y2="212" stroke="var(--k-diagram-stroke)"/>
  <g font-size="10.5" fill="var(--k-diagram-text-mute)">
    <text x="16" y="236">五级容错解析：去空白 → 剥围栏 → 键名正则 → 标量归一 → JSON 解码；</text>
    <text x="16" y="256">解不开走修复通道，再不行走抽取通道（只捞出能认出的对象）。</text>
    <text x="16" y="284" fill="var(--k-diagram-red)">输入超过 2 MiB ⇒ 判「不是报告」直接失败（不截断——截断曾让一份 3.4 MB 的产出</text>
    <text x="16" y="304" fill="var(--k-diagram-red)">「解析成功」且 confidence=high）；经修复救回的报告，置信度被钳为 low。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">两个解析入口不是别名：走错等于把呈现权交给模型；上游是「能不能修」，下游是「该不该拦」。</p>

模型侧要守的规矩：

- **产出里不要带引擎侧字段。** `ParseLLMReport` 会剥掉恰好五个引擎字段
  （`followups` / `meta` / `profile` / `awaiting_user` / `guidance`）；
  走 `ParseReport` 等于把呈现权交给模型——它**不是** `ParseLLMReport` 的别名。
- **置信度是序数档位，不是概率。** `high / medium / low` 三档；
  数值置信度在另一套 `Verdict` 信封里，且**跨 kind 的置信不可比**。
  把「我大概有 0.8 的把握」塞进 ordinal 字段，是把不可比的量当可比的量用。
- **被拦下来的原因分两级。** `warn` 只留痕（不拦），`critical` 整份拦
  （`ErrViolation`）。典型 critical：置信度缺失或越界、裁决立场不在
  `adopt / dispute / reject`、`scope=out` 却没有 `redirect`、
  可执行类动作没声明 `requires_approval`。
- **证据要能查。** `AssignEvidenceIDs` 编号、`CheckEvidenceRefs` 查引用完整性；
  片段超长（200 字符）只 warn，**不截断**——被截断的证据等于没有证据。

## 2 · 模型看得见什么：工具面按需披露

工具面一次性全量铺给模型，模型就会在几百个描述里瞎猜。这里是**两阶段披露**：
第一轮只给名称与描述，真正的 schema 要模型自己去取。

<div class="diagram">
<svg viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg3a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
    <marker id="lg3b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-red)"/></marker>
  </defs>
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <rect x="16" y="24" width="176" height="62" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="104" y="48" text-anchor="middle">第 1 轮：工具面</text>
    <text x="104" y="68" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">名称 + 描述 + 占位 schema</text>
    <rect x="228" y="24" width="176" height="62" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="316" y="48" text-anchor="middle">模型检索工具</text>
    <text x="316" y="68" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">按关键词取回真实 schema</text>
    <rect x="440" y="24" width="176" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="528" y="48" text-anchor="middle">目录 Load</text>
    <text x="528" y="68" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">「披露」的唯一写入口</text>
    <rect x="628" y="24" width="116" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="686" y="48" text-anchor="middle">按真实 schema</text>
    <text x="686" y="68" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">调用</text>
  </g>
  <line x1="192" y1="55" x2="224" y2="55" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg3a)"/>
  <line x1="404" y1="55" x2="436" y2="55" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg3a)"/>
  <line x1="616" y1="55" x2="624" y2="55" stroke="var(--k-diagram-blue)" stroke-width="1.6" marker-end="url(#lg3a)"/>
  <line x1="16" y1="112" x2="744" y2="112" stroke="var(--k-diagram-stroke)"/>
  <text x="16" y="136" font-size="12" fill="var(--k-diagram-text)">被拒时，模型看到的是「为什么 + 下一步该干什么」，两种拒绝的可重试性不同：</text>
  <g font-size="11" fill="var(--k-diagram-text)">
    <rect x="16" y="152" width="356" height="70" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="30" y="174" fill="var(--k-diagram-red)">ErrNotLoaded（在目录里，schema 没取回）</text>
    <text x="30" y="194" font-size="10" fill="var(--k-diagram-text-mute)">指引：先用检索工具取回完整 schema，再调用</text>
    <text x="30" y="212" font-size="10" fill="var(--k-diagram-text-mute)">可重试 = true，但「原样重试不会成功」——它只认 schema</text>
    <rect x="388" y="152" width="356" height="70" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="402" y="174" fill="var(--k-diagram-red)">ErrNotInCatalog（压根不存在这个工具）</text>
    <text x="402" y="194" font-size="10" fill="var(--k-diagram-text-mute)">指引：请不要重试，改用目录内的其他工具</text>
    <text x="402" y="212" font-size="10" fill="var(--k-diagram-text-mute)">可重试 = false；认不出的错误一律判不可重试</text>
  </g>
  <text x="16" y="252" font-size="10.5" fill="var(--k-diagram-text-mute)">检索只排序、不激活：无命中就返回空，不兜底给「随便几个」；默认给 3 条、最多 5 条。</text>
  <text x="16" y="276" font-size="10.5" fill="var(--k-diagram-red)">注意：被拒**不会**顺手激活工具——那会让「猜参数 ⇒ 被拒 ⇒ 原样重试 ⇒ 放行」成为最短路径。</text>
</svg>
</div>
<p class="diagram-cap">披露是两步：先给「有这个工具」，再给「怎么调」；被拒给的是可执行的下一步，不是「禁止」。</p>

占位 schema 是**有意的**：第一轮给模型的 schema 是个空壳（类型 `none`），
描述里挂一句提示——「schema 未加载：先用检索工具按关键词取回本工具的完整 schema，再调用」。
理由很实在：模型读的是描述里的祈使句，**一个空 schema 不会被它理解成「禁止调用」**。

拒绝在链上**不是 error，而是一个 `invoke.Verdict`**：
`Allow:false` + `Reason`（给模型看的指引）+ `Retryable`（给宿主看的机械结论）。
链上抛 error 表示「这个门自己坏了」，不是「这个工具被拒了」——两者别混。

## 3 · 模型被拒之后：判定环与失败分类

工具调用的「披露、守卫、失败、审批、派发」五件事若各走各的路，必有绕过点。
这里是把它们接成**同一条链**，而不是五个各自为政的检查。

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
<p class="diagram-cap">三处入口汇成一条链；guardset 是链上的一环（内部一并求值），拒绝是结构化 Verdict 而非 error。</p>

被拒之后模型拿到的信息分两路，**别搞混**：

- **给模型的**：`Guidance`——处置指引 + 「可以直接重试 / 可以重试但先确认上一次调用是否已经生效 /
  不要重试」+ 可选的「建议等待 N 秒」+「需要人工介入后才能继续」。
- **给人的**：`Reason`、`Message`、`Code`、`Class`——进日志、进工单，会被改写、含具体数值，
  **不适合进提示词**。

失败分类是**一张表**，不是散落的 if：类别 → 可重试性 / 退避 / 提示延迟 / 需人工 / **可重发** / 处置指引。

<div class="diagram">
<svg viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <g font-size="11" fill="var(--k-diagram-text)">
    <text x="16" y="22" font-size="12.5" fill="var(--k-diagram-text)">失败是数据表：类别 → 五个维度（上游 guardx/toolfail）</text>
    <rect x="16" y="36" width="150" height="26" rx="6" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="91" y="54" text-anchor="middle" font-size="10.5">类别</text>
    <rect x="170" y="36" width="84" height="26" rx="6" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="212" y="54" text-anchor="middle" font-size="10.5">可重试</text>
    <rect x="258" y="36" width="84" height="26" rx="6" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="300" y="54" text-anchor="middle" font-size="10.5">退避</text>
    <rect x="346" y="36" width="84" height="26" rx="6" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="388" y="54" text-anchor="middle" font-size="10.5">需人工</text>
    <rect x="434" y="36" width="84" height="26" rx="6" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="476" y="54" text-anchor="middle" font-size="10.5">可重发</text>
    <rect x="522" y="36" width="222" height="26" rx="6" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="633" y="54" text-anchor="middle" font-size="10.5">模型看到的处置指引</text>
    <rect x="16" y="68" width="150" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="28" y="88">限流</text>
    <rect x="170" y="68" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="212" y="88" text-anchor="middle">是</text>
    <rect x="258" y="68" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="300" y="88" text-anchor="middle">是</text>
    <rect x="346" y="68" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="388" y="88" text-anchor="middle">否</text>
    <rect x="434" y="68" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="476" y="88" text-anchor="middle">是</text>
    <rect x="522" y="68" width="222" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="534" y="88" font-size="10">等一会儿再发（唯一可原样重发的类）</text>
    <rect x="16" y="104" width="150" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="28" y="124">鉴权 / 权限</text>
    <rect x="170" y="104" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="212" y="124" text-anchor="middle">否</text>
    <rect x="258" y="104" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="300" y="124" text-anchor="middle">—</text>
    <rect x="346" y="104" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="388" y="124" text-anchor="middle">是</text>
    <rect x="434" y="104" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="476" y="124" text-anchor="middle">否</text>
    <rect x="522" y="104" width="222" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="534" y="124" font-size="10">需要人工介入后才能继续</text>
    <rect x="16" y="140" width="150" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="28" y="160">超时类</text>
    <rect x="170" y="140" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="212" y="160" text-anchor="middle">是</text>
    <rect x="258" y="140" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="300" y="160" text-anchor="middle">是</text>
    <rect x="346" y="140" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="388" y="160" text-anchor="middle">否</text>
    <rect x="434" y="140" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="476" y="160" text-anchor="middle">否</text>
    <rect x="522" y="140" width="222" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="534" y="160" font-size="10">可以重试，但先确认上次是否已生效</text>
    <rect x="16" y="176" width="150" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="28" y="196">不可归类</text>
    <rect x="170" y="176" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="212" y="196" text-anchor="middle">否</text>
    <rect x="258" y="176" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="300" y="196" text-anchor="middle">—</text>
    <rect x="346" y="176" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="388" y="196" text-anchor="middle">否</text>
    <rect x="434" y="176" width="84" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="476" y="196" text-anchor="middle">否</text>
    <rect x="522" y="176" width="222" height="30" rx="6" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="534" y="196" font-size="10">不可分类 = 不可重试（保守缺省）</text>
  </g>
  <line x1="16" y1="222" x2="744" y2="222" stroke="var(--k-diagram-stroke)"/>
  <g font-size="10.5" fill="var(--k-diagram-text-mute)">
    <text x="16" y="246">「值得重试」与「可以原样重发」是两个正交维度：超多半值得重试，可服务端那边请求可能已经执行完毕。</text>
    <text x="16" y="266">只看「可重试」不看「可重发」，工具重试会把一次副作用变成两次——这是最贵的一类错。</text>
    <text x="16" y="290" fill="var(--k-diagram-red)">不可归类 ⇒ 不可重试：重试一个本来不会成功的调用，是把一次失败放大成 N 次。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">失败分类是表不是 if 链；「值得重试」不等于「可以原样重发」，两者要分开读。</p>

顺带一提：**派发失败**是另一套词表（本仓 `dispatch` 包）——三档
（瞬态 / 终态 / 不确定），判定面**只读首次失败的原始错误**。
它问的是「换个执行者值不值」，上游那套问的是「原样重发安不安全」，
两套词表刻意不照搬。

## 4 · Agent Loop：模型侧循环怎么转，什么时候必须停

> 这一节是整页的重点：**agent loop 的循环本体早就收敛成同一段代码**——模型请求工具就执行、
> 回灌结果、继续；模型给终答就停。各家实现真正不一样的，是**循环周边**的三问：
>
> - **这次还能不能转？**（额度）
> - **这一回合以什么结束？**（结局）
> - **这一圈是不是白转？**（无进展）
>
> 这三问放在宿主侧的后果不是「写法风格不同」，而是**同一段循环在 A 宿主算「第 12 圈超了」、
> 在 B 宿主算「第 8 圈还能转」**，而且没有任何机器能发现——两边都觉得自己是对的。
>
> 内核把这三问收成判据面（`roundguard`）：纯函数、零领域词、**内核不调模型**。宿主负责把
> 机械事实映射进来，内核只给结论。

<div class="diagram">
<svg viewBox="0 0 760 545" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="alA" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
    <marker id="alC" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-green)"/></marker>
  </defs>
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <rect x="60" y="28" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="74" y="44" font-size="9" fill="var(--k-diagram-blue)">宿主</text>
    <text x="74" y="60">① 组装 Snapshot：结局 + 用量 + 回合历史</text>
    <rect x="60" y="82" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="74" y="98" font-size="9" fill="var(--k-diagram-purple)">内核</text>
    <text x="74" y="114">② Classify → Route：这一回合以什么结束</text>
    <rect x="60" y="136" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="74" y="152" font-size="9" fill="var(--k-diagram-purple)">内核</text>
    <text x="74" y="168">③ 额度判定：回合数 / 调用总数 / 单工具</text>
    <rect x="60" y="190" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="74" y="206" font-size="9" fill="var(--k-diagram-blue)">宿主</text>
    <text x="74" y="222">④ 跑模型（内核不调模型）</text>
    <rect x="60" y="244" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="74" y="260" font-size="9" fill="var(--k-diagram-blue)">宿主</text>
    <text x="74" y="276">⑤ 执行工具（过同一条判定环）</text>
    <rect x="60" y="298" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="74" y="314" font-size="9" fill="var(--k-diagram-blue)">宿主</text>
    <text x="74" y="330">⑥ 回灌结果给模型（被拒也要回灌）</text>
    <rect x="60" y="352" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="74" y="368" font-size="9" fill="var(--k-diagram-purple)">内核</text>
    <text x="74" y="384">⑦ Detect：无进展五形态</text>
    <rect x="60" y="406" width="440" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="74" y="422" font-size="9" fill="var(--k-diagram-purple)">内核</text>
    <text x="74" y="438">⑧ Decide → Decision：一条可执行结论</text>
  </g>
  <g stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#alA)">
    <line x1="280" y1="70" x2="280" y2="78"/>
    <line x1="280" y1="124" x2="280" y2="132"/>
    <line x1="280" y1="178" x2="280" y2="186"/>
    <line x1="280" y1="232" x2="280" y2="240"/>
    <line x1="280" y1="286" x2="280" y2="294"/>
    <line x1="280" y1="340" x2="280" y2="348"/>
    <line x1="280" y1="394" x2="280" y2="402"/>
  </g>
  <g font-size="10" fill="var(--k-diagram-text-mute)">
    <text x="520" y="140">额度必须在调用前判：</text>
    <text x="520" y="156">调用后判＝最后一次昂贵调用</text>
    <text x="520" y="172">已经发生了。</text>
    <text x="520" y="356">指纹与归一指纹由宿主算；</text>
    <text x="520" y="372">Exempt：轮询/查状态、</text>
    <text x="520" y="388">刻意的规划步。</text>
    <text x="520" y="410">判定优先级：结局终局 &gt;</text>
    <text x="520" y="426">额度耗尽 &gt; 无进展 &gt; 继续</text>
  </g>
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <rect x="60" y="470" width="140" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="130" y="487" text-anchor="middle">继续</text>
    <text x="130" y="503" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">按原样进下一回合</text>
    <rect x="212" y="470" width="140" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="282" y="487" text-anchor="middle">温和叫停</text>
    <text x="282" y="503" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">产物可用，不是失败</text>
    <rect x="364" y="470" width="140" height="42" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="434" y="487" text-anchor="middle">失败即止</text>
    <text x="434" y="503" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">失败型结局</text>
  </g>
  <path d="M60,491 L30,491 L30,49 L56,49" fill="none" stroke="var(--k-diagram-green)" stroke-width="1.5" marker-end="url(#alC)"/>
  <text x="24" y="290" font-size="10" fill="var(--k-diagram-green)" transform="rotate(-90 24 290)">下一回合</text>
  <text x="16" y="16" font-size="11" fill="var(--k-diagram-text-mute)">宿主跑循环，内核只给判据——分界线是「机械事实」：内核不调模型、不认识工具语义</text>
</svg>
</div>
<p class="diagram-cap">回合的顺序不能改：先判额度再花钱，结局优先于额度，无进展只警告不停止。</p>

先把三个容易混的词分开——**混用本身就是口径漂移的来源**：

| 词 | 是什么 | 谁定义 |
|---|---|---|
| **轮次** turn | 一次**编排会话运行** | `eventbus.EventTurnStarted/Ended` |
| **环节** step | 宿主定义的**业务步骤** | `statemachine.StepStatus` |
| **回合** round | 模型侧**一次往返**（一次模型调用 + 其工具执行） | 本章的单位 |

「回合」是内核判定的单位，「轮次」是事件流的单位，「环节」是业务语义的单位。三者**不是同一个计数器**。

### 4.1 结局分类：10 个取值，先具体后笼统

宿主把这一回合的机械事实（`Outcome`：有没有调工具、完没完成、是不是被中止）映射进来，
内核给一个 `Reason` 与一个 `Continue`。

<div class="diagram">
<svg viewBox="0 0 760 450" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <text x="16" y="22" font-size="11" fill="var(--k-diagram-text-mute)">Classify 的判定序（取证据最硬的那条）</text>
    <rect x="16" y="32" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="28" y="52">Aborted → aborted</text>
    <rect x="16" y="68" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="28" y="88">Failed → failed</text>
    <rect x="16" y="104" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="28" y="124">Handoff → handed_off</text>
    <rect x="16" y="140" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="28" y="160">ToolCalls &gt; 0 → tool_calls（继续）</text>
    <rect x="16" y="176" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="28" y="196">ForcedFinal → forced_final</text>
    <rect x="16" y="212" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="28" y="232">Completed → completed</text>
    <rect x="16" y="248" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="28" y="268">Paused → paused（继续）</text>
    <rect x="16" y="284" width="270" height="30" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="28" y="304">都不成立 → no_outcome</text>
    <rect x="302" y="32" width="442" height="76" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="316" y="54">继续（Continue = true）· 2 值</text>
    <text x="316" y="74" font-size="10" fill="var(--k-diagram-text-mute)">tool_calls 要工具 · paused 服务端要求原样再问（pause_turn）</text>
    <text x="316" y="92" font-size="10" fill="var(--k-diagram-text-mute)">挂起态的回合「暂停」不是「停止」——它要等外部条件回来。</text>
    <rect x="302" y="124" width="442" height="100" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="316" y="146">失败型（IsFailure() = true）· 4 值</text>
    <text x="316" y="166" font-size="10" fill="var(--k-diagram-text-mute)">aborted 中止 · failed 报错 · no_outcome 读不懂/零值形态</text>
    <text x="316" y="184" font-size="10" fill="var(--k-diagram-text-mute)">budget_exhausted 额度用尽（fail_fast 口径下）</text>
    <text x="316" y="206" font-size="10" fill="var(--k-diagram-red)">no_outcome 是保守收敛：宁可判「读不懂」，也不冒充任何结局。</text>
    <rect x="302" y="240" width="442" height="100" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="316" y="262">温和 / 终局但不是失败 · 4 值</text>
    <text x="316" y="282" font-size="10" fill="var(--k-diagram-text-mute)">completed 终答 · forced_final 强制收尾（产物仍可用）</text>
    <text x="316" y="300" font-size="10" fill="var(--k-diagram-text-mute)">handed_off 交接 · stalled 温和叫停</text>
    <text x="316" y="322" font-size="10" fill="var(--k-diagram-yellow)">这四个上报时别当故障——否则温和收停会变成假红。</text>
  </g>
  <g font-size="10" fill="var(--k-diagram-text-mute)">
    <text x="16" y="350">Classify 只给 8 个取值；budget_exhausted 与 stalled 由 Decide 判出 ⇒ 一张词表覆盖「这一回合怎么结束的」。</text>
    <text x="16" y="374" fill="var(--k-diagram-red)">为什么「要工具」排在「给终答」前面：模型可以同时给文本与工具调用——此时</text>
    <text x="16" y="392" fill="var(--k-diagram-red)">「还要工具」才是控制流（文本是给用户看的）。把终答排前面 ⇒ 工具还没执行</text>
    <text x="16" y="410" fill="var(--k-diagram-red)">就判完成，那是谎报已完成。</text>
    <text x="16" y="434">判定词表只能引用本包的常量，别处手抄同一个字符串 ⇒ 消费方与出口会漂。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">结局是一张表：8 个由 Classify 给、2 个由 Decide 判出，宿主按三桶处置。</p>

- **「要工具」优先于「给终答」**——模型可以一边收尾一边补一个工具调用，这时该看工具调用。
- **`no_outcome` 是保守收敛**：零值形态读不懂时判「读不懂」，不冒充任何结局。
- **`forced_final` 不是失败**：那是宿主已禁用工具、要求模型用已收集的证据给出终答，**产物仍然可用**。
- **`paused` 仍然继续**：挂起态要等外部条件回来，不是结束。

### 4.2 额度：维度是事实，处置是策略，而且必须在调用前

<div class="diagram">
<svg viewBox="0 0 760 400" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="alE" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
    <marker id="alF" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-green)"/></marker>
  </defs>
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <text x="16" y="18" font-size="11" fill="var(--k-diagram-text-mute)">三档额度：维度是事实（各家一致），处置是策略（各家没共识）</text>
    <rect x="16" y="30" width="236" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="30" y="52">LimitRounds · 回合数</text>
    <text x="30" y="70" font-size="10" fill="var(--k-diagram-text-mute)">Budget.MaxRounds = 0 表示不限</text>
    <text x="30" y="84" font-size="10" fill="var(--k-diagram-text-mute)">数的是回合，不是 provider 请求数</text>
    <rect x="266" y="30" width="236" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="280" y="52">LimitToolCalls · 调用总数</text>
    <text x="280" y="70" font-size="10" fill="var(--k-diagram-text-mute)">Budget.MaxToolCalls = 0 表示不限</text>
    <text x="280" y="84" font-size="10" fill="var(--k-diagram-text-mute)">整轮工具调用总和</text>
    <rect x="516" y="30" width="236" height="58" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="530" y="52">LimitPerTool · 单工具</text>
    <text x="530" y="70" font-size="10" fill="var(--k-diagram-text-mute)">Budget.MaxPerTool（开放键）</text>
    <text x="530" y="84" font-size="10" fill="var(--k-diagram-text-mute)">值 0 = 该工具不限</text>
    <text x="16" y="118" font-size="11" fill="var(--k-diagram-text-mute)">一次回合里的判定时机：三个额度门都在花钱之前</text>
    <rect x="16" y="128" width="176" height="44" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="104" y="146" text-anchor="middle">CheckRounds</text>
    <text x="104" y="163" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">模型调用前</text>
    <rect x="212" y="128" width="196" height="44" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="310" y="146" text-anchor="middle">CheckToolCalls</text>
    <text x="310" y="163" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">模型调用前</text>
    <rect x="428" y="128" width="324" height="44" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="590" y="146" text-anchor="middle">CheckTool</text>
    <text x="590" y="163" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">每个工具执行前各判一次</text>
    <rect x="16" y="196" width="236" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="30" y="218">fail_fast · 硬失败</text>
    <text x="30" y="236" font-size="10" fill="var(--k-diagram-text-mute)">Reason = budget_exhausted</text>
    <text x="30" y="252" font-size="10" fill="var(--k-diagram-text-mute)">额度一撞就判失败</text>
    <rect x="266" y="196" width="236" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="280" y="218">force_final · 强制收尾（缺省）</text>
    <text x="280" y="236" font-size="10" fill="var(--k-diagram-text-mute)">Reason = forced_final</text>
    <text x="280" y="252" font-size="10" fill="var(--k-diagram-text-mute)">禁用工具 + 要一次终答，产物可用</text>
    <rect x="516" y="196" width="236" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="530" y="218">缺省不是「随便」</text>
    <text x="530" y="236" font-size="10" fill="var(--k-diagram-text-mute)">Budget.Policy 没设 ⇒ 走 force_final</text>
    <text x="530" y="252" font-size="10" fill="var(--k-diagram-text-mute)">校验用 EffectivePolicy()，别用 Valid()</text>
    <rect x="16" y="282" width="368" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="30" y="304">护栏一：Budget 必须先 Validate</text>
    <text x="30" y="322" font-size="10" fill="var(--k-diagram-text-mute)">负值上限在校验前是 fail-open（≤0 一律判成「不限」）——</text>
    <text x="30" y="336" font-size="10" fill="var(--k-diagram-text-mute)">「负值 = 不限」语义上是错的，故非法声明要在装配期被拒。</text>
    <rect x="400" y="282" width="352" height="62" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="414" y="304">护栏二：未登记的工具 ID 一律放行</text>
    <text x="414" y="322" font-size="10" fill="var(--k-diagram-text-mute)">CheckTool 对没登记额度的工具返回 Allowed=true——</text>
    <text x="414" y="336" font-size="10" fill="var(--k-diagram-text-mute)">没登记额度就不管它，而不是当成「额度为 0 ⇒ 拒」。</text>
  </g>
  <g stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#alE)">
    <line x1="192" y1="150" x2="208" y2="150"/>
    <line x1="408" y1="150" x2="424" y2="150"/>
  </g>
  <path d="M212,180 L212,186 L586,186 L586,180" fill="none" stroke="var(--k-diagram-green)" stroke-width="1.5" marker-end="url(#alF)"/>
  <text x="16" y="366" font-size="10" fill="var(--k-diagram-text-mute)">五家主流实现三种口径：硬失败 / 强制收尾 / 触顶后再调一次模型产出终答 ⇒ 处置是策略不是事实。</text>
  <text x="16" y="386" font-size="10" fill="var(--k-diagram-red)">被额度拒掉的工具调用，宿主仍要回灌一条失败结果给模型——否则配不上对，下一轮请求会被 provider 拒。</text>
</svg>
</div>
<p class="diagram-cap">额度维度各家一致，处置口径在五家实现里有三种；所以内核只判事实，口径由宿主选。</p>

- **额度维度（事实）各家一致，处置口径（策略）没有共识**——所以内核分开给：`Budget` 管事实，
  `Policy` 管口径。处置口径不要在宿主侧各写一遍分流分支，两条分支迟早漂成同一个值。
- **`Budget` 必须先 `Validate()`**：未校验的负值上限在判定里是 fail-open（`≤0` 一律判成「不限」）。
- **计量器是进程内的**：`Meter` 带锁、零值可用，但跨进程续跑时回合数**不随之累计**——要全局累计就得
  自己落库，并把 `Used` 灌回本包。

### 4.3 无进展：五种形态，首档只警告不停止

<div class="diagram">
<svg viewBox="0 0 760 470" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <text x="16" y="20" font-size="11" fill="var(--k-diagram-text-mute)">判定序（取病因最具体的那条）：error_spiral → repeat → oscillation → drift → idle</text>
    <rect x="16" y="32" width="520" height="54" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="30" y="54">error_spiral 错误螺旋：连着失败</text>
    <text x="30" y="74" font-size="10" fill="var(--k-diagram-text-mute)">阈值 k</text>
    <rect x="16" y="92" width="520" height="54" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="30" y="114">repeat 精确重复：同指纹连着来</text>
    <text x="30" y="134" font-size="10" fill="var(--k-diagram-text-mute)">阈值 k</text>
    <rect x="16" y="152" width="520" height="54" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-purple)"/>
    <text x="30" y="174">oscillation 震荡：A B A B …</text>
    <text x="30" y="194" font-size="10" fill="var(--k-diagram-text-mute)">阈值 2k（需要更长的窗口才看得出「在来回摆」）</text>
    <rect x="16" y="212" width="520" height="54" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="30" y="234">drift 缓慢漂移：归一后同、原指纹不同</text>
    <text x="30" y="254" font-size="10" fill="var(--k-diagram-text-mute)">阈值 k</text>
    <rect x="16" y="272" width="520" height="54" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-text-mute)"/>
    <text x="30" y="294">idle 空转：有回合、零工具、未收敛</text>
    <text x="30" y="314" font-size="10" fill="var(--k-diagram-text-mute)">阈值 k</text>
    <rect x="552" y="32" width="192" height="146" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="566" y="54">首档 Warn</text>
    <text x="566" y="74" font-size="10" fill="var(--k-diagram-text-mute)">连续长度 L == 阈值</text>
    <text x="566" y="92" font-size="10" fill="var(--k-diagram-text-mute)">只警告回灌，不停止</text>
    <text x="566" y="110" font-size="10" fill="var(--k-diagram-text-mute)">给模型一次自我修正的</text>
    <text x="566" y="126" font-size="10" fill="var(--k-diagram-text-mute)">机会（Continue 仍为 true）</text>
    <text x="566" y="150" font-size="10" fill="var(--k-diagram-red)">一档之差就是</text>
    <text x="566" y="166" font-size="10" fill="var(--k-diagram-red)">「继续」与「停」的分界</text>
    <rect x="552" y="186" width="192" height="150" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="566" y="208">停止档 Stalled</text>
    <text x="566" y="228" font-size="10" fill="var(--k-diagram-text-mute)">连续长度 L &gt; 阈值</text>
    <text x="566" y="246" font-size="10" fill="var(--k-diagram-text-mute)">温和叫停（不是失败）</text>
    <text x="566" y="272" font-size="10" fill="var(--k-diagram-text-mute)">k = Repeat 缺省 3，</text>
    <text x="566" y="288" font-size="10" fill="var(--k-diagram-text-mute)">下界钳位到 2</text>
    <text x="566" y="306" font-size="10" fill="var(--k-diagram-text-mute)">窗口下界 2k+2 = 8</text>
    <text x="566" y="324" font-size="10" fill="var(--k-diagram-yellow)">判据不可达＝判据不存在</text>
    <text x="16" y="356" font-size="10" fill="var(--k-diagram-text-mute)">Exempt（本回合不参与判定）：① 轮询/查状态类动作——「输出变了才是进展信号」；② 刻意的规划步——</text>
    <text x="16" y="374" font-size="10" fill="var(--k-diagram-text-mute)">每隔 N 步插一个规划回合，它必然零工具调用，按空转判据会被误伤。</text>
    <text x="16" y="396" font-size="10" fill="var(--k-diagram-blue)">Observation.ResultChanged：「相同调用但输出变了」是进展不是重复——只看参数去重会把</text>
    <text x="16" y="414" font-size="10" fill="var(--k-diagram-blue)">「换个页码再查一次」这类有意义的重试误判成循环。</text>
    <text x="16" y="438" font-size="10" fill="var(--k-diagram-text-mute)">指纹与归一指纹都由宿主算：内核若自己决定「什么是易变部分」（数字？时间戳？行号？），就成了领域解释者。</text>
    <text x="16" y="458" font-size="10" fill="var(--k-diagram-red)">Exempt 与归一指纹没有默认值可抄——必须由宿主显式给，否则判据要么误伤要么看不见。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">无进展是「同一件事做了好几遍」的判据：五种病因，先给一次警告，再温和叫停。</p>

### 4.4 「停」有三种含义，宿主不能一律当失败

<div class="diagram">
<svg viewBox="0 0 760 390" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <rect x="16" y="30" width="236" height="210" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-red)"/>
    <text x="30" y="52">失败型</text>
    <text x="30" y="72" font-size="10" fill="var(--k-diagram-text-mute)">aborted · failed · no_outcome</text>
    <text x="30" y="88" font-size="10" fill="var(--k-diagram-text-mute)">budget_exhausted（fail_fast）</text>
    <text x="30" y="116" font-size="10" fill="var(--k-diagram-red)">IsFailure() = true</text>
    <text x="30" y="144" font-size="10" fill="var(--k-diagram-text-mute)">宿主：落失败终态、记失败原因、</text>
    <text x="30" y="160" font-size="10" fill="var(--k-diagram-text-mute)">允许人工介入。</text>
    <text x="30" y="186" font-size="10" fill="var(--k-diagram-text-mute)">「失败型收敛」要能分出来——</text>
    <text x="30" y="202" font-size="10" fill="var(--k-diagram-text-mute)">和下面的温和收停混在一起，</text>
    <text x="30" y="218" font-size="10" fill="var(--k-diagram-text-mute)">温和收停会被当故障上报。</text>
    <rect x="266" y="30" width="236" height="210" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-yellow)"/>
    <text x="280" y="52">强制收尾</text>
    <text x="280" y="72" font-size="10" fill="var(--k-diagram-text-mute)">forced_final</text>
    <text x="280" y="96" font-size="10" fill="var(--k-diagram-text-mute)">宿主已禁用工具，要求模型用</text>
    <text x="280" y="112" font-size="10" fill="var(--k-diagram-text-mute)">已收集的证据给出终答。</text>
    <text x="280" y="140" font-size="10" fill="var(--k-diagram-text-mute)">产物仍然可用 ⇒ 不是失败。</text>
    <text x="280" y="166" font-size="10" fill="var(--k-diagram-text-mute)">这是额度用尽时最省事也最</text>
    <text x="280" y="182" font-size="10" fill="var(--k-diagram-text-mute)">体面的收口：宁可让它交</text>
    <text x="280" y="198" font-size="10" fill="var(--k-diagram-text-mute)">一份不完整的答案，也别让它</text>
    <text x="280" y="214" font-size="10" fill="var(--k-diagram-text-mute)">无限转下去。</text>
    <rect x="516" y="30" width="236" height="210" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-green)"/>
    <text x="530" y="52">温和叫停</text>
    <text x="530" y="72" font-size="10" fill="var(--k-diagram-text-mute)">stalled（无进展停止档）</text>
    <text x="530" y="96" font-size="10" fill="var(--k-diagram-text-mute)">IsFailure() = false，产物可用。</text>
    <text x="530" y="124" font-size="10" fill="var(--k-diagram-green)">上报时别写「失败」——</text>
    <text x="530" y="140" font-size="10" fill="var(--k-diagram-green)">它是被判据叫停的，不是坏了。</text>
    <text x="530" y="166" font-size="10" fill="var(--k-diagram-text-mute)">首档（Warn）时 Continue 仍是</text>
    <text x="530" y="182" font-size="10" fill="var(--k-diagram-text-mute)">true——只警告回灌，不停止。</text>
    <rect x="16" y="258" width="736" height="100" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="30" y="280">读 Decision 的三个字段：别只看一个</text>
    <text x="30" y="302" font-size="10" fill="var(--k-diagram-text-mute)">Continue：是否按原样进入下一回合（false 有多种含义，靠 Reason 区分）。</text>
    <text x="30" y="318" font-size="10" fill="var(--k-diagram-text-mute)">Warn：无进展首档——只该警告回灌，不停止。</text>
    <text x="30" y="334" font-size="10" fill="var(--k-diagram-red)">Limit：命中的额度类别——无进展命中时首档与停止档都置 LimitStalled，单看它判不出停不停。</text>
    <text x="30" y="378" font-size="10" fill="var(--k-diagram-text-mute)">Decision.Used 与 Snapshot.Used 共享同一份 Tools 映射（不是副本）——要审计凭据，自己留一份副本。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">同一个「停」字底下是三种处置：判据只给结论，宿主按 Reason 决定怎么收。</p>

### 4.5 组合结论与接线

`Decide(Snapshot, Budget, Config)` 把上面三张判据收成**一条可执行结论**，宿主拿到就能直接决定
「继续 / 温和叫停 / 失败即止」，不必自己把三张判据重新拼一遍（各宿主各拼一套 = 同一段循环在不同
宿主得到不同结论，且没有任何机器能发现）。

- **`Snapshot` 三项事实缺一不可**：结局（这一回合以什么结束）、用量（额度还剩多少）、回合历史
  （最近这几圈是不是在原地打转）。内核只对这三组机械事实做判定。
- **判定优先级不可改**：结局终局 > 额度耗尽 > 无进展 > 继续。结局已是终局时，**不给额度或停滞
  改写它的机会**——模型说完了就是完了，哪怕刚好撞上上限。
- **额度用尽时按处置口径分流**：`fail_fast` ⇒ `budget_exhausted`；否则 ⇒ `forced_final`。
- **本包目前无生产调用方**（`roundguard` 自 v0.60.0 定级 **Experimental**，理由：结局分类的取值
  集合与「不得续跑」的判据边界仍待第二个宿主验证）⇒ 拿来先用，别当稳定面写进宿主契约。

### 4.6 另一条路：把 N 轮 ReAct 塌缩成 1 轮

治理回合数有两条路，别混：

| 路线 | 做什么 | 适合 |
|---|---|---|
| **`roundguard`** | 让每一圈转得更有据，并判定何时必须停 | 工具杂、需要模型每轮决策 |
| **`ptc`** | 模型写一段程序 → 进程内 goja VM 执行 → 经 bindings 调宿主工具，**N 轮 ReAct 往返塌缩成 1 轮**，原始数据不过模型面 | 工具链短、确定性高、原始数据大 |

`ptc` 是「**让模型别转那么多圈**」，`roundguard` 是「**转多少圈就必须停**」。两者不冲突：先用 `ptc`
把机械的往返消掉，剩下的判断交给 `roundguard` 兜底。

### 4.7 与既有机制的划界（同名不同域，别互相替代）

| 对照 | 管什么 | 与本节的差别 |
|---|---|---|
| `budget` | 输入侧 token 估算（「下一轮要发多少」） | 量纲不同：token vs 次数，不合并 |
| `cost` | 已发生消费的记账 | 记账 vs 约束 |
| `deadline` | 墙钟时限 | 墙钟 vs 次数 |
| `watchdog` | 工具调用配对 + 编排轮次括号配对 | 它的单位是编排轮次，本节是模型回合 |
| `finalize` / `contract` | 会话级 Cause→终态路由、状态词表 | 本节是回合级结局分类，不产 ctx Cause、不碰会话状态词表 |
| `retryplan` / 上游 `toolfail` | 「该不该重试」「失败算哪一类」 | 本节不重判重试；只在「额度用尽」「无进展」这两种续跑必然再撞的情形下判不得续跑 |

## 5 · 上下文怎么给：有限预算

上下文窗口是有限预算，不是无限历史。三件套各管一段：**进来的计量与压实、
出去的溢出与取回、躺在线里的大对象**。

<div class="diagram">
<svg viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg6a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
  </defs>
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <rect x="16" y="40" width="212" height="58" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="122" y="62" text-anchor="middle">budget 输入侧</text>
    <text x="122" y="82" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">六段计量 · 压实计划</text>
    <rect x="16" y="140" width="212" height="58" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="122" y="162" text-anchor="middle">spill 输出侧</text>
    <text x="122" y="182" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">溢出存档 · 分页取回</text>
    <rect x="16" y="240" width="212" height="58" rx="10" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="122" y="262" text-anchor="middle">offload 大对象</text>
    <text x="122" y="282" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">两阶段外置 · 首次只标记</text>
    <rect x="292" y="120" width="200" height="76" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="392" y="146" text-anchor="middle" font-size="13">上下文窗口</text>
    <text x="392" y="166" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">有限预算</text>
    <text x="392" y="183" text-anchor="middle" font-size="10.5" fill="var(--k-diagram-text-mute)">不是无限历史</text>
    <rect x="548" y="56" width="196" height="76" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="646" y="80" text-anchor="middle">压力分档</text>
    <text x="646" y="98" text-anchor="middle" font-size="10" fill="var(--k-diagram-text-mute)">正常 → 紧张（75%）→ 危急（90%）</text>
    <text x="646" y="116" text-anchor="middle" font-size="10" fill="var(--k-diagram-red)">容量未知 ⇒ 直接判危急</text>
    <rect x="548" y="160" width="196" height="94" rx="10" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="646" y="184" text-anchor="middle">模型实际看到什么</text>
    <text x="560" y="206" font-size="10" fill="var(--k-diagram-text-mute)">截断提示：省略中段 N 字节（全长 M），</text>
    <text x="560" y="222" font-size="10" fill="var(--k-diagram-text-mute)">全文已存档，需要时用工具分页取回</text>
    <text x="560" y="242" font-size="10" fill="var(--k-diagram-text-mute)">外置占位符：原文不在本条消息内，</text>
  </g>
  <line x1="228" y1="69" x2="288" y2="130" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="228" y1="169" x2="288" y2="158" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="228" y1="269" x2="288" y2="186" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="492" y1="146" x2="544" y2="96" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="492" y1="170" x2="544" y2="200" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <g font-size="10.5" fill="var(--k-diagram-text-mute)">
    <text x="16" y="20">压实不调模型：归档与摘要都是宿主实现（摘要是一次 LLM 调用，属于模型面）。</text>
    <text x="16" y="116">先归档、后摘要；摘要失败时原文已归档，可直接重试。</text>
    <text x="16" y="216">减账用净口径：压实掉的毛额与真正减掉的净额不是一个数。</text>
    <text x="16" y="316" fill="var(--k-diagram-red)">offload 首次只标记不替换——首次就替换，模型这一轮从未真正看过那个对象。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">三段各管一头：进来的先计量再压实，出去的超预算就存档，大对象先标记再外置。</p>

- **计量是分段的**（系统 / 工具 / 规则 / 技能 / MCP / 对话六段）。只给总数的话，
  你分不清「工具面膨胀」和「历史膨胀」——两者的处置完全不同。
- **压实由宿主做**。内核给的是「压什么、压多少」的纯函数计划；
  摘要本身是一次 LLM 调用，归模型面，宿主提供实现。
- **截断提示的措辞是契约**：模型看到的是「省略中段 N 字节（全长 M），全文已存档」，
  并被告知可以用工具分页取回——不是一句「输出被截断」。
- **未知容量按危急处理**，不是按「不限」——沉默放行是最贵的失效形态。

## 6 · 崩溃与续跑：模型不在场的时候

这一段与模型无关，但接模型时必须知道：**恢复不是模型的责任**。
模型不在场时，恢复判定是一个纯函数（快照 → 续跑计划），不是谁临场的决定。

<div class="diagram">
<svg viewBox="0 0 760 270" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg7a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="var(--k-diagram-blue)"/></marker>
  </defs>
  <g font-size="11.5" fill="var(--k-diagram-text)">
    <rect x="16" y="20" width="150" height="40" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="91" y="45" text-anchor="middle">崩溃前快照</text>
    <rect x="202" y="20" width="170" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="287" y="45" text-anchor="middle">BuildPlan（纯函数）</text>
    <rect x="16" y="92" width="356" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="194" y="117" text-anchor="middle">跳过已终态 · 重跑缺口 · 并行组按各自前驱采信</text>
    <rect x="420" y="20" width="150" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-blue)"/>
    <text x="495" y="45" text-anchor="middle">Acquire 令牌</text>
    <rect x="586" y="20" width="158" height="40" rx="8" fill="var(--k-diagram-panel)" stroke="var(--k-diagram-stroke)"/>
    <text x="665" y="45" text-anchor="middle">提交终态</text>
    <rect x="420" y="92" width="324" height="40" rx="8" fill="var(--k-diagram-panel-alt)" stroke="var(--k-diagram-stroke)"/>
    <text x="582" y="117" text-anchor="middle">凭唯一令牌放闸：同一条 (run, 环节) 的终态只提交一次</text>
  </g>
  <line x1="166" y1="40" x2="198" y2="40" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg7a)"/>
  <line x1="287" y1="60" x2="287" y2="88" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg7a)"/>
  <line x1="570" y1="40" x2="582" y2="40" stroke="var(--k-diagram-blue)" stroke-width="1.5" marker-end="url(#lg7a)"/>
  <g font-size="10.5" fill="var(--k-diagram-text-mute)">
    <text x="16" y="162">可信边界按依赖判定：默认只信连续完成的前缀，失序时宁可多跑几步。</text>
    <text x="16" y="182">终态不自动重开——拒绝替宿主决定要不要重开一个已完成的环节。</text>
    <text x="16" y="202">序号单调闸：重启后要回灌已持久化的事实，否则闸重启即空（陈旧快照写畅通无阻）。</text>
    <text x="16" y="236" fill="var(--k-diagram-red)">放闸靠凭证不靠内容比对：同名、同序的两条并发分支，只有一条能拿到令牌。</text>
    <text x="16" y="258">落库失败必须释放令牌，否则那个环节再也提交不了终态。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">恢复判定是纯函数；终态独占靠令牌——「两条分支都赢了」在机制上不可能发生。</p>

## 7 · 模型侧十一条纪律

给接模型的人（或给模型看的清单）——每一条背后都有一个具体的失败形态。

| # | 纪律 | 背后的事故形态 |
|---|---|---|
| 1 | 解析模型产出走 `ParseLLMReport`，不走 `ParseReport` | 走错等于把呈现权交给模型 |
| 2 | 产出里不写引擎侧字段 | 五个字段会被剥掉，写了等于没写 |
| 3 | 置信度当序数档位用，不当概率用 | 跨 kind 的置信不可比，混用会得出相反结论 |
| 4 | 工具面按需披露，不一次性铺开 | 模型在几百个描述里瞎猜，越选越错 |
| 5 | 被拒时照指引改参数或换工具，不原样重试 | schema 没取回，原样重试必然再被拒 |
| 6 | 失败先看「可重试」再看「可重发」 | 只看重试会把一次副作用变成两次 |
| 7 | 不可归类的失败不重试 | 把一次失败放大成 N 次 |
| 8 | 额度在调用前判 | 调用后判，最后一次昂贵调用已经发生了 |
| 9 | 上下文按预算分段给，不给无限历史 | 工具面膨胀与历史膨胀的处置完全不同 |
| 10 | 流程顺序、失败口径、隔离档位写进代码，不写进提示词 | 提示词里的「规矩」每次都可能不一样 |
| 11 | 「停」不等于「失败」——按 `Reason.IsFailure()` 分流，不按 `Continue` 一刀切 | `Continue=false` 有三种含义：失败型 / 强制收尾 / 温和叫停；一律当失败，温和收停会变成假红 |

## 8 · 接完线自查

- □ 工具调用、程序内调用、子进程侧调用**走的是同一条判定环**（没有第二条路）。
- □ 解析模型产出用的是 `ParseLLMReport`。
- □ 模型拿到的失败文本是**处置指引**（`Guidance`），不是给人看的 `Message`。
- □ 工具被拒时，宿主仍回灌了一条失败结果给模型。
- □ 额度判定在模型调用**之前**。
- □ 上下文有明确预算，容量未知时按危急处理。
- □ 大对象首次只标记、不替换。
- □ 续跑判定是纯函数；终态提交有令牌。
- □ 额度用尽时选定了处置口径（`fail_fast` 还是 `force_final`），不是各分支各写一遍分流。
- □ 拿到 `Decision` 时读的是 `Reason` + `Continue` + `Warn` 三项，不只看 `Continue`（它有多种含义）。
- □ 无进展判据的指纹与归一指纹由宿主算；轮询/规划步等 `Exempt` 场景显式标了。

## 下一步

- [能力详解](./capabilities.md)——九组能力逐组拆开（含每组「什么时候用」）。
- [怎么用](./getting-started.md)——从零接入的六步路线与最小闭环。
- [坑速查](../reference/pitfalls.md)——症状 → 真因，接完线自查一遍。
- 仓内 [docs/USAGE.md](https://github.com/yi-nology/goagent-kernel/blob/main/docs/USAGE.md)
  ——逐包的「职责 → 集成片段 → 关键符号 → 坑」。
- [llms.txt](https://oagentkit.github.io/goagent-kernel/llms.txt)——给 AI 代理直接读的紧凑事实页。

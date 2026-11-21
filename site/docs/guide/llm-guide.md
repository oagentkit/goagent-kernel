---
title: 模型接入
---

# 给大模型的接入指南

> 🤖 本页要点已收录进 [llms.txt](https://yi-nology.github.io/goagent-kernel/llms.txt)——AI 代理可直接读取。

这一页只回答一个问题：**把模型接进来之后，模型到底看到什么、要吐出什么、被拒了会怎样、
上下文从哪来。**

内核的核心命题是「编排是代码，LLM 只做单环节」。这句话落到接线层面是三条硬边界：
**模型只在一个环节里发挥；内核只判定、不处置；处置与领域资产归宿主。**
下面每一节都按这三条边界展开——先给图，再给能照着做的口径。

## 0 · 三方分工：模型做什么，内核做什么，宿主做什么

<div class="diagram">
<svg viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg1a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
    <marker id="lg1b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#a371f7"/></marker>
  </defs>
  <g font-size="12.5" fill="#e6edf3">
    <rect x="250" y="12" width="230" height="76" rx="10" fill="#1c2330" stroke="#a371f7"/>
    <text x="365" y="38" text-anchor="middle" font-size="13.5">模型（大模型）</text>
    <text x="365" y="58" text-anchor="middle" font-size="10.5" fill="#9aa7b8">产出结构化结果 · 选择工具</text>
    <text x="365" y="75" text-anchor="middle" font-size="10.5" fill="#9aa7b8">环节内自由发挥，出了环节交回契约</text>
    <rect x="250" y="132" width="230" height="76" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="365" y="158" text-anchor="middle" font-size="13.5">内核（机制面）</text>
    <text x="365" y="178" text-anchor="middle" font-size="10.5" fill="#9aa7b8">解析校验产出 · 判定工具准入</text>
    <text x="365" y="195" text-anchor="middle" font-size="10.5" fill="#9aa7b8">失败分类 · 回合额度 · 续跑终态</text>
    <rect x="250" y="252" width="230" height="76" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="365" y="278" text-anchor="middle" font-size="13.5">宿主（你的产品）</text>
    <text x="365" y="298" text-anchor="middle" font-size="10.5" fill="#9aa7b8">装上下文 · 执行工具 · 落库</text>
    <text x="365" y="315" text-anchor="middle" font-size="10.5" fill="#9aa7b8">阻断 / 重试 / 审批 · 领域资产与文案</text>
  </g>
  <line x1="352" y1="88" x2="352" y2="126" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg1a)"/>
  <line x1="398" y1="132" x2="398" y2="94" stroke="#a371f7" stroke-width="1.6" marker-end="url(#lg1b)"/>
  <line x1="352" y1="208" x2="352" y2="246" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg1a)"/>
  <line x1="398" y1="252" x2="398" y2="214" stroke="#a371f7" stroke-width="1.6" marker-end="url(#lg1b)"/>
  <text x="358" y="112" font-size="9.5" fill="#9aa7b8">产出</text>
  <text x="404" y="112" font-size="9.5" fill="#9aa7b8">判定结论 / 回灌</text>
  <text x="358" y="232" font-size="9.5" fill="#9aa7b8">判定</text>
  <text x="404" y="232" font-size="9.5" fill="#9aa7b8">处置动作</text>
  <g font-size="10" fill="#9aa7b8">
    <text x="20" y="46">模型侧只有两件事：</text>
    <text x="20" y="64">① 产出结构化结果</text>
    <text x="20" y="82">② 在工具面里选择</text>
    <text x="20" y="108" fill="#f85149">流程顺序、失败口径、</text>
    <text x="20" y="126" fill="#f85149">隔离档位都不该由模型决定</text>
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
    <marker id="lg2a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
    <marker id="lg2b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#a371f7"/></marker>
  </defs>
  <g font-size="12" fill="#e6edf3">
    <rect x="16" y="128" width="132" height="56" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="82" y="152" text-anchor="middle">模型产出</text>
    <text x="82" y="170" text-anchor="middle" font-size="10" fill="#9aa7b8">一段 JSON 文本</text>
    <rect x="196" y="36" width="212" height="58" rx="10" fill="#161b26" stroke="#a371f7"/>
    <text x="302" y="60" text-anchor="middle">ParseLLMReport</text>
    <text x="302" y="80" text-anchor="middle" font-size="10" fill="#9aa7b8">剥掉 5 个引擎侧字段</text>
    <rect x="196" y="128" width="212" height="56" rx="10" fill="#161b26" stroke="#262e3d"/>
    <text x="302" y="152" text-anchor="middle">ParseReport</text>
    <text x="302" y="170" text-anchor="middle" font-size="10" fill="#9aa7b8">保留引擎字段 · 落库往返</text>
    <rect x="452" y="84" width="140" height="58" rx="10" fill="#161b26" stroke="#262e3d"/>
    <text x="522" y="108" text-anchor="middle">Validate</text>
    <text x="522" y="126" text-anchor="middle" font-size="10" fill="#9aa7b8">warn 留痕 / critical 拦死</text>
    <rect x="624" y="84" width="120" height="58" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="684" y="108" text-anchor="middle">Render</text>
    <text x="684" y="126" text-anchor="middle" font-size="10" fill="#9aa7b8">标题走注入</text>
  </g>
  <line x1="148" y1="140" x2="192" y2="70" stroke="#a371f7" stroke-width="1.6" marker-end="url(#lg2b)"/>
  <line x1="148" y1="168" x2="192" y2="156" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg2a)"/>
  <line x1="408" y1="65" x2="448" y2="104" stroke="#a371f7" stroke-width="1.6" marker-end="url(#lg2b)"/>
  <line x1="408" y1="156" x2="448" y2="122" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg2a)"/>
  <line x1="592" y1="113" x2="620" y2="113" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg2a)"/>
  <text x="164" y="62" font-size="9.5" fill="#a371f7">模型产出走这条</text>
  <text x="164" y="188" font-size="9.5" fill="#9aa7b8">落库往返走这条</text>
  <line x1="16" y1="212" x2="744" y2="212" stroke="#262e3d"/>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="16" y="236">五级容错解析：去空白 → 剥围栏 → 键名正则 → 标量归一 → JSON 解码；</text>
    <text x="16" y="256">解不开走修复通道，再不行走抽取通道（只捞出能认出的对象）。</text>
    <text x="16" y="284" fill="#f85149">输入超过 2 MiB ⇒ 判「不是报告」直接失败（不截断——截断曾让一份 3.4 MB 的产出</text>
    <text x="16" y="304" fill="#f85149">「解析成功」且 confidence=high）；经修复救回的报告，置信度被钳为 low。</text>
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
    <marker id="lg3a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
    <marker id="lg3b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#f85149"/></marker>
  </defs>
  <g font-size="11.5" fill="#e6edf3">
    <rect x="16" y="24" width="176" height="62" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="104" y="48" text-anchor="middle">第 1 轮：工具面</text>
    <text x="104" y="68" text-anchor="middle" font-size="10" fill="#9aa7b8">名称 + 描述 + 占位 schema</text>
    <rect x="228" y="24" width="176" height="62" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="316" y="48" text-anchor="middle">模型检索工具</text>
    <text x="316" y="68" text-anchor="middle" font-size="10" fill="#9aa7b8">按关键词取回真实 schema</text>
    <rect x="440" y="24" width="176" height="62" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="528" y="48" text-anchor="middle">目录 Load</text>
    <text x="528" y="68" text-anchor="middle" font-size="10" fill="#9aa7b8">「披露」的唯一写入口</text>
    <rect x="628" y="24" width="116" height="62" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="686" y="48" text-anchor="middle">按真实 schema</text>
    <text x="686" y="68" text-anchor="middle" font-size="10" fill="#9aa7b8">调用</text>
  </g>
  <line x1="192" y1="55" x2="224" y2="55" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg3a)"/>
  <line x1="404" y1="55" x2="436" y2="55" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg3a)"/>
  <line x1="616" y1="55" x2="624" y2="55" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#lg3a)"/>
  <line x1="16" y1="112" x2="744" y2="112" stroke="#262e3d"/>
  <text x="16" y="136" font-size="12" fill="#e6edf3">被拒时，模型看到的是「为什么 + 下一步该干什么」，两种拒绝的可重试性不同：</text>
  <g font-size="11" fill="#e6edf3">
    <rect x="16" y="152" width="356" height="70" rx="10" fill="#161b26" stroke="#f85149"/>
    <text x="30" y="174" fill="#f85149">ErrNotLoaded（在目录里，schema 没取回）</text>
    <text x="30" y="194" font-size="10" fill="#9aa7b8">指引：先用检索工具取回完整 schema，再调用</text>
    <text x="30" y="212" font-size="10" fill="#9aa7b8">可重试 = true，但「原样重试不会成功」——它只认 schema</text>
    <rect x="388" y="152" width="356" height="70" rx="10" fill="#161b26" stroke="#f85149"/>
    <text x="402" y="174" fill="#f85149">ErrNotInCatalog（压根不存在这个工具）</text>
    <text x="402" y="194" font-size="10" fill="#9aa7b8">指引：请不要重试，改用目录内的其他工具</text>
    <text x="402" y="212" font-size="10" fill="#9aa7b8">可重试 = false；认不出的错误一律判不可重试</text>
  </g>
  <text x="16" y="252" font-size="10.5" fill="#9aa7b8">检索只排序、不激活：无命中就返回空，不兜底给「随便几个」；默认给 3 条、最多 5 条。</text>
  <text x="16" y="276" font-size="10.5" fill="#f85149">注意：被拒**不会**顺手激活工具——那会让「猜参数 ⇒ 被拒 ⇒ 原样重试 ⇒ 放行」成为最短路径。</text>
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
    <marker id="chk1" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
    <marker id="chk2" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#3fb950"/></marker>
    <marker id="chk3" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#f85149"/></marker>
  </defs>
  <g font-size="11" fill="#e6edf3">
    <rect x="14" y="76" width="150" height="36" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="89" y="99" text-anchor="middle">普通工具调用</text>
    <rect x="14" y="124" width="150" height="36" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="89" y="147" text-anchor="middle">ptc 程序内调用</text>
    <rect x="14" y="172" width="150" height="36" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="89" y="195" text-anchor="middle">子进程侧调用</text>
    <rect x="200" y="40" width="256" height="200" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="328" y="64" text-anchor="middle" font-size="12">invoke.Chain（逐环短路 · 顺序即语义）</text>
    <rect x="214" y="78" width="228" height="40" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="328" y="95" text-anchor="middle">policy.Stage · 审批门</text>
    <text x="328" y="111" text-anchor="middle" font-size="9" fill="#9aa7b8">上游 toolgate/policy</text>
    <rect x="214" y="124" width="228" height="40" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="328" y="141" text-anchor="middle">tooldisclose.Stage · 披露门</text>
    <text x="328" y="157" text-anchor="middle" font-size="9" fill="#9aa7b8">目录内 + schema 已取回</text>
    <rect x="214" y="170" width="228" height="40" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="328" y="187" text-anchor="middle">guardset.Stage · 一并求值</text>
    <text x="328" y="203" text-anchor="middle" font-size="9" fill="#9aa7b8">多条拒绝合成一条</text>
    <text x="328" y="230" text-anchor="middle" font-size="9" fill="#9aa7b8">每环都是链上的一环 · 先后由宿主管线决定</text>
    <rect x="490" y="60" width="254" height="56" rx="10" fill="#161b26" stroke="#3fb950"/>
    <text x="617" y="82" text-anchor="middle" fill="#3fb950">通过 → 宿主执行工具</text>
    <text x="617" y="102" text-anchor="middle" font-size="9.5" fill="#9aa7b8">结果回灌模型；判定环不执行、不解释工具语义</text>
    <rect x="490" y="140" width="254" height="88" rx="10" fill="#161b26" stroke="#f85149"/>
    <text x="617" y="162" text-anchor="middle" fill="#f85149">拒绝 → 结构化 Verdict（不是 error）</text>
    <text x="617" y="184" text-anchor="middle" font-size="9.5" fill="#9aa7b8">Reason：给模型看的是 Guidance（能照着做）</text>
    <text x="617" y="202" text-anchor="middle" font-size="9.5" fill="#9aa7b8">留痕另取 Reason()（全且准，给人看）</text>
    <text x="617" y="220" text-anchor="middle" font-size="9.5" fill="#9aa7b8">链上抛 error = 这一环自己坏了</text>
  </g>
  <line x1="164" y1="94" x2="196" y2="86" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#chk1)"/>
  <line x1="164" y1="142" x2="196" y2="134" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#chk1)"/>
  <line x1="164" y1="190" x2="196" y2="182" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#chk1)"/>
  <line x1="456" y1="110" x2="486" y2="88" stroke="#3fb950" stroke-width="1.5" marker-end="url(#chk2)"/>
  <line x1="456" y1="170" x2="486" y2="180" stroke="#f85149" stroke-width="1.5" marker-end="url(#chk3)"/>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="16" y="272">少接一段，那里就是整条链上唯一绕过所有门禁的路径——模型写的程序里的调用也不例外。</text>
    <text x="16" y="294">guardset 把自己接成链上的**一环**：链照旧短路，但这一环内部已经把该说的都说了。</text>
    <text x="16" y="316" fill="#f85149">披露门也可以直接作为 guardset 的一条 guard 一并求值——两种装配都成立，关键是三处入口走同一条链。</text>
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
  <g font-size="11" fill="#e6edf3">
    <text x="16" y="22" font-size="12.5" fill="#e6edf3">失败是数据表：类别 → 五个维度（上游 guardx/toolfail）</text>
    <rect x="16" y="36" width="150" height="26" rx="6" fill="#1c2330" stroke="#262e3d"/>
    <text x="91" y="54" text-anchor="middle" font-size="10.5">类别</text>
    <rect x="170" y="36" width="84" height="26" rx="6" fill="#1c2330" stroke="#262e3d"/>
    <text x="212" y="54" text-anchor="middle" font-size="10.5">可重试</text>
    <rect x="258" y="36" width="84" height="26" rx="6" fill="#1c2330" stroke="#262e3d"/>
    <text x="300" y="54" text-anchor="middle" font-size="10.5">退避</text>
    <rect x="346" y="36" width="84" height="26" rx="6" fill="#1c2330" stroke="#262e3d"/>
    <text x="388" y="54" text-anchor="middle" font-size="10.5">需人工</text>
    <rect x="434" y="36" width="84" height="26" rx="6" fill="#1c2330" stroke="#262e3d"/>
    <text x="476" y="54" text-anchor="middle" font-size="10.5">可重发</text>
    <rect x="522" y="36" width="222" height="26" rx="6" fill="#1c2330" stroke="#262e3d"/>
    <text x="633" y="54" text-anchor="middle" font-size="10.5">模型看到的处置指引</text>
    <rect x="16" y="68" width="150" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="28" y="88">限流</text>
    <rect x="170" y="68" width="84" height="30" rx="6" fill="#161b26" stroke="#3fb950"/>
    <text x="212" y="88" text-anchor="middle">是</text>
    <rect x="258" y="68" width="84" height="30" rx="6" fill="#161b26" stroke="#3fb950"/>
    <text x="300" y="88" text-anchor="middle">是</text>
    <rect x="346" y="68" width="84" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="388" y="88" text-anchor="middle">否</text>
    <rect x="434" y="68" width="84" height="30" rx="6" fill="#161b26" stroke="#3fb950"/>
    <text x="476" y="88" text-anchor="middle">是</text>
    <rect x="522" y="68" width="222" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="534" y="88" font-size="10">等一会儿再发（唯一可原样重发的类）</text>
    <rect x="16" y="104" width="150" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="28" y="124">鉴权 / 权限</text>
    <rect x="170" y="104" width="84" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="212" y="124" text-anchor="middle">否</text>
    <rect x="258" y="104" width="84" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="300" y="124" text-anchor="middle">—</text>
    <rect x="346" y="104" width="84" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="388" y="124" text-anchor="middle">是</text>
    <rect x="434" y="104" width="84" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="476" y="124" text-anchor="middle">否</text>
    <rect x="522" y="104" width="222" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="534" y="124" font-size="10">需要人工介入后才能继续</text>
    <rect x="16" y="140" width="150" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="28" y="160">超时类</text>
    <rect x="170" y="140" width="84" height="30" rx="6" fill="#161b26" stroke="#3fb950"/>
    <text x="212" y="160" text-anchor="middle">是</text>
    <rect x="258" y="140" width="84" height="30" rx="6" fill="#161b26" stroke="#3fb950"/>
    <text x="300" y="160" text-anchor="middle">是</text>
    <rect x="346" y="140" width="84" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="388" y="160" text-anchor="middle">否</text>
    <rect x="434" y="140" width="84" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="476" y="160" text-anchor="middle">否</text>
    <rect x="522" y="140" width="222" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="534" y="160" font-size="10">可以重试，但先确认上次是否已生效</text>
    <rect x="16" y="176" width="150" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="28" y="196">不可归类</text>
    <rect x="170" y="176" width="84" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="212" y="196" text-anchor="middle">否</text>
    <rect x="258" y="176" width="84" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="300" y="196" text-anchor="middle">—</text>
    <rect x="346" y="176" width="84" height="30" rx="6" fill="#161b26" stroke="#262e3d"/>
    <text x="388" y="196" text-anchor="middle">否</text>
    <rect x="434" y="176" width="84" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="476" y="196" text-anchor="middle">否</text>
    <rect x="522" y="176" width="222" height="30" rx="6" fill="#161b26" stroke="#f85149"/>
    <text x="534" y="196" font-size="10">不可分类 = 不可重试（保守缺省）</text>
  </g>
  <line x1="16" y1="222" x2="744" y2="222" stroke="#262e3d"/>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="16" y="246">「值得重试」与「可以原样重发」是两个正交维度：超多半值得重试，可服务端那边请求可能已经执行完毕。</text>
    <text x="16" y="266">只看「可重试」不看「可重发」，工具重试会把一次副作用变成两次——这是最贵的一类错。</text>
    <text x="16" y="290" fill="#f85149">不可归类 ⇒ 不可重试：重试一个本来不会成功的调用，是把一次失败放大成 N 次。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">失败分类是表不是 if 链；「值得重试」不等于「可以原样重发」，两者要分开读。</p>

顺带一提：**派发失败**是另一套词表（本仓 `dispatch` 包）——三档
（瞬态 / 终态 / 不确定），判定面**只读首次失败的原始错误**。
它问的是「换个执行者值不值」，上游那套问的是「原样重发安不安全」，
两套词表刻意不照搬。

## 4 · 一轮里模型做几件事：回合循环

「回合」= 一次模型往返 = 一次模型调用 + 它的工具执行。
这一段是**模型与内核之间的回路**：模型每轮产出被归类，被计量，被判是否还有进展，
然后决定继续还是停。

<div class="diagram">
<svg viewBox="0 0 760 380" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg5a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
    <marker id="lg5b" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#f85149"/></marker>
    <marker id="lg5c" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#a371f7"/></marker>
  </defs>
  <g font-size="11.5" fill="#e6edf3">
    <rect x="16" y="16" width="196" height="52" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="114" y="38" text-anchor="middle">① 调用前：额度判定</text>
    <text x="114" y="56" text-anchor="middle" font-size="10" fill="#9aa7b8">回合数 / 调用数 / 单工具配额</text>
    <rect x="16" y="84" width="196" height="52" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="114" y="106" text-anchor="middle">② 模型调用（宿主执行）</text>
    <text x="114" y="124" text-anchor="middle" font-size="10" fill="#9aa7b8">内核不调模型</text>
    <rect x="16" y="152" width="196" height="52" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="114" y="174" text-anchor="middle">③ 工具执行（判定环内）</text>
    <text x="114" y="192" text-anchor="middle" font-size="10" fill="#9aa7b8">拒绝也要回灌失败结果</text>
    <rect x="256" y="84" width="196" height="52" rx="10" fill="#161b26" stroke="#262e3d"/>
    <text x="354" y="106" text-anchor="middle">④ 归类 Classify → Route</text>
    <text x="354" y="124" text-anchor="middle" font-size="10" fill="#9aa7b8">结局 + 是否继续（宿主注入事实）</text>
    <rect x="256" y="152" width="196" height="52" rx="10" fill="#161b26" stroke="#262e3d"/>
    <text x="354" y="174" text-anchor="middle">⑤ 无进展检测 Detect</text>
    <text x="354" y="192" text-anchor="middle" font-size="10" fill="#9aa7b8">重复 / 震荡 / 漂移 / 错误螺旋 / 空转</text>
    <rect x="496" y="118" width="248" height="52" rx="10" fill="#161b26" stroke="#a371f7"/>
    <text x="620" y="140" text-anchor="middle">⑥ Decide → Decision</text>
    <text x="620" y="158" text-anchor="middle" font-size="10" fill="#9aa7b8">理由 + 是否继续 + 命中上限 + 形态</text>
    <rect x="16" y="248" width="196" height="44" rx="10" fill="#161b26" stroke="#3fb950"/>
    <text x="114" y="275" text-anchor="middle">继续 → 下一回合</text>
    <rect x="496" y="248" width="248" height="44" rx="10" fill="#161b26" stroke="#f85149"/>
    <text x="620" y="275" text-anchor="middle">停：失败型 / 强制终答 / 停滞</text>
  </g>
  <line x1="114" y1="68" x2="114" y2="80" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg5a)"/>
  <line x1="114" y1="136" x2="114" y2="148" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg5a)"/>
  <line x1="212" y1="178" x2="252" y2="110" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg5a)"/>
  <line x1="452" y1="110" x2="492" y2="136" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg5a)"/>
  <line x1="452" y1="178" x2="492" y2="152" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg5a)"/>
  <line x1="620" y1="170" x2="620" y2="244" stroke="#f85149" stroke-width="1.5" marker-end="url(#lg5b)"/>
  <path d="M212,270 C340,270 380,270 420,260 C440,255 460,244 492,240" fill="none" stroke="#3fb950" stroke-width="1.5" marker-end="url(#lg5c)"/>
  <text x="16" y="224" font-size="10" fill="#9aa7b8">判定优先级：</text>
  <text x="16" y="240" font-size="10" fill="#f85149">结局终局 &gt; 额度耗尽 &gt; 无进展 &gt; 继续</text>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="256" y="224">模型说完了就是完了——哪怕刚好撞上上限。</text>
    <text x="256" y="308">「警告回灌」只警告、不停止：给模型一次自我修正的机会。</text>
    <text x="256" y="328">「停」有三种含义，宿主不能一律对待（失败型 / 强制终答 / 停滞）。</text>
    <text x="16" y="348" fill="#f85149">额度判定必须在调用前——调用后检查意味着最后一次昂贵调用已经发生。</text>
    <text x="16" y="368">指纹与归一指纹由宿主算：内核若自己决定「什么算易变部分」，就成了领域解释者。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">回路的顺序不能改：先判额度再花钱，结局优先于额度，无进展只警告不停止。</p>

几个反直觉但很关键的点：

- **内核不调模型，也不认识工具语义。** 回合的「机械事实」（有没有调工具、完没完成、
  是不是被中止）由宿主映射进来；内核只给结论。
- **「暂停」不是「停止」。** 挂起态的回合 `Continue` 仍为真——它要等外部条件回来。
- **额度耗尽默认是软的**：可以「禁用工具再要一次终答」（强制终答，不是失败），
  也可以直接判失败。两种都合法，选哪种是你的事。
- **计量器是进程内的。** 跨进程续跑时回合数**不随之累计**——需要全局计数就得自己落库。
- **被工具拒绝时，宿主仍要回灌一条失败结果给模型**，否则工具调用与结果配不上，
  模型侧看到的是一段断掉的对话。

## 5 · 上下文怎么给：有限预算

上下文窗口是有限预算，不是无限历史。三件套各管一段：**进来的计量与压实、
出去的溢出与取回、躺在线里的大对象**。

<div class="diagram">
<svg viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="lg6a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
  </defs>
  <g font-size="11.5" fill="#e6edf3">
    <rect x="16" y="40" width="212" height="58" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="122" y="62" text-anchor="middle">budget 输入侧</text>
    <text x="122" y="82" text-anchor="middle" font-size="10" fill="#9aa7b8">六段计量 · 压实计划</text>
    <rect x="16" y="140" width="212" height="58" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="122" y="162" text-anchor="middle">spill 输出侧</text>
    <text x="122" y="182" text-anchor="middle" font-size="10" fill="#9aa7b8">溢出存档 · 分页取回</text>
    <rect x="16" y="240" width="212" height="58" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="122" y="262" text-anchor="middle">offload 大对象</text>
    <text x="122" y="282" text-anchor="middle" font-size="10" fill="#9aa7b8">两阶段外置 · 首次只标记</text>
    <rect x="292" y="120" width="200" height="76" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="392" y="146" text-anchor="middle" font-size="13">上下文窗口</text>
    <text x="392" y="166" text-anchor="middle" font-size="10.5" fill="#9aa7b8">有限预算</text>
    <text x="392" y="183" text-anchor="middle" font-size="10.5" fill="#9aa7b8">不是无限历史</text>
    <rect x="548" y="56" width="196" height="76" rx="10" fill="#161b26" stroke="#262e3d"/>
    <text x="646" y="80" text-anchor="middle">压力分档</text>
    <text x="646" y="98" text-anchor="middle" font-size="10" fill="#9aa7b8">正常 → 紧张（75%）→ 危急（90%）</text>
    <text x="646" y="116" text-anchor="middle" font-size="10" fill="#f85149">容量未知 ⇒ 直接判危急</text>
    <rect x="548" y="160" width="196" height="94" rx="10" fill="#161b26" stroke="#262e3d"/>
    <text x="646" y="184" text-anchor="middle">模型实际看到什么</text>
    <text x="560" y="206" font-size="10" fill="#9aa7b8">截断提示：省略中段 N 字节（全长 M），</text>
    <text x="560" y="222" font-size="10" fill="#9aa7b8">全文已存档，需要时用工具分页取回</text>
    <text x="560" y="242" font-size="10" fill="#9aa7b8">外置占位符：原文不在本条消息内，</text>
  </g>
  <line x1="228" y1="69" x2="288" y2="130" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="228" y1="169" x2="288" y2="158" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="228" y1="269" x2="288" y2="186" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="492" y1="146" x2="544" y2="96" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <line x1="492" y1="170" x2="544" y2="200" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg6a)"/>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="16" y="20">压实不调模型：归档与摘要都是宿主实现（摘要是一次 LLM 调用，属于模型面）。</text>
    <text x="16" y="116">先归档、后摘要；摘要失败时原文已归档，可直接重试。</text>
    <text x="16" y="216">减账用净口径：压实掉的毛额与真正减掉的净额不是一个数。</text>
    <text x="16" y="316" fill="#f85149">offload 首次只标记不替换——首次就替换，模型这一轮从未真正看过那个对象。</text>
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
    <marker id="lg7a" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
  </defs>
  <g font-size="11.5" fill="#e6edf3">
    <rect x="16" y="20" width="150" height="40" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="91" y="45" text-anchor="middle">崩溃前快照</text>
    <rect x="202" y="20" width="170" height="40" rx="8" fill="#161b26" stroke="#58a6ff"/>
    <text x="287" y="45" text-anchor="middle">BuildPlan（纯函数）</text>
    <rect x="16" y="92" width="356" height="40" rx="8" fill="#161b26" stroke="#262e3d"/>
    <text x="194" y="117" text-anchor="middle">跳过已终态 · 重跑缺口 · 并行组按各自前驱采信</text>
    <rect x="420" y="20" width="150" height="40" rx="8" fill="#161b26" stroke="#58a6ff"/>
    <text x="495" y="45" text-anchor="middle">Acquire 令牌</text>
    <rect x="586" y="20" width="158" height="40" rx="8" fill="#1c2330" stroke="#262e3d"/>
    <text x="665" y="45" text-anchor="middle">提交终态</text>
    <rect x="420" y="92" width="324" height="40" rx="8" fill="#161b26" stroke="#262e3d"/>
    <text x="582" y="117" text-anchor="middle">凭唯一令牌放闸：同一条 (run, 环节) 的终态只提交一次</text>
  </g>
  <line x1="166" y1="40" x2="198" y2="40" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg7a)"/>
  <line x1="287" y1="60" x2="287" y2="88" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg7a)"/>
  <line x1="570" y1="40" x2="582" y2="40" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#lg7a)"/>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="16" y="162">可信边界按依赖判定：默认只信连续完成的前缀，失序时宁可多跑几步。</text>
    <text x="16" y="182">终态不自动重开——拒绝替宿主决定要不要重开一个已完成的环节。</text>
    <text x="16" y="202">序号单调闸：重启后要回灌已持久化的事实，否则闸重启即空（陈旧快照写畅通无阻）。</text>
    <text x="16" y="236" fill="#f85149">放闸靠凭证不靠内容比对：同名、同序的两条并发分支，只有一条能拿到令牌。</text>
    <text x="16" y="258">落库失败必须释放令牌，否则那个环节再也提交不了终态。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">恢复判定是纯函数；终态独占靠令牌——「两条分支都赢了」在机制上不可能发生。</p>

## 7 · 模型侧十条纪律

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

## 8 · 接完线自查

- □ 工具调用、程序内调用、子进程侧调用**走的是同一条判定环**（没有第二条路）。
- □ 解析模型产出用的是 `ParseLLMReport`。
- □ 模型拿到的失败文本是**处置指引**（`Guidance`），不是给人看的 `Message`。
- □ 工具被拒时，宿主仍回灌了一条失败结果给模型。
- □ 额度判定在模型调用**之前**。
- □ 上下文有明确预算，容量未知时按危急处理。
- □ 大对象首次只标记、不替换。
- □ 续跑判定是纯函数；终态提交有令牌。

## 下一步

- [能力详解](./capabilities.md)——九组能力逐组拆开（含每组「什么时候用」）。
- [怎么用](./getting-started.md)——从零接入的六步路线与最小闭环。
- [坑速查](../reference/pitfalls.md)——症状 → 真因，接完线自查一遍。
- 仓内 [docs/USAGE.md](https://github.com/yi-nology/goagent-kernel/blob/main/docs/USAGE.md)
  ——逐包的「职责 → 集成片段 → 关键符号 → 坑」。
- [llms.txt](https://yi-nology.github.io/goagent-kernel/llms.txt)——给 AI 代理直接读的紧凑事实页。

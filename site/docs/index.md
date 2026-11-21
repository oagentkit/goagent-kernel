---
home: true
title: goagent-kernel
heroText: goagent-kernel
tagline: 编排是代码，LLM 只做单环节
actions:
  - text: 怎么用 →
    link: /guide/getting-started.html
    type: primary
  - text: 设计初衷
    link: /guide/philosophy.html
    type: secondary
features:
  - title: 机制面，不认识领域
    details: 只给确定性编排的机制——内容、领域、拓扑归宿主。
  - title: 工具调用只有一条路
    details: 披露、守卫、失败、审批、拓扑接成同一条判定环，程序内调用也不例外。
  - title: 崩了能接着跑
    details: 恢复判定是纯函数；终态独占凭令牌，「两条分支都赢了」不可能。
  - title: 上下文是有限预算
    details: budget 计量、offload 外置、spill 溢出，三件套各管一段。
  - title: 失败是数据表
    details: 可重试性 / 退避 / 重发安全做成数据表；不可归类一律不可重试。
  - title: 边界不是文档，是门禁
    details: 域词、依赖方向、结构判定全由机器判，挂在 make check 上。
footer: MIT License · github.com/yi-nology/goagent-kernel
---

<div class="home-punch"><strong>「这个包需要知道自己在给谁干活吗？」</strong>需要 → 属于宿主。</div>

<div class="home-install"><span class="prompt">▸</span>go get github.com/yi-nology/goagent-kernel@latest</div>

<div class="home-links">
  <a class="chip" href="./guide/introduction.html">介绍</a>
  <a class="chip" href="./guide/capabilities.html">能力详解</a>
  <a class="chip" href="./guide/getting-started.html">接入路线</a>
  <a class="chip" href="./guide/llm-guide.html">模型接入</a>
  <a class="chip" href="./reference/pitfalls.html">坑速查</a>
  <a class="chip" href="./reference/packages.html">机器事实</a>
  <a class="chip chip-ai" href="/goagent-kernel/llms.txt">AI 直读 llms.txt</a>
  <a class="chip" href="https://github.com/yi-nology/goagent-kernel">GitHub &#8599;</a>
</div>

<div class="diagram">
<svg viewBox="0 0 760 250" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="hm1" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
    <marker id="hm2" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#a371f7"/></marker>
  </defs>
  <g font-size="12.5" fill="#e6edf3">
    <rect x="20" y="16" width="230" height="96" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="135" y="42" text-anchor="middle" font-size="13.5">大模型</text>
    <text x="135" y="66" text-anchor="middle" font-size="10.5" fill="#9aa7b8">只做单环节的判断</text>
    <text x="135" y="84" text-anchor="middle" font-size="10.5" fill="#9aa7b8">吐结构化产出 · 选工具</text>
    <text x="135" y="100" text-anchor="middle" font-size="10.5" fill="#9aa7b8">不确定时交回上下文</text>
    <rect x="265" y="16" width="230" height="96" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="380" y="42" text-anchor="middle" font-size="13.5">内核（机制面）</text>
    <text x="380" y="66" text-anchor="middle" font-size="10.5" fill="#9aa7b8">判定：该不该 · 合不合法 · 可不可重试</text>
    <text x="380" y="84" text-anchor="middle" font-size="10.5" fill="#9aa7b8">协议：产出怎么解析与校验</text>
    <text x="380" y="100" text-anchor="middle" font-size="10.5" fill="#9aa7b8">预算：上下文怎么计量与压实</text>
    <rect x="510" y="16" width="230" height="96" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="625" y="42" text-anchor="middle" font-size="13.5">宿主</text>
    <text x="625" y="66" text-anchor="middle" font-size="10.5" fill="#9aa7b8">处置：阻断 / 重试 / 落库 / 审批</text>
    <text x="625" y="84" text-anchor="middle" font-size="10.5" fill="#9aa7b8">资产：拓扑 · 词表 · 文案</text>
    <text x="625" y="100" text-anchor="middle" font-size="10.5" fill="#9aa7b8">存储 · 通道 · 权限</text>
  </g>
  <line x1="250" y1="64" x2="261" y2="64" stroke="#58a6ff" stroke-width="1.6" marker-end="url(#hm1)"/>
  <line x1="495" y1="64" x2="506" y2="64" stroke="#a371f7" stroke-width="1.6" marker-end="url(#hm2)"/>
  <text x="236" y="130" text-anchor="middle" font-size="9.5" fill="#9aa7b8">产出</text>
  <text x="520" y="130" text-anchor="middle" font-size="9.5" fill="#9aa7b8">判定结论</text>
  <line x1="20" y1="150" x2="740" y2="150" stroke="#262e3d"/>
  <text x="20" y="176" font-size="12.5" fill="#e6edf3">判定与处置分离，是这个仓唯一不能被绕开的设计：</text>
  <g font-size="11" fill="#9aa7b8">
    <text x="20" y="202">· 内核回报「该阻断 / 该重试 / 不可归类」，动作由宿主执行 —— 内核不抛错中断控制流。</text>
    <text x="20" y="224">· 模型拿到的是「结构化产出 + 判定结论」，不是宿主的内部实现细节。</text>
  </g>
</svg>
</div>
<p class="diagram-cap">三方分工：大模型只在一个环节里发挥，内核管判定，宿主管处置与资产——三层各管一段，互不越界。</p>

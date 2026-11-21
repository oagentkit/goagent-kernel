---
title: 门禁体系
---

# 门禁体系：边界由机器判

「内核不许长出领域知识」「文档数字不许漂」这类纪律，写在散文里就会烂掉。
本仓的做法是：**凡是能机械判定的，一律做成门禁，全部挂在 `make check` 上，
CI 只执行这一条命令**——门禁若只挂在工作流里，本地要到推送之后才知道违规，
那是「推送后门禁」而不是「提交前门禁」。

```bash
make check                # 全量门禁（提交前必须绿）
make architecture-check   # 单跑架构门禁；--report 看全量
make docsync              # README ↔ 仓库事实一致
```

## make check 全链

<div class="diagram">
<svg viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <defs>
    <marker id="gt1" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8 z" fill="#58a6ff"/></marker>
  </defs>
  <g font-size="11" fill="#e6edf3">
    <rect x="16" y="28" width="132" height="56" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="82" y="50" text-anchor="middle">fmt · vet · lint</text>
    <text x="82" y="68" text-anchor="middle" font-size="9.5" fill="#9aa7b8">格式与静态检查</text>
    <rect x="164" y="28" width="132" height="56" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="230" y="50" text-anchor="middle">test · cover-gate</text>
    <text x="230" y="68" text-anchor="middle" font-size="9.5" fill="#9aa7b8">全量单测 + 覆盖率两条判据</text>
    <rect x="312" y="28" width="132" height="56" rx="10" fill="#1c2330" stroke="#262e3d"/>
    <text x="378" y="50" text-anchor="middle">vuln · race · crossbuild</text>
    <text x="378" y="68" text-anchor="middle" font-size="9.5" fill="#9aa7b8">漏洞 / 竞态 / 交叉编译</text>
    <rect x="460" y="28" width="132" height="56" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="526" y="50" text-anchor="middle">architecture-check</text>
    <text x="526" y="68" text-anchor="middle" font-size="9.5" fill="#9aa7b8">域词 · 依赖方向 · 门包</text>
    <rect x="608" y="28" width="136" height="56" rx="10" fill="#161b26" stroke="#58a6ff"/>
    <text x="676" y="50" text-anchor="middle">docsync · ledgercheck</text>
    <text x="676" y="68" text-anchor="middle" font-size="9.5" fill="#9aa7b8">文档与台账的一致性</text>
  </g>
  <line x1="148" y1="56" x2="160" y2="56" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#gt1)"/>
  <line x1="296" y1="56" x2="308" y2="56" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#gt1)"/>
  <line x1="444" y1="56" x2="456" y2="56" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#gt1)"/>
  <line x1="592" y1="56" x2="604" y2="56" stroke="#58a6ff" stroke-width="1.5" marker-end="url(#gt1)"/>
  <text x="16" y="112" font-size="12.5" fill="#e6edf3">红了先查哪一条：症状 → 门禁</text>
  <g font-size="10.5" fill="#9aa7b8">
    <text x="16" y="136">域词污染 / 依赖方向越界 / 门包长胖</text>
    <text x="360" y="136" fill="#e6edf3">→ architecture-check</text>
    <text x="16" y="160">README 包表或工程底座行数漂了</text>
    <text x="360" y="160" fill="#e6edf3">→ docsync</text>
    <text x="16" y="184">台账里同一个单号两个状态</text>
    <text x="360" y="184" fill="#e6edf3">→ ledgercheck</text>
    <text x="16" y="208">某个包覆盖率塌方 / 平台编不过</text>
    <text x="360" y="208" fill="#e6edf3">→ cover-gate / crossbuild</text>
    <text x="16" y="232">疑似并发问题</text>
    <text x="360" y="232" fill="#e6edf3">→ race（失败形态由 cmd/raceprobe 机器分类，不许人猜）</text>
  </g>
  <line x1="16" y1="250" x2="744" y2="250" stroke="#262e3d"/>
  <text x="16" y="274" font-size="10.5" fill="#9aa7b8">门禁本体全在 make check 里，CI 只执行这一条——只挂在工作流上的门禁，是「推送后门禁」。</text>
  <text x="16" y="294" font-size="10.5" fill="#f85149">提交前必须绿：红了先判断是「代码错了」还是「规则该改」，后者先改 policy 再写代码。</text>
</svg>
</div>
<p class="diagram-cap">全链挂在一条命令上；红了先按症状定位到具体门禁，而不是从头重跑一遍猜。</p>

| 环节 | 判什么 |
|---|---|
| fmt · vet · lint | 格式、静态检查（lint 版本 CI 与 Makefile 双向对账，不一致即红） |
| test · cover-gate | 全量单测 + 覆盖率**两条判据**：全局门槛 + 逐包下限 |
| vuln | govulncheck：依赖与标准库漏洞 |
| race | 竞态检测（Linux 上强制 cgo，故单独一套环境） |
| crossbuild | 声明支持的平台必须真的编得过（四平台矩阵） |
| architecture-check | 域词污染红线 + 依赖方向 + 门包不变 |
| docsync | README 包表 / 工程底座基线 ↔ 仓库实际 |
| modreplace · modwork | go.mod 不得含 replace；go.work 不得入库 |

## 两类判定：正则规则与结构判定

**`rules` 段的正则规则**（清单见[包清单与机器事实](./packages.html)，机器生成）管写法类红线：域词污染、测试夹具领域词面、
宿主依赖、宿主运行时、裸 goroutine、printf 回调、双 errors 包、沙箱执行单入口。
完整清单见[包清单与机器事实](./packages.md)（机器生成）。

**`go/ast` 结构判定**（清单见[ADR-0001 · 领域知识红线](../adr/ADR-0001-领域知识红线.html)）管形状类红线：

| 判定 | 拦什么 |
|---|---|
| 门包不变（facade） | 门面包只允许类型别名 / 常量别名 / 单条上游转发函数——多一条语句就红（登记集合为空时本条不判任何包；**再引入门面须同步登记**） |
| 包间依赖方向 | `modules[].requires` 登记的才许 import；未登记包目录**对门禁隐形**，所以新包必须同步登记 |
| 沙箱档位登记 | 档位声明要有人签字（sandbox_providers） |
| 执行路径登记 | 不得绕过沙箱登记簿 |
| 子进程出口登记 | 子进程执行面显式登记（egress） |

为什么结构判定必须走语法树：import 声明有六种以上书写形态（单行/块/别名/空白导入/
点导入/括号同行），逐行正则必然漏，且**漏得毫无声响**。

## 门禁的失败形态必须是变红

这是全套纪律里最核心的一条：**规则的失败形态若是变绿，规则就等于不存在。**

- 改任何判定逻辑，必须同时给**「坏实现会红」**的用例——只测「好实现能过」的用例，
  一个把数字比较写成字符串比较的错实现照样全过；
- **反向验证**不是仪式：每条规则都做过「注入违规 → 报错 + 退出码 1」的实测；
- **门禁自己也要能被检查**：测试文件被整体跳过、import 规则漏形态、例外到期不失效、
  规则可无执行体——这些「静默失效」都实测发生过，修法都配了反向用例；
- **例外登记必须写 `until` 与拆除条件，到期即失效**——失效的豁免会被门禁点名
  要求当天拆除，而不是永远留着变成装饰（当前例外数：0）。

## 数字纪律：docsync 的两种口径

| 数字 | 判定 | 为什么 |
|---|---|---|
| 工程底座：行数 / 文件数 / 测试文件数 | **判相等，不留容差** | 确定性量；历史漂移量恰好落在容差里，给容差等于让静默过期成立 |
| README 包表：行数列 | 25% 相对容差 | 逐行涨落的噪声滤波 |
| 覆盖率 | **不判实测值**，只判门槛 | 实测值随用例批次漂（同批用例测出过 85.5 / 85.0 / 87.0 三个值），让散文复述实时数字只会制造两处不一致 |
| 本站数字 | **全部生成** | `site/gen.mjs` 从 README / go.mod / policy / Makefile / USAGE 抽取，读不到即构建失败 |

派生计数（包数、规则数、门包数）不抄进多处散文——抄进几处就有几份不一致。
**每个数字要么被机器判，要么在旁边写明「不受机器管，改前请手算」。没有第三条路。**

## 提交纪律

- **提交前必须绿**。门禁红了先判断是「代码错了」还是「规则该改」；
  后者必须**先改 policy 再写代码**，并在提交说明里写清理由；
- **新增包必须登记** `architecture-policy.yaml` 的 `modules` 段
  （漏登记的包对依赖方向门禁完全隐形），新增内部依赖同理——先改 policy 再写代码；
- **覆盖率阈值是真阈值**：全局 + 逐包两条判据，逐包豁免必须写原因且**失效即红**。

规则源：`architecture-policy.yaml`（声明面）。执行体：`archcheck` 包
（宿主可直接导入复用同一份判据——判据各写一套必然漂移）。

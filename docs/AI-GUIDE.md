# AI 改造指南（goagent-kernel）

> 这份文档写给**要动手改这个仓的 AI 编码代理**。
>
> 它是**操作型**的：回答「我现在要做 X，该改哪些文件、什么顺序、会被哪条门禁拦、
> 怎么验收」。**为什么**这么设计由 [AGENTS.md](../AGENTS.md) 回答；
> **是什么**由 [README](../README.md) 回答；**怎么接进自己产品**由
> [USAGE.md](USAGE.md) 回答（那篇是给宿主开发者的，不是给改本仓的人的）。
>
> 读本文前先读一遍 [AGENTS.md](../AGENTS.md) 的「边界纪律」与「工程门禁」两节——
> 那里是不可协商的约束，本文只讲怎么在约束下干活。

---

## 0. 怎么用这份文档

**别从头读。** 按你现在要做的事跳：

| 你的任务 | 直接跳到 |
|---|---|
| 第一次进这个仓，不了解结构 | [§1 心智模型](#1-一分钟建立心智模型) → [§2 上手路径](#2-十分钟上手路径) |
| **准备动手改任何一个文件** | [§2.3 改动前的自检清单](#23-改动前的自检清单)（10 条，走一遍再动） |
| 要加一个新能力（新包） | [§3.1 加一个新能力包](#31-任务-a加一个新能力包) |
| 要改某个已有包的判定逻辑 | [§3.2 改已有包的判据](#32-任务-b改一个已有包的判据) |
| 要新增跨进程/分布式能力 | [§3.2.1 补故障矩阵](#321-任务-b2新增分布式能力必须补故障矩阵issue-193--274) |
| 要加或改一条门禁规则 | [§3.3 改门禁规则](#33-任务-c加或改一条门禁规则) |
| 要引入/升级上游矩阵依赖 | [§3.4 动上游依赖](#34-任务-d动上游依赖地板) |
| 门禁报超长文件，要拆 | [§3.5 拆文件](#35-任务-e拆文件) |
| 要发版 | [§3.6 发版](#36-任务-f发版) |
| 门禁红了，不知道怎么修 | [§4 门禁速查](#4-门禁速查红了就查这里) |
| 上手期反复踩同一个坑 | [§5 高发失败模式](#5-ai-代理高发失败模式) |
| 改完了不确定对不对 | [§6 反向验证](#6-反向验证改门禁必做) |
| 只想要命令 | [§7 命令速查](#7-命令速查) |

---

## 1. 一分钟建立心智模型

### 1.1 这个仓是什么

**多智能体编排的通用运行时内核。** 一句话定位：

> **编排是代码，LLM 只做单环节。**

本仓只提供**确定性编排的机制面**（报告协议、环节状态机、事件总线、派发守卫、
审计策略、溢出存档、PII 脱敏、出站守卫等 **48** 个机制面包；另有契约套件 /
组合机制 / 门禁执行体三类，见 §1.3），**不认识任何领域概念**。

### 1.2 判定口诀（改任何代码前先问这一句）

> **「这个包需要知道自己在给谁干活吗？」需要 → 属于宿主，不进本仓。**

配套三条：

1. **判定语义归内核，判定依据归宿主。** 内核说「这是引导式直答还是诊断报告」，
   宿主决定「哪条算引导」——所以内核给结构化字段 + 注册表，不硬编码名字。
2. **展示文案归宿主。** 标题、兜底、品牌词一律走 `RenderOptions` 注入。
3. **测试夹具用合成数据。** 夹具是契约的示例，示例带领域味等于把契约钉死在某个领域上。

<div align="center">
<svg width="760" viewBox="0 0 760 330" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <rect x="0" y="0" width="760" height="330" rx="10" fill="#1c2330" stroke="#262e3d"/>
  <rect x="150" y="16" width="460" height="48" rx="8" fill="#161b26" stroke="#58a6ff"/>
  <text x="380" y="37" font-size="14" fill="#e6edf3" text-anchor="middle">改任何代码前先问这一句</text>
  <text x="380" y="56" font-size="13" fill="#9aa7b8" text-anchor="middle">这个包需要知道自己在给谁干活吗？</text>
  <line x1="300" y1="64" x2="198" y2="98" stroke="#f85149" stroke-width="1.5"/>
  <line x1="460" y1="64" x2="562" y2="98" stroke="#3fb950" stroke-width="1.5"/>
  <text x="224" y="88" font-size="12" fill="#f85149" text-anchor="middle">需要</text>
  <text x="536" y="88" font-size="12" fill="#3fb950" text-anchor="middle">不需要</text>
  <rect x="30" y="98" width="330" height="72" rx="8" fill="#161b26" stroke="#f85149"/>
  <text x="48" y="122" font-size="14" fill="#f85149">属于宿主 —— 不进本仓</text>
  <text x="48" y="142" font-size="12" fill="#9aa7b8">判定语义归内核，判定依据归宿主</text>
  <text x="48" y="159" font-size="12" fill="#9aa7b8">展示文案归宿主（走 RenderOptions 注入）</text>
  <rect x="400" y="98" width="330" height="72" rx="8" fill="#161b26" stroke="#3fb950"/>
  <text x="418" y="122" font-size="14" fill="#3fb950">是机制 —— 进本仓</text>
  <text x="418" y="142" font-size="12" fill="#9aa7b8">换个宿主还要改吗？要改就不进</text>
  <text x="418" y="159" font-size="12" fill="#9aa7b8">本仓只提供机制面，零领域词</text>
  <rect x="30" y="188" width="330" height="98" rx="8" fill="#161b26" stroke="#262e3d"/>
  <text x="48" y="210" font-size="12.5" fill="#e6edf3">怎么落地</text>
  <text x="48" y="230" font-size="12" fill="#9aa7b8">① 收成接口，由宿主实现</text>
  <text x="48" y="248" font-size="12" fill="#9aa7b8">② 本仓不 import 宿主仓</text>
  <text x="48" y="268" font-size="11.5" fill="#f85149">门禁 harness-no-host-dependency</text>
  <text x="48" y="282" font-size="11.5" fill="#f85149">门禁 harness-no-domain-vocabulary</text>
  <rect x="400" y="188" width="330" height="98" rx="8" fill="#161b26" stroke="#262e3d"/>
  <text x="418" y="210" font-size="12.5" fill="#e6edf3">怎么落地（顺序不能反）</text>
  <text x="418" y="230" font-size="12" fill="#9aa7b8">① 先改 architecture-policy.yaml</text>
  <text x="418" y="248" font-size="12" fill="#9aa7b8">② 再写生产代码 + 用例</text>
  <text x="418" y="266" font-size="12" fill="#9aa7b8">③ 同步 README 包表 + 生命周期登记</text>
  <text x="418" y="282" font-size="11.5" fill="#3fb950">门禁 architecture-check / docsync</text>
  <rect x="30" y="296" width="700" height="24" rx="8" fill="#161b26" stroke="#262e3d"/>
  <text x="380" y="312" font-size="12" fill="#9aa7b8" text-anchor="middle">判错的后果：把宿主资产写进内核，宿主就再也换不掉自己的领域</text>
</svg>
</div>

*图 1：判定口诀的决策路径——「需要」与「不需要」各有一条确定的落地序列。*

### 1.3 53 个包分四组（另有 `taskboard/boardtest` 一个子包）

> 口径：与 README 包表（**53** 行，`make docsync` 判）逐行对齐；policy `modules` 段登记
> **61 个 module id**（`make architecture-check` 打印的「61 个包的依赖方向」就是它）。
> **61 与 53 差的 8 个不是「漏登记的包」**，而是**根目录不在一级目录**的 module：
> `reference-runtime` / `pgrefstore` / `sqliteadapter` / `containerbackend` /
> `worker-runtime-ref`（根在 `examples/*`）、`ptcchild`（根在 `cmd/ptcchild`）、
> `subhub` / `ledgerstatus`（根在 `internal/*`）——README 包表只收一级目录，故它们
> 不进表，但**照样在依赖方向门禁下**。
> ⚠️ **两个数不是同一种量**：61 数的是 **module id**（`archcheck` 打印
> `len(pol.Modules)`），这些 id 覆盖的 **root 目录共 63 个**——`taskboard` 与
> `pgrefstore` 各有两个 root；`taskboard` 的第二个 root 是 `taskboard/boardtest`，
> 它计在 `taskboard` 行内，**不是独立的 module id**。
> 本表此前写「51 个包 / 门面 4 个」，与实际差在三处：`policy` / `reminder` /
> `toolfail` / `sandbox` 四个门包在 v0.35.0 已整包删除（**本仓不再提供这四个包**，
> 宿主直连上游矩阵 `oagentkit/toolgate/policy` 与 `oagentkit/guardx/*`），
> 而 `scheduling` / `eval` / `coord` / `handoff` / `ownership` / `worker` /
> `backend` / `metrics` / `cost` / `tenant` / `controlplane` / `roundguard`
> 十二个包此前漏登记。
> **包名顺序与 README 包表一致**——两处表若各自排序，读者要自己对齐。

| 组 | 包 | 特征 |
|---|---|---|
| **机制面**（48 个） | `protocol` `scheduling` `eventbus` `dispatch` `spill` `pii` `netguard` `statemachine` `contract` `checkpoint` `commitgate` `budget` `offload` `tooldisclose` `toolname` `sideeffect` `cmdpolicy` `egress` `delta` `pending` `sessionref` `drain` `deadline` `reconcile` `finalize` `steer` `ptc` `backend` `watchdog` `guardset` `trajectory` `eval` `taskboard` `coord` `skilldisclose` `hookengine` `memorypipe` `delegate` `handoff` `ownership` `worker` `worktree` `ticket` `metrics` `cost` `tenant` `controlplane` `roundguard` | 真正的逻辑所在。改这些包 = 改本仓的对外能力。 |
| **契约套件**（1 个 + 1 个子包） | `sidetest`（+ `taskboard/boardtest`） | 判「宿主的实现是否满足契约」的**判据**，不是能力本身。只给「怎么判」，不给「一份能用的实现」——见 AGENTS.md「共享契约测试套件」那条纪律。`boardtest` 是同一形态：`taskboard` 的原子性由存储保证，本仓只能给判据（`ConcurrentClaim` 跑一遍，成功者必须恰好 1 个）。 |
| **组合机制**（3 个） | `retryplan` `gatecheck` `preset` | 把多个机制的既有判定**组合**成一条结论（与 `guardset` 同族：**组合逻辑归内核，判定归各自机制**）。`retryplan` 只回答「该走哪条路径」；`gatecheck` 把审批层与沙箱层合成一条判定（两层都同意才放行）；`preset` 把档位 × 审批 × 出口收成一份具名组合。三者都**不重新判定**任何东西。**依赖方向是 preset → gatecheck**（高层组装件认识低层判据，反过来不行）。 |
| **门禁执行体**（1 个 + 2 个 CLI） | `archcheck` / `cmd/archcheck` `cmd/docsync` | policy 的可执行面。可导入包，宿主可复用同一套判据。 |

> **门包不可变是硬约束，不是建议**（当前 policy `facades: []`，清单为空；规则与
> 执行体仍在）。`docs/OAGENTKIT-OVERLAP.md` 里那句「保持门面，别再在本仓加逻辑」
> 原本只是散文，散文不是门禁——所以才有了 `harness-facade-stays-facade` 这条
> AST 规则：门包只允许四种声明（类型别名 / 常量别名 / 单条上游转发函数 /
> 允许集合内的 import），加真逻辑三者必居其一 ⇒ 当天即红。
> ⚠️ **本仓现在一个门包都没有**：`facades: []` 是空清单 ⇒ 这条规则**今天没有对象
> 可判**（恒绿），规则与执行体留着是为了将来有人在本仓造门面时当场拦住。
> 五个旧门包（`dispatch` / `policy` / `reminder` / `toolfail` / `sandbox`）
> 在 v0.35.0 整包删除、消费方直连上游 `oagentkit/*`。
> ⚠️ **`dispatch` 这个名字现在被另一个机制包占用**（派发域的**失败面**：改派错误
> 三档分类与兜底改派环，见 `dispatch/dispatch.go` 包头「四问」里的第四问）——
> 它**不是**门包，别把它当门包看待。

### 1.4 三条贯穿全仓的纪律（改代码时会撞上）

1. **门禁的失败形态必须是变红，不能是变绿。** 任何「改判定逻辑」的动作，
   必须同时给出「坏实现会红」的用例——见 [§6](#6-反向验证改门禁必做)。
2. **结构性判定走 `go/ast`，不走正则逐行看文本。** import 声明有六种以上书写形态
   （单行/块/别名/空白/点导入/括号同行），逐行正则必然漏且漏得毫无声响。
3. **派生状态只能有一个真相。** 「代次/版本/序号」这类计数不许开第二个计数器；
   需要全局视图时让它是真相的投影。

### 1.5 五个反直觉点（凭直觉写会写反的那几条）

<div align="center">
<svg width="760" viewBox="0 0 760 372" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <rect x="0" y="0" width="760" height="372" rx="10" fill="#1c2330" stroke="#262e3d"/>
  <text x="40" y="30" font-size="13" fill="#9aa7b8">凭直觉会写反的说法</text>
  <text x="412" y="30" font-size="13" fill="#58a6ff">现行事实（已逐条核实）</text>
  <line x1="392" y1="14" x2="392" y2="356" stroke="#262e3d" stroke-width="1"/>
  <circle cx="398" cy="62" r="4" fill="#a371f7"/>
  <text x="40" y="66" font-size="12.5" fill="#e6edf3">「门包的门永远朝外」</text>
  <text x="412" y="58" font-size="12" fill="#e6edf3">本仓现在一个门包都没有</text>
  <text x="412" y="76" font-size="12" fill="#9aa7b8">policy facades: [] 是空清单；规则留着等人踩</text>
  <line x1="24" y1="92" x2="736" y2="92" stroke="#262e3d" stroke-width="1"/>
  <circle cx="398" cy="124" r="4" fill="#f85149"/>
  <text x="40" y="128" font-size="12.5" fill="#e6edf3">「被拒就激活，重试不绕路」</text>
  <text x="412" y="120" font-size="12" fill="#e6edf3">被拒不激活：schema 未取回，原样重试不会成功</text>
  <text x="412" y="138" font-size="12" fill="#9aa7b8">Gate.Check 从不调 Catalog.Load</text>
  <line x1="24" y1="154" x2="736" y2="154" stroke="#262e3d" stroke-width="1"/>
  <circle cx="398" cy="186" r="4" fill="#58a6ff"/>
  <text x="40" y="190" font-size="12.5" fill="#e6edf3">「额度判定调用后查也一样」</text>
  <text x="412" y="182" font-size="12" fill="#e6edf3">一律在调用前：调用后查 = 最后一次昂贵调用已发生</text>
  <text x="412" y="200" font-size="12" fill="#9aa7b8">roundguard.CheckRounds / CheckTool</text>
  <line x1="24" y1="216" x2="736" y2="216" stroke="#262e3d" stroke-width="1"/>
  <circle cx="398" cy="248" r="4" fill="#58a6ff"/>
  <text x="40" y="252" font-size="12.5" fill="#e6edf3">「文档里的数字差一点不红」</text>
  <text x="412" y="244" font-size="12" fill="#e6edf3">生产行数 / 文件数 / 测试文件数判相等，不留容差</text>
  <text x="412" y="262" font-size="12" fill="#9aa7b8">覆盖率那一格仍然没人判</text>
  <line x1="24" y1="278" x2="736" y2="278" stroke="#262e3d" stroke-width="1"/>
  <circle cx="398" cy="310" r="4" fill="#a371f7"/>
  <text x="40" y="314" font-size="12.5" fill="#e6edf3">「零值就是未设」</text>
  <text x="412" y="306" font-size="12" fill="#e6edf3">Reason 零值 = 空串、不在词表内</text>
  <text x="412" y="324" font-size="12" fill="#9aa7b8">Limit / Shape 显式取合法缺省 LimitNone / ShapeNone</text>
  <line x1="24" y1="340" x2="736" y2="340" stroke="#262e3d" stroke-width="1"/>
  <text x="380" y="360" font-size="12" fill="#9aa7b8" text-anchor="middle">这五条都是「门禁红过一次」换来的：直觉对，代码就反了</text>
</svg>
</div>

*图 2：五条与直觉相反的现行事实——每条后面的证据都能在仓里逐字查到。*

### 1.6 `roundguard` 的几条反直觉语义（接它之前必须知道）

`roundguard` 是本仓**最晚下沉**的机制包（`api-lifecycle.yaml` 定级 **Experimental，
自 v0.60.0**），**本仓没有生产调用方**，集成参考只有 `docs/USAGE.md` 里的伪码——
若你被要求「接上 roundguard」，先知道它**还没被第二个宿主验证过**。

| 事实 | 出处 |
|---|---|
| 四个词表（`Reason` 10 值 / `Limit` 5 值 / `StopPolicy` 2 值 / `Shape` 6 值）**取值决定结论**，别处出现必须引用本包常量，**不得手抄** | `roundguard/roundguard.go:48-51`、`architecture-policy.yaml` 的 `word_tables` 段 |
| `Reason` 零值是**空串、不在词表内**（`Valid` 判假）；`Decision` 里的 `Limit` / `Shape` 被**显式初始化**为 `LimitNone` / `ShapeNone`（合法取值，不是「未设」） | `roundguard/roundguard.go:54-58`、`roundguard/decide.go:85` |
| `StopPolicy.Valid()` **不要**用来校验 `Budget.Policy`——`Budget.Policy` 的零值是**合法缺省**（= 强制收尾），校验预算声明用 `Budget.Validate`，取生效口径用 `Budget.EffectivePolicy` | `roundguard/budget.go:26-35` |
| `Budget` **必须先经 `Validate()`**：未校验的负值上限是 **fail-open**（上限 ≤ 0 一律判成「不限」）；`Validate` 自身对非法值报错，**不静默降级为不限** | `roundguard/budget.go:74-80, 149-151` |
| `CheckTool` 对**未知**工具 ID 返回 `Allowed=true`（未登记的工具不消耗单工具额度） | `roundguard/budget.go:180-183` |
| 额度判定**一律在模型调用前**：调用后判意味着**最后一次昂贵调用已经发生** | `roundguard/budget.go:145-151` |
| `Decision.Used` 与 `Snapshot.Used` **共享同一份 `Tools` 映射**（不是副本）——判定不受影响，但**别拿它当审计凭据** | `roundguard/decide.go:50-54` |
| `Meter` 是**进程内**的：跨进程续跑时回合数**不随之累计**（跨进程累计要宿主自己落库） | `roundguard/meter.go:5-10` |

---

## 2. 十分钟上手路径

### 2.1 按这个顺序读

| 顺序 | 读什么 | 读多久 | 读完你要能回答 |
|---|---|---|---|
| 1 | [README](../README.md) 的「边界」与「包清单」两节 | 3 min | 哪个包管什么事 |
| 2 | [AGENTS.md](../AGENTS.md) 的「边界纪律」+「工程门禁」 | 5 min | 什么不能做、什么会红 |
| 3 | `architecture-policy.yaml` 的 `modules` 段（61 个 id + requires） | 2 min | 哪个包能依赖哪个 |
| 4 | 你要动的那一个包的包头注释 | 3 min | 这个包为什么长这样 |
| 5 | 那个包的 `_test.go` | — | 契约的实际形状（比注释准） |

> **包头注释是这个仓的特产**：每个包开头都有一段「为什么是这样而不是那样」，
> 包含真实事故的记录。改一个包之前先把它的包头读完——那里写着的往往正是
> 你正想重复的那个错。

### 2.2 先跑一遍，确认环境是绿的

**上手第一步就跑一次**，这样后面每次红都知道是自己造成的，不是本来就红的。

```bash
cd /Users/zhangyi/my_project/goagent-kernel

make test                 # ① 全量单测（当前 539 个测试文件）
make architecture-check   # ② 架构门禁（规则 / 门包 / 包数见输出）
make docsync              # ③ README 包表与「工程底座」三项确定性量
make ledgercheck          # ④ 演进台账纪律
```

**绿长什么样**（实测输出，数字随代码变，以当场输出为准）：

```
# ① make test —— 每行一个包，结尾无 FAIL，退出码 0
ok  	github.com/yi-nology/goagent-kernel/protocol	0.482s
ok  	github.com/yi-nology/goagent-kernel/ptc	2.913s
ok  	github.com/yi-nology/goagent-kernel/worker	3.859s
ok  	github.com/yi-nology/goagent-kernel/worker/faultmatrix	0.436s

# ② make architecture-check
架构门禁通过（13 条规则 + 0 个门面包 + 61 个包的依赖方向，policy version 1；无阻断级违反）

# ③ make docsync
README 与仓库一致（53 个包；工程底座 78673 行 / 411 文件 / 539 个测试文件；ADR 9 条 / docs 20 篇已登记）；USAGE 口径已判✓

# ④ make ledgercheck
台账校验通过：140 个单号 / 568 处出现点 ✓
```

**四条都绿再动手。** 任一条红 ⇒ 先查它是不是本来就红的（见 §4.2），别把自己的改动
混进一个先天红的仓。

> macOS：一切 `go build` / `go test` 必须带 `CGO_ENABLED=0`（新 SDK 27.0 与
> CLT linker 不兼容 + 许可未接受）。Makefile 已内置，**直接用 make 目标**，
> 别手敲 `go test`。
> ⚠️ 例外：`make race` **不能**跟着这个开关——Linux 上 `-race` 强制要求 cgo
> （`CGO_ENABLED=0 go test -race -c` 直接报 `-race requires cgo`）。Makefile 的
> `race` 目标已单独用 `CGO_ENABLED=1`，别手改它。

### 2.3 改动前的自检清单

**10 条，走一遍再动文件。** 每条都能落到本仓的真实门禁上，不是新发明的规矩。

1. **这个包需要知道自己在给谁干活吗？** 需要 → 属于宿主，别加进本仓（§1.2）。
2. **要改的包在 policy 的 `modules` 段登记了吗？** 新包必须先登记 `{id, roots,
   requires, owner}`；`requires` 填**真实**依赖，不是「打算用」的。
3. **要改的包在 `thresholds.scan_roots` 里吗？** 漏一个 ⇒ 该包对「超长文件」
   「god object」两条阈值**完全隐形**（policy 自校验强制两者一致）。
4. **新包的生产包名出现在 README 包表里吗？** `docsync` 判包名 / 行数 / 依赖列 /
   职责列；「工程底座」表另判生产行数 / 文件数 / 测试文件数三项，**判相等不留容差**。
5. **新包在 `api-lifecycle.yaml` 登记了吗？** 还要在包注释里写
   `// 生命周期: <级别>（vX.Y.Z 定级）`——两处不一致即红。
6. **判据是结构性的吗？** import 声明、函数体形态、字段出现这类判定走 `go/ast`，
   不要塞进正则表（正则表判不了「函数体是不是只有一条转发」）。
7. **新增/修改的判据有「坏实现会红」的用例吗？** 只测「好实现能过」的用例，
   一个把数字比较写成字符串比较的错实现照样全过（§6）。
8. **失败形态是变红还是变绿？** 变绿 = 没生效——**最贵的形态**。
   改判据后必须做一次「注入违规 → 必红 → 撤销 → 绿」。
9. **计数 / 代次 / 序号只有一个真相吗？** 需要全局视图时让它是真相的**投影**，
   不开第二个计数器。
10. **改完之后 `make check` 绿了吗？** `check` 的**全链目标**按顺序执行、**红即停**
    （见 §4.0）；新增门禁还要接进 CI 跑的那个分层目标，否则 CI 一次都不会跑。

### 2.4 认地图：包 → 典型改动

| 你要改的东西 | 去哪个包 | 注意 |
|---|---|---|
| 报告结构、解析容错、Markdown 渲染 | `protocol` | `report_types.go` 367 行（单文件上限 400，以 `make architecture-check` 为准）；枚举位归一在 `report_normalize.go` |
| 环节状态流转 | `statemachine` | 纯函数件，305 行，依赖 oekit/pkg |
| 工具失败分类（可重试/退避/需人工/重发安全） | 上游 `guardx/toolfail`（**本仓不再提供该包**）| 分类是**数据表**，不要散落 if |
| 渐进披露、披露门禁 | `tooldisclose` | **被拒不激活**：schema 未取回时原样重试不会成功（`ErrNotLoaded` 的模型指引原文）。恢复路径是「先取回 schema，再构造参数调用」 |
| 多守卫合成拒绝 | `guardset` | 取「全部可重试才算可重试」的保守侧 |
| 工具名 → JS 标识符 | `toolname` | **单一真相**，`tooldisclose` 与 `ptc` 共用 |
| 沙箱内程序化编排 | `ptc` | 程序内 `host.<tool>()` 也要过判定环 |
| 执行沙箱的档位语义与后端协议 | 上游 `guardx/sandbox`（**本仓不再提供该包**）| 档位是后端**自报**的，内核不校验其真实性；要求档位**无满足者即拒绝**，不降级 |
| 续跑快照、序号闸 | `checkpoint` | 「续跑而非重放」三条硬规则 + 依赖声明（并行组乱序采信）|
| 终态独占提交闸 | `commitgate` | 放闸靠**令牌**，不靠内容比对认人 |
| 输入侧上下文预算 | `budget` | 分段计量（区分工具面膨胀 vs 历史膨胀） |
| 输出溢出取回 | `spill` | 与 `offload` 是姊妹机制，别搞混职责 |
| 大对象外置 | `offload` | **两阶段**：首次只标记不替换 |
| 出站 SSRF | `netguard` | 拨号层闭窗 + 放行面具名 |
| 个人信息脱敏 | `pii` | 只回报信号，**阻断动作归宿主** |
| 回合守卫（结局分类 / 额度 / 无进展 / 组合结论） | `roundguard` | Experimental、无生产调用方；额度判定**一律在调用前**；`Budget` 必须先 `Validate()`（§1.6）|
| 依赖方向 / 门禁规则 | `archcheck` + `architecture-policy.yaml` | 先改 policy 再写代码 |

---

## 3. 改造任务手册

> 通用顺序：**先改声明（policy / 表），再改实现，最后跑门禁。**
> 反过来做会得到「代码写完门禁红，再回头补声明」的双份工作。

<div align="center">
<svg width="760" viewBox="0 0 760 400" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <rect x="0" y="0" width="760" height="400" rx="10" fill="#1c2330" stroke="#262e3d"/>
  <text x="34" y="28" font-size="13" fill="#9aa7b8">任务类型</text>
  <text x="190" y="28" font-size="13" fill="#9aa7b8">改哪些文件（按这个顺序）</text>
  <text x="530" y="28" font-size="13" fill="#9aa7b8">会被哪条门禁拦</text>
  <line x1="24" y1="38" x2="736" y2="38" stroke="#262e3d" stroke-width="1"/>
  <text x="34" y="62" font-size="13" fill="#e6edf3">加能力包</text>
  <text x="190" y="62" font-size="11.5" fill="#9aa7b8">policy modules → scan_roots → 代码 + 用例</text>
  <text x="190" y="79" font-size="11.5" fill="#9aa7b8">→ README 包表 → api-lifecycle.yaml</text>
  <text x="530" y="62" font-size="11.5" fill="#58a6ff">architecture-check · docsync</text>
  <text x="530" y="79" font-size="11.5" fill="#58a6ff">apilifecheck · cover-gate</text>
  <line x1="24" y1="94" x2="736" y2="94" stroke="#262e3d" stroke-width="1"/>
  <text x="34" y="118" font-size="13" fill="#e6edf3">改判据</text>
  <text x="190" y="118" font-size="11.5" fill="#9aa7b8">包源码 → 反向验证用例（坏实现会红）</text>
  <text x="190" y="135" font-size="11.5" fill="#9aa7b8">→ 跨包契约的端到端用例</text>
  <text x="530" y="118" font-size="11.5" fill="#58a6ff">test · cover-gate</text>
  <text x="530" y="135" font-size="11.5" fill="#58a6ff">architecture-check（结构阈值）</text>
  <line x1="24" y1="150" x2="736" y2="150" stroke="#262e3d" stroke-width="1"/>
  <text x="34" y="174" font-size="13" fill="#e6edf3">加门禁规则</text>
  <text x="190" y="174" font-size="11.5" fill="#9aa7b8">policy rules 段 + examples → 执行体</text>
  <text x="190" y="191" font-size="11.5" fill="#9aa7b8">结构性判据走 go/ast，不进 rules 段</text>
  <text x="530" y="174" font-size="11.5" fill="#a371f7">architecture-check</text>
  <text x="530" y="191" font-size="11.5" fill="#a371f7">缺样例即红（require_rule_examples）</text>
  <line x1="24" y1="206" x2="736" y2="206" stroke="#262e3d" stroke-width="1"/>
  <text x="34" y="230" font-size="13" fill="#e6edf3">动上游依赖</text>
  <text x="190" y="230" font-size="11.5" fill="#9aa7b8">go.mod 版本 → requires 的 ext: 声明</text>
  <text x="190" y="247" font-size="11.5" fill="#9aa7b8">→ README 下游升级须知</text>
  <text x="530" y="230" font-size="11.5" fill="#58a6ff">modreplace · architecture-check</text>
  <text x="530" y="247" font-size="11.5" fill="#58a6ff">releasecheck</text>
  <line x1="24" y1="262" x2="736" y2="262" stroke="#262e3d" stroke-width="1"/>
  <text x="34" y="286" font-size="13" fill="#e6edf3">拆文件</text>
  <text x="190" y="286" font-size="11.5" fill="#9aa7b8">拆实现文件（按职责分类型，不按行数）</text>
  <text x="190" y="303" font-size="11.5" fill="#9aa7b8">→ 同步 README 包表行数</text>
  <text x="530" y="286" font-size="11.5" fill="#58a6ff">architecture-check</text>
  <text x="530" y="303" font-size="11.5" fill="#58a6ff">（400 行 / 25 方法两条阈值）</text>
  <line x1="24" y1="318" x2="736" y2="318" stroke="#262e3d" stroke-width="1"/>
  <text x="34" y="342" font-size="13" fill="#e6edf3">发版</text>
  <text x="190" y="342" font-size="11.5" fill="#9aa7b8">CHANGELOG → 回执提交 → annotated tag</text>
  <text x="190" y="359" font-size="11.5" fill="#9aa7b8">→ 推 tag → GitHub Release</text>
  <text x="530" y="342" font-size="11.5" fill="#3fb950">releasecheck · ledgercheck</text>
  <text x="530" y="359" font-size="11.5" fill="#3fb950">docsync</text>
  <line x1="24" y1="374" x2="736" y2="374" stroke="#262e3d" stroke-width="1"/>
  <text x="380" y="390" font-size="12" fill="#9aa7b8" text-anchor="middle">顺序反了不会更早红，只会让你做两遍</text>
</svg>
</div>

*图 3：六类改造任务各自的文件顺序与对应门禁——先改声明、再改实现、最后跑门禁。*

### 3.1 任务 A：加一个新能力包

**判据**：这个能力是机制吗？换个宿主还要改吗？要改 → 属于宿主，别加。

**改动清单**（按顺序，漏任何一步门禁立刻红）：

1. **`architecture-policy.yaml` → `modules` 段**，先加条目：

   ```yaml
   - id: 新包名
     roots: [新包名]
     requires: []        # 真实依赖，不是理想形态
     owner: harness
     managed: true
     notes: ...
   ```

   - `requires` 填**真实**依赖（不是「打算用」的）。内部包写包名，
     第一方矩阵写 `ext:github.com/oagentkit/xxx` 这种**矩阵模块级**前缀。
   - `ext:` 前缀必须落在 `dependency_direction.ext_prefixes` 内
     （当前是 `github.com/oagentkit` + `github.com/oekit` 整体）。
   - **同时把新包加进 `thresholds.scan_roots`**（见第 2 步）。

2. **`thresholds.scan_roots`**：把新包加进去。
   `scan_roots` 必须**覆盖 `modules` 段的全部 roots**——漏一个，该包对
   「超长文件」「god object」两条阈值**完全隐形**（policy 自校验会强制两者一致）。

3. **写代码**（生产代码 + `_test.go`）。

4. **`README.md` 包表**加一行，格式固定：

   ```markdown
   | `新包名` | 123 | oekit/pkg | 一句话职责 |
   ```

   - 行数口径：`*.go` **不含** `_test.go`。
   - 依赖列：仓外 import，取 module 前两段，**逐项相等**才过。
   - 职责列不能为空。
   - 行数按 **25% 相对容差**判定——容差内的逐行涨落不拦，跨量级误述拦。
   - 仓里有包但 README 没写 → 同样红（反向检查）。

   > ⚠️ **README「工程底座」那张表只有一半在判定面内**（issue #31 之后）——
   > 生产代码**行数 / 文件数**与**测试文件数**三项由 `docsync` **判相等、不留容差**，
   > 漂了当场红，报错里直接给出实测值与重算命令。
   > **覆盖率与其余描述性行仍是散文**（覆盖率刻意不判：它随「跑的是哪个用例集合」变），
   > 那几格改前请手算，别信上一版。

5. **`api-lifecycle.yaml` 登记 + 包注释标记**（issue #113，`make apilifecheck` 判）：

   ```yaml
   - pkg: 新包名
     level: Experimental      # 或 Stable，判定规则见 docs/api-compat.md
     since: v0.33.0
     reason: 为什么定这个级（每条必填）
   ```

   - 并在包注释（package doc）里加一行 `// 生命周期: <级别>（vX.Y.Z 定级）`，
     与清单 level 一致——两处不一致、或新增生产包没登记，都当场红；
   - 分级语义与「什么算 breaking」见 [docs/api-compat.md](api-compat.md)；
   - 新包若声明错误哨兵 / 导出错误类型，**同一提交**里登记进
     [docs/error-semantics.md](error-semantics.md)（同门禁双向判定）。

6. **验收**：

   ```bash
   make architecture-check && make docsync && make apilifecheck && make test
   ```

**新包必带测试**：覆盖率门槛 `COV_MIN=60` 是真阈值，内核包新增必须带测试。

### 3.2 任务 B：改一个已有包的判据

改动清单：

1. **先读包的 `_test.go`**，确认现有契约钉住了什么。
2. 改实现。
3. **改判据 → 必须补反向验证用例**（见 [§6](#6-反向验证改门禁必做)）。
   只测「好实现能过」的用例，一个把数字比较写成字符串比较的错实现照样全过。
4. 跨包契约的改动 → **端到端用例**钉「派生 → 装配 → 执行」整条链，
   不要只测生产侧或只测消费侧（两侧漂移时两边都绿）。
5. 若该包的**外部依赖或生产行数**跨了容差 → 同步 `README.md` 包表。
6. 验收：`make check`（全套）。

**改判据时的三条自检**：

- 新判据是**结构性**的（函数体是不是只有一条转发、import 有几种写法）吗？
  是 → 走 `go/ast`（`archcheck/` 里已有现成模式可抄），不要塞进正则表。
- 新判据的**失败形态是变红还是变绿**？变绿 = 没生效（最贵的形态）。
- 判据涉及**计数/代次/序号**吗？是 → 检查是不是只有**一个真相**。

### 3.2.1 任务 B2：新增分布式能力必须补故障矩阵（issue #193 / #274）

跨进程/跨副本的正确性（认领、续期、租约接管、事件发布、副作用对账）不能只靠
零散用例。矩阵的**唯一声明面**在 `worker/faultmatrix/matrix_test.go` 的
`deploymentMatrix`：每条声明故障类、适用部署模式（Embedded / Standalone /
Cluster）和执行函数；`TestFaultDeploymentMatrix` 先自检「**每个已登记故障类**每类至少一条
用例、模式合法、执行函数非空」，再逐格执行。**漏一类或删一条 = 门禁红**。

> **故障类清单不要手抄**：以 `worker/faultmatrix/matrix_test.go` 的类常量
> （`Class*`）与 `requiredMatrixCells` 为唯一真相源。下表是**给人读的说明**，
> 增删类时以代码为准（#274 已把矩阵从 6 类扩到 14 类）。

| 故障类 | 落在哪一格 | 判据 |
|---|---|---|
| 并发认领唯一性 | 三模式 | 100 个执行者同时 Claim，成功者恰好 1 个（`taskboard.ConcurrentClaim`）|
| 写库成功但发布失败 | Standalone / Cluster | 存储行与游标是权威事实；实时 Relay 丢弃不回滚、不双写，event-tail 可回放补达 |
| 数据库故障及恢复 | Standalone / Cluster | 故障期读/写显式失败；存储错误**不得**伪装成 `ErrLeaseLost`；恢复后同一租约可续期/收场 |
| 多 Worker 时钟偏移 | Standalone / Cluster | 到期只由存储时钟判；节点本地时钟快/慢不改变接管结论，接管代次严格递增 |
| 网络分区与租约接管 | Cluster | 续期不可达 ⇒ 存储时钟越过 TTL 后 `worker.Recover` 可接管；旧持有者只被取消、迟到收场被拒，副作用恰好一次 |
| Pod 强删与副作用对账 | Cluster | 过期执行意图 ⇒ `OutcomeUncertain`；外部事实裁决后只重放结果，不重复施加 |
| K8s API 响应丢失 | Cluster（+真集群） | 作业已建但创建响应丢失；重试按**稳定执行标识**对账返回既有结果，不重跑一份 |
| 跨租户任务碰撞 | Standalone / Cluster | 同一 TaskID 在两租户下互不可见/不覆盖/令牌互不认；缺租户上下文 fail-closed |
| 长任务租约失效 | Cluster | 执行超 TTL、续期中断后他副本按过期认领接管；旧持有者的 Renew/Complete 被拒，代次严格递增 |
| 数据库主备切换 | Standalone（+真 PG） | 主库提交在备库可见；主库故障期写面显式失败、不半写；promote 后旧主被 fence（不产生双主） |
| 事件乱序提交 | Standalone / Cluster | 先拿号后提交，尾随器取**已提交水位线**（非裸 max(id)），不得越过 2 跳过 1；1 提交后按序补达 |
| 配额悬挂 | Cluster | 预占不能凭「心跳消失」就归还：须先确认预占过期**且**任务租约已失效；迟到的 Release 不得二次归还 |
| 控制面并发介入 | Cluster | 强制释放与 Worker 完成并发时恰好一方生效，输家得可观测 `ErrLeaseLost`，任务只有一次有效状态转换 |
| 真实 NetworkPolicy 验证 | Cluster（+真集群） | 声明面：策略开启 + CNI 能力事实 + 出站探测三者齐备才自报 `EgressDenied`，缺一降级 |

**怎么跑**：

```bash
make fault-injection                                              # 全仓 TestFault*（门禁层）
go test ./worker/faultmatrix -run TestFaultDeploymentMatrix -v    # 只跑矩阵
```

**加格子的纪律**：

1. 新增任何跨进程能力（租约/认领/发布/对账/接管），同一提交里在
   `deploymentMatrix` 补一条用例；没有对应故障类的，先加故障类常量。
2. 判据必须能**变红**：写出「坏实现会怎样违约」，断言违约形态，而不是
   「不报错就行」。
3. 进程内可复现的格子直接进矩阵（内存后端 + 可注入时钟/故障模拟），
   验证判定与时序；跨进程原子性仍由 `boardtest` / `sidetest` 契约套件与
   `examples/pgstore` 的 `make integration` 覆盖——两层各判一段，不互相冒充。
4. 只能在真集群验证的格子，给 `matrixCase.requiresEnv` 填所需环境并用
   `t.Skipf` 显式跳过（原因必须打出来）。**不许静默跳过**。

### 3.3 任务 C：加或改一条门禁规则

**规则源**：`architecture-policy.yaml` 的 `rules` 段（正则类）、`facades` 段（AST 类，
门包专用），或**「有执行体的声明表」那类段落**——`sandbox_providers` / `execution_paths` /
`egress_paths` / `seams` / `word_tables`。三类要分清，塞错地方不会报错、只会让规则变成空转：

- `rules` 段是**正则表**，只判文本形态（领域词、import 路径、裸 goroutine 的调用形态）；
- `facades` 段与上面几张声明表要**判结构或跨表一致**，执行体在 `archcheck/*.go` 里走 `go/ast`；
  它们**不在 `rules` 段**，因为正则表达不出「函数体是不是只有一条转发语句」
  或「这个 spawn 点之前有没有真的生成 env」。

**加规则**：

1. 先想清楚**这条规则拦的失效形态是什么**。写不出具体形态 → 这条规则
   是仪式，不是门禁。
2. `rules` 段条目：

   ```yaml
   - id: harness-xxx
     desc: 一句话说清拦什么
     severity: error
     scope: [.]
     include: ["*.go"]
     ignore_test: true          # 三态：true=只扫生产 / false=生产+测试都扫
     only_test: false
     scope_exclude: [cmd]
     forbid_regex: '...'        # 或 forbid_import_regex（判 import 路径，走 AST）
     negative_follow:           # 负向前置：RE2 不支持后顾断言，用结构化表达
       - anchor: 'strings.Contains(out, "'
         forbid_follow: 'xxx")'
   ```

3. **判 import 路径的规则用 `forbid_import_regex`**，不要用 `forbid_regex` 逐行看源码——
   单行 import、别名 import、括号同行全漏。
4. **例外必须显式登记**（`exceptions` 段：rule + path + line 三元组 + `until` + 拆除条件）。
   当前例外数 = 0。**不做静默豁免。**
5. **规则必须自带能过的样例**（`require_rule_examples: true`，判定在 `archcheck/examples.go`）。
   `examples` 段写 `match` 与 `not_match`，`make architecture-check` 会拿它们跑**真实判定路径**：
   正则写错、词表漂移、规则被改成永不命中，都在这里红。
   **缺样例即红**，不是建议——「从没见过它触发的规则等于没有规则」，
   而样例是唯一能让规则自证「我还在判」的东西。样例本身也会过领域词检查，
   所以样例文本必须是**合成数据**（写成真实场景就等于把领域钉进契约）。
6. **反向验证**（[§6](#6-反向验证改门禁必做)）：注入违规 → 门禁报错 + 退出码 1。

**改规则**：先确认现有规则**真的在判**——本仓有 5 处「规则照常出现在报告里、
severity 照常是 error，唯独一件事都不判」的历史静默失效（`AGENTS.md` 有逐条记录）。
所以改判据时，必须验证「坏实现会红」。

> **policy 自身的红线**：
> - 规则须有执行体（两个正则都为空 = 静默跳过，自校验会报「规则无执行体」）。
> - `forbid_import_regex` 须**结构上可能命中**真正的 import 路径：判据面是 import
>   路径（永远是 clean 形式，不含 `//`、不以 `/` 结尾），所以 `(前缀/)(/|$)` 这种
>   「前缀尾部多写一个斜杠」一条都匹配不到却照常计入条数。边界写成 `(前缀)(/|$)`。
>   判定走 `regexp/syntax` 判结构，不是文本比 `//`（后者既误报字符类 `[//]`，
>   又抓不到这个真正要防的形态——它里面并没有相邻的两个斜杠）。
> - `include` 须**可能命中**一个文件名：文件发现只拿 `filepath.Base(path)` 与模式比，
>   所以 `**/*.go`、`src/*.go` 这类带路径分隔符的写法**恒不命中**（`*` 不跨分隔符，
>   基名永远没有分隔符），`*.[go` 这类语法非法的写法则被 `filepath.Match` 报
>   `ErrBadPattern`、被扫描路径静默当成「不匹配」。两者的效果相同：规则在、
>   `severity: error`、报告里仍计入条数，**却一个文件都不扫**。目录维度请写进
>   `scope`（`scope: [pkg]` + `include: ["*.go"]`），`include` 只管文件名。
> - `id` 唯一。
> - `scan_roots` 须覆盖全部登记包。
> - 例外须写 `until` 与拆除条件。
> - 写不存在的字段会被 YAML **静默丢弃**（那条规则于是变成「声明还在、一条都判不了」）。

### 3.4 任务 D：动上游依赖地板

本仓处在 `oagentkit`（agent 原语）/ `oekit`（基础库）矩阵里，
上游**把能力搬家的频率不低**（2026-10-20 一轮就搬了两处：
`toolgate/invoke` → `guardx/invoke`、`obs/obsx` → `llm/obsx`）。

**升级依赖时**：

1. `go.mod` 里 require 的版本必须是**已发布的 tag**。未发布的新包用
   `make devlink`（见下），不要动 go.mod。
2. 用了矩阵模块 → 在该包的 `requires` 里声明 `ext:<矩阵模块>`。
   上游搬家时，**声明面必须跟着改**——搬家在本仓门禁眼里不能「什么都没发生」。
3. 地板抬高对下游是**隐式 breaking**：`go mod tidy` 不跑会报
   `updates to go.mod needed`，且报错不提真正原因。
   → 破坏性变更要写进 [README 的「下游升级须知」表](../README.md#下游升级须知破坏性变更)。

**本地并行开发（改上游仓时）**：

```bash
make devlink     # 生成 go.work，把 guardx 工作副本接入（不改任何 go.mod）
# ...改上游，本仓构建即时生效...
make devunlink   # 断开，构建回到 go.mod 声明的已发布版本
```

- **go.mod 零 replace**（`replace` 只在主模块生效 → 下游被静默忽略 → 断掉所有下游）。
- **workspace 期间别跑 `go work sync`**（会按构建列表回写各模块 go.mod，
  实测把 oagentkit 四个依赖整片剥掉）。要整合同步：先 `devunlink` 再 `go mod tidy`。
- `devlink` 可重复执行（已有 go.work 时走 `use -add` 追加，不重建）。

### 3.5 任务 E：拆文件

两条真阈值（`thresholds.enforce: true`，超限即非零退出）：

| 阈值 | 值 | 含义 |
|---|---|---|
| `max_file_lines` | 400 | 单文件行数上限 |
| `max_methods_per_type` | 25 | 单类型**实质方法数**上限（god object 判据）|
| `forwarding_shell_max_chars` | 140 | 「转发壳」的方法体有效长度上限（不计入上一条）|

**拆分只为满足机械阈值。** 若某个模块拆完仍读不动，该**重新分组**，
不要靠继续拆小来假装问题解决了。

拆分时注意 `max_methods_per_type` 是**按类型**判的——把一个胖类型拆成两个胖类型
仍然红。**按职责分类型**，不是按行数分文件。

### 3.6 任务 F：发版

**顺序不可反**（本仓是按版本引入的下游依赖，发版本身就是给下游的契约）：

```
① 改代码并提交
② 生成 CHANGELOG 并提交（这个提交只改 CHANGELOG.md）
②′ 回执提交（README「下游升级须知」的版本身份 / 台账状态翻面 / 关闭 PR 号回填）
③ 打 tag（**tag 指向 ②′ 那个回执提交**）
④ 推 tag
⑤ 发 GitHub Release
```

**②′ 为什么夹在 ② 与 ③ 之间**：回执是把本版的可核验事实写回文档的那次提交
——README 升级须知那一行从「（本版）」翻成「（已发版，tag `vX.Y.Z`）」、
台账 §9 登记行的状态词翻面、关闭 PR 号回填。让 **tag 指向它**，tag 快照里
这些事实就**已经在场** ⇒ 快照自证这一版已经发出去。反过来（先 tag 再回执）
⇒ 快照自称「本版」，而那个版本已经发出去了 ⇒ 下游从模块缓存里读到的发布说明
**自称未发版**（先例：v0.51.0 的 tag 指向它的回执提交 `cf312e8` 本身）。

**② 的生成命令**（生成器必须是 matrix-tools 的当前版本）：

```bash
python3 <matrix-tools>/scripts/gen_changelogs.py \
    --repo goagent-kernel --root /Users/zhangyi/my_project \
    --next-version vX.Y.Z
```

> 生成器旧版有两个会**静默丢东西**的缺陷（分类渲染分支对 chore/docs/test 类提交
> 只取标题、body 整段丢弃；round 前缀使 conventional commit 解析失效）。
> 判据：**产物行数突然变少、或某版的说明只剩标题没有正文 = 踩到了这个坑**。

**② 的提交纪律**：生成 CHANGELOG 的那个提交**必须只改 `CHANGELOG.md`**。
生成器会过滤掉「只改 CHANGELOG.md」的提交（否则自我指涉）；一旦那个提交同时改了
别的文件，过滤失效——它就会漏在自己那一版的说明之外。

**tag 快照里的 CHANGELOG 必须已含该 tag 自己那一段**——顺序反了会重演上游那条
「已发布 tag 的 CHANGELOG 缺自己那段」的缺口（下游拿到模块缓存才发现，
而 `go mod download` 拿到的就是 tag 快照）。

**重新生成时只插入新段，不要整文件覆盖**（v0.41.0 发版实测）。整文件覆盖会把历史段里
的**手写勘误**冲掉——v0.40.0 段有一条「v0.39.0 的快照不编译，消费过 v0.39.0 的宿主请改钉
v0.40.0」，一次重生成就没了，而那是下游必须看到的信息。做法：生成后取**新版本的段**，
插进提交版的对应位置，保留其余各段原样（判据：`git diff` 只有`+`、没有`-`）。

**提交正文里的行首 `#` 要降级**：生成器忠实复现正文，`# 小节名` 在 CHANGELOG 里会渲染成
H1，把版本段的标题层级压平。改成粗体即可（与「编码损坏只在呈现层修」同口径，下次重新
生成会回来，属已知）。

**这四条现在有机器判据**：`make releasecheck`（已进 `make check`）。它只在 **tag 快照**上判
（HEAD 无 exact tag 时显式跳过并 exit 0，故日常绿）：annotated tag / CHANGELOG 含本版段 /
无 U+FFFD / 区间内无「依赖文件 + 非依赖文件」混装提交（混装清单是**闭合**的：已发布历史
不可重写，只登记不新增）。CI 的 `on.push.tags` 也跑它——tag 必须打在 CI 全绿的提交上。

### 3.6.1 ② 之前的「导出面审计」：breaking 不会自己跳出来

**生成器只渲染 commit 的 body——它不告诉你这版有没有 breaking。** 一次发版要跨十几个
提交时，逐条读 commit body 找「接口改了」是不可靠的：改接口的那个提交往往同时改了
实现与用例，正文里一句话带过（v0.43.0 实录：`#267` 的 `QuotaStore` 扩三个方法藏在
11 单一簇的第三条 bullet 里，生成段里那一行被别的条目压着）。

**发版前对区间跑三问**（三问都只读 git 历史，不改代码）：

1. **导出符号有没有消失**——`git diff <prev>..HEAD -- '*.go'` 里 `^-func [A-Z]` /
   `^-type [A-Z]` / `^-const [A-Z]` / `^-var [A-Z]` 的行；**同包内挪文件不算**
   （import 面不变），**跨包挪算**（import 路径变了）。
2. **既有接口有没有加方法**——`docs/api-compat.md` 的第 1 类 breaking 明写「接口新增
   方法也算」。这条**编译期即红**，但它不会出现在任何一条 commit 的标题里。
3. **既有导出函数的签名有没有变**——变了的函数，逐个核对参数与返回值。

判据落在 `docs/api-compat.md` 的四类 breaking（改签名 / 改语义 / 改默认值 / 删导出
符号）与 `api-lifecycle.yaml` 的定级上：

- 定级 **Stable** 的包命中任何一类 ⇒ **违反承诺**，要么回滚，要么走 `docs/api-compat.md`
  的五步破坏性变更流程；
- 定级 **Experimental** 的包命中 ⇒ **不违约**，但**仍须进 CHANGELOG**（这一步容易漏——
  「不违约」被读成了「不用写」）；
- 命中的每一类都要落进 **README「下游升级须知」表** + **CHANGELOG 的 BREAKING 段**
  （动机 / 旧新对照 / 迁移路径三件齐），tag 注解与 GitHub Release 正文的第一段都是
  「下游要做什么」。

**别用「本版没有依赖地板变化」推断「本版没有 breaking」**：依赖地板（go.mod/go.sum）
只覆盖第 3 类地板问题，导出面改动与它无关。v0.43.0 实录：区间内 go.mod/go.sum 零改动
（⇒ 下游无需 `go mod tidy`），但 `scheduling.QuotaStore` 扩了三个方法——那是编译期即红
的 breaking，两者互不相关。

---

## 4. 门禁速查：红了就查这里

一条命令跑全套：

```bash
make check
```

### 4.0 `make check` 的全链：按顺序，红即停

`make check` 不带 `-j` 时**按依赖链顺序逐个执行，第一个失败即停** ⇒
「红了先查哪条」= **第一条红的**。加了 `-j` 就是并行，顺序不再保证。

<div align="center">
<svg width="760" viewBox="0 0 760 452" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <rect x="0" y="0" width="760" height="452" rx="10" fill="#1c2330" stroke="#262e3d"/>
  <text x="30" y="26" font-size="12.5" fill="#9aa7b8">make check 的依赖链（自上而下执行）</text>
  <rect x="30" y="36" width="700" height="54" rx="10" fill="#161b26" stroke="#262e3d"/>
  <text x="44" y="68" font-size="12.5" fill="#e6edf3">口径层</text>
  <rect x="150" y="54" width="110" height="22" rx="8" fill="#0d1117" stroke="#58a6ff"/>
  <text x="205" y="69" font-size="12" fill="#e6edf3" text-anchor="middle">versioncheck</text>
  <rect x="268" y="54" width="108" height="22" rx="8" fill="#0d1117" stroke="#58a6ff"/>
  <text x="322" y="69" font-size="12" fill="#e6edf3" text-anchor="middle">cijobname</text>
  <text x="630" y="69" font-size="11" fill="#9aa7b8">工具版本 / job 名</text>
  <rect x="30" y="100" width="700" height="54" rx="10" fill="#161b26" stroke="#262e3d"/>
  <text x="44" y="132" font-size="12.5" fill="#e6edf3">静态层</text>
  <rect x="150" y="118" width="92" height="22" rx="8" fill="#0d1117" stroke="#58a6ff"/>
  <text x="196" y="133" font-size="12" fill="#e6edf3" text-anchor="middle">fmt-check</text>
  <rect x="250" y="118" width="46" height="22" rx="8" fill="#0d1117" stroke="#58a6ff"/>
  <text x="273" y="133" font-size="12" fill="#e6edf3" text-anchor="middle">vet</text>
  <rect x="304" y="118" width="46" height="22" rx="8" fill="#0d1117" stroke="#58a6ff"/>
  <text x="327" y="133" font-size="12" fill="#e6edf3" text-anchor="middle">lint</text>
  <text x="630" y="133" font-size="11" fill="#9aa7b8">格式 / vet / 静态检查</text>
  <rect x="30" y="164" width="700" height="54" rx="10" fill="#161b26" stroke="#262e3d"/>
  <text x="44" y="196" font-size="12.5" fill="#e6edf3">测试层</text>
  <rect x="150" y="182" width="56" height="22" rx="8" fill="#0d1117" stroke="#3fb950"/>
  <text x="178" y="197" font-size="12" fill="#e6edf3" text-anchor="middle">test</text>
  <rect x="214" y="182" width="56" height="22" rx="8" fill="#0d1117" stroke="#3fb950"/>
  <text x="242" y="197" font-size="12" fill="#e6edf3" text-anchor="middle">race</text>
  <text x="630" y="197" font-size="11" fill="#9aa7b8">单测 / 竞态（开 cgo）</text>
  <rect x="30" y="228" width="700" height="54" rx="10" fill="#161b26" stroke="#262e3d"/>
  <text x="44" y="260" font-size="12.5" fill="#e6edf3">阈值扫描</text>
  <rect x="150" y="246" width="96" height="22" rx="8" fill="#0d1117" stroke="#a371f7"/>
  <text x="198" y="261" font-size="12" fill="#e6edf3" text-anchor="middle">cover-gate</text>
  <rect x="254" y="246" width="54" height="22" rx="8" fill="#0d1117" stroke="#a371f7"/>
  <text x="281" y="261" font-size="12" fill="#e6edf3" text-anchor="middle">vuln</text>
  <rect x="316" y="246" width="102" height="22" rx="8" fill="#0d1117" stroke="#a371f7"/>
  <text x="367" y="261" font-size="12" fill="#e6edf3" text-anchor="middle">crossbuild</text>
  <text x="630" y="261" font-size="11" fill="#9aa7b8">覆盖 / 漏洞 / 平台</text>
  <rect x="30" y="292" width="700" height="76" rx="10" fill="#161b26" stroke="#262e3d"/>
  <text x="44" y="330" font-size="12.5" fill="#e6edf3">声明一致性</text>
  <rect x="150" y="304" width="152" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="226" y="319" font-size="12" fill="#e6edf3" text-anchor="middle">architecture-check</text>
  <rect x="310" y="304" width="84" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="352" y="319" font-size="12" fill="#e6edf3" text-anchor="middle">docsync</text>
  <rect x="402" y="304" width="92" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="448" y="319" font-size="12" fill="#e6edf3" text-anchor="middle">seamcheck</text>
  <rect x="502" y="304" width="108" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="556" y="319" font-size="12" fill="#e6edf3" text-anchor="middle">apilifecheck</text>
  <rect x="150" y="334" width="100" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="200" y="349" font-size="12" fill="#e6edf3" text-anchor="middle">deploycheck</text>
  <rect x="258" y="334" width="112" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="314" y="349" font-size="12" fill="#e6edf3" text-anchor="middle">storagecheck</text>
  <rect x="378" y="334" width="108" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="432" y="349" font-size="12" fill="#e6edf3" text-anchor="middle">ledgercheck</text>
  <rect x="494" y="334" width="108" height="22" rx="8" fill="#0d1117" stroke="#f85149"/>
  <text x="548" y="349" font-size="12" fill="#e6edf3" text-anchor="middle">releasecheck</text>
  <text x="630" y="330" font-size="11" fill="#9aa7b8">声明面漂移</text>
  <rect x="30" y="378" width="700" height="54" rx="10" fill="#161b26" stroke="#262e3d"/>
  <text x="44" y="410" font-size="12.5" fill="#e6edf3">仓库卫生</text>
  <rect x="150" y="396" width="102" height="22" rx="8" fill="#0d1117" stroke="#9aa7b8"/>
  <text x="201" y="411" font-size="12" fill="#e6edf3" text-anchor="middle">modreplace</text>
  <rect x="260" y="396" width="92" height="22" rx="8" fill="#0d1117" stroke="#9aa7b8"/>
  <text x="306" y="411" font-size="12" fill="#e6edf3" text-anchor="middle">modwork</text>
  <rect x="360" y="396" width="80" height="22" rx="8" fill="#0d1117" stroke="#9aa7b8"/>
  <text x="400" y="411" font-size="12" fill="#e6edf3" text-anchor="middle">hygiene</text>
  <text x="630" y="410" font-size="11" fill="#9aa7b8">入库卫生</text>
</svg>
</div>

*图 4：`make check` 的全链目标分层——自上而下逐条执行，第一条红的就先查它。*

> **CI 执行面（2026-11-18 实测补记，别按旧文理解）**：CI 的**七个作业**
> （`unit` / `race` / `contract` / `fault-injection` / `gates` /
> `windows-test` / `integration`）执行的是**分层目标**，
> **没有任何一个执行 `make check` 本身**
> ⇒ **只挂在 `check` 上、没进任何分层目标的门禁，CI 上一次都不会跑**
> （实测被漏掉的是 `deploycheck` 与 `releasecheck`——本轮已接进 `unit` 层）。
> ⇒ **新增门禁时两处都要接**：接进 `check`（本地一条命令跑全）**还要**接进
> CI 跑的那个分层目标（否则 CI 不判）。改 job 名要同步分支保护的 required
> contexts（逐字相同），所以优先**不动 job 名**、只动分层目标里的门禁清单。
> **当前状态**：`check` 的**全链目标全部**落在 CI 的分层目标里
> （`unit` 覆盖 fmt/vet/lint/单测 + architecture-check/docsync/seamcheck/
> apilifecheck/deploycheck/storagecheck/ledgercheck/releasecheck/modreplace/
> modwork/hygiene/cijobname，`gates` 覆盖 versioncheck/cover-gate/vuln/
> crossbuild，`race` 单列一层）。

| 目标 | 拦什么 | 红了怎么修 |
|---|---|---|
| `versioncheck` | 门禁工具版本口径：ci.yml 装的版本与 Makefile 声明的不一致 | 两处同步（Makefile 的 `LINT_VERSION` / `GOVULNCHECK_VERSION` 是唯一来源）|
| `cijobname` | ci.yml 的 job 名超 90 字节（GitHub 截断 ⇒ 分支保护的 required context 匹配不上 ⇒ 每个 PR 卡在 BLOCKED 且看不出原因）| 改短 job 名，并同步分支保护的 required contexts（两者必须逐字相同）|
| `fmt-check` | gofmt 未格式化 | `gofmt -w <文件>`；文件集由 git 决定，不递归扫 `.*-work/` |
| `vet` | go vet | 按提示改 |
| `lint` | golangci-lint（未安装时**明确报错退出**，不静默跳过） | `brew install golangci-lint`；CI 钉 v2.14.0 |
| `test` | 全量单测 | 看失败断言 |
| `cover-gate` | 全局覆盖率 < 60% **或**任一包 < 70%（判据执行体 `cmd/pkgcov`；实测值以该目标输出为准） | 补**该包**的用例，别降阈值；豁免登记在 Makefile 的两张表：`PKG_COV_EXEMPT`（普通，当前为空）与 `PKG_COV_STRUCTURAL_EXEMPT`（结构性：包的性质决定通用判据不适用，自己达标不判红，但没命中任何包仍判红），两张表都失效即红|
| `architecture-check` | 规则 + 门包 + 各包依赖方向（**条数 / 门包数 / 包数一律以该命令输出为准**，本表不抄——抄进来就是给自己造一份会静默过期的真相）| 见下表 |
| `vuln` | `govulncheck` 报出可达漏洞（未安装时**明确报错退出**） | 依赖漏洞抬版本；**标准库**漏洞抬 go.mod 的 `go` 指令（`toolchain` 指令会被忽略）|
| `race` | 数据竞争（`-race`，**开 cgo**：Linux 上 `-race requires cgo`）。失败时由 `cmd/raceprobe` 出**形态判定**：数据竞争 / 用例挂死（**有** goroutine 栈 ⇒ 代码面）/ **启动期楔死**（**没有**栈 ⇒ Go 运行时压根没起来 ⇒ **环境面**，改代码没用，查机器面）/ 普通用例失败 / 认不出 | 按形态处置：代码面按栈或失败用例名修；环境面看 raceprobe 落的取证（原始输出 + 结论块，默认在 `$TMPDIR/goagent-kernel-race`）；别把该目标改成 `CGO_ENABLED=0` |
| `crossbuild` | 声明支持的平台（`CROSS_TARGETS` = linux/amd64、linux/arm64、darwin/arm64、**windows/amd64**）编不过 | 看是否引入了平台专属符号；windows 侧是**语义降级**（无进程组信号语义，TERM 优雅退出降级为整树硬终止），不是不支持 |
| `docsync` | README **包表** ↔ 仓库实际（包名 / 行数 / 依赖列 / 职责列）+ **「工程底座」表的三项确定性量**（生产行数 / 文件数 / 测试文件数，判相等不留容差）| 按报错给的实测值改 README；**覆盖率与其余描述性行不在判定面内**，那几格要自己手算 |
| `seamcheck` | 接缝契约三形态（A 只读 / B 阻断 / C 阻断）没写齐（issue #400）| 按 `docs/SEAM-CONTRACT.md` 补声明与三条约束；基线用 `go run ./cmd/seamcheck -update` 落 |
| `apilifecheck` | API 生命周期四方一致性：`api-lifecycle.yaml` ↔ 包注释标记 ↔ 生产包覆盖（漏登记/幽灵登记）↔ Deprecated 替代指向存在性；外加错误哨兵登记表 ↔ 代码双向判等（issue #113/#104）| 新包补登记 + 包注释 `// 生命周期:` 标记；改级别两处同步；Deprecated 补真实存在的 replacement；新增哨兵登记进 `docs/error-semantics.md`；口径见 `docs/api-compat.md` |
| `deploycheck` | 部署配方能力清单的证据不可解析 / 状态与证据不符 / USAGE 逐行不一致（issue #277）| 见 `docs/DEPLOY-MATRIX.yaml`；**issue 证据只有在对应单号仍开着时才算缺口**（单号状态由台账派生） |
| `storagecheck` | 存储选型判据：场景声明的 storage 必须在选型声明面的允许集内 + 该 storage 必须实际提供场景必需的全部能力 + 「该 storage 提供该能力」的断言必须能在代码里找到落点（issue #424）| 见 `docs/STORAGE-SELECTION.yaml`；**第三条是它与「写份文档让人读」的分界**——没有它，「SQLite 提供租户隔离」就成了一句没人验证的话 |
| `ledgercheck` | 演进台账自身的纪律：同一单号各处状态矛盾 / 已合入却没给关闭 PR 号 / 关闭 PR 号形态不对 / 正文引用的单号没进登记面（单号漂浮 = 无主）| 台账 `docs/DISTRIBUTED-EVOLUTION-20261117.md`；单号 → 状态的派生器是 `internal/ledgerstatus`（登记列 / 状态列 / 散文标记三处约定）；PR 号要写成 `PR #N` 才不进单号视图 |
| `releasecheck` | 发版纪律：annotated tag / CHANGELOG 含本版段 / 无 U+FFFD / 区间内无混装提交 | 非 tag 提交**显式跳过**（打 tag 后必判）；口径见 §3.6 |
| `modreplace` | go.mod 含 `replace` | 改用 `make devlink` |
| `modwork` | go.work / go.work.sum 被跟踪 | `git rm --cached` |
| `hygiene` | 仓库**跟踪**了编译产物二进制（本地构建产物留在工作树里是正确状态，判的是「进了版本库」）| `git rm --cached <路径>` + 删掉工作树副本 + 把该路径补进 `.gitignore`；构建产物请用 `make` 目标或 `go build -o bin/` 产出 |

### 4.1 架构门禁的规则表

> **条数口径**：`make architecture-check` 打印的是「N 条规则 + 0 个门面包 + M 个包的
> 依赖方向」——「N 条」指 policy `rules` 段的条目数（**当前 13 条**），
> **不含**用 `go/ast` 判的结构规则（条数随 `archcheck/*.go` 增减而变，
> 以 `make architecture-check` 的输出为准）——后者含门包不变 / 依赖方向 /
> 沙箱档位登记 / 执行路径 / 子进程出口 / 未受管逃逸 / 接缝契约三形态 /
> 租户隔离六条 / 判定词表七条 / policy 自校验。
> 包数与规则数都**以命令输出为准**，本表不抄条数（抄进来就是给自己造一份会静默
> 过期的真相）。下表把两类都列出来，故行数比「N 条」多。
> **门禁表里不写死条数**：此前写死「9 条规则」既是拿混了量纲（那句话对着 `rules` 段），
> 又会随规则增减**静默过期** ⇒ 一律以命令输出为准。
>
> 另记：`require_rule_examples: true` 让每条 `rules` 条目**必须自带能过的样例**——
> 缺样例即红。这使 `rules` 段的条数有了下限保证：不会因为某人删了样例而变成空转。

**第一组：policy `rules` 段（正则类，当前 13 条）**

| 规则 | 拦什么 | 典型修法 |
|---|---|---|
| `harness-no-domain-vocabulary` | 生产代码里的运维领域词 | 换成机制侧通用表述；词表属宿主 |
| `harness-test-fixture-domain-vocabulary` | 测试夹具用了真实运维词面 | **改合成数据**（不是改词表） |
| `harness-no-host-dependency` | 任何对宿主仓的 import | 定义接口由宿主实现 |
| `harness-no-domain-runtime-deps` | 对宿主仓 / 旧门面仓 / 已退役 `agentkit` 门面的 import | 改走 `github.com/oagentkit/*` |
| `harness-no-bare-goroutine` | 生产代码裸 `go func` / `go someFunc()` | 走 `concurrency/async.GoSafe` |
| `harness-test-goroutine-joined` | **用例内**起的 goroutine 在用例返回前没被等待（测试侧无等待点即违规）| 在用例内等它（`<-done` / `wg.Wait()` / 收尾助手）；「长得像等待、其实不是」（`<-ch` 写在**被启动的那个 goroutine 体内**）仍判红 |
| `harness-test-defer-cleanup` | **用例内**起的 goroutine，其**收尾等待点**写在普通语句里，而它与 `go` 语句之间有 `t.Fatal` 族 / `runtime.Goexit` 的直接调用 ⇒ 失败路径上 Goexit 会跳过这一步 ⇒ goroutine 实际未被 join（**#440**）| 把收尾等待写进 `defer`（`defer func(){ <-done }()` 或 `defer` 收尾助手）；**同步等待**（go 之后到等待点之间没有任何能让用例提前终止的调用）不判 |
| `harness-test-wallclock-ruler` | 测试函数体里的**裸 `time.Sleep`**（不在 for/range 循环体内）⇒ 拿墙钟窗口当尺子：窗口长度单独决定结论 ⇒ 机器一忙就假红、机制抢跑就白测（**#441**）| 改成「等可观测事实」（计数器 / 通道 / 标志 / 状态）；**轮询退避**（sleep 在循环体内）、**goroutine 体里**的 sleep、policy `managed_helpers:` 登记的**受管辅助**不判 |
| `harness-no-printf-log-callback` | 公开 API 里的 `logf func(string, ...any)` | 留痕走 `logx.Delegating` 懒解析 |
| `harness-single-errors-package` | stdlib `"errors"` 与 `github.com/pkg/errors` | 只用 `oekit/pkg/errors` 一套 |
| `harness-sandbox-execution-single-entry` | 生产代码在 `ptc` 执行后端外直接 `import "os/exec"` | 外部程序执行只允许经 **`ptc` 执行后端**（收口点已从 `sandbox` 迁到 `ptc`——sandbox 门包已删，档位语义直连 guardx）|
| `tool-pairing-key-single-source` | 工具配对键（`call_id`）被直接读 `Payload["call_id"]`（两处各写一份 ⇒ 同一事件流两个相反结论）| 改读 `eventbus.ToolCallID(ev)` |

**第二组：`go/ast` 结构类（不在 `rules` 段，执行体在 `archcheck/`）**

| 规则 | 拦什么 | 典型修法 |
|---|---|---|
| `harness-facade-stays-facade` | 门包长出实现（本仓当前 `facades` 段是**空清单**，故这条今天没有对象可判）| 逻辑放上游 `oagentkit/*`，本仓只留转发 |
| `harness-module-dependency-direction` | 越界内部依赖 / 越界跨仓依赖 / **未登记包目录** | 改 `modules` 段登记 `requires` |
| `harness-policy-self-check` | policy 自相矛盾：规则无执行体、id 重复、`scan_roots` 与 `modules` 不一致、例外无 `until`、**包根指向空目录**、租户门缺形状、**词表登记面自相矛盾**（包名 / 类别写错、归一出口挂错表、重复登记、豁免缺理由）| 补执行体 / 改声明 / 删陈旧包根 |
| `harness-sandbox-provider-declared` | 执行沙箱后端的档位没登记在 `sandbox_providers` 段，或登记与代码事实不一致 | 补登记 / 同步登记的档位常量名（LevelNone/LevelProcess/LevelOS/LevelMachine）|
| `harness-sandbox-execution-path` | `execution_paths` 登记的「档位 → 构造器」与代码事实偏离（构造器改名 / 档位没人造）| 同步 `execution_paths` 段与代码 |
| `harness-subprocess-egress` | 子进程启动点没在 `egress_paths` 登记（含别名 / 点导入 / 包级初始化，以及 `os.StartProcess`、`syscall.*` 入口）；登记 `proxy` 却在 spawn 前看不到兑现的**两半**（env 生成 + 后端能力问询 `SatisfiedBy`）；登记 `denied`（本层无法兑现）；登记已陈旧；**启动点所在文件把整份父环境读出来**（`os.Environ` / `procx.InheritAllExcept`——按**文件**判，不按启动函数判）| 补登记 / 在启动函数里真的用 `egress.Spec.Env` 生成 env **并**在 spawn 前问一次后端能力（`Spec.SatisfiedBy`）/ 改档位。**`denied` 在本层是造假，不是配置**；子进程环境必须由 `procx.ChildEnv` 白名单生成 |
| `harness-ptc-unmanaged-escape` | 生产代码显式选择「无判定环执行」（`AllowUnmanaged` 字段**出现**即违规，不看值——配置驱动的逃逸口也必须显式登记豁免）| 去掉逃逸口；确实要留就先在 policy 的 `exceptions` 登记 |
| `harness-seam-contract` | 接缝契约**形态 B**：契约标记不完备（缺 `no-reentry` / `no-blocking` 等约束词）| 按 `docs/SEAM-CONTRACT.md` 补标记 |
| `harness-seam-lock-held` | 接缝契约**形态 A**：接缝内持锁调用了时钟访问器（只读扫描，**warn 不阻断**）| 把时钟取值移出锁区 |
| `harness-seam-panic-escape` | 接缝契约**形态 C**：宿主调用未包在 panic 隔离包装器里（panic 外溢，**阻断**）| 用登记的包装器包住（`subhub.Do` / `async.GoSafe` 等）|
| `tenant-surface-missing-evidence` | 带授权上下文形参 + 返回租户域类型的服务层函数，既没调登记的共享判定面、查询条件也没含过滤列、也没 `// tenant:iso <理由>` 声明 | 补隔离判定（共享判定面 / 过滤列）或在函数注释里写明隔离点 |
| `viewer-dropped` | 带授权上下文形参却完全不用（授权上下文被丢弃） | 用它做隔离判定，或删掉这个形参 |
| `shared-surface-missing` | 登记在册的共享隔离判定面在代码里不存在（清单漂移 = 登记失效） | 同步 `tenant_gate.shared_surfaces` 与代码（改名/迁走即改，删面即撤登记） |
| `tenant-policy-missing` | 含租户字段的模型映射出的物理表**不在** `tenant_gate` 清单里 | 补登记该表 |
| `tenant-policy-drift` | 清单登记的表在模型面**已不存在**（登记漂移） | 删掉陈旧登记项 |
| `tenant-table-name-missing` | 含租户字段的模型**没有可从 AST 识别的表名** | 给该模型补可解析的表名方法 |
| `harness-word-table-enum-missing` | `word_tables` 段登记的词表**没有导出枚举出口**，或出口读的是另一张表（第二个真相源），或登记与事实已偏离（包改名 / 表改名 / 形状与 `kind` 不符）| 补 `All*()` 枚举出口并让它读**登记的那张表**；登记漂移就改 `word_tables` 段；分组形状的表写 `kind: input` |
| `harness-word-table-handcopy` | 别的包在生产代码里**手抄**了登记**判定词表**的取值（那处字面量没有名字：既不在主人表定义处、也不在常量声明处）| 引用主人常量（`contract.StatusFailed`），或在本包把该取值**声明成自己的常量**（同一个拼写、不同概念时用后者）|
| `harness-word-table-predicate-drift` | 登记的**判定谓词**不存在，或它不读登记的那张表（判定与词表分叉）| 补谓词（包级函数或 `Type.Method` 形态）并让它读**登记的那张表**；判定面改名了就同步 `word_tables` 段 |
| `harness-word-table-normalizer-missing` | **输入归一词表**没有**导出**的归一出口，或归一出口不读登记的那张表；判定词表却声明了归一出口 | 补一个**导出**的归一函数并让它读那张表；判定词表的归一出口声明删掉（那是 input 类的属性）|
| `harness-word-table-family-uncovered` | 登记的词表**没有覆盖它那一族常量的全部取值**（主人包新增了同族常量却忘了加进表 ⇒ 那个新取值的消费侧从此没人管）；或表的键不属于族前缀；或族豁免已失效 | 把族常量加进那张表；或改族前缀；或让**归属别处**的那张表（哪怕未登记）声明自己拥有的前缀（第十四阶段起族归属按**最长匹配前缀**派生，`family_exclude` 已废弃）|
| `harness-word-table-value-collision` | 两张**判定词表**有同形取值却**没声明关系**（撞车没登记 ⇒ 没人说得清「这处字面量想说的是哪张表」，手抄指路只能瞎猜）；或**声明了关系却没撞车**（声明失真：今天没关系，将来一旦同形就会无声生效）| 在 `word_tables` 段的 `relations` 清单里声明这对表的关系（`same` = 同一概念 / `distinct` = 不同概念）；或删掉失真的声明 |
| `harness-word-table-relation-anchor-drift` | 同形关系的**锚点**指向的东西不再对：锚点常量**改名或被删**（= 登记漂移）；锚点常量的取值**不再属于它那张表**；两端锚定的取值**不成对**（关系的前提「同形」不成立）；`distinct` 却锚到**同一处声明**（同一处声明不可能是两个概念 ⇒ 「不同概念」是说谎的）；**all 形态**下：共有取值在本端没有对应常量，或 `distinct` 的**每一个**共有取值都解析到同一处声明 | 改锚点指向现存的常量；或让两端锚点按取值成对；或改用 `{all: true}` 锚住两端共有取值的全集；或把 `distinct` 改成 `same` |

> `harness-module-dependency-direction` 的第三种形态最隐蔽：**未登记的包对本门禁完全隐形**——
> 任何模块都能 import 它而不判违规。判定粒度是**包目录**，不是顶层目录。

### 4.2 门禁红了先自查的三件事

1. **是代码错了还是规则该改？** 后者必须**先改 policy 再写代码**，
   且在 commit 说明里写清理由。**不要为了变绿而绕过。**
2. **这条规则真的在判吗？** 本仓有 5 处「规则出现在报告里、severity 是 error、
   但一件事都不判」的历史静默失效。改判据时必须做反向验证。
3. **门禁是不是根本没跑？** 「从没见过它触发」的规则等于没有规则；
   「没人跑的门禁」也不等于有门禁——本仓有 `lint` 曾同时不在 `check` 也不在 CI。

---

## 5. AI 代理高发失败模式

> 这一节是本仓的**经验沉淀**。每一条都对应一次真实事故，
> 触发条件都是「AI 按最直觉的写法写代码」。
> **用法**：门禁红 ⇒ 拿报出的那行去「症状」列对号 ⇒ 照「怎么改」列走 ⇒
> 确认「会被哪条门禁拦」那一列就是它。**没有一列写着门禁名的，说明它靠人记**
> ⇒ 改完顺手补一条用例或一条声明。

<div align="center">
<svg width="760" viewBox="0 0 760 246" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
  <rect x="0" y="0" width="760" height="246" rx="10" fill="#1c2330" stroke="#262e3d"/>
  <rect x="20" y="28" width="160" height="84" rx="8" fill="#161b26" stroke="#f85149"/>
  <text x="100" y="52" font-size="14" fill="#f85149" text-anchor="middle">症状</text>
  <text x="100" y="74" font-size="11.5" fill="#e6edf3" text-anchor="middle">门禁报出的那一行</text>
  <text x="100" y="92" font-size="11.5" fill="#9aa7b8" text-anchor="middle">逐字抄下来</text>
  <line x1="180" y1="70" x2="200" y2="70" stroke="#9aa7b8" stroke-width="1.5"/>
  <polygon points="200,70 193,66 193,74" fill="#9aa7b8"/>
  <rect x="206" y="28" width="160" height="84" rx="8" fill="#161b26" stroke="#58a6ff"/>
  <text x="286" y="52" font-size="14" fill="#58a6ff" text-anchor="middle">真因</text>
  <text x="286" y="74" font-size="11.5" fill="#e6edf3" text-anchor="middle">为什么直觉会错</text>
  <text x="286" y="92" font-size="11.5" fill="#9aa7b8" text-anchor="middle">找判据的前提</text>
  <line x1="366" y1="70" x2="386" y2="70" stroke="#9aa7b8" stroke-width="1.5"/>
  <polygon points="386,70 379,66 379,74" fill="#9aa7b8"/>
  <rect x="392" y="28" width="160" height="84" rx="8" fill="#161b26" stroke="#a371f7"/>
  <text x="472" y="52" font-size="14" fill="#a371f7" text-anchor="middle">改哪</text>
  <text x="472" y="74" font-size="11.5" fill="#e6edf3" text-anchor="middle">改哪个文件</text>
  <text x="472" y="92" font-size="11.5" fill="#9aa7b8" text-anchor="middle">先改声明，再改实现</text>
  <line x1="552" y1="70" x2="572" y2="70" stroke="#9aa7b8" stroke-width="1.5"/>
  <polygon points="572,70 565,66 565,74" fill="#9aa7b8"/>
  <rect x="578" y="28" width="162" height="84" rx="8" fill="#161b26" stroke="#3fb950"/>
  <text x="659" y="52" font-size="14" fill="#3fb950" text-anchor="middle">验收</text>
  <text x="659" y="74" font-size="11.5" fill="#e6edf3" text-anchor="middle">反向验证</text>
  <text x="659" y="92" font-size="11.5" fill="#9aa7b8" text-anchor="middle">注入违规 ⇒ 必红</text>
  <text x="100" y="140" font-size="11" fill="#9aa7b8" text-anchor="middle">「越界依赖」</text>
  <text x="286" y="140" font-size="11" fill="#9aa7b8" text-anchor="middle">声明先行，声明没写</text>
  <text x="286" y="156" font-size="11" fill="#9aa7b8" text-anchor="middle">实现对门禁隐形</text>
  <text x="472" y="140" font-size="11" fill="#9aa7b8" text-anchor="middle">policy modules 段</text>
  <text x="472" y="156" font-size="11" fill="#9aa7b8" text-anchor="middle">→ 再写代码</text>
  <text x="659" y="140" font-size="11" fill="#9aa7b8" text-anchor="middle">make check 全绿</text>
  <text x="659" y="156" font-size="11" fill="#9aa7b8" text-anchor="middle">+ 坏实现会红</text>
  <rect x="20" y="176" width="720" height="50" rx="8" fill="#161b26" stroke="#262e3d"/>
  <text x="380" y="197" font-size="12" fill="#e6edf3" text-anchor="middle">四步缺一不可：缺「改哪」= 写完再补声明（双份工作）；缺「验收」= 判据可能恒绿</text>
  <text x="380" y="215" font-size="11.5" fill="#9aa7b8" text-anchor="middle">恒绿是本仓最贵的失效形态——它比红更难发现，因为没有任何信号会响</text>
</svg>
</div>

*图 5：一条失败模式的四段解剖——「怎么改」必须落到具体文件，「验收」必须是可复跑的注入。*

### 5.1 速查表

| # | 症状 | 真因（为什么直觉会错） | 怎么改 | 会被哪条门禁拦 |
|---|---|---|---|---|
| 1 | 门禁报「越界依赖」或「未登记包」 | AI 倾向「先把功能写完，配置最后补」；本仓门禁**声明先行**，声明不写，实现对门禁隐形 | 照 §3.1 的顺序：**先改 policy `modules` → `scan_roots`**，再写代码，最后同步 README 包表 | `architecture-check`（`harness-module-dependency-direction`）|
| 2 | `harness-test-fixture-domain-vocabulary` 报红 | 写测试时**为让场景真实**引入领域词（真实主机名、真实告警文本）；但夹具是**契约的示例**，示例带领域味 = 把契约钉死在某领域 | 一律用**合成数据**（`host-a` / `node-1` / 中性描述）| `architecture-check`（`harness-test-fixture-domain-vocabulary`）|
| 3 | 在门包位置「顺手加两行」 | 门包只允许**四种声明**（类型别名 / 常量别名 / 单条上游转发函数 / 允许集合内的 import）；加真逻辑三者必居其一，**没有「就两行」的例外** | 逻辑写进上游 `oagentkit/*`。⚠️ **本仓现在一个门包都没有**（`facades: []`）⇒ 这条规则今天恒绿；**`dispatch` 不是门包**，它是派发域的失败面 | `architecture-check`（`harness-facade-stays-facade`，当前无对象可判）|
| 4 | `harness-single-errors-package` 报红；或 `errors.Is/As` 判定失效（Not Found 变 500）| 双 import 两套 errors 包会**打断类型同一性** | 哨兵用 `oekit/pkg/errors.New`，包装用 `errors.Wrap(err, msg, kv...)`（**变参 kv，不是 `Wrapf`**），判定用同包 `Is`/`As`，解包到根用 `Cause`；链路**单 import** | `architecture-check`（`harness-single-errors-package`）|
| 5 | `harness-no-bare-goroutine` 报红 | 裸 goroutine 里的 **panic 无法被外层 recover**（panic 发生在 goroutine 内部），会直接杀进程 | 走 `concurrency/async.GoSafe` | `architecture-check`（`harness-no-bare-goroutine`）|
| 6 | `harness-test-goroutine-joined` 报红 | 用例里起了 goroutine 却在**测试侧**没有等待点 ⇒ 用例返回时 goroutine 还在跑（泄漏）| 在用例内等它（`<-done` / `wg.Wait()` / 同包收尾助手）；**注意**：`<-ch` 写在**被启动的那个 goroutine 体内**不算等待点 | `architecture-check`（`harness-test-goroutine-joined`）|
| 7 | `harness-no-printf-log-callback` 报红 | printf 回调一旦焊进公开签名就是**不可逆 API 债** | 留痕走 `logx.Delegating` 懒解析，宿主用 `logx.SetDefault` 换后端 | `architecture-check`（`harness-no-printf-log-callback`）|
| 8 | 并发下代次不唯一 / 回退；轮询方看到「6 → 6」以为没换过 | 为「方便查询」加第二个计数器 ⇒ 两次交换拿到同一代次是**静默**的：不崩溃，只是偶尔给错答案 | 只允许**一个真相**；需要全局视图时让它是真相的**投影** | 无门禁 ⇒ **靠用例钉**（这类失效靠人记）|
| 9 | 某个位置成了整条链上**唯一绕过所有门禁**的路径 | 把「程序化调用」当特例（普通工具调用过判定环，程序内 `host.<tool>()` 走另一条路）| `ptc` 程序内调用与普通工具调用**走同一套 Stage 判定环**；逃逸口 `AllowUnmanaged` 在生产代码里出现即违规 | `architecture-check`（`harness-ptc-unmanaged-escape`）|
| 10 | 「生产侧派得出、消费侧认不出」 | 跨包契约各写一份 ⇒ 两边慢慢漂移 | 规则收成**单一真相**（如 `toolname`），两侧共用；用**端到端用例**钉「派生 → 装配 → 执行」整条链 | `architecture-check`（`tool-pairing-key-single-source`）+ `test` |
| 11 | 模型写 `"OUT"` 而非 `"out"`，scope 被**反转**，且不归一的条目**被静默丢弃**（连 warn 都没有）| 逐个字段补枚举位 ⇒ 漏一个就静默丢一条 | 归一**收成一处共享口径**（`protocol/report_normalize.go`），并用**枚举式**用例把「漏一个就红」变成机械判定 | `test`（枚举式用例）+ `architecture-check`（域词）|
| 12 | README「工程底座」表里的数字与实际差很远，但**没有任何门禁报红** | 以为「改了代码文档自然会更新」，或以为「README 是文档，文档不会被测」| 生产行数 / 文件数 / 测试文件数**已判相等不留容差** ⇒ 不用手算，门禁会告诉你实测值。**覆盖率那一格仍没人判** ⇒ 改前手算 | `docsync`（三项确定性量）|
| 13 | 某类型的方法数超 `max_methods_per_type`，但它明明是**已经拆干净的 façade** | 「方法多 = 复杂」在两种类型上含义不同：真 god object 上是**复杂度集中度**，façade 上是**公共 API 表面规模** | 阈值比的是**实质方法数**（总数 − 转发壳数）；新增纯转发访问器**不计入**。别为「让数好看」去调 `forwarding_shell_max_chars` | `architecture-check`（`max_methods_per_type`）|
| 14 | 协议对拍单测全绿，真实端点一打就红（形状不同 / 语义口径不同），**两端各自自洽、都不报错** | 以为「文档样例 = 协议」，但样例只是**一种可能的实现**（字段形状与语义口径都可能不同）| ① 单测对拍官方文档样例（保形状）；② 留**可复跑**的真机用例（build tag + 环境变量开关）；③ mock 模式只验**接线**不验质量；④ 首启要加载资源的端点加**预热**并**异步**装配 | 无门禁 ⇒ 靠真机用例背书 |
| 15 | 并行阶段明明逐个完成了一部分，续跑时**整批重来**；宿主于是绕开内核自建存取 | 把「有序步骤链」的判据套到并行图上：「等前驱完成才可信」对**有序链**正确，对**并行组**是错的 | 判据的量纲要跟图的形状对齐：有序链 ⇒ 只信连续前缀；并行组 ⇒ 显式声明依赖（`StepDef.DependsOn` / `Isolated`），按各自前驱判定 | `test` + `fault-injection`（矩阵）|
| 16 | 库里有修复函数、CHANGELOG 有回执、issue 已 CLOSED，但**下游调用点仍是旧写法**，缺陷照旧生效 | 判断「修了没」时看的是「库里有什么」（grep 到新 API 就收工），而缺陷消失与否取决于**调用点怎么写** | 判据取**调用点**，不取 issue 状态；新增「归一/兜底」类 API 时**同步改接线手册里的范式代码块**；「有意保留的原语」用用例钉死；能给编译器拒的就换**命名类型** | 无门禁 ⇒ 靠用例钉 |
| 17 | 本机全绿，目标平台 CI 偶发红，**同一提交**两个平台结论不同；红的那条只给一句 errno | 以为「错误码就那么一个」——同一个语义在不同平台有多种错误表达 | 判定结构放平台无关的文件、取值由平台注入；平台专属分支配专属用例（`//go:build windows`）；判据按**语义集合**写不按单个错误码；获取与释放两条路径一起看 | `crossbuild`（只证编得过）+ CI 的 `windows-test` 作业（真跑）|
| 18 | 「被拒的工具重试一次就放行」 | 以为「拒绝即激活」——**这条已被移除**（提交 `2ac10a4` / issue #286）：保留它会让「猜参数 ⇒ 被拒 ⇒ 原样重试 ⇒ 放行」成为最短路径，模型从未取回 schema，渐进披露省上下文的前提被自身恢复路径绕开 | **schema 未取回 ⇒ 原样重试不会成功**（`ErrNotLoaded` 的模型指引原文）。恢复路径是「先取回 schema，再构造参数调用」；`Catalog.Load` 是披露的**唯一写入口**，检索只排序不激活 | `test`（`tooldisclose/gate_recovery_test.go` 钉住）|
| 19 | 提交用 `git add -A` 卷进 scratch 目录 / 中文文档批量编辑后出现 U+FFFD / macOS 下 `$VAR` 后紧跟中文被吞掉多字节首字节 | 习惯，不是判据 | 提交用 `git add <具体路径>`；编辑后 `grep U+FFFD` 自检；一律写 `${VAR}` | 无门禁 ⇒ 靠纪律 |
| 20 | 接缝契约三形态没写齐（声明 / 调用点 / 三条约束）| 以为「接缝是注释」 | 按 `docs/SEAM-CONTRACT.md` 补声明与三条约束；基线用 `go run ./cmd/seamcheck -update` 落 | `seamcheck` |
| 21 | `harness-test-defer-cleanup` 报红：收尾等待点写在普通语句里 | 用例体末尾（或中途）写 `stopTailer(...)` / `<-done` 这类**普通语句**当收尾点 ⇒ 用例中途 `t.Fatalf` 触发的 `runtime.Goexit` **会跳过它**（Goexit 只执行 defer）⇒ goroutine 活过用例结束 | 把收尾点写进 `defer`（`defer func(){ <-done }()` 或 `defer` 收尾助手）；**中途**收尾别一刀切 defer（会把 join 拖到用例结束 ⇒ 语义改变）⇒ 圈成闭包、defer 写闭包里 | `architecture-check`（`harness-test-defer-cleanup`）|
| 22 | `harness-test-wallclock-ruler` 报红：拿裸 `time.Sleep` 当尺子 | 睡一段再断言计数 ⇒ 窗口长度单独决定结论：机器一忙就**假红**，机制抢跑就**白测**（什么都没验却绿）| 改成「等可观测事实」（计数器 / 通道 / 标志 / 状态）+ 明确的截止时间；**轮询间隔不是尺子**；确需在辅助里等事实的，在 policy 的 `managed_helpers:` 登记该辅助 | `architecture-check`（`harness-test-wallclock-ruler`）|

| 23 | `harness-policy-self-check` 报「僵尸例外」：例外命中不到任何违反 | 例外登记后**没人回头看它还命中不命中**：站点修好 / 文件拆分之后它照常躺在声明面里计数，**已失效的豁免被当成在用**（#447 实录：6 条例外在文件拆分后全部失效而无人知晓）| 删掉它；确实还要留着的标 `status: stale`（**已失效待删**）。标了 `stale` 却**仍命中**违反也会红——否则标记就成了「写一次永久免检」的免死金牌 | `architecture-check`（`harness-policy-self-check` 的例外对账）|
| 24 | `harness-test-no-direct-mutator` 报红：测试文件直调包级可变入口 | 用例为了「直接驱动策略」绕过受管 helper 去调 setter / `updatePolicy` ⇒ 每个用例自己还原全局态、时机各写各的 ⇒ `-count>1` 概率串扰（#442 实录 19/20）| 改走受管 helper；确需在某函数里直写的，把该函数登记进 `managed_helpers:`（按**函数名**结构性放行——行号例外改几行就漂）| `architecture-check`（`harness-test-no-direct-mutator`）|
| 25 | `ledgercheck` 报「单号 #N：台账登记为已合入，而 issue 状态快照是 open」（或反向）| 台账与 GitHub 各说各话，而**两侧都没有判据**：`ledgercheck` 此前只判台账内部一致性（#455 实录：台账把三条记成「已合入」、代码也核实落地，GitHub 上三条全开着）| 先判断**哪一侧是真的**：issue 没关就关掉它；台账没跟着翻面就把状态列补成「✅ 已合入 (#N)」。快照过期先跑 `make issue-state` 再判 | `ledgercheck`（双向对账；判据离线，只读 `docs/issue-state.yaml`）|

### 5.2 六条判据通则（跨模式复用）

1. **判据的量纲要跟判据的前提对齐。** 量纲与前提错位 ⇒ 报错把正确的工作判成错的
   （模式 13、17）。
2. **口径单源。** 同一件事只允许一处定义；需要多处用时，让它们都是那一处的**投影**
   （模式 8、10）。
3. **零值必须逐字兼容旧口径。** 新增判据能力时，默认（零值）路径要与旧行为
   **一字不差**（含异常条数与文案），否则每个下游都得重新验一遍既有行为。
4. **声明是「取代」不是「叠加」。** 显式声明只能**加**约束 ⇒ 并行组第二步仍会依赖
   第一步，组退化成链——病没治好。显式声明**取代**隐含约束，代价是作者要列全
   直接前驱（漏列 = 少一条约束），这个取舍要写进文档。
5. **判据取调用点，不取状态。** 「修了没」要看调用点现在写的是什么，不是 issue
   状态、也不是库里有没有那个函数（模式 16）。
6. **平台专属的失败语义要抽成平台无关的判据。** 取值由平台注入 ⇒ 判据在任何机器上
   被真跑，本地就能验、修完也能证（模式 17）。

---

## 6. 反向验证（改门禁必做）

**纪律原文**：

> 门禁的失败形态是**变绿**，不是变红。改任何门禁判定逻辑，必须同时给
> 「坏实现会红」的用例——只测「好实现能过」的用例，一个把数字比较写成字符串比较的
> 错实现照样全过。

### 6.1 怎么做

```bash
# 1. 注入违规（临时改坏实现）
# 2. 跑门禁，必须红
make architecture-check; echo "退出码: $?"     # 期望：报错 + 退出码 1
# 3. 撤销注入
```

对**每条**新增/修改的判据都要做一遍。判定标准：**注入 → 红 → 撤销 → 绿**，
且红的**位置**正确（红在该红的地方，不是红在相邻的偶然原因上）。

### 6.2 三个必须分开看的失败形态

| 形态 | 症状 | 含义 |
|---|---|---|
| **假红** | 用例红了，但红在错的点 | 断言选错了区分特征 |
| **假绿** | 用例没红，但根本没执行到 | 共享失败点 / 提前 return，**更隐蔽**——它连「红」的机会都没有 |
| **用例没红 ≠ 用例钉住了** | 绿了 | 变异验证时每个子用例都要**单独**看过「红/绿」；绿的要问「它在旧实现下也绿吗」 |

**表驱动用例的写法**（避免假绿的关键）：

```go
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {   // ← 逐条独立报告
        ...
    })
}
```

**不要**用一张表 + 一个共享 `t.Fatalf`——那样只有第一条会跑，
后面的形态**从未被执行过**，却让用例看起来钉住了全部形态。

### 6.3 parity 测试要比「归因」，不只是「结论」

「静态预检的通过/拒绝结论」与「启动时的拒载结论」一致，**不代表两者说的是同一件事**——
两边都拒、但归因不同（通用层报通用文案 vs 专门层报专门文案）时，
运维照预检的归因去改会改错地方。

**判据**：断言要选**区分性最强**的那个特征；用例必须包含
「静态层说 OK、启动说拒」或反之的形态，否则分叉只藏在错误文案里，测试看不见。

---

## 7. 命令速查

```bash
# ── 门禁 ──────────────────────────────────
make check                          # 全套（按顺序，红即停）
make architecture-check             # 架构门禁
make architecture-check --report    # 看全量违规清单（默认每条截断到 10 条）
make architecture-check-types       # 严格档（go/types 类型感知，慢，opt-in，不进 check）
make test                           # 全量单测
make cover                          # 覆盖率报告
make cover-gate                     # 覆盖率阈值门禁（全局 + 逐包）
make fmt-check                      # 只查格式
make vet                            # 只查 vet
make lint                           # 只跑 golangci-lint

# ── CI 的分层目标（本地复跑同一批判据）──
make unit                           # 静态门禁 + 全量单测 + 一串一致性门禁
make race                           # 竞态检测（开 cgo）
make contract                       # 契约套件自测（sidetest + boardtest）
make fault-injection                # 全仓 TestFault*（注入矩阵）
make gates                          # versioncheck + cover-gate + vuln + crossbuild
make integration                    # PG 契约套件 + SIGKILL e2e（要 docker，起不来明确报错）

# ── 单包迭代 ──────────────────────────────
go test ./protocol/ -run TestXxx -count=1
go test ./protocol/ -race -count=1
go build ./...

# ── 门禁工具直用 ───────────────────────────
go run ./cmd/archcheck                      # 默认读仓库根的 architecture-policy.yaml
go run ./cmd/archcheck -policy <path>       # 判另一份 policy（宿主复用）
go run ./cmd/archcheck --report
go run ./cmd/docsync                        # README 包表一致性
go run ./cmd/seamcheck -update              # 接缝基线落盘（改完接缝后）

# ── 依赖与本地并行 ─────────────────────────
go mod tidy                           # 先确认没有 go.work（workspace 期间别跑）
make devlink                          # 接入本地 workspace（不改 go.mod）
make devunlink                        # 断开

# ── 宿主侧复用本仓门禁（示例）─────────────
# import "github.com/yi-nology/goagent-kernel/archcheck"
# root, err := archcheck.FindRoot(".")                                  # 向上找 policy
# pol, err := archcheck.Load(filepath.Join(root, "architecture-policy.yaml"))
# res, err := archcheck.Run(root, pol)
# archcheck.WriteReport(os.Stderr, res, archcheck.ReportOptions{Full: false})
# if res.HasBlocking() { os.Exit(1) }
```

> `archcheck.PolicyPath(root, pol)` **不是加载路径**——它返回报告里显示用的相对路径
> （缺省即 `"architecture-policy.yaml"`）。拿它去 `Load` 会读到错的地方。

**门禁执行体是包不是脚本**（`archcheck`）——宿主仓要复用同一套判据时
**import 它**，不要复制一份实现（各写一遍必然各判一套，漂移到没人发现为止）。

---

## 8. 不确定时怎么办

```
「这东西该放本仓还是宿主？」
  └─ 问：这个包需要知道自己在给谁干活吗？需要 → 宿主

「门禁红了，是代码错了还是规则该改？」
  └─ 先看规则想拦的失效形态写得出来吗
       写得出来 + 代码确实错了 → 改代码
       写得出来 + 代码没错    → 规则过时 → 先改 policy，再改代码，commit 写清理由
       写不出来               → 这条规则是仪式，考虑删掉

「加个功能，从哪开始？」
  └─ 先改 policy（modules / scan_roots），再写代码，最后同步 README 包表

「改完了怎么知道对？」
  └─ make check 全绿 + 反向验证（注入违规会红）
```

---

## 附：相关文档

| 文档 | 回答什么 | 性质 |
|---|---|---|
| [README](../README.md) | 是什么、为什么这么设计、边界纪律、下游升级须知 | 活文档 |
| [AGENTS.md](../AGENTS.md) | 在本仓干活的工作纪律（含每条纪律的事故来源） | 活文档 |
| [docs/USAGE.md](USAGE.md) | **宿主开发者**：怎么把 harness 接进自己产品 | 活文档 |
| [docs/OAGENTKIT-OVERLAP.md](OAGENTKIT-OVERLAP.md) | 本仓与上游 `oagentkit` 的能力边界 | 活文档 |
| [docs/SEAM-CONTRACT.md](SEAM-CONTRACT.md) | 接缝契约三形态与写法 | 活文档 |
| [docs/api-compat.md](api-compat.md) | 什么算 breaking、五步破坏性变更流程 | 活文档 |
| [docs/error-semantics.md](error-semantics.md) | 错误哨兵登记表 | 活文档 |
| [docs/STORAGE-SELECTION.yaml](STORAGE-SELECTION.yaml) | 存储选型声明面（`storagecheck` 判）| 活文档（机器判定）|
| [docs/DEPLOY-MATRIX.yaml](DEPLOY-MATRIX.yaml) | 部署配方能力清单（`deploycheck` 判）| 活文档（机器判定）|
| [docs/SANDBOX-DESIGN-20261021.md](SANDBOX-DESIGN-20261021.md) | 执行沙箱的下一层：跨域执行与资源兜底（设计推导 + 与上游 guardx 的分工） | 活文档 |
| [docs/CHECKPOINT.md](CHECKPOINT.md) | 续跑契约的细节（设计推导 + 跨进程闸） | 活文档 |
| [architecture-policy.yaml](../architecture-policy.yaml) | 门禁声明面（模块拓扑 / 规则 / 阈值 / 词表） | 活文档（机器判定）|
| [api-lifecycle.yaml](../api-lifecycle.yaml) | API 生命周期定级（`apilifecheck` 判）| 活文档（机器判定）|
| [CHANGELOG.md](../CHANGELOG.md) | 版本历史与破坏性变更 | 活文档（按 tag 切段生成）|
| [docs/DISTRIBUTED-EVOLUTION-20261117.md](DISTRIBUTED-EVOLUTION-20261117.md) | 演进台账（`ledgercheck` 判）| 活文档（机器判定）|
| [docs/SPLIT_PLAN.md](SPLIT_PLAN.md) | 从宿主仓拆分的分层依据 | **历史快照**（2026-10-17 写于 10 个包时）|
| [docs/MIGRATION_OAGENTKIT_20261018.md](MIGRATION_OAGENTKIT_20261018.md) | 门面迁移与 errors 链路改写的执行记录 | **历史快照**（2026-10-18 已执行完毕）|
| [docs/SELF-AUDIT-20261018.md](SELF-AUDIT-20261018.md) | 独立复核发现并修复的缺陷 | **历史快照**（2026-10-18 收尾）|

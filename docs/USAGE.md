# goagent-kernel 使用指南

> 这份文档写给**要把 harness 接进自己产品的宿主开发者**。
>
> [README](../README.md) 回答「这是什么、为什么这么设计、边界纪律是什么」；
> 本文回答另一个问题——**「我怎么接、接错了会怎样」**。
>
> 本文所有 API 签名与行为均照抄源码，行号对应写稿时的 HEAD。
> 代码块是**集成片段**（用于说明装配顺序与调用面），不是可直接 `go run` 的完整程序；
> 完整可编译示例需要你在宿主工程里补齐存储、HTTP、模型客户端等上下文。

---

## 怎么读这份文档

**按需跳读，不必全接。** 本文覆盖 25 个能力包，分 10 组彼此独立的能力，典型接入顺序如下：

```
① 契约面        contract + statemachine    ← 你的状态长什么样（必接）
② 工具判定环    tooldisclose + guardset + 上游 toolfail ← 工具怎么被管住（必接）
③ 报告协议      protocol                   ← 环节产出怎么变成结构化报告（必接）
④ 续跑契约      checkpoint + commitgate    ← 崩了怎么接着跑（强烈建议）
⑤ 上下文三件套  budget + spill + offload   ← 上下文怎么不被撑爆（建议）
⑥ 实时事件      eventbus + watchdog        ← 进度怎么给前端（可选）
⑦ 流式与退出    delta + drain + pending    ← 流式输出与优雅停机（可选）
⑧ 安全面        netguard + pii             ← 出站与个人信息（按需）
⑨ 程序化编排    ptc                        ← 多工具聚合场景（进阶）
⑩ 判定与改派    dispatch + roundguard + 上游 toolfail / policy / sandbox / injectband ← 失败与循环怎么判（上游四件的核心在 oagentkit，宿主直连上游）
```

每节的结构一致：**职责 → 最小集成片段 → 关键符号 → 坑 → 什么时候不该用**。
最后一节 [§6 坑速查表](#6-坑速查表) 是全文浓缩，接线完成后拿它自查一遍。

> **四个口径，各说一件事**（别混）：
>
> | 口径 | 数字 | 是什么 |
> |---|---|---|
> | 本文覆盖 | 25 个能力包 | 上面 10 组里逐个讲过的机制包——**文档取舍**，由本文决定 |
> | 仓里有什么 | **53** 个包 | README 包表口径（机器校验，`make docsync` 判）|
> | policy 登记 | **61** 个 module id | `architecture-policy.yaml` 的 `modules` 段（机器校验，`make docsync` 判）|
> | 生命周期登记 | **62** 个包 | `api-lifecycle.yaml` 的 `packages` 段（机器校验，`make apilifecheck` 判）|
>
> 后三个数是**仓库事实**（机器判，漂了即红）；第一个数是**文档取舍**
> （机器判不了「该不该讲某个包」，故只由本文负责）。
>
> **包数口径（机器校验，`make docsync` 判）**：本仓共 **53** 个包（README 包表口径）；
> `architecture-policy.yaml` 的 `modules` 段登记 **61** 个 module id（61 数的是
> `modules` 段的 `- id:` 条目；这些 id 覆盖的 root 目录共 **63** 个——`taskboard`
> 与 `pgrefstore` 各有两个 root。**两个数不是同一种量**：61 数 module id，63 数
> root 目录）。
>
> 三个数不一样是对的：本文没讲的那些包里，有门禁工具（`archcheck`）、
> CLI 装配层（`cmd/*`）、判定所需的单一真相包（`toolname`）、契约测试套件
> （`sidetest`）、子包（`taskboard/boardtest`）与参考示例
> （`examples/reference-runtime`）——它们不是「能力包」，但都在仓里、都在门禁下。

---

## 0. 五分钟速览

### 0.1 定位

**编排是代码，LLM 只做单环节。**

harness 提供的是确定性编排的**机制面**：环节状态怎么流转、工具调用怎么被判定、
产出怎么被结构化、崩了怎么续跑、上下文怎么计量。它**不认识任何领域概念**——
专家包、诊断链、运维词表、拓扑内容、品牌文案全部属于宿主。

判定口诀：**「这个包需要知道自己在给谁干活吗？」需要 → 属于宿主，不属于本仓。**

### 0.2 你会拿到什么

| 你要解决的事 | 用哪个包 | 你要提供什么 |
|---|---|---|
| 会话/环节状态存哪、怎么流转 | `contract` + `statemachine` | `RunContext` / `StepSink` 两个接口的实现 |
| 模型输出怎么变成可信结构 | `protocol` | agent 名注册（可选）、报告标题（可选） |
| 工具怎么被限制 | `tooldisclose` + `guardset` + 上游 `toolgate/policy` | 工具目录、宿主守卫 |
| 崩了怎么接着跑 | `checkpoint` + `commitgate` | `StoreCAS` 实现、终态词表注入 |
| 上下文怎么不被撑爆 | `budget` + `spill` + `offload` | `Summarizer` / `Archive` / `Store` 三个接口 |
| 进度怎么推给前端 | `eventbus` + `watchdog` | 落库 + 广播、告警发布出口 |
| 出站请求怎么防 SSRF | `netguard` | 挂在自己的 `DialContext` 上 |
| 个人信息怎么脱敏 | `pii` | 阻断决策（内核只回报信号） |
| 多工具聚合怎么省往返 | `ptc` | 工具绑定、判定环切片 |
| 失败后还能不能派、派给谁 | `dispatch` | 候选枚举接缝、执行步接缝（改派面在宿主侧）|
| 执行的隔离强度怎么声明与保证 | 上游 `guardx/sandbox` | 执行后端（可选）、每次执行的档位要求 |
| 副作用怎么保证只做一次 | `sideeffect` + `retryplan` | `Store` 实现（跨进程原子）、幂等键、下游拒收契约 |
| 我的存储实现守没守住契约 | `sidetest` | 让实现支持注入时钟，然后跑一次套件 |
| 模型侧循环还能不能转 / 是不是在空转 | `roundguard` | 回合事实（结局 / 用量 / 指纹）——模型与工具仍归宿主 |

### 0.3 三个必须先知道的约束

**① 内核只判定，不处置。**
判定语义归内核，判定依据与处置归宿主。典型表现：`pii.Apply` 检出敏感信息时返回
`(文本, true)`——`true` 表示「宿主应当阻断」，但**阻断动作由你执行**，内核不抛错、
不中断你的控制流。上游 `toolfail` 同理：分类完了，怎么恢复是你的事。

**② 本仓的门包已清零，判定面在上游。**
v0.35.0 起本仓**不再提供** `policy` / `reminder` / `toolfail` / `sandbox` 四个门面包
（`architecture-policy.yaml` 的 `facades` 段是**空清单**），宿主**直连上游**：
`github.com/oagentkit/toolgate/policy`、`github.com/oagentkit/guardx/toolfail`、
`github.com/oagentkit/guardx/sandbox`、`github.com/oagentkit/guardx/injectband`。
门包纪律仍在（`harness-facade-stays-facade`，error 级）：本仓若再设门包，只允许四种
声明（类型别名 / 常量别名 / 单条上游转发函数 / 允许集合内的 import），加真逻辑当天即红。

**③ 本仓不 import 你，你 import 本仓。**
方向是单向的硬约束（`harness-no-host-dependency` 门禁）。harness 需要宿主能力时
定义接口由宿主实现，从不反过来依赖。

---

### 0.4 模型侧怎么接

内核与模型的分界线只有一条：**模型负责「说」，内核负责「判」与「记」。**

| 归谁 | 做什么 |
|---|---|
| 模型 | 产出报告 JSON、决定调哪个工具、给环节终答、面对拒绝后自我修正 |
| 内核 | 判定这次调用要不要放行、这条结论可不可信、上下文超没超、恢复从哪里接着跑 |
| 宿主 | 提供模型客户端与工具实现；**在**内核判定之后**执行**或**拒绝** |

**模型不负责、也不该负责的事**：守卫是否放行（`tooldisclose` / `guardset` / 上游 `policy`）、
失败该不该重试（上游 `toolfail` / `retryplan`）、恢复计划怎么算（`checkpoint`）、
上下文怎么压（`budget`）、副作用是不是已经做过（`sideeffect`）。
模型**只消费结论**：`invoke.Verdict.Reason`（留痕版）与 `guardset.Outcome.Guidance()`（模型版）、
`toolfail.Failure.Guidance()`、`spill.TruncHint`、`offload.Render` 的占位与取回指引。

⇒ 模型侧要做的只有两件事：**按指引改**（指引里写明了该改什么、能不能靠重试解决）
与 **把结论写进产物**（报告的 `steps` / `verdicts` / `evidence`）。

模型侧的完整接法（提示词怎么写、输出怎么约束、被拒后怎么自我修正）见
[模型侧接入指南](https://yi-nology.github.io/goagent-kernel/guide/llm-guide.html)。

---

## 1. 引入

```bash
# 模块路径
module github.com/yi-nology/goagent-kernel
```

```bash
# 按最新发布版引入
go get github.com/yi-nology/goagent-kernel@latest

# 或钉到具体版本（查已发布版本：git ls-remote --tags origin）
go get github.com/yi-nology/goagent-kernel@latest
```

> **版本号为什么写 `@latest` 而不是钉死一个数字**：本文写稿时是 v0.62.0，
> 而它会随发版变旧。钉死的数字在几轮发版后就是**过期指引**——
> 按它引入会拿到一个缺了好几轮能力的旧版本，且没有报错。
> `go get @latest` 不会过期；要复现特定版本再显式钉。

**零 `replace`。** 本仓 go.mod 不得含 `replace`（`make check` 的 `modreplace` 门禁）。
本地需要同时改上游仓时用 `make devlink` 走 `go.work`，见 [§5](#5-本地多仓并行开发)。

依赖全部为 public 模块：`github.com/oagentkit/*` 与 `github.com/oekit/*`。
**按包引入**即可——`contract` 只依赖 `oekit/pkg`（哨兵），`statemachine` 是零外部依赖的叶子包；
只想拿状态机不需要拖进 goja、jsonschema 这些。

```go
import "github.com/yi-nology/goagent-kernel/contract"
import "github.com/yi-nology/goagent-kernel/statemachine"
```

---

## 2. 心智模型：为什么接进来是这些形状

理解三条设计取舍，后面的 API 形状就都不奇怪了。

### 2.1 编排语义定死，领域字段开放

`contract.RunContext` 是**混合**设计：编排语义字段（`ID` / `Status` / `CurrentStep`）
定死在接口上，因为编排引擎的算法只依赖这些；领域状态走开放键
（`Field(key)` / `SetField(key, value)`），内核不解释、不校验、不假设它存在。

这样约 80% 的编排逻辑可下沉，剩下 20% 留在宿主适配层。
纯开放键契约太松、两边必然漂移；纯定死则每加一个领域概念都要改内核。

> 取不到的键返回 `(false)` 由调用方降级；`SetField` 遇未知键**返回错误**而不是静默吞——
> 宿主可以 fail-closed 拒绝，但不能悄悄把数据丢了。

### 2.2 续跑而非重放

「编排是代码」的必然推论是编排过程可被重演。既然可重演，恢复方式就该是**可机械判定**
的，而不是留给宿主在崩溃那一刻临场决定——临场决定必然选最坏的那个
（要么盲目全量重跑、要么盲目跳过）。

所以 `checkpoint.BuildPlan` 是纯函数，把恢复判定做成内核机制。
详见 [§3.6](#36-断点续跑checkpoint--commitgate)。

### 2.3 内部一致优于外部通用

同一件事的多个维度分列而非合并，因为合并会丢信息：

- 上游 `toolfail`：**重发安全**与**重试价值**是两个正交维度——超时值得重试，但服务端可能
  已执行，原样重发就是重复施加副作用。
- `budget`：**毛移除量**（扣摘要之前的量）与**净移除量**（扣掉摘要回填后的量）分开。
- `guardset`：**多条拒绝合成一条**时，可重试取「全部可重试才算」，不是「任一」。

---

## 3. 接入路线图

<div align="center">
<svg width="760" viewBox="0 0 760 250" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="250" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="28" y="30" font-size="14" fill="#e6edf3">接入全景：六段机制的依赖顺序</text>
<text x="28" y="50" font-size="11" fill="#9aa7b8">前一段是后一段的前提——跳过任何一段，右侧的判据就失去依据</text>
<rect x="28" y="70" width="104" height="94" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="80" y="94" font-size="11" fill="#e6edf3" text-anchor="middle">① 契约面</text>
<text x="80" y="114" font-size="8" fill="#58a6ff" text-anchor="middle">contract</text>
<text x="80" y="126" font-size="8" fill="#58a6ff" text-anchor="middle">statemachine</text>
<line x1="38" y1="136" x2="122" y2="136" stroke="#262e3d" stroke-width="1"/>
<text x="80" y="150" font-size="7.5" fill="#9aa7b8" text-anchor="middle">宿主实现</text>
<text x="80" y="160" font-size="7.5" fill="#9aa7b8" text-anchor="middle">RunContext+StepSink</text>
<rect x="148" y="70" width="104" height="94" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="200" y="94" font-size="11" fill="#e6edf3" text-anchor="middle">② 报告协议</text>
<text x="200" y="114" font-size="8" fill="#58a6ff" text-anchor="middle">protocol</text>
<text x="200" y="126" font-size="8" fill="#58a6ff" text-anchor="middle">报告 JSON → 报告</text>
<line x1="158" y1="136" x2="242" y2="136" stroke="#262e3d" stroke-width="1"/>
<text x="200" y="150" font-size="7.5" fill="#9aa7b8" text-anchor="middle">宿主喂原始 JSON</text>
<text x="200" y="160" font-size="7.5" fill="#9aa7b8" text-anchor="middle">标题走注入</text>
<rect x="268" y="70" width="104" height="94" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="320" y="94" font-size="11" fill="#e6edf3" text-anchor="middle">③ 判定环</text>
<text x="320" y="114" font-size="8" fill="#58a6ff" text-anchor="middle">tooldisclose</text>
<text x="320" y="126" font-size="8" fill="#58a6ff" text-anchor="middle">guardset</text>
<line x1="278" y1="136" x2="362" y2="136" stroke="#262e3d" stroke-width="1"/>
<text x="320" y="150" font-size="7.5" fill="#9aa7b8" text-anchor="middle">宿主装 Stage 切片</text>
<text x="320" y="160" font-size="7.5" fill="#9aa7b8" text-anchor="middle">工具目录自己登记</text>
<rect x="388" y="70" width="104" height="94" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="440" y="94" font-size="11" fill="#e6edf3" text-anchor="middle">④ 续跑</text>
<text x="440" y="114" font-size="8" fill="#58a6ff" text-anchor="middle">checkpoint</text>
<text x="440" y="126" font-size="8" fill="#58a6ff" text-anchor="middle">commitgate</text>
<line x1="398" y1="136" x2="482" y2="136" stroke="#262e3d" stroke-width="1"/>
<text x="440" y="150" font-size="7.5" fill="#9aa7b8" text-anchor="middle">宿主实现 StoreCAS</text>
<text x="440" y="160" font-size="7.5" fill="#9aa7b8" text-anchor="middle">终态词表自己注入</text>
<rect x="508" y="70" width="104" height="94" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="560" y="94" font-size="11" fill="#e6edf3" text-anchor="middle">⑤ 上下文</text>
<text x="560" y="114" font-size="8" fill="#58a6ff" text-anchor="middle">budget</text>
<text x="560" y="126" font-size="8" fill="#58a6ff" text-anchor="middle">spill · offload</text>
<line x1="518" y1="136" x2="602" y2="136" stroke="#262e3d" stroke-width="1"/>
<text x="560" y="150" font-size="7.5" fill="#9aa7b8" text-anchor="middle">宿主实现三个接口</text>
<text x="560" y="160" font-size="7.5" fill="#9aa7b8" text-anchor="middle">Summarizer 等</text>
<rect x="628" y="70" width="104" height="94" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="680" y="94" font-size="11" fill="#e6edf3" text-anchor="middle">⑥ 程序化编排</text>
<text x="680" y="114" font-size="8" fill="#58a6ff" text-anchor="middle">ptc</text>
<text x="680" y="126" font-size="8" fill="#58a6ff" text-anchor="middle">进程内 goja VM</text>
<line x1="638" y1="136" x2="722" y2="136" stroke="#262e3d" stroke-width="1"/>
<text x="680" y="150" font-size="7.5" fill="#9aa7b8" text-anchor="middle">宿主提供 bindings</text>
<text x="680" y="160" font-size="7.5" fill="#9aa7b8" text-anchor="middle">复用同一套 Stage</text>
<line x1="134" y1="112" x2="142" y2="112" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="145,112 140,108.5 140,115.5" fill="#58a6ff"/>
<line x1="254" y1="112" x2="262" y2="112" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="265,112 260,108.5 260,115.5" fill="#58a6ff"/>
<line x1="374" y1="112" x2="382" y2="112" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="385,112 380,108.5 380,115.5" fill="#58a6ff"/>
<line x1="494" y1="112" x2="502" y2="112" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="505,112 500,108.5 500,115.5" fill="#58a6ff"/>
<line x1="614" y1="112" x2="622" y2="112" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="625,112 620,108.5 620,115.5" fill="#58a6ff"/>
<rect x="28" y="180" width="704" height="54" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="44" y="202" font-size="10.5" fill="#e6edf3">不变量：内核只判定，不处置——判定依据由宿主给，处置动作由宿主执行</text>
<text x="44" y="221" font-size="9" fill="#9aa7b8">旁挂（可后接）：eventbus/watchdog 进度 · delta/drain/pending 流式与退出 · netguard/pii 安全 · dispatch 改派 · roundguard 回合守卫</text>
</svg>
</div>

*图 1：六段机制的依赖顺序。前五段是第六段的前提；旁挂的四组能力可以按需后接，互不依赖。*

### 3.1 契约面：`contract` + `statemachine`

**这一层是你与内核之间的唯一边界。** 编排引擎不碰存储、不碰数据库——
所有环节状态的读写都经 `StepSink` 出口。

> **状态词表只有一个定义处**（issue #354/#357）：`contract` 单点定义会话与
> 环节共用的状态取值，`statemachine` 的 12 个状态常量全部是它的别名
> （标注 Deprecated，新代码请直接用 `contract` 的状态常量，如 `contract.StatusCreated`）。终态判定的公开入口只有
> 一份：`contract.IsTerminalStatus`；`statemachine.IsTerminal` 是环节侧的类型化
> 入口，从转移表派生并与前者对 12 个环节状态逐条一致（跨包用例钉住）。
> 别在宿主侧另抄一份终态清单——两份必然漂移，且今天看不出差别。

```go
package myapp

import (
	"context"

	"github.com/yi-nology/goagent-kernel/contract"
)

// ① 会话运行上下文：编排语义字段定死，领域状态走开放键。
type myRun struct {
	id     string
	caseID string

	status string
	step   string

	fields map[string]string
}

func (r *myRun) ID() string                 { return r.id }
func (r *myRun) CaseID() string             { return r.caseID }
func (r *myRun) Status() string             { return r.status }
func (r *myRun) SetStatus(s string) error   { r.status = s; return nil }
func (r *myRun) CurrentStep() string        { return r.step }
func (r *myRun) SetCurrentStep(s string) error { r.step = s; return nil }
func (r *myRun) Field(k string) (string, bool) {
	v, ok := r.fields[k]
	return v, ok
}
func (r *myRun) SetField(k, v string) error {
	r.fields[k] = v
	return nil
}

// ② 环节推进出口：编排只说「开一个环节 / 收口」，落库由你做。
type myStepSink struct{ repo *Repo }

func (s myStepSink) Open(name, label string) (contract.StepRef, error) {
	// 建行 + 状态迁移到 running，两件事你都要做
	return s.repo.insertStep(name, label)
}

func (s myStepSink) Finish(ref contract.StepRef, status string) error {
	return s.repo.finishStep(ref.ID, status)
}

// ③ 环节状态机：纯谓词件，先快速失败，DB 侧仍有条件 UPDATE 兜底。
func advance(from, to statemachine.StepStatus) error {
	if err := statemachine.MustTransition(from, to); err != nil {
		return err // 非法转移，repo 条件 UPDATE 前快速失败
	}
	// ...
	return nil
}
```

**关键符号**

| 符号 | 说明 |
|---|---|
| `RunContext` | 会话运行上下文；实现须**并发安全**（事件回调与主流程共享同一实例） |
| `StepSink` | 环节推进出口：`Open` / `Finish` |
| `StepRef` | `{ID uint64; Name string}`，宿主分配的稳定标识 |
| `ToolBinder` / `Tools` | 工具面绑定出口——**不关心工具从哪来**（MCP 池 / 静态注册表 / 测试替身同接口） |
| `Clock` | 时钟注入点，测试可注入确定性时钟 |
| `FieldQueuedInput` / `FieldPendingAsk` / `FieldTargetSummary` | 内核**建议**的语义键，不强制 |

**坑**

- **`ToolBinder.Tools` 的 server 级失败必须显式报错**，不得静默返回空面。空面会让
  编排把「没工具」误判成「工具失败」——两者处置完全不同。
- **`statemachine` 的终态包含 `interrupted`**：环节因用户取消/会话超时/进程中断而中止，
  **区别于失败**（执行了但失败）。下一轮对话历史如实携带，LLM 自行决定重做或跳过。
- **`RequiresApproval(TypePlan)` 与 `TypeExecute` 为真**；`readonly` 裁剪审批。
  大小写/空白变体（`"PLAN"` / `" plan "`）先归一，结论与规范形态一致；
  **词表外类别一律要求审批**（fail-closed），宿主可用 `StepType.Valid()`
  区分「本就免审」与「内核不认识」。
- **`MustTransition` 区分「状态不在词表」与「迁移非法」**：词表外 `from`/`to`
  包装 `ErrUnknownStatus`（文案不含「非法状态转移」），词表内但不允许的迁移
  包装 `ErrIllegalTransition`。宿主要按 `errors.Is` 分流排障方向。
- `statemachine` 只依赖 `oekit/pkg/errors`（错误哨兵与判定），无编排/存储依赖。

---

**什么时候不该用**

- **你的编排里没有「环节」概念**（单步任务）——`contract` 的价值在于给多环节一份共同的词表；
  没有多环节就别引入它，也别指望它替你管状态存储（本仓**不碰存储**，落库是你的事）。
- **想把领域字段加进内核契约**——`SetField` 的开放键是给**运行时**数据用的；
  形状上的领域字段属于宿主，进了内核就是边界纪律的破口。

### 3.2 报告协议：`protocol`

模型输出的 JSON 不可信。`protocol` 给的是一条**容错解析 + 白名单校验 + 渲染**
的流水线（解析分**四级成功通道 + 一级兜底**），核心思路是「能修就修，但该拦的拦死」。

<div align="center">
<svg width="760" viewBox="0 0 760 330" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="330" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="16" y="24" font-size="13" fill="#e6edf3">报告解析与校验的分级：能修就修，但该拦的拦死</text>
<rect x="16" y="40" width="380" height="272" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="64" font-size="11.5" fill="#e6edf3">解析：四级成功通道 + 一级兜底</text>
<text x="32" y="84" font-size="8.5" fill="#9aa7b8">前置闸：&gt;2 MiB / 空串 / 无报告键 ⇒ ErrNotReport（不截断）</text>
<rect x="32" y="94" width="348" height="32" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="46" y="114" font-size="9.5" fill="#e6edf3">① 直解：Normalize + Unmarshal</text>
<text x="366" y="114" font-size="8.5" fill="#58a6ff" text-anchor="end">置信原样</text>
<rect x="32" y="130" width="348" height="32" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="46" y="150" font-size="9.5" fill="#e6edf3">② 修复通道：尾逗号 / 坏转义 / 未闭合</text>
<text x="366" y="150" font-size="8.5" fill="#a371f7" text-anchor="end">置信钳 low</text>
<rect x="32" y="166" width="348" height="32" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="46" y="186" font-size="9.5" fill="#e6edf3">③ 提取通道：散文包裹的 JSON 对象</text>
<text x="366" y="186" font-size="8.5" fill="#58a6ff" text-anchor="end">置信原样</text>
<rect x="32" y="202" width="348" height="32" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="46" y="222" font-size="9.5" fill="#e6edf3">④ 提取 + 修复</text>
<text x="366" y="222" font-size="8.5" fill="#a371f7" text-anchor="end">置信钳 low</text>
<rect x="32" y="238" width="348" height="32" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="46" y="258" font-size="9.5" fill="#f85149">⑤ 全不中 ⇒ ErrNotReport（降级读正文）</text>
<text x="32" y="292" font-size="8.5" fill="#9aa7b8">置信 high 只可能出现在直解 / 提取通道</text>
<rect x="412" y="40" width="332" height="272" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="428" y="64" font-size="11.5" fill="#e6edf3">校验：Validate() 只分两级</text>
<rect x="428" y="80" width="300" height="48" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="444" y="100" font-size="9.5" fill="#e6edf3">warn 级：证据 snippet 超长</text>
<text x="444" y="117" font-size="8.5" fill="#9aa7b8">只留痕，不截断（证据保真）</text>
<rect x="428" y="136" width="300" height="48" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="444" y="156" font-size="9.5" fill="#f85149">critical：steps 以 { 或 【 开头</text>
<text x="444" y="173" font-size="8.5" fill="#9aa7b8">执行器会当 shell 跑 ⇒ 整份拦截</text>
<rect x="428" y="192" width="300" height="48" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="444" y="212" font-size="9.5" fill="#f85149">critical：证据引用悬空</text>
<text x="444" y="229" font-size="8.5" fill="#9aa7b8">ErrViolation：整份报告被拦</text>
<rect x="428" y="248" width="300" height="48" rx="8" fill="#1c2330" stroke="#58a6ff"/>
<text x="444" y="268" font-size="9.5" fill="#58a6ff">信任级别分两入口</text>
<text x="444" y="285" font-size="8.5" fill="#9aa7b8">ParseLLMReport 剥离五个引擎字段</text>
</svg>
</div>

*图 4：左边是解析的降级阶梯（修复通道救回的报告置信被钳为 low），右边是校验的两级（warn 留痕 / critical 拦死）。*

```go
package myapp

import (
	"github.com/oekit/pkg/errors"
	"github.com/yi-nology/goagent-kernel/protocol"
)

func toReport(raw string) (*protocol.Report, error) {
	// ① 解析：宿主解析 LLM 产出**必须用 ParseLLMReport**
	r, err := protocol.ParseLLMReport(raw)
	if errors.Is(err, protocol.ErrNotReport) {
		return nil, err // 降级读 markdown 主体，不阻塞聚合
	}
	if err != nil {
		return nil, err
	}

	// ② 白名单校验：返回 warn 列表 + critical 拦截
	warns, err := r.Validate()
	if err != nil {
		// errors.Is(err, protocol.ErrViolation) → 整份报告被拦
		return nil, err
	}
	for _, w := range warns {
		metrics.Counter("report.warn", w) // warn 只留痕，不拦截
	}

	// ③ 渲染：标题走注入，内核只留中性兜底
	view := protocol.RenderViewWith(r, protocol.ViewSummary,
		protocol.RenderOptions{}.WithTitle("你的品牌标题"))
	send(view)

	return r, nil
}
```

**关键符号**

| 符号 | 签名 |
|---|---|
| `ParseLLMReport` | `func ParseLLMReport(raw string) (*Report, error)` |
| `ParseReport` | `func ParseReport(raw string) (*Report, error)` |
| `(*Report).Validate` | `func (r *Report) Validate() ([]string, error)` |
| `Render` / `RenderView` / `RenderViewWith` | 三档渲染，`ViewFull` / `ViewSummary` |
| `RenderOptions` | `{DefaultTitle string}` + `.WithTitle(...)` |
| `SetGuidanceAgentNames` | `func SetGuidanceAgentNames(names ...string)` |
| `PayloadCandidates` / `UnwrapMCPText` | MCP 信封候选提取 |
| `ValidateStructured` | `func ValidateStructured(schemaSrc, envelope string) error` |
| `AssignEvidenceIDs` / `CheckEvidenceRefs` | 证据编号与引用校验 |
| `ErrNotReport` / `ErrViolation` | 两个哨兵，用 `errors.Is` 判 |

**坑**

- **`ParseReport` 与 `ParseLLMReport` 信任级别不同，不是别名。**
  前者**保留**引擎侧字段（`Followups` / `Meta` / `Profile` / `AwaitingUser` / `Guidance`），
  是**落库往返入口**；后者剥离这五个字段。**宿主解析 LLM 产出必须用 `ParseLLMReport`**——
  走 `ParseReport` 等于把呈现权交给模型。
- **解析输入有 2 MiB 上限，超限直接判非报告**（`ErrNotReport`），不做截断。
  这条曾经写成「截断到 2 MiB 再解析」，而截断会让提取通道把「报告对象 + 巨量尾巴」
  里的对象原样捞出并丢弃其余——**3.4 MB 输入实测以 `confidence=high` 解析成功**，
  上限等于没有，且残缺内容未被降置信。已修为 fail-closed，
  由 `protocol.TestOversizeInputRejectedNotTruncated` 反向验证钉住。
- **`Validate` 的 `[]string` 是 warn 级，不拦截**；只有 critical 才 `ErrViolation`。
  snippet 超长**不截断**——证据保真。
- **`steps` 的执行契约是 critical**：以 `{` 或 `【` 开头的步骤条目被整份拦截，
  因为执行器会把每条当 shell 命令跑，中文/JSON 形态会「假成功」（exit=0 但什么都没执行）。
- **内核不认识任何具体 agent 名**。引导声位判定二选一：`Report.Guidance = true`（推荐）
  或装配期 `SetGuidanceAgentNames(...)`。早期版本硬编码过中文哨兵名，宿主改名即静默失效。
- **「证据要点」这个标题字面不可单边改**——前端折叠按它匹配，改字面即破坏折叠。
- **severity 序表刻意不导出**。导出的 map 可被就地改写，
  `SeverityRank["critical"] = 0` 会静默把最高危降级为「未评估」。
  读取一律走 `SeverityRankOf` / `MaxSeverity` / `IsSeverityValid`；
  合法值集合走 `AllSeverities()` / `SeverityRanks()`（返回排序副本）。
  大小写/空白变体先归一（`"Critical"` 与 `"critical"` 同序）；空串=未评估
  （rank 0），非空但词表外=读不懂（`SeverityRankUnknown`，聚合按高于 critical
  保守处理，不会与「未填」同档）。
- **经修复通道救回的报告 `Confidence` 被钳为 low**——下游 `needs_review` 语义据此承接。
- `ValidateStructured` 对「未声明 schema」与「旧 server 无 structuredContent」双写兼容，
  **直接放行**；校验失败是漂移信号，不阻断主流程。

---

**什么时候不该用**

- **你的产出根本不是结构化报告**（纯文本回答）——`protocol` 只认带报告键的 JSON，其余一律判 `ErrNotReport`。
- **你想要一个通用 JSON schema 校验器**——它只认本包那份报告 schema，通用校验请另找。
- **想让引擎侧字段被模型回填后再落库**——那是 `ParseReport` 的路（保留五个引擎字段），
  **不是** `ParseLLMReport`；走错入口等于把呈现权交给模型。

### 3.3 工具判定环：`tooldisclose` + `guardset` + 上游 `toolfail`

这是 harness 里最核心的一组：**任何一次工具调用，都必须过同一套判定环。**

<div align="center">
<svg width="760" viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="300" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="16" y="24" font-size="13" fill="#e6edf3">一次工具调用穿过 invoke 判定环：三条入口汇入一条链</text>
<rect x="16" y="44" width="148" height="64" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="90" y="68" font-size="10.5" fill="#e6edf3" text-anchor="middle">普通工具调用</text>
<text x="90" y="84" font-size="8.5" fill="#9aa7b8" text-anchor="middle">模型直接发起</text>
<text x="90" y="98" font-size="8.5" fill="#58a6ff" text-anchor="middle">host 工具面</text>
<rect x="16" y="132" width="148" height="64" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="90" y="156" font-size="10.5" fill="#e6edf3" text-anchor="middle">ptc 程序内调用</text>
<text x="90" y="172" font-size="8.5" fill="#9aa7b8" text-anchor="middle">程序里 host.&lt;tool&gt;()</text>
<text x="90" y="186" font-size="8.5" fill="#58a6ff" text-anchor="middle">进程内 goja VM</text>
<rect x="16" y="220" width="148" height="64" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="90" y="244" font-size="10.5" fill="#e6edf3" text-anchor="middle">子进程侧调用</text>
<text x="90" y="260" font-size="8.5" fill="#9aa7b8" text-anchor="middle">SubprocessProvider</text>
<text x="90" y="274" font-size="8.5" fill="#58a6ff" text-anchor="middle">判定在父侧做</text>
<line x1="166" y1="76" x2="196" y2="76" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="199,76 194,72.5 194,79.5" fill="#58a6ff"/>
<line x1="166" y1="164" x2="196" y2="164" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="199,164 194,160.5 194,167.5" fill="#58a6ff"/>
<line x1="166" y1="252" x2="196" y2="252" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="199,252 194,248.5 194,255.5" fill="#58a6ff"/>
<rect x="200" y="44" width="196" height="240" rx="10" fill="#161b26" stroke="#a371f7" stroke-width="1.5"/>
<text x="298" y="68" font-size="11.5" fill="#a371f7" text-anchor="middle">invoke.Chain</text>
<text x="298" y="84" font-size="8.5" fill="#9aa7b8" text-anchor="middle">逐环短路，首拒即停</text>
<rect x="212" y="96" width="172" height="44" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="298" y="114" font-size="9.5" fill="#e6edf3" text-anchor="middle">policy.Stage（审批）</text>
<text x="298" y="130" font-size="8" fill="#9aa7b8" text-anchor="middle">上游 toolgate/policy</text>
<rect x="212" y="148" width="172" height="44" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="298" y="166" font-size="9.5" fill="#e6edf3" text-anchor="middle">tooldisclose.Stage（披露）</text>
<text x="298" y="182" font-size="8" fill="#9aa7b8" text-anchor="middle">目录内 + schema 已取回</text>
<rect x="212" y="200" width="172" height="44" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="298" y="218" font-size="9.5" fill="#e6edf3" text-anchor="middle">guardset.Stage（合成）</text>
<text x="298" y="234" font-size="8" fill="#9aa7b8" text-anchor="middle">多条拒绝合成一条</text>
<text x="298" y="266" font-size="8" fill="#9aa7b8" text-anchor="middle">顺序即语义</text>
<line x1="398" y1="112" x2="436" y2="112" stroke="#3fb950" stroke-width="1.5"/>
<polygon points="439,112 434,108.5 434,115.5" fill="#3fb950"/>
<line x1="398" y1="224" x2="436" y2="224" stroke="#f85149" stroke-width="1.5"/>
<polygon points="439,224 434,220.5 434,227.5" fill="#f85149"/>
<rect x="440" y="64" width="304" height="96" rx="8" fill="#161b26" stroke="#3fb950"/>
<text x="456" y="88" font-size="11" fill="#3fb950">放行</text>
<text x="456" y="108" font-size="9.5" fill="#e6edf3">宿主执行工具 → 结果回灌模型</text>
<text x="456" y="126" font-size="9" fill="#9aa7b8">判定环不执行、不解释工具语义</text>
<text x="456" y="144" font-size="9" fill="#9aa7b8">执行与拒绝都在宿主这一侧</text>
<rect x="440" y="176" width="304" height="96" rx="8" fill="#161b26" stroke="#f85149"/>
<text x="456" y="200" font-size="11" fill="#f85149">拒绝</text>
<text x="456" y="220" font-size="9.5" fill="#e6edf3">Reason（留痕）/ Guidance（模型）</text>
<text x="456" y="238" font-size="9" fill="#9aa7b8">拒绝走 (Verdict, nil)，不是 error</text>
<text x="456" y="256" font-size="9" fill="#9aa7b8">模型只消费结论，不自己判</text>
</svg>
</div>

*图 2：三条入口（普通调用 / ptc 程序内调用 / 子进程侧调用）汇入同一条 invoke 链。判定只装一次，处处生效。*

#### 3.3.1 为什么不是「一条链逐个过」

`invoke.Chain` 的语义是排成一队逐个过、第一个拒就收口——顺序即语义，
但**后面的守卫永远没机会说话**。模型一次只听得到第一个理由，于是它改掉那个理由、
重试、再撞上下一个，如此往复若干轮。

`guardset` 补的正是这一面：**一组相互独立的判定一并求值，多条拒绝合成一条**。
二者叠加而非取代——`guardset.Set` 把自己接成 `invoke.Chain` 上的**一环**，
链照旧短路，只是这一环内部已经把该说的都说了。

<div align="center">
<svg width="760" viewBox="0 0 760 300" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="300" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="16" y="22" font-size="13" fill="#e6edf3">两种组装形态：拒绝怎么送到模型面前</text>
<rect x="16" y="36" width="352" height="248" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="60" font-size="11.5" fill="#f85149">invoke.Chain：逐个过，首拒即收口</text>
<rect x="32" y="80" width="96" height="52" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="80" y="102" font-size="9.5" fill="#e6edf3" text-anchor="middle">配额守卫</text>
<text x="80" y="120" font-size="9" fill="#f85149" text-anchor="middle">拒绝</text>
<rect x="132" y="80" width="96" height="52" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="180" y="102" font-size="9.5" fill="#9aa7b8" text-anchor="middle">审计守卫</text>
<text x="180" y="120" font-size="9" fill="#9aa7b8" text-anchor="middle">灰掉</text>
<rect x="232" y="80" width="96" height="52" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="280" y="102" font-size="9.5" fill="#9aa7b8" text-anchor="middle">披露守卫</text>
<text x="280" y="120" font-size="9" fill="#9aa7b8" text-anchor="middle">灰掉</text>
<line x1="80" y1="134" x2="80" y2="158" stroke="#f85149" stroke-width="1.5"/>
<polygon points="80,161 76,155 84,155" fill="#f85149"/>
<rect x="32" y="162" width="296" height="48" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="48" y="182" font-size="9.5" fill="#e6edf3">模型只听到第 1 条理由</text>
<text x="48" y="198" font-size="8.5" fill="#9aa7b8">改掉它 → 重试 → 再撞下一条</text>
<text x="32" y="234" font-size="8.5" fill="#9aa7b8">后面的守卫永远没机会说话</text>
<text x="32" y="252" font-size="8.5" fill="#9aa7b8">顺序即语义：换顺序就换结论</text>
<text x="32" y="270" font-size="8.5" fill="#9aa7b8">需要严格先后时，仍然该直接排链</text>
<rect x="392" y="36" width="352" height="248" rx="10" fill="#161b26" stroke="#3fb950" stroke-width="1.5"/>
<text x="408" y="60" font-size="11.5" fill="#3fb950">guardset.Set：一并求值，合成一条</text>
<rect x="408" y="80" width="96" height="52" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="456" y="102" font-size="9.5" fill="#e6edf3" text-anchor="middle">配额守卫</text>
<text x="456" y="120" font-size="9" fill="#f85149" text-anchor="middle">拒绝</text>
<rect x="508" y="80" width="96" height="52" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="556" y="102" font-size="9.5" fill="#e6edf3" text-anchor="middle">审计守卫</text>
<text x="556" y="120" font-size="9" fill="#f85149" text-anchor="middle">拒绝</text>
<rect x="608" y="80" width="96" height="52" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="656" y="102" font-size="9.5" fill="#e6edf3" text-anchor="middle">披露守卫</text>
<text x="656" y="120" font-size="9" fill="#3fb950" text-anchor="middle">放行</text>
<line x1="456" y1="134" x2="456" y2="158" stroke="#f85149" stroke-width="1.5"/>
<polygon points="456,161 452,155 460,155" fill="#f85149"/>
<line x1="556" y1="134" x2="556" y2="158" stroke="#f85149" stroke-width="1.5"/>
<polygon points="556,161 552,155 560,155" fill="#f85149"/>
<line x1="656" y1="134" x2="656" y2="158" stroke="#3fb950" stroke-width="1.5"/>
<polygon points="656,161 652,155 660,155" fill="#3fb950"/>
<rect x="408" y="162" width="296" height="80" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="424" y="184" font-size="9.5" fill="#e6edf3">合成拒绝 Outcome.Guidance()</text>
<text x="424" y="202" font-size="8.5" fill="#9aa7b8">该调用未通过 2 项检查：1) … 2) …</text>
<text x="424" y="218" font-size="8.5" fill="#9aa7b8">其中第 1 项无法通过重试解决，请优先处理它。</text>
<text x="424" y="234" font-size="8.5" fill="#9aa7b8">全部可重试才算可重试（保守侧）</text>
<text x="408" y="264" font-size="8.5" fill="#9aa7b8">Outcome.Reason() 是给审计的一行版</text>
<text x="408" y="278" font-size="8.5" fill="#9aa7b8">守卫 panic / nil / 无理由 ⇒ 不可重试的拒绝</text>
</svg>
</div>

*图 3：左＝链式逐个过，模型一轮只拿到一个理由；右＝一并求值后合成一条，并指出哪一项重试也解决不了。*

#### 3.3.2 渐进式披露：`tooldisclose`

首轮只给「名字 + 描述」的**退化占位**，模型用关键词检索换完整 schema。
披露门与检索同包——因为渐进披露制造了「模型凭占位猜出一个调用」这一新失败形态，
必须在同一处判。

```go
package myapp

import (
	"github.com/oagentkit/guardx/invoke"
	"github.com/oekit/pkg/errors"
	"github.com/yi-nology/goagent-kernel/guardset"
	"github.com/yi-nology/goagent-kernel/tooldisclose"
)

func buildGate() (invoke.Stage, *tooldisclose.Catalog) {
	// ① 目录：宿主登记全部工具。跨轮持有 Catalog 是宿主的责任
	//    （本包没有隐式请求作用域），按会话键持有、会话结束释放。
	cat := tooldisclose.NewCatalog([]tooldisclose.Tool{
		{
			Name:        "get_logs",
			Description: "读取目标主机近期日志",
			Schema: tooldisclose.Schema{
				Type: "object",
				Properties: map[string]tooldisclose.Param{
					"host":  {Type: "string", Description: "目标主机"},
					"count": {Type: "integer", Description: "返回条数"},
				},
				Required: []string{"host"},
			},
		},
	})

	// ② 门禁：目录与门禁是两件事——Gate.Check 只判「能不能调」，
	//    它**不产生任何副作用**（不会顺手把工具激活成已加载）。
	gate := tooldisclose.NewGate(cat)

	// ③ 守卫集：披露门 + 宿主自己的守卫，一并求值
	set, err := guardset.New(
		guardset.Guard{
			Name: "tool_disclosure",
			Rank: 10,
			Check: func(ctx context.Context, c invoke.Call) invoke.Verdict {
				if e := gate.Check(c.Name); e != nil {
					return invoke.Verdict{
						Allow:     false,
						Reason:    tooldisclose.Guidance(e), // 给模型看，要能照着做
						Retryable: tooldisclose.Retryable(e),
					}
				}
				return invoke.Verdict{Allow: true}
			},
		},
		guardset.Guard{
			Name:  "quota",
			Rank:  20,
			Check: quotaCheck, // 你自己的配额守卫
		},
	)
	if err != nil {
		panic(err) // 空集合 / 空名 / 重名都在这里失败
	}

	// ④ 单一 Stage 出口。两个变体的差别只在 Reason 的用途：
	//    Stage            → Reason = out.Reason()  （给人看，要全且准）
	//    StageWithGuidance→ Reason = out.Guidance()（给模型看，要能照着做）
	return guardset.StageWithGuidance(set), cat
}
```

**判定环的严格二分**——`Gate.Check` 三种结局互不代偿：

| 情形 | 结局 | 可重试 | 恢复动作 |
|---|---|---|---|
| 已加载（`Catalog.Load` 已交付 schema） | `nil` | — | — |
| 在目录内但未加载 | `*GateError`（`ErrNotLoaded`） | ✅ | **先 `Catalog.Load` 取回 schema**，再按 schema 构造参数调用 |
| 不在目录内 | `*GateError`（`ErrNotInCatalog`） | ❌ | 就地终止，改用目录内的其他工具 |

⚠️ **门禁不产生副作用**（issue #286）：`Gate.Check` **从不**调用 `Catalog.Load`，
被拒的尝试**不会**把工具变成已加载。`GateError.Activated` 字段保留只为兼容既有审计面，
**恒为 false**。

⇒ **恢复路径只有一条**：先取回 schema（`Catalog.Load`），再按 schema 构造参数调用。
模型指引原文把这条钉死了——「schema 未取回前，**原样重试不会成功**」
（`tooldisclose/gate.go` 的 `GateError.Guidance`）。**「原样重试不会成功」成立，
「同参重试就过」不成立**——后者是 issue #286 之前的旧语义。

两条指引的措辞也是契约的一部分：**未加载**一路**不得**出现「原样重试 / 以相同参数重新调用」；
**不在目录**一路是「请不要重试，改用目录内的其他工具」。

**关键符号**

| 符号 | 签名 |
|---|---|
| `NewCatalog` / `NewCatalogWithLimit` | `func NewCatalog(tools []Tool) *Catalog` |
| `Catalog.Load` / `ExposeAll` / `Search` | `Load` 是披露的唯一写入口 |
| `NewGate` / `Gate.Check` | 三种结局见上表 |
| `Retryable` / `Guidance` | `func Retryable(err error) bool` / `func Guidance(err error) string` |
| `Stage` / `StageName` | `func Stage(cat *Catalog) invoke.Stage` / `const StageName = "tool_disclosure"` |
| `SignatureOf` / `ErrSchemaNotDerivable` | 从 schema 派生调用形态，如 `get_logs(host, count?)`（函数名是**净化后**的，见下） |
| `guardset.New` / `Set.Evaluate` / `Outcome.Guidance` | 一并求值 + 合成拒绝 |

**坑**

- **目录成员构造后不变**，没有删除/新增入口。要增删工具必须重建 `Catalog`。
- **重名静默去重，first wins**；要发现冲突得自己在调用本包前去重。空名工具构造期被丢弃。
- **描述按 rune 截到 320 字符，在存进目录前截断**——不是渲染时截，
  否则检索打分读全文、模型看摘要。
- **`Search` 无命中返回空，不兜底给「随便几个」；且只排序不激活**——
  检索顺手激活会把刚省下的上下文吐回去。
- **`SignatureOf` 派生不出来就报错，不硬凑**。退化占位 schema（Properties 为 nil）、
  参数名非合法 JS 标识符、`Required` 点名了不存在的参数，三种都返回 `ErrSchemaNotDerivable`。
  宁可装配失败，也不让「看起来能用、实际不对」的东西进模型面前。
- **`SignatureOf` 里的函数名是净化后的名字，不是目录里的原名**。
  程序里真正挂上去的是 `host.<净化名>`（`get-logs` 挂上去叫 `host.get_logs`），
  所以形态串里的函数名也必须是净化后的那个，否则 ptc 装配期直接拒
  （`binding_signature`）。两侧共用 `toolname` 包（`Normalize`/`IsJSIdent`），
  规则只有一份，不会漂移。**别自己手写形态串**——手写就要照净化后的名字写。
- **`Stage(nil)` 按「不在目录内」处理**（fail-closed）。
- **`guardset` 合成取保守侧：全部可重试才算可重试**。只要有一条拒绝不可恢复，
  重试整个调用就绝无可能成功；取「任一」会把永久性拒绝洗白成可重试。
- **`guardset` 的 `Check` 不得依赖其他 Guard 的结论**，否则一并求值就改变单条判定的语义。
  需要有先后的守卫请直接排进 `invoke.Chain`。
- **`Check` 为 nil、panic、或拒绝却不说理由，一律按拒绝收口（fail-closed）**，
  且 `Retryable=false`——与「正常判定得出的拒绝」区分开，两者处置不同。
- **拒绝走 `(Verdict, nil)`，不返回 error**——拒绝是正常判定结论，
  error 在链上会被归口成「这一环自己坏了」。

**什么时候不该用**

- **工具只有三五个、全塞进上下文也不占地方**——渐进披露省的是「几十个工具」那一档的固定成本，
  工具少时它只增加装配面。
- **想用 `Search` 的结果顺带把工具激活**——检索**只排序、不激活**；
  「披露」的唯一写入口是 `Catalog.Load`。
- **想让门禁替你执行工具**——它只判「能不能调」，不执行、不解释参数语义。

#### 3.3.3 失败分类：上游 `toolfail`（`github.com/oagentkit/guardx/toolfail`）

「这个失败值不值得重试」的**机械判据**。类别 → 可重试性/退避/需人工/重发安全/模型指引
是**数据表**而非散落的 if。

```go
package myapp

import (
	"github.com/oekit/pkg/errors"
	"github.com/oagentkit/guardx/toolfail"
)

// 工具侧在知道类别的那一刻调 ClassifyAs，不要指望 Classify 猜出来
func callTool(...) error {
	switch {
	case errPortInvalid(err):
		return toolfail.ClassifyAs(toolfail.ClassInvalidArgument, err)
	case errUpstreamReset(err):
		return toolfail.New(toolfail.ClassTransient, "upstream_reset", "上游连接重置")
	default:
		return err
	}
}

// 模型侧：Guidance() 是唯一消费方
func advise(err error) string {
	if f, ok := toolfail.As(err); ok {
		// 正交决策：
		//   f.Retryable()      —— 重试有没有收益
		//   f.SafeToResubmit() —— 原样重发安不安全
		//   toolfail.PolicyOf(f.Class).NeedsOperator —— 要不要叫人
		return f.Guidance()
	}
	// 链上没有 *Failure：按 Classify 的结论兜底（哨兵 / 自报仍可能判出类别；
	// 都判不出 ⇒ ClassUnknown ⇒ 不可重试，fail-closed）。
	// 注意：Guidance 是 *Failure 的**方法**，上游没有包级 toolfail.Guidance(err)。
	return toolfail.ClassifyAs(toolfail.Classify(err), err).Guidance()
}
```

**两个正交维度**：`Retryable`（重试有没有收益）与 `SafeToResubmit`（原样重发安不安全）。
超时值得重试，但服务端可能已执行，原样重发就把一次调用变成两次副作用。
`Idempotency` 用三态而非 bool——「没想过」与「确实不幂等」不被压成同一个 false。

**坑**

- **不可归类一律不可重试**（fail-closed）。宿主认为某类可重试必须显式归类，
  不能让内核替它乐观。
- **只有 `ClassRateLimited` 的 `SafeToResubmit` 为真**——唯一能证明服务端未受理本次调用的类别。
  超时类不给真。
- **`Remediation` 不得自带重试结论**（不要写「重试」「等待 N 秒」「需要人工」）——
  那是 `Guidance` 的机械判定，写在这里会与 Guidance 直接矛盾。
  取值也不得含具体标识、数值与业务词面——它会原样进入提示词。
- **`Code` 不得写入每次都变的内容**（数值、标识、耗时），它要跨版本稳定用于聚合。
- **`RetryableReporter` 的实现方必须 nil-safe**：链上可能挂 typed-nil 指针。

**什么时候不该用**

- **工具失败没有稳定类别可归**——`toolfail` 的价值在**跨版本稳定的类别 + 码**；
  每次都换的文案对它没用，只能自己去聚合。
- **想让它替你决定重试**——它只给判定与指引，**退避与重试执行**归宿主。
- **想知道「上一次到底生效没有」**——分类回答不了：要么用 `sideeffect` 的闸，要么查下游事实。

#### 3.3.4 审批门接进判定环：`policy.Stage`

审计门（上游 `toolgate/policy`）有两个接入口：

| 入口 | 形态 | 用在哪 |
|---|---|---|
| `policy.AuditGate` | 包住**单个工具**（eino 的 `InvokableTool`） | 一个工具单独加审 |
| `policy.Stage` | 接成**判定环的一环**（`invoke.Stage`） | **一处装配、处处生效** |

`Stage` 才是「装配一次、处处生效」的形态：工具调用、程序内调用
（`ptc` 的 `host.<tool>()`）、子进程侧调用全部走同一条 invoke 链 ⇒
审批规则只装配一次。

```go
import (
	"github.com/yi-nology/goagent-kernel/guardset"
	"github.com/oagentkit/toolgate/policy"
)

// 审批门：策略来自宿主的外置 yaml（阈值只降不升）。
// fsys 与路径都由宿主给出（如 _shared/operation_policy.yaml）；
// 文件不存在 ⇒ 回内置基座，不报错。
p, err := policy.LoadOverrides(fsys, "_shared/operation_policy.yaml")
if err != nil {
	return err // 读取 / 解析失败即拒，不带错跑
}
gate := policy.NewGate(p, nil)

// 接成判定环的一环。Op 决定这一环审的是哪类操作；
// onDecision 是审计回调（留痕用）。
stages := []invoke.Stage{
	policy.Stage(gate, policy.Op{Type: policy.OpToolCall}, func(d policy.Decision) {
		audit("tool_call", d)
	}),
	tooldisclose.Stage(catalog),   // 披露门
	guardset.Stage(guards),        // 多守卫合成
}
```

**三个要点**

- **`Op` 决定审的是哪类操作**：`OpToolCall`（工具调用面）/ `OpExecuteStep`
  （已批准方案的执行步骤）/ `OpPlanSubmit`（方案提审）。判定环里
  `c.Name` 会覆盖 `Op.Tool`（工具名由调用现场给出）。
- **没装审批门（`gate == nil`）⇒ 不放行这道门，但不是静默**：留痕里仍有这一环，
  名称可查（`audit_gate`）。
- **审批与执行后端是两层**：审批判「该不该做」，上游 `sandbox` 的档位判「在哪跑」。
  两者要**一起同意**才放行——那正是 `gatecheck` 包做的事（见 §3.11）。

#### 3.3.5 撤销的归属：`guardset.Assembly`

`guardset.Set` 构造后只读——那是它的**不变量**（并发共享下的可变集合就是竞态源，
且会让「当前有哪些守卫」出现两个真相）。但只读有代价：**运行期想临时停掉某条守卫**
（应急降级、灰度、误伤止损），只能**整个进程重启**。

解法不是「让 `Set` 可变」，而是**换整份**：

```go
import (
	"github.com/oagentkit/guardx/invoke"
	"github.com/yi-nology/goagent-kernel/guardset"
)

// 装配面：持有「当前这一份」，并提供整份换掉的能力。
asm, err := guardset.NewAssembly(initialSet,
	guardset.WithSwapGate(func(ctx context.Context, next *guardset.Set) error {
		return authorizeSwap(ctx, next) // 授权是宿主的事，本包只提供接入点
	}),
	guardset.WithSwapRecorder(func(r guardset.SwapRecord) {
		audit("guard_swap", r) // 换了几次 / 换走了什么 / 换上了什么
	}),
)
if err != nil {
	return err
}

// 链上那一环指向**装配面**，不指向某一份 ⇒ 换份后不用重接链。
stages := []invoke.Stage{asm.Stage(), tooldisclose.Stage(catalog)}

// 撤销一条守卫：**基于当前那一份**算下一份，再整份换上去。
// 用 SwapFunc（不是 Swap）——它基于当前份做读改写，并发下不会丢更新。
err = asm.SwapFunc(ctx, func(cur guardset.Snapshot) (*guardset.Set, error) {
	return cur.Set.Without("rate_limiter")
})
if err != nil {
	return err // 换份失败 ⇒ 当前份保持不变
}
```

**六个要点**

- **撤销 = 换整份，不是改其中一条**。`Without` 是**纯函数**（返回新集合，原集合不变），
  撤销发生在换份上。
- **撤销要用 `SwapFunc`，不要用 `Swap`**。两者语义不同：
  - `SwapFunc(ctx, mutate)`：**基于当前那一份**的读改写（CAS 循环）⇒ 并发下**不丢更新**；
  - `Swap(ctx, next)`：整份替换为 `next`，**不基于当前那一份**。

  拿 `Swap` 做读改写会**丢更新**：两个并发撤销者拿到同一份旧，后写者把先写者盖掉 ⇒
  **撤销被撤销**（被撤掉的守卫悄悄复活，没有任何东西会提示）。
- **换份是原子的**：在途判定跑在**旧份**上，下一次判定跑在新份上，没有中间态；
  且**旧份不被修改**（它可能正被在途判定读着）。
- **换份后不用重接链**：`asm.Stage()` 每次判定取当前那一份。
- **代次回答「换过没有」**：`asm.Generation()` 是当前份的**投影**（不是另一个计数器），
  单调递增、不回卷。判定结论里带 `Outcome.Generation` ⇒ 审计能回答
  「这次判定是按第几代守卫做的」。要同时拿到集合与代次，用 `asm.Snapshot()`——
  分开读会得到自相矛盾的一对。
- **「全部撤销」不是降级，是失守**：换上去的那份永远非空。空集合在 `New` 那里就被拒绝，
  `Without` 去掉最后一条会返回 `ErrEmptySet`；撤销不存在的守卫返回 `ErrUnknownGuard`
  （**不静默忽略**——静默忽略会让宿主以为撤掉了某条守卫，实际那份集合里根本没有它）。

**订阅**：换份真正落地的那一刻，把新记录推给订阅者（当场生效的「换成了什么」）。

```go
h, err := asm.Subscribe(func(r guardset.SwapRecord) {
	audit("guard_swap", r) // 换了几次 / 换走了什么 / 换上了什么 / 第几代
})
// ...
asm.Unsubscribe(h)
```

订阅表是**整份替换**（copy-on-write），通知在**锁外**逐个调用；
某个订阅者 panic 会被吞掉——换份**已经落地**，让回调抛异常就把已经发生的事实
改写成「没发生」。

| 符号 | 说明 |
|---|---|
| `NewAssembly` / `Current` / `Snapshot` | 构造、读当前份、一次读出集合与代次 |
| `Assembly.SwapFunc` | **基于当前那一份**的读改写（CAS，不丢更新）——撤销用这个 |
| `Assembly.Swap` | 整份替换为给定的一份（**不**基于当前那一份） |
| `Assembly.Generation` | 当前份的代次（投影，单调不回卷） |
| `Assembly.Stage` / `StageWithGuidance` | 接成 invoke 链一环，每次判定取当前份 |
| `Assembly.Subscribe` / `Unsubscribe` | 换份订阅与退订 |
| `Set.Without` | 纯函数：产出少了指定守卫的**新**集合 |
| `WithSwapGate` / `WithSwapRecorder` | 换份闸（授权接入点）与换份留痕 |
| `ErrUnknownGuard` / `ErrEmptySet` | 撤销不存在的守卫 / 撤销到最后一条，都拒绝 |

---

**什么时候不该用**

- **守卫之间有严格先后**（前一环的结论是后一环的输入）——那要**排进 `invoke.Chain`**；
  塞进同一个 `Set` 会让一并求值改变单条判定的语义。
- **运行期要临时增删守卫**——`Set` 构造后只读（那是它的不变量），要换份请用 `Assembly`。
- **只有一条守卫**——一条守卫用 `invoke.Stage` 就够，`Set` 的价值在合成多条拒绝。

### 3.4 程序化工具编排：`ptc`

模型写一段程序，进程内 goja VM 执行，程序内经 bindings 调宿主工具，
**只把归并后的小结果带回上下文**。

> **N 轮 ReAct 往返塌缩成 1 轮，原始数据不过模型面。**
> 典型场景：要查 20 台机器的指标，ReAct 要 20 轮往返、每次都过模型面；
> ptc 一轮内循环完，只把「20 台里 3 台异常」带回去。

```go
package myapp

import (
	"context"
	"time"

	"github.com/yi-nology/goagent-kernel/ptc"
)

func runProgram(ctx context.Context, stages []invoke.Stage) ptc.Result {
	bindings := []ptc.Binding{
		{
			// 带 Signature：模型按位置传参，ptc 负责打包成 JSON 再交给 Call
			// 形态里的函数名必须是**净化后**的名字（= 绑定名经 toolname.Normalize），
			// 否则装配期被拒（binding_signature）。最好用 tooldisclose.SignatureOf 派生。
			Name:      "get_logs",
			Signature: "get_logs(host, count?)", // 可选形参以尾部 ? 标记
			Call: func(ctx context.Context, argsJSON string) (string, error) {
				return callHostTool(ctx, "get_logs", argsJSON) // 回原文，不截断
			},
		},
		{
			// 无 Signature：退化为 host.<name>(argsJSON)
			Name: "get_metrics",
			Call: func(ctx context.Context, argsJSON string) (string, error) {
				return callHostTool(ctx, "get_metrics", argsJSON)
			},
		},
	}

	program := `
const out = [];
for (const h of ["h1", "h2", "h3"]) {
  const m = JSON.parse(await host.get_metrics('{"host":"' + h + '"}'));
  if (m.cpu > 0.9) out.push({h, m});
}
return out;
`

	return ptc.Run(ctx, program, bindings, ptc.Options{
		Timeout:  10 * time.Second,
		MaxCalls: 20,
		MaxBytes: 64 << 10,
		Stages:   stages, // ★ 用与普通工具调用**同一条链**的 Stage 切片
	})
}
```

> ★ 这一行是 `ptc` 最重要的接入纪律：程序内的 `host.<tool>()` 与普通工具调用
> 走同一套 Stage 判定环。不接的话，本包就成了整条链上唯一绕过所有门禁的路径。

**三重预算替代 OS 沙盘**：goja 进程内 VM（纯 Go 零 CGO，无 Node 分发面）+
保守 JS 子集（无 fetch/fs/require，能力只来自 bindings）+ 墙钟 deadline（`vm.Interrupt`）、
绑定调用数上限（`MaxCalls`）、绑定输出累计字节上限（`MaxBytes`）。

**`ErrorKind` 取值全集**：`""`（成功）+ `ptc.AllErrorKinds()` 的 **14** 个：
`""`（成功）/ `bad_result` / `binding_collision` / `binding_signature` /
`budget_bytes` / `budget_calls` / `deadline` / `host_panic` / `pending` /
`permission_denied` / `runtime` / `stage_denied` / `stage_failed` / `syntax` /
`unmanaged`。词表与常量集同源，新增取值只改表，`AllErrorKinds()` 自动跟随
（issue #365）。

`stage_denied` 与 `stage_failed` 分开不是洁癖：前者是**正常裁决**（程序改不掉），
后者是**宿主门禁自己坏了**（该修守卫代码，不是程序）。

**坑**

- **撞名即装配期拒，不进 VM**：`toolname.Normalize` 把 `-` `.` 等映射成 `_`，`get-logs` 与
  `get_logs` 净化后同名。放任覆盖会让 `host.get_logs()` 执行的是**另一个工具**——无报错、
  无日志，CallLog 还照常记着 B 的账。规整规则只有一份（`toolname` 包），
  **别在宿主侧另写一套净化逻辑**——两套规则漂移开的形态是「目录里派得出、程序里认不出」。
- **可选形参必须排在必填之后**。否则中间出现可选、后面又有必填时，
  模型为了传后面的必填项就得先把前面的空位填上——比直接判非法更坏。
- **可选槽位只在真的给了值时才写入 JSON**。写进去一个 `undefined` 到 JSON 里就是 `null`，
  宿主收到 `{"count":null}` 与「没给 count」是两件事。
- **被判定环拒绝的调用同样计数**（`res.Calls++`）：程序可以 try/catch 掉这次错误再循环，
  若不计预算，一个被拒的工具就能被无限重试，`MaxCalls` 形同虚设。
- **v0.32.0 起：`Stages` 为空且装配了工具绑定 ⇒ 拒绝执行**（`ErrorKind=unmanaged`）。
  此前是「不接任何判定、照常执行」——那条路径不带门禁，却**没有任何信号**：
  你拿到的是一个正常的执行结果，而程序里的 `host.<tool>(...)` 已经直达你的工具。
  本包是对外推荐的批量调用路径，默认位必须在安全那一侧。两个例外：
  **不装配任何绑定**（纯计算程序）不需要判定环；确需裸执行时显式声明
  `ptc.Options{AllowUnmanaged: true}`——但那是**逃逸口**，本仓门禁会拦生产代码里的显式逃逸。
  **宿主怎么合法用它**（这条路径自 **v0.32.1** 起才真正走得通，见 §3.3.4 末尾）：
  在自己的 `architecture-policy.yaml` 的 `exceptions` 段登记一条
  `{rule, path, line, reason, until, removal}`——`rule` 填
  `harness-ptc-unmanaged-escape`，`path`+`line` **精确指向**那处字面量
  （三元组精确匹配，不做前缀模糊豁免；文件行号一变，豁免即失配 ⇒ 门禁当场变红，
  这是设计：豁免必须跟着代码一起改）。
  ⚠️ **v0.32.0 照着这条指引做会红**：自校验当时只认 policy `rules` 段里的规则 id，
  而逃逸门禁是**恒开**的、不在 rules 段里 ⇒ 你的登记被报「指向不存在的规则」。
  那是门禁自己的缺陷（指引说可以、机制不让），v0.32.1 修掉了：
  「规则存在」的口径并上内置规则 id，豁免**真的生效**（违反计入 excused、不再阻断），
  `until` 到期即失效（债到期重新欠账）。
- **运行期异常的 `Location` 经常是空的**，这是工具能力边界：程序体是 async IIFE，
  函数体异常变成 Promise rejection，goja 未提供「rejection 处回溯调用栈」的公开面。
  拿不到就零值，**不猜**。
- **`await` 永不落定的 Promise → `ErrorKind=pending`**（VM 无定时器、绑定均为同步调用）。
- **审批挂起 = 终止 + 已执行步骤留痕**：`vm.Interrupt` 打不断阻塞中的 Go 绑定调用，
  中断驱动先 cancel 运行期 ctx 再 Interrupt。
- **出站守卫（netguard）不在判定环上，这是有意的**：判定环判「这次调用要不要放行」，
  netguard 判「这个 URL 能不能发出去」，是两个执行点。
  分工前提是沙箱性质——**谁给 VM 加了 fetch，谁就得重新回答 netguard 该接在哪**。

---

**什么时候不该用**

- **只有一两个工具调用**——多写一段 JS 的成本高于省下的一轮往返。
- **程序需要访问文件 / 网络 / 任意 shell**——goja 是**保守 JS 子集**（无 fetch / fs / require）；
  要那种能力请换一个执行后端，而不是给 VM 开口子。
- **编排只跑一次、不可重入**——`ptc` 的价值在「N 轮塌缩成 1 轮」，一次性任务用普通工具调用更直接。

#### 3.4.1 执行沙箱的档位与后端协议：上游 `guardx/sandbox`

`ptc` 是**一个**执行后端（进程内 goja 解释器，档位 `LevelProcess`）。
`sandbox` 是**后端协议**：档位语义 + 登记簿 + 按要求档位选后端。
宿主可以注册自己的后端（容器、microVM、受控子进程），内核不关心它们怎么实现。

**为什么需要它**：「模型写的程序在哪跑、跑起来能触达什么」此前只有一个答案，
而那个答案是**写死在调用点**的——没有档位概念，也没有「这次执行至少要多强」的表达。
于是宿主想跑一段不可信程序时，「它跑在够强的隔离里吗」这件事无处声明，
只能靠人记得别挑错后端。

**档位**（`sandbox.Level`，序即强弱）：

| 档位 | 含义 | 内置后端 |
|---|---|---|
| `LevelNone` | 无隔离：与宿主同权限 | 无（要显式登记才会被选中）|
| `LevelProcess` | 进程内解释器边界：能力白名单 + 资源预算 | `ptc`（goja）|
| `LevelOS` | 内核级强制（文件/网络/系统调用由内核策略裁剪）| 无 |
| `LevelMachine` | 硬件虚拟化边界 | 无 |

**接入自己的后端**（三步）：

① 实现 `sandbox.Provider`：

```go
type Provider interface {
    ID() string
    Level() sandbox.Level
    Exec(ctx context.Context, req sandbox.Request) (sandbox.Outcome, error)
}
```

② 在**你自己的** policy 里登记它的档位（内核的 policy 只登记内核自己的后端）：

```yaml
sandbox_providers:
  enabled: true
  severity: error
  providers:
    - id: my-container-backend
      level: LevelOS
      package: internal/sandboxbackend
      notes: |
        容器后端：只读根文件系统、无网络、非 root、资源上限……
        （这一行是「档位声明的签字处」——写清它凭什么说自己有这一档）
```

③ 登记 + 执行：

```go
reg := sandbox.NewRegistry()
if err := reg.Register(ptc.Provider{Bindings: bindings}); err != nil { /* 标识重复等 */ }
if err := reg.Register(myBackend); err != nil { /* 同上 */ }

// 要求「至少进程内解释器级」——在满足要求的后端里取最弱的
out, err := sandbox.Execute(ctx, reg, sandbox.LevelProcess, sandbox.Request{
    Program: src,
    Budget:  sandbox.Budget{WallClock: 30 * time.Second, MaxCalls: 50, MaxBytes: 256 << 10},
})
```

**档位不足时的行为**：无满足者时 `Execute` 返回错误，**不降级**到弱后端。
这是刻意的：降级会让「宿主声明的强度」与「实际执行的强度」之间没有信号——
宿主写着「本次要内核级」、实际跑在进程内，而日志、审计、测试全绿。
降级必须是宿主**有意识**的动作（补后端，或把要求降到自己承担得起的档位）。

**档位声明的核对**：`archcheck` 的执行体是**可导入包**，宿主仓可以复用同一套判据
（不必各写一遍——各写一遍必然各判一套，漂移到没人发现为止）：

```go
import "github.com/yi-nology/goagent-kernel/archcheck"

pol, err := archcheck.Load("architecture-policy.yaml")
res, err := archcheck.Run(root, pol)
if res.HasBlocking() { os.Exit(1) }
```

**已知边界**：

- **档位是后端自报的，内核不校验其真实性。** 门禁保证的是「声明可见（在 policy 里）、
  不漂移（门禁核对）、有签字（人 review 时看到那一行）」，**不是**「隔离真的到位」。
  宿主不信任某个后端，唯一办法是**不登记它**。
- **档位零值取最弱一档**（`LevelNone`）：未声明档位的后端只会被「要求=none」选中，
  任何更高要求都因「无满足者」而拒绝。方向是安全的那一侧——漏声明的后端拿到的是
  最弱的选择，不是最强的豁免。
- **本仓只提供一个后端**（`ptc`）。`LevelOS` / `LevelMachine` 档位**尚无内置后端**：
  它们要么由宿主实现，要么等内核补上「容器内程序回调宿主工具」那条 RPC 通道——
  程序在容器里要调宿主工具，就必须有一条跨进程的调用通道，那不是后端实现，
  是另一个机制。

---

### 3.5 上下文三件套：`budget` + `spill` + `offload`

三者的分工泾渭分明，接线时别混：

| 包 | 管什么 | 时机 | 方向 |
|---|---|---|---|
| `budget` | 输入面计量与压实 | 每轮前 | 输入侧 |
| `offload` | 二进制本体躺在消息里 | 每轮装配 | 输入侧 |
| `spill` | 输出超预算被截断 | 产出时 | 输出侧 |

<div align="center">
<svg width="760" viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="320" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="16" y="26" font-size="13" fill="#e6edf3">上下文三件套：三段各自的边界</text>
<text x="16" y="48" font-size="10" fill="#9aa7b8">三段各管一件事，边界互不重叠；记账各归各，不可互相顶替</text>
<rect x="16" y="76" width="728" height="64" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="102" font-size="11.5" fill="#58a6ff">budget</text>
<text x="32" y="122" font-size="8.5" fill="#9aa7b8">输入侧 · 每轮前</text>
<line x1="170" y1="88" x2="170" y2="128" stroke="#262e3d"/>
<text x="186" y="98" font-size="8" fill="#9aa7b8">管什么</text>
<text x="186" y="118" font-size="9.5" fill="#e6edf3">分段计量 + 压实计划（纯计算，无 I/O）</text>
<line x1="460" y1="88" x2="460" y2="128" stroke="#262e3d"/>
<text x="476" y="98" font-size="8" fill="#9aa7b8">边界：它不做什么</text>
<text x="476" y="118" font-size="9.5" fill="#e6edf3">不做摘要——摘要是一次 LLM 调用，归宿主</text>
<rect x="16" y="152" width="728" height="64" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="178" font-size="11.5" fill="#a371f7">offload</text>
<text x="32" y="198" font-size="8.5" fill="#9aa7b8">输入侧 · 每轮装配</text>
<line x1="170" y1="164" x2="170" y2="204" stroke="#262e3d"/>
<text x="186" y="174" font-size="8" fill="#9aa7b8">管什么</text>
<text x="186" y="194" font-size="9.5" fill="#e6edf3">二进制本体外置为占位文本</text>
<line x1="460" y1="164" x2="460" y2="204" stroke="#262e3d"/>
<text x="476" y="174" font-size="8" fill="#9aa7b8">边界：它不做什么</text>
<text x="476" y="194" font-size="9.5" fill="#f85149">首次只标记不替换：内容原地保留</text>
<rect x="16" y="228" width="728" height="64" rx="8" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="254" font-size="11.5" fill="#3fb950">spill</text>
<text x="32" y="274" font-size="8.5" fill="#9aa7b8">输出侧 · 产出时</text>
<line x1="170" y1="240" x2="170" y2="280" stroke="#262e3d"/>
<text x="186" y="250" font-size="8" fill="#9aa7b8">管什么</text>
<text x="186" y="270" font-size="9.5" fill="#e6edf3">溢出存档的清单与分页取回</text>
<line x1="460" y1="240" x2="460" y2="280" stroke="#262e3d"/>
<text x="476" y="250" font-size="8" fill="#9aa7b8">边界：它不做什么</text>
<text x="476" y="270" font-size="9.5" fill="#e6edf3">不写盘：写侧与清理归宿主</text>
<text x="16" y="308" font-size="9" fill="#9aa7b8">压实要显式 Shrink 改账；占位替换按轮提交；截断提示是双侧钉档契约——三件事各记各的账</text>
</svg>
</div>

*图 6：budget 管输入侧的量，offload 管输入侧的大对象，spill 管输出侧的截断。三者的边界互不重叠，记账也不能互相顶替。*

#### 3.5.1 `budget`：分段计量 + 压实计划

分段计量不是为了好看——**只给总数无法区分「工具面膨胀」与「历史膨胀」**，
而两者的处置完全不同。

```go
package myapp

import (
	"context"
	"strings"

	"github.com/yi-nology/goagent-kernel/budget"
)

// ① 宿主实现：摘要是一次 LLM 调用，属模型面不属编排面
type llmSummarizer struct{}

func (llmSummarizer) Summarize(ctx context.Context, leading []budget.Item) (budget.Summary, error) {
	var sb strings.Builder
	for _, it := range leading {
		sb.WriteString(it.ID + "；")
	}
	text := sb.String()
	return budget.Summary{Text: text, Tokens: len([]rune(text)) / 2}, nil
}

// ② 宿主实现：原文落盘，返回取回句柄
type blobArchive struct{}

func (blobArchive) Archive(ctx context.Context, items []budget.Item) (budget.Locator, error) {
	return budget.Locator("s3://audit/conv-42/compaction-3.json"), nil
}

func maybeCompact(ctx context.Context, m *budget.Meter, limit int,
	items []budget.Item) error {
	// ③ 压力档：tight 75% / critical 90%（到达即进档，宁可早一档提示）
	if budget.Classify(m.Total(), limit) != budget.PressureCritical {
		return nil
	}

	// ④ 计划是纯计算，无 I/O；三条空操作守卫返回「原因」而非 bool。
	// 条目逐条带 Item.Tokens，Apply 按逐条之和记毛移除量；PlanInput.ItemTokens
	// 只是宿主没提供逐条量时的兜底估算。
	plan, err := budget.BuildPlan(budget.PlanInput{Items: len(items), KeepTail: 4})
	if err != nil {
		return nil // 条数不够等，本轮不压
	}

	// 尾部从 plan.Cutoff 起切（= plan.SummarizeItems），不是从
	// plan.KeepTailItems 起切：后者是「保留几条」，前者才是「从哪里开始」。
	res, err := budget.Apply(ctx, plan,
		items[:plan.Cutoff], items[plan.Cutoff:],
		blobArchive{}, llmSummarizer{})
	if err != nil {
		return err
	}

	// ⑤ 压实不产生模型调用，所以必须显式把账改回来
	m.Shrink(budget.SegmentConversation,
		budget.NetRemovedTokens(res.GrossRemovedTokens, res.Summary.Tokens))
	return nil
}
```

**坑**

- **`Apply` 的尾部从 `plan.Cutoff` 起切，不是从 `plan.KeepTailItems` 起切**。
  `Cutoff` 是「截断下标」（尾部起点），`KeepTailItems` 是「保留几条」——两者
  数值相近、含义不同，抄错会让入参条数与计划不符并撞 `ErrPlanMismatch`。
- **毛/净移除量必须分开取**。`GrossRemovedTokens` 是扣摘要**之前**的毛量；
  按名字对齐参考实现的同名字段会**静默错账**，方向恰好是「看起来更省了」。
  要「减掉多少」请直接用 `NetRemovedTokens`（钳位在 0）。`GrossRemovedTokens`
  优先取被归档条目的逐条 `Tokens` 之和，计划的 `ItemTokens` 只是兜底估算——
  条目逐条带量时不要指望计划估算兜住。
- **`Apply` 的毛移除量以逐条 `Item.Tokens` 为准**。只填 `PlanInput.ItemTokens`
  而条目不带 `Tokens` 时，账会停在兜底估算上；估算与真实量不符（例如计划按
  25/条、实际 900/条）会让账上少减，而压实是不可逆的逐字丢弃。
- **分段估算永不回填 provider 报数**。provider 报的是总数，回填到分段等于让
  tokenizer 误差披上分段精确度的外衣，而压实计划恰按段分配裁剪——喂残差等于喂假账。
- **`MaxInput` 无预留或窗口未知时原样返回 `baseInput`**——扣减纯属内核自作主张。
- **`Classify` 与 `Display.Overflow` 在 `capacity<=0` 时同向**：Classify 返回
  `PressureCritical`，Overflow 返回 `true`——「不知道会不会溢出」与「不会溢出」
  行为完全相反，不得声称余量充足。两者共用同一份「上限是否已知」派生。
- **归档必须先于摘要**：摘要失败时原文已落盘可直接重试（重跑归档幂等）；
  反序可能出现「摘要已丢弃原文、摘要本身又没拿到」的净损失。
- **`Apply` 的三条计划校验全在任何副作用之前**——被拒的调用不得留下任何副作用。

**什么时候不该用**

- **你的上下文窗口由模型侧固定、你根本不打算压**——接 `budget` 只是多一层账。
- **想让内核替你摘要**——摘要是一次 LLM 调用，属模型面；`budget` 只给计划不给摘要
  （`Summarizer` 由宿主实现）。
- **需要删内容**——`budget` 只计量与计划，逐字丢弃是宿主在 `Apply` 这一侧的动作。

#### 3.5.2 `spill`：输出溢出存档

**本包不做写入**，只提供清单、分页取回与写侧清理。

```go
package myapp

import (
	"github.com/yi-nology/goagent-kernel/spill"
)

func onTruncated(root, sessionID string, omitted, total int) string {
	name := "out-0007.bin"
	if err := writeSpillFile(root, sessionID, name, fullOutput); err != nil {
		return "（输出过长且存档失败）" // fail-soft：不让存档失败阻断主流程
	}
	// 提示文案是双侧钉档契约：前端按同一契约解析，改文案须同步前端与其单测
	return spill.TruncHint(spill.TruncNote{
		Omit: omitted, Total: total, Retrievable: true, Name: name,
	})
}

// 模型侧分页取回，防「分页取回」退化成「全文重塞」
func retrieve(root, sessionID, hint string) {
	locator, truncated := spill.ParseHint(hint)
	if !truncated {
		return
	}
	pg, err := spill.ReadPage(root, sessionID, locator, 0, spill.DefaultLimit)
	_ = pg, err
}
```

**坑**

- **会话隔离三重守卫，fail-closed**：`sanitizeName`（只允许单段裸文件名）→
  `sessionDir`（`root` 空报「未装配」、`sessionID` 空报「无法归属会话」）→ 前缀校验。
  任一不过即 `ErrBadName` 或显式 error，**不回退到宽松读**。
- **`List` 无存档返回 `[]Item{}, nil`**，不是错误——宿主不必判空。
- **`Prune` 是 best-effort**：无返回值、清理失败不影响本次存档。
- `ReadPage` 边界钳位：`off<0` 视作 0、`off>total` 收到 `total`、`limit<=0` 用 `DefaultLimit`。
- **`List` 返回 mtime 降序**，`spill` 侧有 `Prune` 而 `offload` 侧没有（回收由宿主决定）。

**什么时候不该用**

- **输出从不超长**——`spill` 只在超预算时才产生价值。
- **你不打算给模型分页取回的能力**——`TruncHint` 是**双侧钉档契约**：前端按同一契约解析，
  文案一改，模型侧的取回指引与前端就脱节。
- **想要一个带写入的存档服务**——本包**不做写入**，只给清单、分页取回与写侧清理。

#### 3.5.3 `offload`：内联大对象外置

把躺在消息历史里的二进制本体（base64 图片/音频/归档）换成占位文本。

**两阶段**：首次见到时落盘 + 标记，但**内容原地保留**（`Replaced` 恒为 false）；
后续轮才替换为占位。理由是若首次就替换，模型这一轮从未真正看到过该对象，
后续轮的占位文本又把它指向「模型没看过的东西」——等于凭空丢失输入。

```go
package myapp

import (
	"github.com/yi-nology/goagent-kernel/offload"
)

// 唯一接缝：Store（Put 必须对同一 relPath 幂等）
type fsStore struct{ root string }

func (s fsStore) Put(relPath string, data []byte) error {
	// relPath 是**逻辑键**（blobs/<前两位>/<摘要><扩展名>），不是本机路径
	return writeFile(s.root, relPath, data)
}

func applyOffload(st *offload.State, msg []byte) offload.Report {
	return offload.Apply(offload.Request{
		Blobs:        scanBlobs(msg),
		State:        st, // 归调用方持有，须跨轮存活
		Store:        fsStore{root: "/var/lib/harness/blobs"},
		MinBytes:     0,  // <=0 取缺省 offload.MinBytes（4 KiB）
		RetrieveHint: "需要时用 read_blob 工具按 path 取回（id 相同即字节相同）。",
	})
}
```

**坑**

- **`Apply` 刻意不返回 error**：条目级失败在机制上无法中断本轮，调用方不存在
  「忘处理 err 整轮报废」的可能。失败一律落到 `Item.Reason` 与 `Report.Inline`，
  内容原样保留。
- **落盘失败不得标记**：否则下一轮会放出指向从未写入的路径的占位文本（悬空引用），
  比不外置更糟。
- **标记按轮提交，不按条目提交**。否则同一消息里两份相同图片，第二份会被自己刚写的
  标记判成「后续轮」而当场替换——那一轮模型从未完整看到过其中任何一份。
  **宿主若自行调用 `State.Mark` 破坏这条，会打掉正确性保证。**
- **`State` 归调用方，`nil` 是保守方向**：`nil` 视作「什么都没见过」，每轮都按首次处理
  ——内容永远完整可见、只是省不下上下文。**永不丢输入。**
- **`Ext` 由调用方给，内核不猜**（不给时用 `ExtForMimeType(m.MimeType)`）。
  宿主各写一套映射会让同一份内容在不同宿主落进不同的键，去重与占位复用**同时失效**。
- **占位文本必须逐字节稳定**（不含时间戳/序号/随机数），`Ref.Path` 必须与真实落盘键逐字
  一致，否则模型按图索骥必然落空。摘要**只暴露 12 位前缀**——全量摘要进消息等于给内容
  做跨库比对指纹。
- **`Item.Reason` 仅供留痕，不可据以重试。**

---

**什么时候不该用**

- **消息里没有大对象**（纯文本）——占位替换会白白增加 token。
- **只是想「顺手压缩一下」**——`offload` 是**外置**不是压缩：本体还在磁盘上，模型靠 `read_blob` 取回。
- **想在一轮内就替换掉**——本包**首次只标记不替换**，强行替换等于让模型凭空失去输入。

### 3.6 断点续跑：`checkpoint` + `commitgate`

<div align="center">
<svg width="760" viewBox="0 0 760 310" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="310" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="16" y="24" font-size="13" fill="#e6edf3">恢复的两条判据：可信边界（checkpoint）与终态独占（commitgate）</text>
<rect x="16" y="40" width="352" height="254" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="64" font-size="11.5" fill="#e6edf3">checkpoint：可信边界按依赖判定</text>
<rect x="32" y="84" width="72" height="38" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="68" y="108" font-size="10" fill="#3fb950" text-anchor="middle">A 完成</text>
<rect x="156" y="84" width="72" height="38" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="192" y="108" font-size="10" fill="#f85149" text-anchor="middle">B 未完成</text>
<rect x="280" y="84" width="72" height="38" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="316" y="108" font-size="10" fill="#3fb950" text-anchor="middle">C 完成</text>
<line x1="106" y1="103" x2="152" y2="103" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="155,103 150,99.5 150,106.5" fill="#58a6ff"/>
<line x1="230" y1="103" x2="276" y2="103" stroke="#58a6ff" stroke-width="1.5"/>
<polygon points="279,103 274,99.5 274,106.5" fill="#58a6ff"/>
<text x="32" y="142" font-size="8.5" fill="#9aa7b8">完成集 = {A, C}，B 是空洞</text>
<rect x="32" y="152" width="320" height="58" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="48" y="172" font-size="9.5" fill="#f85149">空洞之后一律不采信 ⇒ Skip = ∅</text>
<text x="48" y="188" font-size="8.5" fill="#9aa7b8">A、C 一起进 Run（宁可多跑）</text>
<text x="48" y="203" font-size="8.5" fill="#9aa7b8">报 AnomalyDoneOutOfOrder</text>
<text x="32" y="232" font-size="8.5" fill="#9aa7b8">显式声明 DependsOn 后按各自前驱采信</text>
<text x="32" y="250" font-size="8.5" fill="#9aa7b8">并行组不再被前缀规则整体作废</text>
<text x="32" y="268" font-size="8.5" fill="#9aa7b8">终态快照 + 未完成步骤 ⇒ Blocked，不半截放行</text>
<rect x="392" y="40" width="352" height="254" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="408" y="64" font-size="11.5" fill="#e6edf3">commitgate：终态独占靠令牌</text>
<rect x="408" y="80" width="320" height="42" rx="8" fill="#1c2330" stroke="#58a6ff"/>
<text x="424" y="106" font-size="9.5" fill="#e6edf3">Acquire(ctx, Commit) ⇒ Lease{Token}</text>
<line x1="568" y1="124" x2="568" y2="146" stroke="#3fb950" stroke-width="1.5"/>
<polygon points="568,149 564,143 572,143" fill="#3fb950"/>
<line x1="568" y1="124" x2="568" y2="190" stroke="#f85149" stroke-width="1.5"/>
<polygon points="568,193 564,187 572,187" fill="#f85149"/>
<rect x="408" y="150" width="320" height="36" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="424" y="173" font-size="9.5" fill="#3fb950">落库成功 ⇒ 闸保留（不回滚）</text>
<rect x="408" y="194" width="320" height="36" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="424" y="217" font-size="9.5" fill="#f85149">落库失败 ⇒ ReleaseLease(令牌)</text>
<text x="408" y="252" font-size="8.5" fill="#9aa7b8">旧 Release(c) 认内容（三元组不唯一）</text>
<text x="408" y="268" font-size="8.5" fill="#9aa7b8">迟到的释放会放掉别人的闸 ⇒ 必须用令牌</text>
<text x="408" y="284" font-size="8.5" fill="#9aa7b8">回滚要用 context.WithoutCancel(ctx)</text>
</svg>
</div>

*图 5：左＝恢复计划按依赖定可信边界（失序时宁可多跑），右＝终态独占靠凭证放闸（靠内容会放错闸）。*

#### 3.6.1 `checkpoint`：续跑而非重放

内核把恢复判定做成纯函数 `BuildPlan(graph, snap) Plan`，三条硬规则：

**① 可信边界按依赖判定**——默认只信「图内**连续已完成的前缀**」。若快照完成集有空洞
（后面几步完成、前面没完成），空洞之后的完成**一律不采信**，`Skip` 停在前缀末尾、
其余进 `Run`（重跑），并报 `AnomalyDoneOutOfOrder`。
代价是「失序时会多跑几步」——刻意选的：多跑可恢复，重复副作用往往不可。

**并行组（fan-out）要显式声明依赖**，否则合法的乱序完成集会被前缀规则整体作废、
整批重来（并行阶段被迫从零再跑，下游只能自建 checkpoint）。声明后按各自前驱采信：
前置都满足的那一步自己完成即可信，不必等图内更早的步骤。

```go
graph := []checkpoint.StepDef{
    {ID: "prep"},
    {ID: "plug-a", DependsOn: []string{"prep"}}, // 彼此独立、乱序完成
    {ID: "plug-b", DependsOn: []string{"prep"}},
}
// 快照里 plug-b 完成、plug-a 未完成 ⇒ Skip 含 plug-b，Run 含 plug-a，不报异常
```

`StepDef{ID: "x"}`（零值）= 有序链，与旧口径**逐字一致**；`Isolated: true` = 本步
无前驱（可乱序完成）。`DependsOn` 是**取代**位置隐含前驱（不是叠加）——叠加的话
并行组第二步会依赖第一步，组就退化成链。详见
[CHECKPOINT.md §三之 1](CHECKPOINT.md#三、三条不变量)。

**② 终态不自动重开**——快照 `Status` 是终态且图内仍有未完成步骤时，
计划 `Blocked()=true` 且 `Run`/`Skip` **均置 nil**。内核拒绝替宿主决定是否重开，
也不给「看起来能跑」的半截计划。**这是唯一一种阻断异常**，其余异常都按最保守解释
给出可执行计划。

**③ 序号单调闸**——`SeqGate` 保证快照序号不倒退。

```go
package myapp

import (
	"context"

	"github.com/yi-nology/goagent-kernel/checkpoint"
)

// 宿主提供：跨进程单调写 + 读取
type storeCAS struct{ db *DB }

func (s *storeCAS) SaveIfNewer(ctx context.Context, snap checkpoint.Snapshot) error {
	// 条件更新：seq 不大于已存序号则不写，且不得部分写
	return s.db.CASUpsert(snap.RunID, snap.Seq, snap)
}

// ⚠️ v0.33.0 起：**序号为 0 的快照一律拒收**（ErrStaleSeq，且不得写入）。
// 它不是一次真实提交；放它落库等于给了它唯一能覆盖既有快照的写
// （已有 Seq=10 时被 Seq=0 盖掉 ⇒ 恢复点静默回退）。

func (s *storeCAS) Load(ctx context.Context, runID string) (checkpoint.Snapshot, error) {
	snap, ok, err := s.db.Get(runID)
	if !ok {
		return checkpoint.Snapshot{}, checkpoint.ErrNotFound // 不得返回 (零值, nil)
	}
	return snap, err
}

func resume(ctx context.Context, runID string, graph []checkpoint.StepDef) error {
	st := &storeCAS{}
	gate := checkpoint.NewSeqGate()
	cm := checkpoint.NewCommitter(gate, st)

	// ★ 进程启动/重启后必须回灌进程内闸，否则闸是空的、陈旧写畅通无阻
	if err := cm.SeedFromStore(ctx, runID); err != nil {
		return err
	}

	snap, err := st.Load(ctx, runID)
	if errors.Is(err, checkpoint.ErrNotFound) {
		snap = checkpoint.Snapshot{} // 首次执行，全量 Run
	} else if err != nil {
		return err
	}

	plan := checkpoint.BuildPlan(graph, &snap)
	if plan.Blocked() {
		return errNeedsExplicitReopen // 终态不自动重开：交宿主显式决定
	}
	for _, a := range plan.Anomalies {
		metrics.Inc("checkpoint.anomaly", a.Kind) // 只报告不裁决
	}

	for _, step := range plan.Run {
		if err := cm.Commit(ctx, snapshotAfter(snap, step)); err != nil {
			return err // Commit 内部已按「成功不回滚 / 失败回滚」固化顺序
		}
	}
	return nil
}
```

**坑**

- **`SeqGate` 只在进程内**，记录只在内存，重启后为空。补救是 `Seed` / `SeedFromStore`。
  **不灌不是「闸没生效」，是「闸被重置后没人告知」。**
- **裸用 `SeqGate` 时 `Accept → Save` 且 Save 失败必须 `Release`**，
  否则该 run 卡死（重试被拒为陈旧、跳过则留永久空洞）。用 `Committer.Commit` 可免抄这一步。
- **`Commit` 成功时绝不能回滚**，否则闸退到存储前面，一次陈旧慢写就畅通无阻。
- **`Release` 回滚到前一个值，不是清零**。内部靠 `prev` 一格保留上一个已成功落库的序号；
  走 delete 会把「地板」一起抹掉。
- **`Snapshot` 落库前必须 `Clone`**：持引用的话，调用方落库后继续改动快照，
  恢复判定的依据不再可信。
- **`Seq == 0` 恒视为不参与单调判定**——不是一次真实提交。
- **`store == nil` 时 `Commit` 恒失败**（`ErrStoreMissing`），**不静默放行**。

**什么时候不该用**

- **编排不可重演**（外部动作不可逆且无幂等键）——续跑的前提是「同样的步骤再做一次是安全的」，
  否则宁可全量重来也不要半截续跑。
- **想让内核替你落快照**——`StoreCAS` 由宿主实现；`SeqGate` 还是**进程内**的，重启即空。
- **没有跨进程 / 跨重启的恢复需求**——单进程短命任务接这一套是净负担。

#### 3.6.2 `commitgate`：终态独占提交闸

**为什么 `statemachine` 排不出来**：它是**纯谓词**，只回答「这次迁移合不合法」，
**不回答「有没有人已经提交过了」**。编排把一个环节拆成两条并发分支时，
两条分支各自通过同一个纯谓词检查、各自把活干完、各自提交终态——
没有任何一层会发现这件事发生过。

`checkpoint.SeqGate` 也挡不住：它按 RunID 排**快照序号**，两个分支先后提交
seq=5、seq=6 是完全合法的递增，两个都被接受，后写的覆盖先写的——
**而先写分支的副作用早已发生**。

```go
package myapp

import (
	"context"

	"github.com/yi-nology/goagent-kernel/commitgate"
)

// 终态词表由宿主注入——本包只管「独占」，不认任何状态取值
func newCommitGate() *commitgate.Guard {
	return commitgate.NewGuard(func(status string) bool {
		switch status {
		case contract.StatusSucceeded, contract.StatusFailed,
			contract.StatusCancelled, contract.StatusTimedOut:
			return true
		}
		return false
	})
}

func commitStep(ctx context.Context, g *commitgate.Guard, ref commitgate.Ref,
	status, branch string, seq uint64, save func(context.Context) error) error {
	c := commitgate.Commit{Ref: ref, Status: status, Branch: branch, Seq: seq}
	lease, err := g.Acquire(ctx, c) // ★ 抢终态独占权（拿凭证）；取消/超时透传到存储
	if err != nil {
		return err // 抢不到就别干后面的活
	}
	if err := save(ctx); err != nil {
		// ★ 落库失败必须回滚，否则该环节闸被永久占住。
		// 释放是补偿动作：不要用已随请求取消的 ctx（取消会让释放不执行）。
		g.ReleaseLease(context.WithoutCancel(ctx), lease)
		return err
	}
	return nil
}
```

**坑**

- **Commit / Release 必须成对**。落库失败不 Release，该环节闸被永久占住，
  从此**再也无法提交终态**——不是「多花一个序号」，是彻底卡死。
- **下一版（#185，BREAKING）：`Store` 五方法与 `Guard` 全部触及存储的方法都收 `context.Context`**。
  网络 DB 实现的取消/超时从此能穿到底层驱动；`Acquire` 在 ctx 已取消时**不进入
  存储**，直接返回可 `errors.Is(err, context.Canceled)` 判定的错误。迁移时每个
  调用点补一个 ctx 即可；`ReleaseLease` / `Seed` / `SeedE` / `Status` / `Committed`
  同样多一个 ctx 首参。**回滚用的 ctx 不要随请求取消**（见上例 `WithoutCancel`）。
- **`Ref` 必须是「运行 + 环节」两级**：不同 run 的同名环节是不同对象，
  只按环节名去重会把第二次运行整个判成重复提交。
- **`isTerminal == nil` 时按「全部都是终态」处理**——保守侧，宁可多拦。
  反过来说：宿主漏注入会让闸被架空。
- **回滚要用凭证（`Acquire` + `ReleaseLease`），不要用旧的 `Release(c)`**。
  旧接口靠 (状态, 分支, 序号) 三元组认人，而那三元组**不唯一**：
  并行分支常常同名（`Branch` 写的是 agent 名），「落库失败 → 回滚 → 重试同一份快照」
  时三元组又与上一轮逐字相同。于是**迟到的 `Release` 会把别人的闸放掉**——
  实测 8 路同名并发提交出现 4 个赢家，同一环节的终态被提交两次，
  且失效是静默的（`Commit` 返回 nil，宿主无从察觉）。
  凭证路径把「身份」从内容换成令牌，那条承诺才无条件成立。
  旧接口仍在（同一分支、同一序号不会同时存在的场景可直接用），
  但它已加严：状态也必须相同才算数。
- **零值不可用**，请用 `NewGuard`。
- **`Seed` 重启后回灌**：`Seed` 幂等，且已有记录不被更旧的覆盖。
- 与 `checkpoint` 互补不替代：一个环节推进**先过本闸拿终态独占权，
  再由 SeqGate 保证快照不倒退**。

---

**什么时候不该用**

- **一个环节只有一个执行者、不会并发提交终态**——`statemachine` 的纯谓词就够。
- **想用它判「这一步该不该跑」**——它只管**终态独占**，不管可达性也不管顺序。
- **已经用 `sideeffect` 的幂等键覆盖了同一资源**——两个闸解决的是不同问题
  （副作用只做一次 vs 终态只提交一次），别指望互相替代。

### 3.7 实时事件：`eventbus` + `watchdog`

```go
package myapp

import (
	"github.com/yi-nology/goagent-kernel/eventbus"
	"github.com/yi-nology/goagent-kernel/watchdog"
)

// ① 发布路径：先落库拿自增 ID，再广播——保证 SSE 游标是唯一的单调空间
func publish(ctx context.Context, bus *eventbus.Bus, e eventbus.Event) error {
	id, err := repo.InsertEvent(e) // 拿 bq_event 自增 ID
	if err != nil {
		return err
	}
	e.ID = id
	bus.Broadcast(e)
	return nil
}

// ② SSE 桥接：按会话订阅，满缓冲丢帧由 DB 回放游标兜底补达
func serveSSE(hub *eventbus.Hub, sessionID string) {
	ch, cancel := hub.Subscribe(sessionID)
	defer cancel()
	for ev := range ch {
		pushToClient(ev)
	}
}

// ③ 配对不变量监视：只告警，不裁决
func runWatchdog(ctx context.Context, bus *eventbus.Bus) {
	pub := func(sessionID, caseID string, stepID *uint64, typ, level string, payload map[string]any) {
		publishEvent(ctx, sessionID, caseID, stepID, typ, level, payload)
	}
	go watchdog.New(pub).Run(ctx, bus)
}
```

**「有损」的确切语义**

- **`Bus`**：每订阅者 **64 缓冲**，满了就丢该订阅者的这条事件，**发布永不阻塞**。
- **`Hub`**：按会话键控，每订阅者缓冲 256；`Publish` 用 `select`+`default` 非阻塞投递。
- **丢的时候没有逐条信号**——只有累计计数 `Dropped() int64`。
  丢帧的补偿责任在宿主：SSE 的 **DB 回放游标 + 全局 tail 兜底补达**。
- 因此**持久化在发布点由宿主负责**：先落库拿自增 ID，再广播。
  SSE after 游标只认这一个单调空间，跨重启/跨副本都成立。

**坑**

- **`Bus.Publish` 赋的是未落库的临时序号**，生产路径改用 `InsertEvent` + `Broadcast`。
- **`Hub.Publish` 对 `SessionID` 为空的系统级事件直接忽略**（无会话受众）。
- **`Bus` 与 `Hub` 的 `Dropped` 是两套独立计数**，别混用。
- **`Hub` 的 cancel 幂等安全**；宿主仍应在 SSE 断开时调用。
- **`watchdog` 只告警不裁决**：热路径裁决在守卫侧（`IdentityGuard`），不在本包。
  **别让宿主误以为它能兜底。**
- **配对键是全局 `call_id`**（`eventbus.ToolCallID`，与 `trajectory` 同源，**不含
  step**）：结果可以在后续环节到达，同一 `call_id` 跨环节重复声明（重试/改派）
  只报 `duplicate_call`。INV1 的判定时刻是 `session_finished` 或 2h TTL 清扫——
  不是每个 `step_finished`（按环节判会把「结果晚到」误报成泄漏）。无 `call_id`
  不记缺口（与 `trajectory` 的 `no_call_id` 同口径）。
- **INV3 是轮级括号**（issue #171）：`turn_started` 按 sessionID 开括号，
  `turn_ended` 闭合必须带 `closed_by`（缺省 `loop|recovery|boot-reconcile|panic`，
  宿主经 `watchdog.NewWithConfig(pub, watchdog.Config{ClosedBy: ...})` 扩充）且
  `status` ∈ contract 终态词表（`succeeded|failed|cancelled|timed_out`）。告警
  reason 四条：`turn_end_unattributed` / `turn_end_unknown_status` /
  `turn_end_unopened` / `turn_open_leak`（2h 开放 TTL，与既有 `openIdleTTL` 同款）。
  **停机留开放是合法态**：`Run` 因 ctx 取消退出时不扫尾，否则每个宿主的正常停机
  都会误报泄漏。它判不了「闭合 status 与会话终态是否一致」——那要存储面，语义
  裁决归宿主恢复路径。
- **`Watcher` 不是并发安全的**，只能由 `Run` 单 goroutine 驱动。
- 命名撞车：spec 2026-09-15-tool-identity-otel-guard-design §8 的「INV3（事件面
  checksum 抽验）」与本包的 INV3 轮括号不是一回事，前者仍**不实现**：`tool_result`
  载荷截断到 2000 字符，无法重算 checksum；内容级校验由守卫在源头全量覆盖。

---

**什么时候不该用**

- **单进程、无前端实时进度**——事件总线要落库 + 广播两套面，收益小于成本。
- **指望它兜底裁决**——`watchdog` **只告警不裁决**，热路径裁决在守卫侧。
- **需要「事件必达」**——总线**有损**（每订阅者 64 缓冲，满即丢），
  补达责任在宿主的 DB 回放游标，不在本包。

### 3.8 流式与退出：`delta` + `drain` + `pending`

#### 3.8.1 `delta`：增量节流

⚠️ **生命周期必须是「单个环节」，不是「整个进程」。**

```go
package myapp

func streamRound(p *delta.Pacer, sp *delta.Splitter, publish func(delta.Pub), chunk string) {
	// ① Splitter：安全发射窗口，跨分片的标签不会被污染
	reasoning, content := sp.Feed(chunk)
	if r, ok := p.Offer("reasoning", 1, reasoning); ok {
		publish(r)
	}
	if c, ok := p.Offer("text", 1, content); ok {
		publish(c)
	}
}

func finish(p *delta.Pacer, publish func(delta.Pub)) {
	// ② 环节收口：按 轮升序 × reasoning→text 冲刷所有残尾，保证落序可回放
	for _, pub := range p.FlushAll() {
		publish(pub)
	}
}
```

**这个生命周期约束为什么重要**：`Pacer` 的 `entries` 按 `(kind, iteration)` 惰性建流
且**从不回收**，`FlushAll` 扫序上界又是 `max(iteration)`——两处单独看都像缺陷，
安全**只因为一个外部不变量**：宿主每个 LLM 环节新建一个 Pacer，环节 defer 里
`FlushAll` 后整体丢弃；`iteration` 是环节内局部计数器、不跨环节累加。
改成长生命周期对象（如「整个会话一个 Pacer」）会同时引爆这两条，
症状「跑久了内存涨 / 收口卡住」极难归因。

**其他坑**

- **`Splitter` 不 `Flush` 就丢流 = 永远扣住最后几个字符**。`Feed` 返回空不是丢内容，
  是扣在 `pending` 等下一片。
- **`NewSplitterTags` 任一标签为空则回落缺省**：空标签会让「后缀是标签前缀」恒真，
  内容永远扣住不发，是最难查的挂起。
- **`Text` 是分片非累积快照**：消费方按序追加，避免长文本 O(n²) 重发。
- **超帽静默停发**：`CapBytes`（64KB）单轮单类增量总量帽。权威快照会补全被帽截断的内容。

#### 3.8.2 `drain`：优雅排水

把「被杀」变成「自然收敛」。双件配合：轮 ctx 与根 ctx **取消脱钩**（值保留、
取消传播切断），`Drain` 是唯一收刀点。

```go
package myapp

import (
	"context"
	"time"

	"github.com/yi-nology/goagent-kernel/drain"
)

// ① 每轮派发：轮 ctx 从根 ctx 脱钩，轮内取消/超时/接管语义原样保留
func dispatch(rootCtx context.Context) context.Context {
	return drain.DetachCtx(rootCtx, drain.Budget(0)) // 0 = 缺省 60s
}

// ② 进程收到终止信号：等在途轮收敛，预算耗尽才强制取消
func shutdown(ctx context.Context, turns drain.Turns) {
	drain.Drain(ctx, turns, drain.Budget(0)) // 恒无返回值，排水失败不阻断关停链
}

// ③ 预算来自配置时（最常见的形态）：两个解析入口**不可互换**
func shutdownFromConfig(ctx context.Context, turns drain.Turns, cfgDrain time.Duration) {
	// 收尾预算（给 WithTimeout 用的硬上限）→ ShutdownBudget：d <= 0 一律归缺省，
	// 返回值**恒为正**，喂给 WithTimeout 是安全的。
	ctx, cancel := context.WithTimeout(ctx, drain.ShutdownBudget(cfgDrain)+15*time.Second)
	defer cancel()

	// 轮排水预算 → Budget：负值是「关闭排水」的配置语义，**必须原样**传给
	// Drain / DetachCtx。它和上面那个不是同一个数。
	drain.Drain(ctx, turns, drain.Budget(cfgDrain))
}
```

> **为什么是两个函数而不是一个**：配置里的一个 `time.Duration` 同时承载了两件事——
> 「要不要做轮排水」（负 = 关闭）与「停机收尾给多久」（必须恒正）。压成一个数，
> 就必然有人把负值喂进 `context.WithTimeout`，得到一个**立即过期**的 ctx。
> `Budget` 保留负值透传、`ShutdownBudget` 归一负值，是**刻意**把这两件事分开，
> 不是两个重复的解析器。

> **类型层面也分开了**：`Budget` 返回命名类型 `drain.DrainBudget`，`ShutdownBudget`
> 返回普通 `time.Duration`。故把前者塞进 `context.WithTimeout` **编译不过**：
> `cannot use Budget(...) (value of type DrainBudget) as time.Duration value`。
> 也就是说上面这条「别喂错」不靠记性，靠编译器。真要把排水预算当普通时长用时
> 显式写 `time.Duration(b)`——那个转换就是一次「我知道自己在做什么」。
>
> ⚠️ **v0.17.0 破坏性变更**：`Drain` / `DrainSession` / `DetachCtx` 的 budget 参数
> 类型改为 `DrainBudget`。既有调用点用 `drain.Budget(x)` 包一层即可，
> **行为零变化**（`Budget` 对正值与负值都原样返回，只有 0 补缺省，与旧签名逐字相同）。
> 这一步刻意不做兼容包装：兼容包装会让「直接传原始配置值」继续编译，
> 而那个写法正是本次要消灭的静默硬切入口。

**坑**

- **`Budget` 与 `ShutdownBudget` 不可互换，这是本包最贵的一次踩坑（harness#24）**。
  `Budget` 的负值是「关闭排水」的**配置语义**，必须原样交给 `Drain` / `DetachCtx`；
  而把它的返回值喂给 `context.WithTimeout`——在 v0.17.0 之前会拿到一个**立即过期**
  的 ctx（收尾协程秒退、**且一句错都不报**，表现是「优雅停机静默变硬切」），
  v0.17.0 起直接编译不过。收尾预算一律走 `ShutdownBudget`（`d <= 0` 归缺省，
  返回值恒为正）。
  历史上下游宿主因此都在 `main.go` 里手写了 `d<0→0` 前置归一——
  **那不是各自多虑，是本节的接线范式当时漏了这条**（修 API 而没修手册，
  等于修复停在库里）。
- **不要「顺手」把 `Budget` 的负值也归一**。那会把「关闭排水」静默变成
  「排水 60 秒」：方向从「少做」翻成「多做」，而配置里写下的意图被吞掉，
  比原缺陷更坏。`drain_test.go` 的 `TestBudget` 与
  `TestBudgetAndShutdownBudgetAreNotInterchangeable` 钉着这两个方向。
- **这是本包唯一危险的使用前提**：脱钩后必须仍有一条取消通路兜底（轮内超时 /
  `Drain` 强制收刀），否则轮会真的泄漏。
- **部署宽限期须 ≥ `DefaultTimeout`(60s) + 启动对账余量**，
  否则外层 SIGKILL 先于本包收刀，排水形同虚设。
- **实现 `Turns` / `SessionTurns` 须「快照后逐个调用」，不得持锁调取消回调**——
  取消会触发收尾路径，持锁重入即死锁。
- **`DrainSession` 空 sessionID 拒绝执行**，不退化成 `CancelAll`
  （退化成全局取消正是要消除的误伤）。
- **`CancelOf` 该会话无在途轮时必须无操作**，不得把 cause 广播给其他会话。
- 日志走 `logx.Delegating`，**不收回调**——留痕一律经包级 Delegating logger，
  宿主用 `logx.SetDefault` 换后端即可。

#### 3.8.3 `pending`：多轮挂起态载体

```go
package myapp

import "github.com/yi-nology/goagent-kernel/pending"

// ① 写入：唯一入口，保证列内容恒 JSON
func storePending(clarify string) string {
	return pending.ColumnValue(clarify, "disambig")
}

// ② 读路径只认 Encode 产物；非法/损坏一律降级为「无挂起」
func readPending(col string) (pending.State, bool) {
	s := pending.Parse(col)
	return s, s.Kind != ""
}
```

**坑**

- **必须走 `ColumnValue` 落列**。守卫原先只有 `strings.HasPrefix(clarify, "{")`，
  于是 `"{节点A}和{节点B}"`、`"{"` 这类串原样落列，`Parse` 解不出返回零值，
  **挂起态被静默丢掉**（不是降级，是消失）。透传条件已改成「真能 `Parse` 出带 `Kind`
  的 `State`」，坏串被当作 `Word` 保留，至少内容不丢。
- **读路径只认 JSON**，不留历史协议兼容——挂起态是瞬态，跨版本存量行早已结算完毕。

---

### 3.9 安全面：`netguard` + `pii`

#### 3.9.1 `netguard`：出站 SSRF 守卫

**三层判定**：名字层（解析前按名拒）+ 解析后逐 IP 过 egress + 重定向逐跳重验。

```go
package myapp

import (
	"github.com/oekit/pkg/errors"
	"github.com/yi-nology/goagent-kernel/netguard"
)

func guardOutbound(rawURL string) error {
	// 外部输入面（告警回推 / IM 推送）：零值放行面 = 私网与回环全拒
	// 凭证连通性探测：探测本机/内网自建服务是能力本身，用 AllowSelfService()
	// ——那个名字把意图留在代码里（这组比特也是 SSRF 穿透的入口）
	return netguard.CheckURLWith(rawURL, netguard.Allow{})
}

func guardRedirects(start string, hops []string) error {
	return netguard.CheckChainWith(start, hops, netguard.Allow{}, netguard.DefaultMaxHops)
}

// 扩展名单：整份原子交换，先全量校验再一次性交换
func updatePolicy() error {
	// 同时更新两项时必须用 SetPolicy —— 策略是两份数据合起来的
	return netguard.SetPolicy([]string{"internal-metrics.corp"}, []string{".corp"})
}
```

**坑**

- **只有 `CheckURLWith` / `CheckChainWith` 两个入口**（2026-11-06 破坏性删除）。
  此前的 `CheckURL` / `CheckChain` 双 bool 形态已删——两个 bool 的含义只能靠调用点
  约定区分，而放行面是安全边界；具名 `netguard.Allow` 让调用点自解释，且将来加
  第三个放行维度不必破签名。**零值 `Allow{}` 是严的**（私网与回环全拒），
  要放行必须显式写出来。
- **拨号层要接上，否则 rebinding 窗口仍在**。`CheckURLWith` 是**写面校验**
  （配置时判一次）；实际请求在之后由 HTTP 客户端再解析一次 DNS，两次之间权威
  DNS 可以改口——那是活的 SSRF 窗口。`netguard.DialFunc` 把同一套判定落到
  建连那一刻（名字层 + egress 预检 + 建连后核对真实 `RemoteAddr`），
  **接法见下节**。
- **名单分两组，口径不同**：回环族名字与回环地址同待遇，随 `Allow.Loopback`；
  其余名单项（精确名元数据/容器网桥名、`.internal/.local/.lan` 后缀）**无条件拒绝，
  不看旗标**。`Allow.Private` 的语义是「放行 RFC1918 私网**地址**」，
  不是「放行任意私有命名空间的**主机名**」。
- **内置默认名单不可被削减**：`SetPolicy` 只能增补扩展位。
- **后缀校验发生在注入期**：无前导点的后缀被拒（`corp` 会把语义从「拦 foo.corp」
  悄悄变成「拦一切以 corp 结尾的名字」，比不拦更糟）。任一项非法则**旧策略原样保留**，
  不做部分应用。
- **代次只是信号，不是响应**：两次比对之间换了两次，代次说不出「变成了什么」，
  用 `Subscribe` 接上这半句。
- **`Subscribe` 注册后立刻回调一次当前快照**，否则订阅者带着从没填过的视图上线
  （比没有订阅更危险，因为它以为自己已经跟上了）。
- **`CheckChain` 返回 nil 不代表整条 hops 都被校验过**——空 Location 终止链条，
  其后的 hops 不可达，既不校验也不占预算。
- **`ResolveLocation` 必须解析而非直接送 `CheckURL`**：Location 头允许相对地址；
  协议相对地址 `//host/x` 更危险，不解析看不出跨主机跳转。
- `netguard` **不在工具调用的判定环上，这是有意的**——塞进去就得猜「哪个参数字段是 URL」，
  那是领域知识。

**什么时候不该用**

- **出站目标全是你自己声明的内网服务**——那不叫 SSRF 防护，叫自缚手脚；
  真要放行请写出来（`Allow.Private` / `Allow.Loopback`），别靠默认。
- **想用它判「工具参数里哪个字段是 URL」**——那需要领域知识，判据归宿主；本包判的是**字符串**。
- **只做写面校验就以为安全了**——两次 DNS 解析之间有 rebinding 窗口，必须接 `DialFunc`。

#### 3.9.1.1 拨号层接线：把 rebinding 窗口闭上

写面校验（`CheckURLWith`）与实际请求分属两次 DNS 解析，两次之间权威 DNS 可以改口
（`evil.example` 写面解析成公网地址、实际请求解析成元数据地址）。**接线是宿主的
决策，但机制本仓已经备好**——只剩把 `Transport.DialContext` 换成 `netguard.DialFunc`。

最省事的接法（`NewClient` 把「守卫挂在传输层」收成一行，并保留宿主自己的
client 注入口）：

```go
import "github.com/yi-nology/goagent-kernel/netguard"

// 生产：零值放行面 = 私网与回环全拒
hc, err := netguard.NewClient(nil, netguard.Allow{})
if err != nil {
	return err
}
resp, err := hc.Get(targetURL)
```

已有自己的 `*http.Transport` 时：

```go
tr := netguard.NewTransport(myTransport, netguard.Allow{})
hc := &http.Client{Timeout: 5 * time.Second, Transport: tr}
```

**三条接线纪律**：

1. **两层放行面必须同一个值**。`Allow` 是校验层与拨号层**共用的类型**
   （`CheckURLWith` / `CheckChainWith` / `DialOptions.Allow`），映射到上游 egress
   也只有一处实现。口径不一致的后果是**分层放行**：校验层拒、拨号层放行——后者
   才是安全问题。
2. **逐 URL 的策略无法在拨号层表达**（拨号按连接、校验按 URL）。此时取
   **拨号层最宽、校验层逐条**：拨号层放行校验层可能放行的全部范围，但**不放行
   校验层恒拒的段**（链路本地/元数据、保留段）——那些段的拒绝与放行面无关。
3. **测试要连本机假服务器时，只在测试里放宽回环**：

   ```go
   hc, err := netguard.NewClient(&http.Client{Timeout: 2 * time.Second},
       netguard.Allow{Loopback: true})   // 只在测试里
   ```

   **不要**为了省事把生产配置一起放宽——那等于把刚补上的窗口重新打开。
   若客户端的 `Transport` 是自定义 `RoundTripper`（不是 `*http.Transport`），
   `NewClient` 会**报错**而不是悄悄放行：拨号发生在传输层内部，挂不上就没有
   拨号层防线。

#### 3.9.1.2 判定要可测：解析器接缝

`netguard` 的判定要**解析域名**才知道答案是公网还是私网——这让「判定对不对」这件事
被绑在「这台机器此刻能不能解析这个域名」上。真实后果：CI 干净 runner 绿、机器一忙
就假红（解析器超时后报 `no such host`），而红点看起来像「判定放行了私网」，
排查方向整个跑偏。

本版（v0.43.0）给了判定面一条**解析器接缝**——同一套判定，解析来源可注入：

```go
import (
	"context"
	"net"

	"github.com/yi-nology/goagent-kernel/netguard"
)

// 宿主自己的解析器（测试里则是一个表驱动的假解析器）
type myResolver struct{ table map[string][]net.IP }

func (r myResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if ips, ok := r.table[host]; ok {
		return ips, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

err := netguard.CheckURLWithResolver(ctx, "https://svc.internal/x", netguard.Allow{}, r)
err = netguard.CheckChainWithResolver(ctx, start, hops, netguard.Allow{}, netguard.DefaultMaxHops, r)
```

三点取舍：

- **additive，既有入口逐字不变**：`CheckURLWith` / `CheckChainWith` 的行为不变；
  `r == nil` 等价 `net.DefaultResolver`（缺省路径仍是真实 DNS）。
- **接缝只换解析来源，不换判定**：名字层、逐 IP、逐跳的次序与结论口径完全不变——
  注入解析器不会让判定变松，也不会让判定变紧。
- **链条面也有**：逐跳校验里**每一跳**都要解析一次，所以只给单跳接缝等于留半个洞；
  `CheckChainWithResolver` 是本版补齐的（issue #412）。

**什么时候该用**：① 你要给判定写**确定性**用例（不能靠真实 DNS）；② 你的宿主自己有
解析策略（split-horizon DNS、内网解析器），要让判定**跟你的解析器走**而不是跟系统
解析器走——这两种情况语义不同，安全含义也不同。

**坑**

- **把接缝当成「放宽判定」的后门**。它只换解析来源：注入一个返回公网地址的解析器，
  判定就会放行。所以**注入的解析器只该出现在测试与宿主自有解析策略里**，
  绝不能从外部输入构造。
- **以为传 nil 就能不解析**。`r == nil` 等于「用系统解析器」；想让判定不依赖 DNS，
  就必须真的注入一个解析器。
- **只给单跳接缝、忘了链条面**。逐跳校验每一跳都要解析；只迁移单跳用例的话，
  链条用例仍会在真实 DNS 上假红。
#### 3.9.2 `pii`：个人信息脱敏

在 `logredact`（凭据类值模式）之外补「个人信息价值面」——大陆手机号与 18 位居民身份证。

```go
package myapp

import "github.com/yi-nology/goagent-kernel/pii"

func main() {
	// 装配期：面名自查（fail-closed 是保险，不是日常路径）
	ss, err := pii.ParseSurfaces("input", "output", "tool_results")
	if err != nil {
		panic(err)
	}
	pii.SetEnabled(true)  // 缺省开 = secure by default
	pii.SetSurfaces(ss)   // 缺省三面全开

	// 策略热重载订阅（注册即收到一次当前快照）
	h, err := pii.Subscribe(func(p pii.Policy) {
		// 用回调参数 p 的只读方法，不要改调包级访问器（那可能已是下一代）
		metrics.Gauge("pii.strategy", string(p.Strategy()))
	})
	if err != nil {
		return
	}
	defer pii.Unsubscribe(h)
}

func guardOutflow(userMsg, modelAns string, toolResult any) error {
	// 面未启用/全局关闭 → (原文, false)；检出且策略=block → (文本, true)
	auditText, b1 := pii.Apply(pii.SurfaceInput, userMsg)
	shownText, b2 := pii.Apply(pii.SurfaceOutput, modelAns)
	toolPayload, b3 := pii.ApplyValue(pii.SurfaceToolResults, toolResult)
	if b1 || b2 || b3 {
		return errBlocked // ★ 阻断决策归宿主，本包只回报信号
	}
	_, _, _ = auditText, shownText, toolPayload
	return nil
}
```

**坑**

- **默认策略是 `mask`，不得改成 `block`**——block 会打断整条审计链，
  要用必须宿主显式 `SetStrategy` 并自行履行阻断。
- **`hash` 档必须配套密钥**：`SetDigestKey` 注入 ≥16 字节的 HMAC 密钥（建议走
  环境变量等密钥通道）。摘要 = 域分离 HMAC-SHA256 截断——同值同摘要、异值异摘要，
  但**没有密钥的摘要只是可离线穷举的明文等价物**（手机号域约 9×10⁹），所以
  缺密钥时 hash 档 fail-closed：整段替换 `("[redacted]", true)`，不出摘要。
  接线期用 `DigestKeyed()` 自查。
- **未登记面名 fail-closed**：`Apply` 遇未知面返回 `("[redacted]", true)`——
  整段替换 + 要求阻断，**不 panic、不静默放行**。面未登记与「宿主显式关闭」是两件事，
  塌缩成同一状态等于拼错一个字符即可让该面完全不脱敏。
- **map 的键与值走同一条判定面**：以手机号/身份证为键的载荷不再整键明文外泄，
  键命中与值命中同样产出处置与 blocked 信号。普通键（`order_id` 等）逐字不变；
  不同原键处置出同一形态时按原键字典序加 `#n` 后缀消解——不丢条目、不依赖
  map 迭代顺序。
- **策略是单原子指针，不可撕裂读**。旧的三个独立原子变量在热重载下会产出从未被任何
  写侧设置过的组合。**处置路径必须经 `CurrentPolicy()` 拿快照**，
  不得分字段调访问器再拼。
- **`Policy` 快照可读**：字段仍全部非导出、**没有任何 setter**，但提供一组只读
  访问方法（`Gen()` / `Enabled()` / `Surfaces()` / `SurfaceEnabled()` /
  `Strategy()` / `DigestKeyed()`）。订阅回调参数就是**收到的那一代**；回调里不要
  改调包级访问器——那读的是此刻的全局，可能已经换代。
- **并发换策略时通知按代次单调**：并发写侧的变更可能被合并为最新一代，
  但订阅者绝不会先看到新一代再看到旧一代（每份快照可用 `p.Gen()` 断言）。
- **识别前做数字形态归一化**：全角数字/字母折半角、删除零宽与不可见字符、
  折叠数字间的空格/`-`/`.` 分组（分组折叠只对整段 11 位手机号 / 18 位身份证 /
  `86`、`0086` 前缀形态生效，避免把相邻数字 token 拼成假号）。`Apply` 的输出是
  归一化后的打码形态（`138 1234 5678` → `138****5678`），hash 档对同一值的不同
  书写形态产出同一摘要。仍不覆盖：阿拉伯-印度数字、字母/汉字夹在数字之间、
  数字间夹 `+` 等非分隔符。
- **订阅者 panic 被逐个 recover**。回调里**可以**再调 `SetStrategy`（无锁不自死锁），
  但会再触发一轮通知，**递归深度由订阅者自己收敛**。
- **15 位旧身份证不检**（已停用 20 年）；**裸 `86` 前缀不认**，只认 `+86` / `0086`。
- **不下探标量**：数字/布尔原样返回——JSON number 形态的手机号不在覆盖面。
- **未登记容器形状 fail-closed**：`[]map[string]string`、`map[string][]string`、
  `[]byte`（含 `json.RawMessage`）、struct 等未登记结构不再静默透传——
  `ApplyValue` 整段替换为 `"[redacted]"` 并回报 blocked，宿主不会再把
  「根本没看」误读成「没命中」。接线期用 `ValidateShape` 对典型载荷自查，
  报错带可定位路径（如 `$.items[0].blob`）。
- **`Redact` / `RedactValue` 已于 2026-11-06 删除**（面无关且不回报是否阻断——
  宿主把策略配成 `block` 时它拿不到信号，等于只能靠自觉）。唯一入口是
  `Apply` / `ApplyValue`，**必须显式指定面**；每个出口声明自己在哪一面，
  这条从语义约束变成调用点可见的事实。

---

**什么时候不该用**

- **你要脱敏的不是个人信息**（密钥、令牌）——那是值模式那一类，不是本包的价值面。
- **想要一个「自动决定脱不脱」的策略**——本包 fail-closed：未登记的面、未登记的容器形状、
  未注入密钥的 hash 档，一律整段替换 + 要求阻断；**阻断动作仍归宿主**。
- **指望它识别任意标识符**——覆盖面只到大陆手机号与 18 位居民身份证。

### 3.10 副作用只做一次：`retryplan` + `sideeffect` + `sidetest`

这三个包是一条链上的三段：**`retryplan` 判「该怎么重试」→ `sideeffect`
保证「重试了不会重复施加」→ `sidetest` 验收「你的存储实现守住了契约」**。

**什么时候需要它们**：你的编排会**对外部系统施加动作**（改配置、发布、重启、
写数据库），而这些动作**重复执行会出事**。纯读取的编排不需要。

#### 3.10.1 `retryplan`：该走哪条重试路径

上游 `toolfail` 判「该不该重试、能不能原样重发」，但它对超时类只说
「**先确认上一次调用是否已经生效**」——**没说什么怎么做到**。
`retryplan` 把两问合成一条可执行结论：

```go
import "github.com/yi-nology/goagent-kernel/retryplan"

d := retryplan.Decide(err, retryplan.Options{})
switch d.Path {
case retryplan.PathNone:
    // 不重试。d.Retryable 仍可能为 true（值得重试但没有安全路径），
    // 按 d.NeedsOperator / d.Reason 决定是找人工还是改参数。
case retryplan.PathDirect:
    // 原样重发安全，直接重试。
case retryplan.PathReplayed:
    // 值得重试但原样重发不安全 —— **必须走幂等键重放**（见 3.10.2）。
}
```

**三档路径的由来**：

| 情形 | 路径 | 为什么 |
|---|---|---|
| 不可重试 | `PathNone` | 终态，换处置路径 |
| 可重试 + 原样重发安全 | `PathDirect` | 直接重发 |
| 可重试 + 原样重发不安全 + **有**幂等键 | `PathReplayed` | 走幂等键重放 |
| 可重试 + 原样重发不安全 + **无**幂等键 | `PathNone` | **不许猜着重试** |

最后一行是本包的关键取舍：「值得重试但不确定能不能重发」+「没有幂等键兜底」
= **不能重试**。此时正确处置是如实失败交上层降级/人工——赌一次的代价是一次
重复副作用，正是这套机制要防的那件事。

**你该提供什么**：把 `Options.WithoutIdempotency` 按实际能力填。
你有幂等键就留 false（缺省），没有就置 true——**别谎报有**。

#### 3.10.2 `sideeffect`：幂等键 + 租约 + 栅栏

本包提供「对外部世界施加的一次动作**至多一次**」的跨进程契约。
你实现 `Store` 接口（落库），内核给判据。

```go
import "github.com/yi-nology/goagent-kernel/sideeffect"

runner := sideeffect.NewRunner(myStore, 30*time.Second)

act := sideeffect.Action{
    Key:      sideeffect.Key("run-42/step-7/restart"),
    Resource: "node-a",        // 独占粒度：同一资源同一时刻至多一个持有者
    Payload:  payloadBytes,
}

outcome, res, err := runner.ExecuteWith(ctx, act, sideeffect.Effect{
    // 准备阶段：算结果、构造请求，**不施加副作用**。
    Prepare: func(ctx context.Context) (any, error) { return buildRequest(ctx), nil },
    // 施加阶段：真正对外部世界动手。
    //
    // lease 里带**栅栏令牌**（lease.Fence）：把它传给下游，下游才能拒收
    // 过期持有者的迟到写。没有它，栅栏就只剩上游那半截。
    Apply: func(ctx context.Context, prepared any, lease sideeffect.Lease) (sideeffect.Result, error) {
        return sideeffect.Result{Payload: doIt(ctx, prepared, lease.Fence)}, nil
    },
})
```

**四个必须知道的点**：

1. **`Key` 是幂等键，同一逻辑动作的所有重试必须用同一个**。用不同 Key 重试
   等于声明「这是两个不同的动作」，本包不拦（那是你的语义错误）。
2. **`ExecuteWith` 拆成三段是为了在施加前检查持有资格**。准备阶段可能很慢，
   而租约可能在这段时间里到期被别人接管——若不加检查就施加，就是**两份闸
   同时开着**，副作用施加两次。`Execute`（单段）也做这个检查。
3. **执行失败不记账**（没做成的不能记成做成了），但**会释放闸**（否则失败残留的
   闸把后续重试全挡成「别人占着」，动作从此做不成）。
4. **v0.32.0 起：施加失败且无法判定远端是否已生效 ⇒ 结果未知，禁止重试**。
   「响应丢了」与「远端没做」在调用方看来完全一样，而前者重试就是**重复施加**。
   故闸把该 Key 转入「结果未知」（`OutcomeUncertain`），必须先对账：

   ```go
   // 施加失败后：同一个 Key 再来一次会被**拒**（动作体不会被再次调用）
   outcome, _, err := runner.Execute(ctx, act, fn)   // outcome == OutcomeUncertain

   // 宿主查下游事实后裁决：确认已生效 → 记为完成；确认未生效 → 允许重试
   _, _, err = runner.Reconcile(ctx, act, sideeffect.Resolution{
       Applied: true,                       // 已生效
       Result:  sideeffect.Result{Payload: resultBytes},
       Reason:  "查下游：动作已生效",
   })
   ```

   只有当你的下游**按幂等键去重**时，才可以在 `Action.RetrySafety` 里声明
   `sideeffect.RetrySafetySafe`——那等于断言「重试不会重复施加」。**断言错了
   就是重复施加**，故它是显式选择，且不声明时按最弱处理（结果未知 + 对账）。

5. **v0.33.0 起：闸会记住「本次尝试还在」（执行意图）**。

   `Acquire` 在授予执行权时**顺手**把「这次尝试还没结论」原子地持久化在 Key 上。
   此前这条信息只存在于**进程内存**里——于是「进程在施加成功之后、记账之前
   直接死掉」这一形态在存储侧**不留任何痕迹**：既没有「完成」也没有「未知」，
   下一次同一 Key 的 Acquire 把它当成「没做过」⇒ 重新授予执行权 ⇒
   **同一动作被施加两次**，而调用方全程只拿到一个普通错误。

   有了意图之后：

   - 租约过期且**未裁决** ⇒ 同一 Key 转入**需对账态**（`OutcomeUncertain`），
     **不**重新授予执行权（声明了 `RetrySafetySafe` 的除外——那种情况下
     重做安全，因为下游按幂等键去重）；
   - 两条出口因此必须**分开**（v0.33.0 新增 `Store.Abandon`）：

     | 场景 | 存储操作 | 含义 |
     |---|---|---|
     | 确定没生效 / 下游会去重 | `Abandon`（撤销意图 + 放闸） | 该 Key 重新可执行 |
     | 可能已生效 | `MarkUncertain`（转未知态）+ `Release`（**只**放闸） | 该 Key 继续被挡住 |

   **在「可能已生效」那条路上误用 `Abandon` ⇒ 意图被撤 ⇒ 该 Key 重新变成
   「可被执行」⇒ 重复施加的入口重新打开**，且失效是**静默**的。两条出口分开
   就是为了让这个误用不再是「顺手就能写出来」的代码。

**下游资源侧要拒收陈旧写入**：若你的目标系统能被并发写入，实现
`FencedSink`——它钉了两件必须全局一致的事：比较口径（**小于**已见过的最大令牌
才算陈旧，等于合法）与**原子性**（比较与写入须同一次原子操作）。
写成「先读已见最大 → 判断 → 再写入」三步的话，两个持有者会同时读到同一个旧值，
双双通过检查，然后**更旧的那次写入反而后落**——更新的数据被旧的覆盖。

**承诺边界**：本包承诺「**至多一次**」，**不承诺 exactly-once**。
后者要求**下游也接受幂等键**（把 Key 传到下游写入请求里），那是你的接线责任。
把上界说成 exactly-once 是本包明确拒绝的一种失真。

**你该提供什么**：`Store` 的实现（Redis / PostgreSQL / etcd 皆可）。
`Acquire` **必须是存储侧的一次原子操作**（一条带条件的 UPDATE / 一个 Lua 脚本 /
一个 CAS 事务）——拆成「读闸 → 判 → 写闸」三步，去重就失效了。

**什么时候不该用**

- **动作天然幂等**（`PUT` 到固定路径、写同一个值）——直接重试即可，不需要跨进程闸。
- **动作只在单进程内发生、进程崩了就作废**——那不必付跨进程原子性的成本。
- **想让本包保证 exactly-once**——它承诺的是**至多一次**；
  exactly-once 要求**下游也接受幂等键**，那是接线责任，不是本包能给的。

#### 3.10.3 `sidetest`：给你的存储实现一份验收标准

`Store` 的正确性无法靠「看起来对」保证，而**每家宿主都会自己写一遍存储实现**。
`sidetest.Run` 把「宿主实现必须满足什么」固化成 **34 条**可复跑的子测试
（v0.32.0 新增「结果未知」一族五条；v0.33.0 新增「执行意图」一族六条，
外加「闸已释放后续期被拒」一条）。下游若实现了 `FencedSink`，另跑
`sidetest.RunSinkContract` 的 **6 条**栅栏契约子测试：

```go
import "github.com/yi-nology/goagent-kernel/sidetest"

func TestMyStore(t *testing.T) {
    sidetest.Run(t, func(t *testing.T, clock *sidetest.FakeClock) sidetest.ClockedStore {
        return newMyStore(clock)   // 你的实现要支持注入时钟
    })
}
```

**你的实现须实现 `ClockedStore`**（在 `Store` 之上加一个 `UseClock(now func() time.Time)`）。
这不是可选的：过期场景若读真实时间就恒不触发，套件会退化成一组**空断言**——
全绿，但什么都没验。

**套件硬失败，不静默跳过**：显式接入套件＝声明「我这个实现要满足副作用闸的契约」，
此时跳过一个场景等于把「我没测过」伪装成「我通过了」。

**范式沿用上游 `oekit/pkg/cache/cachetest` 与 `pkg/singleflighttest`**（同家族的做法）。

**注意**：本包**不提供**参考实现。一份「正确的内存实现」会被当成生产可用的存储，
而它恰恰**不具备本包最核心的那条性质**（跨进程）——单进程内存实现无法提供
「Acquire 是跨进程原子操作」，拿它上生产＝以为自己去重了，实际两个进程各做一次。

**什么时候不该用**

- **你的实现不想支持注入时钟**——那套件会退化成一组**空断言**：全绿，但什么都没验。
  与其跑一个假的绿，不如不接。
- **你想拿套件当参考实现抄**——本包**不提供**参考实现：
  一份「正确的内存实现」不具备本包最核心的那条性质（跨进程），照抄上生产即等于自欺。

#### 3.10.4 接线范式：`retryplan` → `sideeffect`

前两节是分开的，宿主真正要连的是这一段：**决策说要走幂等键路径，
那就用同一个幂等键再跑一次**。

```go
// 第一次尝试
outcome, res, err := runner.ExecuteWith(ctx, act, effect)
if err == nil {
    return res, nil
}

// 失败了：问该不该重试、该怎么重试。
d := retryplan.Decide(err, retryplan.Options{})
switch d.Path {
case retryplan.PathNone:
    return sideeffect.Result{}, err          // 不重试，如实失败
case retryplan.PathDirect:
    // 原样重发安全：直接重发（不用幂等键）。
    return doRaw(ctx, act)
case retryplan.PathReplayed:
    // 走幂等键重放：**用同一个 act.Key** 再跑一次。
    // 若第一次其实已生效，这次会取回既有结果（OutcomeReplayed），
    // 而不会重复施加。
    out2, res2, err2 := runner.ExecuteWith(ctx, act, effect)
    if err2 != nil {
        return sideeffect.Result{}, err2
    }
    if out2 == sideeffect.OutcomeContended {
        // 别人正占着这个资源：退避后重试整个流程。
        return sideeffect.Result{}, errContended
    }
    return res2, nil
}
```

<div align="center">
<svg width="760" viewBox="0 0 760 320" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system,'PingFang SC','Microsoft YaHei',sans-serif">
<rect x="0" y="0" width="760" height="320" rx="10" fill="#1c2330" stroke="#262e3d"/>
<text x="16" y="26" font-size="13" fill="#e6edf3">「只做一次」的两段接线：决策说走哪条路，闸负责不再做第二次</text>
<rect x="16" y="44" width="204" height="226" rx="10" fill="#161b26" stroke="#262e3d"/>
<text x="32" y="68" font-size="11.5" fill="#e6edf3">retryplan.Decide(err)</text>
<rect x="32" y="84" width="172" height="42" rx="8" fill="#1c2330" stroke="#262e3d"/>
<text x="46" y="102" font-size="9.5" fill="#9aa7b8">PathNone</text>
<text x="46" y="118" font-size="8.5" fill="#9aa7b8">不重试，如实失败</text>
<rect x="32" y="134" width="172" height="42" rx="8" fill="#1c2330" stroke="#58a6ff"/>
<text x="46" y="152" font-size="9.5" fill="#58a6ff">PathDirect</text>
<text x="46" y="168" font-size="8.5" fill="#9aa7b8">原样重发安全，可直接重发</text>
<rect x="32" y="184" width="172" height="42" rx="8" fill="#1c2330" stroke="#a371f7"/>
<text x="46" y="202" font-size="9.5" fill="#a371f7">PathReplayed</text>
<text x="46" y="218" font-size="8.5" fill="#9aa7b8">必须带幂等键重放</text>
<text x="32" y="248" font-size="8.5" fill="#9aa7b8">可重试但不确定能否重发</text>
<text x="32" y="264" font-size="8.5" fill="#9aa7b8">+ 没有幂等键 ⇒ PathNone</text>
<line x1="222" y1="205" x2="242" y2="205" stroke="#a371f7" stroke-width="1.5"/>
<polygon points="245,205 240,201.5 240,208.5" fill="#a371f7"/>
<text x="234" y="196" font-size="7.5" fill="#9aa7b8" text-anchor="middle">同一 Key</text>
<rect x="246" y="44" width="498" height="226" rx="10" fill="#161b26" stroke="#3fb950" stroke-width="1.5"/>
<text x="262" y="68" font-size="11.5" fill="#3fb950">sideeffect 闸：同一个 act.Key 再跑一次</text>
<rect x="262" y="84" width="466" height="46" rx="8" fill="#1c2330" stroke="#3fb950"/>
<text x="278" y="104" font-size="9.5" fill="#3fb950">OutcomeReplayed：该 Key 已完成</text>
<text x="278" y="121" font-size="8.5" fill="#9aa7b8">取回既有结果，不施加第二次</text>
<rect x="262" y="138" width="466" height="46" rx="8" fill="#1c2330" stroke="#f85149"/>
<text x="278" y="158" font-size="9.5" fill="#f85149">OutcomeUncertain：结果未知</text>
<text x="278" y="175" font-size="8.5" fill="#9aa7b8">禁止盲目重试，先查下游事实再 Reconcile</text>
<rect x="262" y="192" width="466" height="46" rx="8" fill="#1c2330" stroke="#58a6ff"/>
<text x="278" y="212" font-size="9.5" fill="#58a6ff">OutcomeContended：资源被占</text>
<text x="278" y="229" font-size="8.5" fill="#9aa7b8">退避后重试整个流程（不是重发这一次）</text>
<text x="262" y="258" font-size="8.5" fill="#9aa7b8">Acquire 必须是存储侧的一次原子操作</text>
<rect x="16" y="282" width="728" height="28" rx="8" fill="#161b26" stroke="#a371f7"/>
<text x="32" y="300" font-size="9.5" fill="#e6edf3">核心不变量：走 PathReplayed 必须用同一个 act.Key——换了 Key 就是两个动作，去重失效</text>
</svg>
</div>

*图 7：左边只回答「该走哪条重试路径」，右边保证「再跑一次不会变成两次副作用」；两段之间靠幂等键对齐。*

**接线的核心不变量**：走 `PathReplayed` 时**必须用同一个 `act.Key`**。
换了 Key 就是两个不同的动作，去重失效——这正是本包要防的事。

**退避**：`d.Backoff` 与 `d.RetryAfter` 给了退避建议（限流类必须退避：
立刻重试只会立刻再被限流，把一次退避能吸收的拥塞放大成重试风暴）。

### 3.11 审批 × 档位的联合判据：`gatecheck`

审批层（上游 `toolgate/policy`）与沙箱层（上游 `guardx/sandbox`）各自分属两个上游模块，**互不知情**
（`toolgate/policy` 不 import `guardx/sandbox`，反之亦然）。于是
「两层是否都同意」这个判定**没有单点**——它落在每个宿主手里，必然漂移：
同一个调用在 A 宿主被判「需人审」、在 B 宿主被判「自动放行」。

`gatecheck` 把它收成**一条**判定（**只做合成，不重新实现任何一层**）。

```go
import (
	"github.com/yi-nology/goagent-kernel/gatecheck"
	"github.com/oagentkit/toolgate/policy"
	"github.com/oagentkit/guardx/sandbox"
)

// 装配一次，之后每次执行走同一套判据（次序固定在一处）。
g := gatecheck.NewGuard(policyGate, sandboxRegistry)

out, dec := g.Run(ctx, gatecheck.Request{
	Op:       policy.Op{Type: policy.OpExecuteStep, Mode: policy.ModeAuto},
	MinLevel: sandbox.LevelOS,
	Budget:   sandbox.Budget{MaxMemory: 1 << 30},
	Program:  program,
})
switch dec.Outcome {
case gatecheck.OutcomeProceed:
	// out 已执行完成
case gatecheck.OutcomeNeedHuman:
	// 去问人（不要去换后端）
case gatecheck.OutcomeDenied:
	// 这次操作被否
case gatecheck.OutcomeLevelShort:
	// 档位不足 / 预算兜不住 ⇒ 宿主该换环境
case gatecheck.OutcomePlanOnly:
	// 计划模式：只审不执行
}
```

**判据顺序是有意的**：

| 审批层 | 沙箱层 | 结论 | 写反的后果 |
|---|---|---|---|
| 拒绝 | 任意 | **拒绝** | 档位不足 → 宿主去**换后端**，而该做的是「这次被否了」 |
| 需人审 | 选不到 | **需人审** | 档位不足 → 宿主**不找人** ⇒ 这次调用既没执行也没人管，**静默丢失** |
| 放行 | 选不到 | 拒绝（档位不足） | 降级放行 ⇒ 「以为兜住了、实际没有」 |

**「需人审」必须优先于「档位不足」**：两条拒绝对宿主处置完全不同——
档位不足该**换环境**，需人审该**去问人**。先报哪条决定宿主去找谁。

**两处 fail-closed**：没装审批门 ⇒ 按需人审；要求了档位却没有登记簿 ⇒ 拒绝。

**受管后端（`gatecheck.Managed`）**：把真后端包起来，判据在 `Exec` 内部、
紧挨着执行，**不可绕过**——否则「判过的后端 ≠ 实际跑的后端」又有了信号落差。

```go
// 把 ptc.SubprocessProvider（或任何 sandbox.Provider）包成受管后端，
// 注册进登记簿即可。
managed := gatecheck.NewManaged(inner, policyGate, sandboxRegistry, sandbox.LevelProcess)
_ = sandboxRegistry.Register(managed)
```

**承诺边界**：本包只判「两层是否都同意」，**不判「实际隔离强度是否兑现声明」**
——档位是后端自报的，内核不校验其真实性（那要审后端实现，不是判据能做的事）。

**什么时候不该用**

- **你只有一层**（只有审批没有档位，或只有档位没有审批）——直接用上游的 `policy.Stage`
  或 `sandbox.Execute` 就够，不必再叠一层判定。
- **执行后端不受你控制**——本包只判「两层是否都同意」，**不判实际隔离强度**；
  档位是后端自报的。
- **想要本包替你选后端**——它不选；`sandbox.Registry.Resolve` 选不到满足档位的后端就是**拒绝**，
  不降级。

### 3.12 门包清零之后：这几个包去哪找

v0.35.0 起，本仓**不再提供** `policy` / `reminder` / `toolfail` / `sandbox` / `dispatch`
五个门包（`architecture-policy.yaml` 的 `facades` 段是**空清单**）——它们整包迁到了上游矩阵，
宿主**直连上游**：

| 本仓旧名 | 现居上游 | import 路径 |
|---|---|---|
| `policy` | 审批 / 审计门 | `github.com/oagentkit/toolgate/policy` |
| `toolfail` | 工具失败分类 | `github.com/oagentkit/guardx/toolfail` |
| `sandbox` | 执行沙箱的档位与后端协议 | `github.com/oagentkit/guardx/sandbox` |
| `reminder` | 系统注入提示的防伪包裹 | `github.com/oagentkit/guardx/injectband` |

⇒ **本仓现在一个门包都没有**。判定语义与实现都在上游那一侧，宿主 import 上游路径即可：

- `policy`：`Gate` / `Decision` / `Op` / `Stage` / `AuditGate` / `LoadOverrides` /
  `Normalize` / `Mutating` / `PolicyRejectedPrefix`。
- `toolfail`：**9 个** `Class`（`ClassTransient` / `ClassPermanent` / `ClassAuthentication` /
  `ClassNotFound` / `ClassPermissionDenied` / `ClassInvalidArgument` / `ClassRateLimited` /
  `ClassSelfReported` / `ClassUnknown`）+ `PolicyOf` 的策略表
  （`Retryable` / `Backoff` / `SurfaceDelay` / `NeedsOperator` / `SafeToResubmit` /
  `Remediation`）。
- `sandbox`：`Level`（四档）/ `Provider` / `Registry` / `Budget` / `Capable` / `FSFence`；
  **选不到满足档位的后端即 `ErrNoProvider`，不静默降级**。
- `injectband`：`Wrap` / `SanitizeBody`。

本仓保留的是**组合层**（`gatecheck` 合成审批 × 档位、`preset` 收成具名组合、
`egress` 判出口兑现）与**门面纪律**，不含判定本体。

⚠️ **`dispatch` 同名不同物，别混**：旧门包 `dispatch`（权限即拓扑）随 v0.35.0 整包删除，
消费方直连上游 `github.com/oagentkit/agent/dispatch`（allow 矩阵 / 深度上限 / 自派发拒绝，
见本仓 `dispatch/dispatch.go` 包头「它回答的是第四问」）。
本仓现存的 `dispatch`（v0.38.0 起，Experimental）是**派发域失败面**：
三档分类（transient / fatal / other）+ 同域/跨域兜底改派环，与「权限即拓扑」无关。
一句话分界：**可达性归上游，失败后还能不能派归本仓。**

**门包纪律仍在**（`harness-facade-stays-facade`，error 级，走 `go/ast`）：本仓若再设
门包，只允许四种声明——类型别名 / 常量别名 / 单条上游转发函数 / 允许集合内的 import；
加真逻辑（要新类型、要多条语句、或要新依赖）三者必居其一 ⇒ 当天即红。

#### `dispatch`：失败后还能不能派、派给谁

改派面（issue #169）：把「失败怎么分类」与「改派环怎么走」收成一条可执行结论——
**只回答第四问**，不碰可达性（上游 allow 矩阵 / 深度 / 自派发）、不做队列、也不答
「要不要原样重发」（那是 `toolfail` 的活）。

```go
package myapp

import (
	"context"

	"github.com/yi-nology/goagent-kernel/dispatch"
)

// 候选枚举接缝：候选怎么来是宿主的事（注册表 / 能力画像 / 启用开关全在宿主侧）
func candidates(slug string, tried map[string]bool, sameDomain bool) []string {
	return hostRegistry.Candidates(slug, tried, sameDomain)
}

// 执行步接缝：把候选 slug 当执行者跑一次（nil = 该候选失败）
func attempt(ctx context.Context, slug string) error {
	return hostRunner.Run(ctx, slug)
}

res := dispatch.StickyFailover(ctx, dispatch.Request{
	Slug:               firstFailedAgent,
	FirstErr:           firstErr, // 判定面只读**首次失败的原始错误**
	SameDomain:         3,       // 同域改派次数上限（0 = 禁用）
	CrossDomain:        true,    // 瞬态跨域兜底一次
	FailoverCandidates: candidates,
	Attempt:            attempt,
	OnEvent:            func(e dispatch.Event) { audit(e) },
})
if res.Slug == "" {
	return res.Err // 全部候选仍失败（Err 含改派说明，给人看）
}
return use(res.Slug, res.Err)
```

**坑**

- **判定面只读首次失败的原始错误**。累计错误里混进了改派说明
  （`%w（已同域改派 X 仍失败）`），二次判档读到的已不是真因——`FirstErr` 只放原始错误。
- **`Classify` 是两级判据，顺序不能换**：第一级查上游 `toolgate/policy.Disposition(err)`
  （**零文本匹配**，按 toolfail 类别分档）；链与协议层都判不出（`ClassUnknown`）才走
  第二级**双语文本兜底**。状态码判定**必须带 `\b` 词边界**——裸 `strings.Contains("403")`
  会被「省略中段 4032 字节」这类文本命中，把瞬态误判成 fatal（跨域兜底被错误抑制）。
- **fatal 档不进任何改派**：换执行者也是白跑（同域兄弟与跨域候选共享同一套凭证与
  权限面）⇒ 直接终态。
- **候选枚举与执行都是接缝**：本包不排序候选、不发事件、不落库——那些是宿主资产。

#### `policy`：操作审计门（上游 `toolgate/policy`）

```go
package myapp

import "github.com/oagentkit/toolgate/policy"

// 缺文件回内置基座，阈值只降不升
p, err := policy.LoadOverrides(policyFS, "_shared/operation_policy.yaml") // 路径由宿主传入
g := policy.NewGate(p, nil)              // p 为 nil 用内置基座；arbiter 为 nil 则灰区直接升人审

d := g.Decide(ctx, policy.Op{
	Type:     policy.OpToolCall,
	Mode:     policy.Normalize(sessionMode), // 空/非法一律回落 confirm
	Tool:     "restart",
	Mutating: policy.Mutating(true), // 三态 *bool：nil=未知 ⇒ fail-safe 升人审（v0.28.0 起）
})
```

**坑**

- **要接成 invoke 链一环就用上游的 `policy.Stage`**（上游有这个转发；旧门包形态下
  它没被透出，所以旧文档写「需自包一层 `guardset.Guard`」——那段已随门包删除作废）。
- **`DefaultPolicy` 在上游**：要内置基座就传 `NewGate(nil, nil)`。
- **`Normalize` 对空/非法一律回落 `confirm`**——审计门不允许因模式脏值放大权限。
- **`Mutating` 是三态 `*bool`（v0.28.0 随 toolgate v0.17.0）**：字面量直接写
  bool 编译不过，用上游的 `policy.Mutating(true/false)` 内联构造；不填（nil）
  不是「非变更」，是「未知」——审计门 fail-safe 升人审，不替调用方猜。
- **`Decide` 只读 policy，并发安全，可放工具调用热路径。**

#### `reminder`：系统注入提示的防伪包裹（上游 `guardx/injectband`）

```go
package myapp

import "github.com/oagentkit/guardx/injectband"

// 纪律：body 来自用户输入 / 工具产出 / 历史结论等不可信内容时，
// 必须先 SanitizeBody 再进 prompt
msg := injectband.Wrap("alert", injectband.SanitizeBody(webhookText))
```

**坑**

- **调用顺序不可颠倒**：`SanitizeBody` 是「包裹前调用」。
- **与 guardx 的 `fence` 互补而非替代**：`fence` 封数据侧（不可信内容不得越出数据区
  取指令待遇），`injectband` 封系统侧（系统带外注入的身份标记不得被伪造）。
  只做前者时，攻击者不必逃出数据区，只需在内容里写一个像系统注入的标签即可抬升身份。

### 3.13 判定面向下延伸与协作面（2026-11-06 新增的十个包）

> 这十个包按 [GAP-ANALYSIS-20261106.md](GAP-ANALYSIS-20261106.md) 落地。它们与前面各节
> **不是替代关系**：`toolfail` 判「失败怎么处置」，`egress` 判「这次执行要求多宽的出口」，
> `cmdpolicy` 判「工具内部那条命令能不能跑」——三个维度可以同时接。**按需接，接一个也是一个。**

#### `egress` + `ptc`：子进程出口（**有代码变更，优先看**）

```go
import (
	"github.com/oagentkit/guardx/egressproxy"
	"github.com/yi-nology/goagent-kernel/egress"
	"github.com/yi-nology/goagent-kernel/ptc"
)

spec := egress.Spec{Mode: egress.ModeProxy, Proxy: "http://127.0.0.1:7897"}
if err := spec.Validate(); err != nil {
	return err            // 加载期自检：早于任何一次执行，别等到 spawn 时才发现
}

// 兑现者：真正能把「只经代理」落成强制面的那个东西。
// 用上游现成的强制代理（默认拒绝 + 目标白名单 + CONNECT 隧道 + 只读档）；
// 要换成自己的实现，只需实现 sandbox.EgressEnforcer 一个方法。
px, err := egressproxy.New(egressproxy.Policy{AllowHosts: []string{"api.internal"}})
if err != nil {
	return err
}
// Start 返回**可直接用于环境变量的代理地址**（形如 "http://127.0.0.1:3128"），
// 不是 error-only——声明里的地址必须是它真在听的那个。
addr, err := px.Start()
if err != nil {
	return err
}
defer px.Close()
spec.Proxy = addr

prov := ptc.NewSubprocessProvider(ptc.SubprocessOptions{
	Program:       program,   // 原有字段一律不变
	Bindings:      bindings,
	Stages:        stages,
	Egress:        spec,      // 零值 = 不限制，与旧版**逐字一致**
	EgressBackend: px,        // 兑现者。**不填而 Egress 又要了限制 ⇒ 拒绝执行**
})
// 之后按 sandbox 登记簿的常规用法注册（reg.Register(prov)）；
// **不要**直接调 prov.Exec——那会绕开档位下限与预算可兑现性（见 §4.4）。
// 子进程后端还需在 main 里加一行 ptc.MaybeRunChild()（re-exec 宿主自己的二进制）。
```

**几条必须知道的**

- `Spec.Env(base)` 生成子进程环境；**生成失败即不起子进程**（fail closed）。
  所以 `ModeDenied` 在本层**必然报错**——那一档只能由执行后端兑现，靠 env 兑现它是造假。
- **`Allow` 不是「绕过代理」的名单**（v0.20.1 起修正）：`Spec.Allow` 是「**允许经代理访问**的目标」，
  由代理判定（对应 `egressproxy.Policy.AllowHosts`），本包**不**把它写进 `NO_PROXY`。要表达
  「这些目标不走代理」请用 **`Spec.Bypass`**（写 `NO_PROXY` / `no_proxy`）。v0.20.0 及以前
  把 `Allow` 写进了 `NO_PROXY`，语义正好相反：允许名单里的目标直连，于是代理的**拒绝名单、
  端口白名单、只读档对它全部失效**，而报告里那条声明仍然显示「已兑现」。两个相反的语义
  不能塞进同一个字段。
- **兑现者必须显式给**（v0.20.0 起的行为收紧）：`Egress` 只写「要求哪一档」，它自己不会跑到
  任何地方去；父侧唯一能施加影响的时点就是 **spawn 之前**，所以那里先问一句
  `Spec.SatisfiedBy(EgressBackend)`——**不填兑现者或能力不足 ⇒ 不起子进程**。
  v0.18.0 时只判「env 生成成功」，那让「生成了 env」看起来等于「已兑现」：一个能改 env
  却没有任何强制面的后端照样让流量自由出网。
- `Spec.SatisfiedBy(provider)` 问后端「你能不能兑现这一档」。**后端不实现 `Enforcer` 就按最弱处理**，
  不是「没说不支持 = 支持」；否则一个从没实现过出口控制的后端会被选中，而声明与实际之间的落差没有信号。
- **v0.33.0 起：兑现强度分「自报」与「实测」**。`SatisfiedVerdict` 返回的 `Strength` 现在有四档：

  | 强度 | 含义 |
  |---|---|
  | `not_required` | 未要求限制 |
  | `advisory` | 环境变量兑现（永远只是建议） |
  | `declared` | 后端**自报**能力（`Enforcer`）——**未经实测** |
  | `enforced` | **实测**通过（后端实现 `EgressVerifier` 并给出证据） |

  后端只是「声明我能强制哪一档」时，强度是 `declared` 而**不是** `enforced`——
  自报不是实测，把两者并进一档，只看 `Strength` 的读者就会把「后端自称」
  读成「已经强制」。要断言真的被强制，后端得提供实测证据（`egress.EgressVerifier`：
  `VerifyEgress() (Mode, error)`，实现方自己去探测，本包不代它发请求）。
  出口档位的词表与这个接口的**真相在上游** `guardx/sandbox`（`EgressMode` / `EgressEnforcer`），
  本仓的 `egress.Mode` / `egress.Enforcer` 是 type alias（编译期同一类型）。词表若留在本仓，
  上游后端要声明能力就得 import 本仓 ⇒ 成环 ⇒ 那条接口只可能由宿主实现，且编译期一声不响。
- 本仓有一条门禁 `harness-subprocess-egress`：判「每个 spawn 点都登记了档位，且 `proxy` 档
  在 spawn 之前**看得见兑现的两半**——env 生成 + 后端能力问询」。宿主仓复用同一份执行体，
  在自己的 policy 里写 `egress_paths` 段即可（见 §4.4 后半）。

#### `preset`：把「档位 × 审批 × 出口」收成一份具名组合

散参数的病不是难写，是**改一处漏两处**：档位调到 OS 级而出口忘开代理、审批放到 auto 而沙箱仍是进程内，
组合出来的权限谁都没声明过。

```go
import (
	"github.com/yi-nology/goagent-kernel/egress"
	"github.com/yi-nology/goagent-kernel/gatecheck"
	"github.com/yi-nology/goagent-kernel/preset"
	"github.com/oagentkit/guardx/sandbox"
	"github.com/oagentkit/toolgate/policy"
)

set, err := preset.New([]preset.Preset{
	{Name: "readonly", Level: sandbox.LevelOS, Mode: policy.ModeConfirm,
		Egress: egress.Spec{Mode: egress.ModeProxy, Proxy: proxyURL}},
	{Name: "open", Level: sandbox.LevelNone, Mode: policy.ModeAuto,
		Egress: egress.Spec{Mode: egress.ModeUnrestricted},
		Unsafe: true, Justification: "仅本机调试用，不进生产配置"},
})

p, err := set.Resolve(cfg.Preset)                  // 未登记名字 → **报错，不降级**
req, err := set.GateRequest(baseReq, cfg.Preset)   // 三面一次性展开进审批请求
```

**坑**

- **三面全松必须显式 `Unsafe` 且写理由**。这条不是为了拦你，是为了让「先跑起来」这个决定留下痕迹。
- **两个名字同一份组合即报错**：配置里看起来是「两个选择」，而执行报告里出现两个名字，
  读者会以为执行过两种权限。
- **`Resolve` / `All` 的副本可以改，但改不穿集合**：出口 `Allow` / `Bypass` 在构造、
  `Resolve`、`All` 三个边界都做深拷贝（值类型只挡字段替换，挡不住 `p.Egress.Allow[0] = ...`）。
  集合构造后确实可并发共享。
- **语义键只编码该出口档位下参与兑现的维度**：代理模式编 `proxy` / `allow` / `bypass`；
  `unrestricted` / `denied` 下这三个字段不参与 `Env` 兑现，因此**不进键**——两份有效约束
  相同的预设会被判为语义相同并拒收。名单元素做可逆转义，`["a,b"]` 与 `["a","b"]`
  是两份不同白名单，不会被误判成同一份。
- **依赖方向是 preset → gatecheck**。`gatecheck` 不认识 `preset`，别反向接——
  低层判据认识高层组装件会立刻出现循环。

#### `cmdpolicy`：一次审批通过之后，那条命令怎么判

上游 `policy` / `gatecheck` 的判定面停在**工具名**上：一次 `exec` 审批通过后，它内部跑的是
`git status` 还是 `rm -rf /`，此前没有任何机制面看得见。

```go
import "github.com/yi-nology/goagent-kernel/cmdpolicy"

pol, err := cmdpolicy.New([]cmdpolicy.Rule{
	{
		Name:     "git-status",
		Prefix:   cmdpolicy.Prefix{{"git", "status"}},   // 每段是**备选集合**
		Decision: cmdpolicy.DecisionAllow,
		Match:    []string{"git status"},                  // 加载期必须被本规则命中
		NotMatch: []string{"git remote update"},           // 加载期必须判不中
	},
	{
		Name:     "rm",
		Prefix:   cmdpolicy.Prefix{{"rm"}},
		Decision: cmdpolicy.DecisionForbidden,
		Match:    []string{"rm -rf /tmp/x"},
	},
})

for _, one := range cmdpolicy.SplitCommands(cmdline) {   // ← 关键：先切再判
	argv, err := cmdpolicy.Split(one)
	v := pol.Decide(argv)
	if v.Decision != cmdpolicy.DecisionAllow { return v.Explain() }
}
```

**坑**

- **必须 `SplitCommands` 后再逐条判**。对整串只判一次时，`git status; rm -rf /` 的前缀是 `git`，
  一条 `git` 的 allow 规则会放行后半段。
- **规则自带 `Match` / `NotMatch` 样例**，`New` 会在加载期用真实判定路径跑一遍；不一致即报错。
  规则会随命令名漂移，而漂移**不会报错**，只会静静地不再命中。
- 本包**不内置任何命令词表**——词表是你的资产。本仓只给判据与样例机制。

#### `taskboard` + `boardtest`：多 agent 的认领与依赖

`dispatch` 只回答「能不能派」；派出去之后「谁负责哪个子任务、依赖谁、怎么还回去」没有任何机制面。
各宿主拿一张表兜着，于是「同一子任务被两个 agent 同时认领」在报告里读起来完全正常。

```go
import (
	"github.com/yi-nology/goagent-kernel/taskboard"
	"github.com/yi-nology/goagent-kernel/taskboard/boardtest"
)

// 1) 你的存储实现 Board 四个方法（原子性由存储保证，本仓只判结果）
// 2) 快照不变量：
for _, p := range taskboard.Check(tasks) { /* 环 / 重复 id / 认领态与租约不一致… */ }

// 3) 并发判据：成功者必须恰好 1 个（在你的 CI 里跑）
winners, err := taskboard.ConcurrentClaim(ctx, board, "t-1", []taskboard.AgentID{"a", "b", "c"}, time.Minute)
```

**坑**

- **不给存储实现**是刻意的：给一份参考实现你就会照着改它，最后两份真相并存（与 `checkpoint.StoreCAS` 同一分工）。
- **释放 / 完成凭令牌不凭内容**：同名 agent 与「失败 → 释放 → 重试」都会让内容比对认错人。
- **v0.33.0 起：令牌由存储铸造**。`Claim` 改收「要多长的租约」的 `ClaimRequest`，
  令牌随返回的 `Task` 给出（`task.Lease.Token`）：

  ```go
  task, err := board.Claim(ctx, "t-1", "agent-a", taskboard.ClaimRequest{TTL: time.Minute})
  // task.Lease.Token 由存储铸造 —— 调用方**不能**自带令牌
  err = board.Release(ctx, "t-1", task.Lease.Token)
  ```

  此前 `Claim` 收的是调用方自带的 `Lease`（含 token）——契约写着「token 由存储铸造」，
  实现却让调用方造 token，两者对不上时**实现赢**：调用方能造出任意 token，
  于是「输家的迟到释放放掉赢家的闸」又回来了，而报告里一切正常。
  另：到期时刻也归存储算（调用方的时钟与存储的时钟可能对不上）。
- **`failed` 不是终态**。把它当终态会让补救路径静默失效——一次失败就把子任务永久钉死。
- `ConcurrentClaim` 区分三种结果：`ErrTaken`（有输家，说明原子性成立）、`ErrBlocked`（压根没到争用）、
  其它 = **基础设施故障**。把后者当成「原子性没问题」是本判据最容易犯的误读。

#### 其余六个：什么时候你需要它

| 包 | 你需要它的信号 | 最容易接错的一点 |
|---|---|---|
| `sessionref` | 要把**另一个会话的结论**注入当前上下文 | 只判 TTL 不判版本。长 TTL + 源高频更新是最常见的失效形态；版本**回退**同样要判 stale |
| `skilldisclose` | 技能数量多到不能全塞进 prompt | 把「相关度」和「点名」混成一个分数——用户已明确表达的意图会被相关度压过去 |
| `hookengine` | 要支持 hooks，但不想被某一种传输（外部进程 / 插件包）焊死 | 让非阻塞 hook 参与裁决。它一旦能影响裁决就只是「慢的阻塞 hook」，而你会以为它是旁路的 |
| `memorypipe` | 要把长期记忆注入 prompt | `scope` 做父级兜底。按 `project` 查却回退到 `global`，作用域就失去了意义，而报告完全正常 |
| `delegate` | 要把任务交给异构执行方（ACP 一类） | 按配置书写顺序取第一个执行方。一次无关重排就会换掉执行方——应取能力**最少**者 |
| `worktree` | 多个 agent 各在自己的工作区里跑 | 以为只读区重叠也算冲突，或指望通配。**不支持通配**是写明的局限：`*` 与 `**` 各实现语义不同 |

### 3.14 部署配方：场景 → 必需能力（issue #277）

**「我现在这套组合，缺哪些能力才能上生产」**——答案不靠人记，在
[docs/DEPLOY-MATRIX.yaml](DEPLOY-MATRIX.yaml)。每条能力的证据（契约符号 /
门禁目标 / issue 单号）可解析，`state` 由证据推导（不抄会过期的数字）；
本节的机器口径块与清单逐行一致，`make deploycheck` 判不一致即红。

<!-- deploy-matrix:begin -->
| 场景 | 推荐存储 | Worker 模式 | 必需能力 | 当前状态 |
|---|---|---|---|---|
| `single-process-agent` 单进程 Agent | memory | single-process | 幂等键作用域、Context 取消 | available |
| `single-host-persistent` 单机持久化 | examples/sqliteadapter | single-node | 任务板持久化、启动恢复、单一存储全状态、进度对账 | available |
| `multi-process-same-host` 同机多进程 | PostgreSQL | multi-process | 多进程认领、统一租约、数据库时钟、跨进程令牌、租户隔离存储 | available |
| `k8s-single-cluster` K8s 单集群多副本 | PostgreSQL | deployment | 统一租约、续期、可靠事件、配额回收、Job 幂等、出站禁网实测、租户隔离存储 | available |
| `multi-cluster-single-region` 多集群单区域 | shared-authority-pg | local-workers | 权威唯一、可靠事件、租户隔离存储、可靠交接 | available |
| `cross-region-multi-cluster` 跨区域多集群 | regional-authority | regional-workers | Ownership Epoch、分区写入 fence、可靠交接 | available |
<!-- deploy-matrix:end -->

**怎么用**：先按目标场景选存储 / Worker 模式，再把 `required` 列里的能力逐条
对到清单的 `evidence`。`partial` / `missing` 的条目都挂着 issue——它们是**有主
的缺口**，不是「文档里没写所以不存在」。

**选哪个存储**：矩阵只给「这个场景需要哪些能力」，**选型判据**（三条判据 +
「单机多进程一律判 PostgreSQL」的硬边界 + SQLite 毫秒级时钟）在 §3.17
「存储选型」——机器口径在 [docs/STORAGE-SELECTION.yaml](STORAGE-SELECTION.yaml)。

---

### 3.15 跨区域所有权：`ownership`（issue #273）

#### 什么时候需要它

同一条任务域（比如「同一个任务队列/同一张任务板」）会被**多个区域**的
Worker 写，而你需要在网络分区时保证「旧区域不能继续提交」——`taskboard`
的租约管的是**单个任务**在**同一个存储**里的归属，`handoff` 的 Epoch 管的是
**交接单**的代次；两者都不回答「哪个区域是这份任务域的权威」。

单区域部署不需要跨域转移，但建议照常接 `Local`：提交路径从此有同一条
栅栏判据，将来接共享权威时调用方代码不用改。

#### 最小集成片段

```go
import "github.com/yi-nology/goagent-kernel/ownership"

// 单区域：本区域即权威（任意任务域 epoch 1；转移显式 ErrSingleRegion）。
auth, err := ownership.NewLocal("region-a")
if err != nil {
	return err // ErrBadMapping：区域为空
}

// 多区域：把配置事实（任务域 → 权威区域）显式登记。
// MemoryAuthority 是进程内参考实现——真实共享权威由宿主落到同一份
// 可写存储（表结构/事务/选主归宿主，本包不做）。
auth, err = ownership.NewMemoryAuthority(ownership.RegionMap{
	"task-domain-1": "region-a",
	"task-domain-2": "region-c",
})
if err != nil {
	return err // ErrBadMapping：空映射/空键值
}

// 提交落点：取当前授权，Fence 通过才提交。
g, err := auth.Owner(ctx, "task-domain-1")
if err != nil {
	return err // ErrUnknownDomain：映射没登记这个域
}
if err := auth.Fence(ctx, g.Commit()); err != nil {
	return err // 任何错误都必须拒绝，不得降级为放行
}
```

**跨区域转移是两阶段**（确认前候选代次不可用）：

```go
// ① 当前权威区域凭授权令牌提出转移；当前授权不变。
tr, err := auth.BeginTransfer(ctx, ownership.TransferRequest{
	Domain: "task-domain-1",
	From:   "region-a",
	Token:  g.Token,
	To:     "region-b",
})
if err != nil {
	return err
}
// 把 tr.ID 与 tr.Token 交给目标区域（回执令牌只发目标区域）。

// ② 目标区域凭回执令牌确认；成功后 epoch 提升、新区域成为唯一权威。
grant, err := auth.ConfirmTransfer(ctx, ownership.ConfirmRequest{
	ID: tr.ID, Token: tr.Token, By: "region-b",
})
if err != nil {
	return err // ErrNoTransfer：提案被取代；ErrNotTarget：确认方不对
}
// 之后 region-b 用 grant 提交；region-a 的旧 grant 提交得到 ErrStaleEpoch。
```

#### 关键符号

| 符号 | 作用 |
|---|---|
| `RegionMap` | 任务域 → 权威区域的映射：`Validate` / `Owner` / `Domains`，可配置可查 |
| `Authority` | 权威管理面：`Owner` / `Mapping` / `BeginTransfer` / `ConfirmTransfer` + `Fence` |
| `Fencer` | 提交路径唯一接缝（单区域 `Local`、多区域共享权威、参考实现都满足） |
| `Grant` / `Commit` | 当前授权 / 一次提交声明；`Grant.Commit()` 逐字段投影，别手抄 |
| `Transfer` | 两阶段提案：`Candidate`（当前代次 + 1）与回执 `Token`；**不是授权** |
| `Judge` | 纯判据（宿主实现可直接复用；判定次序：回退 → 未确认 → 区域 → 令牌） |
| `ErrSingleRegion` | 单区域本地权威拒绝跨域转移（显式拒绝，不静默不做） |

#### 坑

- **把 `BeginTransfer` 当成授权**。候选代次在确认前提交一律
  `ErrEpochUnconfirmed`；`Owner` / `Mapping` 也仍是旧区域。确认前只有一个权威。
- **Fence 与提交分两次操作**。`Fence` 是判据不是事务：判据通过到提交生效之间
  仍有窗口（与 `coord` 的判定—提交窗口同一类事实）。接共享存储时把 `Judge`
  放进同一次条件写/CAS，不要「先 Fence 再另起一次写」。
- **拿所有权 epoch 当租约 TTL**。本包没有到期/续期：`Grant` 不会随时间失效，
  任务租约仍归 `taskboard.Lease` / `handoff.Ticket.TTL`。把两套时限混在一起，
  会得到「所有权过期但任务租约还在」的双真相。
- **权威不可达时放行**。分区期间可达性失败必须按拒绝处理（fail-closed）；
  「查不到就默认本地权威」正是双主。
- **空映射启动**。`NewMemoryAuthority(RegionMap{})` 直接 `ErrBadMapping`：
  空映射会让每笔提交都报 `ErrUnknownDomain`，配置错误与业务错误从此分不开。
- **指望 `Local` 做转移**。单区域没有第二权威，转移请求显式
  `ErrSingleRegion`；需要跨区域时换共享权威实现，调用方接口不变。

### 3.16 配额账本与悬挂预占回收：`scheduling`（issue #189 / #267）

多副本部署时，进程内的配额账本会**超卖 N 倍**——每个副本各算一份「已占用」，
真实并发上限 = 副本数 × limit，看起来配额生效了，实际没有。`QuotaStore` 把
「占用 / 释放 / 回收」做成**存储中立原语**：扣减与归还在**存储侧的一次原子操作**里
完成，多个副本共享同一份账。调度判据（什么时候该占、占多少）仍在调用方。

**本版（v0.43.0）接口扩了三个方法**——`scheduling` 定级 Experimental、不承诺兼容，
但**实现方编译期即红**，必须改：

| 方法 | 语义 | 实现方要保证 |
|---|---|---|
| `ReserveLease(ctx, QuotaRequest) (QuotaLease, error)` | 带归属与有效期的预占（推荐入口）| 与 `Reserve` 同样原子：余额判定与扣减不可分割，余额不足时**一个额度都不占** |
| `Reclaim(ctx, QuotaLease) error` | 凭令牌回收**悬挂**预占（Worker 被 SIGKILL 后没人再调 `Release`）| 凭 Token 查存储记录；预占未到期 ⇒ `ErrLeaseNotExpired`；关联任务租约仍有效 ⇒ `ErrLeaseStillHeld`；无判定面 ⇒ `ErrNoLeaseOracle`——三种都**不回收** |
| `SweepExpired(ctx) (int, error)` | 扫描全部到期预占，逐条走 `Reclaim` 判据 | 返回**实际回收条数**（租约仍有效 / 未到期 / 已归还的不计）|

`Reserve` / `Release` 的语义不变（`Reserve` 等价于不带归属、不带期限的
`ReserveLease`；`Release` 仍然只认 Token、同一租约至多归还一次）。

**回收为什么还要问任务租约**：到期只说明「可能失联」，**不等于**可以回收——关联
任务的租约仍有效时，旧任务可能还在真跑，回收额度会让别的调度者超额占用同一份资源。
判定面 `TaskLeaseOracle` 由宿主提供（通常包装 `taskboard` 的租约 / 续期状态）：
返回 `alive=true` 表示任务仍被活着持有，此时**不得**回收配额。

**回收与迟到的 Release 的竞争由 Token 保护**：回收即删除存储记录，迟到的旧 Release
查不到记录 ⇒ `ErrOverRelease` 且**不扣账**（不二次归还、不出负余额）。

```go
import "github.com/yi-nology/goagent-kernel/scheduling"

store, err := scheduling.NewMemQuotaStore(
	map[string]int64{"model:opus": 4},
	scheduling.WithQuotaClock(clock.Now),       // 到期判定读它，便于确定性测
	scheduling.WithQuotaLeaseOracle(taskOracle), // 回收判据：那个任务的租约还活着吗
)

lease, err := store.ReserveLease(ctx, scheduling.QuotaRequest{
	Resource: "model:opus",
	Amount:   1,
	TaskID:   taskID,           // 关联任务 ⇒ 回收前可问租约存活
	TTL:      2 * time.Minute, // 不限期（TTL=0）⇒ 不得回收
})

err = store.Release(ctx, lease)   // 正常完成：凭 Token 立即归还
n, err := store.SweepExpired(ctx) // Worker 失联：Reconciler 定期扫
```

跨副本实现把各方法落到 Redis Lua / PostgreSQL 条件 UPDATE / etcd txn 上，**判据不变**。
`MemQuotaStore` 是**进程内参考实现**（互斥锁原子），只在单副本内成立。

#### 坑

- **只实现 `Reserve` / `Release`**。本版接口扩了三个方法，不补齐**编译不过**。
  若你的实现暂时补不齐，先把宿主代码钉在 v0.42.0，补齐再升。
- **把「到期」当「可以回收」**。`Reclaim` 必须同时确认预占已到期**且**关联任务租约
  已失效；只判到期会在旧任务还在跑时把额度还给别人 ⇒ 超卖。
- **忘了注入 `TaskLeaseOracle`**。没有判定面时 `Reclaim` 显式 `ErrNoLeaseOracle`
  （fail-closed）——这是故意的：判不出失联就不回收，宁可额度卡住也不超卖。
- **把 `Until` 当任务租约 TTL**。配额预占的有效期只用于**失联判定**，不是任务租约；
  任务租约仍归 `taskboard.Lease`。
- **拿 `Resource` / `Amount` 当归还凭据**。归还只认 Token 查到的存储记录；租约里的
  `Resource` / `Amount` 是**预占时的快照**，带错字段也只会归还那一笔原始预占。
- **`MemQuotaStore` 上生产**。多副本部署下每个副本各记一份账 ⇒ 超卖 N 倍。

### 3.17 存储选型：什么时候用 SQLite、什么时候用 PostgreSQL（issue #424）

**三条判据，按顺序问**（机器口径在
[docs/STORAGE-SELECTION.yaml](STORAGE-SELECTION.yaml)，`make storagecheck` 逐条判）：

1. **需要跨进程 / 跨机共享写**（多副本、多机、K8s）⇒ **PostgreSQL**。
2. **需要重启后恢复，且不想为此多起一个服务进程** ⇒ **SQLite**
   （单文件 WAL 库，参考实现 `examples/sqliteadapter`）。
3. **只要进程内判据正确、不要持久化** ⇒ **memory**（进程内实现，不落库）。

**边界写死：单机多进程一律判 PostgreSQL。** SQLite 的单写者模型在多写者下
退化为串行，`busy_timeout` 到点即失败（可判、不静默丢写，但可用性下降）
⇒ 同机多进程（两个 Worker 进程抢同一份任务板）**必须**用 PostgreSQL。
这条边界不是靠记性维持的：门禁按「场景形态 → 并发写者数」与「存储 → 写者上限」
两张数据表判，**多写者场景选了单写者档位当场红**。

**SQLite 侧的三条前提**（不是能力，是用它的前提）：

- **存储时钟是毫秒级**（实测）⇒ **租约 TTL 建议 ≥ 1s**——秒级以下的 TTL 会被
  时钟分辨率吃掉，过期判定的边界行为不可预期。
- **留痕写路径要独立句柄**：控制面事务占着写句柄时，留痕面再走同一句柄会挂死
  （不是干净报错）⇒ 审计 / 留痕走独立句柄（进程内或独立库）。
- **一致快照依赖 WAL 快照语义**（deferred 只读事务）⇒ 非 WAL 模式下该判据需重验。

**怎么落地**：场景 → 存储的对应关系在
[docs/DEPLOY-MATRIX.yaml](DEPLOY-MATRIX.yaml)（`single-host-persistent` →
`examples/sqliteadapter`；`multi-process-same-host` / `k8s-single-cluster` →
`PostgreSQL`），**选型声明面**在
[docs/STORAGE-SELECTION.yaml](STORAGE-SELECTION.yaml)——那里逐条写了每个存储
**认领**哪些能力、能力在代码里的落点（`path#Symbol`，`make storagecheck`
走 go/ast 判符号声明）。两份声明面不许脱节：能力 id 的唯一真相是部署矩阵的
能力表，矩阵里删了能力而选型面还认领它（或反过来谁都不认领），当场红。
（两份声明面的 **evidence 粒度不同**：部署矩阵是**面级展开**（一条能力把各张脸
各列一条落点），选型面是**事务域本体**（一条能力只指那条把各张脸收进同一份
事务域的入口）⇒ **不矛盾**，id 层面的一致性由 `make storagecheck` 判据 2 机判。）

#### 门禁还判什么（声明面的形式与一致性）

除了上面三条选型判据与「能力落点可在代码里解析」，`make storagecheck` 还判声明面
**自身**的形式与一致性——失败形态一律是「当场红」，不是提示：

- **`kind` 封闭值域**：每条存储的 `kind` 只能是 `memory` / `sqlite` /
  `postgresql` / `shared-authority` / `regional-authority` 五个取值之一 ⇒ 拼错、乱写、
  写一个不存在的引擎类别当场红（报红时点名写了什么 + 允许哪些取值）。此前 `kind`
  是**自由标签**，那一列字没有任何机器保证。
- **写者数交叉校验（W1 存储侧 + W2 场景侧）**：`scenario_writers`（场景形态 → 并发
  写者数）与 `max_writers`（存储 → 写者上限）**两张表互相钉住**——
  W1：存储的写者上限是多写者档位 ⟺ 它认领了至少一项多写者能力；
  W2：形态写者数 ≥2 ⟺ 该形态下有场景要求多写者能力（写者数 ==1 ⇒ 该形态下所有场景
  都不要求）。
  ⇒ 改弱任一张表当场红，那条硬边界不再靠「记性 + 能力归属碰巧」撑着。
- **两侧门面「同款集合」相等性（F1–F5）**：F1 声明面的 `accessors` ∪ `other_methods`
  与代码派生集（go/ast）**双向**相等；F2 清单形状（面访问器非空 / 不重复 / 与其它导出
  方法不重叠）；F3 各门面的面访问器集合必须相等（差集非空即红，并点名差哪几个）；
  F4 每个面访问器要么被能力证据引用、要么登记 ⇒ 都没有即红；F5 有方法级落点的存储**必须**登记
  `facade:` 段 ⇒ **方法级落点仍在时**整段删掉不会「真空绿」（F5 会响）。
  ⚠ **前提限定**（逐条实测口径见台账 §29 边界 2 与 §31 边界 3 / 4）：「真空绿」被防住的前提是
  **方法级落点仍在**——方法级落点一并降级为类型级后 F5 就不再响，但门禁**不会当场转绿**：
  走到 rc=0 要三步（实测于今天的 `docs/STORAGE-SELECTION.yaml`）：① 删掉那一扇门面的 `facade:`
  段（**共用同一扇门的两个存储都要删**——只删一个则那扇门仍被另一个存储声明着 ⇒ 实测到不了
  rc=0，仍红在 F4 的覆盖面）⇒ 仍红（F5 仍响 + F4 的**登记面**红）；② 把那两个存储的方法级
  落点一并降级为类型级 ⇒ 仍红（F5 不响了，红的是 F4 的**登记面**）；③ 删掉
  `known_unbacked_accessors` 里挂在**没有门面的存储**上的登记项 ⇒ 才 rc=0，而门面判定面只剩
  1 扇 ⇒ **F3 空真**（退化成自我比较）⇒ **该侧门面完全不被判**。
  ⇒ 「两侧门面同款」从此是**机判事实**，不是散文里的断言。
- **`known_unbacked_accessors` 登记面**：没有能力落点的面访问器登记在这里，并写明事实
  理由。⚠ **登记表示「当前没有能力断言覆盖它」，不表示它已被覆盖**；登记若其实已被
  能力证据覆盖 ⇒ 当天即红（过期登记当天删掉）。

⚠ **门禁判的是声明面之间的一致性，不是外部事实**：「某存储**实际上**能不能扛多写者」
仍靠能力认领 + 写者上限**互相印证**（事实源头是代码与机制，不是门禁）⇒ 门禁只保证
两张表不互相打脸。

#### 已登记的已知不对称（两侧**确有**行为差异的部分）

选型面把两侧**已知不等价**的地方逐条登记在 `known_asymmetries` 段，`make storagecheck`
逐条判（落点可解析 / 落在 ≥2 个不同存储上 / summary 非空），并把摘要打进门禁输出：

| id | 一句话 |
|---|---|
| `batch-read-visibility` | 批内读：SQLite 侧**看得见**本批未提交写，PG 侧**看不见**（MVCC 已提交快照）——机制不同、语义同构，不是能力缺失 |
| `batch-cursor-write` | 批内写游标：PG 侧**显式拒绝**（游标独立提交会越过被回滚的事件 ⇒ 静默丢帧），SQLite 侧**挂到 ctx 超时**——都响亮，但**失败形态不同** |
| `same-store-identity` | 「同一份存储」的判据：SQLite 侧按**文件路径**判（两个路径 / 符号链接 / 两处挂载 ⇒ 装配期拒），PG 侧按 **host/port/database/schema** 四元组判——两侧同款口径 |
| `control-version-anchor` | 版本锚只被**控制面自身的提交**推进 ⇒ 检测不到控制面之外的写入——**两侧共有**的契约边界，不是 SQLite 的短板 |
| `facade-schema-bootstrap` | 建表入口的公开面**不对位**：SQLite 侧有 `Store.EnsureSchema`，PG 侧是**包级函数** `pgstore.EnsureSchema` ⇒ 只持门面的宿主在 PG 侧拿不到建表入口 |

⇒ **跨引擎迁移时别假设两侧行为一致**（尤其「批内读看得见什么」「批内写游标怎么报错」）。
逐条全文与落点见 [docs/STORAGE-SELECTION.yaml](STORAGE-SELECTION.yaml) 的
`known_asymmetries` 段。

#### 坑

- **按「数据量小」选 SQLite**。判据是**写者数**与**要不要恢复**，不是数据量
  ——单写者下 SQLite 完全够用，多写者下它一定降级。
- **同机跑两个 Worker 进程，还当自己是「单机」**。进程数 ≥ 2 ⇒ 写者数 ≥ 2 ⇒
  按硬边界判 PostgreSQL。
- **把毫秒级存储时钟当高精度时钟用**。租约 / 过期判定按亚秒 TTL 计算 ⇒
  边界不可预期 ⇒ TTL 取 ≥ 1s。
- **拿 SQLite 当「不用起 PG」的免维护方案**。它省掉的是**服务进程**，不是
  **运维**：WAL 文件、备份、跨机共享都得自己安排。

### 3.18 回合守卫：模型侧循环还能不能转：`roundguard`

#### 什么时候需要它

你在宿主里写**模型侧循环**（模型请求工具 → 执行 → 回灌 → 继续；模型给终答 → 停）。
循环本体早已收敛成同一段代码，各宿主真正不一致的是**循环周边**的三问：

1. **这次还能不能转**（回合数 / 工具调用总数 / 单工具额度还剩多少）？
2. **这一回合以什么结束**（终答 / 要工具 / 交接 / 报错 / 中止）？
3. **这一圈是不是白转**（精确重复 / 震荡 / 缓慢漂移 / 错误螺旋 / 只规划不动手）？

放在宿主侧的后果是**每个宿主各判一套**：同一段循环在 A 宿主算「第 12 圈超了」、
在 B 宿主算「第 8 圈还能转」，且没有任何机器能发现。

`roundguard` 是**判据层**：它**不调模型、不执行工具**，只回答这三问，并把三张判据
合成**一条可执行结论**。**判据归内核，模型侧归宿主**——本包只给结论，不写产物、
不解释工具语义。

#### 最小集成片段

```go
import "github.com/yi-nology/goagent-kernel/roundguard"

// —— 装配期：声明额度与处置口径（负值 / 空工具标识 / 未知口径 ⇒ Validate 报错）——
b := roundguard.Budget{
    MaxRounds:    40,                              // 回合数上限
    MaxToolCalls: 20,                              // 工具调用总数上限
    MaxPerTool:   map[string]int{"send_email": 2}, // 单工具上限
    Policy:       roundguard.PolicyForceFinal,     // 缺省即此；改 FailFast 即失败即止
}
if err := b.Validate(); err != nil {
    return err // 非法声明在装配期红，不静默降级成「不限」
}
cfg := roundguard.Config{} // 零值 = 缺省：窗口 8 / 重复阈值 3
meter := roundguard.NewMeter()
var obs []roundguard.Observation

for {
    // ① 模型调用：这一回合**能不能开始**由上一回合末的 Decide 说了算（见 ③）
    //    —— 额度判定只做一次、就在 Decide 里，宿主侧不再另开一套 Check* 分支
    resp := model.Call(ctx, msgs, tools)
    meter.Round()

    // ② 工具执行前：单工具额度 ⇒ 否决这一次，但**配对不破**
    //    （单工具额度没有内部对应面，只能宿主在工具执行前自己判）
    var results []toolResult
    for _, tc := range resp.ToolCalls {
        if v := roundguard.CheckTool(b, meter.Used(), tc.Name); !v.Allowed {
            results = append(results, toolResult{ID: tc.ID, Err: errors.New(v.Detail)}) // 回灌失败结果
            continue
        }
        res := execTool(ctx, tc)
        meter.Tool(tc.Name)
        results = append(results, res)
    }

    // ③ 回合末：**每回合一条** Observation（工具调用折叠进本回合的计数与指纹）
    obs = append(obs, hostRoundObservation(resp, results))
    d := roundguard.Decide(roundguard.Snapshot{
        Outcome: hostOutcome(resp, results), // 宿主把本回合的模型回复映射成 Outcome
        Used:    meter.Used(),
        Obs:     obs,
    }, b, cfg)

    if !d.Continue {
        if d.Reason == roundguard.ReasonForcedFinal {
            // 软：额度用尽但按「强制收尾」口径处置 ⇒ 再给模型一次**不带工具**的
            // 收尾机会，让它用已收集的证据给出终答（产物仍可用，不是失败）
            final := model.Call(ctx, msgs, noTools())
            meter.Round()
            out := hostOutcome(final, nil)
            out.ForcedFinal = true // 标记这是「额度耗尽后的强制收尾步」
            return finish(roundguard.Decide(roundguard.Snapshot{
                Outcome: out,
                Used:    meter.Used(),
                Obs:     obs,
            }, b, cfg))
        }
        // 其余停法按 Reason 处置：budget_exhausted / failed / aborted / no_outcome
        // 是失败型（IsFailure()==true）；stalled 是温和叫停（产物仍可用）
        return finish(d)
    }
    msgs = append(msgs, assistantMsg(resp), toolResultsMsg(results))
    if d.Warn {
        // 首档：把警告回灌给模型（**文案归宿主**），给它一次自我修正的机会。
        // ⚠️ 必须排在 assistantMsg / toolResultsMsg **之后**：警告是针对**这一回合**的
        //    判据 ⇒ 排在它前面 = 模型先看到警告、后看到触发警告的那一回合
        //    （**语义倒置**；而配对不破 ⇒ 没有编译错误、没有断言能抓到它）。
        // ⚠️ 这个位置**不会**漏掉警告：Warn=true ⇒ Continue=true（首档只警告不停止，
        //    见 decide.go 的 tierFinding：Stalled 与 Warn 互斥）⇒ 上面那个
        //    !d.Continue 分支在 Warn=true 时根本不会进。
        msgs = append(msgs, hostWarnMessage(d.Shape))
    }
}
```

#### 关键符号

| 符号 | 作用 |
|---|---|
| `Classify(Outcome) Route` | 回合结局分类。判定次序「先具体后笼统，取证据最硬的那条」：中止 > 报错 > 交接 > 要工具 > 强制收尾 > 终答 > 原样再问 > 零值形态（保守收敛，不让空循环空转到超时）|
| `Outcome` | 宿主注入的**回合级**事实：`ToolCalls`（本回合工具调用数）/ `Completed`（给出了终答）/ `Handoff` / `Paused`（服务端要求原样再问 ⇒ `Continue=true`）/ `Failed` / `Aborted` / `ForcedFinal`。**一条 Outcome = 一个回合**（不是一次工具调用）。⚠️ 零工具 + 未收敛 + 非 `Paused` ⇒ `ReasonNoOutcome` ⇒ **当场收敛**（保守侧，不让空循环空转到超时）|
| `Reason`（10 值）| 结局判定词表：`Reason.Valid()` 与 `AllRoundReasons()` 读**同一张表**（别处不得手抄取值）。`Reason.IsFailure()` 把**失败型收敛**与**温和叫停**分开 |
| `Budget` / `Used` / `Meter` | 额度声明 / 当前用量 / 进程内回合账（带锁，**只数不判**；跨进程累计归宿主）|
| `CheckRounds` / `CheckToolCalls` | 回合数额度 / 工具调用总额度的判据。**`Decide` 内部调用的就是这两条**（判「下一回合能不能开始」，发生在下一次模型调用之前）⇒ **宿主侧不要再另开一套额度分支**——那套分支在 `Decide` 给出终局结论之后就永远执行不到（死代码）|
| `CheckTool` | 单工具额度。**没有内部对应面**，须宿主在**工具执行前**自己判；被拒时**仍要回灌一条失败结果**给模型（配对不破）|
| `StopPolicy`（2 值）| 额度用尽后的处置口径（**策略**）：`PolicyForceFinal` / `PolicyFailFast`。**额度维度（事实）与处置口径（策略）分开**，且一个口径管全部额度 |
| `Budget.EffectivePolicy()` | 取生效口径（零值缺省 = 强制收尾）。**别直接读 `Budget.Policy` 判口径**——零值是合法缺省，不是第三种口径 |
| `Detect([]Observation, Config) Finding` | 无进展检测：**五形态 + 三档处置**（不判 / 首档 `Warn` 警告回灌 / 停止档 `Stalled` 温和叫停）。窗口是**回合**窗口，向上钳位到 `2×Repeat+2`（否则震荡形态的停止档**永不可达**——判据不可达等于判据不存在）|
| `Observation` | **一条 Observation = 一个回合**（不是一次工具调用）：`Tools` 是本回合的工具调用数、`Fingerprint` / `Normalized` 是本回合动作的**聚合指纹**、`Settled` 是本回合是否已收敛。**指纹与归一指纹都由宿主算**（内核不猜「什么是易变部分」）；`ResultChanged` = 本轮有进展；`Exempt` = 本回合不参与判定（轮询 / 查状态类动作、刻意的规划步）|
| `Decide(Snapshot, Budget, Config) Decision` | **额度判定只此一处**：结局终局优先 → 额度按处置口径分流 → 无进展兜底。`Decision.Continue=false` 有**三种含义**（失败型 / 强制收尾 / 温和叫停），靠 `Reason` 区分 |

#### 三条接线纪律

1. **失败要回灌、也要记账**：工具失败既作为 `tool_result` 回灌给模型（模型自行修正），
   又作为 `Observation.Failed` 记一条——只回灌不记账 ⇒ 无进展检测永远看不到错误螺旋。
2. **指纹要稳定，且要带上「结果变了」**：同一逻辑动作必须给同一指纹；归一指纹要抹掉
   易变部分（页码 / 时间戳 / 行号），否则漂移检测永远不命中。「相同调用但输出变了」
   必须显式记为 `ResultChanged: true`——那是**进展**，不记就会把有意义的重试误判成
   循环。轮询 / 查状态类动作、以及刻意的规划步，要记 `Exempt: true`。
3. **「停止」与「失败」分开处置**：`Decision.Reason` 区分失败型收敛
   （`IsFailure()==true`）与温和叫停 / 强制收尾——把「被上限温和收停」当故障上报
   就是假红。

#### 坑

- **每个工具调用记一条 Observation**（观测粒度比实现细）。`Detect` 的窗口是**回合**
  窗口、空转形态按「连续回合 `Tools==0 && !Settled`」判 ⇒ 调用级观测下**空转形态
  永不可达**，且重复形态的连续长度按**调用数**算、直接越过阈值 ⇒ **首档（自我修正
  机会）被跳过**。**一条 Observation = 一个回合**：工具调用折叠进该回合的 `Tools`
  计数与聚合指纹。
- **宿主侧再判一遍额度**。额度判定只在 `Decide` 一处（它内部用的就是 `CheckRounds` /
  `CheckToolCalls` 同一份判据），分流按 `Budget.Policy`；宿主自己那套 `Check*` 分支
  在 `Decide` 给出终局结论之后**永远执行不到**（死代码），连带地 `forceFinal` 恒为
  false、「软：不再给工具」那条路径根本走不到 ⇒ **强制收尾永远收不起来**。
- **额度判定挪到模型调用之后**。最后一次昂贵调用已经发生，判定就晚了——`Decide` 在
  **上一回合末**给结论，正好赶在下一次模型调用之前。
- **不跑 `Budget.Validate` 就上线**。未校验的负值上限在 `Check*` 里是 **fail-open**
  （上限 ≤ 0 一律判成「不限」）⇒ 非法声明必须在**装配期**被拒。
- **用 `Budget.Policy` 直接判口径**。零值是**合法缺省**（= 强制收尾），拿它跟
  `PolicyForceFinal` 比会把「没设」判成「不是强制收尾」⇒ 要生效口径用
  `EffectivePolicy()`，要校验声明用 `Validate()`。
- **`Decision.Continue == false` 一律当失败**。`ReasonForcedFinal`（强制收尾，产物
  仍可用）与 `ReasonStalled`（温和叫停）都不是失败——一律当故障上报就是假红。
- **无进展一命中就停**。首档（`Warn`）只该**警告回灌**，给模型一次自我修正的机会；
  停止档才温和叫停——一命中就停 = 模型永远没有自我修正的机会。
- **警告回灌排在被评判回合之前**。首档的警告针对的是**这一回合**的判据 ⇒ 必须排在
  本回合的 `assistantMsg` / `toolResultsMsg` **之后**回灌；排在它前面 = 模型先看到
  警告、后看到触发警告的那一回合（**语义倒置**）。而这类错**配对不破**：没有编译
  错误、没有断言会红 ⇒ spec §11 的接线示例由机器判据钉住这个顺序。
- **零工具回合也要给指纹**。多个零工具回合若指纹全等（例如都留空），会先被「精确重复」
  判走（重复的判定次序在空转之前）⇒ **空转形态永远轮不到**。指纹取本回合的产出
  （如规划文本）即可。
- **零工具回合默认被「无产出」收敛掉**。`Outcome` 零值形态 ⇒ `ReasonNoOutcome` ⇒ `Decide`
  当场收口 ⇒ 空转 streak 攒不起来。要让这类回合继续跑（刻意的规划步）：① 标
  `Exempt: true`（豁免，不计任何形态）；② 或把 `Outcome` 映射成 `Paused`（`Continue=true`）。
- **把指纹当内核的事**。指纹 / 归一指纹**由宿主算**：内核若自己决定「什么是易变部分」，
  就成了领域解释者（形状判定归宿主、集合判定归本包）。**聚合指纹的聚合规则也是宿主的
  事**（本回合多次调用怎么合成一个指纹）。
- **拿 `Meter` 当跨进程回合账**。它是**进程内**的（带锁、只数不判）；跨进程续跑时回合数
  不随之累计，要跨进程累计的宿主须自行落库并把 `Used` 灌回本包。

---
**什么时候不该用**

- **你的循环不会空转**（模型每次都给出新信息）——无进展检测只会误报。
- **想要它替你调模型或执行工具**——本包**只给结论**：不调模型、不执行工具、不写产物、不解释工具语义。
- **想做跨进程的回合累计**——`Meter` 是**进程内**的；跨进程累计要宿主自己落库并把 `Used` 灌回本包。

## 4. 门禁与提交前检查

### 4.1 一条命令

```bash
make check
```

CI 只执行它。**门禁本体全部在 Makefile 里，不在 `ci.yml` 里**——
门禁若只挂在工作流文件上，本地要到推送之后才知道自己违规，
那不是「提交前门禁」而是「推送后门禁」。

`make check` 的依赖链：

| 目标 | 拦什么 |
|---|---|
| `versioncheck` | 门禁工具版本口径：`ci.yml` 装的版本与 Makefile 声明的不一致——两处同步（Makefile 的 `LINT_VERSION` / `GOVULNCHECK_VERSION` 是唯一来源）|
| `cijobname` | `ci.yml` 的 job 名超 90 字节——GitHub 会截断它 ⇒ 分支保护的 required context 永远匹配不上 ⇒ 每个 PR 卡在 BLOCKED 且看不出原因；改短 job 名，并同步分支保护的 required contexts（两者必须逐字相同）|
| `fmt-check` | gofmt 未格式化（文件集由 git 决定，不递归扫 `.*-work/`） |
| `vet` | go vet |
| `lint` | golangci-lint（**未安装时明确报错退出**，不静默跳过） |
| `test` | 全量单测 |
| `cover-gate` | 全局覆盖率 < 60% **或**任一包 < 70% 即红（逐包判据执行体 `cmd/pkgcov`；豁免分**三张表**：普通豁免（当前为空）、结构性豁免（`examples/pgstore`，环境依赖型）、零测试文件豁免（`cmd/archcheck`、`cmd/ptcchild`、`examples/pgstore/cmd/pgsmoke`）；实测值以该目标输出为准）|
| `vuln` | `govulncheck` 报出可达漏洞（未安装时**明确报错退出**，不静默跳过）|
| `race` | 数据竞争（`-race`；**开 cgo**，Linux 上 `-race requires cgo`）。由 `cmd/raceprobe` 接管：红时给**形态判定**（数据竞争 / 用例挂死 / 启动期楔死 / 普通用例失败 / 认不出），并把原始输出与结论落盘到 `RACE_EVIDENCE_DIR`（默认 `$TMPDIR/goagent-kernel-race`）|
| `crossbuild` | 声明支持的平台编不过（linux/amd64、linux/arm64、darwin/arm64、**windows/amd64（语义降级）**）|
| `architecture-check` | 10 条规则 + 门面包已清零（v0.35.0 删五门面）+ 各包依赖方向（包数以该命令输出为准）|
| `docsync` | README 包表 ↔ 仓库实际（生产行数 / 外部依赖） |
| `seamcheck` | 接缝契约三形态（A 只读 / B 阻断 / C 阻断）没写齐——按 `docs/SEAM-CONTRACT.md` 补声明与三条约束 |
| `apilifecheck` | API 生命周期四方不一致（清单 ↔ 包注释 ↔ 生产包集合 ↔ Deprecated 替代）+ 错误哨兵登记表 ↔ 代码不等——见 `docs/api-compat.md` |
| `deploycheck` | 部署配方能力清单的证据不可解析 / 状态与证据不符——见 `docs/DEPLOY-MATRIX.yaml` |
| `storagecheck` | 存储选型判据：场景的 storage 不在允许集内 / storage 不提供场景必需的能力 / 能力断言在代码里找不到落点 / 存储 `kind` 不在封闭值域内 / 写者数与写者上限交叉校验（W1 存储侧 + W2 场景侧 + W3 陈旧形态键）/ 两侧门面的面访问器集合相等（F3）+ 门面清单与代码派生集双向相等（F1）+ 清单形状（F2）+ 每个面访问器都有能力落点或已登记（F4）+ 有方法级落点的存储必须登记门面段（F5）——见 `docs/STORAGE-SELECTION.yaml` |
| `releasecheck` | 发版纪律：annotated tag / CHANGELOG 含本版段 / 无 U+FFFD / 区间内无混装提交；非 tag 提交**显式跳过**（打 tag 后必判）|
| `modreplace` | go.mod 不得含 `replace` |
| `modwork` | go.work / go.work.sum 不得入库 |
| `hygiene` | 仓库**跟踪**了编译产物二进制（本地构建产物留在工作树里是正确状态，判的是「进了版本库」）——`git rm --cached <路径>` + 删掉工作树副本 + 把该路径补进 `.gitignore` |

```bash
make architecture-check --report   # 看全量违规清单（默认前 10 条）
```

### 4.2 九条架构规则

| 规则 | 级别 | 拦什么 |
|---|---|---|
| `harness-no-domain-vocabulary` | **error** | 生产代码里的运维领域词 |
| `harness-test-fixture-domain-vocabulary` | **error** | 测试夹具用了真实运维词面（须改合成数据） |
| `harness-no-host-dependency` | **error** | 任何对宿主仓的 import |
| `harness-no-domain-runtime-deps` | **error** | 对宿主仓 / 旧门面仓 / 已退役 `agentkit` 门面的 import |
| `harness-no-bare-goroutine` | **error** | 生产代码裸 `go func`（panic 无法被外层 recover） |
| `harness-no-printf-log-callback` | **error** | 公开 API 里的 `logf func(string, ...any)`（printf 回调焊进签名即不可逆 API 债） |
| `harness-single-errors-package` | **error** | stdlib `"errors"` 与 `github.com/pkg/errors`（全仓只允许 `oekit/pkg/errors` 一套） |
| `harness-sandbox-execution-single-entry` | **error** | 外部程序执行只允许经 **`ptc` 执行后端**；其余生产代码不得直接 import os/exec（收口点已从 `sandbox` 迁到 `ptc`——sandbox 门包已删，档位语义直连 guardx）|
| `tool-pairing-key-single-source` | **error** | 工具配对键（`call_id`）只允许经 `eventbus.ToolCallID` 读取（两处各写一份 ⇒ 同一事件流两个相反结论）|

另有 **18 条**不走正则表、由 `go/ast` 判定的结构性规则（其中**六条**是租户隔离证据门，issue #177 补齐；含一条 policy 自校验）——判据是语法结构，逐行看文本判不出：

| 规则 | 拦什么 | 落点 |
|---|---|---|
| `harness-facade-stays-facade` | 门包长出实现（本仓当前 `facades` 段是**空清单**；`dispatch` 已是本仓真实实现，不在门包之列）| `policy.yaml` 的 `facades` 段 + `archcheck/facade.go` |
| `harness-module-dependency-direction` | 包间依赖超出 `modules[].requires` 声明；**以及未登记的包目录** | `policy.yaml` 的 `modules` 段 + `archcheck/deps.go` |
| `harness-test-goroutine-joined` | 测试函数（`Test*`/`Benchmark*`/`Fuzz*`）里起了 goroutine，**而该函数或它调用的包内函数（深度 ≤2）里没有等待点**（接收表达式 `<-ch` 或 `X.Wait()`）⇒ 用例返回后 goroutine 仍可能在跑 | `policy.yaml` 的 `rules` 段（`check_kind: goroutine_join`）+ `archcheck/testgoroutine.go` |
| 沙箱档位登记 | 执行沙箱后端未在 `sandbox_providers` 段登记，或登记的档位与代码事实漂移 | `policy.yaml` 的 `sandbox_providers` 段 + `archcheck/sandbox.go` |
| `harness-sandbox-execution-path` | `execution_paths` 登记的「档位 → 构造器」与代码事实偏离 | `policy.yaml` 的 `execution_paths` 段 + `archcheck/execution.go` |
| `harness-subprocess-egress` | spawn 点未登记出口档位（含别名 / 点导入 / 包级初始化，以及 `os.StartProcess`、`syscall.*` 入口）；登记 `proxy` 却在 spawn 前看不到兑现的**两半**（env 生成 + 后端能力问询）；登记 `denied`（本层兑现不了）；**启动点所在文件把整份父环境读出来**（`os.Environ` / `procx.InheritAllExcept`，按**文件**判）| `policy.yaml` 的 `egress_paths` 段 + `archcheck/egresspaths.go` |
| `tenant-surface-missing-evidence` | 带授权上下文形参 + 返回租户域类型的服务层函数，既没调登记的共享判定面、查询条件也没含过滤列、也没 `// tenant:iso <理由>` 声明 | `policy.yaml` 的 `tenant_gate` 段 + `archcheck/tenantgate.go` |
| `viewer-dropped` | 带授权上下文形参却完全不用（授权上下文被丢弃） | 同上 |
| `shared-surface-missing` | 登记在册的共享隔离判定面在代码里不存在（清单漂移 = 登记失效） | 同上 |
| `tenant-policy-missing` | 含租户字段的模型映射出的物理表不在 `tenant_tables_manifest` 清单里（漏登记 = 隔离策略不覆盖该表） | `policy.yaml` 的 `tenant_gate` 段 + `archcheck/tenantmanifest.go` |
| `harness-word-table-enum-missing` | 登记的判定词表没有导出枚举出口 / 出口读的是另一张表 / 登记与事实已偏离 | `policy.yaml` 的 `word_tables` 段 + `archcheck/wordtable.go` |
| `harness-word-table-handcopy` | 别的包手抄了登记词表的取值 | 引用主人常量，或在本包把它声明成自己的常量 |
| `harness-word-table-family-uncovered` | 登记的词表没覆盖它那一族常量的全部取值（常量加了、表没加）；或表的键不属于族前缀；或族豁免已失效 | 把族常量加进那张表；或改族前缀；或让**归属别处**的那张表（哪怕未登记）声明自己拥有的前缀（第十四阶段起族归属按最长匹配前缀派生，`family_exclude` 已废弃）|
| `harness-word-table-value-collision` | 两张判定词表有同形取值却没声明关系；或声明了关系却没撞车（声明失真）| `policy.yaml` 的 `word_tables` 段 `relations`（`same` / `distinct`）|
| `harness-word-table-relation-anchor-drift` | 同形关系的锚点指向的东西不再对（常量改名/被删、取值不再属于那张表、两端不成对、distinct 却锚到同一处声明）；**all 形态**下：共有取值在本端没有对应常量，或 distinct 的**每一个**共有取值都解析到同一处声明 | `policy.yaml` 的 `word_tables.relations` 段 `anchors`（`{all: true}` = 锚住两端共有取值的全集）|
| `tenant-policy-drift` | 清单登记的表在模型面已不存在（登记漂移 = 空登记） | 同上 |
| `tenant-table-name-missing` | 含租户字段的模型没有可从 AST 识别的表名方法（缺省 `TableName`）——解析不出物理表名，登记面核对不了 | 同上 |
| `harness-policy-self-check` | policy 自相矛盾：规则无执行体 / id 重复 / `scan_roots` 漏包 / 例外无 `until` / 包根指向空目录 / 租户门缺形状 / **词表登记面自相矛盾**（包名或类别写错、归一出口挂错表、重复登记、豁免缺理由）| `archcheck/selfcheck.go` + `archcheck/tenantgate_selfcheck.go` + `archcheck/selfcheck_wordtable.go` |

> 「未登记的包目录」这条更隐蔽：未登记的包对本门禁完全隐形，
> 任何模块都能 import 它而不判违规。

**例外必须显式登记**（policy `exceptions` 段：rule + path + line 三元组精确豁免 +
必须写 `until` 与拆除条件）。**当前例外数 = 0。**

### 4.3 import 路径级例外：`import_exempt`

`forbid_import_regex` 是**正则禁令**。当你要放行被禁依赖树里的**某个具体子包**
（而不是整棵树）时，用 `import_exempt`：

```yaml
rules:
  - id: agent-memory-single-entry
    forbid_import_regex: 'github\.com/yi-nology/agent-memory'
    scope_exclude: [internal/memstore]
    import_exempt:                      # 精确到 import 路径
      - github.com/yi-nology/agent-memory/evaluation
```

**三条要点**：

1. **粒度是整条路径相等**，不是正则前缀、不是通配。豁免 `a/b` **不会**连带放行
   `a/b/c`——要放行就自己登记。这样上游将来在 `a/b` 下新增一个包时，
   它**不会静默放行**（一条被静默放宽的声明等于没有声明）。
2. **写错会响亮**。空串、带尾斜杠、含通配/正则字符的豁免项**不可能匹配**，
   policy 自校验会直接点名该条目无效——因为它不生效时，报错若只指向 import，
   就是**误诊**（真正的问题在 policy 里）。
3. **声明面与执行体成对**。`import_exempt` 是 `Rule` 的字段，policy YAML 写错字段名
   会被**静默丢弃**；自校验会校验豁免项的形状，执行体判路径。

> **为什么不做前缀归一**：`a/b/` 看着像「放行 a/b 这棵树」，但那个语义正是这条能力
> 要消灭的——前缀放行等于**静默放宽整棵依赖树**。故写成前缀时它不匹配任何具体
> import 路径，让写错的人看到「豁免没生效」，而不是误以为整棵树放行了。

### 4.4 执行必须经登记簿：`execution_paths`

上游 `sandbox` 的核心语义是「选不到满足档位的后端即拒绝，**绝不降级**」，
且「兜不住预算的后端不被选中」——这两条由 `Registry.Resolve` 保证。

但**执行路径**可以完全绕开登记簿：直接调后端的裸执行入口（`ptc.Run`）。
于是档位下限、预算可兑现性、审批门三层**同时**失效，而**门禁全绿**——
说谎的不是档位表，是**执行路径**。

本仓的 `archcheck` 提供这条门禁的**可复用执行体**（宿主仓复用同一份，不必各写一遍）：

```yaml
# 宿主仓的 architecture-policy.yaml
execution_paths:
  enabled: true
  severity: error
  desc: 执行必须经 sandbox 登记簿（sandbox.Execute），不得直接调用裸执行入口
  ignore_dirs: [cmd, docs]
  # 本仓自己的裸执行入口登记在**判据执行体的内置表**里
  # （goagent-kernel/archcheck/execution.go 的 builtinRawEntries）——
  # 那是本仓的 API 事实，让每个仓各登记一份就是两份真相。
  # 这里只登记**宿主自己的**裸入口。
  raw_entries: []
```

**宿主侧要做的接线**（本仓 `ptc.Run` 是内置表登记的裸入口）：

```go
// 之前：绕过登记簿 ⇒ 档位下限 / 预算可兑现性 / 审批门三层全被跳过
res := ptc.Run(ctx, program, bindings, opts)

// 之后：走登记簿。Resolve 选不出满足要求的后端就**拒绝**（绝不降级）。
reg := sandbox.NewRegistry()
_ = reg.Register(ptc.Provider{Bindings: bindings, Stages: stages})
out, err := sandbox.Execute(ctx, reg, wantLevel, sandbox.Request{
    Program: program,
    Budget:  budget,
})
```

**判据的判定面与边界**

- 扫**生产代码**里的**限定符调用** `pkg.Fn(...)`；命中裸入口登记 ⇒ 违规。
- **改 import 别名绕不过去**：判据按 import 路径的**最后一段**（包名）匹配，
  不按限定符（限定符是调用方随手起的）。
- **同包内的调用不算绕过**（没有限定符，压根不会被扫到）。
- **陈旧登记当天即红**：登记的符号在仓内不存在 ⇒ 报出来（否则「这条执行路径
  没人管」会**看起来有人管过**）。
- **判不了的**：持有后端实例直接调 `provider.Exec(...)`——那要判**接收者的静态类型**，
  而 AST 拿不到变量的类型（要 `go/types`）。这类绕过靠人工 review 与接线纪律兜。

#### 同族第二条：子进程出口（`egress_paths`）

`execution_paths` 管的是「**进程内**执行不许绕开登记簿」；`egress_paths` 管的是「**子进程**起来之后
往哪儿连」。两者形态一样（声明表 + AST 判据 + 执行体可复用），但**判的是两件事**，
所以是两张表而不是一张：一张表两个含义，就会出现「登记了执行路径以为出口也管了」。

```yaml
# 宿主仓的 architecture-policy.yaml
egress_paths:
  enabled: true
  severity: error
  entries:
    - path: internal/exec/spawn.go     # 相对仓根
      fn: Start                        # 该文件里的启动函数（留空 = 整个文件）
      mode: proxy                      # unrestricted / proxy / denied
      notes: |
        为什么是这个档。这段会进报告，是给下一个读代码的人看的。
```

**五条判据（每条都对应一种「看起来登记了、其实没管」）**

1. **未登记即红**：扫生产代码里的 `exec.Command` / `os.StartProcess` 一类调用点，
   没进表就是违规。**`unrestricted` 也必须显式写**——「没登记」与「明确不限制」是两件事，
   前者的成因通常是漏了。
2. **`proxy` 必须在启动函数里看得见兑现的**两半****：① 在 spawn 之前真的看到
   `egress.Spec.Env(...)`（或带 `egress` import 的接收者形态）——声明落成了子进程变量；
   ② 也看到 `Spec.SatisfiedBy(...)`——**问过后端「你能强制这一档吗」**。只有 ① 是不够的：
   环境变量只是**建议**，一个能改 env 却没有任何强制面的后端照样让流量自由出网，而
   「env 生成了、挂上了」在报告里看起来就是「已兑现」。两半分开报错
   （`egress_unplumbed` / `egress_unprobed`），因为修法完全不同（加 Env vs 加后端）。
   条目可用 `plumbing` / `capability` 两字段指明你们用的方法名。
3. **`denied` 直接红**：本层（进程内 spawn 前改 env）**兑现不了**禁网。写了就是造假，
   报告会显示「已兑现」而网络其实通着。真要禁网，靠执行后端（`sandbox.LevelMachine` 一类）。
4. **陈旧登记即红**：登记的函数在仓里不存在了 ⇒ 报出来，否则「这条路径没人管」会看起来有人管过。

**判定面覆盖哪些启动形态（每一种都是实测漏判后才补上的）**

- 限定符调用 `exec.Command(...)`。**别名**（`import ex "os/exec"` ⇒ `ex.Command`）同样命中：
  限定符按 **import 路径**解析，不按名字猜。
- **点导入**（`import . "os/exec"` ⇒ `Command(...)`）：调用形态没有限定符，由「本文件点导入了哪个包」
  补齐。共享的 `importPathByName` 按设计会丢掉点导入（它要的是「限定符 → 路径」），故另有
  `dotImportPaths` 单独供给需要它的判据——**同一条盲区也存在于 `execution_paths`**（点导入 `ptc`
  之后 `Run(...)` 既无限定符、也不是本包实现），两条判据一起补。
- **包级初始化**（`var cmd = exec.Command(...)`）：不在任何函数里，报告里显示为「包级初始化」。
- 入口表按 import 路径归：除 `os/exec` 还含 `os.StartProcess` 与 `syscall.Exec/ForkExec/StartProcess`
  ——它们同样绕开出口强制面，也各自接受 Env，故「登记 + 兑现证据」对它们逐字适用。

> **点导入与包级这两条是 2026-11-06 实地注入才发现的漏判**：把 spawn 挪到包级变量、或换个导入
> 写法，旧实现就一声不响地全绿。收集器因此从判定文件里拆到 `archcheck/spawnsites.go`——
> 判定写错会误报（有人会来修），收集写漏是**静默放行**（没人会来），后者必须单列并逐形态钉住。

**本仓自己的实例**：`ptc/childpipes.go` 的 `newChildPipes` 登记为 `proxy`，
兑现方式是把 `SubprocessOptions.Egress` 交给 `spec.Env(base)` 生成子进程环境（失败即不起子进程）。

#### 同族第三条：判定词表登记（`word_tables`）

`execution_paths` 管「进程内执行不许绕开登记簿」，`egress_paths` 管「子进程起来之后往哪儿连」，
`word_tables` 管第三件事：**判定词表的取值不许散落成无名字字面量**。

词表是**决定结论**的取值集合（会话状态、任务状态、环节类别……）。内核侧已补齐导出枚举出口
（`contract.AllStatuses()` 等），但**消费侧**仍各写一份字面量：内核新增一个取值，消费方那份
不会跟着变，且没有任何判据会红——同一个状态在两处得到相反结论。

```yaml
# 宿主仓的 architecture-policy.yaml
word_tables:
  enabled: true
  severity: error
  tables:
    - pkg: contract         # 主人包（相对仓根）
      backing: statusClass  # 承载取值的那张表（map 字面量；键是常量名）
      enum: AllStatuses     # 导出枚举出口（必须读同一张表）
      predicate: IsKnownStatus   # 判定谓词（可选；声明了就得存在且读同一张表）
      # kind: decision       # 缺省即 decision：取值决定结论 ⇒ 判消费方
      # kind: input          # 取值只是外部喂进来的词形 ⇒ 不判消费方，只判出口同源
      # normalizer: ChartStatus   # 仅 kind: input：导出的归一出口（必须读同一张表）
      # family: Status       # 族前缀：主人包里以它开头的字符串常量都属于这张表
      # family_exclude:      # 族内有意不进这张表的一段（按前缀豁免，必须写理由）
      #   - prefix: DecisionSource
      #     reason: 判定来源是另一张表的族，不属于决策档位
  # not_registered:           # 有意不登记的判定词表候选（与 exceptions 同款纪律）
  #   - subject: protocol.severityAll（严重度：info/low/medium/high/critical）
  #     reason: 实测登记后 5 处命中，全是跨概念同形（日志级别的 info）
```

五条判据：

1. **`harness-word-table-enum-missing`** —— 登记的词表必须有导出枚举出口，且该出口读
   **同一张表**（读别的 map 就是第二个真相源：出口那份清单会与判定用的表漂移，而下游会信出口）。
   登记与事实偏离（包改名 / 表改名）当天即红——**陈旧的登记就是失管的登记**。
2. **`harness-word-table-handcopy`** —— 别的包在生产代码里手抄了登记**判定词表**的取值。
   判据是**出现点级**的：豁免精确到**那一处字面量**——

   - 它落在**主人表自身的定义处**（那是这张表的定义，不是抄）；
   - 它在**常量声明**里（本包给这个取值起了名字）；
   - 它不是字面量而是 `owner.Const` 这样的**引用**（结构性放行：限定选择器本就不是字面量）。

   三者都不是 ⇒ 红。
3. **`harness-word-table-predicate-drift`** —— 登记的**判定谓词**必须存在，且必须读
   **登记的那张表**。为什么要有这一面：枚举出口读同一张表只保证「表 ↔ 出口」不漂，
   而**判定面**（`IsKnownStatus` / `StepStatus.Valid` / `State.Valid`）是另一个
   独立读表的地方——它若另抄一份清单，判定就会与词表分叉，而没有任何判据会红。
   谓词形态：包级函数写函数名，方法写 `类型名.方法名`。
4. **`harness-word-table-normalizer-missing`** —— **输入归一词表**必须有一个**导出的**
   归一出口（`normalizer`），且它读登记的那张表。没有归一出口，消费方只能**自己**判
   这个输入词形 ⇒ 同一个输入词在不同消费方得到不同结论（漂移发生在输入侧而不是判定侧）。
   归一出口必须**导出**：宿主要用它，未导出的函数等于没有出口。
   （判定词表不该声明 `normalizer`——那是 input 类的属性；留着无用声明不如报错。）
5. **`harness-word-table-family-uncovered`** —— 登记的词表必须**覆盖它那一族常量的全部取值**。
   取值派生只有「表 → 取值」一个方向 ⇒ 主人包**新增**一个同族常量却忘了加进表，
   那个新取值的消费侧从此**没人管**（它不在表里 ⇒ 手抄判据判不到它；它不在枚举
   出口里 ⇒ 宿主看不到它），而它在主人包里是个正经的导出常量 ⇒ 谁看代码都以为
   它受管。⇒ 声明 `family`（族**前缀**，不是常量名清单——后者是第二份会漂的真相），
   判据两条：**族 ⊆ 表**（族成员漏进表当天即红）与**表 ⊆ 族**（族前缀写错当天即红）。
   同族里常含**另一张表**的常量（`protocol` 的 `Decision*` 同时含两张表的成员）
   ⇒ 给按前缀的豁免口 `family_exclude`，**必须写理由**，且**豁免失效即红**
   （豁免今天什么都没豁免，却会在将来出现同名常量时无声生效）。
6. **`harness-word-table-value-collision`** —— 两张**判定词表**有同形取值时，必须在
   `word_tables` 段的 `relations` 里**显式声明关系**：

   - `same`     ：同一概念（一张表的别名/子集），引用任一家都对；
   - `distinct` ：不同概念（同形只是拼写巧合），手抄方必须**在本包命名**。

   为什么要有这一条：手抄判据只按「值在不在表里」判 ⇒ 它分不出「这处字面量想说的
   是哪张表」。而**错误指路比不指路更坏**——把任务失败改成引用会话失败常量 = 语义损坏。
   ⇒ 声明了关系，手抄文案就**按关系改口**：`same` 时列出全部候选常量；
   `distinct` 时要求「在本包另起一个名字」。
   声明了关系却**没撞车** ⇒ 报红（**声明失真**：今天没关系，将来一旦同形就会无声生效）。
   表身份用 `pkg` + `enum`（一个包里可能有多张表）；关系是**对称**的（用无序对
   做键，否则「同一对表声明两次」会被漏判）。
7. **@@harness-word-table-relation-anchor-drift@@** —— 同形关系的**锚点**必须仍指向真实存在的东西。
   关系声明本身是**人写的事实**（「会话失败 ≠ 任务失败」是人判的）⇒ 主人包若改了某个
   取值的**含义**（不是拼写），关系不会跟着变 ⇒ 一条**说谎**的关系会安静地留着。
   ⇒ 关系的 `anchors` 段把关系绑到**具体常量**上，四条判据当天即红：

   - 锚点常量**改名或被删**（= 登记漂移）；
   - 锚点常量的取值**不再属于它那张表**（锚点说的是别的表的词）；
   - 两端锚定的取值**不成对**（关系的前提「同形」不成立）；
   - `distinct` 的两端锚点**解析到同一处声明**（同一处声明不可能是两个概念 ⇒
     「不同概念」是说谎的，指路会要求本包另起名字，而正确做法是引用主人常量）。

   锚点是**可选的加固**：键是字面量、根本没有常量可锚的表写不出锚点（强制要求会把
   那种表逼成「登记不进来」⇒ 门禁逼人造假比没有锚点更坏）。但**半边锚点没有配对
   语义** ⇒ 要写就两端都写（自校验当天即红）。

   **all 形态（第十四阶段）**：抽样锚定只钉住一两个取值 ⇒ 其余**共有**取值漂了没人判。
   `a: [{all: true}]` / `b: [{all: true}]` 把锚定面换成「两张表的共有取值集合」
   ⇒ 四条判据原样复用，但覆盖面从抽样变成全覆盖：每个共有取值在本端必须有对应常量，
   且 `distinct` 的**每一个**共有取值都要解析到不同处声明。
   实测同一句谎话（把 distinct 指向同源的两张表）：抽样形态报 1 条，all 形态报 **12 条**
   （每个共有取值各一条）⇒ 覆盖面的差别是可数的，不是修辞。

**登记与不登记都要留痕**：登记面是稀疏的，而稀疏本身没人记——「这张表我们决定
不登记」若只留在某个人脑子里，下一个人会重新调研一遍，甚至直接把它登记进去。
⇒ policy 的 `not_registered` 段逐张写明**是什么**与**为什么不登记**（自校验要求
理由非空）。登记与否**由实测决定**：单表注册 → 跑真门禁 → 数手抄命中量。本轮实测
12 张候选表：5 张命中 0（登记），7 张命中 1~28 处且**全是跨概念同形**（日志级别的
"info"、门禁严重度的 "error"、到处都有的 "unknown"）⇒ 不登记——判据退化成拼写
匹配器时，判红到没人信它，它就等于没有。

**多表同形要全部点名**：同一个取值可能属多张表（`contract` 的 "failed" 是会话状态、
`taskboard` 的 "failed" 是任务状态）⇒ 同一处只报一次，但文案必须列出**全部归属**——
只报第一张表是**错误指路**（把人引到另一张表的常量上），比不报更坏。

**为什么是出现点级而不是包级**：包级判据会放过「同一个包里既定义了常量、又写了同一
取值的裸字面量」——而那正是**命名过了却还写字面量**，名字就在眼前却不用，
比「压根没命名」更该改。

**为什么允许「本包自有常量」**：同一个拼写在不同包里可以是不同概念（`contract` 的 "failed"
是会话状态，`delegate` 的 "failed" 是回执状态）。要求它**命名**，而不是要求它**不许用**——
否则合法用法也会被判红，而判红到没人信它，它就等于没有。

**两类词表（`kind`）**：`decision`（缺省）判消费方；`input` 只判「表在 + 出口在 +
出口读同一张表」，**不判消费方**——输入归一词表的取值是外部喂进来的词形（healthy / down /
正常 / 宕机……），消费方自由使用它们，那是输入数据不是判定依据。
不分的代价是实测出来的：把 43 个图表词形按判定词表登记会命中 17 处，大半是假阳
（`{"error": ...}` 这类 JSON 键、HTTP `"ok"` 体）——判据成了拼写匹配器。
`kind` 的值域是**封闭**的（只有 `decision` / `input`）：写错的类别名若被当成
「不是 decision」，这条登记就静默降级成「不判消费方」——未知类别当天即红。

**取值由代码派生，policy 里不写取值清单**：清单本身是第二份会漂移的真相，而它漂移的方向恰好是
「门禁判的和代码跑的不是一套词表」——失败形态是门禁变绿。

**判不了什么**：只判**字面量**形态（常量 / 枚举 / 映射键都判不到——它们已经是名字了）；
判定面只含**生产代码**（测试夹具是临时的）；判据问的是「**有没有名字**」，不是
「这段字面量在不在**判定路径**上」——后者要 `go/types` 档的类型信息
（`archcheck` 的 `RunOptions.TypeAware`），本轮做不到。

### 4.5 租户过滤漏拼门：`tenant_gate`

多租户仓最贵的一次遗漏是「查询忘拼过滤列」——它不会报错、测试可能全绿，只会
在某天变成一次跨租户越权。`archcheck` 把 bianque 的 `tenantcheck` 判据下沉为
通用执行体：**形状归宿主 policy，执行体不出现任何宿主字面**。

```yaml
# 宿主仓的 architecture-policy.yaml
tenant_gate:
  enabled: true
  severity: error
  roots: [internal/service]            # 服务层读面扫描面（递归）
  tenant_type_roots: [internal/model]  # 租户域类型推导面（缺省 = roots）
  auth_context_types: [identity.Viewer] # 授权上下文类型（可带包名）
  tenant_fields: [TeamID]              # 含该字段的 struct = 租户域类型
  filter_columns: [team_id]            # Where/Raw 字面量含其一 = 隔离证据
  shared_surfaces:
    - dir: internal/service/identity
      name: TeamRowsVisible
      note: 列表读面行级团队过滤
  tenant_tables_manifest: docs/tenant_tables.txt  # 租户表登记面（可选，声明后启用 4-6 三条判据）
  # table_name_method: TableName                   # 物理表名方法名（缺省 GORM 惯例）
  # tenant_table_type_roots: [internal/model]      # 登记面专用推导根（缺省 = tenant_type_roots）
```

六条判据（issue #177 补齐到 6，与 bianque tenantcheck 同口径；规则号见 §4.2 表）：
**缺证据即红**、**授权上下文丢弃即红**、**登记面不存在即红**，以及声明
`tenant_tables_manifest` 后启用的**租户表登记面三条**——模型面推导出的租户表
（登记面推导根下含租户字段、且有可解析 `TableName()` 字面量的 struct）
与清单做**双向核对**：缺登记即红（隔离策略不覆盖这张表）、登记漂移即红（清单
里的表模型面已不存在）；含租户字段的模型没有可解析表名方法也红。清单是宿主
自己 dump 的产物（每行一个物理表名，`#` 起行为注释）——宿主持有的 RLS 注册表
是 Go 符号，执行体调不到；清单与注册表的一致性由宿主的一条校验测试保证。
前三条各自可单独豁免（policy `exceptions` 精确到 rule + path + line）。
租户类型清单**不另立**——推导根下任何含 `tenant_fields` 字段名
的 struct 自动入面。

⚠️ **读面与登记面的推导根可以不同**：读面租户域类型（`tenant_type_roots`）常
包含服务层载体（输入/行 DTO——带租户字段但**不映射物理表**），跟着读面扫登记
面会把载体误报 `tenant-table-name-missing`。载体多的宿主用
`tenant_table_type_roots` 把登记面收窄到模型层（缺省与 `tenant_type_roots`
同根，不声明 = 旧口径）。

**判不了什么**：调用判定面但没用返回值、过滤条件写在注释里——静态证据存在性
判不了语义生效。那归宿主运行期判据（`tenant` 包的 `Require/Can` 与配额
`Enforce` 是它的运行时对偶）。本仓 policy 已按自身形状启用（`tenant` 包；
无模型层故不声明 `tenant_tables_manifest`）。

### 4.6 在本仓加新包的正确顺序

**先改 policy 再写代码。**

```yaml
- id: 新包名
  roots: [新包名]
  requires: []        # 真实依赖，不是理想形态
  owner: harness
  managed: true
  notes: ...
```

不登记则 `architecture-check` 立刻红。判定面**只含生产代码**（跳过 `_test.go`）：
依赖方向决定交付物里出现什么，测试依赖是临时的。

### 4.7 结构阈值

`max_file_lines: 400`，`thresholds.enforce: true`，超限即非零退出。

**拆分只为满足机械阈值**——若某个模块拆完仍读不动，该重新分组，
不要靠继续拆小来假装问题解决了。

另有 `max_methods_per_type`（god object 判据），它比的是**实质方法数**而不是方法
总数——类型退化成 façade（实现下沉到子包、自己只做转发）之后，方法数 ≈ **公共 API
表面规模**，不是复杂度集中度，拿它判 god object 等于「把正确的工作判成错的」。

「转发壳」= 方法体只有一条转发语句、且剥掉注释与空白后长度 ≤
`forwarding_shell_max_chars`（**不写 = 缺省 140**；显式写 0 = 关闭该区分，
判定逐字退回旧行为）。老 policy 无需改动即可享受修正。

---

### 4.8 多租户隔离的落点（issue #195）

内核不实现租户体系，但也不再把「数据面」整个推给宿主——任务板这一面已有
**可判定的落点**，其余面仍归宿主。边界写死在这里，不留实现自由。

**已进存储键与领取条件（内核）**

- `taskboard.Task.Tenant`：任务身份是 `(Tenant, ID)`；空租户 = 默认/单租户
  命名空间（旧布局逐字不变）。
- `coord.NewTenantBoardAdapter`：键由 **ctx 里的租户作用域**生成
  （`coord/t/<escaped-tenant>/task/<id>`），`List/Claim/Release/Complete/Renew`
  全部按租户过滤；他租户任务在 List 不可见、在写路径一律
  `taskboard.ErrNotFound`（不可见 = 不存在）；缺租户/空租户一律
  `tenant.ErrNoContext`（fail-closed，与写路径现有判据同口径）。
- `taskboard.Check`：混合租户快照按 `(Tenant, ID)` 判重，依赖只在同租户内
  解析（跨租户依赖按不存在处理）。
- `boardtest.RunTenant`：租户契约套件——跨租户不可见/不可领（含并发）、
  同 id 不同租户互不覆盖、跨租户令牌不能收场、缺租户 fail-closed。

**接线（宿主）**

```go
board, err := coord.NewTenantBoardAdapter(backend) // 不是 NewBoardAdapter
if !board.TenantScoped() { /* 启动自检：多租户部署拿错适配器 */ }
// 请求入口把租户注入 ctx；worker/Board 调用原样透传（WithoutCancel 保留值）：
ctx = tenant.WithContext(ctx, tenant.Context{Tenant: tenantID})
```

单租户部署保持 `NewBoardAdapter`：键布局、任务记录 JSON、行为逐字不变。
**隔离必须显式装配**——单租户适配器不解释 ctx 里的租户上下文，注入租户
不会静默变成隔离，也不会报错；多租户部署用 `TenantScoped()` 做启动断言。

**仍归宿主（内核不提供、也不假装提供）**

- 副作用幂等键（`sideeffect` 的 Key 由宿主组装：必须把租户纳入身份，
  否则 A 租户的副作用会让 B 租户被判「已施加」）；
- 事件流（`eventbus.Event` 没有租户字段，Hub 不按租户过滤：共享 Hub 时
  由宿主在载荷/订阅侧自筛，或每租户一个 Hub）；
- 运行留痕/审计/轨迹（`trajectory`、`controlplane.AuditEntry`）的租户归属；
- 数据库行级隔离（RLS / 唯一约束 / 连接级租户变量）；
- 自定义 `taskboard.Board` 实现：不接 `boardtest.RunTenant` 不算声明支持
  租户隔离。

### 4.9 `make race` 红了怎么读

`make race` 由 `cmd/raceprobe` 接管：它跑 `go test -race`，并在失败时给出**形态判定**
（而不是只丢一行 FAIL）。五种形态与各自的处置：

| 形态 | 指纹 | 你该做什么 |
|---|---|---|
| `data-race` | 竞态检测器报了 `WARNING: DATA RACE` | **代码面**：按报告里的 goroutine 栈修共享状态 |
| `test-hang` | 输出里有 `goroutine N [..]:` 栈 | **代码面**：栈里指着卡住的 goroutine |
| `startup-wedge` | 有 cmd/go 的兜底闹钟那行、**但一行栈都没有** | **环境面**：Go 运行时压根没起来 ⇒ 改代码没用，查机器面（内存压力 / 并行启动的测试二进制太多 / 同时有别的构建在跑）|
| `test-failure` | 普通断言失败（有 `--- FAIL:`）| **代码面**：按失败用例名修 |
| `unknown` | 都没有 | **别猜**：去看落盘的原始输出 |

判定依据是**机制指纹**而不是症状：「超时了」两种形态都超时；「**有没有 goroutine 栈**」
才分得清——能留下栈说明 Go 运行时活到过（用例问题），留不下说明它压根没启动
（机器面问题）。

`startup-wedge` 会自动做一次**串行复核**（同树 `-p 1` 重跑受害包，即「拿掉并行启动
这个变量再跑一次」的对照实验）：复核通过 ⇒ 判「环境面已复核」（门禁判绿，但**留痕**：
结论块 + 原始输出 + 复核输出三份落在 `RACE_EVIDENCE_DIR`，默认
`$TMPDIR/goagent-kernel-race`）；复核失败 ⇒ 按复核结论判红。

三段预算互相咬合（`RACE_GO_TIMEOUT` → cmd/go 兜底闹钟 = 它 + 1m → `RACE_WATCHDOG`
**必须晚于**兜底闹钟）：早了就变成我们先杀 `go test` ⇒ 拿不到楔死指纹 ⇒ 判定退化成
`unknown`（工具自己会拒这种配置，退出码 2）。

---

## 5. 本地多仓并行开发

本仓处在 `oagentkit` / `oekit` 矩阵里，改本仓时经常要同时改上游仓。
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

**配套纪律**

1. **go.mod 零 replace**；本地 `go.work` 由 `.gitignore` 兜底。
2. **go.mod 里 require 的版本必须是已发布的 tag**。若确需依赖未发布的新包，用
   `make devlink`，不要动 go.mod。CI 在干净环境构建，未发布版本解析不到直接红。
3. **workspace 期间别跑 `go work sync`**——它按 workspace 的构建列表**回写**各模块的
   `go.mod` / `go.sum`，仓不全时用不到的依赖会被直接删掉。要整合同步，
   先 `make devunlink`，再在单模块下 `go mod tidy`。
4. **`make devlink` 可重复执行**：已有 `go.work` 时走 `go work use -add` 追加。
5. **验下游而不是只验本仓**——本仓作为主模块绿，不代表作为依赖也绿。

**升级第一方依赖前**，先用 `git ls-remote --tags` 核一遍候选版本真的存在。
`go list -m -versions` 报的是 **proxy 的缓存目录**，不是远端现实——
tag 被删后条目仍留着直到缓存过期，而拿它当采购单会得到「本地全绿、下游全红」。

**若上游发生过 tag 重写**，先清本仓的 setup-go 缓存再推：
`actions/setup-go` 的缓存 key 含 `hashFiles('**/go.sum')`，go.sum 一改 key 就变，
于是命中 restore-keys 前缀回退，恢复**上一份**缓存——那份建于重写之前，
里面存的就是旧模块副本，表现为 checksum mismatch 而本地全绿。

---

## 6. 坑速查表

接线完成后拿这张表自查。左列是症状，右列是真因。

| 症状 | 真因 |
|---|---|
| 工具在目录内但 schema 未取回，模型原样重试仍被拒 | 正常行为——门禁**不激活**工具（issue #286）。恢复路径是「先取回 schema 再调用」，不是重试。看 `Retryable(err)` 是不是 true |
| 同一个工具被反复拒，每次理由都不同 | 你没用 `guardset`，链在第一个拒绝处收口了 |
| 重启后陈旧快照写畅通无阻 | `SeedFromStore` 没回灌，`SeqGate` 重启即空 |
| 落库失败后某个环节再也提交不了终态 | `commitgate` 的 `Release` 没调 |
| 压实后界面还显示「上下文紧张」 | 没显式 `Meter.Shrink` 把账改回来 |
| 净移除量对不上，方向是「看起来更省了」 | 用了 `GrossRemovedTokens`，要减应该用 `NetRemovedTokens` |
| 摘要写进消息的指纹能被跨库比对 | 占位暴露了全量摘要，应该只给 12 位前缀 |
| 模型从未见过某张图片，之后却在引用它 | `offload` 首轮就替换了，或落盘失败仍被标记 |
| 会话一长跑内存就涨、收口卡住 | `delta.Pacer` 被做成了长生命周期对象，应每环节新建 |
| 一个卡死会话把所有会话陪葬 | 用了 `Drain` 而非 `DrainSession` |
| 挂起态莫名其妙消失 | 落列绕过了 `pending.ColumnValue` |
| 超大模型输出居然「解析成功」且置信度不低 | 命中过截断式上限失效的坑，现已 fail-closed；若你 fork 了本仓请确认走的是 `ErrNotReport` 分支 |
| SSE 进度条停在中间态 | 正常——总线有损，靠 DB 回放游标补达；先确认发布路径是「先落库再广播」 |
| 判定环拒绝后 host.<tool>() 仍然执行 | `ptc.Options.Stages` 没传 |
| 工具重试把一次副作用变成两次 | 只看了 `Retryable`，没看 `SafeToResubmit` |
| `make check` 报域词污染 | 领域资产漏进内核了——它该在宿主仓 |
| 加了个新包门禁立刻红 | 没在 `architecture-policy.yaml` 的 `modules` 段登记 |
| 本地全绿但 CI 报 checksum mismatch | 见 §5 的 tag 重写 / 缓存回退两条 |
| 想临时停掉某条守卫，只能重启进程 | 你在用 `guardset.Stage(set)` 绑死了某一份——改用 `guardset.Assembly`，撤销 = `Without` + `Swap`（换整份，链不用重接）|
| 撤销某条守卫后，宿主以为降级生效了，实际没有 | `Set.Without` 对不存在的名字**会报错**（`ErrUnknownGuard`）；若你绕过了它去改 `Set`，那是两个真相 |
| 配额预占泄漏，额度再也回不来，调度慢慢停摆 | 本版 `scheduling.QuotaStore` 扩了 `ReserveLease` / `Reclaim` / `SweepExpired` 三个方法——只实现旧的 `Reserve` / `Release` 编译不过；Worker 失联后没人 `Release` ⇒ 额度永久占用（见 §3.16）|
| 用户取消被判成停机，会话永久滞留 running | 调了 `finalize.Route(ctx)` 却没传宿主自有取消哨兵——路由表已收口为 `deadline.Route` 唯一一份（#355），须改调 `finalize.Route(ctx, mySentinel)` |
| 同一族的两个错误用 `==` 比出来不相等 | 跨包哨兵现在**同一实例**（`finalize` / `deadline` 的哨兵是 `contract` 的别名）——`errors.Is` 不受影响，但用 `==` 严格比较的调用点要复核 |
| 出站判定的用例在本机假红、CI 却绿 | 判定依赖真实 DNS——用 `netguard.CheckURLWithResolver` / `CheckChainWithResolver` 注入解析器（见 §3.9.1.2）|

---

## 7. 延伸阅读

| 文档 | 回答什么 |
|---|---|
| [README](../README.md) | 定位、边界纪律、包清单、门禁总览、下游升级须知 |
| [AI-GUIDE.md](AI-GUIDE.md) | **改本仓的 AI 代理**：任务 → 改哪些文件 → 哪条门禁拦 → 怎么验收 |
| [OAGENTKIT-OVERLAP.md](OAGENTKIT-OVERLAP.md) | 与 oagentkit 的重叠面盘点：哪些该做、哪些上游已有 |
| [CHECKPOINT.md](CHECKPOINT.md) | 续跑契约的设计推导与三条不变量 |
| [AGENTS.md](../AGENTS.md) | 代理在本仓干活的工作纪律 |
| [SPLIT_PLAN.md](SPLIT_PLAN.md) | 从宿主仓拆分的分层依据（**2026-10-17 历史快照**，非当前包清单）|
| [MIGRATION_OAGENTKIT_20261018.md](MIGRATION_OAGENTKIT_20261018.md) | 门面迁移与 errors 链路改写的执行记录（**历史快照**，2026-10-18 已执行完毕）|
| [SELF-AUDIT-20261018.md](SELF-AUDIT-20261018.md) | 独立复核发现并修复的缺陷（**历史快照**，2026-10-18 收尾）|

规则源：[`architecture-policy.yaml`](../architecture-policy.yaml)。
执行体：`archcheck` 包（`deps.go` 判依赖方向、`facade.go` 判门包、`scan.go` 跑正则规则、`selfcheck.go` 校验 policy 自身）；`cmd/archcheck` 只是它的 CLI 装配。

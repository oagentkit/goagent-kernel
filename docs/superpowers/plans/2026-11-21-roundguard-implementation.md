# 回合守卫 roundguard — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**状态**：待实施（2026-11-21 起）。设计定稿见
[spec](../specs/2026-11-21-roundguard-agent-loop-design.md)（1017 行，含 §1.4 五家实现实测对照表）。

**Goal:** 新增 `roundguard` 机制包，为宿主提供模型侧循环的三条**跨宿主共享判据**——回合结局分类、回合次数预算、无进展（循环）检测——并合成一条可执行结论。

**Architecture:** 纯判据包，**零内部依赖**（只依赖 `github.com/oekit/pkg/errors`）。五个纯函数（`Classify` / `CheckRounds` / `CheckToolCalls` / `CheckTool` / `Detect`）+ 一个进程内计数器（`Meter`）+ 一个组合入口（`Decide`）。内核**不调模型**：宿主把「模型这一回合说了什么」映射成 `Outcome`、把「这一回合干了什么」映射成 `Observation[]`，本包只做判定。**额度维度（事实）与处置口径（策略）分开**：`Budget.Policy` 决定额度用尽后走「强制收尾」还是「失败即止」——五家主流实现三种口径，没有共识（spec §1.4）。

**Tech Stack:** Go 1.27.4（标准库 `slices` / `sync`）、`github.com/oekit/pkg/errors`（唯一 errors 包）、`gopkg.in/yaml.v3`（policy 已是该格式，无需新依赖）。

## Global Constraints

- **本机工具链**：`export PATH=/Users/zhangyi/sdk/go1.27.4/bin:$PATH GOTOOLCHAIN=local`（本机 `go version` 已是 go1.27.4；不设 `GOTOOLCHAIN=local` 会卡在工具链下载）。
- **CGO_ENABLED=0**：build / test / vet 一律带。**只跑 `make` 目标**（Makefile 内置 `GOENV`），别手敲 `go test`。
- **errors 包**：只用 `github.com/oekit/pkg/errors`（`New` / `Wrap(err, msg, kv...)` / `Is` / `As` / `Cause`）。禁 stdlib `errors` 与 `github.com/pkg/errors`。**门禁会红**。
- **单文件 ≤ 400 行**（`architecture-policy.yaml` 的 `thresholds.max_file_lines`，`enforce: true`）。
- **禁裸 goroutine**：生产代码不得出现 `go func`。本包无并发面——`Meter` 用 `sync.Mutex`（判据包不该把加锁方式留给调用方各自发明）。
- **禁 printf 回调**：`logf func(string, ...any)` 一类签名不进本包。
- **新增包必须同时登记三处**（漏任一处门禁当场红）：`architecture-policy.yaml` 的 `modules` 段（漏登记 ⇒ 该包目录对依赖方向门禁**完全隐形**）、`thresholds.scan_roots`（漏登记 ⇒ 「超长文件」「god object」两条阈值对它隐形；policy 自校验会红）、`api-lifecycle.yaml`（漏登记 ⇒ `make apilifecheck` 报「未登记」）。**包注释必须同步打标记** `生命周期: Experimental（v0.52.0 定级）——…`（两处不一致即红）。
- **词表登记**：新增 `Xxx*` 常量族 ⇒ **同一个提交内**在 `word_tables.tables` 登记对应表（五项齐全：`backing` / `enum` / `predicate` / `family` / `desc`）；同形撞车须在 `word_tables.relations` 显式声明 `same` / `distinct`（不声明即红）。取值由代码派生，**policy 里不写取值清单**。
- **错误哨兵**：导出错误必须登记 `docs/error-semantics.md`（小节名 = `api-lifecycle.yaml` 的 `pkg` 名，逐字一致），否则 `make apilifecheck` 红。
- **README 两处一起翻**（同一个提交里）：包表新增一行（生产行数，25% 相对容差）、**工程底座表**三项（生产代码行数 / 生产文件数 / 测试文件数——`docsync` **判相等，不留容差**）。刷新命令见 Task 7 Step 3。
- **测试夹具用合成数据**：不得出现「内存分析Agent」「本机」「故障诊断」等词面——夹具是契约的示例，示例带领域味等于把契约钉死在某个域上。
- **判据先于实现**：改任何判定逻辑必须同时给出「坏实现会红」的反向验证用例（Task 6 集中补齐边界钉死用例）。
- **提交身份**：`MurphyYi <zy84338719@hotmail.com>`；`git add <具体路径>`，**禁 `git add -A`**。
- **中文文档/注释与 commit message 提交前扫 U+FFFD**：`grep -rn $'\xef\xbf\xbd' roundguard/ docs/`。
- **历史不可篡改**：`CHANGELOG.md` 只插新段（按 tag 切段），不改历史段落。
- **覆盖率**：全局 60% + **逐包下限 70%**（`PKG_COV_MIN`）。新包默认无豁免。
- **分支与 PR**：feature 分支 → PR → CI 全绿 → 合并 → 打 annotated tag。GitHub 分支保护的 required contexts 与 `make check` 的目标数**逐字对齐**（本轮不加 CI job，故不改分支保护）。

---

## 拍板口径（spec §12 十个未决点 → 本计划采用值）

spec §12 把十项记为「评审需拍板」。本计划**按下表的取值写死代码**；评审若改任一项，
**先改 spec §12，再改本计划**（本仓纪律：先改声明面，再写代码）。

| # | 未决点 | 本计划采用值 | 与 spec §12「默认取值」的关系 |
|---|---|---|---|
| 1 | 包名 | `roundguard` | 一致（取默认） |
| 2 | 「回合」单位中文名 | 回合 / round | 一致 |
| 3 | `Meter` 是否并发安全 | 带锁（`sync.Mutex`） | 一致 |
| 4 | `Completed` 与 `ToolCalls>0` 并存 | 「要工具」优先（`ReasonToolCalls` + `Continue=true`） | 一致 |
| 5 | `Config.Window` / `Repeat` 缺省值 | **8 / 3** | **不一致（spec §12 写的是 6/3）**——计划阶段发现窗口 6 时震荡的停止档永不可达（Task 3 Step 4 已安排改 spec 为 8 + 向上钳位） |
| 6 | 跨进程回合累计 | 不做（进程内 `Meter`，边界写进包注释） | 一致 |
| 7 | 额度用尽后的缺省处置口径 | `PolicyForceFinal` | 一致 |
| 8 | 导出 API 面上限 | 设门禁（Task 5） | 一致 |
| 9 | 处置口径是否按额度维度分别配置 | 不配置（一个 `Policy` 管全部额度） | 一致 |
| 10 | 额度维度的缺省值 | 不设缺省（全 0 = 不限，宿主显式声明） | 一致 |

---

## File Structure

**新建**

| 文件 | 职责 |
|---|---|
| `roundguard/roundguard.go` | 包头（为什么/划界/三分命名/生命周期标记）+ `Reason` 类型 + 10 个常量 + `String` + `Valid` + `IsFailure` |
| `roundguard/word.go` | 四张判定词表的 backing 表 + 枚举出口（`AllRoundReasons` / `AllLimits` / `AllShapes` / `AllStopPolicies`）+ 各自的 `Valid` 谓词 |
| `roundguard/classify.go` | `Outcome`（宿主注入的机械事实）+ `Route` + `Classify`（结局分类，纯函数） |
| `roundguard/budget.go` | `StopPolicy` + `Budget` + `Validate` + `Used` + `Verdict` + 三个 `Check*`（额度判定，纯函数） |
| `roundguard/meter.go` | `Meter`（进程内回合账，带锁） |
| `roundguard/errors.go` | `ErrInvalidBudget` 哨兵 |
| `roundguard/detect.go` | `Observation` + `Shape` + `Finding` + `Config` + `Detect`（无进展检测，纯函数） |
| `roundguard/decide.go` | `Snapshot` + `Decision` + `Decide`（组合结论，纯函数） |
| `roundguard/exportbudget_test.go` | 导出 API 面上限门禁（抄 agentloop 的 `apibudget_test.go`） |
| `roundguard/mutation_test.go` | 反向验证矩阵：把每条判据的边界钉死（坏实现必须会红） |

**修改**

| 文件 | 改动 |
|---|---|
| `architecture-policy.yaml` | `modules` 段增 `roundguard`；`thresholds.scan_roots` 增 `roundguard`；`word_tables.tables` 增四张表；`word_tables.relations` 增四条 `distinct` |
| `api-lifecycle.yaml` | 增 `roundguard`（Experimental，since v0.52.0） |
| `docs/error-semantics.md` | 增 `### roundguard` 小节（登记 `ErrInvalidBudget`） |
| `README.md` | 包表增一行；**工程底座表**三项刷新 |
| `docs/AI-GUIDE.md` | 包数与分组表刷新（机制面包数 +1） |
| `docs/USAGE.md` | 增「宿主怎么接 roundguard」章节（接线示例 + 三条纪律） |
| `docs/superpowers/specs/2026-11-21-roundguard-agent-loop-design.md` | Task 3 的窗口下限修正（见该任务的 Step 4） |
| `CHANGELOG.md` | 发版时插新段（Task 8） |

---

### Task 1: 回合结局分类（`Reason` 词表 + `Classify`）+ 三处登记面

**Files:**
- Create: `roundguard/roundguard.go`
- Create: `roundguard/word.go`
- Create: `roundguard/classify.go`
- Test: `roundguard/classify_test.go`
- Modify: `architecture-policy.yaml`
- Modify: `api-lifecycle.yaml`
- Modify: `README.md`

**Interfaces:**
- Consumes: 无（首个任务）。
- Produces:
  - `type Reason string`，常量 `ReasonCompleted` / `ReasonToolCalls` / `ReasonHandedOff` / `ReasonPaused` / `ReasonFailed` / `ReasonNoOutcome` / `ReasonAborted` / `ReasonBudgetExhausted` / `ReasonForcedFinal` / `ReasonStalled`（各 10 个）
  - `func (r Reason) String() string`、`func (r Reason) Valid() bool`、`func (r Reason) IsFailure() bool`
  - `func AllRoundReasons() []string`
  - `type Outcome struct { ToolCalls int; Completed bool; Handoff bool; Paused bool; Failed error; Aborted bool; ForcedFinal bool }`
  - `type Route struct { Reason Reason; Continue bool }`
  - `func Classify(o Outcome) Route`

- [ ] **Step 1: 写失败测试**

```go
package roundguard_test

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/roundguard"
)

// 合成数据：用中性词面构造事实，不带任何领域味。

// TestClassifyToolCallsContinues 钉住：模型请求工具 ⇒ 允许进入下一回合。
func TestClassifyToolCallsContinues(t *testing.T) {
	r := roundguard.Classify(roundguard.Outcome{ToolCalls: 2})
	if r.Reason != roundguard.ReasonToolCalls {
		t.Fatalf("要工具应判 %q，实得 %q", roundguard.ReasonToolCalls, r.Reason)
	}
	if !r.Continue {
		t.Fatal("要工具应允许继续")
	}
}

// TestClassifyToolCallsBeatsCompleted 钉住**判定次序**：模型同时给终答与工具调用时，
// 「还要工具」才是控制流——终答排前面会把未执行的工具判成已完成。
func TestClassifyToolCallsBeatsCompleted(t *testing.T) {
	r := roundguard.Classify(roundguard.Outcome{ToolCalls: 1, Completed: true})
	if r.Reason != roundguard.ReasonToolCalls {
		t.Fatalf("同时给终答与工具调用应判 %q，实得 %q", roundguard.ReasonToolCalls, r.Reason)
	}
}

// TestClassifyForcedFinalBeatsCompleted 钉住：强制收尾步同时满足 Completed ⇒
// 判强制收尾（宿主据此知道「这是被上限逼出来的收尾」）。
func TestClassifyForcedFinalBeatsCompleted(t *testing.T) {
	r := roundguard.Classify(roundguard.Outcome{Completed: true, ForcedFinal: true})
	if r.Reason != roundguard.ReasonForcedFinal {
		t.Fatalf("强制收尾步应判 %q，实得 %q", roundguard.ReasonForcedFinal, r.Reason)
	}
	if r.Continue {
		t.Fatal("强制收尾步不该判为可继续（要转收束）")
	}
}

// TestClassifyZeroOutcomeIsConservative 钉住零值形态：宿主忘了填 Outcome 时判收敛，
// 而不是「无事发生所以继续」——那会让空循环空转到超时。
func TestClassifyZeroOutcomeIsConservative(t *testing.T) {
	r := roundguard.Classify(roundguard.Outcome{})
	if r.Reason != roundguard.ReasonNoOutcome {
		t.Fatalf("零值形态应判 %q，实得 %q", roundguard.ReasonNoOutcome, r.Reason)
	}
	if r.Continue {
		t.Fatal("零值形态不该判为可继续")
	}
}

// TestIsFailureSeparatesGracefulFromFailure 钉住失败型与温和型可区分。
func TestIsFailureSeparatesGracefulFromFailure(t *testing.T) {
	failures := []roundguard.Reason{
		roundguard.ReasonFailed,
		roundguard.ReasonNoOutcome,
		roundguard.ReasonAborted,
		roundguard.ReasonBudgetExhausted,
	}
	for _, r := range failures {
		if !r.IsFailure() {
			t.Fatalf("%q 应判为失败型", r)
		}
	}
	graceful := []roundguard.Reason{
		roundguard.ReasonForcedFinal,
		roundguard.ReasonStalled,
		roundguard.ReasonCompleted,
		roundguard.ReasonHandedOff,
		roundguard.ReasonToolCalls,
		roundguard.ReasonPaused,
	}
	for _, r := range graceful {
		if r.IsFailure() {
			t.Fatalf("%q 不该判为失败型", r)
		}
	}
}

// TestReasonTableOutletIsSortedAndComplete 钉住：枚举出口与词表逐字一致。
func TestReasonTableOutletIsSortedAndComplete(t *testing.T) {
	got := roundguard.AllRoundReasons()
	if len(got) != 10 {
		t.Fatalf("回合结局词表应有 10 值，实得 %d（%v）", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("枚举出口应排序，实得 %v", got)
		}
	}
	for _, s := range got {
		if !roundguard.Reason(s).Valid() {
			t.Fatalf("枚举出口给出的 %q 不在词表内", s)
		}
	}
}

// TestReasonZeroValueIsNotAMember 钉住零值语义：空串不是词表成员（未设 ≠ 合法结局）。
func TestReasonZeroValueIsNotAMember(t *testing.T) {
	var r roundguard.Reason
	if r.Valid() {
		t.Fatal("零值不该被判为词表成员")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `make test`（只看 `roundguard` 包）
Expected: FAIL —— `roundguard` 包不存在 / 符号未定义。

- [ ] **Step 3: 写实现**

`roundguard/roundguard.go`：

```go
// Package roundguard 回合守卫：模型侧循环的**回合结局分类**、**次数预算**与
// **无进展（循环）检测**——三条跨宿主共享的判据。
//
// 生命周期: Experimental（v0.52.0 定级）——本包仍会改、删、拆，不承诺任何
// 兼容；升级风险自负；定级理由见 api-lifecycle.yaml，分级规则见
// docs/api-compat.md。
//
// # 它补的是哪一段
//
// 主流模型侧 loop 的**循环本体**已经收敛成同一段代码（模型请求工具就执行、
// 回灌结果、继续；模型给终答就停）——差异全在**循环周边**：这次还能不能转、
// 这一回合以什么结束、这一圈是不是白转。这三问与「谁来跑模型」无关，与「跑
// 多少圈」有关；放在宿主侧的后果是**每个宿主各判一套**：同一段循环在 A 宿主算
// 「第 12 圈超了」、在 B 宿主算「第 8 圈还能转」，且没有任何机器能发现。
//
// 本包把这三问收成**机制面**：纯函数 + 进程内计数器，零领域词，**内核不调模型**。
//
// # 与既有机制划界（同词不同域，互指不重复）
//
//   - vs `budget`：budget 管**输入侧 token 估算**（「下一轮要发多少」），
//     本包管**次数**。量纲不同，不合并。
//   - vs `cost`：cost 是**已发生消费的记账**，本包是**约束**——同 budget/cost
//     的既有切分：记与判分属两包。
//   - vs `deadline`：deadline 是**墙钟**，本包是**次数**。
//   - vs `watchdog`：watchdog 判**工具调用配对**与**编排轮次**括号配对；
//     本包的单位是**模型回合**（一次模型往返），与它不互相替代。
//   - vs `finalize` / `contract`：finalize 是**会话级** Cause→终态路由；
//     本包是**回合级**结局分类，不产出 ctx Cause、不碰会话状态词表。
//   - vs `retryplan` / `toolfail`：本包**不重判**「该不该重试」，只在「额度用尽」
//     「无进展」这两种**续跑必然再撞**的情形下判不得续跑。
//
// # 三分命名
//
//   - **轮次**（turn）：一次**编排会话运行**——`eventbus.EventTurnStarted/Ended`。
//   - **环节**（step）：宿主定义的**业务步骤**——`statemachine.StepStatus`。
//   - **回合**（round）：模型侧**一次往返**（一次模型调用 + 其工具执行）——本包。
//
// # 单元词表
//
// 回合结局（Reason，10 值）、额度类别（Limit，5 值）、无进展形态（Shape，6 值）、
// 处置口径（StopPolicy，2 值）四张都是**判定词表**：取值决定结论，别处出现必须
// 引用本包的常量，不得手抄（登记面见 architecture-policy.yaml 的 word_tables 段）。
package roundguard

// Reason 回合结局（判定词表；主人表 roundReasonAll，枚举出口 AllRoundReasons）。
//
// 零值是**空串**——不在词表内（Valid 判假），即「未设 / 读不懂」，**不冒充任何
// 结局**。与 statemachine.StepStatus 的零值处理同款。
type Reason string

const (
	// ReasonCompleted 模型给出终答，本回合不再要工具。
	ReasonCompleted Reason = "completed"
	// ReasonToolCalls 模型请求工具 ⇒ 允许进入下一回合。
	ReasonToolCalls Reason = "tool_calls"
	// ReasonHandedOff 模型请求交接给别的执行方 ⇒ 收敛并移交。
	ReasonHandedOff Reason = "handed_off"
	// ReasonPaused 服务端要求原样再问（pause_turn）⇒ 允许进入下一回合。
	ReasonPaused Reason = "paused"
	// ReasonFailed 模型调用或工具执行明确报错 ⇒ 失败型收敛。
	ReasonFailed Reason = "failed"
	// ReasonNoOutcome 本回合无任何产出 ⇒ 失败型收敛（保守侧）。
	ReasonNoOutcome Reason = "no_outcome"
	// ReasonAborted 被中止（用户取消 / 停机 / 接管）⇒ 失败型收敛。
	ReasonAborted Reason = "aborted"
	// ReasonBudgetExhausted 额度用尽且按**失败**口径处置 ⇒ 失败型收敛。
	ReasonBudgetExhausted Reason = "budget_exhausted"
	// ReasonForcedFinal 额度用尽但按**强制收尾**口径处置 ⇒ 产物仍可用。
	ReasonForcedFinal Reason = "forced_final"
	// ReasonStalled 无进展 ⇒ 温和叫停（不是失败）。
	ReasonStalled Reason = "stalled"
)

// String 机器可读短码（展示文案归宿主）。
func (r Reason) String() string { return string(r) }

// Valid 报告该取值是否在回合结局词表内。
func (r Reason) Valid() bool { return roundReasonAll[string(r)] }

// IsFailure 报告该结局是否为**失败型**收敛。
//
// 为什么要单独一个判据而不是让宿主自己列清单：「被温和收停」与「真的坏了」在
// 宿主侧必须可区分——前者仍有可用产出（模型已给出终答、或证据已齐备只差收束），
// 后者只有半截 transcript。两类混在一张「都收敛了」的清单里，宿主就会把「被上限
// 温和收停」也当故障上报（假红）。
//
// 与上游模型侧实现的同构：agentloop 的 AnswerStatus 把 StatusStopped（hook 跳过
// / 重复检测命中）与 StatusFailed（传输/hook/ctx 错误）分成两个值。
func (r Reason) IsFailure() bool {
	switch r {
	case ReasonFailed, ReasonNoOutcome, ReasonAborted, ReasonBudgetExhausted:
		return true
	}
	return false
}
```

`roundguard/word.go`：

```go
package roundguard

import "slices"

// roundReasonAll 回合结局词表 → 成员（单一事实源）。
//
// 判定（Reason.Valid）与枚举出口（AllRoundReasons）都读这一张表，新增取值
// 只需改一处。
var roundReasonAll = map[string]bool{
	ReasonCompleted:       true,
	ReasonToolCalls:       true,
	ReasonHandedOff:       true,
	ReasonPaused:          true,
	ReasonFailed:          true,
	ReasonNoOutcome:       true,
	ReasonAborted:         true,
	ReasonBudgetExhausted: true,
	ReasonForcedFinal:     true,
	ReasonStalled:         true,
}

// AllRoundReasons 返回回合结局词表的全集（排序后的**新切片**）。
//
// 宿主据此渲染结局选择器 / 校验落库值，不必手抄常量清单——手抄清单必然漂移：
// 内核新增一个取值，宿主那份不会跟着变，且没有任何判据会红（issue #356）。
// 返回的是副本：调用方改写它不影响判定面。
func AllRoundReasons() []string {
	out := make([]string, 0, len(roundReasonAll))
	for s := range roundReasonAll {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}
```

`roundguard/classify.go`：

```go
package roundguard

// Outcome 一回合结束后的机械事实（宿主注入；内核不调模型、不认识工具语义）。
type Outcome struct {
	// ToolCalls 本回合请求的工具调用次数（>0 ⇒ 模型还要工具）。
	ToolCalls int
	// Completed 模型给出了终答。
	Completed bool
	// Handoff 模型请求交接。
	Handoff bool
	// Paused 服务端要求原样再问（pause_turn）。
	Paused bool
	// Failed 模型调用或工具执行的错误（nil = 无）。
	Failed error
	// Aborted ctx 被取消。
	Aborted bool
	// ForcedFinal 本回合是「额度耗尽后的强制收尾步」：宿主已禁用工具，要求模型
	// 用已收集的证据给出终答。它是**产出仍然可用**的收束，不是失败。
	ForcedFinal bool
}

// Route 结局分类结果。
type Route struct {
	// Reason 回合结局分类。
	Reason Reason
	// Continue 允许进入下一回合。
	Continue bool
}

// Classify 回合结局分类（纯函数）。
//
// 判定顺序（**先具体后笼统**，取证据最硬的那条）：
//
//	1. o.Aborted       → ReasonAborted
//	2. o.Failed != nil → ReasonFailed
//	3. o.Handoff       → ReasonHandedOff
//	4. o.ToolCalls > 0 → ReasonToolCalls（要工具优先于终答）
//	5. o.ForcedFinal   → ReasonForcedFinal（强制收尾优先于普通终答）
//	6. o.Completed     → ReasonCompleted
//	7. o.Paused        → ReasonPaused
//	8. 都不成立         → ReasonNoOutcome（零值形态，保守收敛）
//
// 为什么「要工具」排在「给终答」前面：模型可以同时给文本与工具调用；此时「还要
// 工具」才是决定下一步的事实（文本是给用户看的，工具调用才是控制流）。把终答排
// 前面会让「模型一边收尾一边补一个工具调用」被误判成已完成——而工具**还没执行**，
// 判完成就是谎报已完成。
func Classify(o Outcome) Route {
	switch {
	case o.Aborted:
		return Route{Reason: ReasonAborted}
	case o.Failed != nil:
		return Route{Reason: ReasonFailed}
	case o.Handoff:
		return Route{Reason: ReasonHandedOff}
	case o.ToolCalls > 0:
		return Route{Reason: ReasonToolCalls, Continue: true}
	case o.ForcedFinal:
		return Route{Reason: ReasonForcedFinal}
	case o.Completed:
		return Route{Reason: ReasonCompleted}
	case o.Paused:
		return Route{Reason: ReasonPaused, Continue: true}
	default:
		return Route{Reason: ReasonNoOutcome}
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `make test`
Expected: PASS（`roundguard` 包 6 个用例全绿）。

- [ ] **Step 5: 登记面（policy + 生命周期）**

改 `architecture-policy.yaml`：

（a）`modules` 段**追加**（不要插到中间，段内顺序不影响判定）：

```yaml
  - id: roundguard
    roots: [roundguard]
    requires: ["ext:github.com/oekit/pkg"] # 零内部依赖：哨兵/错误用 pkg/errors；budget/cost/deadline/watchdog 是平级的不同量纲，互相不引
    owner: harness
    managed: true
    notes: |
      回合守卫：模型侧循环的回合结局分类 + 次数预算 + 无进展检测。
      零内部依赖是刻意的——budget/cost/deadline/watchdog 是**平级的不同量纲**，
      互相不引（引了就成环，且两处口径会漂）。回合结局是**第三根轴**，
      不复用 contract 会话状态 / statemachine 环节状态。
```

（b）`thresholds.scan_roots` 列表**末尾**追加 `roundguard`。

（c）`word_tables.tables` **追加**：

```yaml
    - pkg: roundguard
      backing: roundReasonAll
      enum: AllRoundReasons
      predicate: Reason.Valid
      family: Reason
      desc: 回合结局词表（10 值）
```

（d）`word_tables.relations` **追加**四条（本包的 `failed` 与三张既有表同形异义；
`none` 在 Limit 与 Shape 两张表里同形）：

```yaml
    # 会话失败 ≠ 回合失败：一个是会话终态，一个是某一回合的结局。
    - a: {pkg: contract, enum: AllStatuses}
      b: {pkg: roundguard, enum: AllRoundReasons}
      relation: distinct
      anchors:
        a: [{all: true}]
        b: [{all: true}]
    # 同上（statemachine 的环节状态与 contract 同族）。
    - a: {pkg: statemachine, enum: AllStepStatuses}
      b: {pkg: roundguard, enum: AllRoundReasons}
      relation: distinct
      anchors:
        a: [{all: true}]
        b: [{all: true}]
    # 任务失败 ≠ 回合失败（taskboard 的 failed 允许重新认领，非终态）。
    - a: {pkg: taskboard, enum: AllStates}
      b: {pkg: roundguard, enum: AllRoundReasons}
      relation: distinct
      anchors:
        a: [{all: true}]
        b: [{all: true}]
```

（e）`api-lifecycle.yaml` **追加**：

```yaml
  - pkg: roundguard
    level: Experimental
    since: v0.52.0
    reason: 回合守卫三件套刚下沉：结局分类的取值集合与「不得续跑」的判据边界仍待第二个宿主验证（比照 steer 的定级理由）。
```

- [ ] **Step 6: README 两处一起翻**

（a）包表**追加**一行（行数按实际生产行数填，docsync 给 25% 容差）：

```
| `roundguard` | <生产行数> | oekit/pkg | 回合守卫：模型侧循环的**回合结局分类** + **次数预算** + **无进展（循环）检测**——三条跨宿主共享的判据，纯函数 + 进程内计数器，零领域词，内核不调模型。额度维度（回合数 / 工具调用总数 / 单工具）与处置口径（强制收尾 vs 失败即止）**分开**：前者是事实，后者是可配策略（五家主流实现三种口径，spec §1.4 有实测对照）。结局词表区分**失败型收敛**与**温和叫停**（`Reason.IsFailure()`）——「被上限温和收停」与「真的坏了」在宿主侧必须可分。无进展检测覆盖五形态（精确重复 / 震荡 / 缓慢漂移 / 错误重试螺旋 / 只规划不动手），处置分档（警告回灌 → 再犯才停），带**通用豁免面**（轮询类动作、刻意的规划步） |
```

（b）**工程底座表**（README 的「工程底座」节）三项按实测刷新：

```
| 生产代码 | <行数> 行 / <文件数> 文件（不含 `_test.go`）|
| 测试 | <测试文件数> 个测试文件 |
```

重算命令（docsync 报错时也会原样给出）：

```bash
find . -name '*.go' -not -name '*_test.go' -not -path './.*' -not -path './vendor/*' -print0 | xargs -0 cat | wc -l
find . -name '*.go' -not -name '*_test.go' -not -path './.*' -not -path './vendor/*' | wc -l
find . -name '*_test.go' -not -path './.*' -not -path './vendor/*' | wc -l
```

- [ ] **Step 7: 跑三个门禁**

Run: `make architecture-check && make docsync && make apilifecheck`
Expected: 三个都 rc=0。**若 relations 报出本计划没预料到的撞车对**（例如某张既有表也有
`failed`），按门禁打印的**确切配对**补 `relations` 条目——不要凭猜加。

- [ ] **Step 8: 提交**

```bash
git add roundguard/roundguard.go roundguard/word.go roundguard/classify.go \
        roundguard/classify_test.go architecture-policy.yaml api-lifecycle.yaml README.md
git commit -m "feat(roundguard): 回合结局分类（词表 + Classify）+ 三处登记面"
```

---

### Task 2: 回合次数预算（`Budget` / `Used` / `Meter` / 三个 `Check*`）

**Files:**
- Create: `roundguard/budget.go`
- Create: `roundguard/meter.go`
- Create: `roundguard/errors.go`
- Modify: `roundguard/word.go`（增两张表的 backing 表 + 枚举出口 + 谓词）
- Test: `roundguard/budget_test.go`
- Modify: `architecture-policy.yaml`（增两张词表登记）
- Modify: `docs/error-semantics.md`（增 `### roundguard` 小节）

**Interfaces:**
- Consumes: Task 1 的 `roundguard.Reason`（本任务只用 `ReasonBudgetExhausted` 与
  `ReasonForcedFinal` 作注释引用，不产生依赖）。
- Produces:
  - `type StopPolicy string`，常量 `PolicyForceFinal` / `PolicyFailFast`；`func (p StopPolicy) Valid() bool`；`func AllStopPolicies() []string`
  - `type Limit string`，常量 `LimitNone` / `LimitRounds` / `LimitToolCalls` / `LimitPerTool` / `LimitStalled`；`func (l Limit) Valid() bool`；`func AllLimits() []string`
  - `type Budget struct { MaxRounds int; MaxToolCalls int; MaxPerTool map[string]int; Policy StopPolicy }`
  - `func (b Budget) Validate() error`
  - `type Used struct { Rounds int; ToolCalls int; Tools map[string]int }`
  - `type Verdict struct { Allowed bool; Limit Limit; Detail string }`
  - `func CheckRounds(b Budget, used Used) Verdict`、`func CheckToolCalls(b Budget, used Used) Verdict`、`func CheckTool(b Budget, used Used, toolID string) Verdict`
  - `type Meter struct{}`，`func NewMeter() *Meter`、`func (m *Meter) Round() int`、`func (m *Meter) Tool(toolID string) int`、`func (m *Meter) Used() Used`
  - `var ErrInvalidBudget error`

- [ ] **Step 1: 写失败测试**

```go
package roundguard_test

import (
	"errors"
	"testing"

	"github.com/yi-nology/goagent-kernel/roundguard"
)

// TestCheckRoundsBoundary 钉住边界：已用 2、上限 3 ⇒ 放行；已用 3 ⇒ 拒绝。
// 这条是「把 >= 写成 > 会被抓住」的那道反向验证。
func TestCheckRoundsBoundary(t *testing.T) {
	b := roundguard.Budget{MaxRounds: 3}
	if v := roundguard.CheckRounds(b, roundguard.Used{Rounds: 2}); !v.Allowed {
		t.Fatalf("已用 2 / 上限 3 应放行，实得 %+v", v)
	}
	v := roundguard.CheckRounds(b, roundguard.Used{Rounds: 3})
	if v.Allowed {
		t.Fatal("已用 3 / 上限 3 应拒绝")
	}
	if v.Limit != roundguard.LimitRounds {
		t.Fatalf("应命中 %q，实得 %q", roundguard.LimitRounds, v.Limit)
	}
}

// TestCheckRoundsZeroMeansUnlimited 钉住：零值 = 显式关闭该额度（合法）。
func TestCheckRoundsZeroMeansUnlimited(t *testing.T) {
	if v := roundguard.CheckRounds(roundguard.Budget{}, roundguard.Used{Rounds: 999}); !v.Allowed {
		t.Fatal("上限为 0 应表示不限")
	}
}

// TestCheckToolCallsBoundary 钉住工具总额度的边界。
func TestCheckToolCallsBoundary(t *testing.T) {
	b := roundguard.Budget{MaxToolCalls: 2}
	if v := roundguard.CheckToolCalls(b, roundguard.Used{ToolCalls: 1}); !v.Allowed {
		t.Fatal("已用 1 / 上限 2 应放行")
	}
	v := roundguard.CheckToolCalls(b, roundguard.Used{ToolCalls: 2})
	if v.Allowed {
		t.Fatal("已用 2 / 上限 2 应拒绝")
	}
	if v.Limit != roundguard.LimitToolCalls {
		t.Fatalf("应命中 %q，实得 %q", roundguard.LimitToolCalls, v.Limit)
	}
}

// TestCheckToolIsPerToolScoped 钉住：单工具上限只作用于该工具，且 Detail 带工具标识。
func TestCheckToolIsPerToolScoped(t *testing.T) {
	b := roundguard.Budget{MaxPerTool: map[string]int{"read": 2}}
	if v := roundguard.CheckTool(b, roundguard.Used{Tools: map[string]int{"read": 2}}, "read"); v.Allowed {
		t.Fatal("read 已用满应拒绝")
	} else if v.Limit != roundguard.LimitPerTool || v.Detail != "read" {
		t.Fatalf("应命中 %q 且带工具标识，实得 %+v", roundguard.LimitPerTool, v)
	}
	if v := roundguard.CheckTool(b, roundguard.Used{Tools: map[string]int{"read": 2}}, "write"); !v.Allowed {
		t.Fatal("别的工具不该受 read 的上限影响")
	}
}

// TestValidateRejectsNegative 钉住：负值上限非法（不是「不限」）——把写错的负值默默
// 当成「不限」跑一整轮是 fail-open。
func TestValidateRejectsNegative(t *testing.T) {
	for _, b := range []roundguard.Budget{
		{MaxRounds: -1},
		{MaxToolCalls: -1},
		{MaxPerTool: map[string]int{"read": -1}},
	} {
		if err := b.Validate(); err == nil {
			t.Fatalf("负值上限应报错：%+v", b)
		}
	}
}

// TestValidateRejectsEmptyToolKey 钉住：空工具标识无法计数 ⇒ 非法声明。
func TestValidateRejectsEmptyToolKey(t *testing.T) {
	b := roundguard.Budget{MaxPerTool: map[string]int{"": 1}}
	if err := b.Validate(); err == nil {
		t.Fatal("空工具标识应报错")
	}
}

// TestValidateRejectsUnknownPolicy 钉住未知处置口径。
func TestValidateRejectsUnknownPolicy(t *testing.T) {
	if err := (roundguard.Budget{Policy: "nonsense"}).Validate(); err == nil {
		t.Fatal("未知处置口径应报错")
	}
}

// TestValidateAcceptsZeroPolicy 钉住：处置口径零值合法（缺省 = 强制收尾）。
func TestValidateAcceptsZeroPolicy(t *testing.T) {
	if err := (roundguard.Budget{}).Validate(); err != nil {
		t.Fatalf("零值处置口径应合法：%v", err)
	}
}

// TestMeterCountsRoundsAndTools 钉住计量。
func TestMeterCountsRoundsAndTools(t *testing.T) {
	m := roundguard.NewMeter()
	if got := m.Round(); got != 1 {
		t.Fatalf("第一次记回合应得 1，实得 %d", got)
	}
	m.Tool("read")
	if got := m.Tool("read"); got != 2 {
		t.Fatalf("read 累计应得 2，实得 %d", got)
	}
	u := m.Used()
	if u.Rounds != 1 || u.ToolCalls != 2 || u.Tools["read"] != 2 {
		t.Fatalf("用量快照不对：%+v", u)
	}
	u.Tools["read"] = 99
	if m.Used().Tools["read"] != 2 {
		t.Fatal("用量快照必须是副本")
	}
}

// TestErrInvalidBudgetIsIdentifiable 钉住哨兵可判定。
func TestErrInvalidBudgetIsIdentifiable(t *testing.T) {
	err := roundguard.Budget{MaxRounds: -1}.Validate()
	if !errors.Is(err, roundguard.ErrInvalidBudget) {
		t.Fatalf("应能 errors.Is 判定 %v", roundguard.ErrInvalidBudget)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `make test`
Expected: FAIL —— 符号未定义。

- [ ] **Step 3: 写实现**

`roundguard/errors.go`：

```go
package roundguard

import "github.com/oekit/pkg/errors"

// ErrInvalidBudget 预算声明非法（负值上限 / 空工具标识 / 未知处置口径）。
//
// 处置：**修声明**（调用方配置错，重试无意义）。
var ErrInvalidBudget = errors.New("回合预算声明非法")
```

`roundguard/budget.go`（全文）：

```go
package roundguard

import "github.com/oekit/pkg/errors"

// StopPolicy 额度用尽后的处置口径（判定词表；主人表 stopPolicyAll）。
//
// 五家主流实现三种口径（spec §1.4 有实测对照）：pydantic-ai 与 Claude 是硬失败；
// agentloop 把工具预算做成强制收尾；OpenAI 装了 max-turn handler 就产出合成输出；
// smolagents 触顶后单独再调一次模型产出终答。⇒ 处置是**策略**，不是事实。
type StopPolicy string

const (
	// PolicyForceFinal 强制收尾：不再给工具（模型面），要求模型用已收集的证据
	// 给出终答；产物仍然可用（不是失败）。**缺省**。
	PolicyForceFinal StopPolicy = "force_final"
	// PolicyFailFast 直接失败：本次运行以失败收场。
	PolicyFailFast StopPolicy = "fail_fast"
)

// Valid 报告该处置口径是否在词表内。
func (p StopPolicy) Valid() bool { return stopPolicyAll[string(p)] }

// Limit 额度类别（判定词表；主人表 roundLimitAll，枚举出口 AllLimits）。
type Limit string

const (
	// LimitNone 未因额度叫停。
	LimitNone Limit = "none"
	// LimitRounds 回合数额度用尽（处置按 Budget.Policy）。
	LimitRounds Limit = "rounds"
	// LimitToolCalls 工具调用总额度用尽（处置按 Budget.Policy）。
	LimitToolCalls Limit = "tool_calls"
	// LimitPerTool 单工具额度用尽 ⇒ 该次调用被拒。
	LimitPerTool Limit = "per_tool"
	// LimitStalled 无进展（由 Detect 产出，见 detect.go）。
	LimitStalled Limit = "stalled"
)

// Valid 报告该额度类别是否在词表内。
func (l Limit) Valid() bool { return roundLimitAll[string(l)] }

// Budget 回合预算（宿主给；内核不解释工具标识的含义）。
//
// **额度维度（事实）与处置口径（策略）分开**——前者各家一致，后者没有共识
// （详见 spec §6.1.1 与 §1.4 的五家实测对照）。
type Budget struct {
	// MaxRounds 回合数上限（0 = 不限）。
	MaxRounds int
	// MaxToolCalls 工具调用总数上限（0 = 不限）。
	MaxToolCalls int
	// MaxPerTool 单工具调用上限（开放键；值为 0 = 该工具不限）。
	// 工具标识是宿主词表——内核只做键存在性与计数判定，不解释它是哪个工具。
	MaxPerTool map[string]int
	// Policy 额度用尽后的处置口径（零值 = PolicyForceFinal）。
	Policy StopPolicy
}

// Validate 校验预算声明（装配期用）。非法值一律报错，不静默降级为「不限」。
//
// 零值 = **显式关闭**对应额度（合法的「不限」）；**负值一律非法**——把一个写错的
// 负值默默当成「不限」跑一整轮，是典型的 fail-open；宁可装配期报错。
// 另：MaxPerTool 的键不得为空串（空标识无法计数，等于声明了一个无法判的上限）。
func (b Budget) Validate() error {
	if b.MaxRounds < 0 {
		return errors.Wrap(ErrInvalidBudget, "roundguard: 回合数上限不能为负（负值不是「不限」）")
	}
	if b.MaxToolCalls < 0 {
		return errors.Wrap(ErrInvalidBudget, "roundguard: 工具调用上限不能为负（负值不是「不限」）")
	}
	for id, n := range b.MaxPerTool {
		if id == "" {
			return errors.Wrap(ErrInvalidBudget, "roundguard: 单工具上限的键不得为空串（空标识无法计数）")
		}
		if n < 0 {
			return errors.Wrap(ErrInvalidBudget, "roundguard: 单工具上限不能为负："+id)
		}
	}
	if b.Policy != "" && !b.Policy.Valid() {
		return errors.Wrap(ErrInvalidBudget, "roundguard: 未知处置口径："+string(b.Policy))
	}
	return nil
}

// effectivePolicy 缺省处置口径（零值 = 强制收尾）。
func (b Budget) effectivePolicy() StopPolicy {
	if b.Policy == "" {
		return PolicyForceFinal
	}
	return b.Policy
}

// Used 当前用量（回合数 + 工具调用总数 + 按工具的调用次数）。
type Used struct {
	// Rounds 回合数（**不是** provider 请求数——fallback/重试/续跑请求可能更多）。
	Rounds int
	// ToolCalls 工具调用总数（MaxToolCalls 额度用）。
	ToolCalls int
	// Tools 按工具的调用次数（MaxPerTool 额度用）。
	Tools map[string]int
}

// Verdict 准入判定结论。
type Verdict struct {
	// Allowed 是否放行。
	Allowed bool
	// Limit 命中的额度类别（Allowed=true 时恒为 LimitNone）。
	Limit Limit
	// Detail 机械留痕（命中的工具标识等）；**展示文案归宿主**。
	Detail string
}

// CheckRounds 判定下一回合能否开始（**模型调用前**检查；回合数额度）。
//
// 调用后检查意味着**最后一次昂贵调用已经发生** ⇒ 额度判定一律在调用前。
func CheckRounds(b Budget, used Used) Verdict {
	if b.MaxRounds <= 0 || used.Rounds < b.MaxRounds {
		return Verdict{Allowed: true, Limit: LimitNone}
	}
	return Verdict{Allowed: false, Limit: LimitRounds}
}

// CheckToolCalls 判定本回合还能不能再调工具（**模型调用前**检查；工具调用总额度）。
func CheckToolCalls(b Budget, used Used) Verdict {
	if b.MaxToolCalls <= 0 || used.ToolCalls < b.MaxToolCalls {
		return Verdict{Allowed: true, Limit: LimitNone}
	}
	return Verdict{Allowed: false, Limit: LimitToolCalls}
}

// CheckTool 判定某工具的下一次调用能否进行（**工具执行前**检查；单工具额度）。
//
// 被拒时宿主仍须回灌一条失败结果给模型——否则 tool_call 没有配对的 tool_result，
// 下一轮请求会被 provider 拒（配对纪律见 watchdog INV1）。
func CheckTool(b Budget, used Used, toolID string) Verdict {
	limit, ok := b.MaxPerTool[toolID]
	if !ok || limit <= 0 || used.Tools[toolID] < limit {
		return Verdict{Allowed: true, Limit: LimitNone}
	}
	return Verdict{Allowed: false, Limit: LimitPerTool, Detail: toolID}
}
```

`roundguard/word.go` **追加**：

```go
// stopPolicyAll 处置口径词表 → 成员（单一事实源）。
var stopPolicyAll = map[string]bool{
	PolicyForceFinal: true,
	PolicyFailFast:   true,
}

// AllStopPolicies 返回处置口径词表的全集（排序后的新切片）。
func AllStopPolicies() []string {
	out := make([]string, 0, len(stopPolicyAll))
	for s := range stopPolicyAll {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// roundLimitAll 额度类别词表 → 成员（单一事实源）。
var roundLimitAll = map[string]bool{
	LimitNone:      true,
	LimitRounds:    true,
	LimitToolCalls: true,
	LimitPerTool:   true,
	LimitStalled:   true,
}

// AllLimits 返回额度类别词表的全集（排序后的新切片）。
func AllLimits() []string {
	out := make([]string, 0, len(roundLimitAll))
	for s := range roundLimitAll {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}
```

`roundguard/meter.go`（全文）：

```go
package roundguard

import "sync"

// Meter 进程内回合账（只数不判）。
//
// **进程内**：跨进程续跑时回合数**不随之累计**——跨进程累计需要存储形态，那是宿主
// 资产；要跨进程累计的宿主须自行落库并把 Used 灌回本包。
//
// **并发安全**：带锁。判据包不该把加锁方式留给调用方各自发明——宿主各写一遍加锁
// 方式 = 同一种并发缺陷的 N 个变体。
type Meter struct {
	mu      sync.Mutex
	rounds  int
	calls   int
	perTool map[string]int
}

// NewMeter 构造一个空的回合账。
func NewMeter() *Meter {
	return &Meter{perTool: make(map[string]int)}
}

// Round 记一回合并返回累计回合数。
func (m *Meter) Round() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rounds++
	return m.rounds
}

// Tool 记一次工具调用并返回该工具的累计调用数。
func (m *Meter) Tool(toolID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.perTool[toolID]++
	return m.perTool[toolID]
}

// Used 导出用量快照（副本——调用方改写它不影响记账）。
func (m *Meter) Used() Used {
	m.mu.Lock()
	defer m.mu.Unlock()
	tools := make(map[string]int, len(m.perTool))
	for k, v := range m.perTool {
		tools[k] = v
	}
	return Used{Rounds: m.rounds, ToolCalls: m.calls, Tools: tools}
}
```

- [ ] **Step 4: 登记面（两张词表 + 错误语义表）**

（a）`architecture-policy.yaml` 的 `word_tables.tables` **追加**：

```yaml
    - pkg: roundguard
      backing: roundLimitAll
      enum: AllLimits
      predicate: Limit.Valid
      family: Limit
      desc: 额度类别词表（5 值）
    - pkg: roundguard
      backing: stopPolicyAll
      enum: AllStopPolicies
      predicate: StopPolicy.Valid
      family: Policy
      desc: 额度用尽后的处置口径词表（2 值）
```

（b）`docs/error-semantics.md` **追加**小节（小节名 = `api-lifecycle.yaml` 的 `pkg` 名，
逐字一致）：

```markdown
### roundguard

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrInvalidBudget` | 装配期 `Budget.Validate` 判负值上限 / 空工具标识 / 未知处置口径 | 修声明（调用方配置错，重试无意义） |
```

- [ ] **Step 5: 跑测试与门禁**

Run: `make test && make architecture-check && make apilifecheck`
Expected: 全 rc=0。

- [ ] **Step 6: 刷新 README 工程底座表并提交**

（工程底座表三项按 Task 1 Step 6(b) 的命令重算；包表的行数按新值调整。）

```bash
git add roundguard/budget.go roundguard/meter.go roundguard/errors.go roundguard/word.go \
        roundguard/budget_test.go architecture-policy.yaml docs/error-semantics.md README.md
git commit -m "feat(roundguard): 回合次数预算（额度维度 + 处置口径 + 进程内回合账）"
```

---

### Task 3: 无进展（循环）检测（五形态 + 定档 + 豁免面）

**Files:**
- Create: `roundguard/detect.go`
- Modify: `roundguard/word.go`（增 Shape 表）
- Test: `roundguard/detect_test.go`
- Modify: `architecture-policy.yaml`（增 Shape 表 + Limit×Shape 的 distinct 关系）
- Modify: `docs/superpowers/specs/2026-11-21-roundguard-agent-loop-design.md`（窗口下限修正，见 Step 4）

**Interfaces:**
- Consumes: Task 1 的 `Reason`（本任务产出 `Shape` 与 `Finding`，不消费 `Reason`）。
- Produces:
  - `type Observation struct { Fingerprint, Normalized string; Tools int; Failed, Settled, ResultChanged, Exempt bool }`
  - `type Shape string`，常量 `ShapeNone` / `ShapeRepeat` / `ShapeOscillation` / `ShapeDrift` / `ShapeErrorSpiral` / `ShapeIdle`；`func (s Shape) Valid() bool`；`func AllShapes() []string`
  - `type Finding struct { Stalled, Warn bool; Shape Shape; Since int }`
  - `type Config struct { Window, Repeat int }`；常量 `DefaultWindow` / `DefaultRepeat`
  - `func Detect(obs []Observation, cfg Config) Finding`

- [ ] **Step 1: 写失败测试**

```go
package roundguard_test

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/roundguard"
)

// 合成数据：指纹用中性字母串，不带任何领域味。

func obs(fingerprint string, o roundguard.Observation) roundguard.Observation {
	o.Fingerprint = fingerprint
	return o
}

// TestDetectRepeat 钉住精确重复：同一指纹连着来。
func TestDetectRepeat(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape != roundguard.ShapeRepeat {
		t.Fatalf("应判 %q，实得 %q", roundguard.ShapeRepeat, f.Shape)
	}
	if !f.Warn || f.Stalled {
		t.Fatalf("刚凑够阈值应为首档（Warn）：%+v", f)
	}
}

// TestDetectRepeatEscalates 钉住定档：再犯一次 ⇒ 停止档。
func TestDetectRepeatEscalates(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if !f.Stalled || f.Warn {
		t.Fatalf("再犯一次应判停止档：%+v", f)
	}
}

// TestDetectResultChangedBreaksStreak 钉住：相同动作但**结果变了**是进展，
// 不计入重复。
func TestDetectResultChangedBreaksStreak(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{ResultChanged: true}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape == roundguard.ShapeRepeat {
		t.Fatalf("结果变了不该判重复：%+v", f)
	}
}

// TestDetectOscillation 钉住震荡：A B A B …
func TestDetectOscillation(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{}),
		obs("b", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
		obs("b", roundguard.Observation{}),
		obs("a", roundguard.Observation{}),
		obs("b", roundguard.Observation{}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape != roundguard.ShapeOscillation {
		t.Fatalf("应判 %q，实得 %q", roundguard.ShapeOscillation, f.Shape)
	}
}

// TestDetectDrift 钉住缓慢漂移：归一后同、原指纹不同。
func TestDetectDrift(t *testing.T) {
	seq := []roundguard.Observation{
		{Fingerprint: "page=1", Normalized: "page=N"},
		{Fingerprint: "page=2", Normalized: "page=N"},
		{Fingerprint: "page=3", Normalized: "page=N"},
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape != roundguard.ShapeDrift {
		t.Fatalf("应判 %q，实得 %q", roundguard.ShapeDrift, f.Shape)
	}
}

// TestDetectErrorSpiralBeatsRepeat 钉住判定次序：错误螺旋优先于重复（病因更具体）。
func TestDetectErrorSpiralBeatsRepeat(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{Failed: true}),
		obs("a", roundguard.Observation{Failed: true}),
		obs("a", roundguard.Observation{Failed: true}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape != roundguard.ShapeErrorSpiral {
		t.Fatalf("应判 %q，实得 %q", roundguard.ShapeErrorSpiral, f.Shape)
	}
}

// TestDetectIdle 钉住只规划不动手。
func TestDetectIdle(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{}),
		obs("b", roundguard.Observation{}),
		obs("c", roundguard.Observation{}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape != roundguard.ShapeIdle {
		t.Fatalf("应判 %q，实得 %q", roundguard.ShapeIdle, f.Shape)
	}
}

// TestDetectIdleNotTriggeredBySettled 钉住：已收敛的回合不算空转。
func TestDetectIdleNotTriggeredBySettled(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{Settled: true}),
		obs("b", roundguard.Observation{Settled: true}),
		obs("c", roundguard.Observation{Settled: true}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape == roundguard.ShapeIdle {
		t.Fatalf("已收敛不该判空转：%+v", f)
	}
}

// TestDetectExemptIsSkipped 钉住豁免面：Exempt 的回合不参与计数
// （轮询类动作 / 刻意的规划步——smolagents 的 planning_interval 就是后者）。
func TestDetectExemptIsSkipped(t *testing.T) {
	seq := []roundguard.Observation{
		obs("a", roundguard.Observation{}),
		obs("a", roundguard.Observation{Exempt: true}),
		obs("a", roundguard.Observation{}),
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape == roundguard.ShapeRepeat {
		t.Fatalf("豁免回合不该计入重复：%+v", f)
	}
}

// TestDetectEmptyIsNone 钉住：没有可判的回合 ⇒ 不判任何形态。
func TestDetectEmptyIsNone(t *testing.T) {
	f := roundguard.Detect(nil, roundguard.Config{})
	if f.Shape != roundguard.ShapeNone || f.Warn || f.Stalled {
		t.Fatalf("空输入应判无形态：%+v", f)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `make test`
Expected: FAIL —— 符号未定义。

- [ ] **Step 3: 写实现**

`roundguard/detect.go`（全文）：

```go
package roundguard

// Observation 一回合的可机判事实（宿主注入）。
//
// ⚠️ **指纹与归一指纹都由宿主算**：内核若自己决定「什么是易变部分」（数字？
// 时间戳？行号？），就成了领域解释者——那是宿主的事。内核只对**宿主算好的两组
// 字符串**做集合判定（与 budget.Item 只携带 ID+Tokens、cost.Dims 是开放键同
// 一条纪律）。
type Observation struct {
	// Fingerprint 本回合动作指纹（宿主算）：同一动作同一指纹。
	Fingerprint string
	// Normalized 抹掉易变部分后的指纹（宿主算）：page=1/2/3 ⇒ "page=N"。
	Normalized string
	// Tools 本回合工具调用数。
	Tools int
	// Failed 本回合是否出现失败结果。
	Failed bool
	// Settled 本回合是否已收敛（终答 / 交接）。
	Settled bool
	// ResultChanged 本回合的结果与上一轮**同指纹**的结果不同（true = 有进展）。
	//
	// 为什么必须显式给这个事实：「相同调用但输出变了」是**进展**，不是重复。
	// 只看参数的去重会把「换个页码再查一次」这类有意义的重试误判成循环。
	ResultChanged bool
	// Exempt 本回合**不参与无进展判定**（重复计数与空转计数都不参与）。
	//
	// 两个已知的必须豁免的场景：
	// ① 轮询 / 查状态类动作——「输出变了才是进展信号」（agentloop 的 Repetitive
	//    标记同款）；
	// ② **刻意的规划步**——smolagents 的 planning_interval 会每隔 N 步插一个规划
	//    回合，而规划回合**必然零工具调用**，按空转判据会被误伤。
	Exempt bool
}

// Shape 无进展形态（判定词表；主人表 roundShapeAll，枚举出口 AllShapes）。
type Shape string

const (
	// ShapeNone 未命中任何形态。
	ShapeNone Shape = "none"
	// ShapeRepeat 精确重复：同指纹连着来。
	ShapeRepeat Shape = "repeat"
	// ShapeOscillation 震荡：A B A B …
	ShapeOscillation Shape = "oscillation"
	// ShapeDrift 缓慢漂移：归一后同、原指纹不同。
	ShapeDrift Shape = "drift"
	// ShapeErrorSpiral 错误重试螺旋：连着失败。
	ShapeErrorSpiral Shape = "error_spiral"
	// ShapeIdle 只规划不动手：有回合、零工具、未收敛。
	ShapeIdle Shape = "idle"
)

// Valid 报告该形态是否在词表内。
func (s Shape) Valid() bool { return roundShapeAll[string(s)] }

// Finding 无进展检测结果。
type Finding struct {
	// Stalled 已达**停止档**：应当温和叫停。
	Stalled bool
	// Warn 首档命中：只该**警告回灌**，不停止——给模型一次自我修正的机会。
	Warn bool
	// Shape 形态。
	Shape Shape
	// Since 该形态连续成立的起点（全局 1 起；0 = 判不出）。
	Since int
}

// Config 无进展检测参数（零值 = 缺省）。
type Config struct {
	// Window 看最近多少个回合（0 = DefaultWindow）。
	//
	// **下限会被向上钳位到 2×Repeat+2**：震荡形态的最小命中窗口是 2×Repeat 个
	// 回合，再加一次「再犯」才到停止档 ⇒ 窗口不够时停止档**不可达**，而判据
	// 不可达等于判据不存在。故此处向上钳位（不是判不出）。
	Window int
	// Repeat 连续几回合构成重复（0 = DefaultRepeat）。
	Repeat int
}

const (
	// DefaultWindow 缺省观察窗口（回合数）。
	//
	// 8 = 2×DefaultRepeat+2 ⇒ 每一形态的停止档都可达。
	DefaultWindow = 8
	// DefaultRepeat 缺省重复阈值（回合数）。
	DefaultRepeat = 3
)

// Detect 无进展检测（纯函数；只看最近 Window 个回合）。
//
// 判定顺序（取病因最具体的那条）：错误螺旋 → 精确重复 → 震荡 → 缓慢漂移 → 空转。
//
// 定档：形态的**连续成立长度 L** == 阈值 ⇒ 首档（Warn，给模型一次自我修正机会）；
// L > 阈值 ⇒ 停止档（Stalled，温和叫停）。
func Detect(obs []Observation, cfg Config) Finding {
	window := cfg.Window
	if window <= 0 {
		window = DefaultWindow
	}
	k := cfg.Repeat
	if k <= 0 {
		k = DefaultRepeat
	}
	if min := 2*k + 2; window < min {
		window = min
	}
	seq, idx := windowRounds(obs, window)
	if len(seq) == 0 {
		return Finding{Shape: ShapeNone}
	}
	if L := trailingFailed(seq); L >= k {
		return tierFinding(seq, idx, ShapeErrorSpiral, L, k)
	}
	if L := trailingSameFingerprint(seq); L >= k {
		return tierFinding(seq, idx, ShapeRepeat, L, k)
	}
	if L := trailingAlternating(seq); L >= 2*k {
		return tierFinding(seq, idx, ShapeOscillation, L, 2*k)
	}
	if L := trailingSameNormalized(seq); L >= k {
		return tierFinding(seq, idx, ShapeDrift, L, k)
	}
	if L := trailingIdle(seq); L >= k {
		return tierFinding(seq, idx, ShapeIdle, L, k)
	}
	return Finding{Shape: ShapeNone}
}

// windowRounds 取窗口内**未豁免**的回合，并给出它们在原序列中的下标（用于报 Since）。
func windowRounds(obs []Observation, window int) ([]Observation, []int) {
	start := len(obs) - window
	if start < 0 {
		start = 0
	}
	seq := make([]Observation, 0, window)
	idx := make([]int, 0, window)
	for i := start; i < len(obs); i++ {
		if obs[i].Exempt {
			continue
		}
		seq = append(seq, obs[i])
		idx = append(idx, i)
	}
	return seq, idx
}

// trailingFailed 尾部连续失败长度。
func trailingFailed(seq []Observation) int {
	n := 0
	for i := len(seq) - 1; i >= 0; i-- {
		if !seq[i].Failed {
			break
		}
		n++
	}
	return n
}

// trailingSameFingerprint 尾部同指纹连续长度。
//
// 「结果变了」是进展 ⇒ streak 重新起算（与 agentloop 的
// "a repeated call whose output changed is progress, so the streak restarts" 同款）。
func trailingSameFingerprint(seq []Observation) int {
	if len(seq) == 0 {
		return 0
	}
	last := seq[len(seq)-1]
	if last.ResultChanged {
		return 1
	}
	n := 1
	for i := len(seq) - 2; i >= 0; i-- {
		if seq[i].Fingerprint != last.Fingerprint || seq[i].ResultChanged {
			break
		}
		n++
	}
	return n
}

// trailingAlternating 尾部周期 2 交替的连续长度（周期 1 属「重复」，返回 0）。
func trailingAlternating(seq []Observation) int {
	n := len(seq)
	if n < 3 {
		return 0
	}
	last, prev := seq[n-1], seq[n-2]
	if last.Fingerprint == prev.Fingerprint || last.ResultChanged || prev.ResultChanged {
		return 0
	}
	length := 2
	for i := n - 3; i >= 0; i-- {
		if seq[i].ResultChanged || seq[i].Fingerprint != seq[i+2].Fingerprint {
			break
		}
		length++
	}
	return length
}

// trailingSameNormalized 尾部归一指纹相同的连续长度。
func trailingSameNormalized(seq []Observation) int {
	if len(seq) == 0 {
		return 0
	}
	last := seq[len(seq)-1].Normalized
	if last == "" {
		return 0 // 没有归一指纹 ⇒ 无法判漂移（内核不猜「什么是易变部分」）
	}
	n := 1
	for i := len(seq) - 2; i >= 0; i-- {
		if seq[i].Normalized != last || seq[i].ResultChanged {
			break
		}
		n++
	}
	return n
}

// trailingIdle 尾部「零工具且未收敛」的连续长度。
func trailingIdle(seq []Observation) int {
	n := 0
	for i := len(seq) - 1; i >= 0; i-- {
		if seq[i].Tools != 0 || seq[i].Settled {
			break
		}
		n++
	}
	return n
}

// tierFinding 按连续成立长度定档：L == 阈值 ⇒ 首档；L > 阈值 ⇒ 停止档。
func tierFinding(seq []Observation, idx []int, shape Shape, L, threshold int) Finding {
	since := 0
	if len(idx) >= L {
		since = idx[len(idx)-L] + 1
	}
	f := Finding{Shape: shape, Since: since}
	if L > threshold {
		f.Stalled = true
	} else {
		f.Warn = true
	}
	return f
}
```

`roundguard/word.go` **追加**：

```go
// roundShapeAll 无进展形态词表 → 成员（单一事实源）。
var roundShapeAll = map[string]bool{
	ShapeNone:        true,
	ShapeRepeat:      true,
	ShapeOscillation: true,
	ShapeDrift:       true,
	ShapeErrorSpiral: true,
	ShapeIdle:        true,
}

// AllShapes 返回无进展形态词表的全集（排序后的新切片）。
func AllShapes() []string {
	out := make([]string, 0, len(roundShapeAll))
	for s := range roundShapeAll {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}
```

- [ ] **Step 4: 修正 spec 的窗口下限（计划阶段发现的缺陷）**

spec §7.2/§7.3 写的「默认 Window=6」与「定档需 L>阈值」互相矛盾：震荡形态的最小
命中窗口是 `2×Repeat`（=6），要再犯一次到停止档需要 **8** 个回合 ⇒ 窗口 6 时
**停止档永不可达**（判据不可达 = 判据不存在）。

改 spec 文件（**只改这一处，其余不动**）：

- §7.2 的 `Config` 注释：`Window` 补一句「**下限会被向上钳位到 2×Repeat+2**——
  震荡形态的停止档需要那么多回合才可达；窗口不够时向上钳位（不是判不出）」。
- §7.2 的 `DefaultWindow`：`6` ⇒ `8`（并注明 8 = 2×3+2）。
- §7.3 的表述：从「最近 k 回合全等」改为「**尾部连续成立长度 L**」（尾部形态能
  区分首档与停止档；定长窗口写法做不到）。

- [ ] **Step 5: 登记面（Shape 表 + Limit×Shape 的 distinct 关系）**

（a）`architecture-policy.yaml` 的 `word_tables.tables` **追加**：

```yaml
    - pkg: roundguard
      backing: roundShapeAll
      enum: AllShapes
      predicate: Shape.Valid
      family: Shape
      desc: 无进展形态词表（6 值）
```

（b）`word_tables.relations` **追加**一条（`none` 在两张表里同形：额度类别 vs 形态
类别，含义不同）：

```yaml
    # 额度类别「none」≠ 形态类别「none」：一个是「未因额度叫停」，一个是
    # 「未命中任何形态」——同形只是拼写巧合，手抄方必须**在本包命名**。
    - a: {pkg: roundguard, enum: AllLimits}
      b: {pkg: roundguard, enum: AllShapes}
      relation: distinct
      anchors:
        a: [{all: true}]
        b: [{all: true}]
```

- [ ] **Step 6: 跑测试与门禁、刷新 README、提交**

Run: `make test && make architecture-check && make apilifecheck`
Expected: 全 rc=0。

```bash
git add roundguard/detect.go roundguard/word.go roundguard/detect_test.go \
        architecture-policy.yaml \
        docs/superpowers/specs/2026-11-21-roundguard-agent-loop-design.md README.md
git commit -m "feat(roundguard): 无进展检测（五形态 + 定档 + 通用豁免面）"
```

---

### Task 4: 组合结论（`Decide`）

**Files:**
- Create: `roundguard/decide.go`
- Test: `roundguard/decide_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Classify` / `Reason` / `Route`；Task 2 的 `Budget` /
  `Used` / `Verdict` / 三个 `Check*` / `StopPolicy`；Task 3 的 `Detect` /
  `Finding` / `Config`。
- Produces:
  - `type Snapshot struct { Outcome Outcome; Used Used; Obs []Observation }`
  - `type Decision struct { Reason Reason; Continue bool; Limit Limit; Shape Shape; Warn bool; Used Used }`
  - `func Decide(s Snapshot, b Budget, cfg Config) Decision`

- [ ] **Step 1: 写失败测试**

```go
package roundguard_test

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/roundguard"
)

// TestDecideTerminalOutcomeBeatsBudget 钉住**结局终局优先**：模型给了终答，哪怕
// 刚好撞上额度上限，也以结局为准（模型说完了就是完了）。
func TestDecideTerminalOutcomeBeatsBudget(t *testing.T) {
	d := roundguard.Decide(roundguard.Snapshot{
		Outcome: roundguard.Outcome{Completed: true},
		Used:    roundguard.Used{Rounds: 99},
	}, roundguard.Budget{MaxRounds: 1}, roundguard.Config{})
	if d.Reason != roundguard.ReasonCompleted {
		t.Fatalf("终局优先应判 %q，实得 %q", roundguard.ReasonCompleted, d.Reason)
	}
	if d.Continue {
		t.Fatal("终局不该判为可继续")
	}
}

// TestDecidePolicyForceFinal 钉住处置口径分流：强制收尾 ⇒ 不是失败。
func TestDecidePolicyForceFinal(t *testing.T) {
	d := roundguard.Decide(roundguard.Snapshot{
		Outcome: roundguard.Outcome{ToolCalls: 1},
		Used:    roundguard.Used{Rounds: 5},
	}, roundguard.Budget{MaxRounds: 2, Policy: roundguard.PolicyForceFinal}, roundguard.Config{})
	if d.Reason != roundguard.ReasonForcedFinal {
		t.Fatalf("应判 %q，实得 %q", roundguard.ReasonForcedFinal, d.Reason)
	}
	if d.Reason.IsFailure() {
		t.Fatal("强制收尾不该判为失败型")
	}
	if d.Limit != roundguard.LimitRounds {
		t.Fatalf("应命中 %q，实得 %q", roundguard.LimitRounds, d.Limit)
	}
}

// TestDecidePolicyFailFast 钉住：同一额度、另一种口径 ⇒ 失败型。
func TestDecidePolicyFailFast(t *testing.T) {
	d := roundguard.Decide(roundguard.Snapshot{
		Outcome: roundguard.Outcome{ToolCalls: 1},
		Used:    roundguard.Used{Rounds: 5},
	}, roundguard.Budget{MaxRounds: 2, Policy: roundguard.PolicyFailFast}, roundguard.Config{})
	if d.Reason != roundguard.ReasonBudgetExhausted {
		t.Fatalf("应判 %q，实得 %q", roundguard.ReasonBudgetExhausted, d.Reason)
	}
	if !d.Reason.IsFailure() {
		t.Fatal("失败即止口径应判为失败型")
	}
}

// TestDecideStalledWinsOverContinue 钉住：停滞 ⇒ 温和叫停（不是失败）。
func TestDecideStalledWinsOverContinue(t *testing.T) {
	obs := []roundguard.Observation{
		{Fingerprint: "a"},
		{Fingerprint: "a"},
		{Fingerprint: "a"},
		{Fingerprint: "a"},
	}
	d := roundguard.Decide(roundguard.Snapshot{
		Outcome: roundguard.Outcome{ToolCalls: 1},
		Obs:     obs,
	}, roundguard.Budget{}, roundguard.Config{})
	if d.Reason != roundguard.ReasonStalled {
		t.Fatalf("应判 %q，实得 %q", roundguard.ReasonStalled, d.Reason)
	}
	if d.Reason.IsFailure() {
		t.Fatal("温和叫停不该判为失败型")
	}
	if !d.Stalled {
		t.Fatalf("应判停止档：%+v", d)
	}
}

// TestDecideContinuesNormally 钉住正常路径。
func TestDecideContinuesNormally(t *testing.T) {
	d := roundguard.Decide(roundguard.Snapshot{
		Outcome: roundguard.Outcome{ToolCalls: 1},
	}, roundguard.Budget{}, roundguard.Config{})
	if !d.Continue {
		t.Fatalf("应判为可继续：%+v", d)
	}
	if d.Reason != roundguard.ReasonToolCalls {
		t.Fatalf("应判 %q，实得 %q", roundguard.ReasonToolCalls, d.Reason)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `make test`
Expected: FAIL —— 符号未定义。

- [ ] **Step 3: 写实现**

`roundguard/decide.go`（全文）：

```go
package roundguard

// Snapshot 一回合末尾的完整事实（宿主组装）。
type Snapshot struct {
	// Outcome 本回合的结局事实。
	Outcome Outcome
	// Used 当前用量快照。
	Used Used
	// Obs 回合历史（含本回合，按时间序）。
	Obs []Observation
}

// Decision 一条可执行结论（组合判定面）。
type Decision struct {
	// Reason 结局分类。
	Reason Reason
	// Continue 允许**按原样**进入下一回合。
	Continue bool
	// Limit 命中的额度类别（LimitNone = 未因额度叫停）。
	Limit Limit
	// Shape 无进展形态（仅 LimitStalled 时有效）。
	Shape Shape
	// Warn 无进展**首档**：只该警告回灌，不停止。
	Warn bool
	// Used 判定时的用量快照（留痕用）。
	Used Used
}

// Decide 合成一条结论：结局 → 额度（按 Policy 处置）→ 无进展。
//
// 判定优先级（顺序即语义）：
//
//  1. **结局终局优先**：模型给了终答 / 交接 / 报错 / 中止 ⇒ 以结局为准，
//     **不被额度打断**（模型说完了就是完了——哪怕刚好撞上上限）。
//  2. 否则（结局要求继续）：额度用尽 ⇒ 按 Budget.Policy 处置——
//     PolicyForceFinal ⇒ ReasonForcedFinal（转强制收尾，产物仍可用）；
//     PolicyFailFast ⇒ ReasonBudgetExhausted（失败型收敛）。
//  3. 否则：无进展命中 ⇒ ReasonStalled（按档位决定 Continue）。
//  4. 否则：Continue=true，照常进入下一回合。
//
// ⚠️ Continue=false 有**三种含义**，靠 Reason 区分，宿主不得一律当失败处理：
// ReasonFailed / ReasonNoOutcome / ReasonAborted / ReasonBudgetExhausted 是
// **失败型**（IsFailure()==true）；ReasonForcedFinal 是**转强制收尾**；
// ReasonStalled 是**温和叫停**。
func Decide(s Snapshot, b Budget, cfg Config) Decision {
	d := Decision{Reason: ReasonNoOutcome, Used: s.Used}
	route := Classify(s.Outcome)
	d.Reason = route.Reason
	if !route.Continue {
		// 结局已是终局：不给额度/停滞改写它的机会。
		return d
	}
	// 额度判定（调用前口径的同一份判据）。
	switch v := CheckRounds(b, s.Used); {
	case !v.Allowed:
		d.Limit = v.Limit
		if b.effectivePolicy() == PolicyFailFast {
			d.Reason = ReasonBudgetExhausted
		} else {
			d.Reason = ReasonForcedFinal
		}
		return d
	}
	if v := CheckToolCalls(b, s.Used); !v.Allowed {
		d.Limit = v.Limit
		if b.effectivePolicy() == PolicyFailFast {
			d.Reason = ReasonBudgetExhausted
		} else {
			d.Reason = ReasonForcedFinal
		}
		return d
	}
	// 无进展检测。
	f := Detect(s.Obs, cfg)
	if f.Shape != ShapeNone {
		d.Limit = LimitStalled
		d.Shape = f.Shape
		d.Warn = f.Warn
		d.Reason = ReasonStalled
		if f.Stalled {
			d.Continue = false
		}
		return d
	}
	d.Continue = true
	return d
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `make test`
Expected: PASS。

- [ ] **Step 5: 刷新 README 工程底座表并提交**

```bash
git add roundguard/decide.go roundguard/decide_test.go README.md
git commit -m "feat(roundguard): 组合结论 Decide（结局 → 额度按策略 → 无进展）"
```

---

### Task 5: 导出 API 面上限门禁（抄 agentloop 的 `apibudget_test.go`）

**Files:**
- Create: `roundguard/exportbudget_test.go`

**Interfaces:**
- Consumes: 无（只读本包导出符号）。
- Produces: 无导出符号（门禁在测试里）。

- [ ] **Step 1: 写门禁测试**

```go
package roundguard_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportedAPICeiling 本包**导出符号总数**的上限。
//
// 为什么要有这道门禁（抄 smhanov/agentloop 的 apibudget_test.go，2026-11-21 实读）：
// 「加公开 API 必须是一次自觉行为」——判据包一旦开始长胖，宿主就会开始依赖那些
// 「其实只是实现细节」的符号，于是它再也删不掉。上限让「加一个导出符号」变成一次
// 需要抬天花板（并附理由）的显式决定，而不是顺手为之。
//
// 计数口径（刻意保持简单）：
//   - 每个导出的包级 func / var / const / type 声明记 1（const/var 块按 spec 记）；
//   - 每个导出结构体的导出字段记 1；
//   - 每个导出类型上的导出方法记 1。
//
// 抬高上限必须是**单独一次变更**并附理由，不得与「顺手加个导出」混在一起。
const exportedAPICeiling = 60

func TestExportedAPISurfaceStaysUnderCeiling(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("解析本包失败：%v", err)
	}
	count := 0
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			count += countExported(f)
		}
	}
	if count > exportedAPICeiling {
		t.Fatalf("导出符号 %d 个，超出上限 %d——抬高上限必须是单独一次变更并附理由", count, exportedAPICeiling)
	}
}

// countExported 统计一个文件里的导出符号数。
func countExported(f *ast.File) int {
	n := 0
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil && decl.Name.IsExported() {
				n++
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						n++
						n += countExportedFields(s.Type)
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							n++
						}
					}
				}
			}
		}
	}
	return n
}

// countExportedFields 统计导出结构体的导出字段数。
func countExportedFields(expr ast.Expr) int {
	st, ok := expr.(*ast.StructType)
	if !ok || st.Fields == nil {
		return 0
	}
	n := 0
	for _, field := range st.Fields.List {
		for _, name := range field.Names {
			if name.IsExported() {
				n++
			}
		}
	}
	return n
}

// TestNoTestHelperLeaksIntoPackage 钉住：门禁本身不得把测试辅助符号暴露成导出符号。
func TestNoTestHelperLeaksIntoPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读目录失败：%v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("读 %s 失败：%v", e.Name(), err)
		}
		if strings.Contains(string(data), "package roundguard_test") {
			t.Fatalf("%s 的包名与目录不符（测试文件不得混进生产包）", e.Name())
		}
	}
}
```

- [ ] **Step 2: 跑测试**

Run: `make test`
Expected: PASS。若超上限，按实测数**单独一次变更**抬高并写清理由。

- [ ] **Step 3: 提交**

```bash
git add roundguard/exportbudget_test.go README.md
git commit -m "test(roundguard): 导出 API 面上限门禁（加公开 API 必须是一次自觉行为）"
```

---

### Task 6: 反向验证矩阵（坏实现必须会红）

**Files:**
- Create: `roundguard/mutation_test.go`

**Interfaces:**
- Consumes: Task 1–4 的全部导出符号。
- Produces: 无导出符号。

- [ ] **Step 1: 写边界钉死用例**

```go
package roundguard_test

import (
	"testing"

	"github.com/yi-nology/goagent-kernel/roundguard"
)

// 本文件是**反向验证矩阵**：每条用例都钉在「坏实现会红」的边界上。
//
// 本仓纪律：改任何判定逻辑，必须同时给出「坏实现会红」的用例——只测「好实现能过」
// 的用例，一个把数字比较写成字符串比较的错实现照样全过。下面每条都对应一类
// 具体变异（把 >= 写成 >、把顺序颠倒、把零值当不限、把豁免只对一种形态生效…）。

// TestMutationGuardCheckRoundsOffByOne 钉住「>=/ >」这一类变异。
func TestMutationGuardCheckRoundsOffByOne(t *testing.T) {
	b := roundguard.Budget{MaxRounds: 3}
	if v := roundguard.CheckRounds(b, roundguard.Used{Rounds: 2}); !v.Allowed {
		t.Fatal("上限 3 / 已用 2 必须放行（把 >= 写成 > 的错实现会红）")
	}
	if v := roundguard.CheckRounds(b, roundguard.Used{Rounds: 3}); v.Allowed {
		t.Fatal("上限 3 / 已用 3 必须拒绝（把 >= 写成 > 的错实现不会红——这是第二道）")
	}
}

// TestMutationGuardZeroMeansUnlimitedNotInvalid 钉住「零值 = 不限」这条口径。
func TestMutationGuardZeroMeansUnlimitedNotInvalid(t *testing.T) {
	b := roundguard.Budget{}
	if err := b.Validate(); err != nil {
		t.Fatalf("零值应合法（显式关闭）：%v", err)
	}
	if v := roundguard.CheckRounds(b, roundguard.Used{Rounds: 10000}); !v.Allowed {
		t.Fatal("零值应表示不限")
	}
}

// TestMutationGuardOutcomeOrder 钉住判定次序：把「要工具」与「给终答」调换的实现
// 必须被抓住。
func TestMutationGuardOutcomeOrder(t *testing.T) {
	r := roundguard.Classify(roundguard.Outcome{ToolCalls: 1, Completed: true})
	if r.Reason != roundguard.ReasonToolCalls {
		t.Fatalf("要工具必须优先：%q", r.Reason)
	}
}

// TestMutationGuardShapeOrder 钉住形态次序：错误螺旋必须优先于精确重复。
func TestMutationGuardShapeOrder(t *testing.T) {
	seq := []roundguard.Observation{
		{Fingerprint: "a", Failed: true},
		{Fingerprint: "a", Failed: true},
		{Fingerprint: "a", Failed: true},
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape != roundguard.ShapeErrorSpiral {
		t.Fatalf("错误螺旋必须优先：%q", f.Shape)
	}
}

// TestMutationGuardTierBoundary 钉住定档边界：L == 阈值 ⇒ 首档；L == 阈值+1 ⇒ 停止档。
func TestMutationGuardTierBoundary(t *testing.T) {
	k := roundguard.DefaultRepeat
	at := make([]roundguard.Observation, k)
	for i := range at {
		at[i] = roundguard.Observation{Fingerprint: "a"}
	}
	f := roundguard.Detect(at, roundguard.Config{Repeat: k})
	if !f.Warn || f.Stalled {
		t.Fatalf("L==阈值应为首档：%+v", f)
	}
	over := append(append([]roundguard.Observation{}, at...), roundguard.Observation{Fingerprint: "a"})
	f = roundguard.Detect(over, roundguard.Config{Repeat: k})
	if !f.Stalled {
		t.Fatalf("L==阈值+1 应为停止档：%+v", f)
	}
}

// TestMutationGuardWindowFloor 钉住窗口下限：窗口不够时向上钳位，而不是让停止档
// 静默不可达。
func TestMutationGuardWindowFloor(t *testing.T) {
	k := roundguard.DefaultRepeat
	// 造一段足以触发震荡停止档的序列：窗口给 1（远小于下限）也必须判得出。
	seq := make([]roundguard.Observation, 0, 12)
	for i := 0; i < 12; i++ {
		if i%2 == 0 {
			seq = append(seq, roundguard.Observation{Fingerprint: "a"})
		} else {
			seq = append(seq, roundguard.Observation{Fingerprint: "b"})
		}
	}
	f := roundguard.Detect(seq, roundguard.Config{Window: 1, Repeat: k})
	if f.Shape != roundguard.ShapeOscillation {
		t.Fatalf("窗口被钳位后仍应判震荡：%+v", f)
	}
}

// TestMutationGuardExemptAppliesToBothCounters 钉住：豁免对重复计数与空转计数
// 同时生效（只对一种生效的错实现会红）。
func TestMutationGuardExemptAppliesToBothCounters(t *testing.T) {
	seq := []roundguard.Observation{
		{Fingerprint: "a"},
		{Fingerprint: "a", Exempt: true},
		{Fingerprint: "a"},
	}
	f := roundguard.Detect(seq, roundguard.Config{})
	if f.Shape == roundguard.ShapeRepeat {
		t.Fatalf("豁免必须对重复计数生效：%+v", f)
	}
	idle := []roundguard.Observation{
		{},
		{Exempt: true},
		{},
	}
	f = roundguard.Detect(idle, roundguard.Config{})
	if f.Shape == roundguard.ShapeIdle {
		t.Fatalf("豁免必须对空转计数生效：%+v", f)
	}
}

// TestMutationGuardIsFailureCoversEveryValue 钉住：IsFailure 的结论对**全部**取值
// 都有定义（漏一个取值 ⇒ 宿主对它无从判断）。
func TestMutationGuardIsFailureCoversEveryValue(t *testing.T) {
	for _, s := range roundguard.AllRoundReasons() {
		r := roundguard.Reason(s)
		if !r.Valid() {
			t.Fatalf("枚举出口给出的 %q 不合法", s)
		}
		// 每一项都必须有确定的结论（true 或 false），不允许「未知」。
		_ = r.IsFailure()
	}
}

// TestMutationGuardPolicySwitch 钉住：两种处置口径必须走不同分支。
func TestMutationGuardPolicySwitch(t *testing.T) {
	snap := roundguard.Snapshot{
		Outcome: roundguard.Outcome{ToolCalls: 1},
		Used:    roundguard.Used{Rounds: 9},
	}
	force := roundguard.Decide(snap, roundguard.Budget{MaxRounds: 1, Policy: roundguard.PolicyForceFinal}, roundguard.Config{})
	fast := roundguard.Decide(snap, roundguard.Budget{MaxRounds: 1, Policy: roundguard.PolicyFailFast}, roundguard.Config{})
	if force.Reason == fast.Reason {
		t.Fatalf("两种口径必须给出不同结局：%q", force.Reason)
	}
	if force.Reason.IsFailure() == fast.Reason.IsFailure() {
		t.Fatal("强制收尾与失败即止的失败型结论必须相反")
	}
}
```

- [ ] **Step 2: 跑测试**

Run: `make test`
Expected: PASS。**逐条变异验证**：临时把某个判定改错（例如把 `>=` 写成 `>`），
确认对应用例变红，然后改回。

- [ ] **Step 3: 提交**

```bash
git add roundguard/mutation_test.go README.md
git commit -m "test(roundguard): 反向验证矩阵——坏实现必须会红"
```

---

### Task 7: 文档登记面收口（README / AI-GUIDE / USAGE）

**Files:**
- Modify: `README.md`（包表行数 + 工程底座表三项）
- Modify: `docs/AI-GUIDE.md`（包数与分组表）
- Modify: `docs/USAGE.md`（宿主接线章节）

**Interfaces:**
- Consumes: Task 1–4 的导出符号（USAGE 章节要给宿主看接线示例）。
- Produces: 无代码符号。

- [ ] **Step 1: README 包表 + 工程底座表**

按 Task 1 Step 6 的格式刷新区块；工程底座表三项按那三条命令重算。

- [ ] **Step 2: docs/AI-GUIDE.md 的包数与分组**

AI-GUIDE §1.3 的「51 个包分五组」与分组表要更新：`roundguard` 属**机制面**包 ⇒
机制面包数 +1；并把 `roundguard` 加进机制面那一行的包列表（该表的包名顺序与
README 包表一致）。

- [ ] **Step 3: docs/USAGE.md 增「宿主怎么接 roundguard」**

追加一节，内容 = spec §11 的接线示例 + 三条纪律：

```markdown
## 宿主怎么接 roundguard

roundguard 是**判据层**：它不调模型、不执行工具，只回答三个问题——这次还能不能转、
这一回合以什么结束、这一圈是不是白转。宿主把「模型这一回合说了什么」映射成
`Outcome`、把「这一回合干了什么」映射成 `Observation[]`，然后调用本包的判据。

```go
for {
    if v := roundguard.CheckRounds(b, meter.Used()); !v.Allowed {
        // 额度用尽 ⇒ 按 Policy 处置（强制收尾 / 失败即止）
    }
    resp := model.Call(ctx, msgs, tools)
    meter.Round()
    // 工具执行前查单工具额度；被拒时**仍回灌一条失败结果**给模型（配对不破）
    d := roundguard.Decide(roundguard.Snapshot{Outcome: out, Used: meter.Used(), Obs: obs}, b, cfg)
    if !d.Continue {
        return finish(d)
    }
}
```

**三条纪律**：

1. **失败要回灌、也要记账**：工具失败既作为 `tool_result` 回灌给模型（模型自行修正），
   又作为 `Observation.Failed` 记一条——只回灌不记账 ⇒ 无进展检测永远看不到错误螺旋。
2. **指纹要稳定，且要带上「结果变了」**：同一逻辑动作必须给同一指纹；归一指纹要抹掉
   易变部分（页码/时间戳/行号），否则漂移检测永远不命中。「相同调用但输出变了」
   必须显式记为 `ResultChanged: true`——那是**进展**，不记就会把有意义的重试误判成
   循环。轮询 / 查状态类动作、以及刻意的规划步，要记 `Exempt: true`。
3. **「停止」与「失败」分开处置**：`Decision.Reason` 区分失败型收敛
   （`IsFailure()==true`）与温和叫停 / 强制收尾——把「被上限温和收停」当故障上报
   就是假红。
```

- [ ] **Step 4: 跑门禁**

Run: `make docsync && make architecture-check && make apilifecheck`
Expected: 全 rc=0。

- [ ] **Step 5: 提交**

```bash
git add README.md docs/AI-GUIDE.md docs/USAGE.md
git commit -m "docs: roundguard 登记面收口（包表 / 分组表 / 宿主接线章节）"
```

---

### Task 8: 全量门禁 + 覆盖率 + 发版

**Files:**
- Modify: `CHANGELOG.md`（插新段）

**Interfaces:**
- Consumes: Task 1–7 的全部产出。
- Produces: 一个打上 annotated tag 的版本。

- [ ] **Step 1: 跑全量门禁**

Run: `make check`
Expected: rc=0（含 `test` / `cover-gate` / `vuln` / `race` / `crossbuild` /
`architecture-check` / `docsync` / `apilifecheck` / `hygiene` 等）。

⚠️ `make race` 需要 cgo（`RACE_ENV := CGO_ENABLED=1`），首次跑可能较慢。

- [ ] **Step 2: 确认逐包覆盖率**

Run: `make cover-gate`
Expected: `roundguard` 包 ≥ 70%（无豁免）。若不达标，补用例**而不是**登记豁免——
豁免要写原因，且「理由消失 ⇒ 当天拆掉」。

- [ ] **Step 3: 扫 U+FFFD**

Run: `grep -rn $'\xef\xbf\xbd' roundguard/ docs/ README.md`
Expected: 无输出。

- [ ] **Step 4: CHANGELOG 插新段**

在 `CHANGELOG.md` 的**最新段之前**插入本版段（只插新段，不改历史段落）：

```markdown
## v0.52.0 — 回合守卫（roundguard）

**新增**：`roundguard` 机制包——模型侧循环的三条跨宿主共享判据：

- 回合结局分类（10 值判定词表，`Classify`）：结局终局与「温和叫停 / 强制收尾」
  通过 `Reason.IsFailure()` 可分——「被上限温和收停」与「真的坏了」在宿主侧必须可分。
- 回合次数预算（`CheckRounds` / `CheckToolCalls` / `CheckTool` + 进程内 `Meter`）：
  **额度维度（事实）与处置口径（策略）分开**——五家主流实现三种口径，没有共识，
  故处置由 `Budget.Policy` 承载（缺省强制收尾）。
- 无进展检测（`Detect`）：五形态（精确重复 / 震荡 / 缓慢漂移 / 错误重试螺旋 /
  只规划不动手）+ 定档（警告回灌 → 再犯才停）+ 通用豁免面（轮询类动作、刻意规划步）。

**设计依据**：五家主流实现（agentloop / OpenAI Agents SDK / pydantic-ai / smolagents /
Claude Agent SDK）的源码实读对照，见
`docs/superpowers/specs/2026-11-21-roundguard-agent-loop-design.md` §1.4。

**登记面**：`architecture-policy.yaml`（模块 / scan_root / 四张判定词表 / 四条
relations）、`api-lifecycle.yaml`（Experimental）、`docs/error-semantics.md`、
README 包表与工程底座表、`docs/AI-GUIDE.md` 分组表、`docs/USAGE.md` 接线章节。
```

- [ ] **Step 5: 提交并打 tag**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): v0.52.0——回合守卫（roundguard）"

git tag -a v0.52.0 -m "v0.52.0 回合守卫：结局分类 + 次数预算 + 无进展检测"
git push origin <feature-branch>
git push origin v0.52.0
```

⚠️ tag 必须是 **annotated**（`releasecheck` 判）。tag CI 与 releasecheck 需再绿一次。
合并前确认 **GitHub 分支保护的 required contexts** 与 `make check` 的目标一致。

---

## 实施后现状（待填）

- Task 1–7 全部合入后，`roundguard` 包的导出面：`Reason`（10 值 + 3 方法）、
  `Limit`（5 值 + 1 方法）、`Shape`（6 值 + 1 方法）、`StopPolicy`（2 值 + 1 方法）、
  `Outcome` / `Route` / `Budget` / `Used` / `Verdict` / `Meter` / `Snapshot` /
  `Decision` / `Finding` / `Config` / `Observation` + 五个判据函数 + 四个枚举出口
  + 1 个哨兵。
- 门禁：`make check` 全绿；`roundguard` 逐包覆盖 ≥ 70%。
- 版本：v0.52.0。

---

## 自审记录

**spec 覆盖核对**（逐节过了一遍，14 节全部有落点）：
§0/§1/§2.1/§2.3/§3（问题、目标、外部参照、命名三分表）→ Task 1 的包头注释（命名撞词与
划界写进 `roundguard/roundguard.go` 的包注释）；§2.2 非目标（不调模型 / 不重判重试 /
不做 token 预算 / 不做跨进程账）→ 实现中不越界，边界写进包注释；
§4.1 判定面清单 → 本计划 File Structure 表；§4.2 组合结论 → Task 4；
§5（结局分类）→ Task 1；§6（次数预算）→ Task 2；§7（无进展检测）→ Task 3；
§8（与既有机制逐条划界）→ Task 1 包注释 + Task 7 的 USAGE 三条纪律；
§9（登记面与门禁）→ Task 1/2/3/5/7 的登记步骤；§10（反向验证）→ Task 6；
§11（宿主接线示例）→ Task 7（`docs/USAGE.md` 增「宿主怎么接 roundguard」）；
§12（十个未决点）→ 本计划「拍板口径」小节（逐项列出采用值与与默认值的差异）；
§13（导出 API 面上限门禁）→ Task 5；§14（变更历史）→ spec 自身维护。

**占位符扫描**：无 TBD / TODO /「同 Task N」/「视情况处理」类空话。仅有的尖括号占位是
README 两张表里的行数/文件数（`<生产行数>` 等）——那是**实测值**，代码写完才有，
计划已给出逐字的重算命令与口径（docsync 对包表给 25% 容差、对工程底座表判相等）。
另有「实施后现状（待填）」一节，是实施完成时回填的现状记录，不是任务步骤。

**计划阶段发现的 spec 缺陷**（已在 Task 3 Step 4 修）：默认 `Window=6` 与定档规则
「L > 阈值 ⇒ 停止档」互相矛盾——震荡形态的停止档需要 2×Repeat+2 = 8 个回合，
窗口 6 时该分支**永不可达**（判据不可达 = 判据不存在）。改为默认 8 + 向上钳位。

**类型一致性**：`Reason`（Task 1）→ `IsFailure`（Task 1）→ `Decision.Reason`
（Task 4）；`Budget.Policy`（Task 2）→ `Decide` 的分流（Task 4）；
`Finding.Shape`（Task 3）→ `Decision.Shape`（Task 4）；`Limit`（Task 2）→
`Decision.Limit`（Task 4）。跨任务的类型名与字段名逐字一致。

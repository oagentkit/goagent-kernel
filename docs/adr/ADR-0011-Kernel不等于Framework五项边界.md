# ADR-0011 · Kernel ≠ Framework：宿主五项边界

| 项 | 内容 |
|---|---|
| **状态** | 已采纳（Accepted） |
| **日期** | 2026-11-28 |
| **背景** | 外部评审（2026-11-27 §3.3 问题五）点名：Kernel 不能变成另一个 Agent Framework——专家角色、Prompt、工具实现、领域判断、模型调用**五项都属于宿主**，Kernel 不需要知道它们是什么，只需要知道它们在执行过程中产生了什么**可判定的状态和操作**。这是必须守住的边界；评审同时给出口号：goagent-kernel 应该是 **Agent Execution Kernel，而不是 Agent Framework**——核心竞争力是长任务、多任务、多副本、异常恢复下的执行正确性，不是能调用多少模型。 |
| **决策** | 五项归属逐项成文（下表）；补**两条新判据**接入 `make architecture-check`：① `harness-no-prompt-template`（生产代码不得承载 prompt 模板）② `harness-no-model-client-import`（生产与测试代码不得 import 模型 client）；README 与文档站上线定位声明。 |
| **后果** | 内核里「长出模型侧叙事」的两条最常见路径（存 prompt 模板、直接 import 模型 SDK）从散文纪律变成**加载期就会红**的门禁；五项边界从口头约定变成有判据指针的对照表。规则拦**常见形态**（字符串特征 / import 路径），变体形态（拼接、编码构造）是如实登记的已知边界。 |
| **依据** | 下文「五项对照」与「判据落地」节；ADR-0001（领域知识红线）、ADR-0004（执行沙箱入口唯一）、裁决 #506–#509（worker/taskboard/hookengine 分界）、`docs/HOST-SURFACE.md`（宿主接入面清单——五项的宿主侧落点）。 |

> **一句话结论**：五项归宿主，内核只收「可判定的状态与操作」——Prompt 与模型
> client 两条最常见 Framework 化路径已成门禁（注入即红），其余三项靠既有判据
> （ADR-0001 领域红线 / 沙箱入口唯一 / 开放键契约）覆盖。

---

## 五项对照（逐项：归属 / kernel 对应物 / 判据指针）

| 项 | 归属 | kernel 对应物（可判定的状态与操作） | 现有判据 | 本 ADR 补的判据 |
|---|---|---|---|---|
| **专家角色** | 宿主 | kernel 不知「角色」——只认 `contract` 开放键里的状态值与报告字段；角色怎么分派是宿主的编排 | `harness-no-domain-vocabulary`（领域词不进内核生产代码） | `harness-no-prompt-template` 的 `# Role:` / `你是一个` 分支（角色叙事只许住在宿主 prompt 里） |
| **Prompt** | 宿主 | kernel 处理的是「执行中产生的可判定状态与操作」，不处理 prompt 的形状与内容 | 无（此前只有散文：AGENTS.md「不得长出领域知识」） | **`harness-no-prompt-template`（新增）**：`You are a…` / `# Role:` / `system prompt` / `act as a…` / `<|im_start|>` 等模板特征不得出现在内核生产代码 |
| **工具实现** | 宿主 | `ptc` 的 bindings/Stage 切片接缝、`backend` 后端注册——内核只定「要求 vs 实际」的判定口径，不长工具 | ADR-0004（执行沙箱入口唯一）、`tooldisclose`/`gatecheck` 分工、裁决 #507–#509 | 无新增（既有判据面已覆盖：工具本体进不来，进来的只有判定面） |
| **领域判断** | 宿主 | `contract` 开放键（`Field`/`SetField`，内核不解释不校验不假设）；领域字段走开放键是 2.1 心智模型的本体 | **ADR-0001**（领域知识红线，机器判）+ `harness-no-domain-vocabulary` | 无新增（红线已是最强形态：error 级 + 词表分支全覆盖样例） |
| **模型调用** | 宿主 | `roundguard` 类判据面只判「这一回合的结局」，**不调模型**；kernel go.mod 零模型 client 依赖 | 依赖方向 ext 面（隐式：`oagentkit/llm` 未登记任何模块 ⇒ import 即越界——但这条是**搭便车**，没人看得见它是边界） | **`harness-no-model-client-import`（新增）**：`oagentkit/llm` / `openai` / `anthropic` 的 import 显式禁令——边界从「恰好没登记」变成「明文不许」 |

## 判据落地（issue #528 的两条新判据）

1. **`harness-no-prompt-template`**（`forbid_regex`，行文本形态判据）：
   - 判定面：生产 `.go`（`ignore_test: true`——夹具引用样例是示例不是承诺；
     `scope_exclude: [cmd]`——门禁工具的 policy 文本自带样例词）；
   - 正反样例各若干（`require_rule_examples: true` 加载期自检，正反方向缺一即红
     ——只有正例证明不了判据没被放宽，只有反例证明不了判据还判得动）；
   - 反向验证（2026-11-28 实测）：任一生产文件注入 `var s = "You are a helper"`
     ⇒ `make architecture-check` 当场红；移除即绿。
2. **`harness-no-model-client-import`**（`forbid_import_regex`，import 路径形态，
   go/ast 判定，单行/块/别名/空白导入一视同仁）：
   - 判定面：生产 **与测试** 代码（`ignore_test: false`——测试里起真模型调用
     比生产代码更危险）；`scope_exclude: [cmd]`；
   - 反向验证（2026-11-28 实测）：任一文件注入 `import _ "github.com/oagentkit/llm/client"`
     ⇒ 当场红；移除即绿。

## 判据边界（如实登记）

- 两条规则拦**常见形态**：模板特征字符串、import 路径。**拼接/编码构造**的
  prompt、 vendored 改名的模型 client 拦不住——那需要语义分析，代价远大于收益。
  规则的价值是把「顺手存一段 You are a…」「顺手 import 一下 llm」这两条最常见
  的 Framework 化路径变成**当天显形**；
- docs 面（README/USAGE/文档站承载 prompt 示例）不在本规则判定面（规则判 `.go`），
  由本 ADR + review 兜；
- 五项对照的「现有判据」列随判据演进会变——**本表是对照的单源**，新增判据先改
  这里再落规则（与 api-lifecycle 的 reason 同纪律）。

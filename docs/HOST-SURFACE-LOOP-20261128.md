# 最小闭环对齐 HOST-SURFACE 的实跑证据（2026-11-28）

acceptance-path-id: host-surface-loop
acceptance-path-ran-at: 2026-11-28
acceptance-path-conclusion: pass

> **这是什么**：kernel#524 出口条件 5 的跨仓证据——goagent-runtime 的最小闭环
> 示例对照 `docs/HOST-SURFACE.md` 六步清单**真跑一次**并留结论。登记面见
> `docs/acceptance-paths.yaml`（owner: goagent-runtime；结论存档留内核仓——
> 别仓的仓内文件本仓核实不到，核验不到就只能靠人记，判据就空了）。
>
> **跟踪单**：goagent-runtime#54（本证据的产出单）。

## 一、跑了什么（可复跑入口）

```bash
# 1) 六步演示（零外部服务，脚本化假模型）
cd goagent-runtime && CGO_ENABLED=0 go run ./examples/reference-runtime
# 2) 示例套件（含两个共享契约套件）
cd goagent-runtime && CGO_ENABLED=0 go test ./examples/... -count=1
# 3) 全量测试（补强 pass 结论）
cd goagent-runtime && CGO_ENABLED=0 go test ./... -count=1
```

执行结果（2026-11-28，goagent-runtime main@f66c327）：

```
[1] 建任务：3 个步骤，依赖链 prepare → collect(依赖 prepare) → finalize(依赖 collect)
[2] 执行前两步：完成 [prepare collect]；进度快照已落库 seq=2；这 2 步的终态各提交一次
[3] 中断：流程停在第 2 步（[prepare collect]），第 3 步未开始；已落库的快照就是恢复点
[4] 恢复续跑：跳过 [prepare collect]，只跑 [finalize]；各步骤执行体调用次数 collect=1, finalize=1, prepare=1（前两步未重放）
[5] 并发认领：2 个执行者同时认领同一条待领任务，赢家 1 个（契约要求恰好 1 个）
[6] 副作用对账：首次 uncertain（错误可判 ErrOutcomeUnknown）；重试 uncertain 且执行体调用次数不变（1 次）；裁决后 replayed，之后同一键 replayed（结果 "applied"），执行体累计仍为 1 次，不再执行
```

- `go test ./examples/... -count=1`：**reference-runtime / worker-runtime 均 ok**（其余示例无测试文件）；
- `go test ./... -count=1`：**全绿**（含 sqliteadapter / pgstore / cluster / agentloop 等）。

## 二、对照 HOST-SURFACE 六步清单（逐步判定）

| HOST-SURFACE 步 | 示例中的兑现 | 判定 |
|---|---|---|
| **create 创建** | 步 1：宿主 store 落任务拓扑 + `checkpoint.StepDef` 声明依赖——任务拓扑是宿主资产（taskboard 契约明文无创建入口），内核兑现计划判定 | ✅ 对齐（宿主提供拓扑 ✓）；备注：内核默认创建面 `controlplane.SeedRun` 由 runtime 的 pgstore/sqliteadapter `ControlStore` 覆盖，本最小示例不演示控制面 API |
| **claim 领取** | 步 5：两执行者并发认领同一条待领任务 ⇒ **恰好 1 个赢家**（`taskboard.Board`） | ✅ 对齐 |
| **execute 执行** | 步 2：执行体由宿主注入，每步落进度快照（`checkpoint.Committer`） | ✅ 对齐 |
| **commit 提交** | 步 2：每步终态各提交一次（`commitgate.Guard`）；步 6：副作用**至多一次**（`sideeffect.Runner` 幂等键，调用次数恒为 1） | ✅ 对齐 |
| **recover 恢复** | 步 3–4：中断后续跑**不重放**（`checkpoint.BuildPlan` 可信完成集，执行体调用次数为证） | ✅ 对齐 |
| **intervene 介入** | 步 6：结果未知 → 重试被拒 → **裁决** → 同键只重放结果（介入/裁决的最小形态） | ✅ 对齐；备注：控制面 pause/cancel 的 HTTP API 面由 runtime `ControlStore` 覆盖，本示例不演示 |

**import 对齐**：reference-runtime 的 kernel 侧 import = `contract` / `checkpoint` /
`commitgate` / `sideeffect` / `taskboard`——**全部落在 HOST-SURFACE「必须接」分类**，
零「可选/内部件」误用（hostsurfacecheck 的白名单口径在 kernel 仓持续机判）。

## 三、对齐结论与如实备注

1. **六步全部对齐**，且示例自带的三条硬断言（恢复不重放 / 认领单赢家 / 副作用至多
   一次）正是 HOST-SURFACE 各步「kernel 兑现」列承诺的机制行为——**承诺与实跑一致**；
2. **两种装配形态（不是缺口，是边界）**：本示例是 **embedded 形态**（宿主 runner 直驱
   taskboard + checkpoint 计划），六步中的 create/intervene 走宿主 store 与裁决回调；
   `worker`/`backend`/`controlplane` 是**驱动循环部署形态**的默认面（worker-runtime /
   cluster 形态使用）。HOST-SURFACE 的「必须接」指默认机制面，两种形态各自成链——
   该边界已补进 HOST-SURFACE §三判据边界；
3. **证据边界**：本证据证的是「示例真跑 + 清单对齐」，不替代 kernel 仓各门禁
   （invariantcheck 等）对机制本身的持续机判。

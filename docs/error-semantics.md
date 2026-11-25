# 全仓错误哨兵语义总表

> 写给**要在自己产品里判定内核错误的宿主开发者**：哪个包有哪些哨兵、
> 什么条件下触发、拿到之后该怎么处置——一张表判完，不再逐包翻注释。
>
> 本表与代码由 `make apilifecheck` 机器校验（`cmd/apilifecheck`）：
> 代码里新增哨兵/导出错误类型而未登记 ⇒ 红；表里出现代码中已不存在的
> 幽灵条目 ⇒ 红。**先登记再合入**，不是发版后补文档。
> 清单小节名必须与 `api-lifecycle.yaml` 的 `pkg` 完全一致（含 `internal/`
> 前缀这类路径形态），段落挂错包名同样红。
>
> **本表只登记本仓的包**：已上移上游的包（`netguard` → `github.com/oagentkit/guardx/netguard`、
> `ticket` → `github.com/oagentkit/guardx/ticket`、`cmdpolicy` → `github.com/oagentkit/tools/cmdpolicy`；
> v0.73.0 起本仓不再有副本）其哨兵与结构化错误类型**去上游各自的包注释查**——
> 本表不留幽灵行（同门禁双向判定：代码里没有的条目，表里写了即红）。

## 判定方式

- 本仓全仓单套 errors 包：`github.com/oekit/pkg/errors`（家规
  `harness-single-errors-package` 门禁强制）。哨兵用它 `New`，包装用它
  `Wrap`（输出 `msg + ": " + err`，与 `%w` 同型），判定用同包
  `errors.Is` / `errors.As`，解到根用 `Cause`。
- **未导出哨兵也参与判定**：包内私有哨兵会经包装链到达调用方，
  `errors.Is` 判包装链不需要符号导出。表中标注「包内」的那些，
  宿主用字面哨兵判不了，但能通过导出包装函数或结构化类型间接判定
  （ptc 的三个预算哨兵经 `Result` 分类暴露，egress/gatecheck 的经
  返回错误文案与其所属档位判定暴露）。
- 处置三档口径（下表「调用方处置」列的动词统一按这个语义用）：
  - **重试**：可恢复，退避后重试同一动作即可；
  - **对账**：结论未知或已有赢家，先裁决再动作，不许盲目重做；
  - **修声明**：装配/配置/编程缺陷，重试无意义，改调用方或配置。

## 哨兵总表（按包）

### backend

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotSatisfied` | 要求未被该后端满足（隔离不够强 / 出站档低于要求 / 自报兜不住预算） | 换后端或降要求；报错文案带两侧值，重试无意义 |
| `ErrNoBackend` | 登记簿里没有能满足要求的后端 | **绝不降级**：补登记能满足的后端，或改要求；重试无意义 |
| `ErrBadSpec` | 要求自身不合法（越界档位 / 负预算） | 修声明：改调用方参数 |
| `ErrSilent` | 后端未声明该能力面（沉默按最弱处理） | 补声明或换后端——「没声明」与「声明了但不够强」修法不同，两类报错分得开 |
| `ErrDuplicateID` | 同一 ID 被登记两次 | 修声明：登记簿去重 |
| `ErrBadBackend` | 登记的后端不合法（nil / 空 ID） | 修声明 |
| `ErrUnknownProgram` | 兜底后端没有登记这个名字的处理函数 | 修声明：先 `Inline.Register` 再执行；不静默跑空 |

### checkpoint

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotFound` | 该 runID 无进度快照（**首次执行的正常情形**） | 按「没有快照」走全量首跑；宿主实现 `Load` 无记录时**必须**返回它，不得返回 `(零值, nil)`——那会被误判成「从没跑过」而重复副作用 |
| `ErrStaleSeq` | 快照序号未严格递增，被单调闸拒绝 | 放弃本次写入，按 Committer 流程重新抢新序号（对账：以库内最新为准） |
| `ErrStoreMissing` | 未装配存储就要提交快照 | 修声明：装配存储（显式失败，不静默成功） |
| `ErrStaleFence` | 栅栏条件提交（`SaveIfNewerFenced` / `CommitFenced`）发现出示的会话代次已被更新（任务已易主） | 停手：本执行者已失去持有资格，进度不得再写；按重新认领后的新会话提交，或放弃本次执行 |
| `ErrFenceUnsupported` | 存储不实现栅栏条件提交，调用方仍要求带代次提交（不支持时不降级、显式失败） | 换实现栅栏条件提交的存储；或改走普通 `SaveIfNewer` 并自行接受「无栅栏则旧持有者迟到写可落库」的弱口径（显式选择，不得静默） |

### commitgate

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrAlreadyCommitted` | 该环节终态已被提交过（两条分支都赢了） | 对账：放弃本次提交；用 `As` 取 `AlreadyCommittedError` 读先前提交者与状态，判定谁是赢家 |
| `ErrEmptyRef` | 引用缺字段（空 RunID 或空 Step） | 修声明：补全引用后重试 |

### contract

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrSessionDeadline` | 会话级墙钟到点（`deadline.WithCause` 装饰的轮 ctx 携带的 cause；`finalize` 用同一值做收尾路由） | 收敛 timed_out 终态（deny-by-default：超时的产出不可信）；**必须先于停机臂判定**（次序纪律，见 `deadline.Route`/`finalize.Route`）。与 `drain.ErrDeadline` 是两个 Cause，不要互相代入。deadline/finalize 都是本哨兵的别名（issue #333） |
| `ErrUserCancelled` | 用户经取消 API 触发的协作中止（宿主 `context.WithCancelCause(contract.ErrUserCancelled)` 携带；`deadline.Route` 缺省即认） | 收敛 cancelled 终态；与裸 ctx 取消（进程停机，不落终态）及 `steer.ErrSteered`（接管，回 created）三分可辨。finalize 的 `ErrUserCancelled` 是本哨兵的别名（issue #355） |

### controlplane

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotFound` | 查询/介入的目标不存在（任务或运行） | 修声明：核对目标 id（可能拼错或已被清理）；对不存在的目标重试无意义 |
| `ErrInvalidRequest` | 介入请求不完整或结论词表不合格（介入者/依据为空、裁决结论不在词表内）——无归责不介入 | 修声明：补全介入者与依据后重试；缺归责的介入不得放行 |
| `ErrNoAuditor` | 留痕面未装配，介入被拒（fail-closed——没有留痕的介入无法回答「谁依据什么改了状态」） | 修声明：装配 Auditor 再发介入；不得绕过留痕直接改运行状态 |
| `ErrStaleRun` | 运行代次已前进，介入基于过期视图（Terminate 的乐观并发闸） | 对账：重查视图拿新代次再介入；按过期视图硬介入会与在场持有者打架 |
| `ErrTerminalRun` | 运行已是终态，无需也无法终止（介入重试撞上它说明第一次介入已生效） | 幂等收手：视为首次介入已成功，不重试 |
| `ErrAlreadyExists` | Seed* 重复登记（run / 任务 / 效果键已存在）——介入类动作不会报它，介入对已存在目标的语义是执行 | 修声明：登记前先查重；重复登记重试无意义 |
| `ErrInvalidAudit` | 留痕条目不完整（谁/动作/目标/依据缺一不可，或动作不在词表内） | 修声明：补齐留痕字段；留痕面拒收不完整的条目，不得静默补空 |
| `ErrNoStore` | 未装配 Store 就构造持久化 Plane（`NewStorePlane(nil, ...)`） | 修声明：装配 Store（任务/运行/效果键的持久化后端）；不得静默退化成进程内状态——多副本下两份真相 |
| `ErrStoreConflict` | Store 的版本锚不匹配：快照之后目标被另一个副本改过 | **重试**：重读快照、重判后再提交；StorePlane 已内部重试，穷尽后仍见它说明并发介入持续冲突 |
| `ErrUnknownRunStatus` | 运行状态不在已知词表内（可能是宿主自定义终态，如 `aborted`）；无法判定是否终态，Terminate 拒绝改写 | 修声明/对账：由宿主显式判定该状态的含义；内核不把「读不懂」当成「还活着」（issue #334） |
| `ErrAuditorPanic` | 宿主留痕实现 panic（已被内核隔离，状态一字不动） | 修实现：Auditor.Append 不得 panic；本次介入按失败处置，可重试 |
| `ErrAuthorityMismatch` | 权威控制面的 Store 与任务权威面来源标识不一致，或缺任务权威面（装配期拒绝） | 修装配：强制释放必须接到运行时的同一份 taskboard.Board（issue #268） |
| `ErrNoEffectAuthority` | 权威控制面未装配副作用权威面，裁决无处落地（fail-closed） | 修装配：注入落到 sideeffect.Store 同一份状态的 EffectAuthority |

### coord

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrTxnConflict` | CAS 循环重试到上限仍未提交（高争用，或后端原子性可疑） | **重试**：退避后重试整个操作；持续出现则怀疑后端的 Txn 没有实现原子性 |
| `ErrMalformedRecord` | 键下的记录无法解码（内容不是适配器的编码） | 修声明：键前缀撞了或后端被别的写者占用，换键空间；放行会把别人的字节当自己的状态判 |
| `ErrNoTimeAuthority` | 装配期拒绝：后端既不实现 `ClockSource`（到期由存储时钟算）也未声明 `SingleMachine`（仅单机）——适配器不静默退化成节点本地时钟（issue #194） | 修装配：让后端实现 ClockSource 报存储侧时间（PG/Redis 形态），或显式声明仅单机；不得为绕过装配检查注入进程本地时钟 |

### cost

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrEmptyKind` | 成本事件缺计量种类（Kind 为空——没有种类的账无从聚合） | 修声明：报数侧补上计量种类；拒收留痕，不静默入账 |
| `ErrNegativeAmount` | 成本事件量为负（记账 bug） | 修声明：查报数侧的记账 bug；拒收留痕，不静默归零、不许反向抹账 |
| `ErrGroupKeyCollision` | 聚合分组键碰撞：维度取值序列不同却编出同一个键（长度前缀编码下不应发生，出现即编码退化） | 修声明：这是内核编码缺陷信号，保留事件源并上报；**不得静默并组**（跨租户串账） |

### deadline

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNoStore` | 未注入锚点持久化接缝（`Store`）就要读/写 shared 锚点 | 修声明：shared 模式必须注入 Store——静默跳过会让总时限被续跑轮重置，且没有任何信号 |

> `ErrSessionDeadline` 的身份来自 `contract`（issue #333）：本包变量是别名，与
> 收尾侧 `finalize` 共享同一个 error 值（`errors.Is` 一条链）。`Route` 是收尾
> 路由的**唯一判据**（接管/超时/用户取消/停机/未取消），`finalize.Route` 只做
> `Outcome`→`Action` 投影（issue #355）。

### delegate

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrUnsupported` | 被选中的执行方不提供所需能力 | 换执行方（最小能力原则选出的这一个接不了） |
| `ErrNoRunner` | 没有任何 offer 能满足请求 | 放弃或扩 offer 集合；重试无意义 |

### drain

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrDeadline` | 排水预算耗尽，轮次被强制中断 | 不是普通停机：宿主按此区分留痕来源，对被中断的轮次按「未收口」对账 |

### egress

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `errEnvCannotEnforce`（包内） | 禁网档位无法靠环境变量兑现（必须由带能力声明的后端强制） | 修声明：区分「档位声明写错」与「执行环境缺能力」，后者换带能力声明的后端 |

### eval

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNoChecks` | 评估清单为空（零判项不给「全过」的假绿） | 修声明：显式列出判项；「没有检查」与「检查通过」必须分得开 |
| `ErrBadSpec` | 评估清单不自洽（判项不在词表内 / 重复列出 / 期望清单缺失） | 修声明 |
| `ErrBadRecord` | 录制文件不合法（编码失败 / 行解码失败） | 修数据：按行号定位坏行 |

### finalize

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrUserCancelled` | 用户经取消 API 触发协作中止的 Cause（`Route` 据此落 cancelled 终态） | 按取消收敛；与停机（裸 ctx 取消）/ 接管（`steer.ErrSteered`）三分可辨——宿主用 `WithCancelCause` 携带 |

> `ErrSessionDeadline` 的身份来自 `contract`（issue #333），`ErrUserCancelled`
> 的身份也来自 `contract`（issue #355）：本包两个变量都是别名，与 `deadline`
> 收尾路由共享同一个 error 值；裸 deadline 仍归停机臂、不落终态。

### gatecheck

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `errNoInner`（包内） | 受管后端没有内层真后端 | 修声明：装配内层执行后端 |

### guardset

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrEmptySet` | 构造空守卫集合 | 修声明：没人看着的调用不该看起来能用 |
| `ErrUnnamedGuard` | 守卫缺名字（留痕无法回答「哪一环拒的」） | 修声明：给守卫命名 |
| `ErrDuplicateGuard` | 守卫重名（同一答案两个候选） | 修声明：去重 |
| `ErrUnassembled` | 未经构造的 Set 被拿来求值（运行期 fail-closed） | 修声明：先构造再求值 |
| `ErrUnknownGuard` | 撤销一个不存在的守卫 | 修声明：不静默忽略，核对宿主的守卫账本 |
| `ErrNilMutate` | 换份的改写函数为 nil | 修声明：静默无动作比报错更难查 |
| `ErrNilSubscriber` | 换份订阅回调为 nil | 修声明：装配订阅回调 |

### handoff

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrUnknownKey` | 交接单不存在 | 修声明：先 `Offer` 再动作；首交前的查询走它属正常态 |
| `ErrTaken` | 交接单已被领取（并发 Take 的输家 / 已领取后 Cancel） | 输家属正常结果：放弃本次领取；收回方按「已被接手」改走对账 |
| `ErrExpired` | 交接单已超时限（迟到领取 / 迟到回执） | 重新交接 = 新一代；先对账（工作可能已被做了或没人做） |
| `ErrNotAddressee` | 领取者不是指定接收方 | 修声明：只有 To 能领取 |
| `ErrNotSource` | 收回者不是交接发起方 | 修声明：收回权只在 From |
| `ErrLeaseLost` | 领取令牌不匹配（凭令牌认人，不凭内容） | 对账：责任期已易主或已收口，迟到动作一律放弃 |
| `ErrAlreadySettled` | 交接单已是终态 | 放弃本次动作；重新交接开新一代 |
| `ErrUnknownState` | 交接单状态不在词表内（offered / accepted / closed / expired / withdrawn） | 修数据：查状态来源（写库 bug / 跨版本 schema / 截断写）；与体检面 `CodeUnknownState` 同向——写路径（Take / Reply / Cancel / Offer）与 `Ledger.Save` 一律拒绝，不得铸造令牌或改写状态（issue #359） |
| `ErrNoStoreClock` | 构造分布式交接台时 Ledger 未实现 `ClockedLedger`（无存储时钟） | 修装配：实现存储时钟或改用单机 `NewDesk`；跨副本不得回退进程本地时钟（issue #270） |
| `ErrConflict` | 持久化记录的 Revision 已被并发写者推进（条件写失败，`Ledger.Save`） | 对账：重新 `Load` 并按最新状态重新判定；不得盲目重放同一次写入（并发领取的输家要在最新状态下读到 accepted，才能返回 `ErrTaken`） |
| `ErrStaleEpoch` | 出示的所有权代次不是当前代（旧代 `Save` / `TakeAt` / Delete 重建后用旧代） | 放弃本次动作，按最新代次重新挂出/领取；重试同一次旧代写入无意义（防 ABA 的判据） |

### ownership

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrUnknownDomain` | 任务域没有权威映射（未配置 / 拼错 / 已移除） | 修声明：先登记区域权威映射；提交路径拿到它一律拒绝，不得回退成「本地默认权威」 |
| `ErrNotOwner` | 出示的区域不是该任务域当前权威区域（代次相同） | 对账：按 `Owner` / `Mapping` 查当前权威；不是重试能解决的 |
| `ErrStaleEpoch` | 提交代次低于当前代次（旧代提交 / 回退） | 对账：本区域已不是权威，丢弃本地视图；不得凭旧代重试（防 ABA 的判据） |
| `ErrEpochUnconfirmed` | 提交代次高于当前代次（候选 / 未来代次未经确认） | 对账：只有确认成功后代次才可用；等确认回执，不得自行推高代次 |
| `ErrTokenMismatch` | 授权令牌 / 确认回执令牌不匹配（凭令牌认人） | 对账：重新取得当前授权；旧令牌永不复活，重试同一令牌无意义 |
| `ErrNoTransfer` | 转移提案不存在或已被新提案取代 | 重新 `BeginTransfer` 取新提案与回执令牌；旧回执作废，不得凭令牌猜测复活 |
| `ErrNotTarget` | 确认方不是提案的目标区域 | 修声明：确认只归目标区域；核对提案的 `To` |
| `ErrBadTransfer` | 转移请求不合法（空目标 / 目标等于当前权威区域） | 修声明：改正目标区域后再发起 |
| `ErrSingleRegion` | 单区域本地权威不支持跨区域转移 | 修装配：多区域部署接共享权威；单区域无需转移 |
| `ErrBadMapping` | 区域权威映射不合法（空映射 / 空任务域 / 空区域） | 修声明：装配期修正映射；不得以空映射启动（否则配置错误与业务错误分不开） |

### protocol

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotReport` | 输出不是统一报告 JSON | 降级读 markdown 主体，**不阻塞**（容错解析的正常分支） |
| `ErrViolation` | 报告未通过 schema 白名单校验 | **必须拦截**；违规项经 `errors.Join` 附带，逐项处置 |

### ptc

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `errDeadline`（包内，经 `Result` 分类暴露） | 程序执行超过墙钟预算 | 放弃本轮程序执行，走逐轮 ReAct 兜底；对已发生的绑定调用按幂等键对账 |
| `errBudgetCall`（包内，同上） | 绑定调用数超预算 | 同 `errDeadline` |
| `errBudgetByte`（包内，同上） | 绑定输出字节超预算 | 同 `errDeadline`（并缩小采集口径） |
| `ErrInvalidSignature` | 绑定调用形态串非法（装配期缺陷，非程序问题） | 修声明：修正装配期签名 |
| `ErrMemoryLimitUnsupported` | 本环境无法施加内存上限 | 换执行环境或去掉该要求；**与执行域异常终止是两类失败**，不得合并处置 |

### scheduling

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrInvalidConfig` | 权重非正 / 配额上限为负 / 背压阈值为负 / 占用释放量非正 / 公平选择器空集 | 修声明：装配期参数错误，重试无意义 |
| `ErrQuotaExhausted` | 配额余额不足以整单占用 | **重试**：等别的调用方 Release 后同一调用可能成功；当永久失败处理会让动作永远丢失 |
| `ErrOverRelease` | 释放/回收凭据未知或已归还（重复释放、已回收后的迟到释放） | 修调用点：重复释放或释放了别人的额度；钳位会把配额账本变成负数 |
| `ErrLeaseNotExpired` | 配额预占尚未到期就要回收 | 等待到期再回收：Worker 可能还在真跑（issue #267） |
| `ErrLeaseStillHeld` | 关联任务的租约仍有效，回收被拒 | 放弃回收：旧任务可能还在真跑；确认任务租约失效后再走 Reclaim |
| `ErrNoLeaseOracle` | 预占带任务归属但未注入任务租约判定面（TaskLeaseOracle） | 修装配：注入任务租约存活判定；判不了就不回收（fail-closed） |

### invocation

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotFound` | 取一条不存在的账目（会话/调用 ID 对不上） | 修声明：核对会话与调用 ID；不要当成「已完成」放行（读不懂的账目不是空账目） |
| `ErrIdentityConflict` | 同一 (会话, 调用) 的工具名或参数摘要与首次登记不一致 | 修声明：那是**另一个动作**（同款口径见 sideeffect.ErrActionConflict）；不要改参数硬塞，按新调用落账 |
| `ErrIllegalTransition` | 状态转移不合法（跳态 / 从终态再动 / 目标状态不在词表内） | 修声明：按账本状态机推进（planned → executing → applied → result_recorded）；终态不可再动 |

### sideeffect

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrInvalidAction` | 动作定义不完整（缺 Key 或 Resource） | 修声明：补全动作定义 |
| `ErrNoStore` | 未装配存储就要执行副作用 | 修声明：装配存储（显式失败，不静默成功） |
| `ErrUnknownOutcome` | 存储侧返回了未定义的结论 | 修声明：存储实现违约（必须显式处理「不知道」，不许下游猜默认值） |
| `ErrLeaseLost` | 续期时发现闸已易主（持有资格失效） | **事前收手**：本次动作不得继续；与 `ErrFenceStale` 的区别是它发生在施加之前，还能悬崖勒马 |
| `ErrFenceStale` | 栅栏令牌过期，或本次尝试已被存储侧转入未知态——这把钥匙不再有效 | 下游拒收迟到写入是对的；持有者停止使用旧租约，重新走接管/重试流程（对账） |
| `ErrOutcomeUnknown` | 该 Key 处于「结果未知」态 | **对账**：先裁决（确认已生效/未生效）才能再动这个 Key；默认挑「没生效」＝默认重复施加 |
| `ErrNotUncertain` | 对不处于未知态的 Key 发起裁决 | 按裁决对象的实际状态处置：可能已正常完成，也可能压根没失败过；排查调用时序 |
| `ErrNotHolder` | 持有者身份不匹配——出示的凭据不是本次授予铸造的那份（令牌经下游流转后被人冒用） | 放弃本次操作并排查凭据流转链；与 `ErrFenceStale` 的分工：那个是「这一代已过去」，这个是「代或许还在，出示者不对」 |
| `ErrResourceMismatch` | 租约与动作的资源不一致（A 资源的租约被拿去做 B 资源的操作） | 修声明：Complete/Renew/MarkUncertain 的 act 与 lease 必须同资源，两个真相等于没有真相 |
| `ErrReconcileTaken` | 该 Key 已有未过期的对账占用（同一时刻至多一个对账者） | 等待或放弃：占用过期后可重新认领（BeginReconcile 可重入）；不要并发抢对账 |
| `ErrUnsafeNotRetryable` | 动作声明为不可重试（Unsafe）——对账判「未生效」也不解除阻塞 | 二选一：承认查询不可信重新对账；或确认结论可靠并接受风险后，带 `ForceRetryable=true` **显式**重新裁决 |
| `ErrActionConflict` | 同一 Key 的动作身份（Resource/Payload/RetrySafety）与首次登记不一致——判定优先于「已完成 → 重放」 | 不可重试：要么用与登记一致的身份，要么承认 Key 撞号（两条逻辑动作共用一个 Key），换 Key |
| `ErrReconcileLost` | 出示的对账占用凭据不是当前有效的一份（已过期 / 已被接管 / 从未认领） | 迟到的对账者：放弃本次裁决；要提交就重新 BeginReconcile 认领后再查（旧查询窗口跨越了状态变化，不可信） |
| `ErrInvalidTTL` | BeginReconcile 的 ttl ≤ 0（出生即过期的认领 = 没有占用） | 修正入参：给正时长的认领窗口（对账查询的预估耗时 + 余量） |

### sidetest

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `errSinkBoom`（包内） | 栅栏契约套件 `write_error_no_partial_write` 场景注入的写入体故障——断言「写入体报错 ⇒ 预占与部分写一起回滚」 | 套件内部判据用：断言错误经 `WriteFenced` 原样透出且零落地；宿主无需处置 |

### statemachine

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrIllegalTransition` | 两个状态都在词表内，但该转移不在合法转移表里（业务态：跳过阶段/终态复活） | 放弃本次迁移，按状态机流程重排；重试同一迁移不会变合法 |
| `ErrUnknownStatus` | `from` 或 `to` 不在状态词表内（读不懂，不是迁移写错） | 修数据/版本：查写库口径、跨版本 schema 或截断写；不得按「非法迁移」方向排查 |

### steer

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrSteered` | 用户运行中注入新输入的协作中止 Cause（`steer.Signal` 发出；`finalize.Route` 据此路由到接管臂） | 交 `steer.Run` 收敛：队列非空回 created 续跑、队列空按失败收敛不开空轮；**不落 cancelled 终态**——与 `finalize.ErrUserCancelled` 分流，勿混 |

### taskboard

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotFound` | 任务不存在 | 修声明：核对任务 id（可能拼写错或已被清理） |
| `ErrTaken` | 任务已被他人活着持有 | 不重试：走协调（或等租约过期后接管） |
| `ErrBlocked` | 依赖未完成 | 等：依赖完成后再来认领（可稍后重试） |
| `ErrTerminal` | 任务已是终态 | 放弃：读既有结论，不重开 |
| `ErrLeaseLost` | 令牌不匹配（租约已易主或被释放过），或**租约已过期**（`Complete` 侧，issue #497：资格在到期时刻终止） | 对账：本次操作作废，重新 `Claim`（过期 ⇒ 任务已可被接管，先看它归了谁） |
| `ErrInvalidBatch` | 批量领取数量非正（`ClaimReady` 的 n <= 0） | 修声明：给正整数；零/负数不是「暂时没有活」，不得静默当空结果 |
| `ErrUnknownState` | 任务状态不在词表内（open/claimed/done/failed），写路径拒绝认领 | 修数据：查状态来源（写库 bug / 跨版本 schema / 截断写）；与体检面 `CodeUnknownState` 同向，不得当成可领任务 |

### tenant

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNoContext` | 写路径判据在未注入租户上下文时被调用（fail-closed，配额判定同） | 修声明：先 `WithContext` 注入上下文再走写路径；读路径用 `From`（宽容）不报错 |
| `ErrUnauthorized` | 授权不足（required 不在注入上下文的 Scopes 内） | 放弃本次动作并换有权限的上下文；重试不会让凭据多出权限 |
| `ErrQuotaExceeded` | 配额超限（Enforce 判定出口，明细随包装链消息携带） | 等配额释放或提升限额后重试；结构性超限按拒绝处理，不绕过 Enforce |

### tooldisclose

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrNotLoaded` | 工具在目录内，但 schema 尚未取回 | **重试**：门禁已把它作为副作用激活，重试即成（两哨兵重试性相反，混淆会把策略否定变成无穷重试） |
| `ErrNotInCatalog` | 工具不在目录内，从未可用 | **放弃**：不存在的东西不会因重试出现，任何退避策略都应停手 |
| `ErrSchemaNotDerivable` | 该工具的 schema 派不出合法的位置调用形态（含工具名为空） | 修声明：修工具 schema/名字 |

### metrics

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrUnregisteredMetric` | 埋点用了未在 `Specs` 登记表登记的指标名（`Validate` 判，`Mem` 严格档计数；`SpecSet.Validate` 对宿主自有登记表同一判据） | 修声明：先在 `metrics.go` 登记指标（名称/类型/维度/基数上界）+ ACCESS-POINTS.md 加接入点，再埋点——未登记的数是看板口径外的暗数 |
| `ErrInvalidSpec` | 经 `SpecSet.Register` / `Merge` 注册的指标登记项自身不自洽（名称/类型/单位/说明/维度取值域/基数上界依据缺一不可） | 修声明：按报错补全登记项；拒收的登记项不会进入表，也不会影响已有登记 |
| `ErrDuplicateMetric` | `SpecSet.Register` / `Merge` 遇到同名指标（看板与查询按名字对应，重名等于两个口径） | 修声明：改名或去掉重复登记；拒绝「后者覆盖前者」的静默替换 |

### worker

| 哨兵 | 触发条件 | 调用方处置 |
|---|---|---|
| `ErrInvalidConfig` | 注册声明缺 ID / TTL 非正 / 心跳间隔不短于 TTL / Loop 缺组件 / 续期失败阈值非负 | 修声明：装配期参数错误，重试无意义 |
| `ErrNotRegistered` | 注册已失效：令牌不匹配、注册被顶替、或有效期已过 | 停止领取、收刀退出（凭据由注册表铸造，丢了就是丢了）；重启走重新注册并按新令牌接续 |
| `ErrLeaseUnconfirmed` | 在途任务的租约连续续期失败跨过一个 TTL（驱动无法确认持有资格，已取消在途执行并停止领取） | 查任务板/存储健康后重跑 `Recover` 接管：与 `ErrNotRegistered` 分列——那查注册表/顶替，这查任务板/存储；租约失去资格期间的副作用归属需对账 |

## 结构化错误类型（errors.As 判定）

> 哨兵判「是什么错」，下面的类型判「错的现场长什么样」——调用方要读
> 结构化字段（谁提交的、哪一跳被拒）时用 `As`。

| 符号 | 含义 | 调用方处置 |
|---|---|---|
| `commitgate.AlreadyCommittedError` | 终态重复提交的现场：先前提交者（`Prev`）、`PrevSeq`、双方状态与分支 | `As` 后读字段对账谁是赢家；`Unwrap` 回 `ErrAlreadyCommitted` 可统一 Is 判定 |
| `ptc.ExecutionAborted` | 子进程执行域异常终止：退出状态 `Exit` 与**已脱敏**的 `Stderr` 尾部 | `As` 后读 `Stderr` 判死因（内核兜底 vs 自身 fatal）；与 `ErrMemoryLimitUnsupported` 分开处置 |
| `tooldisclose.GateError` | 门禁拒绝的结构化形态：工具名、拒绝类别、是否可重试、是否已激活 | `As` 后按字段分派处置；模型侧指引走链上 `Reason`，不要自己拼文案 |

## 维护纪律

1. 新增哨兵/导出错误类型 ⇒ 同一提交里在本表登记（`make apilifecheck` 拦截漏登记）；
2. 删除哨兵 ⇒ 同步删行，不留幽灵条目（同门禁双向判定）；
3. 「触发条件」「调用方处置」两列写**事实与动作**，不写设计推导——
   推导留在各包注释与本仓 ADR 里；
4. 各包哨兵语义的**权威定义**仍是包内注释；本表是导航与总览，
   两处不一致时以门禁红为信号先对齐再改。

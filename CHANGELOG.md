# goagent-kernel CHANGELOG

> 本文件由 `matrix-tools/scripts/gen_changelogs.py` 从 git 历史生成，按 tag 切段、取每段内的 conventional commit 渲染。

## [v0.69.0] — 2026-11-22

### chore：toolname 上移 oekit/pkg（v0.58.0，PR #472）

边界审计第二批上移：toolname（工具名规范名/净化/一致性校验，纯 stdlib 零依赖，生产 101 行）按「机制与宿主无关 → 归上游基础库」上移至 oekit/pkg（oekit/pkg#578，v0.58.0，API 逐字未变）。

**破坏性变更（minor 理由）**：github.com/yi-nology/goagent-kernel/toolname 删除——外部消费方改指 github.com/oekit/pkg/toolname。仓内消费方 ptc（5 文件）与 tooldisclose（2 文件）import 已改指，零行为变化。依赖地板 oekit/pkg v0.57.0 → v0.58.0。

**同批对账结论**：netguard 维持现状（消费方 tooldisclose/ptc 在编排出站链；与上游 oekit/pkg/urlguard 的分工——IP 策略单源 egress、域名后缀层本仓为超集——已写在包文档）；watchdog 深耦合 contract/eventbus/reconcile/trajectory，属编排判据本体，留 kernel；ticket 留待下批（需先理清消费面）。

## [v0.68.0] — 2026-11-22

### chore：三个演示迁往 goagent-runtime，examples/ 清空（PR #471）

边界迁移第三批：kernel 的 examples/ 目录**清空**——编排演示与容器后端参考实现的受众是装配层与宿主，住 runtime 仓才是它们的家（runtime 侧已收编，随该仓下一版发）。

**破坏性变更（minor 理由）**：examples/reference-runtime、examples/worker-runtime、examples/containerbackend 删除（生产 -3842 行 / 19 文件、测试 -16 文件）。外部消费方改指 github.com/yi-nology/goagent-runtime 的 examples/reference-runtime、examples/worker-runtime、containerbackend（API 逐字未变）。

**faultmatrix 行十四（真实 NetworkPolicy 验证）随 containerbackend 迁出**：声明面格在 runtime 改名 TestNetworkPolicyDeclarationFailClosed（判据逐字随迁，networkpolicy_matrix_test.go），真集群格由该仓 k8sjob_realcluster_test.go 继续守；矩阵自检的「删格必红」集合（newFaultClasses）同步摘除 ClassNetworkPolicy——这是迁移不是退化，判据覆盖由 runtime 兜，行内注释写明去向。

配套：taskboard 指纹判据 2→1（kernel 只剩 coord 一个 Board.Claim 实现）；接缝基线刷新（containerbackend 的 K8sCNICapability 接缝随包迁出）；选型面两处能力落点改指 runtime 仓；policy / api-lifecycle / 哨兵总表 / DEPLOY-MATRIX / backend 包注释全量同步；底座重算为 67,583 行 / 361 文件 / 463 测试文件。

**判据不动**：backend 契约面、faultmatrix 矩阵本体、worker 生命周期契约留在 kernel 且逐字未变。

## [v0.67.0] — 2026-11-22

### chore：PG/SQLite 参考实现迁往 goagent-runtime，撤驱动破例（PR #470）

边界迁移收尾（runtime v0.2.0 已收编）：存储参考实现归装配层，「内核不实现存储」从**门禁破例**变成**结构事实**——主 module 依赖图不再含任何数据库驱动（pgx/modernc 撤除，go.mod 里的长段破例注释换成一段迁移说明）。

**破坏性变更（minor 理由）**：`examples/pgstore` 与 `examples/sqliteadapter` 删除（生产 -7969 行 / 38 文件、测试 -49 文件）。外部若 import，改指 `github.com/yi-nology/goagent-runtime/pgstore` / `.../sqliteadapter`（API 逐字未变，判据套件同源）。CI 的 integration job 与 `make integration` 一并迁 runtime 仓。

**门禁的跨仓降级（判据语义不变，判不了的明说判不了）**：

- `deploycheck`：contract 证据支持 `https://` 跨仓引用——形态检查通过后放行，外部文件的存在性由 runtime 仓自己的门禁兜；
- `storagecheck`：`code_root` / `facade.package` 支持 `external:` 前缀——门面代码派生（F1）跳过由该仓自判，F3 两侧同款对位仍判（声明面↔声明面），F4 门面覆盖按 `/blob/main/` 后的仓内包路径对账，能力落点的 source 位置检查对外部引用豁免；
- DEPLOY-MATRIX / STORAGE-SELECTION 的全部落点 ref 改指 runtime 仓链接；#447 僵尸例外判据当场抓出 pgstore 留下的 wallclock 例外。

配套：`taskboard` 指纹判据 4→2（PG/SQLite 的 Board.Claim 实现随仓走）；覆盖率两张豁免表摘 pgstore/pgsmoke 条目；生命周期登记 60→57；底座重算为 71,421 行 / 380 文件 / 479 测试文件。

**下游要做什么**：import 两个参考实现的改指 runtime 仓（v0.2.0）；其余零动作——契约套件（sidetest/boardtest）留在 kernel 且逐字未变。

## [v0.66.1] — 2026-11-22

### fix(ptc)：stress 测试的 PID 文件隔离到测试专属 TMPDIR（#465 根因）

**根因不是墙钟尺子**（#465 开单时的猜测，**猜错了**），是**共享路径**：
`readChildPID` 与子进程源码都用 `os.TempDir()/gak_ptc_child_pid`——固定路径，
而 `os.TempDir` 是**所有进程共享**的。两个测试进程并发时（本地多 worktree /
并行会话同时跑测试；全量 check 下的「负载偶发」多半就是这么来的）会互相读写：

- 后启动的 `readChildPID` **删掉对方写的文件**、读到**对方子进程的 PID** ⇒
  `SIGKILL` 杀掉的是别人的子进程，而自己的子进程没人杀 ⇒ 父侧 20s 超时「**父侧挂死**」；
- 反过来读到对方**已退出**的 PID ⇒ 「**应存活（还没轮到杀）**」。

**两种失败形态、两次实录同源**（2026-11-21 父侧挂死 31.69s / 2026-11-22 应存活却
已死 0.87s）。

**复现不靠负载，靠并发**：

| 实验 | 结果 | 结论 |
|---|---|---|
| 4 路 CPU 负载 + `-count=10` | 全绿 | **CPU 不是触发条件** |
| 两个进程同时跑同一测试 | **A=0 B=1 必失败** | **并发才是** |

⇒ 此前单跑/负载下绿是**运气**（没有并发就不冲突）——这正是「偶发」的来源，
也是它两次形态不同的原因（读到的别人的 PID 处于不同生命周期）。

**修法**：测试开头 `t.Setenv("TMPDIR", t.TempDir())` ⇒ PID 文件落本测试专属目录。
**子进程源码不用改**——它写 `filepath.Join(os.TempDir(), …)` 而 `TMPDIR` 在
`procx.ChildEnv` 的基础白名单里 ⇒ 继承本测试设下的目录。

**反向验证**：修复后两进程并发 **A=0 B=0**（此前 B 必失败）；**坏实现**（去掉
`t.Setenv`）⇒ 并发回到 **A=1 失败** ⇒ 这条隔离是测试依赖的，不是摆设。

### 下游要做什么

- **零影响**：只改**测试文件内容**（未增删文件、未动生产代码）⇒ 内核包公开契约面
  **逐字未变**，依赖地板未动 ⇒ **无需 `go mod tidy`**。
- README 工程底座三数**不变**（`make docsync` 判相等）。
- 若你在**多进程并发跑本仓测试**：本版起 stress 测试不再互踩（PID 文件已隔离）。

## [v0.66.0] — 2026-11-22

### chore：pii 与 internal/subhub 上移 oekit/pkg（v0.57.0，PR #464）

边界审计结论：pii（PII 价值模式检测打码，生产 765 行 + 测试 3600+ 行）是**文本安全原语、不认识编排**，且在 kernel **零消费方**；internal/subhub（快照式订阅表）是 pii/guardset/eventbus/netguard 共用的通用并发数据结构。两者按「机制与宿主无关 → 归上游基础库」的边界纪律上移至 oekit/pkg（oekit/pkg#571，v0.57.0）。

**破坏性变更（minor 版理由）**：github.com/yi-nology/goagent-kernel/pii 与 internal/subhub 删除，Stable 级的 pii 同步退役——外部若 import 这两个包，改指 github.com/oekit/pkg/pii 与 github.com/oekit/pkg/subhub（API 逐字未变）。仓内零消费方（pii）与四个消费方的 import 重指（eventbus/guardset/netguard/apilifecheck，import path 唯一改动，零行为变化）已在本版完成。

配套清理：policy 摘两模块登记 / scan_roots / requires / wrappers 死引用，harness-test-no-direct-global-setter 规则随 pii 退役（check_kind 判定面留在 archcheck 可供宿主复用）；docs/error-semantics.md 哨兵总表 pii 小节随包摘除；api-lifecycle 登记 62→60；README/USAGE 口径重算（61→59 module id、53→52 包）。依赖地板 oekit/pkg v0.52.0 → v0.57.0（跨 5 版：memorypipe 的 slicex.Contains 改接 stdlib slices.Contains——上游 v0.57.0 已 deprecate 该符号）。

## [v0.65.1] — 2026-11-22

### fix(ledgerstatus)：状态词的同行归属歧义（#411 / #412 / #413 不再被误判）

**缺口**：parse 对**整行**算一个状态、行里所有单号**无条件继承**——于是

```
——#411 就是这条的实例。**本轮动了一处**（#411 的用例换成……），**仍开着**——……
```

里那句「仍开着」修饰的是**那条建议**的开放状态，却把 **#411 派生成待清偿**，
而它 **2026-11-17 就关了**。v0.64.2 的判据 5 拿这个错误状态去对账 GitHub，
当场判出「关了没登记」；**#412 / #413 没撞上纯属排版运气**（它们的标记词在续行上）。

**两道机制（一治标一治本）**：

1. **按出现次数判归属**（`statusForLine`）：同一行里非 PR 单号**出现多于一次**
   ⇒ 返回 `unknown`（不猜状态词修饰谁）。用 `issueMentionsIn`（**不去重**）而不是
   `issueNumbersIn` 是关键——那行 #411 只有**一个编号**却出现在两处语境，去重后
   长度是 1、照样误判。与已有的「一行两种标记 ⇒ 判不出」是**同一条哲学**：
   **归属不确定时宁可不表态**；
2. **围栏代码块不进单号视图**（`fencedCodeLines`）：``` 包围的行由主循环跳过
   （与既有 `inTable` 跳过表格行同款机制）。理由：**代码块不是台账的叙述**——
   它是示例与引用（合成数据、被改掉的原文、命令），把它们当声明会凭空造出状态归属。

第 2 条是被**实录三次**逼出来的：每次修完，说明这个歧义的文档自己又把原文引进来
（第三次时解释句里只有一个单号，第 1 条保护不到）——**靠作者记得改措辞防不住**，
故由机制挡。

**代价（如实登记）**：多单号正文行的辅助状态声明不再生效 ⇒ 判据 1 对这类行可能
漏报矛盾；代码块内的单号不再产生出现点 ⇒ 判据 3 对「只在代码块里被提到」的单号
不再报。两者都是**更宽松**方向，且权威面未动——登记表**状态列**（整行一个状态、
与单号个数无关）与登记列的解析完全不受影响。

**反向验证（坏实现会红）**：

- 坏实现 A「一律不猜」（连单单号也不归属）⇒ `TestSingleNumberStillInheritsStatus` FAIL；
- 坏实现 B「多单号也猜」（旧行为）⇒ `TestSameNumberTwiceDoesNotInheritStatus` +
  `TestMultipleNumbersDoNotInheritStatus` FAIL；
- 坏实现 C「代码块不跳过」⇒ `TestCodeBlockLinesDoNotProduceOccurrences` FAIL
  （实得 3 处出现点，含代码块那行）；
- 另钉住**状态列权威性**（多单号行的状态列仍生效）与**矛盾仍可报**。

**真台账实测**：`#411/#412/#413` 合并状态全部 `unknown`（#411 此前是 `open`），
`#447/#455=closed`、`#279=open` 的权威声明完好；`make ledgercheck` ⇒
**141 个单号 / 564 处出现点 / 319 条 issue 状态已对账 ✓**。

### 顺带开单：#465（ptc stress 测试负载下偶发红，已实录两次）

`TestStressSubprocessKillMidRunReaps` 在本机负载下两次偶发红（2026-11-21 父侧挂死 /
2026-11-22 子进程应存活却已死），单跑与重跑均绿、CI 绿 ⇒ 不是本版引入，但**连续
两次**按「同形面要系统审」开单：它的价值恰是在异常时序下判得准，而它自己在异常
时序下会误报 ⇒ 红的信号被稀释，真回归会被当成「又是那个 flaky」。

### 下游要做什么

- **零影响**：`internal/ledgerstatus` 是 **internal 包**，下游本就不可 import；
  内核包公开契约面**逐字未变**，依赖地板未动 ⇒ **无需 `go mod tidy`**。
- 本版改动只影响**本仓自己的门禁**（`ledgercheck` 判据 1/5 与 `deploymatrix`）
  如何解析台账正文。

## [v0.65.0] — 2026-11-21

### 本版清偿什么

> **#455 的判据本体已随 v0.64.2 出货**（PR #459，实时 `gh` 拉取 + 纯函数判定）。
> 本版不重做它，只补它缺的**第二级数据源**，并清偿另外两件。

- **#455 的离线兜底**：实时拉不到（没装 gh / 离线 / 没 token）时**退回仓内快照**
  `docs/issue-state.yaml`——「跑不起来的门禁」与「没有门禁」是同一件事，而开发者机
  离线跑 `make check` 是常态。**判定仍走同一条 `checkIssueStates`**，换的只是喂给
  它的状态表（两套判定各判一套必然漂移）。
- **#447 的后半句**：v0.64.0 落地的是「僵尸例外当天即红」，而登记行处方里还有
  **「要么被显式标记为已失效待删」**这一半没落地 ⇒ 补齐。
- **顺带订正台账两处声明缺陷**（新判据上线当场抓到的）。
- **文档面**：USAGE/AI-GUIDE/AGENTS/README 的判据面与条数同步（三处「10 条规则」
  早已陈旧，实为 13）。

### feat(ledgercheck): 判据 5 的第二级数据源——实时为主、快照兜底（#455）

**为什么需要兜底**：v0.64.2 的判据 5 每次实时调 `gh`，在**离线 / 没装 gh / 没有
token** 的机器上直接 exit 2 ⇒ 门禁跑不起来。而「跑不起来的门禁」与「没有门禁」
是同一件事。

**两级怎么切**（`issueStatesWithFallback`）：

| 情形 | 结果 |
|---|---|
| 实时拉取成功 | 用实时数据，**不带任何快照标签**（兜底只在主路径失败时出现，否则「实时为主」名存实亡） |
| 实时失败 + 快照在 | 退回快照，并**大声回显**：`Tag` 进成功行、`Warn` 进 stderr，两处都点明「数据可能不是当下事实」 |
| 两级都拿不到 | **退出码 2「给不出结论」**——既不并进 1（有违反）也不当 0（通过） |

**判定只有一条路径**：兜底只是换了一张喂给 `checkIssueStates` 的状态表。本版**删掉了
我方第一版自带的那套 `Reconcile` 判定**——两套判定各判一套必然漂移，而漂移的方向
恰好是「门禁判的和另一套判的不是一回事」。

**快照解析层一并收紧**（三种形态都报错，静默忽略的每一种都指向同一个结局）：

- 未知取值（`closedx`）：会让那个单号从对账里消失；
- **非数字键**（`abc`）：多半是 YAML 键名写错了，静默跳过同上；
- **空快照**：**空结果与「查不到」不可分**，拿它对账等于凭空判「全部一致」。

**为什么不把时效做成红判据**：时间判据会让门禁红在**与被判事实无关的原因上**
（离线三天就红，而台账与 GitHub 可能完全一致）。故数据新不新只**回显**（日期 +
天数），刷新是 `make issue-state` 的事——但回显必须在，否则「判据说谎」。

**反向验证（4 条，全部实跑）**：① 实时失败 + 快照在 ⇒ 退 0 且回显来源与警告；
② **兜底喂的状态表走同一条判定**（快照说 #101 仍开着、台账说已合入 ⇒ 红且点名）；
③ 两级都拿不到 ⇒ 退 2 且指到缺的快照文件；④ 实时成功 ⇒ **不带快照标签**
（挡住「无条件走兜底」的坏实现）。解析层另有 4 条（取值域 / 非数字键 / 空快照 /
往返 + 距今天数）。

### feat(archcheck): 例外对账补上「已失效待删」标记（#447 的后半句）

登记行 #447 的判定栏写的是：每条例外要么命中至少一条违反，**要么被显式标记为
「已失效待删」**。v0.64.0 落地的是前半句——僵尸例外当天即红并点名——但没有标记这一半：
一条「今天不命中、确实还要留着」的例外只能被删掉，而删掉会丢掉 `reason` / `until` /
`removal` 三份债的来龙去脉。

- **`Exception.status`**（yaml `status`，**可选**，缺省行为逐字不变）：省略 = 在用；
  `stale` = 维护者已确认它不再需要。
- **取值域封闭**：只能省略或 `stale`，拼错（`stal`）当天自校验红——拼错的标记若被
  当成「在用」，命中 >0 时会**绿**，维护者写下的意图与结论相反。
- **僵尸判定跳过已标记的例外**，但**报告逐条点名**（`⚠ 已标记失效待删`）——否则这个
  字段会变成写了没人看的东西。
- **矛盾判红**：标了 `stale` 却**仍命中**违反 ⇒ 红，否则 stale 就成了「写一次永久免检」
  的免死金牌。

**反向验证（实跑）**：坏实现注入三条各对应一条用例，注入即红、还原即绿；真机三态注入
（真实 policy）：未标记 exit 2 点名僵尸 / `status: stale` exit 0 / `status: stal` exit 2
同时报取值不合法与僵尸 / 还原 exit 0。

### 上线当场抓到的漂移：台账两处声明缺陷（随本版订正）

新判据第一次跑就报红，两处都是**台账自己的声明面缺陷**，不是判据过严：

1. **§16 用英文 `closed` 而非本台账的标记词** ⇒ 机器读出来是「没表态」。
   事实：#411/#412/#413 三条 2026-11-17 11:39Z 由 **PR #414** 关闭（PR 标题里逐条
   点名三条单）。改用标记词 `已合入`。
2. **§18 第 1 段把建议的「仍开着」错记到那一单头上**：原文整段是**一个物理行**，
   单号引用与「仍开着」同行，而解析器**按行**读状态 ⇒ 建议没做完被读成「这一单还开着」。
   拆行 + 主语改成「这条建议」。

> **本轮最该记住的一条**：**订正注释第一版自己又踩了同一个坑**（注释里单号与
> 「仍开着」同行）⇒ 判据立刻再红一次。**按行判定的判据面，注释也是数据。**

### refactor(archcheck): policy.go 按关注点拆出声明面 policy_decl.go

`archcheck/policy.go` 在基线上已 **395/400 行**——与 #448 判的 `eventbus/tail.go`
398/400 是**同一种病**（下一次加字段就撞红线）。加 `status` 字段后即 **413 行越界**，
故拆成 `policy_decl.go`（声明面 348 行）+ `policy.go`（装载面 78 行）。
**零行为变更**：纯移动，不改任何一行类型定义、标签或函数体。

### 下游要做什么

**① 只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：
零导出符号消失 / 零既有接口加方法 / 零既有导出函数签名变更）。

**② 无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。

**③ 若你复用 `archcheck` 的例外机制**：本版新增**可选**字段 `Exception.status`
（省略 = 在用，行为与旧版**逐字相同**）；**老 archcheck 加载带 `status` 的 policy 会因
严格 YAML 解析失败（退出码 2）** ⇒ 升级 archcheck，或删掉那行。

**④ 若你在 Go 代码里构造 `archcheck.Rule`**：本版**未动** `Rule`（无字段变化）。

**⑤ 若你在自己仓跑 `ledgercheck`**：判据 5 现在是**两级数据源**——有 `gh` 就实时查；
没有则读仓内快照，**快照缺失且实时也失败才 exit 2**。要让离线兜底可用，把
`docs/issue-state.yaml` 一起带走（或跑 `make issue-state` 生成）。


## [v0.64.3] — 2026-11-22

### fix(controlplane)：权威强释的原子接缝——释放与介入留痕同事务（P0-A 任务侧，PR #461）

深度评审确认的 P0：\`forceReleaseAuthoritative\` 的默认形态是两步——先\`Release\`（租约当场作废），成功后 \`appendAudit\`。两步之间死掉或留痕失败 ⇒ **租约已释放、方法返回错误、本次介入缺成功审计**——违背「介入留痕与状态同事务」的既定纪律（非权威路径由 \`Store.Commit*\` 原子兑现，唯独权威路径此前没有这个接缝）。

**\`ForceReleaseAtomic\` 可选接缝**（本版新增的唯一导出接口）：实现方把「校验并释放 + 写审计行」收敛进同一事务；审计失败 ⇒ 释放一并回滚；拒绝路径（令牌不匹配）不产生审计行。\`StorePlane\` 探测到即走原子路径，未实现保留两步形态并在调用点如实声明窗口。同版顺手修了两个同族缺口：探测点必须在分步 Release **之前**（放之后等于同一动作施加两遍，单测当场抓住）；\`ErrNoAuditor\` 检查移到任何状态变更之前（旧实现「租约已放才发现无留痕面」）。

参考实现（examples/pgstore）：新表 \`pgref_control_audit\`（DDL 拆出 \`schema_control.go\` 守结构阈值），留账与释放同事务、留痕时刻一律 \`clock_timestamp()\`（介入方进程时钟不可信）、\`operator\` 列 \`CHECK (operator <> '')\` 落「介入者必填」契约。\`BoardStore.ForceReleaseWithAudit\` 经 \`withinTx\` 兑现。

判据：controlplane 四个单测（原子路径优先 / 审计失败状态未变 / 两步回退兼容 / auditor 前置拒绝）+ pgstore 三个真 PG 用例（同事务落库 / 审计失败回滚 / 拒绝不留痕）+ 接缝基线登记（\`seamcheck\`，调用点 1 = StorePlane 探测处）。

**边界声明**：效果权威（\`adjudicateAuthoritative\`）的两步窗口本版用 auditor 前置收窄，其原子接缝（\`AdjudicateAtomic\`）**待参考实现就绪后同构提供**——不把没有实现方的接口发出去变成死条文。

**下游要做什么**：宿主若用 \`NewAuthoritativeStorePlane\` 装配权威控制面，让任务权威（运行时的 \`taskboard.Board\` 存储实现）实现 \`ForceReleaseAtomic\` 即自动闭合窗口（参考 \`examples/pgstore.BoardStore\`）；不实现则保持两步形态，留痕失败需对账审计平台。

## [v0.64.2] — 2026-11-21

### feat(ledgercheck)：台账 ↔ GitHub issue 状态双向对账（#455）

**补的缺口**：台账说某单「已合入」而 GitHub 上它还开着，**没有任何判据会红**——
v0.63.0 收尾时发现 #427 / #428 / #430 就是这么关漏的（台账登记、关闭 PR、代码逐条
核实全部对得上，四条既有判据全绿，只有 issue 还开着）。既有判据 1–4 判的都是台账
**内部**自洽；而仓里**没有任何东西在看 GitHub**（`deploycheck` 的 issue 缺口也只读台账）。

**判据 5（双向，缺一条就是半个）**：

| 方向 | 台账说 | GitHub 上 | 判定 |
|---|---|---|---|
| 漏关 | 已合入 | 仍开着 | 红，点名单号与台账行 |
| 关了没登记 | 仍开着 | 已关闭 | 红——接手的人按「单还开着」判断「还没人做」，台账 §15 形态 (a) 的同一个坑，错在反方向 |
| 无从核验 | 任一 | 单号查不到 | 红（单号打错 / 仓库不对 / 权限不足，静默放过正是要消灭的形态） |
| 台账没表态 | unknown | 任一 | 不判（把「没说」当成「说错了」是另一种错） |

**判据面与数据源分离**（设计核心）：

- 判据是**纯函数** `checkIssueStates(led, open)`——喂构造的状态表就能钉住「漏关必须红」，
  不必联网；
- 数据源是**可注入的适配层**（包级 var `issueFetcher` / `runWith` 显式收 fetcher）：
  生产走 `gh issue list --state all --json number,state`，测试走构造数据；
- **拉不到 ⇒ exit 2（给不出结论）**：既不并进 1（有违反）也不当 0（通过）——
  「查不到」与「没有违反」混起来就是本仓最贵的失效形态。

**为什么不做仓内快照**：没人刷新时它会**静默过期**——判据对着陈旧数据判、照常绿，
把「没有判据」换成「判据会说谎」没有变好。CI 每次都有 `GITHUB_TOKEN`，实时查一次
就是一次 API 调用。

**CI 接线**（否则就是「目标存在、没人跑」）：`ledgercheck` 本来就在 `make unit` 里；
`ci.yml` 补 `permissions: issues: read` 与 `make unit` 的 `GH_TOKEN`（不给 token 匿名
只有 60 次/小时/IP，共享 runner 必撞限流 ⇒ 判据会 exit 2 而不是静默跳过）。
**job 名一字未改**（分支保护的 required context 不能动）。

**首跑即抓到一个真问题**：#411 在台账里被派生成「待清偿」，而它 2026-11-17 就关了。
根因是**状态标记词的歧义归属**——§16「下一阶段建议」里「那条建议的开放状态」与 #411
在同一物理行上，parse 按「同行单号继承状态」归给了它（同格式的 #412/#413 没撞上纯属
排版运气）。改那句话消除歧义（对**人**同样有误导），并把该歧义登记为**已知边界**
（改 parse 会动判据 1 的既有行为，不动）。

**egress 登记**：`fetchIssueStates` 起 `gh` 子进程 ⇒ 按门禁要求登记 `egress_paths`
（`unrestricted`，事实登记——与 `cmd/releasecheck` 的 `gitOut` 同款口径）；子进程环境走
**`procx.ChildEnv`**（PATH/HOME 白名单 + 按名放行 `GH_TOKEN` / `GITHUB_TOKEN`），
**不继承全量 environ**。

**反向验证**：坏实现 1（不判反方向）⇒ `TestClosedButStillRegisteredFails` FAIL；
坏实现 2（查不到当通过）⇒ `TestFetchFailureExitsTwo` FAIL；七条新用例正反成对；
egress 判据**实测抓到本次改动自己的违规**（`fetchIssueStates` 未登记 ⇒ 红），登记后复绿。

### 下游要做什么

- **零影响**：`cmd/ledgercheck` 是 `package main`（不可 import），内核包公开契约面
  **逐字未变**（零导出符号消失 / 零接口加方法 / 零签名变更），依赖地板未动
  （`go.mod` / `go.sum` 未动）⇒ **无需 `go mod tidy`**。
- 若你**复用本仓的 Makefile 门禁**：`make ledgercheck` 现在需要 GitHub 访问
  （拿不到 ⇒ exit 2 而非跳过），需要 `gh` 已安装并登录，或给 `GH_TOKEN`。
- **本版是门禁工具的能力增强**，不影响任何运行时行为。

### 顺带：#455 关单时机（值得记）

台账 §9 把 #455 翻成「已合入 (#459)」与**关单动作必须同步**：判据 5 会拿声明面去对账
真实 issue 状态，先翻面后关单的中间态**必然红**。本轮顺序是「PR 就绪 + CI 7/7 绿 ⇒
关 #455 ⇒ 翻台账 ⇒ 追加提交 ⇒ 再跑一轮 CI ⇒ 合并」——两个系统的状态要一起动，
这正是这条判据要暴露的东西。
## [v0.64.1] — 2026-11-21

### fix(worker)：持续存储故障下租约会话的本地资格截止（P0-B，PR #458）

深度评审确认的 P0：`LeaseSession.renewLoop` 此前只在**明确收到**
`taskboard.ErrLeaseLost` 时才失格；续期持续返回存储不可达/网络超时时无限
重试，而权威存储侧租约早已过期、任务可能已被新 worker 接管——旧持有者
的会话 ctx 不被取消，仍在执行业务动作（双执行者窗口）。注释里「连续失败
撑满 TTL 后下一拍必然 `ErrLeaseLost` 收口」的隐含前提——**那一拍必须重新
得到存储响应**——在持续网络分区下不成立。

修法：每次续期成功记录本地单调时钟的确认时刻，建立保守资格截止
`T_deadline = T_last_confirmed + TTL − TTL/10`（margin 吸收本地与存储
时钟的传输漂移）。超过截止仍未确认持有权 ⇒ 主动失格、取消执行 ctx，
**不等存储回包**。本地截止点只负责提前撤销执行资格，不用于授予新租约
（授予权永远属于权威存储）。

- `LeaseSessionConfig` 新增 `Now` 时钟注入点（零值 = `time.Now`；
  `@seam: no-reentry no-blocking panic-caller`，同 `ClockedStore.UseClock` 口径）；
- 正对照用例：续期持续报错 + 假时钟推过 TTL ⇒ 会话主动失格；
- 负对照用例：持续故障未过截止 ⇒ 不失格（自愈语义仍在窗口内）；
- 测试面等待一律用可观测事实作尺（`flakyBoard.renewCalls` 计数），
  不用裸 sleep 窗口（`harness-test-wallclock-ruler` 口径）。

**下游要做什么**：使用 `worker.NewLeaseSession` 的宿主若注入过自定义
`RenewInterval`，无需改动；默认形态自动获得持续故障失格保护。多副本
部署建议在故障注入矩阵里补「worker↔DB 持续分区 > TTL」场景，断言旧
worker 在截止后不再发出新的业务动作。


## [v0.64.0] — 2026-11-21

### 本版清偿 §34 登记的三条残余边界（#447 / #448 / #449）

上一版（v0.63.0）在收尾时**登记了三条只登记不修的残余边界**，本版逐条落地：

- **#447 僵尸例外**：policy 的 `exceptions` 段里**命中不到任何违反**的例外此前被**静默忽略**
  ——站点被改、文件被拆、规则口径变了之后，那条例外再也命中不到东西，而门禁照样绿 ⇒
  已不需要的豁免可以**永久留在声明面上**，下一个看声明面的人会以为「这里还有一条已知债」。
  本版把它变成机判：按 `rule + path + line` **三元组对账**，命中不到 ⇒ **阻断级**且点名到那一处。
- **#448 `eventbus/tail.go` 398/400 行**：离结构门禁只差 2 行，下一条判据就会撞红。
  按关注点拆成 `tail.go`（230）+ `tailstore.go`（108，三个存储接缝）+ `tailoption.go`（74，
  装配选项）；同包 `tail_cursor_test.go` 384/400 一并拆成 gap（208）+ form（195）。
- **#449 `pii` 包级 `Set*` 直调无门禁**：#442 把还原收进一个 helper，但「不得直调」
  **只是纪律**——下一个用例作者仍可绕过，#442 那个 `-count=20` 概率红 19 次可原样再生。
  本版新增 `check_kind: test_no_direct_call` 把它变成机判。

### feat(archcheck)：僵尸例外对账（#447）

**复现坐实**：注入一条指向 `does/not/exist.go` 的例外（`until`/`removal` 齐备）⇒ **门禁绿**，
输出只把例外列进「已登记例外」，**没有任何东西点名它命中不到违反**。

⇒ 判据按 `rule + path + line` **三元组对账**（不用「条数相等」的粗判——不同规则命中数不同、
同一站点可能命中多条规则）：登记的例外在**实际违规集**里找不到对应项 ⇒ **阻断级**，且**点名到
那一处**。例外**指向的东西本身有问题**（规则不存在 / 不在判 / 缺字段 / 试图豁免自校验）时
由自校验点名，僵尸判据不叠第二条——同一件事报两遍，下一个人会以为是两个问题。

**实现**：`selfcheck.go` 拆出 `selfCheckWithViolationSet(root, pol, violated)`，
`selfCheckViolations` **保留原签名**做薄包装（传 `nil` 跳过该判据）⇒ **40+ 既有测试调用点零改动**；
主路径在调用前从 `res.Violations` 构造违规集传入（此刻 selfcheck 自己的产出尚未混入 ⇒ 对账面干净）。

**反向验证两处**：注入僵尸例外 ⇒ 门禁**红**且点名 `does/not/exist.go:1`，撤销复绿；**坏实现**
（主路径退回不传违规集）⇒ `TestRunFlagsZombieException` **当场 FAIL**——钉的正是「主路径忘了传
违规集」这个坏实现，纯 policy 层调用（40+ 单测）判不了它。

### feat(archcheck)：新增 `check_kind: test_no_direct_call`（#449）

测试面**不得直调包级全局态 setter**：判**调用点**（`go/ast`）不判文本——函数定义、注释、
字符串（`t.Fatal("SetEnabled(false)…"`）、测试名（`TestSetDigestKeyGuards`）都不是调用，
逐行正则会把它们一起命中，规则误报到没人信它就等于没有规则。

- **被禁符号与受管辅助声明在 policy 侧**（新增 `Rule.ForbidCalls` 导出字段）：`archcheck` 是
  跨宿主复用的库，「哪些是全局态 setter」是宿主的知识，硬编码等于把宿主的事写进内核；
- **受管辅助体内不判**（helper 自己必须调真 setter），**未登记的包内助手体内照判**——
  助手是绕开 helper 的第二条路；
- **自校验**：是这个 `check_kind` 却没有 `forbid_calls` ⇒ **当天即红**（否则规则在、severity
  是 error、报告里计入条数，却一条都判不了——与「字段名拼错」同一类的静默失效）。

**反向验证四处**：真代码注入两处直调 ⇒ 门禁**红 2 处**；policy 样例 `want: hit` 翻成 `miss`
⇒ **红**（证明样例判定核真的在跑，不是静默跳过）；**坏实现**（判定体忽略包内助手）⇒
`TestNoDirectCallFlagsUnregisteredHelperThenHonorsRegistration` **FAIL**；四条单测正反成对。

### refactor(eventbus)：`tail.go` 按关注点拆分（#448）

| 文件 | 拆前 | 拆后 | 余量 |
|---|---|---|---|
| `eventbus/tail.go` | **398/400** | 230 | 170 |
| `eventbus/tailstore.go`（新） | — | 108 | 292 |
| `eventbus/tailoption.go`（新） | — | 74 | 326 |
| `eventbus/tail_cursor_test.go` | **384/400** | → gap 208 + form 195 | 192 / 205 |

拆分只动**文件的内部组织**：**导出面逐字相同**（14 == 14，`diff` 零差异）、**测试名逐字相同**
（4 == 4）、全包测试绿；现存最大文件 356 行，全包远离 400 红线。

**连带影响被 `deploycheck` 当场抓到**：`docs/DEPLOY-MATRIX.yaml` 的 `tail.go#TailStore` /
`#WatermarkTailStore` 引用随符号搬家而失效 ⇒ 已改指 `tailstore.go`。这类 `文件#符号` 形态的
证据引用由 `deploycheck` 机判（证据可解析），不会静默漂——这正是它存在的理由。

### 下游要做什么

- **只用内核包的宿主不受影响**：内核包公开契约面**逐字未变**（导出面审计三问：零导出符号
  消失 / 零接口加方法 / 零签名变更）；新增 `Rule.ForbidCalls` 是**源码级 additive 变更**，
  用字段名写字面量的下游不受影响。
- **无需 `go mod tidy`**：依赖地板**零变化**（`go.mod` / `go.sum` 未动）。
- **若你复用 `archcheck` 的例外机制**：本版起**命中不到任何违反的例外会判红**——你若曾登记过
  已失效的例外（站点已修复 / 文件已拆 / 规则口径已变），升级后门禁会转红，**删掉那条例外即可**。
- **若你启用 `test_no_direct_call`**：需要在 policy 里声明 `forbid_calls`（被禁符号清单），
  否则 policy 自校验当天即红。

### 顺带：三条 issue 关漏了（#427 / #428 / #430）

台账 §32 登记它们「已合入」（关闭 PR 见该三行状态列），PR 确实已 MERGED，而三个 issue
**在 GitHub 上全还开着**——本轮逐条核实代码确已落地后关闭。机制缺口另立登记行（§9 的 #455）：
`ledgercheck` 只判台账**内部**一致性、不看 GitHub ⇒「台账说关了、GitHub 没关」照常绿，
与 #447 是同一类病（声明面与事实面不一致却没有判据会红），只是漂移发生在**两个系统之间**。

## [v0.63.0] — 2026-11-21

### 本版与 v0.62.2 的边界（**先说清，避免同一件事被两个版本各领一次**）

上一版（v0.62.2，tag `v0.62.2`）出货的是**两条纪律的规则本体**：新增
`harness-test-defer-cleanup` / `harness-test-wallclock_ruler` 两条 `check_kind` 判定、
被新规则照到的 **9 处**同形面用例改成「可观测事实 + defer 收尾」形态、以及 **8 条例外**
登记（`eventbus/**` 6 处 + `pii/**` 1 处 + `examples/pgstore` 1 处）。

**那 8 条例外里，有 6 条是当场就知道的假账**——它们登记的理由是「该包正由并行工作线
改造，本轮不碰」，而那 6 处命中**形态是真的**（失败路径上 Goexit 确实会跳过那一步等待点）。
⇒ 本版把其中**能改的 6 处真违规改成代码**，并让例外随修复同步删除（**例外不许无声失效**）。

本版清偿的是**上一版没有落地的部分**：

- **#442**（`pii` 全局态还原的**真缺陷**）：包内两套**相反**的还原约定 + `restore(t)`
  只在 cleanup 还原、用例开始时不重置 + 还原在 goroutine 里跑且超时静默 ⇒
  `-count>1` 下概率红 ⇒ 收进**一个**包级 helper。
- **#443**（`eventbus` 两处「缺席类断言」仍是墙钟窗口 + `tail_test.go` 379/400 行）：
  按关注点拆分 + 缺席断言改成「先等机制跑过 N 读拍」。
- **判据精度修正**：`defer_cleanup` 把**非阻塞 drain**（带 `default` 的 select）误判成
  等待点 ⇒ 给出「把 drain 塞进 defer」的错误处方 ⇒ 收窄口径 + 双向用例钉住。
- **6 处 eventbus 真违规**改代码（不再挂例外）。

### 修复(archcheck)：非阻塞 drain 不是等待点——判据精度修正

新规则落地后把 `eventbus` 的两处缺席断言**误判**成违规：那两处的「等待点」在助手
`assertNoTailFrame` 里，形态是 `select { case ev := <-ch: ...; default: }`——这个 select
**带 default 分支** ⇒ 它**永不挂起** ⇒ 这处接收**不等任何东西**，它是**断言**（看此刻通道里
有没有东西），不是 join。

按宽口径把它算成等待点，判据就会给出「把这个 drain 塞进 defer」的处方——而 drain 本来就不等
东西，塞进 defer 什么也等不到 ⇒ **判据指错路**（守卫抓不住 = 没有判据）。

⇒ 修正：**非阻塞接收不算等待点**（走 `go/ast`：接收落在某个 select 的 CommClause 里、
且该 select 带 default 分支 ⇒ 非阻塞）。**双向用例钉住**：带 default 的 drain 不判红；
没有 default 的 select（带超时分支，仍会挂起）**仍判红**且必须点名用例名——少一条，
另一条就成了「判据瘫掉也绿」的假绿。

⚠ 兄弟规则 `harness-test-goroutine-joined` 仍用宽口径（任意接收表达式都算等待点）——
那是它的**已登记边界 1**，本版不动（改它会把一批现存用例判红，属另一条登记行）。

### 修复(pii)：全局策略的测试还原收进一个包级 helper（#442）

`pii` 包内**两套相反**的全局态还原约定（`SetEnabled(false)` vs `SetEnabled(true)`；缺省值是
**enabled = true**，secure by default），加上那个 `restore(t)` helper **只在 cleanup 里还原、
用例开始时并不重置**，再叠加「还原动作在 goroutine 里跑且**超时后不报错**」⇒ 用例**起跑在脏
状态上**、还原还可能落在下一个用例开始之后 ⇒ `-count>1` 下 `TestApplyHonorsStrategy` 概率红
（`mask 应保长打码，实得 "13800001234"`，`-count=20` 红 19 次；main 上逐字复现 ⇒ 预存在）。

⇒ 修法：**一个**包级 helper（`pii/policy_globals_test.go`）——setup 阶段**同步**把全局重置成
缺省值；cleanup **同步**还原成缺省值，且**还原超时必须让用例失败**（静默通过正是串扰的来源）；
包内 **105 处**直接调 `Set*` 的写法全部改走 helper。

**证据**：基线 `-count=20` 红 19 次 ⇒ 修后 `-count=20` **全绿**、`-race` 全绿；反向验证两处
注入（还原值改回旧约定 / 删掉 setup 重置）⇒ **都红**；两条护栏用例入库
（`TestPolicyGlobalsSetupResetsToDefaults` / `TestPolicyGlobalsCleanupRestoresDefaults`）
——它们断言的是**注入的那一条被看见了**，不是「有违规」。

### 修复(test,eventbus)：tail_test.go 按关注点拆分 + 两处缺席断言可观测化（#443）

- **结构**：`eventbus/tail_test.go` 原 **379/400 行**（离结构门禁只差 21 行）⇒ 按关注点拆成
  5 个文件（夹具/助手、补达与初始化、读失败与幂等、取消与泄漏、装配校验），**最大 181 行**、
  余量 ≥219；原文件**已不存在**；夹具与共享助手**只此一份**（未复制、未改名）。
- **断言**：两处「50ms 内不该有事件」的墙钟窗口改成「**先等机制跑过 N 读拍**（夹具数出来的
  读拍数 = 可观测事实）⇒ 再判缺席」；2s 只作**兜底上限**、不参与结论（拍数没跑满 ⇒ 直接红，
  **不写成绿**——此刻判绿等于什么都没验）。

**反向验证（这一条是本单的核心价值）**：注入「第 20 拍才回灌存量」的坏实现 ⇒ **新断言红**；
**同一个坏实现在旧的 50ms 窗口写法下是绿的**（两个用例各 0.07s PASS）⇒ **旧守卫完全没抓到**
（白测坐实）；再加一档「回灌前先卡 300ms」的慢实现 ⇒ 新断言仍红。

### 修复(test,eventbus)：6 处 tailer goroutine 补 defer 收尾 join（上一版挂例外的真违规）

上一版 `defer_cleanup` 照出 `eventbus` 里 6 处真违规，但当时**挂了例外**（理由是「该包正由
并行工作线改造」）。⇒ 本版改代码：`tail_cancel_test.go` / `tail_delivery_test.go` /
`tail_failure_test.go` / `tail_cursorstore_test.go` 六处收尾等待点改成 `defer` 形态。

**注册顺序**先 join 后 cancel ⇒ `defer` 是 LIFO ⇒ 实际执行**先 cancel 再 join**（互不等待）；
join 带 2s 兜底上限。

⇒ **例外随修复同步删除**（`architecture-policy.yaml` 的 `eventbus/**` 6 条全删）：
例外命中不到任何违反却仍留在声明面上 ⇒ 就是「僵尸例外」，已登记为 #447。

### 门禁口径：例外从 8 条降到 2 条（且两条都是真债）

删掉 `eventbus/**` 6 条（已改代码）⇒ 只剩 **2 条真实债**，各带 `until: 2026-12-21` 与
拆除条件：`examples/pgstore/pg_clock_test.go:121`（替代判据需要 PG 侧可观测事实，
本机无 PostgreSQL 验不了）、`pii/policy_cas_test.go:43`（语义上等不到「PG 已落库」这个
可观测事实）。

### 依赖地板与导出面

- **依赖地板零变化**（`go.mod` / `go.sum` 未动）⇒ 下游**无需 `go mod tidy`**；
- **导出面审计三问**：零导出符号消失 / 零既有接口加方法 / 零既有导出函数签名变更 ⇒
  内核包公开契约面**逐字未变**（`archcheck.Rule` 的 `ManagedHelpers` 字段随 v0.62.2 出货）；
- **本版唯一的新增判定面**：`defer_cleanup` 的**非阻塞 drain 豁免**——判定口径收窄，
  **不新增字段、不新增 `check_kind` 取值** ⇒ 宿主 policy **无需改动**。

## [v0.62.2] — 2026-11-21

### 新增(archcheck)：「收尾等待点必须写成 defer」与「尺子必须是可以观测事实」两条纪律变成门禁判定（#440 / #441）

上一轮（v0.62.1）立了两条纪律，但都只是纪律、**没有门禁** ⇒ 同形面可再生。本版把它们
变成 `check_kind` 判定：

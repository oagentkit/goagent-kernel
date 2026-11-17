# 宿主接缝契约（Seam Contract）

> 适用：`goagent-kernel` 暴露给宿主的**回调/注入接缝**（时钟、留痕、通知、判定面）。
> 机器判据：`make architecture-check`（`architecture-policy.yaml` 的 `seams` 段）。
> 相关 issue：#366 / #367 / #368 是具体缺陷，#369 / #370 是制度根因与门禁缺口。

## 1. 为什么需要这份契约

宿主通过接缝把**自己的实现**注入内核。内核在判定路径上调用它们，于是同一个
接口同时承担两种风险：

- **重入**：宿主回调里回查本包（读一次账本、拍一次快照）是自然写法；
  内核若在持有不可重入的 `sync.Mutex` 时调用回调 ⇒ **永久死锁**。
  失效形态是**静默挂起**——没有错误、没有超时，调用永远不返回。
- **panic 外溢**：宿主实现 panic ⇒ 掀翻内核的心跳/提交协程 ⇒
  全部在途租约停止续期、他副本接管、非幂等副作用双份。
- **阻塞**：回调慢 I/O 排在判定路径上 ⇒ 拖住整条写路径或心跳节拍。

这三条不是某几个接缝的局部约定，而是**所有宿主接缝的默认契约**。
#369 的统计是：时钟族 20 个接缝条目里 0 个写齐三条；被 #317 逼着修过的
3 处恰好是唯一写齐的。本契约把它们变成可机读、可门禁的固定模板。

## 2. 标记模板（形态 B）

在**导出接缝声明点**的文档注释里写：

```go
// # 接缝契约（issue #369）
// @seam: no-reentry no-blocking panic-caller
//
// 不得重入（……）、不得阻塞（……）、panic 由调用方兜（……）。
func WithSomethingClock(clock func() time.Time) Option { ... }
```

三个词的含义：

| 标记 | 含义 |
|---|---|
| `no-reentry` | 回调内不得再次触发会读取同一时钟/账本的**写路径**（会递归）；只读回查由内核保证安全（时钟/接缝在内部锁外调用）。 |
| `no-blocking` | 回调必须立即返回；它位于判定/提交/心跳路径上。 |
| `panic-isolated` | 内核 `recover` 并上报（`async.ReportPanic`），宿主 panic 不外溢。 |
| `panic-caller` | 内核**不** recover；panic 属于编程错误，由调用方兜。这是显式第三态，不许不写。 |

**panic 处置必须恰写一个**：`panic-isolated` 或 `panic-caller`。两个都写等于
没承诺，门禁照样红。

### 枚举面

`archcheck/seamcontract.go` 用 `go/ast` 枚举**导出接缝声明点**：

- 导出函数 / 方法（参数类型为 `func() time.Time`）；
- 导出接口的方法（参数类型同上）；
- 导出结构体的导出字段（字段类型同上）。

判定排除 `_test.go`、`cmd/`、`examples/`。当前真实仓枚举到 **13 个声明点**
（`coord`×2、`controlplane`、`handoff`×3、`reconcile`、`scheduling`、
`sidetest`、`boardtest`、`ticket`×2、`worker`）。

口径差异（如实声明）：#369 的 **20 条**是「字段或参数类型为 `func() time.Time`」
的条目数（字段与参数分开计）；本门禁按**声明点**计，一处 option 函数覆盖它
背后的字段。两个数不是同一个分母，不互相冒充。

新增一个导出的时钟接缝而没写标记 ⇒ 门禁直接红（退出码 1）。
删掉任一已有标记 ⇒ 同一路径红。反向验证由 `archcheck/seams_test.go` 钉住。

## 3. 三种机判形态与取舍（形态 A / B / C）

### 形态 B：契约标记完备性——**阻断**

- 判据：导出接缝声明点必须带齐三条标记。
- 优点：纯结构判定、零假红、反向验证容易（删一行即红）。
- 根本局限：**标记是承诺不是事实**。声明了 `no-reentry` 但代码仍在锁内调，
  形态 B 照样绿。它保证「契约被写下」，不保证「契约被兑现」。

### 形态 A：锁内调用只读扫描——**warn，不阻断**

- 判据：时钟访问器（`now` / `nowLocked` / `nowAt` / `clock`）是否落在同一
  函数内的 `Lock/RLock` 与 `Unlock/RUnlock` 区间里。
- 定位：**疑似清单**，供人工复核。默认 `severity: warn`。
- 漏判形态（必须与工具一起记住）：
  1. **闭包作用域**：函数字面量按独立判定面处理；闭包在锁内被调用时，
     闭包里的接缝调用不会判出来。
  2. **多出口解锁**：按源码顺序回放 Lock/Unlock；早退路径各自 `Unlock`
     会让后续语句被误认为「已解锁」（#366 的 `quota_store.go:306` 正是此形态）。
  3. **helper 形态**：持锁调本包函数、该函数内调接缝 ⇒ 不做跨函数调用图。
  4. **接口接缝的自实现**：静态类型是接口时无法区分内核自调与宿主实现。
  5. **非 mutex 互斥**：channel 信号量、`atomic.Pointer` copy-on-write 判不了。
- 为什么仍然进 `make check`：修复后的代码上，把 `m.now()` 挪回锁内会直接
  显示为 warn；它零误报优势明显（只对明确的 `receiver.now()` 形态发声）。

### 形态 C：panic 外溢检测——**阻断**

- 判据：`seams.panic_scan.guarded_calls` 登记的宿主调用，其调用点必须位于
  隔离包装器回调内（`subhub.Do` / `subhub.Invoke` / `async.GoSafe` /
  `async.RunSafe` 等），或所在函数带 `defer func(){ recover() }()`。
- 清单是**增量**的：当前登记 `RenewBatch`（#368 的宿主任务板批量续期）。
  未登记的接缝由形态 B 的标记完备性兜底；全族清单化是 #400 的后续工作。
- 局限：按调用名精确匹配，不做类型/接口解析；同名但不同接缝会被一并要求
  隔离（宁可多要求，不漏判）。包装器清单是声明面——新增隔离原语要先登记，
  否则会误报。失败方向是**变红**，不是变绿。

## 4. 给实现者的检查清单

新增/修改宿主接缝时：

1. 在导出声明处写 `@seam:` 三件套（`no-reentry no-blocking` + 一个 panic 处置）；
2. 若内核**不**隔离 panic，写 `panic-caller` 并在正文说明由谁兜；
3. 调用宿主回调前**先在锁内取快照、解锁后再调用**（照
   `coord/store.go` 的 `nowAt` / `ticket/backend.go` 的 `Put`）；
4. 批量/并发两条路径的 panic 归属必须同口径（#368）；
5. 新增隔离原语时把它登记进 `seams.panic_scan.wrappers`；新增高危宿主调用
   登记进 `guarded_calls`；
6. 跑 `make architecture-check`；删一条标记确认它会红（反向验证）。

## 5. 与 issue 的对应

| issue | 落点 |
|---|---|
| #366 | `scheduling.MemQuotaStore` 三处时钟调用移出 `m.mu`；`quota_clock_reentry_test.go` 三条重入死锁用例（修复前 3s 超时实测）。 |
| #367 | `controlplane.MemStore` 的 `nowLocked` 拆成锁外 `now`；`clock_reentry_test.go` 覆盖 `Claim`/`Renew`。 |
| #368 | `worker.renewBatchCall` recover + `async.ReportPanic`；`renew_panic_test.go` 钉批量/并发同口径。 |
| #369 | 本文档 + 13 个导出时钟接缝的标记；`seams_test.go` 覆盖真实仓清单。 |
| #370 | `seamcontract.go` / `seamlock.go` / `seampanic.go` 三形态；本文档第 3 节记录取舍与漏判。 |
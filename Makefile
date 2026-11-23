BIN      := archcheck
ENV      ?= local
# 本地并行开发用的 guardx 工作副本路径（未跟踪，见 .gitignore）。
# 走 make devlink 接入 workspace，不要在 go.mod 里写 replace 指向它。
GUARDX_WORK := .guardx-work/guardx
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
# EXPORT：2026-10-18 起本仓依赖全部为 public 模块（github.com/oagentkit/*、
# github.com/oekit/*），门面 agentkit 已退役并从 go.mod/go.sum 移除。
# 曾用于直拉私有模块的 GOPRIVATE/GONOSUMCHECK 一并清理——本仓是公开仓，
# 构建脚本里不该残留任何私有域名。万一来日确需私有模块，在此按需追加。
EXPORT   :=
# macOS 新 SDK 与 CLT linker 不兼容 + 许可未接受：一切 go build/test 带 CGO_ENABLED=0。
GOENV    := CGO_ENABLED=0
# cover-gate 覆盖率阈值（内核仓门禁应随成熟度上调；参照宿主仓做法设真阈值而非永不红）。
COV_MIN  := 60
# 逐包下限。全局 85% 会**掩盖单个包的塌方**——2026-11-06 实测：全局 85.5%，
# 而 cmd/docsync 只有 23.6%、sandbox 60.0%，两个数字都被大盘盖住，没有任何信号。
# 全局阈值管「整体没退步」，逐包下限管「没有一个包躲在大盘后面」，两者不能互相代替。
PKG_COV_MIN := 70
# 逐包下限的显式豁免（逗号分隔，按包路径**后缀**匹配）。**当前为空。**
#
# 这一类豁免的语义是「**覆盖率不达标、但可以用例补上去**」的包：拆它的方式是
# 补用例。故「自己达标即红」对它是正确的——豁免的存在理由（还缺用例）消失的
# 那一刻，判据就该要求拆掉它。
#
# 纪律同 policy 的 exceptions：豁免必须写原因 + 拆除条件，且**到期即拆**——
# **普通豁免**包一旦自己达到下限，cover-gate 会报「豁免失效」并拒绝变绿，
# 逼人当天删掉这一行，而不是让它无限期留着变成装饰。
# （结构性豁免的失效判据不同，见下面 PKG_COV_STRUCTURAL_EXEMPT。）
#
# # 这里曾经有过唯一一条豁免，已当场拆除（2026-11-06，同一天）
#
# 豁免对象是 cmd/docsync（23.6%）。当时登记的原因写的是：判定逻辑全在
# `package main` 里，run() 把 `README.md` 写成相对路径、失败走 os.Exit，没有可注入
# 的接缝 ⇒ 想测就得先改造它。拆除条件写的是「把判定逻辑搬进可导入包」。
#
# 实际清偿走的是一条更短的路径，**没有搬家**：Go 1.24+ 的 `t.Chdir` 让用例可以
# 切进临时目录，在那里摆一份 README 与若干包目录，就是 run() 眼里的整个世界
# （见 cmd/docsync/run_test.go）。覆盖率 23.6% → 88.8%，豁免随之下线。
#
# 记这一笔的原因是它验证了「失效即红」这条机制：补完用例后第一次跑 cover-gate，
# 门禁自己报的就是「豁免失效，请从 PKG_COV_EXEMPT 移除」，而不是默默放行。
#
# ⚠️ examples/pgstore 原先登记在这张表里（2026-11-12，issue #94），但它的豁免
# 理由是**环境依赖**⇒ 登记的**类别**错了（见下面 PKG_COV_STRUCTURAL_EXEMPT）。
PKG_COV_EXEMPT :=
# 逐包下限的**结构性豁免**（逗号分隔，按包路径**后缀**匹配）。
#
# 与 PKG_COV_EXEMPT 的区别在**判据语义**，不在匹配方式（两者都是后缀匹配）：
#
#   PKG_COV_EXEMPT             「覆盖率不达标、但可以用例补上去」的包。
#                              豁免包自己达到下限 ⇒ 判红（豁免失效，当天拆）。
#   PKG_COV_STRUCTURAL_EXEMPT  包的**性质**决定通用覆盖率判据不适用（环境依赖型）。
#                              豁免包自己达标 ⇒ **不判红**（豁免的理由仍在）；
#                              但**没命中任何包** ⇒ 仍判红（包没了/改名了 ⇒ 条目该拆）；
#                              **命中的包形态不适用**（零测试文件 / 无语句）⇒ 仍判红
#                              （豁免对它压根不适用 ⇒ 换表或拆除；两种形态分开报，
#                              否则维护者会照着「没有包命中」去找一个**就在日志里**的包）。
#
# 为什么要分两类（2026-11-20）：原先 examples/pgstore 登记在 PKG_COV_EXEMPT 里，
# 而它的豁免理由恰恰是「环境依赖」⇒ 同一个包、同一份代码，两种环境两种结论：
# 本机有 PostgreSQL 时该包覆盖 80.7% ≥ 70% ⇒ 判红「豁免失效」，可豁免的理由
# （环境依赖）一个字都没变 ⇒ 门禁在逼人写一个**假的拆除**。
# 而「拆豁免」这条路本身是反方向的：无 DSN 时该包覆盖仅 1.5% ⇒ 拆了它，
# CI（gates job 没有 PG service）必红 ⇒ 拆豁免 = 把本机绿换成 CI 红。
#
# ⚠️ 同一个包**不得**同时登记在两张表里 ⇒ 判**口径冲突**并报红。
# 报告里**两个表项都点名** ⇒ 照着提示拆掉错的那条即可。
# 为什么必须报出来而不是默默取一条：两张表的语义互斥（一个说「还缺用例」，
# 一个说「性质决定通用覆盖率判据不适用」），默默取一条等于判据自己藏起一处
# 矛盾 ⇒ 下一个人读到的是一份自相矛盾的声明面，而门禁此时是绿的。
#
# # 曾有一条结构性豁免（2026-11-12 登记，2026-11-20 归类到本表，issue #94；
# # 2026-11-22 随 examples/pgstore 迁往 goagent-runtime，条目已删、本表现为空——
# # 保留下面这段判据设计的来历，因为「两类豁免为什么失效判据不同」仍要讲清）
#
# 豁免对象是 examples/pgstore（PostgreSQL 参考存储）。原因：该包的全部用例
# 都是**环境依赖的 integration 用例**——没有可连的 PostgreSQL 时在环境入口
# 显式 Skipf（写明原因与起法；这是环境依赖入口的显式跳过，不是场景级后门，
# 套件场景本身仍硬失败），⇒ 语句覆盖率恒为 0%，对它判「≥70%」判的是
# 「环境有没有库」，不是「代码测没测」。该包的判据由 sidetest / boardtest /
# RunSinkContract 共享契约套件 + SIGKILL 故障恢复 e2e 承担，在 integration
# 层真跑（`make integration` / CI 的 postgres service container / compose 的
# verify profile）。拆除条件：**结构性豁免，无自然到期日**——与 cmd/ptcchild
# 同型（包的性质决定了通用覆盖率判据不适用）；若将来该包长出不依赖数据库的
# 可测逻辑，把那部分判据接进常规层即可缩窄本豁免。
#
# ⚠️ 为什么它**不受**「自己达标即红」约束：那条判据问的是「豁免的理由还在吗」。
# 本包的豁免理由是「包的性质」——环境依赖是**性质**，不是「用例还没写」。
# 本机碰巧有 PostgreSQL ⇒ 覆盖率上去了，但包的性质一点没变 ⇒ 豁免该留着。
# 反过来，「没命中任何包」（包被删了/改名了）仍判红：那时豁免的**对象**没了，
# 这才是真失效。故本表的纪律仍是「失效即红」，只是失效的判据是「对象没了」
# 而不是「自己达标了」。
PKG_COV_STRUCTURAL_EXEMPT :=
# （原 examples/pgstore 条目随包迁至 goagent-runtime v0.67.0，本表当前为空。）
# 零测试文件豁免（逗号分隔，按包路径**后缀**匹配）。同样「失效即红」。
#
# 为什么要这条判据（issue #30）：`go test` 对无测试文件的包输出
# `?  <pkg>  [no test files]`，而覆盖率判据只认带数值的行——`?` 行被跳过。
# 跳过是**刻意**的（它不是「0% 覆盖」，是「不适用」，混进分母会把判据变成
# 「测试文件计数」），但后果是**没有测试文件的包对逐包下限完全隐形**：新增包若
# 整包不写测试，判据一声不吭。判据分不出「合理的零测试」与「漏写测试」——
# 那就别假装分得出，要求显式声明：要么补测试，要么在这里登记并写清为什么。
#
# 当前四项：
#   cmd/archcheck  纯 CLI 装配层（Load/Run/WriteReport 的拼装与 flag 解析）；
#                  判定逻辑全在 archcheck 包，那里有完整用例。
#   cmd/ptcchild   子进程夹具：不作为独立包被调用，而是由 ptc 的用例用
#                  `go build` 编出来当被测子进程（见 ptc/subprocess_exec_test.go）。
#   （原 examples/pgstore/cmd/pgsmoke 条目随包迁往 goagent-runtime，已摘。）
#
# examples/sqliteadapter 的那条临时豁免已于 2026-11-19（issue #424，Task 2）
# 当天拆除：那是 Task 1 登记驱动破例时的**占位包**豁免（占位包无测试文件），
# 骨架落地并写上真用例后它即失效——豁免留着会与逐包下限判据打架（豁免包自己
# 达标 ⇒ cover-gate 报「豁免失效，请从 PKG_NO_TEST_EXEMPT 移除」）。
PKG_NO_TEST_EXEMPT := cmd/archcheck,cmd/ptcchild
# 门禁工具的版本口径。**单一来源在这里**，ci.yml 必须与之一致（versioncheck 判）。
# 运行时还要再断言一次实跑版本（lint / vuln 目标里）——「钉了版本」与「跑的是
# 那个版本」是两件事：命令在 PATH 里只说明门禁会跑，不说明跑的是同一套判据。
LINT_VERSION := 2.14.0
# ci.yml 路径（versioncheck 交叉比对用）。抽成变量是为了让判据本身只有一处
# 引用文件名——改名时不会漏掉某条判据。
CI_YML := .github/workflows/ci.yml
# -race 必须开 cgo：Linux 上 `-race requires cgo`（实测 GOOS=linux 下
# `CGO_ENABLED=0 go test -race -c` 直接报这句）。故 race 目标**不能**跟着
# 全局的 GOENV(CGO_ENABLED=0)——那样它在 CI 上必红。本机 cgo=1 已验证可
# 完整链接全仓（`CGO_ENABLED=1 go test -race -run '^$' ./...` 零输出）。
RACE_ENV := CGO_ENABLED=1
# ── race 的三段预算（RACE_GO_TIMEOUT / cmd/go 兜底闹钟 / RACE_WATCHDOG）──
#
# 三者互相咬合，**改一处必须改三处**：
#
#   RACE_GO_TIMEOUT  传给 `go test -timeout` 的用例超时（Go 自己的看门狗）。
#   cmd/go 兜底闹钟  = RACE_GO_TIMEOUT + 1m（Go 源码里的 testKillTimeout）。
#                    它杀测试二进制时打的那行
#                    「*** Test killed with quit: ran too long (11m0s).」
#                    **正是「启动期楔死」的指纹**——那种形态下 Go 运行时压根
#                    没起来，连 Go 自己的超时都不会触发，只剩 cmd/go 这层闹钟。
#   RACE_WATCHDOG    本工具（cmd/raceprobe）的兜底看门狗，**必须晚于
#                    cmd/go 的兜底闹钟**：早了就变成我们先杀 go test ⇒
#                    拿不到那行指纹 ⇒ 形态判定退化成 unknown（工具自己会拒）。
#
# 为什么需要看门狗：门禁**挂死**是最坏的失败形态——不产出任何结论，读日志的
# 人会误以为「没输出 = 没跑」。本机实测（2026-11-18 第十轮）出现过：race 步骤的
# 若干测试二进制卡在 macOS 动态链接器里（`sample` 显示主线程停在
# `_dyld_start`）——进程在、CPU 占用 0%、**连 Go 自带的用例超时都不触发**。
#
# 触发条件是机器面负载：race 因必须开 cgo ⇒ 二进制**动态链接** ⇒ 启动期要在
# dyld 上抢锁；本机 10 核并行启动几十个测试二进制 + 同时有别的会话在构建
# ⇒ 内存压力下有进程卡死在启动期。与代码无关（单包重跑 1.1~1.6 秒通过、
# 全量重跑全绿），但门禁不能因此无限期挂着。
#
# 取值：本地 race 步骤实测 1~2 分钟，CI 慢机留足余量；
# 真挂死时半小时内必红，远好过无限期挂着。
RACE_GO_TIMEOUT := 10m
RACE_WATCHDOG := 30m
# 取证落盘目录（原始输出 + 结论块）。默认落临时目录，不污染工作树。
RACE_EVIDENCE_DIR ?= $(or $(TMPDIR),/tmp)/goagent-kernel-race
# 启动期楔死时是否做串行复核（反事实实验）。默认开：复核通过 ⇒ 判「环境面已
# 复核」（判绿但留痕），复核失败 ⇒ 按复核结论判红。
RACE_RECHECK ?= true
# ── 测试阶段的单包超时（TEST_GO_TIMEOUT）──
#
# 为什么必须显式给：Go 的默认单包上限是 10m，而本仓有一个**天然分钟级**
# 的包——`cmd/seamcheck` 的扫描用 packages.Load(NeedTypes|NeedTypesInfo|
# NeedDeps) 对**全仓 + 全部依赖**做完整类型检查。实测（2026-11-20，本机
# load average 280~380）：
#   单独跑（带 -cover）        489.346s（RC=0）
#   在 cover-gate 里与全仓并行  602s ⇒ 撞穿 Go 默认 10m ⇒ 门禁红
# ⇒ 判据的**量纲**（10m 默认上限）与**前提**（这个包的成本）在负载下对不齐。
#
# 取值 15m：与本仓既有做法一致（examples/pgstore 15m、
# examples/containerbackend 20m）。
# ⚠ 这**不是**放宽判据：真挂死仍然是红（15 分钟后必红），只是不再被机器
# 负载放大出来的排队时间误判。根因（seamcheck 全仓类型检查太贵）另开单跟踪。
TEST_GO_TIMEOUT := 15m
# 交叉构建矩阵：只声明**真的验过**的平台。
# windows 回归在列（issue #34 收尾）：上游 runtime/procx 已落地 windows
# 进程树路径（Job Object，runtime v0.11.0 起），windows/amd64 恢复交叉构建
# 门禁；「跑得对」由 CI 的 windows job 真执行测试（见 ci.yml）。
CROSS_TARGETS := linux/amd64 linux/arm64 darwin/arm64 windows/amd64
# govulncheck 版本口径：CI 装这个版本，与本机验证过的一致（同 lint 的理由）。
GOVULNCHECK_VERSION := v1.7.0
# 性能基线五包（issue #101）：基线是记录不是提交前门禁，bench 目标不在 check 里。
# 数字口径与演进纪律见 docs/BENCH-BASELINE-20261113.md。
BENCH_PKGS := ./ptc/ ./guardset/ ./checkpoint/ ./taskboard/ ./sideeffect/
BENCH_PATTERN := BenchmarkPTCRunInProcess|BenchmarkGuardSet|BenchmarkCheckpoint|BenchmarkTaskBoard|BenchmarkSideEffect

.PHONY: build fmt-check vet lint test race contract fault-injection gates cover cover-gate vuln crossbuild versioncheck architecture-check architecture-check-types docsync seamcheck apilifecheck deploycheck storagecheck ledgercheck issue-state releasecheck integration bench site check tidy devlink devunlink hygiene clean

build: ## 编译架构门禁二进制
	$(EXPORT) $(GOENV) go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BIN) ./cmd/$(BIN)

# fmt-check 的文件集由 git 决定，不由 `.` 递归决定。
#
# 为什么不用 `gofmt -l .`：devlink 会在仓内留下 `.*-work/`（当前 .toolgate-work
# 174MB），里面是 Go 官方与各库的 testdata——**故意写坏语法的样本**。gofmt 扫到
# 它们会刷几十行 expected type found ')' 的 stderr，而 `grep -v '^\.'` 只能过滤
# stdout，接不住 stderr。实测 fmt-check 被拖到 12 秒，真违规淹没在噪音里。
#
# git ls-files 覆盖「已跟踪」，--others --exclude-standard 覆盖「新写但没 add」
#（如本仓测试夹具），两者合起来正好等于「gitignore 之外的全部 Go 源码」——
# 第三方工作副本已被 .gitignore 收走，天然不在文件集里。CI 里两者都在版本库中，
# 覆盖面不变。
fmt-check:
	@files=$$(gofmt -l $$(git ls-files '*.go'; git ls-files --others --exclude-standard '*.go') || true); \
	if [ -n "$$files" ]; then echo "gofmt 未格式化文件:"; echo "$$files"; exit 1; fi

vet:
	$(EXPORT) $(GOENV) go vet ./...

# lint 版本口径：CI 装 v2.14.0（见 ci.yml），与本机验证过的版本一致。
# **lint 原先既不在 check 也不在 CI**——它在哪儿都不跑，于是
# `archcheck` 包的死变量、`protocol` 的孤儿谓词这类问题会一路存活到没人查。
# 「从没见过它触发」的规则等于没有规则，同理「没人跑的门禁」也不等于有门禁，
# 故按 AGENTS.md「门禁本体全部在 check 里」把它接进来。CI 侧只负责把二进制装好，
# 规则本体仍在本 Makefile——不放到 ci.yml 里，避免变成「推送后门禁」。
lint: ## 静态检查（未安装 / 版本不符都明确报错退出，与宿主仓同口径）
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint 未安装：brew install golangci-lint"; exit 1; }
	@# 版本断言（issue #37）：`command -v` 只说明门禁**会跑**，不说明跑的是
	@# **同一套判据**。静态分析器的版本决定判定结果（新增 check / 默认配置调整 /
	@# 误报修正），于是版本漂移的两个方向都不好：本机更严 ⇒ 本地红、CI 绿，人会去
	@# 改代码而 CI 永远不会同意；本机更松 ⇒ 本地绿、CI 红，被当成「CI 抽风」，
	@# 而实际漏了一条真问题。
	@#
	@# 不自动降级、不自动安装：版本不符是「判据与声明不一致」，属于必须由人决定的
	@# 动作——静默换版本等于让门禁自己决定自己被谁判。
	@got=$$(golangci-lint --version 2>/dev/null | awk '{print $$4}'); \
	[ "$$got" = "$(LINT_VERSION)" ] || { \
	  echo "golangci-lint 版本不符：Makefile 声明 $(LINT_VERSION)，PATH 里是 '$$got'"; \
	  echo "装对应版本：brew install golangci-lint@$(LINT_VERSION) 或按官方 install.sh 装 v$(LINT_VERSION)"; \
	  exit 1; }
	$(EXPORT) $(GOENV) golangci-lint run ./...

test:
	$(EXPORT) $(GOENV) go test ./... -count=1 -timeout $(TEST_GO_TIMEOUT)

race: ## 竞态检测（**开 cgo**：Linux 上 -race 强制要求 cgo，见 RACE_ENV 注释）
	@# 这条门禁此前从未被跑过：race 目标存在，但既不在 check 的依赖链里，
	@# ci.yml 也全文没有它 ⇒ 「有没有数据竞争」在本仓永远没人判。
	@# 竞态是本类内核最贵的一类缺陷（代次/闸/共享 map 都在并发路径上，
	@# 而它们的失效形态是「偶尔给错答案」，单线程用例永远绿）。
	@#
	@#
	@# **本轮起由 cmd/raceprobe 接管**（2026-11-19，第十七轮）。
	@# 门禁失败时 Go 留下的痕迹有三种，**处置方式完全不同**：
	@#   ① 数据竞争（代码面）② 用例挂死（代码面，带 goroutine 栈）
	@#   ③ 启动期楔死（环境面：**没有 goroutine 栈**，Go 运行时压根没起来）
	@# ③ 在原始日志里长得像 ②，而唯一可靠的区分特征就是「有没有栈」。
	@# 2026-11-18 实录：一次 11m0s/signal:quit 的失败被归因为「某包用例挂死」，
	@# 而那个包零计时敏感点（无 time.*、无 goroutine/channel、无 os/exec）——
	@# 它的用例根本没有能挂住的东西 ⇒ 归因错了，处置必然错（改代码没用，
	@# 要查机器面）。raceprobe 把这个区分做成**机器判定**，不再靠人猜。
	@#
	@# raceprobe 做三件事：**跑**（输出实时透传 + 全量落盘）、**判**（形态分类，
	@# 不猜）、**复核**（形态是③时做串行复核：同树 `-p 1` 重跑受害包 ⇒
	@# 反事实实验；复核通过 ⇒ 判「环境面已复核」并留痕；复核失败 ⇒ 按复核
	@# 结论判红）。
	@#
	@# 看门狗仍是硬约束：到点杀**整棵进程树**（非零退出 ⇒ 门禁红），不留挂死。
	@# 那两条被实测打出来的实现约束仍然成立，且现在由结构保证：
	@# ① **不得持有父进程的 stdout**：子进程输出走 os/exec 自建的管道，本进程
	@#   写完就退 ⇒ 写 `make check | tail` 的那端不会等 EOF，也不会把非零
	@#   退出码吞成 tail 的 0（「红」看起来像「绿」）。
	@# ② **不得留下孤儿**：unix 下子进程进独立进程组，超时按组杀 ⇒ go test 起的
	@#   测试二进制（卡在启动期的那个永不退出）一并带走。
	@$(EXPORT) $(GOENV) go run ./cmd/raceprobe \
	  -packages ./... \
	  -timeout $(RACE_GO_TIMEOUT) \
	  -watchdog $(RACE_WATCHDOG) \
	  -out $(RACE_EVIDENCE_DIR) \
	  -recheck $(RACE_RECHECK)

cover:
	$(EXPORT) $(GOENV) go test ./... -coverprofile=coverage.out -count=1 -timeout $(TEST_GO_TIMEOUT)
	go tool cover -func=coverage.out | tail -1

cover-gate: ## 覆盖率阈值门禁（全局门槛 + 逐包下限；阈值应随内核成熟度上调）
	@# 测试输出先落临时文件：成功时静默（与原口径一致），失败时把日志打出来。
	@#
	@# 原口径是 `go test ... >/dev/null`——**用例失败也只剩一句 "Error 1"**。
	@# 红是红了，但看不出哪个用例、为什么红。而本仓的纪律是「门禁的失败形态
	@# 是变红」：看不见原因的变红与「常红」同样会训练人忽略门禁。
	@#
	@# 为什么这个失败路径值得单独修：`make check` 里 `test` 目标先跑一遍同一套
	@# 用例且**输出可见**，所以 cover-gate 自己失败只可能是「第一次绿、第二次红」
	@# ——即**抖动**。而抖动是唯一一次能看见抖动用例的机会，输出被丢掉就再也
	@# 没线索了（本轮实测撞到过一次）。
	@#
	@# 两条判据：全局门槛（COV_MIN）管「整体没退步」，逐包下限（PKG_COV_MIN）
	@# 管「没有一个包躲在大盘后面」。两者判的是两件事，故都要跑；判定细则见
	@# cmd/pkgcov 的头注（原 scripts/pkgcov.awk，issue #36 收编为 Go 执行体）。
	@#
	@# 逐包下限有**两张豁免表**（语义差别见上面各自的注释块）：
	@# PKG_COV_EXEMPT（普通：豁免包自己达到下限 ⇒ 红，当天拆）与
	@# PKG_COV_STRUCTURAL_EXEMPT（结构性：环境依赖型，不受「自己达标即红」约束，
	@# 但没命中任何包 ⇒ 仍红）。两张表传同一个执行体的两个 flag。
	@#
	@# 日志必须留到逐包判定之后再删：原实现「成功即 rm 日志」，于是逐包数字在
	@# 判定前就已经没了——这正是它此前只能判全局的实现层原因。
	@#
	@# ⚠️ 注释只能写在块**外**：下面是一个用 `\` 续行的 shell 块，块内出现的 `#`
	@# 会把该行连同续行符一起变成注释，报 "syntax error: unexpected end of file"。
	@log=$$(mktemp); \
	$(EXPORT) $(GOENV) go test ./... -cover -coverprofile=coverage.out -count=1 -timeout $(TEST_GO_TIMEOUT) >"$$log" 2>&1 || { \
	  echo "覆盖率测试未通过——失败详情："; \
	  cat "$$log"; \
	  rm -f "$$log"; \
	  exit 1; \
	}; \
	total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print substr($$3, 1, length($$3)-1)}'); \
	echo "harness 覆盖率: $$total% (门槛 $(COV_MIN)%)"; \
	if ! awk -v t="$$total" -v m="$(COV_MIN)" 'BEGIN { exit (t+0 >= m+0) ? 0 : 1 }'; then \
	  echo "覆盖率 $$total% 低于阈值 $(COV_MIN)%"; \
	  rm -f "$$log"; \
	  exit 1; \
	fi; \
	if ! $(EXPORT) $(GOENV) go run ./cmd/pkgcov -min $(PKG_COV_MIN) -exempt "$(PKG_COV_EXEMPT)" -structural-exempt "$(PKG_COV_STRUCTURAL_EXEMPT)" -no-test-exempt "$(PKG_NO_TEST_EXEMPT)" <"$$log"; then \
	  rm -f "$$log"; \
	  exit 1; \
	fi; \
	rm -f "$$log"

vuln: ## 依赖与标准库漏洞扫描（govulncheck）
	@# 这条同样此前从未进过门禁。价值不在「扫出第三方 CVE」——本仓依赖面窄，
	@# 而在**标准库**：2026-11-06 首次跑出 6 条，全部在 stdlib（crypto/x509、
	@# net/http 系列、crypto/tls），修法是抬 go 指令到 1.27.3。而这类漏洞的
	@# 载体正是「下游用本仓编出来的那个二进制」，本仓不判就没人判。
	@#
	@# 未安装时明确报错退出（与 lint 同口径），不静默跳过：门禁缺失的形态必须是
	@# 变红，不能是变绿。
	@command -v govulncheck >/dev/null 2>&1 || { \
	  echo "govulncheck 未安装：go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)"; \
	  exit 1; }
	@# 版本断言（issue #37）：与 lint 同一条理由，但这一条更硬——AGENTS.md 那条
	@# 「修标准库漏洞要抬 go 指令、不是 toolchain 指令」的结论**正是靠 v1.7.0 的
	@# 输出**得出的（6 条 stdlib 漏洞的清单来自它）。扫描器悄悄换了版本，结论的
	@# 依据就变了，而门禁的绿会照旧。
	@#
	@# 只断言 **Scanner**，不断言 `Go:` 行：那行报的是**本地工具链**版本，比 go.mod
	@# 的 go 指令新是合法状态（下游照样能用本仓的地板编出无漏洞的二进制），
	@# 断言相等会在任何较新工具链的机器上假红。
	@got=$$(govulncheck -version 2>/dev/null | awk -F'@' '/^Scanner: /{print $$2}'); \
	[ "$$got" = "$(GOVULNCHECK_VERSION)" ] || { \
	  echo "govulncheck 版本不符：Makefile 声明 $(GOVULNCHECK_VERSION)，PATH 里是 '$$got'"; \
	  echo "装对应版本：go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)"; \
	  exit 1; }
	$(EXPORT) $(GOENV) govulncheck ./...

# ── 门禁工具版本口径（issue #32）────────────────────────────────
#
# 门禁工具的版本**决定判定结果**，而它此前写在两个地方：Makefile 的
# GOVULNCHECK_VERSION 与 ci.yml 的安装行。改一处忘另一处 ⇒ CI 装旧版、本地跑新版
# ⇒「本地绿、CI 红」（或反过来，更糟：CI 绿而本地红，人会以为门禁误报）。
#
# 单一来源是**本文件**：LINT_VERSION / GOVULNCHECK_VERSION。ci.yml 只负责把
# 对应版本的二进制装进 PATH，版本号必须与这里一致。
#
# 为什么不让 ci.yml 反向读 Makefile（如 print-govulncheck-version）：那会让
# 「装什么版本」变成 CI 去解析 Makefile 输出的脆弱动作——判据留在 Makefile、
# 声明也留在 Makefile，CI 只管安装，边界才清楚。
versioncheck: ## 门禁工具版本口径：ci.yml 安装的版本必须与 Makefile 声明一致（issue #32）
	@# 两处版本号不一致即红，且把**两边各自的值**都打出来——只说「不一致」时，
	@# 人要自己去两个文件里翻，而门禁报错的成本恰恰在这里。
	@# 用「取全部唯一值」而不是「取第一个」：ci.yml 里若同一工具出现两次且版本
	@# 不同，取第一个会把「自身内部不一致」放过去。
	@lint_ci=$$(grep -o 'bin" v[0-9][0-9.]*' $(CI_YML) | sed 's/.*" //' | sort -u | tr '\n' '|'); \
	vuln_ci=$$(grep -o 'govulncheck@v[0-9][0-9.]*' $(CI_YML) | sed 's/.*@//' | sort -u | tr '\n' '|'); \
	ok=1; \
	[ "$$lint_ci" = "v$(LINT_VERSION)|" ] || { \
	  echo "golangci-lint 版本不一致：Makefile v$(LINT_VERSION)，ci.yml '$$lint_ci'"; ok=0; }; \
	[ "$$vuln_ci" = "$(GOVULNCHECK_VERSION)|" ] || { \
	  echo "govulncheck 版本不一致：Makefile $(GOVULNCHECK_VERSION)，ci.yml '$$vuln_ci'"; ok=0; }; \
	[ "$$ok" = 1 ] || { \
	  echo "修法：改 ci.yml 的安装行，或改 Makefile 的 LINT_VERSION / GOVULNCHECK_VERSION（两边必须同步）"; \
	  exit 1; }; \
	echo "门禁工具版本口径一致 ✓（golangci-lint v$(LINT_VERSION) / govulncheck $(GOVULNCHECK_VERSION)）"

crossbuild: ## 交叉构建矩阵（声明支持的平台必须真的编得过）
	@# 为什么这条要有：本仓是**给宿主当依赖编进二进制**的，本机 darwin 绿不等于
	@# linux 绿。此前没有任何东西判这件事——2026-11-06 实测 GOOS=linux 能过、
	@# GOOS=windows 编不过（上游 procx 的 unix syscall），而这个事实从未被记录。
	@# 判据取 `go build ./...`：它覆盖全部生产代码，且不需要 cgo。
	@for t in $(CROSS_TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; \
	  echo "交叉构建 $$os/$$arch"; \
	  $(EXPORT) CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build ./... || exit 1; \
	done
	@echo "交叉构建矩阵 ✓（$(CROSS_TARGETS)）"

architecture-check: ## 架构治理门禁（域词污染红线 + 依赖方向）
	$(EXPORT) $(GOENV) go run ./cmd/archcheck $(if $(findstring --report,$(MAKECMDGOALS)),--report,)

# 严格档是**可选档**（issue #103）：整仓类型检查慢一个量级，opt-in 不进 check。
# 「可选」不等于「可静默」：类型解析失败显式报错（退出码 2），开着却没跑过
# 会在报告里以包数凭据显形。
# 刷新外部依赖清单的**骨架**（issue #482）：实测触达 + 保留人的判断字段。
# 人只维护 role / seam / exempt / reason；新出现的条目 role 留空 ⇒ 门禁判红，等人判定。
external-deps: ## 刷新外部依赖清单骨架（judgment 字段保留；新条目 role 留空 ⇒ 门禁判红）
	$(EXPORT) $(GOENV) go run ./cmd/archcheck -update-external-deps

architecture-check-types: ## 架构门禁严格档（go/types 类型感知：覆盖赋值形态盲区，慢，opt-in）
	$(EXPORT) $(GOENV) go run ./cmd/archcheck -types $(if $(findstring --report,$(MAKECMDGOALS)),--report,)

# ── K8s 出站禁网真集群验收（issue #418）──────────────────────────────
#
# 为什么不在 check 里：它要一台**真的实现了 NetworkPolicy 的集群**——数据面
# 行为没有任何替身可证（httptest 替不出「apiserver 接受策略 ≠ 数据面生效」）。
# check 必须在任何机器上一条命令可跑 ⇒ 这条不进 check；CI 侧同样不进：
# 起集群 + 装 Calico 要拉镜像，抖动会连带把别的门禁染红。
#
# 判据本体在 examples/containerbackend/k8sjob_realcluster_test.go：同一台集群、
# **同一条**探针脚本、**同一个**目标，只在「有没有禁网策略」这一个变量上不同
# （对照 Pod 必须连上、禁网作业必须连不上）⇒ 结论不会被「探针压根不通」冒充。
#
# 缺 docker / kind / kubectl 任一样都**明确报错退出**，不静默跳过（与
# integration 同一条纪律）。跑完默认拆集群（K8S_E2E_KEEP=1 保留）。
#
# 复用已有集群：给齐 GAK_K8S_E2E_* 六个环境变量后直接跑
#   go test ./examples/containerbackend/ -run '^TestK8sRealCluster' -v -count=1
K8S_E2E_CLUSTER ?= gak-e2e
K8S_E2E_NS ?= gak-e2e
K8S_E2E_PROBE_IMAGE ?= gak-egress-probe:local
K8S_E2E_CALICO_VERSION ?= v3.30.3
K8S_E2E_NODE_IMAGE ?= kindest/node:v1.31.0
K8S_E2E_WORKDIR ?= $(or $(TMPDIR),/tmp)/goagent-kernel-k8s-e2e
K8S_E2E_KEEP ?= 0

k8s-e2e: ## K8s 出站禁网真集群验收（自建 kind+Calico 集群，跑完即拆；issue #418）
	@command -v docker >/dev/null 2>&1 || { \
	  echo "docker 未安装：真集群验收要起 kind 集群（数据面行为没有替身可证）。"; exit 1; }
	@command -v kind >/dev/null 2>&1 || { \
	  echo "kind 未安装：go install sigs.k8s.io/kind@latest"; exit 1; }
	@command -v kubectl >/dev/null 2>&1 || { \
	  echo "kubectl 未安装：brew install kubectl"; exit 1; }
	@mkdir -p $(K8S_E2E_WORKDIR)
	@set -e; \
	prev_ctx=$$(kubectl config current-context 2>/dev/null || true); \
	trap 'if [ "$(K8S_E2E_KEEP)" = "1" ]; then echo "▸ 保留集群 $(K8S_E2E_CLUSTER)"; else kind delete cluster --name $(K8S_E2E_CLUSTER) >/dev/null 2>&1 || true; fi; if [ -n "$$prev_ctx" ]; then kubectl config use-context "$$prev_ctx" >/dev/null 2>&1 || true; fi' EXIT; \
	echo "▸ 构建探测镜像（非 root + shell 入口点：作业 Pod 级 runAsNonRoot 必需）"; \
	docker build -q --provenance=false --sbom=false -t $(K8S_E2E_PROBE_IMAGE) -f examples/containerbackend/e2e/Dockerfile.probe examples/containerbackend/e2e; \
	echo "▸ 起 kind 集群 $(K8S_E2E_CLUSTER)（关自带 CNI：kindnet 不实现 NetworkPolicy）"; \
	kind create cluster --name $(K8S_E2E_CLUSTER) --image $(K8S_E2E_NODE_IMAGE) --config examples/containerbackend/e2e/kind-cluster.yaml; \
	kind export kubeconfig --name $(K8S_E2E_CLUSTER) >/dev/null; \
	for i in $$(seq 1 90); do kubectl get nodes >/dev/null 2>&1 && break; sleep 2; done; \
	kubectl get nodes >/dev/null || { echo "apiserver 180s 内没起来（kind 集群起失败）"; exit 1; }; \
	echo "▸ 预载探测镜像进节点（本地构建，仓库里没有 ⇒ 必须预载）"; \
	kind load docker-image $(K8S_E2E_PROBE_IMAGE) --name $(K8S_E2E_CLUSTER) || exit 1; \
	echo "▸ 预载 Calico 镜像进节点（节点不能自拉时必需；预载失败不致命——节点若能访问镜像仓库，kubelet 会自拉）"; \
	node_arch=$$(docker exec $(K8S_E2E_CLUSTER)-control-plane uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/'); \
	for img in node cni kube-controllers; do \
	  ref=calico/$$img:$(K8S_E2E_CALICO_VERSION); \
	  docker pull -q $$ref >/dev/null 2>&1 || true; \
	  if kind load docker-image $$ref --name $(K8S_E2E_CLUSTER) >/dev/null 2>&1; then \
	    echo "  已预载 $$ref（kind load）"; \
	  else \
	    echo "  kind load 失败（多架构镜像在 containerd 存储下常见）⇒ 改走单架构导出 + ctr 导入"; \
	    docker save --platform linux/$$node_arch $$ref -o $(K8S_E2E_WORKDIR)/calico-$$img.tar || exit 1; \
	    docker exec --privileged -i $(K8S_E2E_CLUSTER)-control-plane ctr --namespace=k8s.io images import --all-platforms --digests --snapshotter=overlayfs - < $(K8S_E2E_WORKDIR)/calico-$$img.tar >/dev/null || exit 1; \
	    echo "  已预载 $$ref（ctr 导入）"; \
	  fi; \
	done; \
	echo "▸ 装 Calico（NetworkPolicy 数据面）"; \
	if [ ! -s $(K8S_E2E_WORKDIR)/calico.yaml ]; then \
	  curl -fsSL --retry 3 --retry-all-errors --retry-delay 2 -o $(K8S_E2E_WORKDIR)/calico.yaml https://raw.githubusercontent.com/projectcalico/calico/$(K8S_E2E_CALICO_VERSION)/manifests/calico.yaml || { \
	    echo "拉取 Calico 清单失败（网络不通？）：https://raw.githubusercontent.com/projectcalico/calico/$(K8S_E2E_CALICO_VERSION)/manifests/calico.yaml"; exit 1; }; \
	else \
	  echo "  复用已下载的 Calico 清单 $(K8S_E2E_WORKDIR)/calico.yaml"; \
	fi; \
	kubectl apply -f $(K8S_E2E_WORKDIR)/calico.yaml; \
	kubectl wait --for=condition=Ready node --all --timeout=300s || { \
	  echo "节点没能在 300s 内 Ready ⇒ Calico 数据面没起来（节点不能自拉镜像？预载失败？）"; \
	  kubectl -n kube-system get pods -o wide 2>&1 | head -20; exit 1; }; \
	echo "▸ 节点已 Ready（NetworkPolicy 数据面生效）"; \
	echo "▸ 准备执行命名空间与凭据（SA + cluster-admin 绑定 + token + CA）"; \
	kubectl create namespace $(K8S_E2E_NS) 2>/dev/null || true; \
	kubectl -n $(K8S_E2E_NS) create serviceaccount gak-e2e 2>/dev/null || true; \
	kubectl create clusterrolebinding gak-e2e-admin --clusterrole=cluster-admin --serviceaccount=$(K8S_E2E_NS):gak-e2e 2>/dev/null || true; \
	kubectl -n $(K8S_E2E_NS) create token gak-e2e > $(K8S_E2E_WORKDIR)/token; \
	kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > $(K8S_E2E_WORKDIR)/ca.crt; \
	echo "▸ 跑真集群验收（阳性对照 / 禁网作业 / 叠加语义）"; \
	status=0; \
	GAK_K8S_E2E_ENDPOINT=$$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}') \
	GAK_K8S_E2E_TOKEN_FILE=$(K8S_E2E_WORKDIR)/token \
	GAK_K8S_E2E_CA_FILE=$(K8S_E2E_WORKDIR)/ca.crt \
	GAK_K8S_E2E_NAMESPACE=$(K8S_E2E_NS) \
	GAK_K8S_E2E_PROBE_IMAGE=$(K8S_E2E_PROBE_IMAGE) \
	GAK_K8S_E2E_JOB_IMAGE=$(K8S_E2E_PROBE_IMAGE) \
	$(GOENV) go test ./examples/containerbackend/ -run '^TestK8sRealCluster' -v -count=1 -timeout 20m || status=1; \
	if [ $$status -ne 0 ]; then echo "K8s 出站禁网真集群验收未通过（见上方输出）"; exit 1; fi; \
	echo "K8s 出站禁网真集群验收 ✓（阳性对照连上 / 禁网作业连不上 / 叠加语义拒绝创建）"

# ── 测试分层（issue #102）────────────────────────────────────────────
#
# check 是「提交前必须全绿」的聚合（门禁本体全在这里），但本地与 CI 都需要
# **只跑一层**的入口：本地反馈要快（改一个包不必等全仓竞态），CI 分 job 要
# 每层独立给信号（race 红不该把故障注入的信号一起淹没——混在一条链里时
# 「哪个层红了」要靠读日志，拆开后失败的就是失败的层）。
#
# 五层与 ci.yml 的 job 一一对应（unit / race / contract / integration /
# fault-injection）。跳过一律显式：unit 层用 -skip 排除故障注入（那里有自己的
# 层），集成层无 docker 时明确报错（见 integration）——没有静默跳过。
#
# unit：静态门禁 + 全量单测（排除故障注入）。快速路径——fmt / vet / lint /
# 单测一条命令跑完，本地改完代码先跑它。
#
# ⚠️ 这一层必须**覆盖 make check 里的每一条静态门禁**：CI 执行的是**分层目标**
# （unit / race / contract / fault-injection / gates / integration），**不是**
# `make check` 本身 ⇒ 只挂在 `check` 上而没进任何分层目标的门禁，**CI 上从来
# 不会跑**（2026-11-18 实测发现 deploycheck / releasecheck 就是这样）。本层现在
# 带上 deploycheck / ledgercheck / releasecheck（后者在非 tag 提交上显式跳过，
# 在 tag 推送时判那四条发版纪律）。
unit: fmt-check vet lint
	$(EXPORT) $(GOENV) go test ./... -count=1 -skip '^TestFault' -timeout $(TEST_GO_TIMEOUT)
	$(MAKE) --no-print-directory architecture-check docsync seamcheck apilifecheck deploycheck storagecheck ledgercheck releasecheck modreplace modwork hygiene cijobname

# contract：契约套件层（sidetest / taskboard/boardtest 的套件自测）。判「共享
# 契约套件本身是绿的」——宿主实现跑套件前先知道判据自身没坏。
contract:
	$(EXPORT) $(GOENV) go test ./sidetest/... ./taskboard/boardtest/... -count=1
	@echo "契约套件自测 ✓（sidetest + boardtest）"

# fault-injection：故障注入层（checkpoint 的注入矩阵 + ptc 的注入场景 +
# worker/faultmatrix 的跨部署模式故障矩阵，issue #193）。
# -run '^TestFault' 只跑注入用例：这类用例构造的是「坏实现必红」的对照形态，
# 与普通单测分开成层，是为了它红的时候信号不被大批绿灯淹没。
# -v 让每个注入场景的失败形态直接可见（这类用例的红就是它们的文档）。
#
# **2026-11-22 起由 raceprobe 接管**（§26 建议 2：「会挂死」的门禁统一形态）——
# 此前它只回「绿 / 红」，红的形态（挂死 vs 真断言失败 vs 启动期楔死）靠人读
# 日志猜，而处置方向完全不同。现在与 race 门禁**共用同一套判据与取证**：
# 跑（输出实时透传 + 全量落盘）→ 判（raceprobe.Classify 六形态）→ 取证
# （原始输出 + 形态结论落盘）。-race=false：本层不判竞态，绑 cgo 只会白慢一轮
# （childEnv 随开关注入 CGO_ENABLED，与 GOENV 同口径）。
FAULT_EVIDENCE_DIR ?= $(or $(TMPDIR),/tmp)/goagent-kernel-fault
FAULT_GO_TIMEOUT := 7m
FAULT_WATCHDOG := 10m

fault-injection:
	@$(EXPORT) $(GOENV) go run ./cmd/raceprobe \
	  -packages ./... \
	  -run '^TestFault' \
	  -v \
	  -race=false \
	  -label fault-injection \
	  -timeout $(FAULT_GO_TIMEOUT) \
	  -watchdog $(FAULT_WATCHDOG) \
	  -out $(FAULT_EVIDENCE_DIR)
	@echo "故障注入层 ✓（TestFault* 全过）"

# gates：与测试正交的静态门禁聚合（CI 的 gates 层与本地复跑的等价入口）。
# 它们不是测试层——覆盖率阈值 / 交叉构建矩阵 / 漏洞扫描 / 工具版本口径，
# 但同样要独立红绿：覆盖率塌方不该被 race 的抖动盖住。
gates: versioncheck cover-gate vuln crossbuild
	@echo "静态门禁聚合 ✓（versioncheck + cover-gate + vuln + crossbuild）"

bench: ## 性能基线复跑（issue #101；独立目标，不在 check——基线是记录不是门禁）
	$(EXPORT) $(GOENV) go test -run '^$$' -bench '$(BENCH_PATTERN)' -benchmem -count=1 $(BENCH_PKGS)
	@echo "基线数字与口径见 docs/BENCH-BASELINE-20261113.md（allocs/op 是判据，ns/op 是参照）"

docsync: ## README 包表 ↔ 仓库实际（生产行数 / 外部依赖）一致性门禁
	$(EXPORT) $(GOENV) go run ./cmd/docsync

seamcheck: ## 接缝契约成文率统计与棘轮（声明 / 调用点 / 三条写齐数，issue #400）
	@# 统计口径与棘轮规则见 cmd/seamcheck 包头：接缝集合与基线逐条相等，
	@# 每个接缝已写的约束只增不减，fully_written 只允许上升。基线文件
	@# seam-baseline.json 是**机器生成**的清单，改代码后先跑 -update 评审。
	$(EXPORT) $(GOENV) go run ./cmd/seamcheck

releasecheck: ## 发版纪律门禁（annotated tag / CHANGELOG 含本版段 / 无编码损坏 / 区间无混装提交）
	@# 只在 **tag 快照** 上判：HEAD 无 exact tag（普通提交 / PR 分支）时显式跳过
	@# 并 exit 0，故可安全挂进 check——日常绿，打 tag 后必判。
	@# 四条纪律此前只写在 AGENTS.md「发版纪律」与 AI-GUIDE §3.6 里靠人核对，
	@# 而它们各自对应一种「只有下游才会发现」的残缺（tag 快照缺本版说明 /
	@# 编码损坏 / 行为变更藏进依赖段 / 轻量 tag 无说明）。
	$(EXPORT) $(GOENV) go run ./cmd/releasecheck

apilifecheck: ## API 生命周期门禁（清单 ↔ 包注释 ↔ 生产包覆盖 ↔ 错误登记表，issue #113/#104）
	@# 与 architecture-check / docsync 同一条纪律：声明面（api-lifecycle.yaml +
	@# 包注释标记 + 错误语义总表）必须有机器判据，否则「文档里写了但代码里
	@# 没改」的漂移没有任何信号。挂进 check 而不是 ci.yml 新 job——门禁本体
	@# 在本仓的 Makefile 里，推送后才说话的门禁等于没有门禁。
	$(EXPORT) $(GOENV) go run ./cmd/apilifecheck

deploycheck: ## 部署配方能力清单门禁（docs/DEPLOY-MATRIX.yaml ↔ USAGE / 契约 / issue / 门禁，issue #277）
	@# 清单里的每条能力都要有可查事实：contract 走 go/ast 判符号声明，gate 判
	@# Makefile 目标存在，issue 判单号在 docs/*.md 或 CHANGELOG 有落点；state /
	@# readiness 由证据推导（手写结论与证据不符即红）。USAGE 的机器口径块与
	@# 清单逐行比对——口径不一致时这条判据先响。
	$(EXPORT) $(GOENV) go run ./cmd/deploymatrix

storagecheck: ## 存储选型判据门禁（docs/STORAGE-SELECTION.yaml ↔ 部署矩阵 / 代码落点，issue #424）
	@# 回答「什么时候用 SQLite、什么时候用 PostgreSQL」——答案不靠人记：场景声明的
	@# storage 必须在选型声明面的允许集内 + 该 storage 必须实际提供场景必需的全部
	@# 能力 + 「该 storage 提供该能力」的断言必须能在代码里找到落点（go/ast 判符号
	@# 声明）。第三条是本门禁与「写份文档让人读」的分界：没有它，「SQLite 提供租户
	@# 隔离」就成了一句没人验证的话。
	$(EXPORT) $(GOENV) go run ./cmd/storagecheck .

ledgercheck: ## 演进台账纪律门禁（单号状态一致 / 关闭 PR 号 / 无主单号，台账 §15 建议 1）
	@# 台账回答「这条单清了没」，此前这个答案只存在于散文里，机器读不出来——
	@# 代价是台账 §15 记着的一次真实翻车：一批单早已由 8 个 PR 合入主干，
	@# 接手的人按「单还挂着 open 标签」判断「还没人做」，整条工作树作废。
	@# 本门禁把「单号 → 状态」变成机器可查的事实（internal/ledgerstatus 是唯一
	@# 派生器），并当场判四条：状态矛盾 / 已合入却没给关闭 PR / 关闭 PR 号形态
	@# 不对 / 正文引用的单号没进登记面（单号漂浮 = 无主）。
	$(EXPORT) $(GOENV) go run ./cmd/ledgercheck

# ── issue 状态快照的数据源（issue #455）─────────────────────────────
# 台账是**声明面**、GitHub 是**事实面**，两者会漂：2026-11-21 实录——台账把
# #427/#428/#430 记成「已合入 (#436)」、代码也逐条核实落地，而 gh issue list 里
# 三条**全部还开着**（当轮手工关闭）。
#
# 判据必须**离线可跑**（CI 不一定有 GitHub token / 网络）⇒ 判据面与数据源分离：
# 本目标是**唯一允许调 gh 的地方**，它刷新 docs/issue-state.yaml；
# make ledgercheck 只读那份快照、一个网络调用都没有。
#
# ⚠ 本目标**不在** make check 里（它需要网络）。发版前跑一次即可。
issue-state: ## 刷新 docs/issue-state.yaml（需要 gh 与网络；ledgercheck 只读它、不联网）
	@command -v gh >/dev/null 2>&1 || { echo "issue-state: 需要 gh CLI（https://cli.github.com/）"; exit 1; }
	@{ \
	  echo "# 由 make issue-state 刷新——手改无效（重跑会整份覆盖）。"; \
	  echo "# 门禁（make ledgercheck）只读本文件、不联网；刷新需要 gh 与网络。"; \
	  echo "updated: $$(date -u +%F)"; \
	  echo "issues:"; \
	  gh issue list --state all --limit 1000 --json number,state \
	    --jq '.[] | "  \(.number): \(.state|ascii_downcase)"'; \
	} > docs/issue-state.yaml
	@echo "issue-state: 已刷新 docs/issue-state.yaml（$$(grep -c ': ' docs/issue-state.yaml) 条状态）"

site: ## 构建 VuePress 文档站到 site/docs/.vuepress/dist（gen.mjs 先生成机器事实页，解析失败即退出）
	cd site && (test -d node_modules || npm ci --no-audit --no-fund) && npm run docs:build

# ── 仓库卫生门禁（issue #1 的修法②）────────────────────────────────
# 这两条原先只挂在 ci.yml 上，于是本地 `make check` 跑不到——门禁要等推到远端
# 才说话，等于把「提交前」变成了「推送后」。搬进 check，与其余门禁同一条路径。
# 逻辑原样搬，未重写：重写会重新引入下面那条已被反向验证排除的漏报。

modreplace: ## go.mod 不得含 replace（本仓是公开 module）
	@# replace 只在**主模块**生效：本仓自己是主模块 → 本地全绿；下游把它当依赖
	@# → 该 replace 被静默忽略 → 按 MVS 回落旧版本 → 报 no required module
	@# provides package ...，错指依赖而非真正原因（见 issue #1）。
	@if grep -nE '^[[:space:]]*replace' go.mod; then \
	  echo "go.mod 含 replace 指令。本仓是公开 module，本地路径的 replace 一旦入库，"; \
	  echo "所有外部 go get 都会失败。本地并行开发请用 'make devlink' 走 go.work。"; \
	  exit 1; \
	fi
	@echo "go.mod 无 replace ✓"

modwork: ## go.work / go.work.sum 不得入库（只应存在于本机）
	@# go.work 会让 clone 下本仓的人进入 workspace 模式（go.mod 的版本解析被整体
	@# 旁路），而那些 use 的本机路径在别处不存在，报错与真实原因毫无关联。
	@# 判据是「是否被 git 跟踪」而非「文件是否存在」：本地生成未提交才是正确状态。
	@#
	@# 注意别写成 `git ls-files --error-unmatch go.work go.work.sum`：
	@# --error-unmatch 是「任一 pathspec 没命中就整体非零退出」，于是
	@# 「go.work 已入库、go.work.sum 没入库」这个最常见的组合会被**放行**。
	@# 该写法已实测漏报（反向验证时才发现），故用无 --error-match 的
	@# `git ls-files -- <paths>`，输出为空即未跟踪。
	@tracked=`git ls-files -- go.work go.work.sum`; \
	if [ -n "$$tracked" ]; then \
	  echo "go.work / go.work.sum 被 git 跟踪：$$tracked"; \
	  echo "本地并行开发用 make devlink 生成的文件只应存在于本机。"; \
	  echo "请 git rm --cached go.work go.work.sum，并确认 .gitignore 已覆盖。"; \
	  exit 1; \
	fi
	@echo "go.work 未入库 ✓"

hygiene: ## 仓库卫生：被跟踪的编译产物二进制不得入库（issue #413）
	@# 复发形态是「本地构建完顺手 git add .」——靠人记不住，故机器判。
	@# 判据在 cmd/hygiene：读 git ls-files 的**被跟踪**清单（与 modwork/modreplace
	@# 同口径），逐个比对可执行二进制魔数。判魔数不判扩展名——扩展名可以改。
	@# git 不可用时**报错退出**而不是静默跳过：判据跑不起来却报绿，
	@# 是最贵的失效形态。
	$(EXPORT) $(GOENV) go run ./cmd/hygiene

# check 的依赖链就是「提交前必须全绿」的定义。2026-11-06 复核时补上三条
# **此前根本没被跑过**的门禁：race（竞态）、vuln（依赖与标准库漏洞）、
# crossbuild（声明支持的平台必须真编得过）。它们的共同点是「目标/文档里有，
# 但没人执行」——与 lint 当年同样的形态，故按同一条纪律接进 check，
# 不挂到 ci.yml 上变回「推送后门禁」。
## 提交前门禁聚合。versioncheck 排在最前：工具版本不符时后面每条门禁的绿都不
## 代表「本仓合规」，先让它说话，免得红在别处、人往别处查。
# ci.yml 的 job 名不得超长：GitHub 会把它截断，于是**分支保护的 required
# context 永远匹配不上** ⇒ 每个 PR 都卡在 BLOCKED（三个检查全绿、合并被拒
# 「the base branch policy prohibits the merge」），而报告里看不出原因。
#
# 2026-11-12 实录：PG 车道给 check job 名加「repo 卫生」几个字 ⇒ 名字超 100
# 字节 ⇒ GitHub 记下的 check 名以「...」结尾 ⇒ 与保护规则里的全名失配 ⇒
# 本仓此后每一个 PR 都卡住（三个 job 全绿也合不进去）。
#
# 判据：每个 job 名的**字节数** ≤ 90（GitHub 的截断阈值是 100 字节，留 10
# 字节余量给将来加字）。超了当场红，并指出该改哪一行。
#
# 另注：改 job 名时必须**同步**更新分支保护的 required contexts（两者逐字
# 相同）——本门禁只管长度，那条「逐字相同」的关系在 GitHub 侧，离线判不了。
cijobname: ## ci.yml 的 job 名长度（超长 ⇒ GitHub 截断 ⇒ 分支保护的 context 失配 ⇒ 所有 PR 卡住）
	@ok=1; \
	for name in $$(grep -E '^    name: ' $(CI_YML) | sed 's/^    name: //'); do \
		bytes=$$(printf '%s' "$$name" | wc -c | tr -d ' '); \
		if [ "$$bytes" -gt 90 ]; then \
			echo "ci.yml 的 job 名超长（$$bytes 字节 > 90）：GitHub 会截断它 ⇒ 分支保护的 required context 永远匹配不上 ⇒ 每个 PR 都卡在 BLOCKED 且看不出原因"; \
			echo "  改短这个 job 名，并同步更新分支保护的 required contexts（两者必须逐字相同）：$$name"; \
			ok=0; \
		fi; \
	done; \
	if [ "$$ok" != 1 ]; then exit 1; fi; \
	echo "ci.yml job 名长度 ✓（均 ≤ 90 字节，GitHub 不会截断）"

check: versioncheck cijobname fmt-check vet lint test cover-gate vuln race crossbuild architecture-check docsync seamcheck apilifecheck deploycheck storagecheck ledgercheck releasecheck modreplace modwork hygiene

tidy:
	$(EXPORT) $(GOENV) go mod tidy

# ── 本地多仓并行开发（issue #1 的修法①）────────────────────────────
# 为什么不能图省事在 go.mod 里写 `replace ... => $(GUARDX_WORK)`：
# Go 的 replace 只在**主模块**生效。本仓自己是主模块 → 本地构建绿；
# 但下游（bianque 等以 `replace => ../goagent-kernel` 引入本仓的宿主）里
# goagent-kernel 是**依赖模块**，它的 replace 被静默忽略 → guardx 按 MVS
# 回落到已发布的旧版本 → 报 `no required module provides package
# github.com/oagentkit/guardx/injectband`，指向 guardx 而非真正原因。
# 一行本地 replace，代价是断掉所有下游，且只在别人构建时才暴露。
#
# workspace 的 `use` 跨仓生效且不碰任何 go.mod，是本地并行开发的正确姿势：
# 下游零影响，go.mod 保持对外契约（零 replace），也不存在「忘记删」的风险。

# 已有 go.work 时只**追加**不重建：重建会抹掉别人已加的仓，
# 而「再跑一次 devlink」本该是无操作（幂等），不是一次事故。
# （注：续行块内部不能放 # 注释——shell 会连同反斜杠一起注释掉，
#   报 "syntax error: unexpected end of file"。注释一律写在块外。）
devlink: ## 把 guardx 工作副本接入本地 workspace（不改任何 go.mod，可重复执行）
	@test -d "$(GUARDX_WORK)" || { \
	  echo "找不到 $(GUARDX_WORK) —— 先把 guardx 克隆到该路径："; \
	  echo "  git clone https://github.com/oagentkit/guardx.git $(GUARDX_WORK)"; \
	  exit 1; }
	@if [ -f go.work ]; then \
	  $(EXPORT) $(GOENV) go work use -add . "$(GUARDX_WORK)"; \
	  echo "已把 $(GUARDX_WORK) 追加进现有 go.work（其余条目保留）。"; \
	else \
	  $(EXPORT) $(GOENV) go work init . "$(GUARDX_WORK)"; \
	  echo "已生成 go.work：本仓 + $(GUARDX_WORK) 进入同一 workspace。"; \
	fi
	@echo "注意：workspace 模式下 guardx 读的是本地副本，go.mod 里写的版本被旁路。"
	@echo "验完跑 'make devunlink' 断开，否则构建会一直读本地副本。"
	@echo "另：workspace 期间**别跑 go work sync**——它按 workspace 的构建列表回写各"
	@echo "模块的 go.mod/go.sum。仓不全时会把用不到的依赖直接删掉（实测把 oagentkit"
	@echo "四个依赖整片剥掉）。要整合同步请先 devunlink 再在单模块下 go mod tidy。"

devunlink: ## 断开本地 workspace（删除 go.work / go.work.sum）
	@rm -f go.work go.work.sum
	@echo "已断开本地 workspace，构建回到 go.mod 声明的已发布版本。"

clean:
	rm -rf bin coverage.out

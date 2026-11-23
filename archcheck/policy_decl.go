// policy 声明面的 schema：规则、阈值、例外登记、模块与沙箱配置的反序列化目标。
//
// 这一层是**可导入包**的第一道边界：宿主仓要用同一套判据，就得能读同一份
// policy 格式。故类型全部导出，且 policy 文件路径不写死成
// "architecture-policy.yaml"——宿主那份可能叫别的名字。
//
// 为什么单独成文件：这些声明类型是 policy 的**数据面**，而 Load / FindRoot 是
// **装载面**。同文件时 policy.go 在 395 行的基数上再加 issue #447 的 `status`
// 字段即越过 400 行结构门禁——而 395/400 与 #448 判的 `eventbus/tail.go` 398/400
// 是同一种病（下一次加字段就撞红线），按关注点拆开各归各位。
// **零行为变更**：纯移动，不改任何一行类型定义。

package archcheck

// NegFollow 是负向前置的结构化表达：anchor 紧跟 forbid_follow 时，命中位置
// （被禁词）落在 forbid_follow 里即豁免。
//
// 为什么不写进正则：Go 原生只支持 RE2，没有后顾断言（(?!)）。
type NegFollow struct {
	Anchor       string `yaml:"anchor"`
	ForbidFollow string `yaml:"forbid_follow"`
}

// Rule 是一条正则式规则：匹配面由 scope/include/scope_exclude 圈定，判据由
// forbid_regex（源码文本）或 forbid_import_regex（import 路径）给出。
type Rule struct {
	ID           string   `yaml:"id"`
	Desc         string   `yaml:"desc"`
	Severity     string   `yaml:"severity"` // error（默认，违反即非零退出）| warn（报告口径，不阻断）
	Scope        []string `yaml:"scope"`
	ScopeExclude []string `yaml:"scope_exclude"`
	Include      []string `yaml:"include"`
	IgnoreTest   bool     `yaml:"ignore_test"`
	OnlyTest     bool     `yaml:"only_test"` // 只扫测试文件（配合 severity: warn 做夹具体检）
	ForbidImport string   `yaml:"forbid_import_regex"`
	ForbidRegex  string   `yaml:"forbid_regex"`
	// CheckKind 选**结构化**执行体（当前取值域：goroutine_join）。
	//
	// 为什么需要第三个执行体：forbid_regex 判「源码文本里有没有某个形状」，
	// forbid_import_regex 判「import 路径里有没有某个形状」。而有些纪律判的是
	// **结构关系**——「用例体内有没有 go 语句」与「测试侧有没有等待点」这两件事
	// 之间的关系，逐行正则认不出来（go 语句有多种形态，等待点还散在助手里）。
	//
	// 取值域必须封闭：写错的类别名若被当成「不是已知类别」，这条登记就**静默降级**
	// 成「不判」——登记了一张表却没生效，且没有任何机器能发现。未知类别当天即红
	// （见 selfcheck.go）。
	CheckKind string `yaml:"check_kind"`
	// ManagedHelpers 是「受管的等可观测事实辅助」清单：这些助手函数体内部的
	// time.Sleep 不被 wallclock_ruler 判成尺子（当前只有 wallclock_ruler 用它）。
	//
	// 为什么声明在 policy 侧而不是硬编码在 archcheck 里：archcheck 是要跨宿主
	// 复用的库，硬编码辅助名单等于把宿主的事写进内核——宿主仓的助手名各不相同。
	//
	// 取值形态：裸名（waitTail）或带包名的限定名（eventbus.waitTail）。
	//
	// 兼容性：这是**源码级 additive 变更**——给导出的结构体加字段，会让下游
	// 用**位置参数**（无字段名）写结构体字面量的代码编译不过（Go 要求位置参数
	// 字面量写出全部字段）。用字段名写字面量的代码不受影响。
	ManagedHelpers []string `yaml:"managed_helpers"`
	// ForbidCalls 是「测试面不得直接调用」的**符号清单**（check_kind:
	// test_no_direct_call 用，issue #449）。
	//
	// 为什么由 policy 声明而不是 archcheck 硬编码：哪些函数是「包级全局态 setter」
	// 是宿主的知识（本仓是 SetEnabled/SetSurfaces/SetStrategy/SetDigestKey，别的
	// 仓各不相同）。archcheck 只提供判定面——「测试里出现这些符号的调用点即违规，
	// 除非它落在受管辅助内」。
	//
	// 判的是**调用点**（go/ast）：函数定义、注释、字符串、测试名（TestSetDigestKeyGuards）
	// 都不算——逐行正则会把它们一起命中，规则误报到没人信它就等于没有规则。
	//
	// 兼容性：与 ManagedHelpers 同款，是**源码级 additive 变更**（给导出结构体
	// 加字段会让下游用位置参数写字面量的代码编译不过）。
	ForbidCalls []string    `yaml:"forbid_calls"`
	NegFollow   []NegFollow `yaml:"negative_follow"`
	// ImportExempt import 路径级的**精确**例外（issue #15）。
	//
	// 存在的原因：只有 `forbid_import_regex`（正则禁令）时，宿主若要放行
	// 被禁依赖树里的**某个具体子包**，只有三条路：
	//   1. 放宽整条正则 ⇒ 把整棵树的其余部分一起放行；
	//   2. 改成逐包白名单 ⇒ 上游将来新增一个包就**静默放行**
	//      （一条被静默放宽的声明等于没有声明）；
	//   3. 绕开 —— 在宿主侧自写一层转译。
	//
	// 第 3 条可行，但代价是同一个上游能力要在宿主侧再写一遍转译
	// （类型不许外泄、等价性自己核）——而这层转译本可以由上游的一个
	// import 例外免掉。
	//
	// 粒度必须是**精确 import 路径**（不是正则前缀——后者等于放宽整棵树）。
	// 未登记的包**仍然被拒** ⇒ 上游新增包不会静默放行。
	ImportExempt []string `yaml:"import_exempt"`

	// Examples 规则自带的样例：**加载期**就判「这条规则还活着吗」。
	//
	// 与 tests 的分工：测试证明「今天这条规则能判」，examples 证明「这次加载
	// 这条规则确实判得动」——前者在 CI 里跑一次，后者在**每一次**门禁运行、
	// 每一个复用本判据的宿主仓里跑。规则会随包名/命令名/词表漂移，而漂移
	// 不会报错：它只是静静地不再命中，门禁继续绿着。
	//
	// 判据复用**真实判定核**（regexViolationsIn / importViolationsIn，与文件遍历
	// 调的是同一份代码），不是另写一套近似实现——另写一套的话，样例过了也说明不了
	// 真判定能过（两套实现各自漂移，而样例那套只看几个字符串、漂得更慢）。
	Examples []RuleExample `yaml:"examples"`
	// RulerClassifications 是「墙钟判定」的逐条分类登记（check_kind: wallclock_ruler 用，issue #481）。
	//
	// 它回答的是 issue 的那句话：「凡是把阈值挂在墙钟上的判据，都要问一句『这个量变了
	// 我判的东西还成立吗』」。答案不能留在脑子里 ⇒ 逐条登记，并由门禁对账
	//（漏登记即红、登记成尺子类即红、改完不删登记即红）。
	//
	// 兼容性：这是**源码级 additive 变更**——给导出的结构体加字段，会让下游
	// 用**位置参数**（无字段名）写结构体字面量的代码编译不过（Go 要求位置参数
	// 字面量写出全部字段）。用字段名写字面量的代码不受影响。
	RulerClassifications []RulerClassification `yaml:"ruler_classifications"`
}

// RulerClassification 一条「墙钟判定」的逐条分类登记（issue #481）。
//
// 键是三段：**目录 + 函数名 + 形态**——目录与函数名在代码演进中稳定（行号不稳），
// 形态段让同一个函数里的两类判定点分别登记。同一键下多处判定点按出现顺序与
// 登记项一一对应（登记少一条即漏登记，多一条即僵尸登记）。
type RulerClassification struct {
	// Package 相对 module 根的**目录**（如 "handoff"、"cmd/raceprobe"）。
	Package string `yaml:"package"`
	// Func 归属函数名（测试函数或包内助手）。
	Func string `yaml:"func"`
	// Form 形态（取值域封闭：fixed_seconds_assert | timer_only_select）。
	Form string `yaml:"form"`
	// Class 分类（取值域封闭：timeout_cap | ruler）。
	Class string `yaml:"class"`
	// Reason 理由（必填——空理由的登记等于没判过）。
	Reason string `yaml:"reason"`
}

// RuleExample 一条规则的样例。
//
// Text 的**判据面**随规则类型而定，与真实判定同构：
//   - forbid_regex 规则：Text 是一行源码；
//   - forbid_import_regex 规则：Text 是一条 import 路径（不含引号）。
//
// Want 只接受 hit / miss 两个值。**拼写错误必须报错**：把 want 写成 hitt 时
// 若按「非 hit 即 miss」处理，一条判据彻底坏掉的规则会因为「反例都通过」而绿。
type RuleExample struct {
	// Text 样例（源码行或 import 路径）。
	Text string `yaml:"text"`
	// Want 期望：hit = 必须被判违规；miss = 必须不被判违规。
	Want string `yaml:"want"`
}

// 样例期望的取值域。
const (
	exampleWantHit  = "hit"
	exampleWantMiss = "miss"
)

// Threshold 是结构债阈值：超长文件与 god object 的机械判据。
type Threshold struct {
	MaxFileLines      int `yaml:"max_file_lines"`
	MaxMethodsPerType int `yaml:"max_methods_per_type"`
	// ForwardingShellMaxChars 是「转发壳」的方法体长度上限（字符）。nil = 用缺省值。
	//
	// 转发壳 = 方法体只有一条转发语句、且剥掉注释与空白后长度不超过此值。
	// god object 阈值比的是**实质方法数**（方法数 − 转发壳数），因为方法总数在
	// façade 上等于**公共 API 表面规模**，不是复杂度集中度（issue #13）。
	//
	// 显式写 0 = 关闭该区分（所有方法都算实质方法），判定逐字退回旧行为。
	ForwardingShellMaxChars *int     `yaml:"forwarding_shell_max_chars"`
	ScanRoots               []string `yaml:"scan_roots"`
	Enforce                 bool     `yaml:"enforce"` // true=超限即非零退出；false=仅报告
}

// forwardingShellLimit 返回「转发壳」的长度上限。
//
// 未声明（nil）时用缺省值；显式 0 = 关闭该区分（此时没有方法会被算成壳，
// 因为任何含调用的方法体都长于 0——判定逐字退回旧行为）。
func (t Threshold) forwardingShellLimit() int {
	if t.ForwardingShellMaxChars == nil {
		return defaultForwardingShellMaxChars
	}
	return *t.ForwardingShellMaxChars
}

// Exception 是例外登记：精确豁免某条规则的某处违反。
//
// 豁免必须写 until（到期日）与 removal（拆除条件）——没有到期日的豁免等于永久豁免，
// 没有拆除条件的豁免是赖账。两者都由 policy 自校验强制。
type Exception struct {
	Rule      string `yaml:"rule"`
	Path      string `yaml:"path"`
	Line      int    `yaml:"line"`
	Violation string `yaml:"violation"`
	Reason    string `yaml:"reason"`
	Until     string `yaml:"until"`
	Removal   string `yaml:"removal"`

	// Status 是这条例外的**对账标记**（issue #447 的后半句）：省略 = 在用；
	// exceptionStatusStale = 维护者已确认它**不再需要**（已失效待删）。
	//
	// 为什么需要它：例外命中不到任何违反时，登记行的处方是「要么命中至少一条违反，
	// 要么被显式标记为已失效待删」——没有这个标记，一条**暂时不命中但确实还要留着**
	// 的例外（站点被别的改动临时改干净、规则口径待回滚）只能被删掉，而删掉会丢掉
	// `reason` / `until` / `removal` 这三份债的来龙去脉。
	//
	// 取值域**封闭**（自校验强制）：拼错的取值（如 `stal`）若被当成「在用」，命中 >0
	// 时它会**绿**——维护者写下的意图与结论相反，且没有任何机器能发现。
	Status string `yaml:"status"`
}

// exceptionStatusStale 是 Exception.Status 的唯一合法取值（空 = 在用）。
//
// 词面取 stale（陈旧 / 失效）而不是 pending 之类：它就是「僵尸例外」的反面标记——
// 声明面、判定面、报告三处共用同一个词，读的人不用在两个词之间做映射。
const exceptionStatusStale = "stale"

// facadeRuleID 门面包不可变规则的 ID。所有门面包共用一条规则 ID：它们守的是同一条
// 纪律（转发到上游、不得在本仓长实现），逐包差异体现在 violation 的 path 与 text 里。
const facadeRuleID = "harness-facade-stays-facade"

// Module 是 modules 段的一条登记：roots 定位包目录，requires 声明**允许**的
// 依赖方向。
//
// 为什么这段必须被真的执行：requires 是一张包之间的依赖拓扑声明，写错或漏写时
// 它不会自己报警——半年后没人知道它已经不成立，而依赖缠绕恰好是最难靠 review
// 发现的债类型（零报错、零警告、构建全绿）。
//
// requires 的条目有两种形态：
//
//   - 模块 id：本仓内部模块，必须在 modules 段登记过，否则声明自相矛盾（判为
//     policy 解析失败，不静默降级）。
//   - ext:<import 路径前缀>：外部包面，用于表达「本模块依赖某个外仓的哪个包」。
//     机制面迁到外仓之后，宿主的依赖拓扑表需要能表达跨仓依赖，而只认本仓模块 id
//     的判据表达不了。
type Module struct {
	ID       string   `yaml:"id"`
	Roots    []string `yaml:"roots"`
	Requires []string `yaml:"requires"`
	Owner    string   `yaml:"owner"`
	Managed  bool     `yaml:"managed"`
	Notes    string   `yaml:"notes"`
}

// DependencyDirection 是 dependency_direction 段的配置：依赖方向门禁的开关、
// 级别、豁免目录与**受管外部包面**。
//
// 为什么不塞进 modules 段：modules 段是**逐包**的声明，依赖方向是**跨包**的
// 规则，混在一段里会让「这条登记的字段」与「整个文件的配置」两种粒度互相干扰。
type DependencyDirection struct {
	Enabled    *bool    `yaml:"enabled"` // 指针：缺省即启用，只有显式 false 才关
	Severity   string   `yaml:"severity"`
	Desc       string   `yaml:"desc"`
	IgnoreDirs []string `yaml:"ignore_dirs"`

	// ExtPrefixes 是**受管外部包面**清单：这些 import 路径前缀下的外部包，
	// 全部纳入本门禁的判定面。
	//
	// 为什么要有这一层（policy 级），而不能只靠各模块 requires 里写的 ext: 条目：
	// 若「哪些外部包受管」本身也只由各模块自己声明，那么**最后一个声明它的模块
	// 一旦撤掉声明，整片包面就随之消失**——那些 import 重新变成不判定的。
	// 而外部包没有「未登记目录」那道兜底（它是别的仓的包，本仓没有它的目录），
	// 于是这条边就静默失管了：门禁变绿，而不是变红。
	//
	// 这一层与内部模块的结构同构：
	//   - 受管面：modules 段（内部）/ ext_prefixes（外部）
	//   - 逐模块的边：requires
	ExtPrefixes []string `yaml:"ext_prefixes"`
}

// on 报告依赖方向门禁是否启用（缺省启用）。
func (d DependencyDirection) on() bool { return d.Enabled == nil || *d.Enabled }

// SandboxProvider 是 sandbox_providers 段里的一条**档位登记**：一个执行后端
// 自称提供哪一档隔离。
//
// # 为什么档位要登记在案，而不是只写在代码里
//
// 后端自报的档位若只存在于实现中，它有三重失效：review 时看不见、改了没人会响、
// 标高了没人管。而档位是宿主做风险判断的依据——宿主据它决定「这次执行能不能
// 碰不可信输入」。于是档位声明需要一个**签字处**，policy 这一段就是那个签字处。
//
// # level 填常量名而不是档位值
//
// 填 `LevelProcess` 而不是 `process`：门禁**不能 import 被判的包**（它必须是最
// 底层的，否则「用它判别人」就变成「它自己也在被判定」，门禁的红绿会依赖它所判
// 的代码），所以它拿不到档位名表，只能比对代码里可静态识别的那一面——常量名。
// policy 侧填同一份常量名，两侧就共用一份真相。
type SandboxProvider struct {
	ID      string `yaml:"id"`
	Level   string `yaml:"level"`
	Package string `yaml:"package"`
	// Supports 登记该后端能兑现哪些预算项。
	//
	// 取值是上游 `guardx/sandbox` 的预算项名：wall_clock / max_calls /
	// max_bytes / max_memory。缺省（未填）= 只兑现基本三闸——
	// 与上游「不实现 Capable 的后端只兑现三闸、不兑现内存」的缺省语义一致。
	//
	// 为什么登记这一维：档位（Level）答的是「能力面多强」，预算支持面答的是
	// 「资源兜底兑现哪几项」。两者**正交**——进程内解释器的能力面与
	// LevelProcess 同级，却兜不住内存。少了这一维，「要求了内存兜底而后端
	// 兜不住」这件事在声明面是隐形的，宿主只能在运行期才发现。
	//
	// 门禁核对的是「登记与代码事实一致」：后端**没有** `Supports` 方法 ⇒ 只能
	// 登记基本三闸；后端**有** `Supports` 方法 ⇒ 可以登记含 max_memory。
	// 判据同样走 `go/ast` 的方法指纹（门禁不能 import 被判的包）。
	Supports []string `yaml:"supports"`
	Notes    string   `yaml:"notes"`
}

// SandboxProviders 是 sandbox_providers 段的配置：开关、级别、受忽略目录与登记清单。
//
// 形态与 dependency_direction 段同构（配置 + 清单），因为两者都是「一段跨包的
// 规则 + 一张逐条声明的表」。段缺省即启用——**未登记的后端会被报出来**，
// 而不是「没登记就当没这回事」。
type SandboxProviders struct {
	Enabled    *bool             `yaml:"enabled"`
	Severity   string            `yaml:"severity"`
	Desc       string            `yaml:"desc"`
	IgnoreDirs []string          `yaml:"ignore_dirs"`
	Providers  []SandboxProvider `yaml:"providers"`
}

// on 报告档位登记门禁是否启用（缺省启用）。
func (s SandboxProviders) on() bool { return s.Enabled == nil || *s.Enabled }

// Policy 是 architecture-policy.yaml 的反序列化结果。
type Policy struct {
	Version    int                 `yaml:"version"`
	Modules    []Module            `yaml:"modules"`
	Rules      []Rule              `yaml:"rules"`
	Facades    []FacadeRule        `yaml:"facades"`
	DepDir     DependencyDirection `yaml:"dependency_direction"`
	Thresholds Threshold           `yaml:"thresholds"`
	Exceptions []Exception         `yaml:"exceptions"`

	// SandboxProviders 是执行沙箱后端的档位登记段（声明面）。
	//
	// 指针而非值：段整体缺省时，**门禁仍然开启**——因为「本仓有后端实现却没登记」
	// 必须报出来，而不是「没登记就当没这回事」。指针只用于区分「段不存在」与
	// 「段存在但为空」，两者的判定结果相同（都要靠代码事实说话）。
	SandboxProviders *SandboxProviders `yaml:"sandbox_providers"`

	// ExecutionPaths 是执行路径门禁的声明面（裸执行入口清单）。
	//
	// 段缺省即启用——「执行必须经登记簿」是**默认要求**，
	// 不是「登记了才管」。没登记的裸入口不会被这条门禁抓到（判据面有限，
	// 见 execution.go 的「判不了什么」），但默认开着总比默认关着好。
	ExecutionPaths *ExecutionPaths `yaml:"execution_paths"`

	// EgressPaths 是子进程出口门禁的声明面（逐个启动点登记出口档位）。
	//
	// 段缺省即启用：「子进程在不在出口强制面内」这个问题必须有人问，
	// 而不是「宿主登记了才问」——不登记就没人问，正是这条门禁要防的静默失效。
	EgressPaths *EgressPaths `yaml:"egress_paths"`

	// TenantGate 是租户过滤漏拼门的声明面（授权上下文的类型形状、租户域
	// 字段名、共享判定面清单）。段缺省即关闭——形状是判定前提，没有形状
	// 就判不了；声明了段却漏形状由自校验报红。
	TenantGate *TenantGate `yaml:"tenant_gate"`
	// Seams 是宿主接缝契约门禁的声明面（#369/#370）；段缺省即关闭。
	Seams *SeamRules `yaml:"seams"`
	// WordTables 是判定词表登记门的声明面（词表清单 + 枚举出口）。
	//
	// 段缺省即关闭：这条门禁的判定面是**这张表登记了什么**，
	// 不是「仓里有没有词表」——判什么由各仓自己决定，机制面只管
	// 「登记了就得守住，登记与事实偏离当天即红」。
	WordTables *WordTables `yaml:"word_tables"`

	// RequireRuleExamples 要求每条**有执行体**的规则都自带 examples。
	//
	// 默认 false：宿主仓不必为了升级判据而先给全部规则补样例（那会让一次
	// 依赖升级变成一次策略改写）。本仓自己开 true——口径是「本仓的每条规则
	// 都必须能被加载期验活」，而这条口径若只写在注释里就是散文，不是门禁。
	RequireRuleExamples bool `yaml:"require_rule_examples"`

	// Path 记录 policy 文件路径。自校验违规要指向它，而宿主仓的 policy
	// 不一定叫 architecture-policy.yaml——写死文件名等于让复用方永远看到
	// 一个指向不存在文件的报错位置。
	Path string `yaml:"-"`
}

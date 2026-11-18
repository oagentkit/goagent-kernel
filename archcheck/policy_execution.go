// policy_execution.go —— execution_paths 段的声明面。
//
// 单独成文件是为了让 policy.go 保持在结构阈值之内（max_file_lines: 400）：
// 「文件超长」判的是**文件是否还读得动**，而 policy.go 承载的是全部段的
// 声明面，塞满之后新增一段就得先挪走另一段。

package archcheck

// ExecutionEntry 是 execution_paths 段里的一条**裸执行入口**登记。
//
// # 「裸」指什么
//
// 不经 `sandbox` 登记簿就能跑起一段程序的入口。绕开登记簿意味着三样东西
// 同时失效：**档位下限**（`Resolve` 选不到就拒绝的那条）、**预算可兑现性**
// （兜不住内存的后端不被选中的那条）、**审批门**（`gatecheck.Managed` 那层）。
//
// 而这三条恰恰是宿主据以做风险判断的依据——声明还在，强制没了。
//
// # 为什么按**包名**登记而不是完整 import 路径
//
//  1. **别名改不掉**：按完整 import 路径匹配时，宿主 `import p "…/ptc"` 换个别名
//     就绕过了。按包名匹配，别名不影响命中。
//  2. **module 路径迁移不会陈旧**：本仓的 module 路径迁移过一次
//     （私有域名 → github.com），写死完整路径会在迁移那天静默失配。
//
// 需要更精确时填 `import_path` 二次收紧（限定到某一个具体模块）。
type ExecutionEntry struct {
	// PackageName 入口所在包的**包名**（import 路径的最后一段，如 `ptc`）。
	PackageName string `yaml:"package_name"`
	// Symbol 入口的符号名（包级函数）。
	Symbol string `yaml:"symbol"`
	// ImportPath 可选：填了则 import 路径必须**逐字相等**（更精确）。
	ImportPath string `yaml:"import_path"`
	// Notes 该入口为什么是裸的（写给人看的那一句）。
	Notes string `yaml:"notes"`
}

// ExecutionPaths 是 execution_paths 段的配置：开关、级别、受忽略目录与裸入口清单。
//
// 形态与 sandbox_providers 段同构（配置 + 逐条声明的表）。
//
// # 本仓自己的裸入口不在这一段里
//
// 它们在判据执行体的**内置表**里（见 execution.go）：内置表记的是本仓的 API 事实，
// 让每个仓各登记一遍就是两份真相。policy 段只登记**宿主自己的**裸入口。
type ExecutionPaths struct {
	Enabled    *bool            `yaml:"enabled"`
	Severity   string           `yaml:"severity"`
	Desc       string           `yaml:"desc"`
	IgnoreDirs []string         `yaml:"ignore_dirs"`
	RawEntries []ExecutionEntry `yaml:"raw_entries"`
}

// on 报告执行路径门禁是否启用（缺省启用）。
func (e ExecutionPaths) on() bool { return e.Enabled == nil || *e.Enabled }

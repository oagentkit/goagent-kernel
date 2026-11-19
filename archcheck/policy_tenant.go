// policy_tenant.go —— tenant_gate 段的声明类型（issue #172）。
//
// 与 policy.go 拆开的理由：policy.go 已接近 max_file_lines 阈值；租户门的形状
// 声明是一组自洽的字段（授权类型 / 租户字段 / 扫描面 / 共享面登记），单独成文件
// 比继续往 policy.go 里堆更容易读。
package archcheck

// TenantSharedSurface 是一条登记在册的共享租户隔离判定面。
//
// Dir 是相对仓根的目录，Name 是函数/方法名。执行体做两件事：
//   - 调用点匹配（服务层做隔离判定时走这些面）；
//   - 存在性核对（Dir 下必须有同名 FuncDecl，否则清单漂移 = 红）。
type TenantSharedSurface struct {
	Dir  string `yaml:"dir"`
	Name string `yaml:"name"`
	Note string `yaml:"note"`
}

// TenantGate 是租户过滤漏拼门（issue #172）的声明面。
//
// 形状（哪些类型算授权上下文、哪些字段算租户域、共享判定面清单）由宿主
// policy 声明；执行体是通用 AST 分析。内核代码里不得出现宿主租户字面——
// 它们只出现在这份 policy 里。
//
// # 六条判据（issue #177 补齐到 6，与 bianque tenantcheck 同口径）
//
//  1. tenant-surface-missing-evidence：带授权上下文形参 + 返回租户域类型的
//     函数必须有隔离判定证据（调用登记的共享判定面 / 查询条件含过滤列 /
//     显式 `// tenant:iso <理由>`）；三者皆无 = 红。
//  2. viewer-dropped：带授权上下文形参却完全不用 = 红。
//  3. shared-surface-missing：登记在册的共享判定面在代码里不存在 = 清单漂移 = 红。
//
// 后三条是**租户表登记面**判据（bianque 的 RLS 登记面），声明
// tenant_tables_manifest 后启用；不声明则只跑前三条：
//
//  4. tenant-policy-missing：含租户字段的模型映射出的物理表不在清单里 = 红
//     （漏登记 = 隔离策略不覆盖这张表）。
//  5. tenant-policy-drift：清单登记的表在模型面已不存在 = 红（登记漂移 =
//     空登记）。双向核对，清单与模型面谁走了谁都不行。
//  6. tenant-table-name-missing：含租户字段的模型没有可从 AST 识别的表名
//     方法（缺省 TableName，可用 table_name_method 配置）= 红——解析不出
//     物理表名，登记面核对不了这张表。
//
// 宿主持有的 RLS 注册表是 Go 符号，执行体调不到；清单是宿主自己 dump 的
// 产物（或由一条校验测试保证与注册表一致），语义与 shared_surfaces 的
// 「存在性核对」同族。
//
// # 租户类型由代码推导
//
// 不另立类型清单：扫描 TenantTypeRoots（缺省 = Roots）下所有生产代码，
// 任何含 TenantFields 中字段名的 struct 都算租户域类型。新增模型自动入面。
type TenantGate struct {
	Enabled  *bool  `yaml:"enabled"`
	Severity string `yaml:"severity"`
	Desc     string `yaml:"desc"`

	// Roots 是服务层扫描面（相对仓根，递归）。
	Roots []string `yaml:"roots"`
	// TenantTypeRoots 是租户类型推导面；缺省（空）= Roots。
	TenantTypeRoots []string `yaml:"tenant_type_roots"`
	// TenantTableTypeRoots 是**登记面（判据 4-6）专用**的类型推导面；缺省
	// （空）= TenantTypeRoots。读面（判据 1-2）的租户域类型可以包括服务层
	// 载体（输入/行 DTO——带租户字段但**不映射物理表**），而登记面只关心
	// 「映射到物理表的模型」：两者扫描根不同构时（典型：读面 = model+service、
	// 登记面 = model）在这里单独声明，避免载体 DTO 被误报
	// tenant-table-name-missing（它们本就没有表名方法）。
	TenantTableTypeRoots []string `yaml:"tenant_table_type_roots"`
	// AuthContextTypes 授权上下文类型名。可写限定名（pkg.Actor）或裸名（Actor）。
	// 限定名按**源级限定符**匹配，并解析 import 别名（`import a ".../pkg"`
	// 后的 a.Actor 仍算 pkg.Actor）——改名别名绕不过去。
	AuthContextTypes []string `yaml:"auth_context_types"`
	// TenantFields 租户域字段名：struct 含任一字段即租户域类型。
	TenantFields []string `yaml:"tenant_fields"`
	// FilterColumns 查询过滤列名（Where/Raw 的字符串字面量包含其一即证据）。
	FilterColumns []string `yaml:"filter_columns"`
	// SharedSurfaces 共享隔离判定面登记表（调用证据 + 存在性核对）。
	SharedSurfaces []TenantSharedSurface `yaml:"shared_surfaces"`
	// TenantTablesManifest 租户表登记面清单（issue #177）：相对仓根的文本
	// 文件，每行一个物理表名（`#` 起行为注释）。声明后启用判据 4-6：执行体
	// 把模型面推导出的租户表集合与清单做**双向**核对（缺登记 / 登记漂移），
	// 并要求每个租户域类型有可解析的表名方法。空 = 只跑判据 1-3。
	TenantTablesManifest string `yaml:"tenant_tables_manifest"`
	// TableNameMethod 物理表名解析方法名（缺省 TableName，GORM 惯例）。
	// 只影响判据 6 与登记面集合的推导。
	TableNameMethod string `yaml:"table_name_method"`
}

// on 报告租户门是否启用。
//
// 与 sandbox_providers 等段的缺省语义**相反**：本段缺省即关闭——没有形状
// （授权类型/租户字段/扫描面）就无从判定，强行开启只会对每个函数报「无证据」。
// 声明了段却漏形状由自校验报红（那才是「声明了但判不动」的形态）。
func (t TenantGate) on() bool { return t.Enabled == nil || *t.Enabled }

package archcheck

// 租户表登记面判据回归（issue #177）：双向核对 + 表名方法三态的 AST 夹具
// 正反例。夹具全部合成名字，形状与 tenantgate_test.go 同一口径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tenantManifestPolicy 在基础 policy 上声明登记面（清单 + 表名方法）。
func tenantManifestPolicy(manifest, method string) string {
	extra := "  tenant_tables_manifest: " + manifest + "\n"
	if method != "" {
		extra += "  table_name_method: " + method + "\n"
	}
	return tenantPolicy(extra)
}

// runTenantManifest 造带清单文件的最小仓并跑门禁（manifest 传空串 = 空清单
// 文件；要测「文件不存在」直接用 fixtureRoot）。
func runTenantManifest(t *testing.T, polYAML, code, manifest string) *Result {
	t.Helper()
	root := fixtureRoot(t, polYAML, code)
	if err := os.WriteFile(filepath.Join(root, "tables.txt"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	return res
}

// manifestModels 正例模型面：一个租户域 struct，带 TableName 字面量方法。
const manifestModels = `package x

type Record struct {
	ID       uint64
	TenantID string
}

func (Record) TableName() string { return "records" }

func VisibleTo(a identity.Actor, r Record) bool { return a.TenantID == r.TenantID }
`

// TestTenantManifestAllGreen 双向核对全对上：登记面三条全绿（判据 1 的
// missing-evidence 由 VisibleTo 的调用面豁免——它不返回租户类型）。
func TestTenantManifestAllGreen(t *testing.T) {
	res := runTenantManifest(t, tenantManifestPolicy("tables.txt", ""), manifestModels, "records\n")
	for _, rule := range []string{tenantPolicyMissingRuleID, tenantPolicyDriftRuleID, tenantTableNameMissingRuleID} {
		if vs := onlyRule(res, rule); len(vs) != 0 {
			t.Fatalf("%s 应全绿: %v", rule, textsOf(vs))
		}
	}
}

// TestTenantManifestMissing 模型面表不在清单里 = tenant-policy-missing
// （漏登记 = 隔离策略不覆盖这张表；报错定位到表名方法）。
func TestTenantManifestMissing(t *testing.T) {
	res := runTenantManifest(t, tenantManifestPolicy("tables.txt", ""), manifestModels, "")
	vs := onlyRule(res, tenantPolicyMissingRuleID)
	if len(vs) != 1 {
		t.Fatalf("应有恰好一条缺登记，实得 %d：%v", len(vs), textsOf(res.Violations))
	}
	if !strings.Contains(vs[0].Text, `"records"`) || !strings.Contains(vs[0].Text, "Record") {
		t.Fatalf("缺登记应点名表与模型: %s", vs[0].Text)
	}
	if vs[0].Line == 0 {
		t.Fatalf("违规位置不符: %+v", vs[0])
	}
}

// TestTenantManifestDrift 清单登记的表在模型面已不存在 = tenant-policy-drift
// （空登记等于没有登记；报错定位到清单行号）。
func TestTenantManifestDrift(t *testing.T) {
	res := runTenantManifest(t, tenantManifestPolicy("tables.txt", ""), manifestModels, "records\nghost\n")
	vs := onlyRule(res, tenantPolicyDriftRuleID)
	if len(vs) != 1 {
		t.Fatalf("应有恰好一条登记漂移，实得 %d：%v", len(vs), textsOf(res.Violations))
	}
	if !strings.Contains(vs[0].Text, "ghost") {
		t.Fatalf("漂移应点名幽灵表: %s", vs[0].Text)
	}
	if vs[0].Path != "tables.txt" || vs[0].Line != 2 {
		t.Fatalf("漂移应定位清单第 2 行: %+v", vs[0])
	}
}

// TestTenantTableNameMethodMissing 无表名方法的租户域模型 = 红
// （解析不出物理表名，登记面无从核对）。
func TestTenantTableNameMethodMissing(t *testing.T) {
	code := `package x

type Widget struct {
	ID       uint64
	TenantID string
}
`
	res := runTenantManifest(t, tenantManifestPolicy("tables.txt", ""), code, "widgets\n")
	vs := onlyRule(res, tenantTableNameMissingRuleID)
	if len(vs) != 1 {
		t.Fatalf("应有恰好一条表名方法缺失，实得 %d：%v", len(vs), textsOf(res.Violations))
	}
	if !strings.Contains(vs[0].Text, "Widget") || !strings.Contains(vs[0].Text, "TableName") {
		t.Fatalf("报错应点名模型与缺省方法名: %s", vs[0].Text)
	}
	// 边界（已写进漂移文案）：解析不出表名就关联不了清单条目 ⇒ widgets 同时
	// 判漂移，两条一起红（deny-by-default；若因此豁免漂移，一条坏模型就能
	// 把真正过期的清单条目藏住）。
	if vs := onlyRule(res, tenantPolicyDriftRuleID); len(vs) != 1 || !strings.Contains(vs[0].Text, "widgets") {
		t.Fatalf("解析不出时清单条目应同报漂移（两种成因不可区分）: %v", textsOf(vs))
	}
}

// TestTenantTableNameMethodUnresolvable 方法体不是字面量 return = 红
// （动态表名执行体读不出，deny-by-default）。
func TestTenantTableNameMethodUnresolvable(t *testing.T) {
	code := `package x

type Gadget struct {
	ID       uint64
	TenantID string
}

func (Gadget) TableName() string { return prefix + "gadgets" }
`
	res := runTenantManifest(t, tenantManifestPolicy("tables.txt", ""), code, "gadgets\n")
	vs := onlyRule(res, tenantTableNameMissingRuleID)
	if len(vs) != 1 {
		t.Fatalf("动态表名应判红，实得 %d：%v", len(vs), textsOf(res.Violations))
	}
	if !strings.Contains(vs[0].Text, "字面量") {
		t.Fatalf("报错应写明字面量要求: %s", vs[0].Text)
	}
	// 同上：解析不出 ⇒ 清单条目同报漂移（两种成因不可区分，文案写明）。
	if vs := onlyRule(res, tenantPolicyDriftRuleID); len(vs) != 1 || !strings.Contains(vs[0].Text, "gadgets") {
		t.Fatalf("解析不出时清单条目应同报漂移: %v", textsOf(vs))
	}
}

// TestTenantTableNameMethodCustom 配置 table_name_method 后按声明方法解析。
func TestTenantTableNameMethodCustom(t *testing.T) {
	code := `package x

type Record struct {
	ID       uint64
	TenantID string
}

func (Record) PhysicalTable() string { return "records" }
`
	res := runTenantManifest(t, tenantManifestPolicy("tables.txt", "PhysicalTable"), code, "records\n")
	if vs := onlyRule(res, tenantTableNameMissingRuleID); len(vs) != 0 {
		t.Fatalf("声明的方法名应被识别: %v", textsOf(vs))
	}
	if vs := onlyRule(res, tenantPolicyMissingRuleID); len(vs) != 0 {
		t.Fatalf("登记应判绿: %v", textsOf(vs))
	}
}

// TestTenantManifestNotDeclaredNoRules 不声明清单 ⇒ 登记面三条不启用：
// 无方法的租户域模型也不报（宿主没买这半边判据，门禁不越权）。
func TestTenantManifestNotDeclaredNoRules(t *testing.T) {
	code := `package x

type Widget struct {
	ID       uint64
	TenantID string
}
`
	res := runTenant(t, code)
	for _, rule := range []string{tenantPolicyMissingRuleID, tenantPolicyDriftRuleID, tenantTableNameMissingRuleID} {
		if vs := onlyRule(res, rule); len(vs) != 0 {
			t.Fatalf("未声明清单时 %s 不得触发: %v", rule, textsOf(vs))
		}
	}
}

// TestTenantManifestFileMissingFailsLoud 清单文件不存在 = 门禁给不出结论即报错
// （读不到清单，双向核对一条都判不了——「给不出结论」不能显示成「没有违规」，
// 与 parseTenantTree 同一纪律）。
func TestTenantManifestFileMissingFailsLoud(t *testing.T) {
	root := fixtureRoot(t, tenantManifestPolicy("absent.txt", ""), manifestModels)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	if _, err := Run(root, pol); err == nil || !strings.Contains(err.Error(), "absent.txt") {
		t.Fatalf("清单缺失必须响亮报错, got %v", err)
	}
}

// TestTenantGateSelfCheckManifestMissing policy 自校验面：声明清单但文件不存在
// = 自校验红（与 roots 指向空目录同型；直测自校验函数——Run 的执行错误会先
// 于自校验短路，两层都要红）。
func TestTenantGateSelfCheckManifestMissing(t *testing.T) {
	tg := &TenantGate{
		Enabled:              boolPtr(true),
		Roots:                []string{"x"},
		AuthContextTypes:     []string{"identity.Actor"},
		TenantFields:         []string{"TenantID"},
		TenantTablesManifest: "absent.txt",
	}
	vs := tenantGateSelfCheck(t.TempDir(), "policy.yaml", tg)
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "tenant_tables_manifest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("清单文件缺失应被自校验点名: %v", textsOf(vs))
	}
}

// TestTenantManifestSeveritiesAndInfos 登记面三条进 severity 分流表与规则
// 元信息（未声明清单时为空——报告面与判定面同口径）。
func TestTenantManifestSeveritiesAndInfos(t *testing.T) {
	gate := &TenantGate{Enabled: boolPtr(true), TenantTablesManifest: "tables.txt"}
	sevOf := map[string]string{}
	tenantGateSeverities(gate, sevOf)
	for _, rule := range []string{tenantPolicyMissingRuleID, tenantPolicyDriftRuleID, tenantTableNameMissingRuleID} {
		if sevOf[rule] != "error" {
			t.Fatalf("%s 的 severity 未登记: %v", rule, sevOf)
		}
	}
	infos := tenantGateRuleInfos(gate)
	if len(infos) != 6 {
		t.Fatalf("声明清单后应报 6 条规则元信息, got %d: %v", len(infos), idsOf(infos))
	}

	empty := &TenantGate{Enabled: boolPtr(true)}
	if got := tenantManifestRuleInfos(empty); got != nil {
		t.Fatalf("未声明清单时登记面元信息应为空: %v", got)
	}
	sevOf2 := map[string]string{}
	tenantGateSeverities(empty, sevOf2)
	if _, ok := sevOf2[tenantPolicyMissingRuleID]; ok {
		t.Fatal("未声明清单时 severity 不得登记")
	}
}

// boolPtr / idsOf 测试小件（tenantgate_test.go 没有现成的）。
func boolPtr(b bool) *bool { return &b }

func idsOf(infos []RuleInfo) []string {
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.ID)
	}
	return out
}

// ── 登记面专用推导根（tenant_table_type_roots）──
//
// 夹具形状对齐真实宿主：读面租户域类型 = 模型 + 服务层载体（输入 DTO 带租户
// 字段但不映射物理表）；登记面只推模型。不收窄时载体 DTO 会被误报缺表名方法。

// narrowRootPolicy 读面双根 + 登记面收窄到 model。
func narrowRootPolicy(tableRoots string) string {
	extra := "  tenant_type_roots: [model, svc]\n"
	if tableRoots != "" {
		extra += "  tenant_table_type_roots: " + tableRoots + "\n"
	}
	return tenantManifestPolicyWithDirs(extra)
}

// tenantManifestPolicyWithDirs 在多目录夹具 policy 上声明登记面。
func tenantManifestPolicyWithDirs(extra string) string {
	return policyHead + `tenant_gate:
  enabled: true
  severity: error
  roots: [svc]
  auth_context_types: [identity.Actor]
  tenant_fields: [TenantID]
  filter_columns: [tenant_id]
  shared_surfaces:
    - dir: model
      name: VisibleTo
      note: 合成判定面
  tenant_tables_manifest: tables.txt
` + extra
}

// narrowRootFixture 两目录仓：model/ 带表名方法的模型，svc/ 无表名方法的载体。
func narrowRootFixture(t *testing.T, polYAML string) *Result {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"model", "svc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "architecture-policy.yaml"), []byte(polYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	model := `package model

type Record struct {
	ID       uint64
	TenantID string
}

func (Record) TableName() string { return "records" }

func VisibleTo(a identity.Actor, r Record) bool { return a.TenantID == r.TenantID }
`
	svc := `package svc

type RecordInput struct {
	Name     string
	TenantID string
}

func (RecordInput) Filter(a identity.Actor) bool { return a.TenantID != "" }
`
	if err := os.WriteFile(filepath.Join(root, "model", "a.go"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "svc", "b.go"), []byte(svc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tables.txt"), []byte("records\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	return res
}

// TestTenantTableTypeRootsNarrow 收窄后：服务层载体不进登记面 ⇒ 不报缺表名方法；
// 模型面照判（表已登记 ⇒ 三条全绿）。
func TestTenantTableTypeRootsNarrow(t *testing.T) {
	res := narrowRootFixture(t, narrowRootPolicy("[model]"))
	for _, rule := range []string{tenantPolicyMissingRuleID, tenantPolicyDriftRuleID, tenantTableNameMissingRuleID} {
		if vs := onlyRule(res, rule); len(vs) != 0 {
			t.Fatalf("收窄后 %s 应全绿（载体不该进登记面）: %v", rule, textsOf(vs))
		}
	}
}

// TestTenantTableTypeRootsDefaultBackwardCompat 不声明收窄 = 与旧行为逐字一致：
// 载体 DTO 照样被判缺表名方法（证明新字段真的在起作用，且缺省不改旧口径）。
func TestTenantTableTypeRootsDefaultBackwardCompat(t *testing.T) {
	res := narrowRootFixture(t, narrowRootPolicy(""))
	vs := onlyRule(res, tenantTableNameMissingRuleID)
	if len(vs) != 1 {
		t.Fatalf("缺省口径下应恰好一条缺表名方法（RecordInput），实得 %d：%v", len(vs), textsOf(vs))
	}
	if !strings.Contains(vs[0].Text, "RecordInput") {
		t.Fatalf("应点名载体类型: %s", vs[0].Text)
	}
}

// TestTenantTableTypeRootsMissingDir 收窄根不存在 = 自校验点名（登记面对该树
// 完全隐形的形态不能静默）。
func TestTenantTableTypeRootsMissingDir(t *testing.T) {
	tg := &TenantGate{
		Enabled:              boolPtr(true),
		Roots:                []string{"x"},
		AuthContextTypes:     []string{"identity.Actor"},
		TenantFields:         []string{"TenantID"},
		TenantTableTypeRoots: []string{"absent-model-dir"},
	}
	vs := tenantGateSelfCheck(t.TempDir(), "policy.yaml", tg)
	found := false
	for _, v := range vs {
		if strings.Contains(v.Text, "absent-model-dir") {
			found = true
		}
	}
	if !found {
		t.Fatalf("收窄根缺失应被自校验点名: %v", textsOf(vs))
	}
}

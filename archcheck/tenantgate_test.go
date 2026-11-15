package archcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// tenantPolicy 造一份启用租户门的 policy（形状全部为合成名字）。
func tenantPolicy(extra string) string {
	return policyHead + `tenant_gate:
  enabled: true
  severity: error
  roots: [x]
  auth_context_types: [identity.Actor]
  tenant_fields: [TenantID]
  filter_columns: [tenant_id]
  shared_surfaces:
    - dir: x
      name: VisibleTo
      note: 合成判定面
` + extra
}

// runTenant 跑 fixture 门禁并返回结果。
func runTenant(t *testing.T, code string) *Result {
	t.Helper()
	root := fixtureRoot(t, tenantPolicy(""), code)
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

func onlyRule(res *Result, rule string) []Violation {
	var out []Violation
	for _, v := range res.Violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

// tenantTypesCode 是各用例共用的类型面与共享判定面（不含待判函数）。
const tenantTypesCode = `package x

type Record struct {
	ID       uint64
	TenantID string
}

func VisibleTo(a identity.Actor, r Record) bool { return a.TenantID == r.TenantID }
`

// tenantBaseCode 判据 1 的红样本：返回租户域类型却无任何证据。
const tenantBaseCode = tenantTypesCode + `
func ListRecords(a identity.Actor) []Record {
	if a.TenantID == "" {
		return nil
	}
	return nil
}
`

// TestTenantSurfaceMissingEvidence 判据 1 红：带授权上下文 + 返回租户域类型，
// 但既没调共享判定面、也没过滤列、也没 tenant:iso 声明。
func TestTenantSurfaceMissingEvidence(t *testing.T) {
	res := runTenant(t, tenantBaseCode)
	vs := onlyRule(res, tenantSurfaceMissingRuleID)
	if len(vs) != 1 {
		t.Fatalf("应有恰好一条 missing-evidence，实得 %d：%v", len(vs), textsOf(res.Violations))
	}
	if vs[0].Path != "x/a.go" || vs[0].Line == 0 {
		t.Fatalf("违规位置不符: %+v", vs[0])
	}
	if !strings.Contains(vs[0].Text, "tenant:iso") {
		t.Fatalf("报错应给出显式声明修法: %s", vs[0].Text)
	}
}

// TestTenantEvidenceViaSharedSurface 判据 1 绿：调用登记的共享判定面。
func TestTenantEvidenceViaSharedSurface(t *testing.T) {
	res := runTenant(t, tenantTypesCode+`
func ListVisible(a identity.Actor) []Record {
	if !VisibleTo(a, Record{}) {
		return nil
	}
	return nil
}
`)
	if vs := onlyRule(res, tenantSurfaceMissingRuleID); len(vs) != 0 {
		t.Fatalf("调用共享判定面应算证据: %v", textsOf(vs))
	}
}

// TestTenantEvidenceViaFilterColumn 判据 1 绿：查询条件含 policy 声明的过滤列。
func TestTenantEvidenceViaFilterColumn(t *testing.T) {
	res := runTenant(t, tenantTypesCode+`
func ListFiltered(a identity.Actor) []Record {
	_ = db.Where("tenant_id = ?", a.TenantID)
	return nil
}
`)
	if vs := onlyRule(res, tenantSurfaceMissingRuleID); len(vs) != 0 {
		t.Fatalf("过滤列应算证据: %v", textsOf(vs))
	}
}

// TestTenantEvidenceViaIsoDecl 判据 1 绿：`// tenant:iso <理由>` 显式声明。
func TestTenantEvidenceViaIsoDecl(t *testing.T) {
	res := runTenant(t, tenantTypesCode+`
// tenant:iso 隔离由调用方会话守卫完成
func ListDelegated(a identity.Actor) []Record {
	_ = a
	return nil
}
`)
	if vs := onlyRule(res, tenantSurfaceMissingRuleID); len(vs) != 0 {
		t.Fatalf("tenant:iso 声明应算证据: %v", textsOf(vs))
	}
}

// TestTenantViewerDropped 判据 2 红：授权上下文形参完全没用。
func TestTenantViewerDropped(t *testing.T) {
	res := runTenant(t, tenantTypesCode+`
func CountRecords(a identity.Actor) int { return 0 }
`)
	vs := onlyRule(res, tenantViewerDroppedRuleID)
	if len(vs) != 1 || !strings.Contains(vs[0].Text, "完全没用") {
		t.Fatalf("应报 viewer-dropped: %v", textsOf(res.Violations))
	}
}

// TestTenantSharedSurfaceDrift 判据 3 红：登记在册的判定面在代码里不存在。
func TestTenantSharedSurfaceDrift(t *testing.T) {
	root := fixtureRoot(t, tenantPolicy("    - dir: x\n      name: MissingSurface\n      note: 已改名\n"), tenantBaseCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	vs := onlyRule(res, tenantSurfaceDriftRuleID)
	if len(vs) != 1 || !strings.Contains(vs[0].Text, "MissingSurface") {
		t.Fatalf("应报登记面漂移: %v", textsOf(res.Violations))
	}
}

// TestTenantQualifiedAuthTypeNotFalsePositive 限定名认证类型不误伤同名类型：
// 代码里的 context.Context 不是 identity.Actor。
func TestTenantQualifiedAuthTypeNotFalsePositive(t *testing.T) {
	res := runTenant(t, `package x

import "context"

type Record struct {
	ID       uint64
	TenantID string
}

func VisibleTo(a struct{}, r Record) bool { return true }

func ListRecords(ctx context.Context) []Record {
	return nil
}
`)
	if n := len(res.Violations); n != 0 {
		t.Fatalf("context.Context 不得被 identity.Actor 形状误判: %v", textsOf(res.Violations))
	}
}

// TestTenantImportAliasResolved import 别名仍能认出授权上下文（改名别名绕不过判据）。
func TestTenantImportAliasResolved(t *testing.T) {
	res := runTenant(t, `package x

import id "example.com/identity"

type Record struct {
	ID       uint64
	TenantID string
}

func VisibleTo(a id.Actor, r Record) bool { return true }

func ListAliased(a id.Actor) []Record {
	if a.TenantID == "" {
		return nil
	}
	return nil
}
`)
	if vs := onlyRule(res, tenantSurfaceMissingRuleID); len(vs) != 1 {
		t.Fatalf("import 别名后的授权上下文仍须判定: %v", textsOf(res.Violations))
	}
}

// TestTenantGateShapeSelfCheck 自校验：启用却没有扫描面 = 门禁判不动，必须红。
func TestTenantGateShapeSelfCheck(t *testing.T) {
	root := fixtureRoot(t, policyHead+`tenant_gate:
  enabled: true
  auth_context_types: [Actor]
  tenant_fields: [TenantID]
`, `package x

type Record struct {
	TenantID string
}
`)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	found := false
	for _, v := range onlyRule(res, selfCheckRuleID) {
		if strings.Contains(v.Text, "tenant_gate") && strings.Contains(v.Text, "roots") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 roots 的 tenant_gate 必须被自校验报红: %v", textsOf(res.Violations))
	}
}

// TestTenantGateDefaultOff 段缺省即关闭：没有形状时不误报任何东西。
func TestTenantGateDefaultOff(t *testing.T) {
	root := fixtureRoot(t, policyHead, tenantBaseCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	for _, v := range res.Violations {
		if v.Rule == tenantSurfaceMissingRuleID || v.Rule == tenantViewerDroppedRuleID || v.Rule == tenantSurfaceDriftRuleID {
			t.Fatalf("tenant_gate 缺省关闭时不应有租户门违规: %v", textsOf(res.Violations))
		}
	}
}

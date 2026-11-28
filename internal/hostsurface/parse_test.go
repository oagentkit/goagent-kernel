// parse_test.go —— 登记面 md 解析的可执行判据（合成文档，不读盘）。
package hostsurface

import (
	"strings"
	"testing"
)

// fullDoc 合成一份结构完整的接入面登记面（同时是 docs/HOST-SURFACE.md 的格式样板）。
func fullDoc() string {
	var b strings.Builder
	b.WriteString("# 宿主接入面\n\nupdated: 2026-11-28\n\n")
	b.WriteString("## 主链六步对照\n\n")
	b.WriteString("| 步骤 | 宿主提供 | kernel 兑现 |\n")
	b.WriteString("|---|---|---|\n")
	steps := []struct{ id, name string }{
		{"create", "创建"}, {"claim", "领取"}, {"execute", "执行"},
		{"commit", "提交"}, {"recover", "恢复"}, {"intervene", "介入"},
	}
	for _, s := range steps {
		b.WriteString("| " + s.id + " " + s.name + " | 宿-" + s.id + " | 内-" + s.id + " |\n")
	}
	b.WriteString("\n## 包三分类\n\n")
	b.WriteString("| pkg | 分类 | 职责 |\n")
	b.WriteString("|---|---|---|\n")
	b.WriteString("| contract | 必须接 | 编排运行契约 |\n")
	b.WriteString("| dispatch | 可选 | 改派兜底 |\n")
	b.WriteString("| internal/routeledger | 内部件 | 路线对账判据 |\n")
	return b.String()
}

// TestParseDocFull 全量文档：两张表都解析对，步骤 id 取首词。
func TestParseDocFull(t *testing.T) {
	reg, err := ParseDoc(fullDoc())
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if reg.Updated != "2026-11-28" {
		t.Fatalf("版本戳应为 2026-11-28，实得 %q", reg.Updated)
	}
	if len(reg.Steps) != 6 {
		t.Fatalf("步骤应 6 行，实得 %d", len(reg.Steps))
	}
	if reg.Steps[0].ID != "create" || reg.Steps[0].Name != "创建" {
		t.Fatalf("首行应为 create/创建，实得 %q/%q", reg.Steps[0].ID, reg.Steps[0].Name)
	}
	if reg.Steps[0].HostProvides != "宿-create" {
		t.Fatalf("宿主提供格解析错，实得 %q", reg.Steps[0].HostProvides)
	}
	if len(reg.Packages) != 3 {
		t.Fatalf("包分类应 3 行，实得 %d", len(reg.Packages))
	}
	if reg.Packages[2].Pkg != "internal/routeledger" || reg.Packages[2].Class != ClassInternal {
		t.Fatalf("第三行解析错，实得 %+v", reg.Packages[2])
	}
}

// TestParseDocRoundTrip 解析结果过得了 Judge（格式样板自洽）。
func TestParseDocRoundTrip(t *testing.T) {
	reg, err := ParseDoc(fullDoc())
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	f := Facts{
		RegisteredPackages: map[string]bool{
			"contract": true, "dispatch": true, "internal/routeledger": true,
		},
		DocImports: map[string]bool{},
	}
	if res := Judge(reg, f); len(res.Problems) != 0 {
		t.Fatalf("格式样板应判全绿，实得：%v", res.Problems)
	}
}

// TestParseDocMissingUpdated 没有版本戳行 ⇒ 解析错误。
func TestParseDocMissingUpdated(t *testing.T) {
	doc := strings.Replace(fullDoc(), "updated: 2026-11-28\n\n", "", 1)
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "版本戳") {
		t.Fatalf("缺版本戳应报错并说明，实得：%v", err)
	}
}

// TestParseDocMissingStepTable 缺主链对照表 ⇒ 解析错误并点名列。
func TestParseDocMissingStepTable(t *testing.T) {
	doc := fullDoc()
	doc = doc[:strings.Index(doc, "## 主链六步对照")]
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "主链对照表") {
		t.Fatalf("缺步骤表应报错并点名，实得：%v", err)
	}
}

// TestParseDocMissingColumn 分类表缺「职责」列 ⇒ 解析错误。
func TestParseDocMissingColumn(t *testing.T) {
	doc := strings.Replace(fullDoc(), "| 职责 |", "| 备注 |", 1)
	if _, err := ParseDoc(doc); err == nil || !strings.Contains(err.Error(), "职责") {
		t.Fatalf("缺列应报错并点名列名，实得：%v", err)
	}
}

// TestParseDocShortRow 行比表头短 ⇒ 不 panic，缺格按空处理、由 Judge 判。
func TestParseDocShortRow(t *testing.T) {
	doc := strings.Replace(fullDoc(),
		"| contract | 必须接 | 编排运行契约 |", "| contract | 必须接 |", 1)
	reg, err := ParseDoc(doc)
	if err != nil {
		t.Fatalf("短行不该是解析错误，实得：%v", err)
	}
	if reg.Packages[0].Duty != "" {
		t.Fatalf("短行的职责格应为空，实得 %q", reg.Packages[0].Duty)
	}
	res := Judge(reg, Facts{
		RegisteredPackages: map[string]bool{"contract": true, "dispatch": true, "internal/routeledger": true},
		DocImports:         map[string]bool{},
	})
	if !hasProblem(res.Problems, "contract") {
		t.Fatalf("短行的空职责格应由 Judge 报，实得：%v", res.Problems)
	}
}

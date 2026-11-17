// audit_wordlist_test.go —— 介入动作词表必须单源且契约指向它（issue #372）。
//
// 此前包里有两份动作词表：导出的 Action* 常量是 4 值（含 ActionQuery），
// 私有 actions 是 3 值（不含查询）。契约注释写「词表见 Action* 常量」，
// 参考实现 MemAuditor 却按私有 3 值拒收——宿主照契约实现会接受查询动作与
// 词表外动作，内核参考实现拒绝；同一条留痕两边结论相反。
//
// 判据：
//   - 存在导出的合法值判定入口（宿主不必手抄）；
//   - 该入口与 MemAuditor.Append 的接受集逐字一致；
//   - AuditEntry.Action 的契约注释指向这套导出词表，并写明查询动作不经留痕。

package controlplane

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/oekit/pkg/errors"
)

// TestInterventionActionVocabularySingleSource 导出词表就是 MemAuditor 的接受集。
func TestInterventionActionVocabularySingleSource(t *testing.T) {
	all := AllInterventionActions()
	if len(all) != 3 {
		t.Fatalf("介入动作词表应恰好 3 个（force_release / adjudicate / terminate），实得 %v", all)
	}
	for _, want := range []Action{ActionForceRelease, ActionAdjudicate, ActionTerminate} {
		if !IsKnownInterventionAction(want) {
			t.Errorf("介入动作 %q 不在导出词表内", want)
		}
	}
	if IsKnownInterventionAction(ActionQuery) {
		t.Fatal("ActionQuery 是查询动作，不经留痕，不得算介入动作")
	}

	// 返回副本：宿主改写不得污染内核判定面。
	all[0] = Action("__tampered__")
	if IsKnownInterventionAction("__tampered__") {
		t.Fatal("AllInterventionActions 返回了共享底层数组：宿主改写污染了词表")
	}
}

// TestKnownInterventionActionMatchesAuditor 导出判定入口与参考实现的接受集
// 必须逐字一致：4 个导出常量 + 词表外取值逐个过 Append。
func TestKnownInterventionActionMatchesAuditor(t *testing.T) {
	ctx := context.Background()
	cases := []Action{
		ActionQuery,
		ActionForceRelease,
		ActionAdjudicate,
		ActionTerminate,
		"bogus_action",
		"force-release",
		"",
	}
	for _, action := range cases {
		want := action == ActionForceRelease || action == ActionAdjudicate || action == ActionTerminate
		if got := IsKnownInterventionAction(action); got != want {
			t.Errorf("IsKnownInterventionAction(%q)=%v，want %v", action, got, want)
		}

		var aud MemAuditor
		err := aud.Append(ctx, AuditEntry{Operator: "op", Action: action, Target: "t", Basis: "b"})
		accepted := err == nil
		if accepted != want {
			t.Errorf("MemAuditor.Append(%q) accepted=%v，导出词表说 %v（err=%v）",
				action, accepted, want, err)
		}
		if !want && !errors.Is(err, ErrInvalidAudit) {
			t.Errorf("拒收 %q 应返回 ErrInvalidAudit，实得 %v", action, err)
		}
	}
}

// TestAuditActionContractPointsAtExportedVocabulary 契约注释必须指向导出的
// 介入词表，并写明查询动作 ActionQuery 不经留痕。
//
// 这一条防的是 issue #372 的根因：词表在实现里改了，而给宿主看的契约仍
// 指向另一份（更宽的）清单。
func TestAuditActionContractPointsAtExportedVocabulary(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "audit.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 audit.go 失败: %v", err)
	}

	var actionDoc, auditorDoc string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			switch name := ts.Name.Name; name {
			case "AuditEntry":
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					if len(field.Names) == 1 && field.Names[0].Name == "Action" && field.Doc != nil {
						actionDoc = field.Doc.Text()
					}
				}
			case "Auditor":
				// 单独一条 `type Auditor interface` 时注释挂在 GenDecl 上；
				// 分组声明时挂在 TypeSpec 上，两处都取。
				switch {
				case ts.Doc != nil:
					auditorDoc = ts.Doc.Text()
				case gd.Doc != nil:
					auditorDoc = gd.Doc.Text()
				}
			}
		}
	}

	if actionDoc == "" {
		t.Fatal("没有取到 AuditEntry.Action 的契约注释")
	}
	if !strings.Contains(actionDoc, "AllInterventionActions()") {
		t.Errorf("AuditEntry.Action 注释没有指向 AllInterventionActions()（宿主会照旧手抄/按宽松清单实现）: %q", actionDoc)
	}
	if !strings.Contains(actionDoc, "ActionQuery") || !strings.Contains(actionDoc, "不经留痕") {
		t.Errorf("AuditEntry.Action 注释没有写明 ActionQuery 不经留痕: %q", actionDoc)
	}
	if strings.Contains(actionDoc, "词表见 Action* 常量") {
		t.Errorf("AuditEntry.Action 注释仍指向含 ActionQuery 的宽清单: %q", actionDoc)
	}

	if auditorDoc == "" {
		t.Fatal("没有取到 Auditor 接口的契约注释")
	}
	if !strings.Contains(auditorDoc, "词表外") {
		t.Errorf("Auditor 契约没有声明「词表外动作同样拒绝」: %q", auditorDoc)
	}
}

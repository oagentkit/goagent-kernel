// action_vocab_test.go —— 介入动作词表的单一真相（issue #372）。
//
// 此前包里有两份词表：4 个导出常量（含查询动作）与一份 3 值的私有清单，
// 而契约文本指向的是前者、参考实现用的是后者——「按契约实现的宿主」与
// 「参考实现」对同一条留痕给出相反结论（ActionQuery 被拒收、词表外动作
// 被收下）。
package controlplane

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// auditEntryActionDoc 取 AuditEntry.Action 字段的文档注释（走 go/ast：
// 注释挂在哪个声明上是结构性问题，逐行正则分不出「字段注释」与「类型
// 注释」）。
func auditEntryActionDoc(t *testing.T) string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "audit.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 audit.go 失败: %v", err)
	}
	for _, decl := range parsed.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "AuditEntry" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == "Action" {
						if field.Doc == nil {
							t.Fatal("AuditEntry.Action 没有字段注释（契约文本必须写在注释里）")
						}
						return field.Doc.Text()
					}
				}
			}
		}
	}
	t.Fatal("audit.go 里找不到 AuditEntry.Action 字段")
	return ""
}

// TestAuditEntryActionDocNamesTheRealVocabulary 字段注释所指引的词表必须
// 就是实现用的那一份（两套口径说清：查询动作不经留痕，留痕面只接受
// AllInterventionActions() 内的值）。
func TestAuditEntryActionDocNamesTheRealVocabulary(t *testing.T) {
	doc := auditEntryActionDoc(t)
	if !strings.Contains(doc, "ActionQuery") {
		t.Errorf("AuditEntry.Action 的注释必须点明查询动作（ActionQuery）不经留痕：%q", doc)
	}
	if !strings.Contains(doc, "AllInterventionActions()") {
		t.Errorf("AuditEntry.Action 的注释必须指向留痕面的唯一词表 AllInterventionActions()：%q", doc)
	}
}

// TestInterventionActionVocabulary 留痕面的受理集合必须与词表逐字一致：
// 4 个导出常量 + 词表外取值，逐条判。
//
// 反向验证：
//   - 把 ActionQuery 塞回留痕词表（4 值）⇒ 「查询动作」那条转红；
//   - 把某个词表外值塞进词表 ⇒ 对应那条转红。
func TestInterventionActionVocabulary(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		accept bool
	}{
		{"导出常量：查询（不经留痕）", ActionQuery, false},
		{"导出常量：强制释放", ActionForceRelease, true},
		{"导出常量：裁决", ActionAdjudicate, true},
		{"导出常量：终止", ActionTerminate, true},
		{"词表外：随便一个名字", Action("bogus_action"), false},
		{"词表外：空串", Action(""), false},
		{"词表外：查询的近亲", Action("query_all"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsKnownInterventionAction(tc.action); got != tc.accept {
				t.Fatalf("%q 的受理判定=%v，期望 %v", tc.action, got, tc.accept)
			}
			// 留痕面必须与判定面同一个口径（不另写一套闸）。
			err := (&MemAuditor{}).Append(context.Background(), AuditEntry{
				Operator: "op-1", Action: tc.action, Target: "t1", Basis: "合成依据",
			})
			if accepted := err == nil; accepted != tc.accept {
				t.Fatalf("留痕面的受理判定=%v（err=%v），期望 %v", accepted, err, tc.accept)
			}
		})
	}
}

// TestAllInterventionActionsIsTheSingleSource 导出面必须是词表的唯一出口：
// 宿主不必手抄，且拿到的是**副本**（改写它不影响判定面）。
//
// 反向验证：把词表扩到 4 值（含查询动作）⇒ 本条转红。
func TestAllInterventionActionsIsTheSingleSource(t *testing.T) {
	got := AllInterventionActions()
	want := []Action{ActionAdjudicate, ActionForceRelease, ActionTerminate}
	if !slices.Equal(got, want) {
		t.Fatalf("介入动作词表漂移：AllInterventionActions=%v，期望 %v", got, want)
	}
	// 返回的是副本：调用方改写它不影响判定面。
	got[0] = Action("mutated_by_host")
	if !IsKnownInterventionAction(ActionAdjudicate) {
		t.Fatal("AllInterventionActions 返回了共享底层数组：宿主改写污染了词表")
	}
}

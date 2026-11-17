// closedby_test.go —— closed_by 词表的自校验（issue #391）。
//
// 词表下沉到本包后，唯一的漂移面变成「加了 ClosedBy* 常量却忘了加进
// ClosedByDefaults」：生产方 reconcile 引用得到新值、消费方 watchdog 的缺省
// 表却仍不认它——正是 #391 的原始形态在包内重演。这里用 AST 枚举本文件的
// 全部 ClosedBy* 常量，与 ClosedByDefaults() 做集合恒等断言。
package eventbus

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestClosedByDefaultsCoversAllConstants 缺省词表与 ClosedBy* 常量集合恒等。
//
// 反向验证：从 closedby.go 删掉 ClosedByDeadOwner 常量而不动 ClosedByDefaults
// ⇒ 本用例转红；反向另一向：加一个 ClosedByX 常量但不加进缺省函数 ⇒ 转红。
func TestClosedByDefaultsCoversAllConstants(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "closedby.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 closedby.go: %v", err)
	}

	declared := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		// 词表常量必须保持字面量：写成常量表达式会让扫描器读不到值，
		// 最终退化成「无法机器判」。本用例把这条也一并钉住。
		if vs.Type != nil || len(vs.Values) != len(vs.Names) {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "ClosedBy") {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("常量 %s 不是字符串字面量：词表必须可被机器枚举", name.Name)
			}
			v, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				t.Fatalf("常量 %s 的字面量无法解析: %v", name.Name, uerr)
			}
			if declared[v] {
				t.Fatalf("归因值 %q 重复声明：同一取值两个常量，消费方查表没有意义", v)
			}
			declared[v] = true
		}
		return true
	})
	if len(declared) == 0 {
		t.Fatal("closedby.go 未枚举到任何 ClosedBy* 常量：扫描器失效等于这条断言恒绿")
	}

	got := map[string]bool{}
	for _, v := range ClosedByDefaults() {
		if got[v] {
			t.Fatalf("ClosedByDefaults 中归因值 %q 重复", v)
		}
		got[v] = true
	}

	for v := range declared {
		if !got[v] {
			t.Errorf("ClosedBy* 常量 %q 不在 ClosedByDefaults 里：生产方发得出、消费方不认——#391 的漂移形态", v)
		}
	}
	for v := range got {
		if !declared[v] {
			t.Errorf("ClosedByDefaults 里的 %q 没有对应的 ClosedBy* 常量：字形漂移的面", v)
		}
	}
}

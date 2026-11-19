// symbols_test.go —— 落点解析（go/ast）的用例。
//
// 单独成文件：这一层判的是「符号是不是真的声明在那个文件里」，与能力侧
// 的判据是两层东西 ⇒ 各自成文件，任一层加用例都不挤另一层的行数余量。
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeclaredSymbolsFindsMethodForm 符号解析认「类型.方法」形态（go/ast 判定）。
func TestDeclaredSymbolsFindsMethodForm(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	syms, err := declaredSymbols(filepath.Join(root, "pkgdemo", "store.go"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !syms["Store"] {
		t.Fatal("应找到类型 Store")
	}
	if !syms["Store.Apply"] {
		t.Fatal("应找到方法 Store.Apply")
	}
	if syms["Store.Nope"] {
		t.Fatal("不该找到 Store.Nope")
	}
	if syms["Nope"] {
		t.Fatal("不该找到 Nope")
	}
}

// TestUnderRoot 落点归类：只有代码根之下才算「自证」。
func TestUnderRoot(t *testing.T) {
	cases := []struct {
		root string
		path string
		want bool
	}{
		{"pkgdemo", "pkgdemo/store.go", true},
		{"pkgdemo", "pkgdemo", true},
		{"pkgdemo", "pkgdemo2/store.go", false},
		{"pkgdemo", "other/store.go", false},
		{"", "pkgdemo/store.go", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := underRoot(c.root, c.path); got != c.want {
			t.Fatalf("underRoot(%q, %q) = %v, 期望 %v", c.root, c.path, got, c.want)
		}
	}
}

// TestResolveRefRejectsBadShapes 落点形态不对 ⇒ 判不成立且给出理由。
func TestResolveRefRejectsBadShapes(t *testing.T) {
	root := writeRoot(t, demoMatrix, demoSelection)
	if ok, reason := resolveRef(root, "pkgdemo/store.go"); ok || reason == "" {
		t.Fatal("缺 #Symbol 必须判不成立且给出理由")
	}
	if ok, _ := resolveRef(root, "pkgdemo/store.go#Store"); !ok {
		t.Fatal("合法落点必须判成立")
	}
}

// TestDeclaredSymbolsOnUnparsableFile 解析不了的文件 ⇒ 报错，不当「符号不存在」。
func TestDeclaredSymbolsOnUnparsableFile(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.go")
	if err := os.WriteFile(bad, []byte("package x\nfunc (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := declaredSymbols(bad); err == nil {
		t.Fatal("解析失败必须报错（不能当成「符号不存在」——那是两种不同的结论）")
	}
}

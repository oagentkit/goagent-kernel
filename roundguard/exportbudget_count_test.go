package roundguard_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ── 导出面计数引擎（判据在 exportbudget_test.go，这里只管「怎么数」）──
//
// 口径（什么算、什么不算）写在门禁文件的头注里——那里是**规则的声明面**，
// 本文件是它的**执行面**。两者分开放的理由与本仓「声明面 / 执行面分离」同款：
// 规则改在声明面，执行面不动；反过来，执行面被放松时，门禁文件里的自检用例
// （TestCountingRules*）会当场变红。
//
// 为什么全走 go/ast 而不逐行看文本：import / 声明 / 字段 / 方法的书写形态远不止
// 一种，逐行正则必然漏，且漏得毫无声响（与本仓 archcheck 当年同一个病）。

// exportedSurface 实测一个目录下的导出面（总数 + 逐文件）。
func exportedSurface(t *testing.T, dir string) (int, map[string]int) {
	t.Helper()
	files, err := productionGoFiles(dir)
	if err != nil {
		t.Fatalf("列生产文件失败：%v", err)
	}
	total := 0
	perFile := make(map[string]int, len(files))
	for _, path := range files {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败：%v", path, perr)
		}
		n := countExportedSymbols(f)
		perFile[filepath.Base(path)] = n
		total += n
	}
	return total, perFile
}

// productionGoFiles 列目录下的**生产** .go 文件（排除 _test.go）。
//
// 排除 _test.go 是口径的一部分，不是实现细节：门禁数的是**交付面**（宿主
// import 得到的符号），测试函数宿主 import 不到。
func productionGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	slices.Sort(out)
	return out, nil
}

// countExportedSymbols 数一个文件里的导出符号数（口径见门禁文件头注）。
func countExportedSymbols(f *ast.File) int {
	n := 0
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				// 包级函数。
				if d.Name.IsExported() {
					n++
				}
				continue
			}
			// 导出方法：方法名导出 **且** 接收者类型名导出
			//（未导出类型上的方法宿主 import 不到，不算交付面）。
			if d.Name.IsExported() && ast.IsExported(receiverTypeName(d.Recv)) {
				n++
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						n++
						n += countExportedFields(s.Type)
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							n++
						}
					}
				}
			}
		}
	}
	return n
}

// countExportedFields 数一个导出结构体的导出字段数。
func countExportedFields(expr ast.Expr) int {
	st, ok := unwrapStruct(expr)
	if !ok {
		return 0
	}
	n := 0
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			// 嵌入字段：被嵌入类型的名字成为本包的名字（并把它的导出方法提升上来）。
			if name := embeddedFieldName(field.Type); ast.IsExported(name) {
				n++
			}
			continue
		}
		for _, name := range field.Names {
			if name.IsExported() {
				n++
			}
		}
	}
	return n
}

// unwrapStruct 取类型表达式底层的结构体（泛型要拆一层索引）。
func unwrapStruct(expr ast.Expr) (*ast.StructType, bool) {
	switch e := expr.(type) {
	case *ast.StructType:
		return e, true
	case *ast.IndexExpr:
		return unwrapStruct(e.X)
	case *ast.IndexListExpr:
		return unwrapStruct(e.X)
	}
	return nil, false
}

// receiverTypeName 取接收者类型名（剥指针、剥泛型索引）。
//
// 跨包接收者（a.T）取不到本包的类型名 ⇒ 返回空串 ⇒ 不计入（那种方法不可能声明
// 在本包文件里，但口径上要认得出来）。
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return baseTypeName(recv.List[0].Type)
}

// baseTypeName 取类型表达式的名字（剥指针与泛型索引）。
func baseTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return baseTypeName(e.X)
	case *ast.IndexExpr:
		return baseTypeName(e.X)
	case *ast.IndexListExpr:
		return baseTypeName(e.X)
	}
	return ""
}

// embeddedFieldName 取嵌入字段的类型名（它同时是本包暴露出去的那个名字）。
func embeddedFieldName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return embeddedFieldName(e.X)
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.IndexExpr:
		return embeddedFieldName(e.X)
	case *ast.IndexListExpr:
		return embeddedFieldName(e.X)
	}
	return ""
}

// countSource 解析一段源码并数它的导出符号数（自检夹具的入口）。
func countSource(t *testing.T, src string) int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fixture.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析夹具源码失败：%v", err)
	}
	return countExportedSymbols(f)
}

// writeFile 写一个夹具文件。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写 %s 失败：%v", path, err)
	}
}

// formatPerFile 把逐文件计数排成一行（失败信息要能一眼看出是哪个文件变了）。
func formatPerFile(perFile map[string]int) string {
	names := make([]string, 0, len(perFile))
	for name := range perFile {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+strconv.Itoa(perFile[name]))
	}
	return strings.Join(parts, " ")
}

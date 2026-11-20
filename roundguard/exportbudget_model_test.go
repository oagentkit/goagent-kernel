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

// ── 导出面计数的包级模型与 AST 解析（口径在 exportbudget_count_test.go）──
//
// 为什么需要**包级模型**（跨文件）：导出面不是「逐个文件数一遍」能得到的——
//  ① 「未导出类型被导出类型嵌入 ⇒ 它的导出方法被提升进导出面」这条关系要跨文件
//     才看得出来（嵌入在 A 文件、被嵌入的类型声明在 B 文件）；
//  ② 「给导出接口加一个方法 ⇒ 宿主实现方被迫多实现一个」这条关系必须按**方法集**
//     而不是按**声明**来数。
// 故先把整个包（生产文件）的类型与方法声明收成一张表，再逐文件计数。

// packageModel 汇总一个包的生产文件里的类型与方法声明。
//
// 只收**导出方法名**（方法名导出即可；接收者类型导不导出是另一层判定）：
// 接收者类型是否导出，决定这条方法是不是「宿主 import 得到的名字」；而
// 「被提升进导出面的方法」用的正是「方法名导出」这一半。
type packageModel struct {
	types   map[string]*ast.TypeSpec
	methods map[string]map[string]bool // 接收者类型名 → 导出方法名集合
}

// newPackageModel 收一张包级表（类型声明 + 方法声明）。
func newPackageModel(files []*ast.File) *packageModel {
	m := &packageModel{
		types:   make(map[string]*ast.TypeSpec),
		methods: make(map[string]map[string]bool),
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || !d.Name.IsExported() {
					continue
				}
				recv := receiverTypeName(d.Recv)
				if recv == "" {
					continue
				}
				if m.methods[recv] == nil {
					m.methods[recv] = map[string]bool{}
				}
				m.methods[recv][d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, dup := m.types[ts.Name.Name]; !dup {
						m.types[ts.Name.Name] = ts
					}
				}
			}
		}
	}
	return m
}

// exportedSurface 实测一个目录下的导出面（总数 + 逐文件）。
func exportedSurface(t *testing.T, dir string) (int, map[string]int) {
	t.Helper()
	files, err := productionGoFiles(dir)
	if err != nil {
		t.Fatalf("列生产文件失败：%v", err)
	}
	parsed := make([]*ast.File, 0, len(files))
	for _, path := range files {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败：%v", path, perr)
		}
		parsed = append(parsed, f)
	}
	// 包级模型必须**先建后数**：提升关系与方法集都要跨文件看。
	model := newPackageModel(parsed)
	total := 0
	perFile := make(map[string]int, len(files))
	for i, path := range files {
		n := countExportedSymbols(parsed[i], model)
		perFile[filepath.Base(path)] = n
		total += n
	}
	return total, perFile
}

// productionGoFiles 列目录下的**生产** .go 文件（排除 _test.go）。
//
// 排除 _test.go 是口径的一部分，不是实现细节：门禁数的是**交付面**（宿主
// import 得到的符号），测试函数宿主 import 不到。
//
// ⚠️ **只列本目录**（不递归子目录）：测量面 = 本包目录下的生产文件。将来若
// 出现 roundguard/xxx 子包，它的导出符号对这道门禁**不可见**（那时要另设门禁
// 或把子包纳入）——这是口径的已知边界，不是隐含假设。
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
	return countExportedSymbols(f, newPackageModel([]*ast.File{f}))
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

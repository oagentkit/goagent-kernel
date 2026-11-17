// seam.go —— 接缝（宿主实现、内核调用）的清单扫描与契约门禁。
//
// # 什么是「接缝」
//
// 接缝是**宿主与内核之间的接地面**：宿主填的回调、内核调的方法。
// 本仓的纪律是「机制面不认识领域」——而接缝恰恰是机制面**把控制权交出去**
// 的地方：宿主在回调里可以回查本包、可以做 I/O、可以 panic。
//
// # 这条门禁判什么、不判什么
//
// 判的是**契约成文**（issue #369/#370 的形态 B）：每个接缝声明处必须写齐
// 三条约束——不得重入（no-reentry）、不得阻塞（no-blocking）、
// panic 已隔离（panic-isolated）——外加**清单完备**（issue #400）：
// 扫描器产出的接缝集合必须与 policy 登记的集合逐条相等。
//
// # 根本局限：标记是**承诺**，不是事实
//
// 声明了 no-reentry 而代码仍在锁内调宿主回调，这条门禁照样绿。
// 它保证的是「契约被写下」，不是「契约被兑现」。兑现面靠**契约用例**
// （issue #370 的形态 C）与运行期行为保证。
//
// 形态 A（纯 AST 判「调用点是否在锁内」）**本轮不做**，因为它的漏判形态
// 是已知的且成体系的：闭包作用域、多出口解锁、helper 形态（跨函数可达性）、
// 接口接缝的宿主自实现、非 mutex 互斥（channel 信号量 / atomic copy-on-write）。
// 把它当阻断门禁等于制造假红；当成只读扫描器又容易被读成「跑过了就没事」。
// 故本文件**不做**它，也不在报告里暗示它已被覆盖。
//
// # 判据为什么必须走 go/ast
//
// 「这个声明是不是一个接缝声明」「它的文档注释里有没有那三个 token」
// 都是结构性问题。逐行正则判不出：函数型字段可以写成
// `Clock func() time.Time`、`Clock    func() time.Time`，文档注释可以
// 写成 `// @seam: ...`、`//@seam: ...`、`//  @seam:no-reentry ...`。
//
// # 清单完备性为什么是双向的
//
// 只判「扫描到但没登记」⇒ 新增接缝没人登记，接缝清单静默过期。
// 只判「登记了但扫描不到」⇒ 接缝被改名/删除后登记不清理，清单变成死条文。
// 两条都判，清单才是**活的**。

package archcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
)

// seamRuleID 接缝契约门禁这条规则自己的 id。
const seamRuleID = "harness-seam-contract-declared"

// seamRequiredTokens 接缝契约标记必须写齐的三个 token（顺序固定，允许多余空白）。
var seamRequiredTokens = []string{"no-reentry", "no-blocking", "panic-isolated"}

// SeamKind 是接缝的种类。三类如下（定义与 policy 的 seams 段一致）：
//
//   - func-field：导出结构体的**导出函数型字段**（宿主填回调、内核调）。
//   - func-param：**导出函数或方法**（含导出构造函数）的函数型入参。
//   - iface：**导出接口类型**（宿主实现、内核调；一条接缝覆盖其全部方法）。
type SeamKind string

const (
	// SeamFuncField 导出结构体的函数型字段。
	SeamFuncField SeamKind = "func-field"
	// SeamFuncParam 导出函数/方法的函数型入参。
	SeamFuncParam SeamKind = "func-param"
	// SeamIface 导出接口类型。
	SeamIface SeamKind = "iface"
)

// Seam 是一个接缝声明点。
type Seam struct {
	// ID 是接缝的稳定标识：包路径 + 声明名（见各 Kind 的 id 形状）。
	ID string
	// Kind 是接缝的种类。
	Kind SeamKind
	// Package 是接缝所在包的 import 路径。
	Package string
	// Type 是承载声明的类型名（结构体 / 接口 / 方法接收者）。
	Type string
	// Name 是声明名（字段名 / 函数名 / 接口名）。
	Name string
	// File 是相对仓库根的路径。
	File string
	// Line 是声明所在行。
	Line int
	// Doc 是声明处的文档注释（标记就写在这里）。
	Doc string
}

// ScanSeams 扫描全仓生产代码，产出接缝清单（按 ID 排序）。
//
// 扫描面 = 全仓 *.go（排除 _test.go），**包含 examples/**——参考实现是宿主
// 照抄的模板，模板里的接缝同样要写契约。
//
// 解析失败一律响亮失败：门禁给不出结论却显示成通过，是这条工具最贵的失效形态。
func ScanSeams(root string) ([]Seam, error) {
	module, err := moduleImportPath(root)
	if err != nil {
		return nil, err
	}
	files, err := iterFiles(root, []string{"*.go"}, true)
	if err != nil {
		return nil, err
	}
	var out []Seam
	for _, f := range files {
		seams, err := scanSeamFile(root, module, f)
		if err != nil {
			return nil, err
		}
		out = append(out, seams...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// packageImportPath 由包所在目录推出它的 import 路径。
func packageImportPath(module, dir string) string {
	dir = filepath.ToSlash(dir)
	if dir == "." || dir == "" {
		return module
	}
	return module + "/" + dir
}

// scanSeamFile 扫一个文件里的接缝声明点。
func scanSeamFile(root, module, path string) ([]Seam, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("接缝扫描解析 %s 失败（门禁给不出结论，不得静默跳过）: %w", path, err)
	}
	rel, _ := filepath.Rel(root, path)
	pkg := packageImportPath(module, filepath.Dir(rel))
	file := filepath.ToSlash(rel)
	var out []Seam
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				switch t := ts.Type.(type) {
				case *ast.StructType:
					out = append(out, structSeams(fset, pkg, file, ts, d, t)...)
				case *ast.InterfaceType:
					doc := docText(ts.Doc)
					if doc == "" {
						doc = docText(d.Doc)
					}
					out = append(out, Seam{
						ID:      pkg + "." + ts.Name.Name,
						Kind:    SeamIface,
						Package: pkg,
						Type:    ts.Name.Name,
						Name:    ts.Name.Name,
						File:    file,
						Line:    fset.Position(ts.Pos()).Line,
						Doc:     doc,
					})
				}
			}
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if !hasFuncParam(d.Type.Params) {
				continue
			}
			recv := receiverTypeName(d)
			id := pkg + "." + d.Name.Name
			if recv != "" {
				id = pkg + "." + recv + "." + d.Name.Name
			}
			out = append(out, Seam{
				ID:      id,
				Kind:    SeamFuncParam,
				Package: pkg,
				Type:    recv,
				Name:    d.Name.Name,
				File:    file,
				Line:    fset.Position(d.Pos()).Line,
				Doc:     docText(d.Doc),
			})
		}
	}
	return out, nil
}

// structSeams 收集一个导出结构体里的函数型字段接缝。
//
// 标记的位置：字段自己的文档注释优先；字段没有文档注释时，退到**结构体类型**
// 的文档注释（一整个结构体的回调共享同一条契约，不必逐字段重写一遍）。
func structSeams(fset *token.FileSet, pkg, file string, ts *ast.TypeSpec, gd *ast.GenDecl, st *ast.StructType) []Seam {
	typeDoc := docText(ts.Doc)
	if typeDoc == "" {
		typeDoc = docText(gd.Doc)
	}
	var out []Seam
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // 嵌入字段：不是接缝声明点
		}
		if _, ok := field.Type.(*ast.FuncType); !ok {
			continue
		}
		doc := docText(field.Doc)
		if doc == "" {
			doc = typeDoc
		}
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			out = append(out, Seam{
				ID:      pkg + "." + ts.Name.Name + "." + name.Name,
				Kind:    SeamFuncField,
				Package: pkg,
				Type:    ts.Name.Name,
				Name:    name.Name,
				File:    file,
				Line:    fset.Position(field.Pos()).Line,
				Doc:     doc,
			})
		}
	}
	return out
}

// hasFuncParam 报告参数表里有没有**函数型**入参。
func hasFuncParam(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	for _, p := range params.List {
		if _, ok := p.Type.(*ast.FuncType); ok {
			return true
		}
	}
	return false
}

// receiverTypeName 取方法接收者的类型名（泛型接收者取基类型名）。
func receiverTypeName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	expr := d.Recv.List[0].Type
	switch t := expr.(type) {
	case *ast.StarExpr:
		expr = t.X
	case *ast.IndexExpr:
		expr = t.X
	case *ast.IndexListExpr:
		expr = t.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// docText 取文档注释的正文（注释标记被剥掉，行间以换行分隔）。
func docText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return cg.Text()
}

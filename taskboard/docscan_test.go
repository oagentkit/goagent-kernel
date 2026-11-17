package taskboard

// docscan_test.go —— 按**符号**取文档注释（go/ast）。
//
// 为什么走 AST 而不是正则扫源码文本：注释挂在哪个声明上、属于哪个符号，
// 是**结构性**问题。逐行正则分不出「StateFailed 的注释」与「Terminal()
// 的注释」，也没法把 Board 接口的 Claim 方法注释单独取出来——而这几条
// 契约断言要的正是「按符号取注释」：
//
//   - issue #360：Board.Claim 的契约文本必须列出 ErrUnknownState；
//   - issue #375：状态常量的注释宣称什么，Terminal() 就得是什么；
//   - issue #379：Task 的类型注释不得宣称「可比较」。

import (
	"go/ast"
	"go/token"
	"strconv"
	"testing"

	"github.com/yi-nology/goagent-kernel/internal/gosrc"
)

// pkgDocs 按符号收集本包（非测试文件）的文档注释。
type pkgDocs struct {
	// consts 常量名 → 注释文本。
	consts map[string]string
	// values 常量名 → 字面值（仅字符串字面量）。
	values map[string]string
	// types 类型名 → 注释文本。
	types map[string]string
	// methods "接口名.方法名" → 注释文本。
	methods map[string]string
}

// constNameByValue 按取值反查常量名（状态常量按取值判文档）。
func (d pkgDocs) constNameByValue(value string) (string, bool) {
	for name, v := range d.values {
		if v == value {
			return name, true
		}
	}
	return "", false
}

// loadPkgDocs 解析本包源码，取出文档注释。
func loadPkgDocs(t *testing.T) pkgDocs {
	t.Helper()
	fset := token.NewFileSet()
	files, err := gosrc.ParsePackage(fset, ".", true)
	if err != nil {
		t.Fatalf("解析本包源码失败: %v", err)
	}
	out := pkgDocs{
		consts:  map[string]string{},
		values:  map[string]string{},
		types:   map[string]string{},
		methods: map[string]string{},
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gd.Tok {
			case token.CONST:
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						out.consts[name.Name] = docText(vs.Doc, gd.Doc)
						if i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								if v, err := strconv.Unquote(lit.Value); err == nil {
									out.values[name.Name] = v
								}
							}
						}
					}
				}
			case token.TYPE:
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					out.types[ts.Name.Name] = docText(ts.Doc, gd.Doc)
					iface, ok := ts.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, m := range iface.Methods.List {
						if len(m.Names) == 0 {
							continue
						}
						for _, name := range m.Names {
							out.methods[ts.Name.Name+"."+name.Name] = docText(m.Doc, nil)
						}
					}
				}
			}
		}
	}
	return out
}

// docText 取注释文本（剥掉注释符号）。
func docText(doc *ast.CommentGroup, fallback *ast.CommentGroup) string {
	if doc == nil {
		doc = fallback
	}
	if doc == nil {
		return ""
	}
	return doc.Text()
}

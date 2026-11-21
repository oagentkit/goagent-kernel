// facade_derive.go —— 门面「派生集扫描」：从 go/ast 派生门面上某类型的全部导出方法。
//
// 为什么单独成文件：派生是**代码面**的扫描（目录遍历 + go/ast + 类型形状），
// 与「声明面」的对位比较是两种关注点 ⇒ 混在一个文件里时，改派生口径会顺手改到
// 对位判据，而结构阈值只剩几行余量 ⇒ 任何改动都要靠折行腾位置。
//
// ⚠ 本文件由 cmd/storagecheck/facade.go **原样搬移**而来：判定逻辑与报错文案逐字未变。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/oekit/pkg/errors"
)

// facadeMethods 派生「门面包内某类型上的全部导出方法」，并报出该类型**派生集不完整**的原因。
//
// 口径：指定**包目录**下（**只取生产代码**，跳过 _test.go）指定**类型**上的全部**导出方法**。
// 用 go/ast 判（与 symbols.go 同一口径）：逐行正则判不出「方法挂在哪个接收者上」，而门面
// 要的恰恰是「这一扇门上有什么」。接收者写法（*T / T）由 receiverName 归一。
//
// ⚠ **派生集不完整**有三种形态，都报出来判红（fail-closed）：① **类型声明不存在**——
// 门包里根本没这个类型 ⇒ 无从派生；② **嵌入字段**——其方法被**提升**到门面类型；③
// **类型不是 struct 字面量**——**解析不出 struct 字段** ⇒ 嵌入字段看不见。
func facadeMethods(root, pkg, typ string) (map[string]bool, []string, error) {
	dir := filepath.Join(root, pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, errors.Wrap(err, "读门面包失败", "package", pkg)
	}
	out := map[string]bool{}
	var incomplete []string
	typeFound := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, nil, errors.Wrap(err, "解析门面包失败", "file", name)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 || receiverName(d.Recv.List[0].Type) != typ || !ast.IsExported(d.Name.Name) {
					continue
				}
				out[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if ok && ts.Name.Name == typ {
						typeFound = true
						st, ok := ts.Type.(*ast.StructType)
						if !ok {
							// 类型别名 / 定义类型 ⇒ 解析不出 struct 字段 ⇒ 嵌入字段看不见
							// ⇒ 派生集不完整 ⇒ 不拿它去比（fail-closed）。
							shape := "定义类型，底层类型取自 " + types.ExprString(ts.Type)
							if ts.Assign.IsValid() {
								shape = "类型别名，指向 " + types.ExprString(ts.Type)
							}
							incomplete = append(incomplete, "门面类型 "+typ+" 不是 struct 字面量（"+shape+"）⇒ 解析不出 struct 字段 ⇒ 请写成 struct 字面量（或扩展本判据以解析别名/派生类型的结构）")
							continue
						}
						for _, fld := range st.Fields.List {
							if len(fld.Names) == 0 { // 匿名 ⇒ 嵌入 ⇒ 其方法被提升
								incomplete = append(incomplete, "含嵌入字段（"+types.ExprString(fld.Type)+"）⇒ 其导出方法集含提升方法 ⇒ 请改为显式委托方法（或扩展本判据以纳入提升方法）")
							}
						}
					}
				}
			}
		}
	}
	// 整个门包里都没有这个类型的声明 ⇒ 无从派生 ⇒ 派生集不完整 ⇒ 判红。
	// 为什么**追加**一条：真病因是「类型名根本不存在」，只报「登记与代码不符」会把排查方向带偏。
	if !typeFound {
		incomplete = append(incomplete, fmt.Sprintf("门面包 %s 里找不到类型 %s 的声明 ⇒ 派生集不完整", pkg, typ))
	}
	return out, incomplete, nil
}

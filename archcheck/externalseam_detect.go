// externalseam_detect.go —— 外部依赖触达的**检测**（issue #482）。
//
// 检测面：生产代码里碰了外部世界（clock / env / fs / exec / net）的函数。
//
// 两种形态都要认：
//
//   - **调用形态**：`time.Now()`、`os.ReadFile(...)`；
//   - **函数值形态**：`time.Now` 不被调用、而是当值传下去。
//
// 后者不是边角料：它正是「接缝的缺省实现」的样子（`NewDesk(..., time.Now)`
// ⇒ 宿主可换成自己的时钟）。只认调用形态 ⇒ 这批函数在清单里**整个消失**，
// 而它们恰恰是「已有接缝」的那一批 ⇒ 清单会漏掉最有价值的那半边。
package archcheck

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"
	"strings"
)

// ── 检测 ──────────────────────────────────────────────────────────────

// extTouch 一处外部依赖触达。
type extTouch struct {
	pkg   string
	fn    string
	class string
	api   string
	rel   string
	line  int
}

// extTouches 找出全部生产代码里的外部依赖触达（按函数归并）。
//
// 判定面只含**生产代码**：测试夹具也问外部世界，但结论由用例自己给 ⇒ 不是
// 「判定面问外部世界」的那种。
func extTouches(root string, r Rule) ([]extTouch, error) {
	files, err := gjScanFiles(root, r)
	if err != nil {
		return nil, err
	}
	var out []extTouch
	for _, f := range files {
		if strings.HasSuffix(f.rel, "_test.go") {
			continue
		}
		out = append(out, extFileTouches(f)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pkg != out[j].pkg {
			return out[i].pkg < out[j].pkg
		}
		if out[i].fn != out[j].fn {
			return out[i].fn < out[j].fn
		}
		if out[i].class != out[j].class {
			return out[i].class < out[j].class
		}
		return out[i].api < out[j].api
	})
	return out, nil
}

// extFileTouches 找出一个文件里的全部外部依赖触达。
func extFileTouches(f gjFile) []extTouch {
	locals := map[string]string{} // 本地名 → import 路径
	for _, imp := range f.file.Imports {
		path, err := strconvUnquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			name = path
			if i := strings.LastIndex(path, "/"); i >= 0 {
				name = path[i+1:]
			}
		}
		if name == "_" || name == "." {
			continue
		}
		locals[name] = path
	}
	pkg := filepath.Dir(f.rel)
	parents := tsParentMap(f.file)
	var out []extTouch
	for _, dec := range f.file.Decls {
		fd, ok := dec.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			name = extRecvType(fd.Recv.List[0].Type) + "." + name
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				x, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, ok := locals[x.Name]
				if !ok {
					return true
				}
				for _, api := range extAPIs {
					if api.path != path || api.name != sel.Sel.Name {
						continue
					}
					out = append(out, extTouch{
						pkg: pkg, fn: name, class: api.class,
						api: path + "." + api.name, rel: f.rel,
						line: f.fset.Position(v.Pos()).Line,
					})
				}
			case *ast.SelectorExpr:
				// **函数值形态**：`time.Now` 不被调用、而是当值传下去。
				//
				// 这一形态不是边角料：它正是「接缝的缺省实现」的样子
				// （`NewDesk(..., time.Now)` ⇒ 宿主可换成自己的时钟）。
				// 只认调用形态 ⇒ 这批函数在清单里**整个消失**，而它们恰恰是
				// 「已有接缝」的那一批 ⇒ 清单会漏掉最有价值的那半边。
				if call, ok := parents[v].(*ast.CallExpr); ok && call.Fun == v {
					return true // 调用形态已在上面记过
				}
				x, ok := v.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, ok := locals[x.Name]
				if !ok {
					return true
				}
				for _, api := range extAPIs {
					if api.path != path || api.name != v.Sel.Name {
						continue
					}
					out = append(out, extTouch{
						pkg: pkg, fn: name, class: api.class,
						api: path + "." + api.name, rel: f.rel,
						line: f.fset.Position(v.Pos()).Line,
					})
				}
			}
			return true
		})
	}
	return out
}

// extRecvType 取方法接收者的类型名（*Foo ⇒ Foo；Foo ⇒ Foo）。
func extRecvType(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return extRecvType(v.X)
	case *ast.Ident:
		return v.Name
	case *ast.IndexExpr: // 泛型接收者
		return extRecvType(v.X)
	}
	return ""
}

// strconvUnquote 解 import 路径的字面量。
func strconvUnquote(s string) (string, error) {
	if len(s) < 2 {
		return "", fmt.Errorf("import 路径字面量不合法: %q", s)
	}
	return strings.Trim(s, `"`), nil
}

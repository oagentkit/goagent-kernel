// constresolve.go —— 词表派生时的常量解析（字面量 / 同包常量 / pkg.Const
// 单源引用），issue #384 落地、#392 扩展。
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// constResolver 把常量表达式还原为字符串值（issue #392 的单源化要求）。
//
// #384 落地时只支持字面量与同包已解析常量；#392 把 watchdog 的 reason 改成
// 引用 eventbus / trajectory 的导出常量（同一事实只有一份）后，门禁必须能
// 顺着**选择器**与 `string(x)` 转换走到被引用包的常量——否则单源化会让这条
// 门禁从「判漂移」退化成「判不了」。
//
// 只解析本仓模块内的 import：跨仓常量不在本仓词表判据的范围内，遇到时
// 响亮失败（由调用方报「无法派生」）。
type constResolver struct {
	root     string
	modPath  string
	cache    map[string]string // "<dir>|<name>" → 值
	visiting map[string]bool   // 递归环保护
}

// newConstResolver 读 go.mod 得到 module 前缀（没有 go.mod 时只支持字面量/
// 同包常量——合成夹具不需要跨包解析）。
func newConstResolver(root string) *constResolver {
	r := &constResolver{root: root, cache: map[string]string{}, visiting: map[string]bool{}}
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				r.modPath = strings.TrimSpace(strings.TrimPrefix(line, "module "))
				break
			}
		}
	}
	return r
}

// constString 把常量表达式还原为字符串值。
//
// 支持四类形态：字符串字面量、同文件已解析常量名、string(x) 转换、以及
// `pkg.Const` 选择器（递归到被引用包的字面量）。
func (r *constResolver) constString(expr ast.Expr, dir string, imports map[string]string, parsed map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	case *ast.Ident:
		v, ok := parsed[e.Name]
		return v, ok
	case *ast.CallExpr:
		// 只接受单参数的类型转换（本项目用到的是 string(...)）。
		if len(e.Args) != 1 {
			return "", false
		}
		return r.constString(e.Args[0], dir, imports, parsed)
	case *ast.SelectorExpr:
		pkgIdent, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		imp, ok := imports[pkgIdent.Name]
		if !ok {
			return "", false
		}
		targetDir, ok := r.dirForImport(imp)
		if !ok {
			return "", false
		}
		return r.lookupConst(targetDir, e.Sel.Name)
	default:
		return "", false
	}
}

// dirForImport 把本仓 import 路径映射到目录；跨仓 import 返回 false。
func (r *constResolver) dirForImport(imp string) (string, bool) {
	if r.modPath == "" {
		return "", false
	}
	if imp == r.modPath {
		return r.root, true
	}
	if !strings.HasPrefix(imp, r.modPath+"/") {
		return "", false
	}
	rel := strings.TrimPrefix(imp, r.modPath+"/")
	return filepath.Join(r.root, filepath.FromSlash(rel)), true
}

// lookupConst 在被引用包里按名取常量（只认字面量与同包已解析常量）。
func (r *constResolver) lookupConst(dir, name string) (string, bool) {
	key := dir + "|" + name
	if v, ok := r.cache[key]; ok {
		return v, true
	}
	if r.visiting[key] {
		return "", false // 常量环：没有可派生的值
	}
	r.visiting[key] = true
	defer delete(r.visiting, key)

	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return "", false
	}
	parsed := map[string]string{}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, file, nil, 0)
		if perr != nil {
			continue
		}
		imports := fileImports(f)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					expr := constExpr(vs, i)
					if expr == nil {
						continue
					}
					if v, ok := r.constString(expr, dir, imports, parsed); ok {
						parsed[id.Name] = v
					}
				}
			}
		}
	}
	v, ok := parsed[name]
	if ok {
		r.cache[key] = v
	}
	return v, ok
}

// fileImports 文件级 alias → import 路径（缺省 alias 用路径末段）。
func fileImports(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = path
	}
	return out
}

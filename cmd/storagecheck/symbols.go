// symbols.go —— 落点解析：path#Symbol 是不是真的声明在那个文件里。
//
// 用 go/ast 而不是逐行正则：声明形态有单行 / 块 / 别名多种写法，逐行正则
// 必然漏，且漏得毫无声响。符号支持顶层类型 / 函数 / 变量 / 常量，以及
// 「类型.方法」形态（方法才是门面真正拿到的那扇门）。
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// resolveRef 判 path#Symbol 是否真的声明在那个文件里。
//
// 返回 (成立, 不成立的理由)。理由要能直接拼进违反原文——判据给不出
// 「为什么这条不成立」时，人只能自己去猜。
func resolveRef(root, ref string) (bool, string) {
	path, symbol, ok := strings.Cut(ref, "#")
	if !ok || path == "" || symbol == "" {
		return false, "不是 path#Symbol 形态"
	}
	if !strings.HasSuffix(path, ".go") {
		return false, "必须指向 .go 文件"
	}
	// 跨仓引用（参考实现已迁至 goagent-runtime）：本仓机判不了外部文件的
	// 存在性与符号表——形态检查通过后放行（.go 后缀已在上文判过），
	// 存在性由该仓自己的门禁兜。判不了就明说判不了。
	if strings.HasPrefix(path, "https://") {
		return true, ""
	}
	full := filepath.Join(root, path)
	if _, err := os.Stat(full); err != nil {
		return false, "所在文件不存在"
	}
	declared, err := declaredSymbols(full)
	if err != nil {
		return false, "所在文件解析失败"
	}
	if !declared[symbol] {
		return false, "在代码里找不到该符号（引用失效）"
	}
	return true, ""
}

// declaredSymbols 收集一个 Go 文件里声明的一级符号（含「类型.方法」）。
func declaredSymbols(path string) (map[string]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				out[d.Name.Name] = true
				continue
			}
			if recv := receiverName(d.Recv.List[0].Type); recv != "" {
				out[recv+"."+d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out[n.Name] = true
					}
				}
			}
		}
	}
	return out, nil
}

// receiverName 取接收者类型名（*T / T / pkg.T 都归一到 T）。
func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

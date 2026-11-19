package archcheck

// issue #333 的门禁：同一句文案的 errors.New 不得在仓内出现多次。
//
// 同名同文案但不同 error 值是「声明写得很细 ≠ 覆盖到位」的典型形态：两个包
// 各写一份 ErrSessionDeadline，字面量相同、errors.Is 却互不认领——同一 ctx
// 上 deadline.Route 判 timed_out、finalize.Route 判 shutdown（不落终态）。
//
// 判据走 go/ast 而不是正则：识别 import 路径以 /errors 结尾的包的 New(...)
// 调用，按字符串字面量聚合并要求唯一。生产代码才算——测试里的替身错误
// 允许重名（它们不构成跨包的判定链）。按构建标签互斥的同一实现（如
// unix/windows 两份 lock 文件）在 allow 里显式登记。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNoDuplicateErrorSentinelLiterals(t *testing.T) {
	const root = ".."

	// 同一份实现按 GOOS 互斥编译：字面量相同是结构使然，不是第二份哨兵。
	// 当前无豁免项（2026-11-19：examples/sqlitestore 删除后，仓内已无 GOOS
	// 互斥的同名哨兵对——sqliteadapter 没有平台专属文件）；机制留着，后续
	// 再出现同类形态时在此登记。
	allow := map[string]bool{}

	type occurrence struct {
		file string
		line int
	}
	seen := map[string][]occurrence{}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "site" || name == "bin" || strings.HasSuffix(name, "-work") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if allow[rel] {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			t.Errorf("解析 %s 失败: %v", rel, perr)
			return nil
		}
		// import 本地名 → 路径（errors 包可能被别名导入）。
		imports := make(map[string]string, len(file.Imports))
		for _, imp := range file.Imports {
			ipath, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				continue
			}
			local := path.Base(ipath)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			imports[local] = ipath
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "New" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			ipath := imports[ident.Name]
			if ipath != "errors" && !strings.HasSuffix(ipath, "/errors") {
				return true
			}
			if len(call.Args) != 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			msg, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			seen[msg] = append(seen[msg], occurrence{file: rel, line: fset.Position(call.Pos()).Line})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("遍历仓库失败: %v", err)
	}

	for msg, locs := range seen {
		if len(locs) < 2 {
			continue
		}
		var b strings.Builder
		for _, loc := range locs {
			b.WriteString("\n  " + loc.file + ":" + strconv.Itoa(loc.line))
		}
		t.Errorf("errors.New(%q) 在仓内出现 %d 次——同一份文案必须是同一个 error 值"+
			"（把哨兵提到共享包做别名，或改名以表达不同语义）：%s", msg, len(locs), b.String())
	}
}

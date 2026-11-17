// Package gosrc 源码扫描的共享件：把「读目录 → 解析 .go 文件」这一步收成一处。
//
// # 为什么要有这个包
//
// 仓内多处判据要读**本包**的源码（扫常量定义、扫文档注释、扫别名来源）。
// 这些判据各写一遍目录遍历，就会出现三件事同时变坏：
//
//   - 用 go/parser 的 ParseDir：它自 Go 1.25 起已废弃（SA1019），
//     且**不考虑构建标签**，会把带 tag 的文件当成同包文件收进来；
//   - 「跳过测试文件」写错：按**包名**判断文件是不是测试文件
//     （ast.File.Name 是包标识符，不是文件名）⇒ 判断恒为假，
//     测试文件里的同名常量被当成生产常量收进词表；
//   - 同一份遍历逻辑改一次要动多处。
//
// 故收成一处：**一个**入口、**一个**跳过测试文件的判据、**一份**注释开关。
package gosrc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ParsePackage 解析 dir 下的 .go 文件，返回「文件名 → 解析结果」的表。
//
// # 口径
//
//   - 只收**本层**的 .go 文件（不进子目录）——调用方扫的是「一个包」；
//   - **跳过 _test.go**：词表类判据要的是生产代码里声明的符号，
//     测试文件里的同名常量（例如用例自己造的夹具常量）不是词表成员；
//   - withComments=true 时保留文档注释（要读注释的判据用），
//     否则不保留（只读符号结构的判据用，省一份内存）；
//   - 返回表以**文件名**为键——要判断「这是不是测试文件」时用键，
//     **不要**用 ast.File.Name 判断文件名——那是包标识符。
//
// 解析失败即返回错误：判据拿不到源码时必须红，不能静默放过。
func ParsePackage(fset *token.FileSet, dir string, withComments bool) (map[string]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	mode := parser.SkipObjectResolution
	if withComments {
		mode |= parser.ParseComments
	}
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, mode)
		if perr != nil {
			return nil, perr
		}
		files[name] = file
	}
	return files, nil
}

package main

// terminalvocab_test.go —— examples/ 全目录的「终态词表不得本地手抄」判据
//（issue #358）。
//
// # 为什么判据住在 examples/ 里
//
// 被判的是 examples/ **全目录**的生产代码，而判据本体必须能被 examples/ 的
// 任何一份实现触发。放在被判目录之外就得跨包读别人的源码；放在里面，宿主
// 照抄 examples/ 时连判据一起抄走——判据与被抄的形态同处一份目录，正是它要
// 防的那类示范的放大器（"示范带坏头"）。
//
// # 判据怎么走（go/ast，不逐行正则看源码文本）
//
// 结构性判定（一个函数体是不是**自己**判终态）必须走语法树：逐行正则判不出
// 「这个函数体里引用了哪些终态取值」，而且 import/字面量有多种书写形态。
//
// 判定的形状：**返回 bool 的函数**且其函数体引用了 **≥2 个**会话终态取值
// ⇒ 它在自己判「什么是终态」⇒ 本地手抄了一份词表。
//
// 取值来源是 contract 的**导出常量**（不是门禁里抄一份字面量）：门禁自己
// 手抄的词表也会漂——contract 新增一个终态时，门禁那份不会跟着变，且照样
// 报「一切正常」。
//
// # 反向验证
//
// 把 examples/reference-runtime/runtime.go 里的 isTerminalStatus 改回本地
// 定义（switch 一份 contract.Status* 常量）⇒ 本用例必须转红。

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

	"github.com/yi-nology/goagent-kernel/contract"
)

// terminalStatusByConst 会话终态取值 ↔ contract 导出常量名（真相源在 contract）。
var terminalStatusByConst = map[string]string{
	"StatusSucceeded": contract.StatusSucceeded,
	"StatusFailed":    contract.StatusFailed,
	"StatusCancelled": contract.StatusCancelled,
	"StatusTimedOut":  contract.StatusTimedOut,
}

// terminalStatusValues 终态取值的集合（反查用）。
var terminalStatusValues = func() map[string]bool {
	out := make(map[string]bool, len(terminalStatusByConst))
	for _, v := range terminalStatusByConst {
		out[v] = true
	}
	return out
}()

// TestExamplesDoNotHandCopyTerminalVocabulary examples/ 全目录不得存在本地
// 定义的终态判定函数（issue #358）。
//
// 判据的失败形态必须是**变红**：参考实现一旦把手抄的终态词表改回来
// （哪怕改了函数名），本用例立刻报出文件与函数名。
func TestExamplesDoNotHandCopyTerminalVocabulary(t *testing.T) {
	root := examplesRoot(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		found, perr := handCopiedTerminalFuncs(path)
		if perr != nil {
			return perr
		}
		offenders = append(offenders, found...)
		return nil
	})
	if err != nil {
		t.Fatalf("扫 examples/ 失败: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("examples/ 里存在本地手抄的终态词表（%d 处）：\n  %s\n"+
			"终态词表的单一真相是 contract.IsTerminalStatus：参考实现是宿主照抄的形态示范，"+
			"示范手抄一份词表等于把第二份真相派发给每一个照抄的宿主——"+
			"内核新增一个终态时它不会跟着变，且没有任何信号。",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// examplesRoot 定位 examples/ 目录（从当前工作目录向上找同名目录）。
//
// 不写死相对路径：go test 把工作目录设成**包目录**，但用例也可能被别的
// 工具以不同工作目录调用——判据的定位面必须是确定的。
func examplesRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	for {
		if filepath.Base(dir) == "examples" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("找不到 examples/ 目录（判据前提不成立）")
		}
		dir = parent
	}
}

// handCopiedTerminalFuncs 列出一个文件里「自己判终态」的函数。
func handCopiedTerminalFuncs(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	rel, rerr := filepath.Rel(filepath.Dir(path), path)
	if rerr != nil {
		rel = path
	}
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !returnsBool(fn) {
			continue
		}
		values := terminalValuesIn(fn.Body)
		if len(values) < 2 {
			continue // 只碰一个终态取值：不是「自己判词表」
		}
		out = append(out, rel+": "+fn.Name.Name+"（本地引用终态取值 "+
			strings.Join(values, " / ")+"）")
	}
	return out, nil
}

// returnsBool 报告函数的返回值是不是单个 bool。
func returnsBool(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
	return ok && ident.Name == "bool"
}

// terminalValuesIn 收集函数体引用到的会话终态取值（去重、排序）。
//
// 两种引用形态都算：常量选择器（contract.StatusSucceeded）与等值字面量
// （"succeeded"）——手抄词表的人两种都写得出来。
func terminalValuesIn(body *ast.BlockStmt) []string {
	seen := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok && x.Name == "contract" {
				if v, found := terminalStatusByConst[node.Sel.Name]; found {
					seen[v] = true
				}
			}
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			v, uerr := strconv.Unquote(node.Value)
			if uerr == nil && terminalStatusValues[v] {
				seen[v] = true
			}
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

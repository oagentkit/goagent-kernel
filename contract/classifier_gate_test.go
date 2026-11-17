// classifier_gate_test.go —— 「什么算词表外」这条边界只有一份的结构判据
// （issue #363）。
//
// # 判据要扫全仓，不能只测一个包
//
// #363 的失效形态是**静默分叉**：checkpoint 与 controlplane 各有一份逐字同构的
// 三态分类器（statusClass / runStatusClass + classifyStatus /
// classifyRunStatus），两处注释都声明「不在本包另立一份」——立的是两份。
// 今天两份结论相同，所以不产生错误结论；可一旦某包改了边界、另一包没改，
// 同一个状态在两包得到**相反**结论（checkpoint 当可续跑 vs controlplane 当需
// 终止），而「两份必须同时改」这件事没有任何机制记录。
//
// 只测某一个包，下一个包照抄契约时仍然会漏。故这里用 go/ast 扫两个消费方的
// **全部生产代码**，断言：
//
//  1. 不再存在本地声明的三态分类器（自己的 statusClass 型）；
//  2. 不再把已知状态**列成清单**（case 分支 / 复合字面量里出现
//     contract.Status* 的枚举列表）；
//  3. 必须调用 contract.ClassifyStatus（唯一判定面）。
//
// # 为什么走 go/ast 而不是正则
//
// 「把已知状态列成一串」是**结构性**事实：它可能写在 case 里、复合字面量里、
// 或换个写法，逐行看文本必然漏——而漏得毫无声响。
//
// # 判据的前提：两处分类结果逐字一致
//
// 断言 3 成立时，两处调用的是**同一个函数**，故对同一状态必然得到同一结论；
// 该函数逐值的结论由 contract 包的 TestClassifyStatusPinned 钉住。两者合起来
// 即「全词表 + 词表外取值逐字一致」（issue #363 验收 1）。
//
// 反向验证：把任一消费方改回各自列举已知过程态（哪怕结论完全相同）→ 本用例
// 立即转红。
package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusClassifierConsumers 三态边界的消费方（#363 点名的两处）。
var statusClassifierConsumers = []string{"checkpoint", "controlplane"}

// TestStatusClassifiersDoNotEnumerateKnownStates 三态边界只有一份：两个
// 消费方都不得自建分类器，也不得各自列举已知状态（issue #363）。
func TestStatusClassifiersDoNotEnumerateKnownStates(t *testing.T) {
	for _, pkg := range statusClassifierConsumers {
		dir := filepath.Join("..", pkg)
		files, err := productionGoFiles(dir)
		if err != nil {
			t.Fatalf("扫描 %s 失败: %v", pkg, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s 没有扫到任何生产文件（扫描面已失效）", pkg)
		}
		for _, path := range files {
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("解析 %s 失败: %v", path, perr)
			}
			rel, _ := filepath.Rel("..", path)
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if strings.Contains(strings.ToLower(ts.Name.Name), "statusclass") {
						t.Errorf("%s: 本地声明了 %q——「什么算词表外」这条边界只有 contract.ClassifyStatus 一份（issue #363）", rel, ts.Name.Name)
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CaseClause:
					if refs := contractStatusRefs(node.List); len(refs) >= 2 {
						t.Errorf("%s: case 分支里列举了 %s——已知状态不得在本包另立一份清单（issue #363）", rel, strings.Join(refs, "、"))
					}
				case *ast.CompositeLit:
					if refs := contractStatusRefs(node.Elts); len(refs) >= 2 {
						t.Errorf("%s: 复合字面量里列举了 %s——已知状态不得在本包另立一份清单（issue #363）", rel, strings.Join(refs, "、"))
					}
				}
				return true
			})
		}
		if !callsContractClassifyStatus(dir) {
			t.Errorf("%s: 没有调用 contract.ClassifyStatus——三态边界必须走唯一判定面（issue #363）", pkg)
		}
	}
}

// contractStatusRefs 收集一组表达式里对 contract 状态常量的引用。
//
// 只认 SelectorExpr（contract.StatusX）——裸标识符形态不认：消费者包里一个
// 裸 Status 标识符是别的包的符号（甚至本地变量），把它算成违规会误报。
//
// 前提：contract 以 import 名 contract 引入。**别名导入不在本判据的覆盖面
// 内**——那种形态需要同步本判据（注释里写明这条前提，别让它无声失效）。
//
// # 为什么要「≥2 个」才算列举
//
// 「另立一份已知状态清单」指的是**一串**取值（如
// `case "", contract.StatusCreated, contract.StatusRunning, ...:`）。
// 而 `case contract.StatusTerminal:` 是**分类结果**分流——两处消费方都要
// 按分类三路处置，它不是清单。一个单值比较不该被判违规（否则判据自身
// 变成噪声，久了就没人看了）。
func contractStatusRefs(exprs []ast.Expr) []string {
	var out []string
	for _, e := range exprs {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "contract" {
			continue
		}
		if !strings.HasPrefix(sel.Sel.Name, "Status") {
			continue
		}
		out = append(out, "contract."+sel.Sel.Name)
	}
	return out
}

// productionGoFiles 列出一个包目录下的全部生产 .go 文件（跳过测试文件）。
func productionGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out, nil
}

// callsContractClassifyStatus 报告该包是否调用了 contract.ClassifyStatus。
func callsContractClassifyStatus(dir string) bool {
	files, err := productionGoFiles(dir)
	if err != nil {
		return false
	}
	for _, path := range files {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			continue
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "contract" && sel.Sel.Name == "ClassifyStatus" {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

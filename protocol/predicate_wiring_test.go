// predicate_wiring_test.go —— 枚举谓词与判据面「同源」的结构判据（issue #362）。
//
// # 为什么用 AST
//
// #320 导出了 All* 与 Is*Valid，但判据面继续直读私有 map：导出一个真相、
// 判另一套真相，两者必然漂移。文本 grep 判不出「这个下标访问是不是在判据面」，
// 也判不出「这个谓词到底有没有生产调用点」——故这里走 go/ast 判结构：
//
//  1. 判据面（report_validate.go）与归一化面（report_normalize.go）不得出现
//     stanceAll / decisionAll / actionWhitelist / scopeAll / confidenceAll /
//     severityAll 的下标访问，只能经 Is*Valid 谓词；
//  2. 每个导出的 Is*Valid 必须至少有一个**生产**调用点，且至少落在判据/归一化
//     面之一——「导出了但没人用」会重新变成第二套真相。
//
// 反向验证：把 report_validate.go 的任一谓词调用改回私有 map ⇒ 用例转红；
// 删掉某个谓词的唯一生产调用点 ⇒ 用例转红。
package protocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// 判据/归一化面必须经谓词访问的私有词表。
var privateEnumMaps = map[string]bool{
	"stanceAll":       true,
	"decisionAll":     true,
	"actionWhitelist": true,
	"scopeAll":        true,
	"confidenceAll":   true,
	"severityAll":     true,
}

// judgmentSurfaceFiles 判据面与归一化面（枚举值只允许经谓词触达）。
var judgmentSurfaceFiles = []string{"report_validate.go", "report_normalize.go"}

// predicateSurfaceFiles 判据/归一化/宽容解码三个面（谓词的生产落点）。
var predicateSurfaceFiles = map[string]bool{
	"report_validate.go":  true,
	"report_normalize.go": true,
	"report_types.go":     true,
}

// reportEnumPredicateFiles 报告枚举谓词的**定义面**（issue #362 的判据对象）。
//
// 只把这三份文件里声明的 Is*Valid 纳入「必须有生产调用」的集合：
//   - report_enum.go：stance / action / decision / scope；
//   - report.go：severity；
//   - report_types.go：confidence。
//
// decision.go 的判定信封（来源/置信种类/升级原因）是另一组枚举面：它的
// 校验器 ValidateVerdict 就地消费前两个谓词，词表归属与报告枚举不同源，
// 不并入本判据（否则「报告枚举同源」这件事会被另一套面稀释）。
var reportEnumPredicateFiles = map[string]bool{
	"report_enum.go":  true,
	"report.go":       true,
	"report_types.go": true,
}

// TestJudgmentSurfaceUsesPredicatesNotPrivateMaps 判据面与归一化面不得直读私有 map。
func TestJudgmentSurfaceUsesPredicatesNotPrivateMaps(t *testing.T) {
	for _, name := range judgmentSurfaceFiles {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			idx, ok := n.(*ast.IndexExpr)
			if !ok {
				return true
			}
			ident, ok := idx.X.(*ast.Ident)
			if ok && privateEnumMaps[ident.Name] {
				t.Errorf("%s:%d 直读私有 map %s——枚举判定必须经 Is*Valid 谓词"+
					"（导出的真相 = 判的真相，issue #362）",
					name, fset.Position(idx.Pos()).Line, ident.Name)
			}
			return true
		})
	}
}

// TestExportedValidityPredicatesAreConsumed 每个导出的 Is*Valid 必须被生产代码消费，
// 且至少落在判据/归一化/解码面之一。
func TestExportedValidityPredicatesAreConsumed(t *testing.T) {
	declared := map[string]bool{}
	calls := map[string]map[string]bool{} // 谓词 → 调用它的生产文件集合

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)

	for _, name := range files {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("解析 %s 失败: %v", name, perr)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if !reportEnumPredicateFiles[name] {
				continue
			}
			if strings.HasPrefix(fn.Name.Name, "Is") && strings.HasSuffix(fn.Name.Name, "Valid") && ast.IsExported(fn.Name.Name) {
				declared[fn.Name.Name] = true
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if calls[ident.Name] == nil {
				calls[ident.Name] = map[string]bool{}
			}
			calls[ident.Name][name] = true
			return true
		})
	}

	// 防假绿：谓词发现逻辑失效时（改名/搬文件），下面的循环会一条断言都不跑。
	if len(declared) < 6 {
		t.Fatalf("只发现 %d 个导出的 Is*Valid 谓词，判据可能失效: %v", len(declared), declared)
	}
	for pred := range declared {
		where := calls[pred]
		if len(where) == 0 {
			t.Errorf("%s 零生产消费——导出了但没人用，判据面仍在读另一套真相（issue #362）", pred)
			continue
		}
		onSurface := false
		for file := range where {
			if predicateSurfaceFiles[file] {
				onSurface = true
			}
		}
		if !onSurface {
			t.Errorf("%s 的生产调用点不在判据/归一化/解码面（实得 %v）——"+
				"枚举与判定仍未接上（issue #362）", pred, where)
		}
	}
}

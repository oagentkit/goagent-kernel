// report_vocab_source_test.go —— 判定词表的「单一真相」断言（issue #362）。
//
// # 这一组钉住什么
//
// 1. 判据面与归一化面**不直读私有词表**（只能经谓词调用）——否则就是「导出一个
//    真相、判另一套真相」，两者必然漂移；
// 2. 每个导出的 Is*Valid **至少有一个生产调用点**——否则「导出了但没人用」复发
//    （第三轮 #320 交付的四个谓词零生产消费就是这么来的）；
// 3. 私有 map 是**唯一真相**：谓词与枚举出口都从它派生——改表即改判定，不需要
//    同步第二份清单。
//
// # 为什么结构性判定走 go/ast
//
// 「这个文件里有没有直读私有 map」是结构性问题：下标访问有六种以上书写形态
// （赋值/取值/作为实参/取地址……），逐行正则必然漏。import 判定同理——本仓纪律
// 明确要求结构性判定走语法树。

package protocol

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// privateVocabularyTables 本包全部判定词表（私有 map）。
//
// 判据面与归一化面**一个都不许直读**：它们只能经导出的谓词访问（issue #362）。
func privateVocabularyTables() map[string]bool {
	return map[string]bool{
		"stanceAll":       true,
		"decisionAll":     true,
		"actionWhitelist": true,
		"scopeAll":        true,
		"confidenceAll":   true,
		"severityAll":     true,
	}
}

// TestJudgeAndNormalizeFacesDoNotIndexPrivateTables 判据面与归一化面不得直读
// 私有词表——私有 map 是唯一真相，判据面必须经谓词访问。
//
// 反向验证：把判据面改回直读私有 map（本组用例的 AST 判定会当场报出行号）。
func TestJudgeAndNormalizeFacesDoNotIndexPrivateTables(t *testing.T) {
	forbidden := privateVocabularyTables()
	for _, file := range []string{"report_validate.go", "report_normalize.go"} {
		for _, hit := range tableIndexHits(t, file, forbidden) {
			t.Errorf("%s:%s 判据面/归一化面不得直读私有词表（只能经 Is*Valid 谓词调用）",
				file, hit)
		}
	}
}

// TestExportedPredicatesHaveProductionCallSites 每个导出的 Is*Valid 至少有一个
// 生产调用点——「导出了但没人用」是 #320 交付不完整的直接证据。
//
// 反向验证：删掉某谓词的全部生产调用 ⇒ 本条转红。
//
// # 覆盖范围与一处既成事实
//
// 本组覆盖报告协议的六条词表谓词与判定面已接上的两条（ValidateVerdict 消费）。
//
// **IsEscalationReasonValid 至今零生产消费**（全仓皆无）——与 #362 同形的既成
// 缺口，但它不在本轮五条 issue 的范围内，且补它要给 Stable 包的判定事件载荷
// 定语义（越界改动）。故本组不把它写成门禁，改为在此显式记名，留给后续单独立单。
func TestExportedPredicatesHaveProductionCallSites(t *testing.T) {
	predicates := []string{
		"IsStanceValid",
		"IsActionValid",
		"IsDecisionValid",
		"IsScopeValid",
		"IsConfidenceValid",
		"IsSeverityValid",
		"IsDecisionSourceValid",
		"IsConfidenceKindValid",
	}
	calls := productionCallSites(t)
	for _, p := range predicates {
		if calls[p] == 0 {
			t.Errorf("导出的 %s 零生产消费：导出了却没人用（私有 map 会与判定面漂移）", p)
		}
	}
}

// TestPrivateTableIsTheSingleSourceOfTruth 私有 map 是唯一真相：谓词与枚举出口
// 都从它派生，往表里加一个取值，谓词与出口必须**立刻**认它。
//
// 这条把「谓词与词表同源」从注释变成判据：谓词若自持一份清单，往表里加值时
// 它不会变——本条转红。
//
// 反向验证：把谓词改回自持清单（不查私有 map）⇒ 本条转红。
func TestPrivateTableIsTheSingleSourceOfTruth(t *testing.T) {
	const probe = "__probe_value__"
	cases := []struct {
		name  string
		table map[string]bool
		valid func(string) bool
		all   func() []string
	}{
		{"stance", stanceAll, IsStanceValid, AllStances},
		{"action", actionWhitelist, IsActionValid, AllActions},
		{"confidence", confidenceAll, IsConfidenceValid, AllConfidences},
		{"decision", decisionAll, IsDecisionValid, AllDecisions},
		{"scope", scopeAll, IsScopeValid, AllScopes},
		{"severity", severityAll, IsSeverityValid, AllSeverities},
		{"decision.source", decisionSources, IsDecisionSourceValid, AllDecisionSources},
		{"confidence.kind", confidenceKinds, IsConfidenceKindValid, AllConfidenceKinds},
		{"escalation.reason", escalationReasons, IsEscalationReasonValid, AllEscalationReasons},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.table[probe] = true
			defer delete(c.table, probe)
			if !c.valid(probe) {
				t.Fatalf("私有词表新增取值后谓词未认它：谓词与词表不同源")
			}
			if !slices.Contains(c.all(), probe) {
				t.Fatalf("私有词表新增取值后枚举出口未认它：出口与词表不同源")
			}
		})
	}
}

// tableIndexHits 找出文件里对指定标识符的**下标访问**（结构性判定走 go/ast）。
func tableIndexHits(t *testing.T, file string, names map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, readSource(t, file), 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		idx, ok := n.(*ast.IndexExpr)
		if !ok {
			return true
		}
		id, ok := idx.X.(*ast.Ident)
		if ok && names[id.Name] {
			hits = append(hits, fmt.Sprintf("%d", fset.Position(idx.Pos()).Line))
		}
		return true
	})
	return hits
}

// productionCallSites 统计**生产代码**（不含 _test.go）对各导出谓词的调用点。
func productionCallSites(t *testing.T) map[string]int {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	calls := map[string]int{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, readSource(t, name), 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				calls[id.Name]++
			}
			return true
		})
	}
	return calls
}

// readSource 读取包内源文件（测试运行目录即包目录）。
func readSource(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return data
}

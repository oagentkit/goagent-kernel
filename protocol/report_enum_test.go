package protocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// vocabularyCase 一张判定词表的四件套：README/宿主看到的枚举出口、判定用的
// 私有词表、合法性谓词。
type vocabularyCase struct {
	name      string
	tableName string // 私有词表的变量名（供 AST 清单交叉核对）
	all       func() []string
	table     map[string]bool
	valid     func(string) bool
}

// vocabularyCases 九张判定词表的**清单**（issue #383）。
//
// 它同时是「新增词表必须配套 All*() 与断言」的登记面：下面的
// TestEveryVocabularyMapHasEnumerationCase 会扫描包内全部 map[string]bool
// 声明，凡不在此清单里的表都必须显式登记为「非判定词表」并写明理由，
// 否则用例转红。漏一张表的形态不是「少测一条」，而是宿主拿到一份
// 无人验证的「口径唯一来源」。
var vocabularyCases = []vocabularyCase{
	{"verdicts.stance", "stanceAll", AllStances, stanceAll, IsStanceValid},
	{"recommendation.action", "actionWhitelist", AllActions, actionWhitelist, IsActionValid},
	{"confidence", "confidenceAll", AllConfidences, confidenceAll, IsConfidenceValid},
	{"decision", "decisionAll", AllDecisions, decisionAll, IsDecisionValid},
	{"scope", "scopeAll", AllScopes, scopeAll, IsScopeValid},
	{"severity", "severityAll", AllSeverities, severityAll, IsSeverityValid},
	{"decision.source", "decisionSources", AllDecisionSources, decisionSources, IsDecisionSourceValid},
	{"confidence.kind", "confidenceKinds", AllConfidenceKinds, confidenceKinds, IsConfidenceKindValid},
	{"escalation.reason", "escalationReasons", AllEscalationReasons, escalationReasons, IsEscalationReasonValid},
}

// nonVocabularyMaps 包内的 map[string]bool 但**不是**判定词表的声明，值写理由。
//
// 为什么需要这份显式登记：判据要能区分「新增了一张判定词表却忘了配套
// All*()」与「新增了一张内部查找表」——前者是 issue #383 的失效形态，
// 后者不是。把「这不是词表」写成一条带理由的声明，下一次加 map 的人
// 必须做一次判断，而不是无声地绕过门禁。
var nonVocabularyMaps = map[string]string{
	"stringKeys": "修复通道的已知字符串键清单（遍历键名，不是枚举取值域）",
	"listKeys":   "修复通道的字符串项列表键清单（同上）",
	// 图表状态词的三张分组表**已**有对外枚举出口，登记在 groupedVocabularyCases
	// （issue #356）——它们是分组词表，形状套不上上面的扁平登记面。
}

// TestVocabularyEnumerationMatchesPredicates 每张判定词表的枚举出口与
// Is*Valid 判定面必须**双向**一致：枚举里的每个值都过谓词；谓词为真的
// 每个值都在枚举里。
//
// 单向断言（只测「枚举出的值合法」）会漏掉「词表里有但没导出」——那正是
// 宿主手抄清单漂移的入口。长度相等 + 逐值包含把两个方向都钉住。
func TestVocabularyEnumerationMatchesPredicates(t *testing.T) {
	for _, c := range vocabularyCases {
		t.Run(c.name, func(t *testing.T) {
			got := c.all()
			if len(got) != len(c.table) {
				t.Fatalf("枚举出口漏值：All*= %d 条，词表 %d 条（%v）", len(got), len(c.table), got)
			}
			if !slices.IsSorted(got) {
				t.Fatalf("枚举出口必须返回排序后的切片：%v", got)
			}
			for i, v := range got {
				if !c.table[v] {
					t.Fatalf("枚举出口出现词表外的值 %q（判定面不认识它）", v)
				}
				if !c.valid(v) {
					t.Fatalf("枚举出的值 %q 未通过对应 Is*Valid", v)
				}
				if i > 0 && got[i-1] == v {
					t.Fatalf("枚举出口有重复值 %q", v)
				}
			}
			for v := range c.table {
				if !slices.Contains(got, v) {
					t.Fatalf("词表里的值 %q 没有出现在枚举出口里", v)
				}
			}
			// 枚举返回的是副本：改写它不得影响下一次枚举 / 判定面。
			if len(got) > 0 {
				got[0] = "__mutated_by_host__"
				again := c.all()
				if slices.Contains(again, "__mutated_by_host__") {
					t.Fatal("枚举出口返回了共享底层数组：宿主改写污染了词表")
				}
				if !c.table[again[0]] {
					t.Fatal("枚举出口返回的切片不是副本")
				}
			}
		})
	}
}

// TestNewVocabularyPredicates 此前 4 张词表只有私有 map、没有合法性谓词；
// 补齐后每条都要能单独判「合法接受 / 非法拒绝」。
func TestNewVocabularyPredicates(t *testing.T) {
	cases := []struct {
		name  string
		valid func(string) bool
		good  string
	}{
		{"stance", IsStanceValid, VerdictAdopt},
		{"action", IsActionValid, ActionTune},
		{"decision", IsDecisionValid, DecisionApproved},
		{"scope", IsScopeValid, "out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.valid(c.good) {
				t.Fatalf("合法值 %q 被拒", c.good)
			}
			// "OUT" 是合法值 out 的写法变体（issue #371），不是非法值；
			// 词表外取值用真正不在词表里的形态。
			for _, bad := range []string{"", "__unknown__", "out_of_scope"} {
				if c.valid(bad) {
					t.Fatalf("非法值 %q 被接受", bad)
				}
			}
		})
	}
}

// TestSeverityRanksMatchVocabulary severityRank 的取值域必须与 severityAll
// 逐字一致，且每条序与 SeverityRankOf 相同。
func TestSeverityRanksMatchVocabulary(t *testing.T) {
	ranks := SeverityRanks()
	if len(ranks) != len(severityRank) || len(ranks) != len(severityAll) {
		t.Fatalf("SeverityRanks 条数 %d，severityRank %d，severityAll %d",
			len(ranks), len(severityRank), len(severityAll))
	}
	for _, e := range ranks {
		if !severityAll[e.Severity] {
			t.Fatalf("SeverityRanks 出现 severityAll 外的值 %q", e.Severity)
		}
		if got := SeverityRankOf(e.Severity); got != e.Rank {
			t.Fatalf("%q 的序漂移：SeverityRanks=%d, SeverityRankOf=%d", e.Severity, e.Rank, got)
		}
	}
	for v := range severityRank {
		if !slices.ContainsFunc(ranks, func(e SeverityRankEntry) bool { return e.Severity == v }) {
			t.Fatalf("severityRank 里的值 %q 没有出现在 SeverityRanks 里", v)
		}
	}
	// 副本：改写返回值不影响下一次。
	ranks[0].Rank = -1
	if SeverityRanks()[0].Rank == -1 {
		t.Fatal("SeverityRanks 返回了共享状态")
	}
}

// parsePackageAST 解析包内全部生产 .go（不含 _test.go）。
//
// 判据读的是**源码清单**而不是运行期反射：新增词表但没有配套 All*()
// 这件事在运行期根本看不见（没有符号可查），只能在声明面数。
func parsePackageAST(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("解析 %s 失败: %v", name, perr)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("没有解析到任何生产 .go 文件")
	}
	return files
}

// isMapStringBool 判断一个类型表达式是否为 map[string]bool。
func isMapStringBool(expr ast.Expr) bool {
	mt, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	k, kok := mt.Key.(*ast.Ident)
	v, vok := mt.Value.(*ast.Ident)
	return kok && vok && k.Name == "string" && v.Name == "bool"
}

// declaredMapStringBool 收集包内全部包级 map[string]bool 变量名。
func declaredMapStringBool(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					typ := vs.Type
					var expr ast.Expr
					switch {
					case len(vs.Values) == len(vs.Names):
						expr = vs.Values[i]
					case len(vs.Values) == 1:
						expr = vs.Values[0]
					}
					if typ == nil && expr != nil {
						if cl, isLit := expr.(*ast.CompositeLit); isLit {
							typ = cl.Type
						}
					}
					if isMapStringBool(typ) {
						out[name.Name] = true
					}
				}
			}
		}
	}
	return out
}

// declaredEnumerationFuncs 收集包内全部「无参、返回 []string」的 All*()。
func declaredEnumerationFuncs(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "All") {
				continue
			}
			ft := fd.Type
			if ft.Params != nil && len(ft.Params.List) > 0 {
				continue
			}
			if ft.Results == nil || len(ft.Results.List) != 1 {
				continue
			}
			arr, ok := ft.Results.List[0].Type.(*ast.ArrayType)
			if !ok {
				continue
			}
			elt, ok := arr.Elt.(*ast.Ident)
			if !ok || elt.Name != "string" {
				continue
			}
			out[fd.Name.Name] = true
		}
	}
	return out
}

// TestEveryVocabularyMapHasEnumerationCase 包内任何 map[string]bool 都必须
// 要么是一张登记在案的判定词表（有枚举出口 + 一致性断言），要么在
// nonVocabularyMaps 里写明「为什么不是词表」。
//
// issue #383 的失效形态是「新增一张判定词表，All*() 忘了配套 / 断言没人写」。
// 只断言现有九张表的集合相等，无法阻止第十张表悄悄出现；把声明面也数一遍，
// 新增表就必须做一次显式选择。
func TestEveryVocabularyMapHasEnumerationCase(t *testing.T) {
	declared := declaredMapStringBool(parsePackageAST(t))
	covered := map[string]bool{}
	for _, c := range vocabularyCases {
		covered[c.tableName] = true
	}
	for _, g := range groupedVocabularyCases {
		for _, n := range g.tableNames {
			covered[n] = true
		}
	}

	for name := range declared {
		if covered[name] {
			continue
		}
		reason, ok := nonVocabularyMaps[name]
		if !ok {
			t.Errorf("包内新增 map[string]bool %q 没有配套的枚举出口与一致性断言；"+
				"若它不是判定词表，请在 nonVocabularyMaps 里写明理由", name)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("nonVocabularyMaps[%q] 的理由为空——「不是词表」也需要可复核的依据", name)
		}
	}
	for name := range covered {
		if !declared[name] {
			t.Errorf("vocabularyCases 登记的词表 %q 在包内不存在（改名/删除后忘了同步清单）", name)
		}
	}
	for name := range nonVocabularyMaps {
		if !declared[name] {
			t.Errorf("nonVocabularyMaps 的登记 %q 已过期（包内没有这个 map）", name)
		}
	}
}

// TestEveryEnumerationExportIsCovered 包内每个 All*() 都必须在
// vocabularyCases 里被断言过——枚举出口是交给宿主的「口径唯一来源」，
// 一个没被验证的出口等于一份无人核对的清单。
func TestEveryEnumerationExportIsCovered(t *testing.T) {
	declared := declaredEnumerationFuncs(parsePackageAST(t))
	covered := map[string]bool{}
	for _, c := range vocabularyCases {
		full := runtime.FuncForPC(reflect.ValueOf(c.all).Pointer()).Name()
		name := full[strings.LastIndex(full, ".")+1:]
		covered[name] = true
	}
	for _, g := range groupedVocabularyCases {
		for _, fn := range []func() []string{g.buckets, g.allWords} {
			full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
			covered[full[strings.LastIndex(full, ".")+1:]] = true
		}
	}
	for name := range declared {
		if !covered[name] {
			t.Errorf("枚举出口 %s() 没有纳入一致性断言（枚举集合 == 私有词表）", name)
		}
	}
	for name := range covered {
		if !declared[name] {
			t.Errorf("vocabularyCases 登记的出口 %s() 在包内不存在", name)
		}
	}
}

package statemachine

// 状态词表单源与终态判据一致性的机器判据（issue #354 / #357）。
//
// 三条判据分开钉：
//  1. 12 个环节状态常量必须是 contract 同值常量的**别名**（AST 判据，不是
//     「值恰好相等」——两份字面量相等今天看不出来，改一处就漂）；
//  2. 两包共有的取值个数钉在具体数字（当前 6），新增会话状态必须同步本用例；
//  3. 12 个环节状态（外加会话独有的 timed_out）在两包终态判据下逐条一致。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"

	"github.com/yi-nology/goagent-kernel/contract"
)

// stepVocabulary 12 个环节状态（顺序按状态机生命周期排列）。
// wantTerminal 是**终态**的唯一口径：见 contract.IsTerminalStatus 的注释
// （failed 是终态；failed → rolled_back 是补偿迁移，不是自行前进）。
var stepVocabulary = []struct {
	name         string // 常量名（两包同名）
	status       StepStatus
	wantTerminal bool
}{
	{"StatusCreated", StatusCreated, false},
	{"StatusRunning", StatusRunning, false},
	{"StatusWaitingApproval", StatusWaitingApproval, false},
	{"StatusApproved", StatusApproved, false},
	{"StatusRejected", StatusRejected, true},
	{"StatusExpired", StatusExpired, true},
	{"StatusSucceeded", StatusSucceeded, true},
	{"StatusFailed", StatusFailed, true},
	{"StatusInterrupted", StatusInterrupted, true},
	{"StatusRolledBack", StatusRolledBack, true},
	{"StatusSkipped", StatusSkipped, true},
	{"StatusCancelled", StatusCancelled, true},
}

// TestStatusConstantsAreContractAliases 12 个状态常量必须是 contract 的别名，
// 不得是第二份字面量（issue #357）。
//
// 反向验证：把任一 `= contract.StatusX` 改回 `= "x"`，本用例立即转红——
// 这正是「改一处不会红」的缺口所在。
func TestStatusConstantsAreContractAliases(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "step.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 step.go: %v", err)
	}

	// 收集包级 const 声明里 name → 值表达式。
	values := map[string]ast.Expr{}
	ast.Inspect(file, func(n ast.Node) bool {
		gen, ok := n.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			return true
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					values[name.Name] = vs.Values[i]
				} else {
					values[name.Name] = vs.Values[len(vs.Values)-1]
				}
			}
		}
		return true
	})

	for _, tc := range stepVocabulary {
		expr, ok := values[tc.name]
		if !ok {
			t.Errorf("%s: 包级 const 里找不到该状态常量", tc.name)
			continue
		}
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok {
			t.Errorf("%s: 值不是别名表达式，而是第二份字面量（%T）——词表单源在 contract", tc.name, expr)
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "contract" || sel.Sel.Name != tc.name {
			t.Errorf("%s: 别名必须指向 contract.%s，实得别处", tc.name, tc.name)
		}
	}
}

// TestStatusVocabularyOverlapPinned 两包共有的取值个数与成员必须钉住
// （issue #357 验收 3）：新增会话状态时必须同时更新两侧词表与本用例的数字。
func TestStatusVocabularyOverlapPinned(t *testing.T) {
	// 会话状态词表（contract 的 7 个常量）。
	session := []string{
		contract.StatusCreated, contract.StatusRunning, contract.StatusWaitingApproval,
		contract.StatusSucceeded, contract.StatusFailed, contract.StatusCancelled,
		contract.StatusTimedOut,
	}
	step := make(map[string]bool, len(stepVocabulary))
	for _, tc := range stepVocabulary {
		step[string(tc.status)] = true
	}
	var overlap []string
	for _, s := range session {
		if step[s] {
			overlap = append(overlap, s)
		}
	}
	sort.Strings(overlap)

	want := []string{"cancelled", "created", "failed", "running", "succeeded", "waiting_approval"}
	if len(overlap) != 6 {
		t.Fatalf("两包共有取值应恰为 6 个（当前词表边界），实得 %d: %v", len(overlap), overlap)
	}
	for i := range want {
		if overlap[i] != want[i] {
			t.Fatalf("共有取值第 %d 个 = %q, want %q（重叠集合不得悄悄漂移）", i, overlap[i], want[i])
		}
	}
}

// TestOverlappingValuesEqualAcrossPackages 重叠取值的字符串逐条相等
// （issue #357 验收 2）。
//
// AST 别名判据已经更强地钉住「不是第二份字面量」；这里再做一次运行期
// 同值断言，宿主与测试失败信息都能直接看到两侧取值。
func TestOverlappingValuesEqualAcrossPackages(t *testing.T) {
	pairs := []struct {
		name string
		step string
		sess string
	}{
		{"StatusCreated", StatusCreated, contract.StatusCreated},
		{"StatusRunning", StatusRunning, contract.StatusRunning},
		{"StatusWaitingApproval", StatusWaitingApproval, contract.StatusWaitingApproval},
		{"StatusSucceeded", StatusSucceeded, contract.StatusSucceeded},
		{"StatusFailed", StatusFailed, contract.StatusFailed},
		{"StatusCancelled", StatusCancelled, contract.StatusCancelled},
	}
	for _, p := range pairs {
		if p.step != p.sess {
			t.Errorf("%s: statemachine=%q contract=%q（重叠取值必须恒等）", p.name, p.step, p.sess)
		}
	}
}

// TestTerminalPredicateConsistent 终态判据在两包逐条一致——12 个环节状态
// 全覆盖，外加会话独有的 timed_out（issue #354）。
//
// 反向验证（#354 验收 2）：把 StatusFailed 从 contract.IsTerminalStatus 的
// case 列表里删掉，本用例在 failed 行转红；把任一环节状态从别名改成词表外
// 取值，也会转红。
func TestTerminalPredicateConsistent(t *testing.T) {
	for _, tc := range stepVocabulary {
		t.Run(tc.name, func(t *testing.T) {
			// 规范值与写法变体（大小写/前后空白）两包必须同结论：statemachine
			// 的归一口径（#373）与 contract 的归一口径（#354）只能有一条。
			for _, variant := range stateVariants(string(tc.status)) {
				if got := IsTerminal(StepStatus(variant)); got != tc.wantTerminal {
					t.Errorf("statemachine.IsTerminal(%q) = %v, want %v", variant, got, tc.wantTerminal)
				}
				if got := contract.IsTerminalStatus(variant); got != tc.wantTerminal {
					t.Errorf("contract.IsTerminalStatus(%q) = %v, want %v（两包判据必须逐条一致）",
						variant, got, tc.wantTerminal)
				}
			}
		})
	}

	// timed_out 是**会话独有**取值，不在环节词表内——这是两根轴的边界，
	// 不是判据分叉：
	//   - 会话侧 contract.IsTerminalStatus 认它是终态；
	//   - 环节侧按「词表外不是终态」返回 false（#374：读不懂不能当成不可再变）。
	// 12 个环节状态才是两包必须逐条一致的状态集合。
	t.Run("StatusTimedOut", func(t *testing.T) {
		s := StepStatus(contract.StatusTimedOut)
		if got := IsTerminal(s); got {
			t.Error("statemachine.IsTerminal(timed_out) 应为 false（不在环节词表内）")
		}
		if got := contract.IsTerminalStatus(contract.StatusTimedOut); !got {
			t.Error("contract.IsTerminalStatus(timed_out) 应为 true（会话终态）")
		}
		if s.Valid() {
			t.Error("timed_out 不得混进 StepStatus 词表（环节轴没有这个状态）")
		}
	})

	// 词表外取值两包都不得判成终态（fail-closed 的反面：不臆断）。
	for _, unknown := range []string{"", "unknown", "degraded", "aborted"} {
		if IsTerminal(StepStatus(unknown)) {
			t.Errorf("statemachine.IsTerminal(%q) 应为 false（词表外）", unknown)
		}
		if contract.IsTerminalStatus(unknown) {
			t.Errorf("contract.IsTerminalStatus(%q) 应为 false（词表外）", unknown)
		}
	}
}

// TestIsTerminalHasProductionCallSite IsTerminal 是导出 API，必须有生产调用点
// （issue #354 验收 3）：它若只是「宿主可注入」的摆设，就是误导宿主的死 API。
//
// 判据是 CanTransition 的终态守卫真实调用了 IsTerminal——删掉该调用或把
// IsTerminal 删成未引用符号，本用例/编译都会红。
func TestIsTerminalHasProductionCallSite(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "step.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 step.go: %v", err)
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "CanTransition" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if ok && id.Name == "IsTerminal" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("CanTransition 必须调用 IsTerminal 做终态守卫——否则 IsTerminal 是零生产调用点的死 API")
	}
}

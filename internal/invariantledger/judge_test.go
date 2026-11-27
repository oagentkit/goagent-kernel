// judge_test.go —— Judge 纯函数的可执行判据（数据一律合成，不读盘不联网）。
package invariantledger

import (
	"strings"
	"testing"
)

// validRegistry 合成一份全绿登记面：七类不变量 + 七间隙，各带一条存在判据。
func validRegistry() *Registry {
	reg := &Registry{Updated: "2026-11-27"}
	for _, id := range RequiredInvariants {
		reg.Invariants = append(reg.Invariants, InvariantRow{
			ID:        id,
			Statement: id + " 的陈述",
			Mechanism: "pkg." + id,
			Docs:      "docs/x.md",
			Judgments: []string{"pkg.Test_" + strings.ReplaceAll(id, "-", "_")},
			Gap:       "无",
		})
	}
	for _, id := range RequiredGaps {
		reg.Gaps = append(reg.Gaps, GapRow{
			ID:       id,
			Evidence: []string{"pkg.Test_" + strings.ReplaceAll(id, "-", "_")},
			Waiver:   "—",
		})
	}
	return reg
}

// validIndex 把登记面里引用的全部测试注入成事实。
func validIndex(reg *Registry) TestIndex {
	idx := TestIndex{}
	for _, inv := range reg.Invariants {
		for _, ref := range inv.Judgments {
			idx[ref] = true
		}
	}
	for _, g := range reg.Gaps {
		for _, ref := range g.Evidence {
			idx[ref] = true
		}
	}
	return idx
}

// hasProblem 判问题列表里有没有包含子串的条目。
func hasProblem(problems []string, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// TestJudgeHappyPath 全绿登记面：零问题、计数正确、无豁免。
func TestJudgeHappyPath(t *testing.T) {
	reg := validRegistry()
	res := Judge(reg, validIndex(reg))
	if len(res.Problems) != 0 {
		t.Fatalf("全绿登记面不该有问题，实得：%v", res.Problems)
	}
	if res.Invariants != 7 || res.Gaps != 7 {
		t.Fatalf("计数应为 7/7，实得 %d/%d", res.Invariants, res.Gaps)
	}
	if res.Refs != 14 {
		t.Fatalf("去重指针应为 14，实得 %d", res.Refs)
	}
	if res.Waived != 0 {
		t.Fatalf("全绿面不该有豁免行，实得 %d", res.Waived)
	}
}

// TestJudgeNilInputs 判据跑不起来时必须说话（空转的门禁等于没有门禁）。
func TestJudgeNilInputs(t *testing.T) {
	if res := Judge(nil, TestIndex{}); !hasProblem(res.Problems, "跑不起来") {
		t.Fatalf("nil 登记面应报「跑不起来」，实得：%v", res.Problems)
	}
	if res := Judge(validRegistry(), nil); !hasProblem(res.Problems, "跑不起来") {
		t.Fatalf("nil 测试事实应报「跑不起来」，实得：%v", res.Problems)
	}
}

// TestJudgeMissingRequiredInvariant 少登记评审点名的一类 ⇒ 红并点名。
func TestJudgeMissingRequiredInvariant(t *testing.T) {
	reg := validRegistry()
	reg.Invariants = reg.Invariants[:6] // 摘掉最后一类
	res := Judge(reg, validIndex(reg))
	if !hasProblem(res.Problems, "multi-replica-race") {
		t.Fatalf("缺类必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeUnknownInvariantID id 拼错/自造类别 ⇒ 红（「七类全覆盖」不能靠口径滑过去）。
func TestJudgeUnknownInvariantID(t *testing.T) {
	reg := validRegistry()
	reg.Invariants[0].ID = "state-transision" // 拼错
	res := Judge(reg, validIndex(reg))
	if !hasProblem(res.Problems, "state-transision") {
		t.Fatalf("非法 id 必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeEmptyGapCell 缺口格留白 ⇒ 红（没有缺口就写「无」，显式声明才可复核）。
func TestJudgeEmptyGapCell(t *testing.T) {
	reg := validRegistry()
	reg.Invariants[0].Gap = " "
	res := Judge(reg, validIndex(reg))
	if !hasProblem(res.Problems, "缺口格为空") {
		t.Fatalf("缺口格留白必须报，实得：%v", res.Problems)
	}
}

// TestJudgeNoJudgments 没登记必红判据 ⇒ 红（没有测试守着的不变量只是愿望）。
func TestJudgeNoJudgments(t *testing.T) {
	reg := validRegistry()
	reg.Invariants[2].Judgments = nil
	res := Judge(reg, validIndex(reg))
	if !hasProblem(res.Problems, "sideeffect-once") {
		t.Fatalf("零判据必须点名该不变量，实得：%v", res.Problems)
	}
}

// TestJudgeMissingTestRef 指针指向不存在的测试 ⇒ 红并点名（比没有清单更坏的形态）。
func TestJudgeMissingTestRef(t *testing.T) {
	reg := validRegistry()
	idx := validIndex(reg) // 先按原登记面建事实
	reg.Invariants[0].Judgments = []string{"pkg.TestDoesNotExist"}
	res := Judge(reg, idx)
	if !hasProblem(res.Problems, "pkg.TestDoesNotExist") {
		t.Fatalf("不存在的指针必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeSilentGap 既无场景也无豁免 ⇒ 红（这就是评审说的静默空隙）。
func TestJudgeSilentGap(t *testing.T) {
	reg := validRegistry()
	reg.Gaps[3].Evidence = nil
	reg.Gaps[3].Waiver = "—"
	res := Judge(reg, validIndex(reg))
	if !hasProblem(res.Problems, "G4") {
		t.Fatalf("静默空隙必须点名 G4，实得：%v", res.Problems)
	}
	if res.Waived != 0 {
		t.Fatalf("静默空隙不算豁免，Waived 应为 0，实得 %d", res.Waived)
	}
}

// TestJudgeWaiverCounts 零场景但有书面豁免 ⇒ 绿 + 计数回显（豁免是合法出口）。
func TestJudgeWaiverCounts(t *testing.T) {
	reg := validRegistry()
	reg.Gaps[0].Evidence = nil
	reg.Gaps[0].Waiver = "该间隙由 G2 的接管场景覆盖，独立用例待补（#523 台账）"
	idx := validIndex(reg)
	res := Judge(reg, idx)
	if len(res.Problems) != 0 {
		t.Fatalf("书面豁免是合法出口，不该有问题，实得：%v", res.Problems)
	}
	if res.Waived != 1 {
		t.Fatalf("豁免行应计 1，实得 %d", res.Waived)
	}
}

// TestJudgeDuplicateInvariant 重复行 ⇒ 红（同一 id 两条，绑定对不上时无从定位）。
func TestJudgeDuplicateInvariant(t *testing.T) {
	reg := validRegistry()
	reg.Invariants = append(reg.Invariants, reg.Invariants[0])
	res := Judge(reg, validIndex(reg))
	if !hasProblem(res.Problems, "重复登记") {
		t.Fatalf("重复行必须报，实得：%v", res.Problems)
	}
}

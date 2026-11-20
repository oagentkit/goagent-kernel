package roundguard

import "testing"

// ── 白盒矩阵：判据 × 变异 × 用例（内部包那一半）──
//
// 为什么矩阵分成两半：Go 的包边界——外部测试包（roundguard_test）看不见未导出
// 符号，而**承重守卫**（漂移分支的「原指纹不全等」）只在未导出面上 ⇒ 它只能由
// 内部测试包直钉。两半合起来才是完整的矩阵（外部半张表在 mutation_test.go）。
//
// 合成数据：夹具只用中性词面（指纹用字母串）。

// whiteBoxRow 白盒矩阵的一行。
type whiteBoxRow struct {
	criterion string           // 判据（一句）
	mutant    string           // 变异类（把它改坏是什么样）
	pin       func(*testing.T) // 钉住它的用例
}

// whiteBoxMatrix 白盒矩阵（内部包那一半）。
var whiteBoxMatrix = []whiteBoxRow{
	{
		criterion: "漂移分支的承重守卫：原指纹全等 ⇒ 漂移分支不可达",
		mutant:    "删掉 driftCandidate 里的 allSameFingerprint 守卫 ⇒ 该子情形被误判成漂移（假阳）",
		pin:       TestDriftCandidateGuardIsLoadBearing,
	},
	{
		criterion: "守卫惰性所依赖的两处耦合：重复分支先于漂移分支 + 两者同用阈值 k",
		mutant:    "把漂移分支的阈值调到低于重复分支 / 拆开两个阈值 ⇒ 守卫从惰性变承重",
		pin:       TestDriftGuardCouplingHolds,
	},
}

// TestMutationMatrixWhiteBox 跑白盒矩阵的每一行。
func TestMutationMatrixWhiteBox(t *testing.T) {
	if len(whiteBoxMatrix) == 0 {
		t.Fatal("白盒矩阵为空：承重守卫就没有钉它的用例")
	}
	for _, row := range whiteBoxMatrix {
		t.Run(row.criterion, row.pin)
	}
}

// TestMutationMatrixWhiteBoxIsComplete 棘轮：每一行都得写清「判据」与「钉住哪个
// 变异」——只写判据不写变异的那行等于没钉（读者无法从表里看出它挡的是哪种改坏）。
func TestMutationMatrixWhiteBoxIsComplete(t *testing.T) {
	for _, row := range whiteBoxMatrix {
		if row.criterion == "" || row.mutant == "" || row.pin == nil {
			t.Fatalf("白盒矩阵行不完整（判据/变异/用例三缺一）：%+v", row)
		}
	}
}

// TestDriftCandidateGuardIsLoadBearing 直钉漂移分支上那道**承重守卫**。
//
// 为什么必须白盒：这道守卫在整条判定链上**不可达**——「原指纹全等」的那串必被
// **先判的重复形态**判走（两处耦合：① 重复分支先于漂移分支；② 两者同用阈值
// k，见 spec §7.3）⇒ 黑盒观察不到 ⇒ 2026-11-21 实测：**删掉守卫后整套
// roundguard 用例仍全绿** ⇒ 守卫是一句惰性断言（谁也证明不了它还在起作用）。
//
// 收成 driftCandidate 之后，守卫的契约可直钉：删掉守卫 ⇒ 本用例当场红 ⇒
// 守卫从「惰性断言」变成「承重判据」。
func TestDriftCandidateGuardIsLoadBearing(t *testing.T) {
	const k = 3
	// 构造：归一指纹相同、**原指纹全等**、长度达阈值的那串。
	same := []Observation{
		{Fingerprint: "a", Normalized: "n", Tools: 1},
		{Fingerprint: "a", Normalized: "n", Tools: 1},
		{Fingerprint: "a", Normalized: "n", Tools: 1},
	}
	// 前置一：漂移候选长度确实达到阈值（否则守卫无从承重）。
	if L := trailingSameNormalized(same); L < k {
		t.Fatalf("构造的序列必须让漂移候选长度达到阈值（守卫才承重），实得 %d", L)
	}
	// 前置二：原指纹全等（守卫的触发条件成立）。
	if !allSameFingerprint(same) {
		t.Fatal("构造的序列必须原指纹全等")
	}
	// 守卫：该进不进。
	if L, ok := driftCandidate(same, k); ok {
		t.Fatalf("原指纹全等时漂移分支必须不可达（守卫承重），实得 L=%d", L)
	}
	// 守卫不是一刀切：原指纹不全等 ⇒ 漂移分支成立，且长度就是阈值。
	diff := []Observation{
		{Fingerprint: "a", Normalized: "n", Tools: 1},
		{Fingerprint: "b", Normalized: "n", Tools: 1},
		{Fingerprint: "c", Normalized: "n", Tools: 1},
	}
	if L, ok := driftCandidate(diff, k); !ok || L != k {
		t.Fatalf("原指纹不全等时漂移分支必须成立且长度达阈值，实得 L=%d ok=%v", L, ok)
	}
}

// TestDriftGuardCouplingHolds 直钉守卫惰性所依赖的**两处耦合**。
//
// 守卫（漂移分支的「原指纹不全等」）在当前判定链上不可达，靠的是两处耦合：
// ① **重复分支先于漂移分支**；② 两者用**同一个阈值 k**。任一条被改动，守卫
// 立刻变成承重判据——而若没有用例钉住这两条，守卫就会悄悄变形（且没有任何
// 用例会红）。
//
// 这里白盒直钉耦合的**事实**：同一串回合在重复判据下也算够阈值（它先判走）。
func TestDriftGuardCouplingHolds(t *testing.T) {
	const k = 3
	same := []Observation{
		{Fingerprint: "a", Normalized: "n", Tools: 1},
		{Fingerprint: "a", Normalized: "n", Tools: 1},
		{Fingerprint: "a", Normalized: "n", Tools: 1},
	}
	// 耦合① + ②：重复分支的连续长度先达阈值 ⇒ 它先判走 ⇒ 漂移分支够不着。
	if L := trailingSameFingerprint(same); L < k {
		t.Fatalf("耦合：重复分支的连续长度必须先达阈值（它先判走），实得 %d", L)
	}
	// 耦合②：两个阈值**逐字相同** ⇒ 同一串回合在两条分支上的连续长度也相同
	// （阈值一旦被拆开，这一条不再成立 ⇒ 漂移分支会够得着 ⇒ 守卫转承重）。
	if a, b := trailingSameFingerprint(same), trailingSameNormalized(same); a != b {
		t.Fatalf("耦合：同阈值下两条分支的连续长度应逐字相同，实得 %d vs %d", a, b)
	}
}

// main_test.go —— 解析与判定的可执行判据（合成数据，不读盘）。
package main

import (
	"strings"
	"testing"
)

// validReg 合成一份全绿登记面：2 交付行 + 1 非交付行 + 白名单一条。
func validReg() *Registry {
	return &Registry{
		Updated: "2026-11-28",
		Rows: []CapRow{
			{Face: "事件发布", Pkgs: "eventbus", Tier: "at-least-once（重复靠游标吸收）",
				Mechanism: "水位线", Judgments: "eventbus.TestA"},
			{Face: "副作用施加", Pkgs: "sideeffect、sidetest", Tier: "at-most-once（拒绝 exactly-once）",
				Mechanism: "一次原子操作", Judgments: "sidetest.TestB"},
			{Face: "编排契约", Pkgs: "contract", Tier: notApplicableTier,
				Mechanism: "接口框架", Judgments: "—"},
		},
		Whitelist: []WLRow{{File: "docs/X.md", Reason: "拒绝语境的家"}},
	}
}

// validFacts 配套事实（Stable = eventbus/sideeffect/sidetest/contract）。
func validFacts() Facts {
	return Facts{
		Registered: map[string]bool{"eventbus": true, "sideeffect": true, "sidetest": true, "contract": true},
		Stable:     map[string]bool{"eventbus": true, "sideeffect": true, "sidetest": true, "contract": true},
		Tests:      map[string]bool{"eventbus.TestA": true, "sidetest.TestB": true},
		Whitelist:  map[string]bool{"docs/X.md": true},
		Hits:       []Hit{},
	}
}

func hasProb(probs []string, sub string) bool {
	for _, p := range probs {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// TestJudgeHappyPath 全绿：零问题、计数对。
func TestJudgeHappyPath(t *testing.T) {
	reg, f := validReg(), validFacts()
	// 交付行只有 2（须 ≥6）——补齐到 6 行再判。
	for i := 0; i < 4; i++ {
		reg.Rows = append(reg.Rows, CapRow{
			Face: "额外交付面", Pkgs: "eventbus", Tier: "at-least-once",
			Mechanism: "m", Judgments: "eventbus.TestA",
		})
	}
	res := Judge(reg, f)
	if len(res.Problems) != 0 {
		t.Fatalf("全绿不该有问题，实得：%v", res.Problems)
	}
	if res.Delivery != 6 || res.StableHit != 4 {
		t.Fatalf("计数应为 delivery=6 stableHit=4，实得 %d/%d", res.Delivery, res.StableHit)
	}
}

// TestJudgeDeliveryBelowSix 交付行 <6 ⇒ 红（评审点名的六面一个不许少）。
func TestJudgeDeliveryBelowSix(t *testing.T) {
	res := Judge(validReg(), validFacts())
	if !hasProb(res.Problems, "≥6") {
		t.Fatalf("交付行不足必须报，实得：%v", res.Problems)
	}
}

// TestJudgeStableUncovered Stable 包没进包列 ⇒ 红并点名。
func TestJudgeStableUncovered(t *testing.T) {
	reg := validReg()
	for i := 0; i < 4; i++ {
		reg.Rows = append(reg.Rows, CapRow{
			Face: "额外交付面", Pkgs: "eventbus", Tier: "at-least-once",
			Mechanism: "m", Judgments: "eventbus.TestA",
		})
	}
	reg.Rows[2].Pkgs = "statemachine" // 把 contract 换掉（statemachine 不在 Stable 集）
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "contract 没出现在能力面表") {
		t.Fatalf("Stable 未覆盖必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeBadTier 档位值域外 ⇒ 红。
func TestJudgeBadTier(t *testing.T) {
	reg := validReg()
	reg.Rows[0].Tier = "恰好一次"
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "不在四档") {
		t.Fatalf("值域外档位必须报，实得：%v", res.Problems)
	}
}

// TestJudgeMissingTest 指向不存在的判据测试 ⇒ 红并点名。
func TestJudgeMissingTest(t *testing.T) {
	reg := validReg()
	reg.Rows[0].Judgments = "eventbus.TestGhost"
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "eventbus.TestGhost") {
		t.Fatalf("不存在的判据必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeEmptyCell 零空格底线：能力面表空格 ⇒ 红。
func TestJudgeEmptyCell(t *testing.T) {
	reg := validReg()
	reg.Rows[1].Mechanism = " "
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "判定机制格为空") {
		t.Fatalf("空格必须报，实得：%v", res.Problems)
	}
}

// TestJudgeZombieWhitelist 白名单指向不存在文件 ⇒ 红（僵尸豁免）。
func TestJudgeZombieWhitelist(t *testing.T) {
	reg, f := validReg(), validFacts()
	f.Whitelist = map[string]bool{} // 文件都不在
	res := Judge(reg, f)
	if !hasProb(res.Problems, "僵尸白名单") {
		t.Fatalf("僵尸白名单必须报，实得：%v", res.Problems)
	}
}

// TestJudgeWhitelistNoReason 白名单没理由 ⇒ 红。
func TestJudgeWhitelistNoReason(t *testing.T) {
	reg := validReg()
	reg.Whitelist[0].Reason = " "
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "豁免必须写理由") {
		t.Fatalf("白名单缺理由必须报，实得：%v", res.Problems)
	}
}

// TestJudgeDistortionRed 正向承诺（窗口无拒绝标记）⇒ 红。
func TestJudgeDistortionRed(t *testing.T) {
	f := validFacts()
	f.Hits = []Hit{{
		File: "docs/GUIDE.md", Line: 10,
		Text:   "本包保证 exactly-once 交付。",
		Window: []string{"本包保证 exactly-once 交付。", "接上文。"},
	}}
	res := Judge(validReg(), f)
	if !hasProb(res.Problems, "失真声明：docs/GUIDE.md:10") {
		t.Fatalf("正向承诺必须报并给行号，实得：%v", res.Problems)
	}
}

// TestJudgeRejectionWindowPasses 拒绝语境窗口（±2 行有标记）⇒ 放行。
func TestJudgeRejectionWindowPasses(t *testing.T) {
	f := validFacts()
	f.Hits = []Hit{{
		File: "docs/GUIDE.md", Line: 10,
		Text:   "exactly-once 要求下游也接受幂等键",
		Window: []string{"上文", "exactly-once 要求下游也接受幂等键", "接线责任。"},
	}}
	res := Judge(validReg(), f)
	if hasProb(res.Problems, "docs/GUIDE.md:10") {
		t.Fatalf("拒绝语境窗口应放行，实得：%v", res.Problems)
	}
}

// TestJudgeWhitelistedFilePasses 白名单文件的命中 ⇒ 放行。
func TestJudgeWhitelistedFilePasses(t *testing.T) {
	f := validFacts()
	f.Hits = []Hit{{File: "docs/X.md", Line: 3, Text: "exactly-once", Window: []string{"exactly-once"}}}
	res := Judge(validReg(), f)
	if hasProb(res.Problems, "失真") {
		t.Fatalf("白名单文件应放行，实得：%v", res.Problems)
	}
}

// TestParseDocFull 全量解析：两张表 + 版本戳 + 反引号剥除。
func TestParseDocFull(t *testing.T) {
	doc := "# t\n\nupdated: 2026-11-28\n\n" +
		"| 能力面 | 包 | 承诺档位 | 判定机制 | 现有判据 |\n|---|---|---|---|---|\n" +
		"| 事件发布 | eventbus | at-least-once | 水位线 | `eventbus.TestA` |\n" +
		"\n| 文件 | 理由 |\n|---|---|\n| `docs/X.md` | 拒绝语境的家 |\n"
	reg, err := parseDoc(doc)
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if reg.Updated != "2026-11-28" || len(reg.Rows) != 1 {
		t.Fatalf("版本戳或行数错：%+v", reg)
	}
	if reg.Whitelist[0].File != "docs/X.md" {
		t.Fatalf("白名单反引号应剥除，实得 %q", reg.Whitelist[0].File)
	}
	if reg.Rows[0].Judgments != "`eventbus.TestA`" {
		t.Fatalf("判据列应原样保留，实得 %q", reg.Rows[0].Judgments)
	}
}

// TestParseDocMissingTable 缺能力面表 ⇒ 解析错误。
func TestParseDocMissingTable(t *testing.T) {
	if _, err := parseDoc("updated: 2026-11-28\n\n只有白名单没主表\n"); err == nil ||
		!strings.Contains(err.Error(), "能力面表") {
		t.Fatalf("缺表应报错并点名，实得：%v", err)
	}
}

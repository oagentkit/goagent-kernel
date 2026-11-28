// main_test.go —— 解析与判定的可执行判据（合成数据，不读盘）。
package main

import (
	"bytes"
	"os"
	"path/filepath"
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

// ── run() 级 fixture（覆盖 I/O 装配面：loadDoc/loadLifecycle/扫描/索引）──

// writeFixture 铺一座最小临时仓。
func writeFixture(t *testing.T, extraDoc string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# t\n\nupdated: 2026-11-28\n\n" +
		"| 能力面 | 包 | 承诺档位 | 判定机制 | 现有判据 |\n|---|---|---|---|---|\n" +
		"| 事件发布 | eventbus | at-least-once | 水位线 | `eventbus.TestA` |\n" +
		"| 副作用施加 | sideeffect | at-most-once | 原子操作 | `sideeffect.TestB` |\n" +
		"| 终态写 | commitgate | at-most-once | 闸 | `commitgate.TestC` |\n" +
		"| 快照 | checkpoint | idempotent-execution | CAS | `checkpoint.TestD` |\n" +
		"| 认领 | taskboard | at-most-once | 条件认领 | `taskboard.TestE` |\n" +
		"| 重试裁决 | retryplan | idempotent-execution | 三档路径 | `retryplan.TestF` |\n" +
		"| 编排契约 | contract | 不适用（非交付面） | 接口框架 | — |\n" +
		"\n| 文件 | 理由 |\n|---|---|\n| docs/DELIVERY.md | 本表 |\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "DELIVERY.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	lifecycle := "packages:\n" +
		"  - pkg: eventbus\n    level: Stable\n" +
		"  - pkg: sideeffect\n    level: Stable\n" +
		"  - pkg: commitgate\n    level: Stable\n" +
		"  - pkg: checkpoint\n    level: Stable\n" +
		"  - pkg: taskboard\n    level: Stable\n" +
		"  - pkg: retryplan\n    level: Stable\n" +
		"  - pkg: contract\n    level: Stable\n"
	if err := os.WriteFile(filepath.Join(root, "api-lifecycle.yaml"), []byte(lifecycle), 0o644); err != nil {
		t.Fatal(err)
	}
	readme := "# r\n" + extraDoc
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for pkg, name := range map[string]string{
		"eventbus": "TestA", "sideeffect": "TestB", "commitgate": "TestC",
		"checkpoint": "TestD", "taskboard": "TestE", "retryplan": "TestF",
	} {
		dir := filepath.Join(root, pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package " + pkg + "\n\nimport \"testing\"\n\nfunc " + name + "(t *testing.T) {}\n"
		if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runFixture 在 fixture 上跑一次，返回退出码与两路输出。
func runFixture(t *testing.T, root string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run([]string{"-root", root, "-doc", "docs/DELIVERY.md"}, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestRunGreen 全绿 fixture ⇒ 0。
func TestRunGreen(t *testing.T) {
	root := writeFixture(t, "# r\n")
	code, out, errOut := runFixture(t, root)
	if code != 0 {
		t.Fatalf("应通过，退出码 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(out, "交付语义对账通过") {
		t.Fatalf("成功行缺失，实得：%q", out)
	}
}

// TestRunDistortionRed README 正向承诺 ⇒ 1 并点名。
func TestRunDistortionRed(t *testing.T) {
	root := writeFixture(t, "# r\n\n本内核保证 exactly-once 交付。\n")
	code, _, errOut := runFixture(t, root)
	if code != 1 {
		t.Fatalf("正向承诺应判违反（1），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "失真声明：README.md") {
		t.Fatalf("必须点名 README 行号，实得：\n%s", errOut)
	}
}

// TestRunMissingDoc 登记面读不到 ⇒ 2（给不出结论）。
func TestRunMissingDoc(t *testing.T) {
	root := writeFixture(t, "# r\n")
	if err := os.Remove(filepath.Join(root, "docs", "DELIVERY.md")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runFixture(t, root)
	if code != 2 {
		t.Fatalf("应为给不出结论（2），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "读不到交付语义登记面") {
		t.Fatalf("错误要说清读不到什么，实得：\n%s", errOut)
	}
}

// TestRunZombieWhitelist 白名单指向不存在文件 ⇒ 1（僵尸豁免）。
func TestRunZombieWhitelist(t *testing.T) {
	root := writeFixture(t, "# r\n")
	docPath := filepath.Join(root, "docs", "DELIVERY.md")
	b, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), "| docs/DELIVERY.md | 本表 |", "| docs/GHOST.md | 本表 |", 1)
	if err := os.WriteFile(docPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runFixture(t, root)
	if code != 1 {
		t.Fatalf("僵尸白名单应判违反（1），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "docs/GHOST.md") {
		t.Fatalf("必须点名僵尸条目，实得：\n%s", errOut)
	}
}

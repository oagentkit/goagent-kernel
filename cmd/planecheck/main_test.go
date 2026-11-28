// main_test.go —— 解析与判定的可执行判据（合成数据 + 临时仓 fixture）。
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validReg 合成全绿登记面：4 包映射 + 2 条规则。
func validReg() *Registry {
	return &Registry{
		Updated: "2026-11-28",
		Mapping: []MapRow{
			{Pkg: "controlplane", Plane: "C", Duty: "控制面"},
			{Pkg: "worker", Plane: "E", Duty: "驱动循环"},
			{Pkg: "reconcile", Plane: "R", Duty: "孤儿回收"},
			{Pkg: "contract", Plane: "X", Duty: "唯一边界"},
		},
		Rules: []RuleRow{
			{From: "C", To: "E", Verdict: verdictAllow, Reason: "权威同账"},
			{From: "E", To: "C", Verdict: verdictForbid, Reason: "执行是被驱动的"},
		},
	}
}

func validFacts(edges ...Edge) Facts {
	return Facts{
		Registered: map[string]bool{
			"controlplane": true, "worker": true, "reconcile": true, "contract": true,
		},
		Edges: edges,
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

// TestJudgeHappyPath 允许方向的观测边 ⇒ 绿。
func TestJudgeHappyPath(t *testing.T) {
	res := Judge(validReg(), validFacts(Edge{From: "controlplane", To: "worker"}))
	if len(res.Problems) != 0 {
		t.Fatalf("全绿不该有问题，实得：%v", res.Problems)
	}
	if res.Cross != 1 {
		t.Fatalf("跨面边应 1，实得 %d", res.Cross)
	}
}

// TestJudgeUnruledDirection 未登记的跨面方向 ⇒ 红（未登记方向对门禁隐形）。
func TestJudgeUnruledDirection(t *testing.T) {
	res := Judge(validReg(), validFacts(Edge{From: "reconcile", To: "worker"})) // R→E 无规则
	if !hasProb(res.Problems, "R→E 未登记") {
		t.Fatalf("未登记方向必须报，实得：%v", res.Problems)
	}
}

// TestJudgeForbiddenHit 禁止方向被真实 import 命中 ⇒ 红并给理由。
func TestJudgeForbiddenHit(t *testing.T) {
	res := Judge(validReg(), validFacts(Edge{From: "worker", To: "controlplane"}))
	if !hasProb(res.Problems, "禁止方向被命中：worker → controlplane") {
		t.Fatalf("禁止方向必须报，实得：%v", res.Problems)
	}
	if !hasProb(res.Problems, "执行是被驱动的") {
		t.Fatalf("违反必须带规则理由，实得：%v", res.Problems)
	}
}

// TestJudgePreventiveForbidQuiet 预防性禁令（零观测）⇒ 绿（禁令登记即生效但不误报）。
func TestJudgePreventiveForbidQuiet(t *testing.T) {
	res := Judge(validReg(), validFacts()) // 无边
	if len(res.Problems) != 0 {
		t.Fatalf("零观测不该有问题，实得：%v", res.Problems)
	}
}

// TestJudgeSamePlaneAndCrossIgnored 同面边与 X 参与边不算跨面。
func TestJudgeSamePlaneAndCrossIgnored(t *testing.T) {
	res := Judge(validReg(), validFacts(
		Edge{From: "controlplane", To: "controlplane"}, // 自身不会出现，防御
		Edge{From: "contract", To: "worker"},           // X 参与
		Edge{From: "reconcile", To: "reconcile"},
	))
	if res.Cross != 0 {
		t.Fatalf("同面/X 边不该计跨面，实得 %d", res.Cross)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("不该有问题，实得：%v", res.Problems)
	}
}

// TestJudgeUncoveredPackage 漏映射一个包 ⇒ 红并点名。
func TestJudgeUncoveredPackage(t *testing.T) {
	// 给全集塞一个没映射的包
	f := validFacts()
	f.Registered["steer"] = true
	res := Judge(validReg(), f)
	if !hasProb(res.Problems, "steer 没有平面归属") {
		t.Fatalf("漏映射必须点名，实得：%v", res.Problems)
	}
}

// TestJudgeBadPlane 平面值域外 ⇒ 红。
func TestJudgeBadPlane(t *testing.T) {
	reg := validReg()
	reg.Mapping[0].Plane = "Q"
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "不在值域") {
		t.Fatalf("值域外平面必须报，实得：%v", res.Problems)
	}
}

// TestJudgeRuleOnX 规则含 X ⇒ 红（横切不参与跨面规则）。
func TestJudgeRuleOnX(t *testing.T) {
	reg := validReg()
	reg.Rules = append(reg.Rules, RuleRow{From: "X", To: "E", Verdict: verdictAllow, Reason: "r"})
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "含 X") {
		t.Fatalf("X 参与规则必须报，实得：%v", res.Problems)
	}
}

// TestJudgeNoReason 规则缺理由 ⇒ 红。
func TestJudgeNoReason(t *testing.T) {
	reg := validReg()
	reg.Rules[0].Reason = " "
	res := Judge(reg, validFacts())
	if !hasProb(res.Problems, "没有理由") {
		t.Fatalf("缺理由必须报，实得：%v", res.Problems)
	}
}

// TestParseDocFull 全量解析：两张表 + 反引号剥除。
func TestParseDocFull(t *testing.T) {
	doc := "# t\n\nupdated: 2026-11-28\n\n" +
		"| pkg | 平面 | 职责 |\n|---|---|---|\n| `worker` | E | 驱动循环 |\n" +
		"\n| from | to | 裁决 | 理由 |\n|---|---|---|---|\n| C | E | 允许 | 权威同账 |\n"
	reg, err := parseDoc(doc)
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if reg.Updated != "2026-11-28" || len(reg.Mapping) != 1 || len(reg.Rules) != 1 {
		t.Fatalf("解析结果错：%+v", reg)
	}
	if reg.Mapping[0].Pkg != "worker" {
		t.Fatalf("包名反引号应剥除，实得 %q", reg.Mapping[0].Pkg)
	}
}

// TestParseDocMissingRuleTable 缺规则表 ⇒ 解析错误。
func TestParseDocMissingRuleTable(t *testing.T) {
	doc := "updated: 2026-11-28\n\n| pkg | 平面 | 职责 |\n|---|---|---|\n| worker | E | x |\n"
	if _, err := parseDoc(doc); err == nil || !strings.Contains(err.Error(), "跨面规则表") {
		t.Fatalf("缺规则表应报错，实得：%v", err)
	}
}

// ── run() 级 fixture ──

// writeFixture 最小临时仓。
func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# t\n\nupdated: 2026-11-28\n\n" +
		"| pkg | 平面 | 职责 |\n|---|---|---|\n| `alpha` | C | 控 |\n| `beta` | E | 执 |\n" +
		"\n| from | to | 裁决 | 理由 |\n|---|---|---|---|\n| C | E | 允许 | 权威同账 |\n| E | C | 禁止 | 执行是被驱动的 |\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "PLANES.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	lifecycle := "packages:\n  - pkg: alpha\n  - pkg: beta\n"
	if err := os.WriteFile(filepath.Join(root, "api-lifecycle.yaml"), []byte(lifecycle), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		src := "package " + pkg + "\n"
		if pkg == "alpha" {
			src += "\nimport _ \"github.com/yi-nology/goagent-kernel/beta\"\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runFixture 在 fixture 上跑。
func runFixture(t *testing.T, root string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run([]string{"-root", root}, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestRunGreen 允许边的 fixture ⇒ 0。
func TestRunGreen(t *testing.T) {
	root := writeFixture(t)
	code, out, errOut := runFixture(t, root)
	if code != 0 {
		t.Fatalf("应通过，退出码 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(out, "三平面对账通过") {
		t.Fatalf("成功行缺失，实得：%q", out)
	}
}

// TestRunForbiddenEdge 禁止边的 fixture ⇒ 1。
func TestRunForbiddenEdge(t *testing.T) {
	root := writeFixture(t)
	// 把 beta 也指回 alpha（E→C 禁止）
	src := "package beta\n\nimport _ \"github.com/yi-nology/goagent-kernel/alpha\"\n"
	if err := os.WriteFile(filepath.Join(root, "beta", "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runFixture(t, root)
	if code != 1 {
		t.Fatalf("禁止边应判违反（1），实得 %d；stderr：\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "禁止方向被命中") {
		t.Fatalf("必须点名禁止边，实得：\n%s", errOut)
	}
}

// TestRunMissingDoc 登记面读不到 ⇒ 2。
func TestRunMissingDoc(t *testing.T) {
	root := writeFixture(t)
	if err := os.Remove(filepath.Join(root, "docs", "PLANES.md")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runFixture(t, root)
	if code != 2 {
		t.Fatalf("应为给不出结论（2），实得 %d", code)
	}
	if !strings.Contains(errOut, "读不到三平面登记面") {
		t.Fatalf("错误要说清读不到什么，实得：\n%s", errOut)
	}
}

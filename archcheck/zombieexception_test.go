package archcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// 僵尸例外对账（issue #447）：例外登记了，却命中不到任何违反。
//
// 这三条用例钉的是「例外与实际命中面必须对账」这件事本身——
// 旧实现只把例外列进报告（「已登记例外 N 条」），命中不到的例外照样绿，
// 于是已不需要的豁免能永久留在声明面上，而下一个看声明面的人以为那里还
// 欠着一笔已知债。本仓最贵的失效形态是门禁变绿，这条是它的又一个实例。

// TestRunFlagsZombieException 正向：例外指向一个**不存在的违规站点** ⇒
// 必须判红并点名那条例外（含 rule / path / line）。
//
// 走的是 Run 主路径，不是直接调 selfCheck——这样「主路径忘了传违规集」
// 这个坏实现会当场红（violated 为 nil 时判据静默跳过，正是要防的形态）。
func TestRunFlagsZombieException(t *testing.T) {
	root := fixtureRoot(t, policyHead+
		"exceptions:\n"+
		"  - rule: harness-ptc-unmanaged-escape\n"+
		"    path: x/a.go\n"+
		"    line: 99\n"+ // 真实违规在第 6 行 ⇒ 99 行命中不到
		"    violation: \"probe\"\n"+
		"    reason: \"宿主的工具另有一套管控\"\n"+
		"    until: \"2099-01-01\"\n"+
		"    removal: \"接上判定环即删\"\n", escapeCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	if !containsText(res.Violations, "僵尸例外") {
		t.Fatalf("命中不到任何违反的例外必须被判红，实得 %v", textsOf(res.Violations))
	}
	// 必须**点名到那一处**（rule + path + line）：只说「有一条例外失效」
	// 等于让人自己去 policy 里翻——判据要能直接指到那一行。
	if !strings.Contains(textsOf(res.Violations), "x/a.go:99") {
		t.Fatalf("僵尸例外必须点名它的 rule/path/line，实得 %v", textsOf(res.Violations))
	}
	if res.Blocking == 0 {
		t.Fatalf("僵尸例外是阻断级（失败形态必须是变红），实得 blocking=%d", res.Blocking)
	}
}

// TestExceptionOnRealViolationIsNotZombie 负对照：例外**命中真实违规**时
// 不得被判成僵尸。
//
// 少了这条，一个「一律报僵尸」的坏实现也能让上面那条正例通过——
// 判据写反了必须有东西能发现。
func TestExceptionOnRealViolationIsNotZombie(t *testing.T) {
	root := fixtureRoot(t, policyHead+
		"exceptions:\n"+
		"  - rule: harness-ptc-unmanaged-escape\n"+
		"    path: x/a.go\n"+
		"    line: 6\n"+ // 与 escapeCode 的真实违规站点逐字对齐
		"    violation: \"生产代码显式选择了未受管执行\"\n"+
		"    reason: \"宿主的工具另有一套管控\"\n"+
		"    until: \"2099-01-01\"\n"+
		"    removal: \"接上判定环即删\"\n", escapeCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	if containsText(res.Violations, "僵尸例外") {
		t.Fatalf("命中真实违规的例外不得被判成僵尸，实得 %v", textsOf(res.Violations))
	}
	if res.Excused != 1 || res.Blocking != 0 {
		t.Fatalf("该例外应当正常豁免（excused=1, blocking=0），实得 excused=%d blocking=%d，违规 %v",
			res.Excused, res.Blocking, textsOf(res.Violations))
	}
}

// TestExceptionOnUnknownRuleIsNotDoubleReported 排除：例外指向的规则本身
// 就不存在时，自校验已经点名它了——僵尸判据不得再叠一条同义的。
//
// 这条守的是报告的可读性：同一件事报两遍，下一个人会以为是两个问题。
func TestExceptionOnUnknownRuleIsNotDoubleReported(t *testing.T) {
	root := fixtureRoot(t, policyHead+
		"exceptions:\n"+
		"  - rule: harness-ghost-rule\n"+
		"    path: x/a.go\n"+
		"    line: 6\n"+
		"    reason: \"r\"\n"+
		"    until: \"2099-01-01\"\n"+
		"    removal: \"rm\"\n", escapeCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	if !containsText(res.Violations, "不存在的规则") {
		t.Fatalf("指向不存在规则的例外仍须被报出，实得 %v", textsOf(res.Violations))
	}
	if containsText(res.Violations, "僵尸例外") {
		t.Fatalf("规则本身不存在的例外不得再叠一条僵尸判据（已由自校验点名），实得 %v",
			textsOf(res.Violations))
	}
}

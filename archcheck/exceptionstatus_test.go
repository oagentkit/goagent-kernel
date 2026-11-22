package archcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// 例外的对账标记 `status`（issue #447 的后半句）：每条例外要么命中至少一条违反，
// 要么被**显式标记**为「已失效待删」（`status: stale`）。
//
// 僵尸判定本身由 zombieexception_test.go 钉住；这里钉的是**标记那半句**——
// 它决定一条「今天不命中但还要留着」的例外是被判红逼着删掉，还是被允许留档。
// 每条用例都写明它挡住的坏实现。

// cleanCode 是不含任何违规的最小生产代码。
//
// 为什么不用 escapeCode：那里面有第 6 行的违规，它本身就会阻断 ⇒ 「标记让门禁
// 变绿」这个结论会与「那条违规还在不在」混在一起，判据分不清是哪一件事导致的红。
const cleanCode = "package x\n" +
	"\n" +
	"func F() int {\n" +
	"\treturn 1\n" +
	"}\n"

// TestStaleMarkedZombiePassesWhileUnmarkedReds 同一夹具、只差一个标记字段：
// 不标 ⇒ 僵尸判红；标 `status: stale` ⇒ 绿。
//
// 这是本组的**对照主用例**：两次 Run 之间唯一的差别就是那一行 YAML，故结论的
// 差别只能由它解释。少了对照，一个「永远不报僵尸」的坏实现也能让「标了就绿」
// 那半条通过——判据必须能区分「标记生效了」与「判定被关掉了」。
func TestStaleMarkedZombiePassesWhileUnmarkedReds(t *testing.T) {
	const exBlock = "exceptions:\n" +
		"  - rule: harness-ptc-unmanaged-escape\n" +
		"    path: x/a.go\n" +
		"    line: 99\n" + // 真实仓里没有任何违规 ⇒ 命中 0
		"    violation: \"probe\"\n" +
		"    reason: \"站点已修复，留档到 until 到期\"\n" +
		"    until: \"2099-01-01\"\n" +
		"    removal: \"到期随那次清理一起删\"\n"

	run := func(t *testing.T, policy string) *Result {
		t.Helper()
		root := fixtureRoot(t, policy, cleanCode)
		pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
		if err != nil {
			t.Fatalf("policy 解析失败: %v", err)
		}
		res, err := Run(root, pol)
		if err != nil {
			t.Fatalf("门禁给不出结论: %v", err)
		}
		return res
	}

	// ① 不标 ⇒ 僵尸判红（这条由 zombieexception_test.go 钉住，这里做对照复跑）。
	bare := run(t, policyHead+exBlock)
	if !containsText(bare.Violations, "僵尸例外") {
		t.Fatalf("未标记的僵尸例外必须判红（对照组），实得 %v", textsOf(bare.Violations))
	}

	// ② 标了 ⇒ 放行，且整棵树必须是**干净的**（没有任何阻断）。
	marked := run(t, policyHead+exBlock+"    status: stale\n")
	if containsText(marked.Violations, "僵尸例外") {
		t.Fatalf("已标记失效待删的例外不得再判僵尸，实得 %v", textsOf(marked.Violations))
	}
	if marked.Blocking != 0 {
		t.Fatalf("标记后整棵树应当无阻断（坏实现：把标记当成了无条件放行的开关），实得 blocking=%d：%v",
			marked.Blocking, textsOf(marked.Violations))
	}
}

// TestStaleMarkedStillHittingIsContradictionRed 标了 stale 却**仍命中**违反 ⇒ 矛盾判红。
//
// 少了这条，stale 会变成「写一次永久免检」的免死金牌：把标记写上，之后无论站点
// 修没修都不会再被对账——#447 要防的静默换个马甲就回来了。且「仍命中」意味着
// 站点没修好，标记与事实相反。
func TestStaleMarkedStillHittingIsContradictionRed(t *testing.T) {
	root := fixtureRoot(t, policyHead+
		"exceptions:\n"+
		"  - rule: harness-ptc-unmanaged-escape\n"+
		"    path: x/a.go\n"+
		"    line: 6\n"+ // 与 escapeCode 的真实违规站点逐字对齐 ⇒ 命中 1
		"    violation: \"生产代码显式选择了未受管执行\"\n"+
		"    reason: \"宿主的工具另有一套管控\"\n"+
		"    until: \"2099-01-01\"\n"+
		"    removal: \"接上判定环即删\"\n"+
		"    status: stale\n", escapeCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	if !containsText(res.Violations, "却仍命中违反") {
		t.Fatalf("标了 stale 却仍命中违反必须判红（坏实现：把 stale 当成无条件豁免），实得 %v",
			textsOf(res.Violations))
	}
	if !strings.Contains(textsOf(res.Violations), "x/a.go:6") {
		t.Fatalf("矛盾判定必须点名那一处（rule/path/line），实得 %v", textsOf(res.Violations))
	}
	if res.Blocking == 0 {
		t.Fatalf("矛盾判定是阻断级（失败形态必须是变红），实得 blocking=%d", res.Blocking)
	}
}

// TestInvalidExceptionStatusValueIsSelfCheckRed 拼错的取值（`stal`）⇒ 自校验当天红。
//
// 取值域封闭的理由：拼错的标记若被当成「在用」，命中 >0 时它会**绿**——维护者
// 写下的意图与结论相反，而没有任何机器能发现（词表 kind 同款纪律）。
//
// 这条同时钉住「非法标记**不被**当成已确认失效」：同一条例外既报取值不合法、
// 又按未标记走僵尸判定 ⇒ 两处都指向同一个修法。
func TestInvalidExceptionStatusValueIsSelfCheckRed(t *testing.T) {
	root := fixtureRoot(t, policyHead+
		"exceptions:\n"+
		"  - rule: harness-ptc-unmanaged-escape\n"+
		"    path: x/a.go\n"+
		"    line: 99\n"+
		"    violation: \"probe\"\n"+
		"    reason: \"宿主的工具另有一套管控\"\n"+
		"    until: \"2099-01-01\"\n"+
		"    removal: \"接上判定环即删\"\n"+
		"    status: stal\n", cleanCode)
	pol, err := Load(filepath.Join(root, "architecture-policy.yaml"))
	if err != nil {
		t.Fatalf("policy 解析失败: %v", err)
	}
	res, err := Run(root, pol)
	if err != nil {
		t.Fatalf("门禁给不出结论: %v", err)
	}
	got := textsOf(res.Violations)
	if !strings.Contains(got, "status 取值") || !strings.Contains(got, "不合法") {
		t.Fatalf("拼错的 status 取值必须被自校验点名，实得 %v", got)
	}
	if res.Blocking == 0 {
		t.Fatalf("取值域违规是阻断级，实得 blocking=%d", res.Blocking)
	}
	// 非法标记不被当成「已确认失效」⇒ 僵尸判定照常判（否则拼错反而更宽松）。
	if !containsText(res.Violations, "僵尸例外") {
		t.Fatalf("非法标记不得被当成已确认失效（坏实现：只要非空就放过），实得 %v", got)
	}
}

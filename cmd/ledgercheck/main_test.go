// main_test.go —— 台账门禁的可执行判据。
//
// 每个用例都往**合成台账**里注入一处坏形态，断言「会红」：只测「好输入能过」
// 的用例，一个把状态比较写成字符串前缀比较的错实现照样全过。
//
// 夹具是编的（单号、标题、阶段全是合成的）：夹具是契约的示例，示例带真实味
// 就等于把契约钉死在某一次仓库状态上。
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticLedger 合成台账：一条已合入、一条仍开着、一条散文提及、一处 PR 引用。
var syntheticLedger = strings.Join([]string{
	"# 合成台账",
	"",
	"## 9. issue 台账",
	"",
	"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
	"|---|---|---|---|---|",
	"| #101 | 合成缺陷甲 | S1 | 合成判据甲 | ✅ 已合入 (#201) |",
	"| #102 | 合成缺陷乙 | S1 | 合成判据乙 |  |",
	"",
	"> 合成备注：#101 已合入。",
	"> 落文档 PR #301。",
}, "\n")

// syntheticIssueState 与 syntheticLedger 逐字对齐的 issue 状态快照：
// #101 已关（台账「已合入」）、#102 仍开（台账状态列空着）。
//
// 两份夹具必须一起给：issue #455 的判据面是「台账 ↔ 快照」，只给台账时门禁
// 会以「快照缺失 ⇒ 给不出结论」（退出码 2）失败——那不是判据红，是输入不全。
const syntheticIssueState = "updated: 2026-01-02\n" +
	"issues:\n" +
	"  101: closed\n" +
	"  102: open\n"

// writeLedger 造一份只含台账（与对齐快照）的临时仓库。
func writeLedger(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ledger.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	writeIssueState(t, root, syntheticIssueState)
	return root
}

// writeIssueState 覆盖快照（对账类用例靠它改事实面）。
func writeIssueState(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "docs", "issue-state.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCheck 跑门禁，返回退出码与输出。
func runCheck(t *testing.T, root string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out)
	return code, out.String()
}

// TestValidLedgerPasses 合法台账必须过（否则判据只是噪声）。
func TestValidLedgerPasses(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	code, out := runCheck(t, root)
	if code != 0 {
		t.Fatalf("合法台账应通过，退出码 %d；输出：\n%s", code, out)
	}
}

// TestConflictingStatusMustFail 同一单号两处状态矛盾 ⇒ 红，且要指出是哪两处。
func TestConflictingStatusMustFail(t *testing.T) {
	bad := strings.Replace(syntheticLedger,
		"> 合成备注：#101 已合入。",
		"> 合成备注：#101 仍开着。", 1)
	root := writeLedger(t, bad)
	code, out := runCheck(t, root)
	if code != 1 {
		t.Fatalf("状态矛盾必须红，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "#101") || !strings.Contains(out, "矛盾") {
		t.Fatalf("报错必须点名单号与矛盾，输出：\n%s", out)
	}
	// 两处位置都要指出来（只知道「矛盾」不知道「哪儿矛盾」等于没报）。
	if strings.Count(out, "ledger.md:") < 2 {
		t.Fatalf("必须指出两处位置，输出：\n%s", out)
	}
}

// TestMissingClosingPRMustFail 「已合入」没带关闭 PR 号 ⇒ 红。
func TestMissingClosingPRMustFail(t *testing.T) {
	bad := strings.Replace(syntheticLedger, "✅ 已合入 (#201)", "✅ 已合入", 1)
	root := writeLedger(t, bad)
	code, out := runCheck(t, root)
	if code != 1 {
		t.Fatalf("缺关闭 PR 号必须红，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "#101") || !strings.Contains(out, "关闭 PR") {
		t.Fatalf("报错必须点名单号与缺失的东西，输出：\n%s", out)
	}
}

// TestMalformedClosingPRMustFail 关闭 PR 号不形如 #N ⇒ 红。
func TestMalformedClosingPRMustFail(t *testing.T) {
	bad := strings.Replace(syntheticLedger, "✅ 已合入 (#201)", "✅ 已合入 (PR-201)", 1)
	root := writeLedger(t, bad)
	code, out := runCheck(t, root)
	if code != 1 {
		t.Fatalf("关闭 PR 号形态不对必须红，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "PR-201") {
		t.Fatalf("报错必须把不合法的原文打出来，输出：\n%s", out)
	}
}

// TestUnregisteredIssueMustFail 正文引用了没登记的单号 ⇒ 红。
func TestUnregisteredIssueMustFail(t *testing.T) {
	bad := syntheticLedger + "\n> 合成备注：#103 还没进台账。\n"
	root := writeLedger(t, bad)
	code, out := runCheck(t, root)
	if code != 1 {
		t.Fatalf("未登记单号必须红，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "#103") || !strings.Contains(out, "登记") {
		t.Fatalf("报错必须点名漂浮的单号，输出：\n%s", out)
	}
}

// TestTableShapeMustFail 表格列数不齐 ⇒ 红（不静默吞掉）。
func TestTableShapeMustFail(t *testing.T) {
	bad := strings.Replace(syntheticLedger,
		"| #102 | 合成缺陷乙 | S1 | 合成判据乙 |  |",
		"| #102 | 合成缺陷乙 | S1 |", 1)
	root := writeLedger(t, bad)
	code, out := runCheck(t, root)
	if code != 1 {
		t.Fatalf("表格列数不齐必须红，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "列数") {
		t.Fatalf("报错必须说清是列数问题，输出：\n%s", out)
	}
}

// TestMissingLedgerGivesNoConclusion 台账读不到 ⇒ 退出码 2（不是 0）。
//
// 「给不出结论」与「通过」必须分开：给不出结论却报 0，等于把判据失效
// 伪装成绿灯。
func TestMissingLedgerGivesNoConclusion(t *testing.T) {
	root := t.TempDir()
	code, _ := runCheck(t, root)
	if code != 2 {
		t.Fatalf("台账缺失必须给不出结论（退出码 2），实得 %d", code)
	}
}

// TestPRReferenceIsNotUnregisteredIssue PR 引用不是单号：不得判成漂浮。
func TestPRReferenceIsNotUnregisteredIssue(t *testing.T) {
	bad := syntheticLedger + "\n> 合并 PR 分布在 #302 / #303。\n"
	root := writeLedger(t, bad)
	code, out := runCheck(t, root)
	if code != 0 {
		t.Fatalf("PR 号不该被当成漂浮单号，退出码 %d；输出：\n%s", code, out)
	}
}

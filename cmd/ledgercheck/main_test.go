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

// writeLedger 造一份只含台账的临时仓库。
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
	return root
}

// syntheticIssueStates 与 syntheticLedger 一致的 issue 开关状态（判据 5 的数据源）。
//
// 必须注入而不是走真 gh：夹具单号 #101/#102 恰好落在**真实** issue 号段里，
// 真查会让「合成台账」与「真实仓库状态」对账——判据对了，用例却错了。
// 且真查每个用例一次 API 调用（实测 3.5s），注入后归零。
func syntheticIssueStates() (map[int]bool, error) {
	// #101 台账标已合入 ⇒ 真实状态应为已关（false = 不再开着）；
	// #102 台账状态列为空（派生成「仍开着」）⇒ 真实状态应为仍开着。
	return map[int]bool{101: false, 102: true}, nil
}

// runCheck 跑门禁，返回退出码与输出（数据源用与夹具一致的构造数据）。
func runCheck(t *testing.T, root string) (int, string) {
	t.Helper()
	return runWithFetcher(t, root, syntheticIssueStates)
}

// runWithFetcher 跑门禁（数据源可换：判据 5 是纯函数，喂什么状态表就判什么）。
func runWithFetcher(t *testing.T, root string, fetch func() (map[int]bool, error)) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out, fetch)
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

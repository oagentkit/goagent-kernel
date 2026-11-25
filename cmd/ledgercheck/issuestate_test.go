// issuestate_test.go —— 判据 5（台账 ↔ GitHub issue 状态双向对账）的可执行判据。
//
// 每条用例都构造一对「台账声明 vs issue 实际状态」，断言该红的红、该绿的绿：
// 只测「漏关会红」的用例，一个**一律报红**的错实现照样全过；只测「一致会绿」，
// 一个把比较写反的错实现也全过。两个方向都要有。
//
// 数据源一律注入（不联网）：判据是纯函数，这正是 issuestate.go 文件头写的
// 「判据面与数据源分离」的第一条理由。
package main

import (
	"bytes"
	"errors"
	"github.com/oagentkit/goagent-kernel/internal/ledgerstatus"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoWayLedger 两条登记行：一条标已合入、一条仍开着。
var twoWayLedger = strings.Join([]string{
	"# 双向对账合成台账",
	"",
	"## 9. issue 台账",
	"",
	"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
	"|---|---|---|---|---|",
	"| #901 | 合成已清偿单 | S1 | 合成判据甲 | ✅ 已合入 (#9901) |",
	"| #902 | 合成未清偿单 | S1 | 合成判据乙 | 🔴 仍开着 |",
}, "\n")

// writeTwoWay 造只含该台账的临时仓库。
func writeTwoWay(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "ledger.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// states 造一个数据源（value: true = 仍开着）。
func states(m map[int]bool) func() (map[int]bool, error) {
	return func() (map[int]bool, error) { return m, nil }
}

// TestIssueStateAgreementPasses 台账与 GitHub 一致 ⇒ 绿（否则「一律红」也能过）。
func TestIssueStateAgreementPasses(t *testing.T) {
	root := writeTwoWay(t, twoWayLedger)
	var out bytes.Buffer
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out,
		states(map[int]bool{901: false, 902: true}))
	if code != 0 {
		t.Fatalf("两边一致应通过，退出码 %d；输出：\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "issue 状态已对账") {
		t.Fatalf("通过信息里要点明对账已完成（否则看不出判据 5 跑没跑），输出：\n%s", out.String())
	}
}

// TestLeakedOpenIssueFails 漏关：台账说已合入，GitHub 上还开着 ⇒ 红并点名。
//
// 这是 issue #455 的**原形**（#427/#428/#430 就是这么关漏的）：台账与 PR 都对，
// 只有 issue 还开着，判据 1–4 全绿。
func TestLeakedOpenIssueFails(t *testing.T) {
	root := writeTwoWay(t, twoWayLedger)
	var out bytes.Buffer
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out,
		states(map[int]bool{901: true, 902: true})) // #901 台账说关了，实际还开着
	if code != 1 {
		t.Fatalf("漏关必须红，退出码 %d；输出：\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "#901") || !strings.Contains(out.String(), "漏关") {
		t.Fatalf("必须点名单号与「漏关」，输出：\n%s", out.String())
	}
	// 必须指到登记表那一行（只知道「有漏关」不知道在哪儿，等于没报）。
	if !strings.Contains(out.String(), "ledger.md:") {
		t.Fatalf("必须指到台账行，输出：\n%s", out.String())
	}
}

// TestClosedButStillRegisteredFails 反方向：台账说仍开着，GitHub 上已关 ⇒ 红。
//
// 少了这条，一个只判「漏关」的半截实现也能让上面那条通过——而这个方向的坑是
// 台账 §15 形态 (a)：接手的人按「单还开着」判断「还没人做」，整条工作树作废。
func TestClosedButStillRegisteredFails(t *testing.T) {
	root := writeTwoWay(t, twoWayLedger)
	var out bytes.Buffer
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out,
		states(map[int]bool{901: false, 902: false})) // #902 台账说开着，实际已关
	if code != 1 {
		t.Fatalf("关了没登记必须红，退出码 %d；输出：\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "#902") || !strings.Contains(out.String(), "已关闭") {
		t.Fatalf("必须点名单号与「已关闭」，输出：\n%s", out.String())
	}
}

// TestMissingIssueFails GitHub 上找不到台账登记的单 ⇒ 红（声明指向不存在的单）。
//
// 「查不到」不许当成「没问题」：单号打错、仓库不对、权限不足都会走到这条，
// 而静默放过正是这条判据要消灭的形态。
func TestMissingIssueFails(t *testing.T) {
	root := writeTwoWay(t, twoWayLedger)
	var out bytes.Buffer
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out,
		states(map[int]bool{})) // 一个都查不到
	if code != 1 {
		t.Fatalf("查不到必须红，退出码 %d；输出：\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "#901") || !strings.Contains(out.String(), "找不到") {
		t.Fatalf("必须点名查不到的单号，输出：\n%s", out.String())
	}
}

// TestFetchFailureExitsTwo 拉不到数据 ⇒ **exit 2（给不出结论）**，不是 0 也不是 1。
//
// 这条钉的是「查不到 ≠ 没有违反」：混成 0 就是门禁变绿，混成 1 会让人去改台账
// （而台账没错）。exit 2 是本仓三个退出码里唯一表达「我判不了」的那个。
func TestFetchFailureExitsTwo(t *testing.T) {
	root := writeTwoWay(t, twoWayLedger)
	var out bytes.Buffer
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out,
		func() (map[int]bool, error) { return nil, errors.New("合成：gh 不可用") })
	if code != 2 {
		t.Fatalf("拉不到数据必须 exit 2，实得 %d；输出：\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "gh 不可用") {
		t.Fatalf("要带上原始错误（否则不知道为什么判不了），输出：\n%s", out.String())
	}
}

// TestUnregisteredNumberNotJudged 正文引用但**没登记**的单号不判——那是判据 3 的活。
//
// 在这里重复报会让同一件事出现两条红，读的人以为是两个问题。
func TestUnregisteredNumberNotJudged(t *testing.T) {
	body := strings.Join([]string{
		"# 合成台账",
		"",
		"正文提到 #903，但它不在任何登记表里。",
		"",
		"## 9. issue 台账",
		"",
		"| 单号 | 标题 | 阶段 | 判据形态 | 状态 |",
		"|---|---|---|---|---|",
		"| #901 | 合成已清偿单 | S1 | 合成判据甲 | ✅ 已合入 (#9901) |",
	}, "\n")
	root := writeTwoWay(t, body)
	var out bytes.Buffer
	// #903 故意给「已关」——若判据 5 去判它，就会与判据 3 的「无主」重复报。
	code := runWith([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out,
		states(map[int]bool{901: false, 903: false}))
	if code != 1 { // 判据 3 仍会报 #903 无主
		t.Fatalf("#903 的无主仍应由判据 3 报，退出码 %d；输出：\n%s", code, out.String())
	}
	if strings.Contains(out.String(), "#903") && strings.Contains(out.String(), "GitHub") {
		t.Fatalf("未登记的单号不得由判据 5 判（那是判据 3 的活），输出：\n%s", out.String())
	}
}

// TestCheckIssueStatesIsPure 判据本体是纯函数：同一输入两次调用结果一致，
// 且不碰文件系统/网络（数据源分离的直接证据）。
func TestCheckIssueStatesIsPure(t *testing.T) {
	root := writeTwoWay(t, twoWayLedger)
	led, err := ledgerstatus.Parse(filepath.Join(root, "docs", "ledger.md"))
	if err != nil {
		t.Fatal(err)
	}
	open := map[int]bool{901: true, 902: true}
	a := checkIssueStates(led, open)
	b := checkIssueStates(led, open)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("纯函数两次调用应一致，实得 %v / %v", a, b)
	}
	if !strings.Contains(a[0], "#901") {
		t.Fatalf("应只报 #901（#902 两边一致），实得 %v", a)
	}
	// 换一张状态表 ⇒ 结果跟着换：它只吃入参，不偷看仓库或网络。
	c := checkIssueStates(led, map[int]bool{901: false, 902: true})
	if len(c) != 0 {
		t.Fatalf("两边一致时应零违规，实得 %v", c)
	}
}

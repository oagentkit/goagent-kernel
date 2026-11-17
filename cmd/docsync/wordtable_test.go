package main

// wordtable_test.go —— README 职责列里三张判定词表的判据（issue #384）。
//
// # 判据的两个方向都要能红
//
//	漏列：词表取值没出现在 README 行里；
//	多列：README 行里出现的同族词不在词表内。
//
// # 反向验证（必须存在，否则判据可能恒绿）
//
//	① 把 trajectory 行删掉 no_call_id ⇒ 必须报「漏列」；
//	② 把 duplicate_call 改成 duplicate_calls ⇒ 必须报「漏列」
//	   （trajectory 与 watchdog 两处都报）。
//
// 另外两条：抽不到词表片段（措辞变了）必须报红而不是静默跳过；正文一致性
// 走 run() 的端到端路径（照 run_test.go 的既有形状：t.Chdir 进临时目录）。

import (
	"strings"
	"testing"
)

// wordDesc 三张词表在 README 里的标准职责列片段。
func wordDesc() map[string]string {
	return map[string]string{
		"trajectory": "工具调用**配对状态**（paired / missing_result / duplicate_result / " +
			"unpaired_result / duplicate_call / no_call_id）",
		"watchdog": "缺口按 declared_not_executed / unpaired_result / duplicate_result / " +
			"duplicate_call 收口",
		"finalize": "`Route` Cause→终态**四分路由**（接管不落终态回 created / 用户取消 cancelled / " +
			"会话超时 timed_out **先于**停机分支 / 进程停机不落终态留给恢复循环，其余 failed）",
	}
}

// wordRows 三张词表的包表行（overrides 可覆写某个包的职责列）。
func wordRows(overrides map[string]string) []row {
	desc := wordDesc()
	for pkg, d := range overrides {
		desc[pkg] = d
	}
	rows := make([]row, 0, 3)
	for _, pkg := range []string{"trajectory", "watchdog", "finalize"} {
		rows = append(rows, row{pkg: pkg, desc: desc[pkg]})
	}
	return rows
}

// hasProblem 判断问题列表里有没有一条同时含 pkg 与关键词。
func hasProblem(problems []string, pkg, keyword string) bool {
	for _, p := range problems {
		if strings.Contains(p, pkg) && strings.Contains(p, keyword) {
			return true
		}
	}
	return false
}

// TestWordTablesAcceptConsistentReadme 正向：三张词表齐全时必须无问题。
//
// 没有这条，其余「该红」的用例可以靠「永远红」全部通过——即判据整个失灵也绿。
func TestWordTablesAcceptConsistentReadme(t *testing.T) {
	problems, checked := checkWordTables(wordRows(nil))
	if checked != 3 {
		t.Fatalf("应判 3 张词表，实得 %d", checked)
	}
	if len(problems) > 0 {
		t.Fatalf("一致时不该有问题: %v", problems)
	}
}

// TestWordTablesCatchMissingValue 漏列：trajectory 行删掉 no_call_id ⇒ 报红。
func TestWordTablesCatchMissingValue(t *testing.T) {
	desc := wordDesc()
	desc["trajectory"] = strings.Replace(desc["trajectory"], " / no_call_id", "", 1)
	problems, _ := checkWordTables(wordRows(map[string]string{"trajectory": desc["trajectory"]}))
	if !hasProblem(problems, "trajectory", "漏列") {
		t.Fatalf("trajectory 漏列必须报红: %v", problems)
	}
	if !strings.Contains(strings.Join(problems, "\n"), "no_call_id") {
		t.Fatalf("报错必须点名缺的那个取值: %v", problems)
	}
}

// TestWordTablesCatchRenamedValue 改名（duplicate_call → duplicate_calls）：
// 词表里的真值仍然缺失 ⇒ 报「漏列」，trajectory 与 watchdog 两处都报。
func TestWordTablesCatchRenamedValue(t *testing.T) {
	desc := wordDesc()
	desc["trajectory"] = strings.ReplaceAll(desc["trajectory"], "duplicate_call", "duplicate_calls")
	desc["watchdog"] = strings.ReplaceAll(desc["watchdog"], "duplicate_call", "duplicate_calls")
	problems, _ := checkWordTables(wordRows(map[string]string{
		"trajectory": desc["trajectory"],
		"watchdog":   desc["watchdog"],
	}))
	if !hasProblem(problems, "trajectory", "漏列") {
		t.Fatalf("trajectory 改名后必须报红: %v", problems)
	}
	if !hasProblem(problems, "watchdog", "漏列") {
		t.Fatalf("watchdog 改名后必须报红: %v", problems)
	}
}

// TestWordTablesCatchExtraValue 多列：README 里出现词表外的同族词 ⇒ 报红。
func TestWordTablesCatchExtraValue(t *testing.T) {
	desc := wordDesc()
	desc["trajectory"] = strings.Replace(desc["trajectory"], " / no_call_id", " / no_call_id / ghost_state", 1)
	problems, _ := checkWordTables(wordRows(map[string]string{"trajectory": desc["trajectory"]}))
	if !hasProblem(problems, "trajectory", "多列") {
		t.Fatalf("trajectory 多列必须报红: %v", problems)
	}
}

// TestWordTablesReportMissingAnchor 抽不到词表片段（措辞变了）必须报红，
// 不得静默跳过——「定位不到」与「词表一致」必须能区分。
func TestWordTablesReportMissingAnchor(t *testing.T) {
	problems, checked := checkWordTables(wordRows(map[string]string{
		"trajectory": "工具调用的配对状态（措辞改了，锚点没了）",
	}))
	if checked != 3 {
		t.Fatalf("应判 3 张词表，实得 %d", checked)
	}
	if !hasProblem(problems, "trajectory", "词表片段") {
		t.Fatalf("锚点失配必须报红（不得静默跳过）: %v", problems)
	}
}

// TestRunReportsWordTableDrift 端到端：README 的职责列漏了一个取值时，
// run() 必须报错（照 run_test.go 的既有形状：t.Chdir 进临时目录）。
func TestRunReportsWordTableDrift(t *testing.T) {
	desc := wordDesc()
	desc["trajectory"] = strings.Replace(desc["trajectory"], " / no_call_id", "", 1)
	readme := pkgTable(
		"| `trajectory` | 3 | 无 | "+desc["trajectory"]+" |",
		"| `watchdog` | 3 | 无 | "+desc["watchdog"]+" |",
		"| `finalize` | 3 | 无 | "+desc["finalize"]+" |",
	)
	fixtureRepo(t, readme, map[string]string{
		"trajectory/a.go": "package trajectory\n\nvar X = 1\n",
		"watchdog/a.go":   "package watchdog\n\nvar X = 1\n",
		"finalize/a.go":   "package finalize\n\nvar X = 1\n",
	})
	err := run()
	if err == nil {
		t.Fatal("README 漏列判定词表的取值时必须报错")
	}
	if !strings.Contains(err.Error(), "漏列") {
		t.Fatalf("应报「漏列」，实得 %v", err)
	}
}

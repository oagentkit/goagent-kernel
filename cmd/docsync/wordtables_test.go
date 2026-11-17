// wordtables_test.go —— README 职责列词表的机器判据（issue #384）。
//
// 每条用例都钉住「报的是不是这一条」：漏列/多列各自转红，且错误内容里
// 出现具体词值与包名。正向用例保证判据不是永远红。
//
// 另有一条端到端用例：checkWordTables 单测通过不代表 run() 接线了。

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// wordTableFixture 在临时目录里摆代码常量文件并切进去。
func wordTableFixture(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		mustWriteFile(t, filepath.Join(dir, name), body)
	}
	t.Chdir(dir)
}

const trajectoryConsts = `package trajectory

type Pairing string

const (
	PairingNone          Pairing = ""
	PairingPaired        Pairing = "paired"
	PairingMissingResult Pairing = "missing_result"
)
`

const watchdogConsts = `package watchdog

const (
	ReasonDeclaredNotExecuted = "declared_not_executed"
	ReasonUnpairedResult      = "unpaired_result"
	ReasonTurnEndUnattributed = "turn_end_unattributed"
)
`

const contractConsts = `package contract

const (
	StatusCreated   = "created"
	StatusRunning   = "running"
	StatusCancelled = "cancelled"
	StatusTimedOut  = "timed_out"
	StatusFailed    = "failed"
)
`

func wordTableFiles() map[string]string {
	return map[string]string{
		"trajectory/trajectory.go": trajectoryConsts,
		"watchdog/watchdog.go":     watchdogConsts,
		"contract/contract.go":     contractConsts,
	}
}

func wordTableRows(trajectory, watchdog, finalize string) []row {
	return []row{
		{pkg: "trajectory", desc: trajectory},
		{pkg: "watchdog", desc: watchdog},
		{pkg: "finalize", desc: finalize},
	}
}

// TestCheckWordTablesAcceptsConsistentReadme 正向：三张表都逐值列出时静默通过。
//
// 同时覆盖两条口径：PairingNone="" 不是取值（不要求列出）；watchdog 的
// ReasonTurn* 被 excludePrefix 排除（不要求出现在工具级缺口清单里）。
func TestCheckWordTablesAcceptsConsistentReadme(t *testing.T) {
	wordTableFixture(t, wordTableFiles())
	problems, err := checkWordTables(wordTableRows(
		"配对状态**（paired / missing_result）",
		"缺口按 declared_not_executed / unpaired_result 告警",
		"四分路由**（回 created / cancelled / timed_out / failed）",
	))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("一致时不该有问题: %v", problems)
	}
}

// TestCheckWordTablesCatchesMissingValue 漏列：代码有、README 没有。
func TestCheckWordTablesCatchesMissingValue(t *testing.T) {
	wordTableFixture(t, wordTableFiles())
	problems, err := checkWordTables(wordTableRows(
		"配对状态**（paired）", // 漏 missing_result
		"缺口按 declared_not_executed / unpaired_result 告警",
		"四分路由**（回 created / cancelled / timed_out / failed）",
	))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !containsProblem(problems, "trajectory", "漏列", "missing_result") {
		t.Fatalf("应报 trajectory 漏列 missing_result，实得 %v", problems)
	}
}

// TestCheckWordTablesCatchesExtraValue 多列：README 有、代码词表没有。
func TestCheckWordTablesCatchesExtraValue(t *testing.T) {
	wordTableFixture(t, wordTableFiles())
	problems, err := checkWordTables(wordTableRows(
		"配对状态**（paired / missing_result）",
		"缺口按 declared_not_executed / unpaired_result / duplicate_call 告警", // duplicate_call 不在 fixture
		"四分路由**（回 created / cancelled / timed_out / failed）",
	))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !containsProblem(problems, "watchdog", "多列", "duplicate_call") {
		t.Fatalf("应报 watchdog 多列 duplicate_call，实得 %v", problems)
	}
}

// TestCheckWordTablesDerivesValuesFromCode 词表取值必须从代码派生：
// 常量值改了，README 的旧词立刻变成「漏列 + 多列」。
//
// 这条防止门禁自己手抄一份字面量——那样代码与门禁一起漂，用户看不到。
func TestCheckWordTablesDerivesValuesFromCode(t *testing.T) {
	files := wordTableFiles()
	files["trajectory/trajectory.go"] = strings.Replace(
		trajectoryConsts, `PairingPaired        Pairing = "paired"`,
		`PairingPaired        Pairing = "paired_v2"`, 1)
	wordTableFixture(t, files)
	problems, err := checkWordTables(wordTableRows(
		"配对状态**（paired / missing_result）",
		"缺口按 declared_not_executed / unpaired_result 告警",
		"四分路由**（回 created / cancelled / timed_out / failed）",
	))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !containsProblem(problems, "trajectory", "漏列", "paired_v2") {
		t.Fatalf("代码常量改成 paired_v2 后应报漏列，实得 %v", problems)
	}
	if !containsProblem(problems, "trajectory", "多列", "paired") {
		t.Fatalf("README 残留旧词 paired 应报多列，实得 %v", problems)
	}
}

// TestCheckWordTablesReportsMissingExtractionPoint 抽取点失效必须响亮报错，
// 不能因为「没抽到区域」就当成 0 个词值静默通过。
func TestCheckWordTablesReportsMissingExtractionPoint(t *testing.T) {
	wordTableFixture(t, wordTableFiles())
	problems, err := checkWordTables(wordTableRows(
		"这段职责列里没有配对状态清单",
		"缺口按 declared_not_executed / unpaired_result 告警",
		"四分路由**（回 created / cancelled / timed_out / failed）",
	))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if !containsProblem(problems, "trajectory", "找不到词表区域") {
		t.Fatalf("抽取点失效应报错，实得 %v", problems)
	}
}

// TestRunCatchesWordTableDriftEndToEnd 端到端：run() 必须把职责列词表的漂移
// 带出来（前面的用例直接调 checkWordTables，接线本身没被证过）。
func TestRunCatchesWordTableDriftEndToEnd(t *testing.T) {
	files := wordTableFiles()
	// 行数按实际文件算，避免包表行数与 measure 的 25% 容差判据干扰本用例。
	row := func(pkg, body, desc string) string {
		return fmt.Sprintf("| `%s` | %d | 无 | %s |", pkg, countLines(body), desc)
	}
	// finalize 也需要一个生产 .go（measure 要求目录里有文件）。
	files["finalize/finalize.go"] = "package finalize\n"
	okReadme := pkgTable(
		row("contract", contractConsts, "会话状态契约"),
		row("finalize", "package finalize\n", "四分路由**（回 created / cancelled / timed_out / failed）"),
		row("trajectory", trajectoryConsts, "配对状态**（paired / missing_result）"),
		row("watchdog", watchdogConsts, "缺口按 declared_not_executed / unpaired_result 告警"),
	)
	fixtureRepo(t, okReadme, files)
	if err := run(); err != nil {
		t.Fatalf("一致的合成仓不该报错: %v", err)
	}

	// 换一份 README：trajectory 行漏掉 missing_result。
	driftReadme := pkgTable(
		row("contract", contractConsts, "会话状态契约"),
		row("finalize", "package finalize\n", "四分路由**（回 created / cancelled / timed_out / failed）"),
		row("trajectory", trajectoryConsts, "配对状态**（paired）"),
		row("watchdog", watchdogConsts, "缺口按 declared_not_executed / unpaired_result 告警"),
	)
	fixtureRepo(t, driftReadme, files)
	err := run()
	if err == nil || !strings.Contains(err.Error(), "漏列") || !strings.Contains(err.Error(), "missing_result") {
		t.Fatalf("run() 应带出 trajectory 漏列 missing_result，实得 %v", err)
	}
}

func containsProblem(problems []string, parts ...string) bool {
	for _, p := range problems {
		all := true
		for _, part := range parts {
			if !strings.Contains(p, part) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

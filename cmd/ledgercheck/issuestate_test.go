// issuestate_test.go —— issue #455 在门禁命令这一层的可执行判据。
//
// 库层（internal/ledgerstatus）钉的是对账逻辑本身；这里钉的是**命令的接线**：
// 快照从哪读、缺了给什么退出码、报错有没有点名单号。两层缺一层都会留洞——
// 判据对了但没接进门禁，等于门禁没判。
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCheckWithState 跑门禁，允许覆盖快照内容（nil = 不写快照文件）。
func runCheckWithState(t *testing.T, root, state string) (int, string) {
	t.Helper()
	if state != "" {
		writeIssueState(t, root, state)
	} else {
		if err := os.Remove(filepath.Join(root, "docs", "issue-state.yaml")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	code := run([]string{"-root", root, "-ledger", "docs/ledger.md"}, &out, &out)
	return code, out.String()
}

// TestMissingSnapshotGivesNoConclusion 快照缺失 ⇒ 退出码 2（给不出结论），不是跳过。
//
// 跳过是最贵的形态：判据静默不存在，门禁照样绿，而 #455 治的正是「声明面与
// 事实面不一致却没有任何判据会红」——把判据本身也弄成静默，等于原地打转。
func TestMissingSnapshotGivesNoConclusion(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	code, out := runCheckWithState(t, root, "")
	if code != 2 {
		t.Fatalf("快照缺失必须退出码 2（给不出结论），实得 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "issue-state.yaml") {
		t.Fatalf("报错要指到缺的那个文件，实得：\n%s", out)
	}
	if !strings.Contains(out, "make issue-state") {
		t.Fatalf("报错要给出生成方式（否则人不知道怎么补输入），实得：\n%s", out)
	}
}

// TestClosedLedgerOpenIssueMustFailAndNameNumber 方向一端到端：台账说已合入、
// 快照说 open ⇒ 红且点名单号。
//
// 这是 #455 的处方原文要求的那条用例。
func TestClosedLedgerOpenIssueMustFailAndNameNumber(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	state := "updated: 2026-01-02\nissues:\n  101: open\n  102: open\n  103: closed\n"
	code, out := runCheckWithState(t, root, state)
	if code != 1 {
		t.Fatalf("台账已合入而 issue 仍开必须退出码 1（有违反），实得 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "#101") {
		t.Fatalf("必须点名单号 #101，实得：\n%s", out)
	}
}

// TestOpenLedgerClosedIssueMustFailAndNameNumber 方向二端到端：issue 已关而
// 台账仍开着 ⇒ 红且点名单号。
func TestOpenLedgerClosedIssueMustFailAndNameNumber(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	state := "updated: 2026-01-02\nissues:\n  101: closed\n  102: closed\n  103: closed\n"
	code, out := runCheckWithState(t, root, state)
	if code != 1 {
		t.Fatalf("issue 已关而台账仍开着必须退出码 1，实得 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "#102") {
		t.Fatalf("必须点名单号 #102，实得：\n%s", out)
	}
}

// TestAlignedSnapshotPasses 两侧一致 ⇒ 绿（否则判据只是噪声）。
func TestAlignedSnapshotPasses(t *testing.T) {
	root := writeLedger(t, syntheticLedger) // 夹具自带对齐快照
	code, out := runCheck(t, root)
	if code != 0 {
		t.Fatalf("两侧一致应通过，退出码 %d；输出：\n%s", code, out)
	}
	// 快照的更新时间要回显：过期的数据判据照样能跑，但读输出的人必须看得见。
	if !strings.Contains(out, "issue 快照") {
		t.Fatalf("通过时应回显快照日期（数据新不新要可见），实得：\n%s", out)
	}
}

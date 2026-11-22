// issuestate_fallback_test.go —— 判据 5 的**两级数据源**（issue #455 的混合设计）。
//
// 实时 gh 为主、拉不到时退回仓内快照。这四条钉的是「级与级之间怎么切」：
// 切错了的两种形态分别是「离线跑不起来」（门禁等于没有）与「对着过期数据
// 却不吭声」（判据说谎）。判定本身（checkIssueStates）由 issuestate_test.go 钉，
// 这里只钉数据源切换——**判定必须仍走同一条路径**，第 2 条就是钉这个的。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errFetcher 一个必然失败的数据源（模拟没装 gh / 没网 / 没 token）。
func errFetcher() (map[int]bool, error) {
	return nil, errFetchFailed
}

// errFetchFailed 是 errFetcher 的固定错误（断言文案用）。
var errFetchFailed = &fetchErr{}

type fetchErr struct{}

func (e *fetchErr) Error() string { return "模拟：gh 不可用" }

// writeIssueSnapshot 落一份仓内快照（与台账同目录）。
func writeIssueSnapshot(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "docs", "issue-state.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// alignedIssueSnapshot 与 syntheticLedger 对齐的快照：#101 已关、#102 仍开。
const alignedIssueSnapshot = "updated: 2026-11-21\n" +
	"issues:\n" +
	"  101: closed\n" +
	"  102: open\n"

// TestFetcherFailureFallsBackToSnapshot 实时拉不到 + 快照在 ⇒ 退回快照并**大声回显**。
//
// 退回是「离线也能跑」的那一半；回显是「别拿过期数据冒充事实」的那一半——
// 少了回显，判据对着一份可能陈旧的数据照常绿，正是「判据说谎」。
func TestFetcherFailureFallsBackToSnapshot(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	writeIssueSnapshot(t, root, alignedIssueSnapshot)

	code, out := runWithFetcher(t, root, errFetcher)
	if code != 0 {
		t.Fatalf("有快照兜底时离线也该通过，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "离线快照") {
		t.Fatalf("退回快照必须在成功行里点明数据来源，实得：\n%s", out)
	}
	if !strings.Contains(out, "⚠") {
		t.Fatalf("退回快照必须给出警告（判据现在对着可能过期的数据在跑），实得：\n%s", out)
	}
}

// TestFallbackFeedsSameJudgmentPath 兜底喂进来的状态表，必须走**同一条**判定。
//
// 快照说 #101 仍开着、台账说它已合入 ⇒ 必须判红并点名单号。这条挡住的坏实现是
// 「兜底走另一套判定」——两套判定各判一套必然漂移，而漂移的方向没人看得见。
func TestFallbackFeedsSameJudgmentPath(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	writeIssueSnapshot(t, root, "updated: 2026-11-21\nissues:\n  101: open\n  102: open\n")

	code, out := runWithFetcher(t, root, errFetcher)
	if code != 1 {
		t.Fatalf("快照与台账不一致必须判红，退出码 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "#101") {
		t.Fatalf("必须点名单号 #101，实得：\n%s", out)
	}
	if !strings.Contains(out, "仓内快照") {
		t.Fatalf("判红时也要说明数据来自快照（否则读的人以为是实时事实），实得：\n%s", out)
	}
}

// TestFetcherFailureWithoutSnapshotGivesNoConclusion 两级都拿不到 ⇒ 退出码 2。
//
// 既不并进 1（有违反）也不当 0（通过）：「查不到」与「没有违反」是两件事，
// 混起来就是本仓最贵的失效形态。
func TestFetcherFailureWithoutSnapshotGivesNoConclusion(t *testing.T) {
	root := writeLedger(t, syntheticLedger) // 不写快照

	code, out := runWithFetcher(t, root, errFetcher)
	if code != 2 {
		t.Fatalf("两级都拿不到必须退出码 2（给不出结论），实得 %d；输出：\n%s", code, out)
	}
	if !strings.Contains(out, "issue-state.yaml") {
		t.Fatalf("报错要指到缺的那个快照文件，实得：\n%s", out)
	}
}

// TestLiveFetchProducesNoSnapshotTag 实时成功时**不**退回、不带快照标签——
// 兜底只在主路径失败时出现，否则「实时为主」名存实亡。
func TestLiveFetchProducesNoSnapshotTag(t *testing.T) {
	root := writeLedger(t, syntheticLedger)
	writeIssueSnapshot(t, root, alignedIssueSnapshot) // 快照在，但不该被用到

	code, out := runWithFetcher(t, root, syntheticIssueStates)
	if code != 0 {
		t.Fatalf("实时成功应通过，退出码 %d；输出：\n%s", code, out)
	}
	if strings.Contains(out, "离线快照") {
		t.Fatalf("实时成功不得退回快照（坏实现：无条件走兜底），实得：\n%s", out)
	}
}

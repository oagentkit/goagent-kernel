// issuestate_test.go —— issue 状态**快照解析**的判据用例（issue #455 的数据源层）。
//
// 判定（双向对账）的用例在 cmd/ledgercheck——那里才是唯一判定路径；这里只钉
// 「快照读不读得对、取值域封不封闭」。夹具是合成的：单号与状态全是编的，
// 不把任何真实仓库状态钉进契约。
package ledgerstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestFile 落一份夹具文件（路径由调用方给，免得每个用例都写一遍样板）。
func writeTestFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

// TestParseIssueSnapshotRejectsUnknownState 取值域封闭：未知取值解析即报错。
//
// 静默忽略一个未知取值会让那个单号从对账里消失，而消失的方向是门禁变绿。
func TestParseIssueSnapshotRejectsUnknownState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-state.yaml")
	if err := writeTestFile(path, "updated: 2026-01-02\nissues:\n  101: closedx\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseIssueSnapshot(path); err == nil {
		t.Fatal("未知取值必须报错（取值域封闭），实得 nil")
	} else if !strings.Contains(err.Error(), "不合法") {
		t.Fatalf("报错要点名取值不合法，实得 %v", err)
	}
}

// TestParseIssueSnapshotRejectsMalformedKey 键不是单号（如 "abc"）也要报错——
// 静默跳过会让那一条从对账里消失。
func TestParseIssueSnapshotRejectsMalformedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-state.yaml")
	if err := writeTestFile(path, "updated: 2026-01-02\nissues:\n  abc: open\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseIssueSnapshot(path); err == nil {
		t.Fatal("非数字键必须报错，实得 nil")
	}
}

// TestParseIssueSnapshotRejectsEmpty 空文件必须报错：空结果与「查不到」不可分，
// 静默当成「没有违反」正是本仓最贵的失效形态。
func TestParseIssueSnapshotRejectsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-state.yaml")
	if err := writeTestFile(path, "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseIssueSnapshot(path); err == nil {
		t.Fatal("空快照必须报错，实得 nil")
	}
}

// TestParseIssueSnapshotRoundTrip 合法快照必须读得出状态、更新时间与距今天数。
func TestParseIssueSnapshotRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issue-state.yaml")
	body := "updated: 2026-11-21\nissues:\n  101: closed\n  102: open\n"
	if err := writeTestFile(path, body); err != nil {
		t.Fatal(err)
	}
	snap, err := ParseIssueSnapshot(path)
	if err != nil {
		t.Fatalf("合法快照必须解析成功: %v", err)
	}
	if snap.Updated != "2026-11-21" {
		t.Fatalf("updated = %q, want 2026-11-21", snap.Updated)
	}
	if st, ok := snap.State(101); !ok || st != IssueClosed {
		t.Fatalf("#101 = (%q, %v), want (closed, true)", st, ok)
	}
	if st, ok := snap.State(102); !ok || st != IssueOpen {
		t.Fatalf("#102 = (%q, %v), want (open, true)", st, ok)
	}
	if _, ok := snap.State(999); ok {
		t.Fatal("快照里没有的号必须报 not found")
	}
}

// TestIssueSnapshotAgeDays 距今天数要算得对——它是回显里「数据多新」的唯一依据。
func TestIssueSnapshotAgeDays(t *testing.T) {
	snap := &IssueSnapshot{Updated: "2026-11-21"}
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	if got := snap.AgeDays(now); got != 3 {
		t.Fatalf("AgeDays = %d, want 3", got)
	}
	// 日期读不出来时返回 -1（报告据此写「日期未知」），不编一个 0 出来。
	bad := &IssueSnapshot{Updated: "not-a-date"}
	if got := bad.AgeDays(now); got != -1 {
		t.Fatalf("坏日期的 AgeDays = %d, want -1", got)
	}
	if got := (*IssueSnapshot)(nil).AgeDays(now); got != -1 {
		t.Fatalf("nil 快照的 AgeDays = %d, want -1", got)
	}
}

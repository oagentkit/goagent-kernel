// issuestate_test.go —— 台账 ↔ issue 状态快照双向对账的判据用例（issue #455）。
//
// 每条都往**合成**台账 + **合成**快照里注入一处不一致，断言「会红且点名单号」：
// 只测「一致时能过」的用例，一个把两个方向的比较写反的错实现照样全过。
package ledgerstatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFile 落一份夹具文件（路径由调用方给，免得每个用例都写一遍样板）。
func writeTestFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

// alignedSnapshot 与 syntheticLedger 逐字对齐的快照：#101/#103 已关、#102 仍开。
func alignedSnapshot() *IssueSnapshot {
	return &IssueSnapshot{
		Updated: "2026-01-02",
		Issues: map[string]IssueState{
			"101": IssueClosed,
			"102": IssueOpen,
			"103": IssueClosed,
		},
	}
}

// TestReconcilePassesWhenBothSidesAgree 对齐时必须零违反——否则判据只是噪声。
func TestReconcilePassesWhenBothSidesAgree(t *testing.T) {
	led := ParseText(syntheticLedger, "synthetic.md")
	if probs := Reconcile(led, alignedSnapshot()); len(probs) != 0 {
		t.Fatalf("两侧一致时不得报违反，实得 %v", probs)
	}
}

// TestReconcileFlagsClosedLedgerOpenIssue 方向一：台账说「已合入」、快照说 open。
//
// 这是 #455 的原始实录形态（#427/#428/#430 三条台账已翻面、GitHub 还开着）。
// 少了这条，一个「只判方向二」的半成品判据照样能让另一条通过。
func TestReconcileFlagsClosedLedgerOpenIssue(t *testing.T) {
	led := ParseText(syntheticLedger, "synthetic.md")
	snap := alignedSnapshot()
	snap.Issues["101"] = IssueOpen // 台账：已合入 (#201)；事实面：仍开着

	probs := Reconcile(led, snap)
	if len(probs) != 1 {
		t.Fatalf("方向一必须且只报一处，实得 %d 处：%v", len(probs), probs)
	}
	// 必须**点名单号**：只说「有一处不一致」等于让人自己去翻两张表。
	if !strings.Contains(probs[0], "#101") {
		t.Fatalf("必须点名单号 #101，实得 %q", probs[0])
	}
}

// TestReconcileFlagsOpenLedgerClosedIssue 方向二：台账说「仍开着」、快照说 closed。
//
// 少了这条，「issue 关早了 / 台账没跟着翻面」这个方向永远发现不了——而它会让
// 台账看起来比事实更欠，接手的人去重做一件已经做完的事。
func TestReconcileFlagsOpenLedgerClosedIssue(t *testing.T) {
	led := ParseText(syntheticLedger, "synthetic.md")
	snap := alignedSnapshot()
	snap.Issues["102"] = IssueClosed // 台账：状态列空着（仍开着）；事实面：已关

	probs := Reconcile(led, snap)
	if len(probs) != 1 {
		t.Fatalf("方向二必须且只报一处，实得 %d 处：%v", len(probs), probs)
	}
	if !strings.Contains(probs[0], "#102") {
		t.Fatalf("必须点名单号 #102，实得 %q", probs[0])
	}
}

// TestReconcileSkipsUnregisteredNumbers 只判**登记面**：正文里顺带提到的号不判。
//
// #104/#105/#106 只在散文里出现（不是状态声明）。拿它们去对快照会把「引用」
// 误判成「声明」⇒ 门禁红在一堆与状态无关的号上，真问题被淹没。
func TestReconcileSkipsUnregisteredNumbers(t *testing.T) {
	led := ParseText(syntheticLedger, "synthetic.md")
	// 快照里只有登记面的三个号；#106 一律查不到。
	snap := alignedSnapshot()
	if probs := Reconcile(led, snap); len(probs) != 0 {
		t.Fatalf("未登记的号不得参与对账，实得 %v", probs)
	}
	// 即使快照声称 #106 已关，也不该有反应（它不在登记面）。
	snap.Issues["106"] = IssueClosed
	if probs := Reconcile(led, snap); len(probs) != 0 {
		t.Fatalf("未登记的号即使状态相反也不判，实得 %v", probs)
	}
}

// TestReconcileFlagsRegisteredNumberMissingFromSnapshot 台账登记了、快照里没有
// ⇒ 声明面指着一个查无此号的对象。
//
// 这条挡住「把单号从快照里删掉就让对账消失」的绕过：消失的方向是门禁变绿。
func TestReconcileFlagsRegisteredNumberMissingFromSnapshot(t *testing.T) {
	led := ParseText(syntheticLedger, "synthetic.md")
	snap := &IssueSnapshot{
		Updated: "2026-01-02",
		Issues:  map[string]IssueState{"102": IssueOpen}, // #101 / #103 缺失
	}
	probs := Reconcile(led, snap)
	if len(probs) != 2 {
		t.Fatalf("登记了却查无此号的两个单号都必须报，实得 %d 处：%v", len(probs), probs)
	}
	for _, want := range []string{"#101", "#103"} {
		if !strings.Contains(strings.Join(probs, "\n"), want) {
			t.Fatalf("必须点名 %s，实得 %v", want, probs)
		}
	}
}

// TestReconcileNilInputsAreSafe 快照为 nil 时不判（缺输入由调用方报「给不出结论」，
// 不在这里编一个结论出来）。
func TestReconcileNilInputsAreSafe(t *testing.T) {
	led := ParseText(syntheticLedger, "synthetic.md")
	if probs := Reconcile(led, nil); len(probs) != 0 {
		t.Fatalf("快照为 nil 时不得报违反，实得 %v", probs)
	}
	if probs := Reconcile(nil, alignedSnapshot()); len(probs) != 0 {
		t.Fatalf("台账为 nil 时不得报违反，实得 %v", probs)
	}
}

// TestParseIssueSnapshotRejectsUnknownState 取值域封闭：未知取值解析即报错。
//
// 静默忽略一个未知取值会让那个单号从对账里消失，而消失的方向是门禁变绿。
func TestParseIssueSnapshotRejectsUnknownState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue-state.yaml")
	body := "updated: 2026-01-02\nissues:\n  101: closedx\n"
	if err := writeTestFile(path, body); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseIssueSnapshot(path); err == nil {
		t.Fatal("未知取值必须报错（取值域封闭），实得 nil")
	} else if !strings.Contains(err.Error(), "不合法") {
		t.Fatalf("报错要点名取值不合法，实得 %v", err)
	}
}

// TestParseIssueSnapshotRoundTrip 合法快照必须读得出状态与更新时间。
func TestParseIssueSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issue-state.yaml")
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
	if _, ok := snap.State(999); ok {
		t.Fatal("快照里没有的号必须报 not found")
	}
}

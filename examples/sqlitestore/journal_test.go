// journal_test.go —— 日志格式的契约判据（v2 批次 / v1 兼容 / 残尾）。
package sqlitestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yi-nology/goagent-kernel/taskboard"
)

// TestLegacyV1JournalStillReadable v1 格式（一行 = 一条任务记录）仍可读：
// 旧目录不必迁移，读进来的记录与新记录走同一条应用路径。
func TestLegacyV1JournalStillReadable(t *testing.T) {
	dir := t.TempDir()
	legacy := strings.Join([]string{
		`{"v":1,"task":{"id":"t1","title":"旧任务","deps":[],"state":"open"},"fence":0}`,
		`{"v":1,"task":{"id":"t1","title":"旧任务","deps":[],"state":"claimed","owner":"w-1","lease":{"token":"tok-legacy","fence":3}},"fence":3}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, journalName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tasks, err := s.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("v1 日志应读出 1 条任务，实得 %d", len(tasks))
	}
	if tasks[0].ID != "t1" || tasks[0].State != taskboard.StateClaimed {
		t.Fatalf("v1 记录读出来不对：%+v", tasks[0])
	}
	if tasks[0].Lease.Token != "tok-legacy" {
		t.Fatalf("v1 记录的租约必须保留：%+v", tasks[0].Lease)
	}

	// 旧目录上继续写 ⇒ 落成 v2 批次（旧记录仍在，重放后状态不丢）。
	if err := s.Complete(t.Context(), "t1", "tok-legacy"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, journalName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"records"`) {
		t.Fatalf("新写入必须是 v2 批次格式：%s", string(data))
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	tasks, err = s2.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != taskboard.StateDone {
		t.Fatalf("v1 + v2 混写的日志重放后状态不对：%+v", tasks)
	}
}

// TestJournalUnknownVersionFailsLoudly 未知格式版本响亮失败，不静默跳过。
func TestJournalUnknownVersionFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	bad := `{"v":99,"records":[]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, journalName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("未知格式版本必须报错（拒绝静默跳过）")
	}
}

// TestJournalEventIDGapFailsLoudly 日志里事件游标出现空洞 ⇒ 响亮失败。
//
// 「无空洞推进」不只是读侧的约定，它在**日志层**也是可判的：事件 ID 必须
// 连续。日志里出现跳号（有人绕过写入协议落了行，或日志被外部改写）⇒
// 拒绝加载。静默跳过会让尾随读侧永久跳过那个 ID（丢帧且无告警）。
func TestJournalEventIDGapFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	gapped := `{"v":2,"records":[{"k":"event","event":{"id":2,"event":{"id":2,"type":"e","level":"info"}}}]}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, journalName), []byte(gapped), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("事件游标出现空洞必须报错（不得静默跳过）")
	}
}

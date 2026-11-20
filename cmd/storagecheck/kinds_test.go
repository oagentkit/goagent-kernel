// kinds_test.go —— 存储 kind 封闭值域的用例。
//
// 夹具（demoMatrix / demoSelection）见 check_test.go（同包共享）。
package main

import (
	"strings"
	"testing"
)

// TestCheckRejectsUnknownStorageKind kind 写了词表外的值 ⇒ 红。
//
// 反向验证：封闭值域的牙在「拼错也红」。只测「合法值能过」的用例，一个把
// 值域判据写成空实现的错实现照样全过。
func TestCheckRejectsUnknownStorageKind(t *testing.T) {
	s := strings.Replace(demoSelection, "    kind: memory\n", "    kind: postgres-x\n", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "只允许")
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, "postgres-x") {
		t.Fatalf("违反必须点名写的是什么，实得:\n%s", joined)
	}
}

// TestCheckRejectsMissingStorageKind 没声明 kind ⇒ 红。
//
// 「没写」不是「什么都行」——它是最容易发生的漏登记。
func TestCheckRejectsMissingStorageKind(t *testing.T) {
	s := strings.Replace(demoSelection, "    kind: memory\n", "", 1)
	root := writeRoot(t, demoMatrix, s)
	probs, _, err := check(root)
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	requireViolation(t, probs, "没有声明 kind")
}

// TestCheckAcceptsEveryKnownStorageKind 词表里的每个取值都能过。
//
// 反向验证的另一半：值域不能收得过窄（收窄会把合法声明面判红 ⇒ 门禁被
// 绕过 ⇒ 没人再信它）。
func TestCheckAcceptsEveryKnownStorageKind(t *testing.T) {
	for _, kind := range storageKinds {
		s := strings.Replace(demoSelection, "    kind: memory\n", "    kind: "+kind+"\n", 1)
		root := writeRoot(t, demoMatrix, s)
		probs, _, err := check(root)
		if err != nil {
			t.Fatalf("判定出错: %v", err)
		}
		for _, p := range probs {
			if strings.Contains(p, "kind") {
				t.Fatalf("合法取值 %q 不该被判红: %s", kind, p)
			}
		}
	}
}

// TestKnownKindClosed 词表本身是封闭的：表外的取值一律不认。
func TestKnownKindClosed(t *testing.T) {
	for _, bad := range []string{"", "file", "postgres", "memory ", "SQLITE"} {
		if knownKind(bad) {
			t.Fatalf("取值 %q 不该被认成合法 kind", bad)
		}
	}
	for _, k := range storageKinds {
		if !knownKind(k) {
			t.Fatalf("词表里的 %q 必须被认成合法 kind", k)
		}
	}
}
